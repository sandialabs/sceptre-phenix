package scorch

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type backgroundTask struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

type taskGroup struct {
	mu     sync.Mutex
	tasks  map[string]*backgroundTask
	failed func(error)
}

func newTaskGroup() *taskGroup {
	return &taskGroup{tasks: make(map[string]*backgroundTask)} //nolint:exhaustruct // mutex has zero value
}

func (g *taskGroup) start(ctx context.Context, key string, run func(context.Context) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.tasks[key]; exists {
		return errors.New("background component is already active in this iteration")
	}
	ctx, cancel := context.WithCancel(ctx)
	t := &backgroundTask{cancel: cancel, done: make(chan struct{})} //nolint:exhaustruct // result is asynchronous
	g.tasks[key] = t
	go func() {
		defer close(t.done)
		err := run(ctx)
		g.mu.Lock()
		if ctx.Err() == nil {
			t.err = err
			if err != nil && g.failed != nil {
				g.failed(err)
			}
		}
		g.mu.Unlock()
	}()
	return nil
}

func (g *taskGroup) stop(key string) (bool, error) {
	g.mu.Lock()
	t := g.tasks[key]
	g.mu.Unlock()
	if t == nil {
		return false, nil
	}
	t.cancel()
	<-t.done
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.tasks, key)
	if errors.Is(t.err, context.Canceled) {
		return true, nil
	}
	return true, t.err
}

func (g *taskGroup) close() error {
	g.mu.Lock()
	keys := make([]string, 0, len(g.tasks))
	for key, t := range g.tasks {
		keys = append(keys, key)
		t.cancel()
	}
	g.mu.Unlock()
	var result error
	for _, key := range keys {
		_, err := g.stop(key)
		result = errors.Join(result, err)
	}
	return result
}

func (g *taskGroup) stopPrefix(prefix string) error {
	g.mu.Lock()
	keys := make([]string, 0)
	for key, t := range g.tasks {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
			t.cancel()
		}
	}
	g.mu.Unlock()
	var result error
	for _, key := range keys {
		_, err := g.stop(key)
		result = errors.Join(result, err)
	}
	return result
}
