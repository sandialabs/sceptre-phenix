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

// Unsubscribe returns while a publisher is blocked on another subscriber,
// on the same topic or on a different one.
func TestUnsubscribeWithOtherPublisherBlocked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		blockedTopic string
		unsubTopic   string
	}{
		{name: "different topic", blockedTopic: "blocked-other-a", unsubTopic: "blocked-other-b"},
		{name: "same topic", blockedTopic: "blocked-same", unsubTopic: "blocked-same"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := Subscribe(tt.blockedTopic)
			blocked := Subscribe(tt.blockedTopic)
			other := Subscribe(tt.unsubTopic)
			published := make(chan struct{})

			t.Cleanup(func() { Unsubscribe(tt.blockedTopic, first) })

			go func() {
				defer close(published)

				Publish(tt.blockedTopic, "msg") // no one reads blocked
			}()

			// Publish sends in subscription order, so it now waits on blocked.
			<-first

			unsubscribed := make(chan struct{})

			go func() {
				defer close(unsubscribed)

				Unsubscribe(tt.unsubTopic, other)
			}()

			select {
			case <-unsubscribed:
			case <-time.After(5 * time.Second):
				t.Fatalf("Unsubscribe(%q) blocked for 5s while Publish(%q) waited, want it to return", tt.unsubTopic, tt.blockedTopic)
			}

			Unsubscribe(tt.blockedTopic, blocked)

			select {
			case <-published:
			case <-time.After(5 * time.Second):
				t.Fatalf("Publish(%q) still blocked 5s after Unsubscribe(), want it to return", tt.blockedTopic)
			}
		})
	}
}

// A second Unsubscribe of the same channel does nothing.
func TestUnsubscribeTwice(t *testing.T) {
	t.Parallel()

	const topic = "unsubscribe-twice"

	ch := Subscribe(topic)

	Unsubscribe(topic, ch)
	Unsubscribe(topic, ch)
	Publish(topic, "msg")
}
