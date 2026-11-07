package pubsub

import (
	"slices"
	"sync"
)

// subscription is one subscriber of a topic. Unsubscribe closes done.
type subscription struct {
	ch   chan any
	done chan struct{}
}

var (
	mu   sync.RWMutex                      //nolint:gochecknoglobals // package level subscription registry
	subs = make(map[string][]subscription) //nolint:gochecknoglobals // package level subscription registry
)

func Subscribe(topic string) chan any {
	mu.Lock()
	defer mu.Unlock()

	ch := make(chan any)

	subs[topic] = append(subs[topic], subscription{ch: ch, done: make(chan struct{})})

	return ch
}

// Unsubscribe stops sending topic's messages to ch. A message a publisher is
// sending to ch meanwhile is dropped, so the publisher does not wait forever.
func Unsubscribe(topic string, ch chan any) {
	mu.Lock()
	defer mu.Unlock()

	subs[topic] = slices.DeleteFunc(subs[topic], func(s subscription) bool {
		if s.ch != ch {
			return false
		}

		close(s.done)

		return true
	})
}

// Publish sends msg to each subscriber of topic in turn. It waits for each
// subscriber to read msg or to unsubscribe.
func Publish(topic string, msg any) {
	mu.RLock()
	targets := slices.Clone(subs[topic])
	mu.RUnlock()

	for _, s := range targets {
		select {
		case s.ch <- msg:
		case <-s.done:
		}
	}
}
