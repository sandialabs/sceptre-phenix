package mmcli

import (
	"context"
	"slices"
	"sync"
)

// connLock guards the shared connection: FIFO, except background commands wait behind all others.
// The zero value is unlocked.
type connLock struct {
	mu   sync.Mutex
	held bool
	// waiting commands in the order they asked: [0] the others, [1] background
	waiting [2][]chan struct{}
}

// acquire waits for the lock until ctx is done, reporting whether it got it.
func (l *connLock) acquire(ctx context.Context, background bool) bool {
	lane := 0
	if background {
		lane = 1
	}

	l.mu.Lock()

	if !l.held {
		l.held = true
		l.mu.Unlock()

		return true
	}

	turn := make(chan struct{})
	l.waiting[lane] = append(l.waiting[lane], turn)

	l.mu.Unlock()

	select {
	case <-turn:
		return true
	case <-ctx.Done():
	}

	l.mu.Lock()

	if i := slices.Index(l.waiting[lane], turn); i >= 0 {
		l.waiting[lane] = slices.Delete(l.waiting[lane], i, i+1)
		l.mu.Unlock()

		return false
	}

	l.mu.Unlock()

	// handed the lock just as ctx ended: pass it on
	l.release()

	return false
}

// release hands the lock to the next command waiting, if any.
func (l *connLock) release() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for lane := range l.waiting {
		if len(l.waiting[lane]) > 0 {
			turn := l.waiting[lane][0]
			l.waiting[lane] = l.waiting[lane][1:]

			close(turn)

			return
		}
	}

	l.held = false
}

// waiters returns how many commands wait for the lock in each lane.
func (l *connLock) waiters() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.waiting[0]), len(l.waiting[1])
}
