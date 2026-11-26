package ringbuf

import (
	"bytes"
	"sync"
)

// DefaultRingCapacity is the default bounded replay buffer size (256 KiB).
const DefaultRingCapacity = 256 * 1024

// Buffer stores a bounded ring buffer for fast WebSocket replay alongside the
// full raw byte stream for transcript snapshots on session close.
type Buffer struct {
	mu       sync.RWMutex
	ring     []byte
	writePos int
	ringSize int
	full     bytes.Buffer
}

// New constructs a Buffer with the specified ring capacity (or DefaultRingCapacity if <= 0).
func New(ringCapacity int) *Buffer {
	if ringCapacity <= 0 {
		ringCapacity = DefaultRingCapacity
	}
	return &Buffer{
		ring: make([]byte, ringCapacity),
	}
}

// Write appends p to both the bounded replay ring buffer and the full raw stream.
func (b *Buffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	_, _ = b.full.Write(p)

	cap := len(b.ring)
	if len(p) >= cap {
		copy(b.ring, p[len(p)-cap:])
		b.writePos = 0
		b.ringSize = cap
		return len(p), nil
	}

	first := min(len(p), cap-b.writePos)
	copy(b.ring[b.writePos:], p[:first])
	if first < len(p) {
		copy(b.ring[0:], p[first:])
	}
	b.writePos = (b.writePos + len(p)) % cap
	b.ringSize = min(cap, b.ringSize+len(p))
	return len(p), nil
}

// ReplayBytes returns a copy of the bounded ring buffer in chronological order.
func (b *Buffer) ReplayBytes() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]byte, b.ringSize)
	if b.ringSize == 0 {
		return out
	}

	cap := len(b.ring)
	if b.ringSize < cap {
		copy(out, b.ring[:b.ringSize])
		return out
	}

	n := copy(out, b.ring[b.writePos:])
	copy(out[n:], b.ring[:b.writePos])
	return out
}

// FullBytes returns a copy of the complete raw session stream captured so far.
func (b *Buffer) FullBytes() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()

	raw := b.full.Bytes()
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

// Len returns the total number of raw bytes written to the buffer.
func (b *Buffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.full.Len()
}
