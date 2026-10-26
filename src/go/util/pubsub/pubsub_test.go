package pubsub

import (
	"testing"
	"time"
)

// Unsubscribing ends a publish blocked on the subscriber, and later
// publishes skip it.
func TestUnsubscribeReleasesPublishers(t *testing.T) {
	t.Parallel()

	const topic = "unsubscribe-test"

	ch := Subscribe(topic)
	published := make(chan struct{})

	go func() {
		defer close(published)

		Publish(topic, "first") // no one reads ch
	}()

	Unsubscribe(topic, ch)

	select {
	case <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish() to an unsubscribed channel still blocked after 5s, want it to return")
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		Publish(topic, "second")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish() after Unsubscribe() blocked, want it to skip the channel")
	}
}
