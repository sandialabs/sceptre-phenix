package main

import (
	"errors"
	"sync"

	ft "phenix/web/forward/forwardtypes"
)

var errListenerNotFound = errors.New("listener not found")

type listenerManager struct {
	mu        sync.RWMutex
	listeners map[string]*LocalListener
	nextID    int
}

func newListenerManager() *listenerManager {
	return &listenerManager{
		listeners: make(map[string]*LocalListener),
	}
}

func (m *listenerManager) add(listener ft.Listener) *LocalListener {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextID++
	local := &LocalListener{ID: m.nextID, Listener: listener} //nolint:exhaustruct // partial initialization
	m.listeners[listener.ToKey()] = local

	return local
}

func (m *listenerManager) snapshot() Listeners {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(Listeners, 0, len(m.listeners))
	for _, listener := range m.listeners {
		result = append(result, *listener)
	}

	return result
}

func (m *listenerManager) hasKey(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.listeners[key]

	return ok
}

func (m *listenerManager) remove(key string) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	listener, ok := m.listeners[key]
	if !ok {
		return 0, false
	}

	if listener.listener != nil {
		_ = listener.listener.Close()
	}

	port := listener.SrcPort
	delete(m.listeners, key)

	return port, true
}

func (m *listenerManager) withListener(id int, fn func(*LocalListener) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, listener := range m.listeners {
		if listener.ID == id {
			return fn(listener)
		}
	}

	return errListenerNotFound
}

// listenerManager is the sole owner of the process-local listener registry.
var localListeners = newListenerManager() //nolint:gochecknoglobals // process-local state
