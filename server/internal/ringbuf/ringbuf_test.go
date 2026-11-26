package ringbuf

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestBufferDefaultCapacity(t *testing.T) {
	b := New(0)
	if len(b.ring) != DefaultRingCapacity {
		t.Fatalf("ring capacity = %d, want %d", len(b.ring), DefaultRingCapacity)
	}
	if got := b.ReplayBytes(); len(got) != 0 {
		t.Fatalf("empty ReplayBytes len = %d, want 0", len(got))
	}
	if got := b.FullBytes(); len(got) != 0 {
		t.Fatalf("empty FullBytes len = %d, want 0", len(got))
	}
}

func TestBufferWrapAroundAndFullBytes(t *testing.T) {
	b := New(8)

	chunks := [][]byte{
		[]byte("abc"),     // ring: "abc"
		[]byte("defg"),    // ring: "abcdefg" (7)
		[]byte("hij"),     // ring wraps: "cdefghij" (8)
		[]byte("klmn"),    // ring wraps: "ghijklmn" (8)
		[]byte("0123456"), // ring wraps: "n0123456" (8)
	}
	for _, c := range chunks {
		n, err := b.Write(c)
		if err != nil || n != len(c) {
			t.Fatalf("Write(%q) = (%d, %v), want (%d, nil)", c, n, err, len(c))
		}
	}

	wantFull := []byte("abcdefghijklm" + "n0123456")
	if got := b.FullBytes(); !bytes.Equal(got, wantFull) {
		t.Fatalf("FullBytes = %q, want %q", got, wantFull)
	}
	if b.Len() != len(wantFull) {
		t.Fatalf("Len() = %d, want %d", b.Len(), len(wantFull))
	}

	wantReplay := []byte("n0123456")
	if got := b.ReplayBytes(); !bytes.Equal(got, wantReplay) {
		t.Fatalf("ReplayBytes = %q, want %q", got, wantReplay)
	}

	// Write chunk larger than ring capacity.
	if _, err := b.Write([]byte("QRSTUVWXYZ")); err != nil {
		t.Fatalf("Write large chunk error = %v", err)
	}
	wantReplay = []byte("STUVWXYZ")
	if got := b.ReplayBytes(); !bytes.Equal(got, wantReplay) {
		t.Fatalf("ReplayBytes after large write = %q, want %q", got, wantReplay)
	}
	wantFull = append(wantFull, []byte("QRSTUVWXYZ")...)
	if got := b.FullBytes(); !bytes.Equal(got, wantFull) {
		t.Fatalf("FullBytes after large write = %q, want %q", got, wantFull)
	}

	// Mutating returned slices must not corrupt internal state.
	replay := b.ReplayBytes()
	replay[0] = '!'
	if got := b.ReplayBytes(); !bytes.Equal(got, []byte("STUVWXYZ")) {
		t.Fatalf("ReplayBytes mutated after caller edit: %q", got)
	}
}

func TestBufferConcurrentWritesAndReads(t *testing.T) {
	b := New(64)

	const writers = 8
	const writesPerWriter = 100

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				msg := []byte(fmt.Sprintf("w%d-%03d|", id, i))
				if _, err := b.Write(msg); err != nil {
					t.Errorf("Write error = %v", err)
				}
				_ = b.ReplayBytes()
				_ = b.FullBytes()
			}
		}(w)
	}
	wg.Wait()

	full := b.FullBytes()
	replay := b.ReplayBytes()

	if len(replay) != 64 {
		t.Fatalf("len(ReplayBytes) = %d, want 64", len(replay))
	}
	if !bytes.HasSuffix(full, replay) {
		t.Fatalf("expected ReplayBytes %q to be suffix of FullBytes (len=%d)", replay, len(full))
	}
}
