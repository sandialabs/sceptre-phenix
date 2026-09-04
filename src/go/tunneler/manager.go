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
	byID      map[int]*LocalListener
	nextID    int
}

func newListenerManager() *listenerManager {
	return &listenerManager{
		listeners: make(map[string]*LocalListener),
		byID:      make(map[int]*LocalListener),
	}
}

func (m *listenerManager) add(listener ft.Listener) *LocalListener {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextID++

	var (
		local = &LocalListener{ID: m.nextID, Listener: listener} //nolint:exhaustruct // partial initialization
		key   = listener.ToKey()
	)

	if previous, ok := m.listeners[key]; ok {
		delete(m.byID, previous.ID)
	}

	m.listeners[key] = local
	m.byID[local.ID] = local

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
	delete(m.byID, listener.ID)

	return port, true
}

func (m *listenerManager) withListener(id int, fn func(*LocalListener) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	listener, ok := m.byID[id]
	if ok {
		return fn(listener)
	}

	return errListenerNotFound
}

// listenerManager is the sole owner of the process-local listener registry.
// Listener IDs are session-scoped and are not persisted across restarts.
var localListeners = newListenerManager() //nolint:gochecknoglobals // process-local state
