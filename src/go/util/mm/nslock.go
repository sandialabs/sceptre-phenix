package mm

import "sync"

// keyedMutex hands out one mutex per key, here a minimega namespace. A key's
// mutex is dropped once nothing holds or waits on it.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu   sync.Mutex
	refs int // holders and waiters, guarded by keyedMutex.mu
}

// lock locks key's mutex and returns the function that unlocks it.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()

	if k.locks == nil {
		k.locks = make(map[string]*keyedLock)
	}

	l, ok := k.locks[key]
	if !ok {
		l = new(keyedLock)
		k.locks[key] = l
	}

	l.refs++

	k.mu.Unlock()

	l.mu.Lock()

	var once sync.Once

	return func() {
		once.Do(func() {
			l.mu.Unlock()

			k.mu.Lock()
			defer k.mu.Unlock()

			l.refs--

			if l.refs == 0 {
				delete(k.locks, key)
			}
		})
	}
}

var (
	// vmConfigLocks serializes phenix's `vm config` ... `vm launch` sequences
	// in each namespace. minimega keeps one VM config per namespace and runs
	// commands from different connections concurrently, and a bare `vm launch`
	// launches every VM queued in the namespace, so two interleaved sequences
	// can launch a VM with the other's config.
	vmConfigLocks keyedMutex //nolint:gochecknoglobals // shared by all callers

	// ccLocks serializes phenix's cc commands that depend on the namespace's
	// `cc filter`, which minimega keeps once per namespace and applies to every
	// cc command posted while it is set.
	ccLocks keyedMutex //nolint:gochecknoglobals // shared by all callers
)

// LockVMConfig locks the namespace's VM config for a `vm config` ... `vm
// launch` sequence (see vmConfigLocks) and returns the function that unlocks
// it.
func LockVMConfig(ns string) func() {
	return vmConfigLocks.lock(ns)
}
