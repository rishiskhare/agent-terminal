package scrollback

import "sync"

// Ring is a fixed-capacity byte ring buffer for PTY scrollback.
type Ring struct {
	mu   sync.Mutex
	buf  []byte
	size int // capacity
	len  int // bytes currently stored
	pos  int // next write index
}

// New returns a ring that retains at most size bytes.
func New(size int) *Ring {
	if size <= 0 {
		size = 1
	}
	return &Ring{
		buf:  make([]byte, size),
		size: size,
	}
}

// Write appends p to the ring, discarding the oldest bytes if needed.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}

	if n >= r.size {
		// Keep only the trailing window.
		copy(r.buf, p[n-r.size:])
		r.len = r.size
		r.pos = 0
		return n, nil
	}

	for _, b := range p {
		r.buf[r.pos] = b
		r.pos = (r.pos + 1) % r.size
		if r.len < r.size {
			r.len++
		}
	}
	return n, nil
}

// Bytes returns a copy of the stored scrollback in chronological order.
func (r *Ring) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.len == 0 {
		return nil
	}

	out := make([]byte, r.len)
	if r.len < r.size {
		copy(out, r.buf[:r.len])
		return out
	}

	// Full buffer: oldest byte is at pos.
	n := copy(out, r.buf[r.pos:])
	copy(out[n:], r.buf[:r.pos])
	return out
}

// Len returns the number of bytes currently stored.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.len
}
