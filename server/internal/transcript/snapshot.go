package transcript

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPageLimit is the default page size in decompressed bytes for ReadPaginated.
const DefaultPageLimit = 65536

// SnapshotPath returns the canonical gzip transcript path under dataDir for sessionID.
func SnapshotPath(dataDir, sessionID string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", errors.New("transcript: dataDir is required")
	}
	id := strings.TrimSpace(sessionID)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") || filepath.Base(id) != id {
		return "", errors.New("transcript: invalid sessionID")
	}
	return filepath.Join(dataDir, "sessions", id+".gz"), nil
}

// WriteSnapshotAsync compresses raw in a dedicated goroutine and atomically writes it
// to <dataDir>/sessions/<sessionID>.gz. It invokes onDone (when non-nil) with the
// final file path and compressed byte size on disk.
func WriteSnapshotAsync(ctx context.Context, dataDir, sessionID string, raw []byte, onDone func(path string, bytes int64, err error)) {
	if ctx == nil {
		ctx = context.Background()
	}
	rawCopy := append([]byte(nil), raw...)

	go func() {
		path, size, err := writeSnapshot(ctx, dataDir, sessionID, rawCopy)
		if onDone != nil {
			onDone(path, size, err)
		}
	}()
}

func writeSnapshot(ctx context.Context, dataDir, sessionID string, raw []byte) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	targetPath, err := SnapshotPath(dataDir, sessionID)
	if err != nil {
		return "", 0, err
	}

	sessionsDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("transcript: create sessions dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(sessionsDir, sessionID+".*.tmp")
	if err != nil {
		return "", 0, fmt.Errorf("transcript: create temp snapshot: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	gw := gzip.NewWriter(tmpFile)
	if _, err := gw.Write(raw); err != nil {
		_ = gw.Close()
		_ = tmpFile.Close()
		return "", 0, fmt.Errorf("transcript: write gzip payload: %w", err)
	}
	if err := gw.Close(); err != nil {
		_ = tmpFile.Close()
		return "", 0, fmt.Errorf("transcript: close gzip writer: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", 0, fmt.Errorf("transcript: close temp file: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return "", 0, fmt.Errorf("transcript: rename snapshot: %w", err)
	}
	cleanupTmp = false

	info, err := os.Stat(targetPath)
	if err != nil {
		return targetPath, 0, fmt.Errorf("transcript: stat snapshot: %w", err)
	}
	return targetPath, info.Size(), nil
}

// ReadAll decompresses and returns the full transcript payload at snapshotPath.
func ReadAll(snapshotPath string) ([]byte, error) {
	f, err := os.Open(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("transcript: open snapshot: %w", err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("transcript: open gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()

	raw, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("transcript: read snapshot: %w", err)
	}
	return raw, nil
}

// ReadPaginated reads up to limit decompressed bytes from snapshotPath starting at offset.
func ReadPaginated(snapshotPath string, offset int64, limit int) (chunk []byte, nextOffset int64, totalBytes int64, eof bool, err error) {
	if offset < 0 {
		return nil, 0, 0, false, errors.New("transcript: offset must be non-negative")
	}
	if limit <= 0 {
		limit = DefaultPageLimit
	}

	f, err := os.Open(snapshotPath)
	if err != nil {
		return nil, 0, 0, false, fmt.Errorf("transcript: open snapshot: %w", err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, 0, 0, false, fmt.Errorf("transcript: open gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()

	if offset > 0 {
		discarded, err := io.CopyN(io.Discard, gr, offset)
		if errors.Is(err, io.EOF) {
			return []byte{}, discarded, discarded, true, nil
		}
		if err != nil {
			return nil, 0, 0, false, fmt.Errorf("transcript: seek offset: %w", err)
		}
	}

	buf := make([]byte, limit)
	n, err := io.ReadFull(gr, buf)
	switch {
	case errors.Is(err, io.EOF):
		return []byte{}, offset, offset, true, nil
	case errors.Is(err, io.ErrUnexpectedEOF):
		total := offset + int64(n)
		return buf[:n], total, total, true, nil
	case err != nil:
		return nil, 0, 0, false, fmt.Errorf("transcript: read chunk: %w", err)
	}

	rem, err := io.Copy(io.Discard, gr)
	if err != nil {
		return nil, 0, 0, false, fmt.Errorf("transcript: count remaining bytes: %w", err)
	}

	next := offset + int64(n)
	total := next + rem
	return buf[:n], next, total, rem == 0, nil
}

// ReadPaginatedBytes reads up to limit bytes from an in-memory raw stream starting at offset.
func ReadPaginatedBytes(raw []byte, offset int64, limit int) (chunk []byte, nextOffset int64, totalBytes int64, eof bool, err error) {
	if offset < 0 {
		return nil, 0, 0, false, errors.New("transcript: offset must be non-negative")
	}
	if limit <= 0 {
		limit = DefaultPageLimit
	}

	totalBytes = int64(len(raw))
	if offset >= totalBytes {
		return []byte{}, totalBytes, totalBytes, true, nil
	}

	end := offset + int64(limit)
	if end > totalBytes {
		end = totalBytes
	}

	chunk = make([]byte, end-offset)
	copy(chunk, raw[offset:end])
	return chunk, end, totalBytes, end >= totalBytes, nil
}
