package pubsub

import "sync"

var (
	mu   sync.RWMutex
	subs = make(map[string][]chan interface{})
)

func Subscribe(topic string) chan interface{} {
	mu.Lock()
	defer mu.Unlock()

	ch := make(chan interface{})

	subs[topic] = append(subs[topic], ch)

	return ch
}

func Publish(topic string, msg interface{}) {
	mu.RLock()
	defer mu.RUnlock()

	for _, ch := range subs[topic] {
		ch <- msg
	}
}
