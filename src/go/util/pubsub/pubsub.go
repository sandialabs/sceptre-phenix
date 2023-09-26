package pubsub

import "sync"

var (
	mu   sync.RWMutex
	subs = make(map[string][]chan any)
)

func Subscribe(topic string) chan any {
	mu.Lock()
	defer mu.Unlock()

	ch := make(chan any)

	subs[topic] = append(subs[topic], ch)

	return ch
}

func Publish(topic string, msg any) {
	mu.RLock()
	defer mu.RUnlock()

	for _, ch := range subs[topic] {
		ch <- msg
	}
}
