package process

import "sync"

// RingBuffer is a thread-safe circular log buffer that keeps the last N lines.
type RingBuffer struct {
	mu   sync.RWMutex
	buf  []string
	cap  int
	head int    // index of the next write position
	size int    // current number of entries
	seq  uint64 // lines ever added, so readers can tell whether anything changed
}

// NewRingBuffer creates a new RingBuffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		buf: make([]string, capacity),
		cap: capacity,
	}
}

// Add appends a line to the buffer, overwriting the oldest entry if full.
func (r *RingBuffer) Add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.head] = line
	r.seq++
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
}

// Reset empties the buffer. Seq still moves, so a reader that skips work
// while it stands still sees the lines gone.
func (r *RingBuffer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.buf)
	r.head, r.size = 0, 0
	r.seq++
}

// Seq counts the lines ever added. It changes exactly when Lines would, so a
// reader can skip copying the buffer while it has not moved.
func (r *RingBuffer) Seq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.seq
}

// Lines returns a snapshot of all stored lines in insertion order.
func (r *RingBuffer) Lines() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return nil
	}
	result := make([]string, r.size)
	if r.size < r.cap {
		copy(result, r.buf[:r.size])
	} else {
		// Buffer is full; oldest entry is at r.head
		n := copy(result, r.buf[r.head:])
		copy(result[n:], r.buf[:r.head])
	}
	return result
}

// LastN returns the last n entries in insertion order.
func (r *RingBuffer) LastN(n int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 || n <= 0 {
		return nil
	}
	if n > r.size {
		n = r.size
	}
	all := make([]string, r.size)
	if r.size < r.cap {
		copy(all, r.buf[:r.size])
	} else {
		cnt := copy(all, r.buf[r.head:])
		copy(all[cnt:], r.buf[:r.head])
	}
	return all[r.size-n:]
}
