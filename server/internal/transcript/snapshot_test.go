package transcript

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteSnapshotAsyncAndReadPaginated(t *testing.T) {
	dataDir := t.TempDir()
	raw := []byte(strings.Repeat("line-of-terminal-output\r\n", 500))

	type result struct {
		path  string
		bytes int64
		err   error
	}
	done := make(chan result, 1)

	WriteSnapshotAsync(context.Background(), dataDir, "sess-123", raw, func(path string, n int64, err error) {
		done <- result{path: path, bytes: n, err: err}
	})

	var res result
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for WriteSnapshotAsync")
	}
	if res.err != nil {
		t.Fatalf("WriteSnapshotAsync error = %v", res.err)
	}

	wantPath := filepath.Join(dataDir, "sessions", "sess-123.gz")
	if res.path != wantPath {
		t.Fatalf("path = %q, want %q", res.path, wantPath)
	}
	if res.bytes <= 0 {
		t.Fatalf("compressed bytes = %d, want > 0", res.bytes)
	}

	// Verify no leftover temp files in sessions directory.
	entries, err := os.ReadDir(filepath.Join(dataDir, "sessions"))
	if err != nil {
		t.Fatalf("ReadDir error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "sess-123.gz" {
		t.Fatalf("unexpected files in sessions dir: %+v", entries)
	}

	// Verify gzip file decompresses to exact raw bytes.
	f, err := os.Open(wantPath)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", wantPath, err)
	}
	gr, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		t.Fatalf("gzip.NewReader error = %v", err)
	}
	decompressed, err := io.ReadAll(gr)
	_ = gr.Close()
	_ = f.Close()
	if err != nil {
		t.Fatalf("ReadAll gzip error = %v", err)
	}
	if !bytes.Equal(decompressed, raw) {
		t.Fatalf("decompressed len = %d, want %d", len(decompressed), len(raw))
	}

	// Paginate across multiple offsets and verify exact reconstruction + EOF.
	var reconstructed bytes.Buffer
	var offset int64
	const pageLimit = 1024
	for {
		chunk, nextOffset, totalBytes, eof, err := ReadPaginated(wantPath, offset, pageLimit)
		if err != nil {
			t.Fatalf("ReadPaginated(offset=%d) error = %v", offset, err)
		}
		if totalBytes != int64(len(raw)) {
			t.Fatalf("totalBytes = %d, want %d", totalBytes, len(raw))
		}
		reconstructed.Write(chunk)
		offset = nextOffset
		if eof {
			if nextOffset != totalBytes {
				t.Fatalf("at EOF nextOffset = %d, want %d", nextOffset, totalBytes)
			}
			break
		}
		if len(chunk) != pageLimit {
			t.Fatalf("non-EOF chunk len = %d, want %d", len(chunk), pageLimit)
		}
	}
	if !bytes.Equal(reconstructed.Bytes(), raw) {
		t.Fatalf("reconstructed transcript mismatch")
	}

	// Reading past EOF returns empty slice and eof=true.
	chunk, nextOffset, totalBytes, eof, err := ReadPaginated(wantPath, int64(len(raw))+100, 512)
	if err != nil {
		t.Fatalf("ReadPaginated past EOF error = %v", err)
	}
	if len(chunk) != 0 || !eof || nextOffset != int64(len(raw)) || totalBytes != int64(len(raw)) {
		t.Fatalf("past EOF = (len=%d, next=%d, total=%d, eof=%v)", len(chunk), nextOffset, totalBytes, eof)
	}
}

func TestReadPaginatedEmptyAndBytesHelper(t *testing.T) {
	dataDir := t.TempDir()
	done := make(chan error, 1)
	var snapPath string
	WriteSnapshotAsync(context.Background(), dataDir, "empty-sess", nil, func(path string, _ int64, err error) {
		snapPath = path
		done <- err
	})
	if err := <-done; err != nil {
		t.Fatalf("WriteSnapshotAsync(empty) error = %v", err)
	}

	chunk, next, total, eof, err := ReadPaginated(snapPath, 0, 100)
	if err != nil {
		t.Fatalf("ReadPaginated(empty) error = %v", err)
	}
	if len(chunk) != 0 || next != 0 || total != 0 || !eof {
		t.Fatalf("empty snapshot = (len=%d, next=%d, total=%d, eof=%v)", len(chunk), next, total, eof)
	}

	// Test in-memory helper parity.
	raw := []byte("0123456789")
	c1, n1, t1, e1, err := ReadPaginatedBytes(raw, 0, 4)
	if err != nil || string(c1) != "0123" || n1 != 4 || t1 != 10 || e1 {
		t.Fatalf("page 1 = (%q, %d, %d, %v, %v)", c1, n1, t1, e1, err)
	}
	c2, n2, t2, e2, err := ReadPaginatedBytes(raw, 8, 4)
	if err != nil || string(c2) != "89" || n2 != 10 || t2 != 10 || !e2 {
		t.Fatalf("page 2 = (%q, %d, %d, %v, %v)", c2, n2, t2, e2, err)
	}
}
