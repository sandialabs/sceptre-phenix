package pubsub

import (
	"slices"
	"sync"
)

var (
	mu   sync.RWMutex                  //nolint:gochecknoglobals // package level subscription registry
	subs = make(map[string][]chan any) //nolint:gochecknoglobals // package level subscription registry
)

func Subscribe(topic string) chan any {
	mu.Lock()
	defer mu.Unlock()

	ch := make(chan any)

	subs[topic] = append(subs[topic], ch)

	return ch
}

// Unsubscribe stops sending topic's messages to ch. A message a publisher is
// sending to ch meanwhile is dropped, so the publisher does not wait forever.
func Unsubscribe(topic string, ch chan any) {
	removed := make(chan struct{})

	go func() {
		defer close(removed)

		mu.Lock()
		defer mu.Unlock()

		subs[topic] = slices.DeleteFunc(subs[topic], func(c chan any) bool { return c == ch })
	}()

	for {
		select {
		case <-ch:
		case <-removed:
			return
		}
	}
}

func Publish(topic string, msg any) {
	mu.RLock()
	defer mu.RUnlock()

	for _, ch := range subs[topic] {
		ch <- msg
	}
}
