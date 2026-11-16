package pubsub

import (
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Unsubscribing ends a publish blocked on the subscriber, and later
// publishes skip it.
func TestUnsubscribeReleasesPublishers(t *testing.T) {
	t.Parallel()

	const topic = "unsubscribe-test"

	first := Subscribe(topic)
	ch := Subscribe(topic)
	published := make(chan struct{})

	t.Cleanup(func() { Unsubscribe(topic, first) })

	go func() {
		defer close(published)

		Publish(topic, "first") // no one reads ch
	}()

	// Publish sends in subscription order, so it now waits on ch.
	<-first

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
	case got := <-first:
		if got != "second" {
			t.Errorf("remaining subscriber got %v, want second", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("remaining subscriber got nothing for 5s, want second")
	}

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

// A second Unsubscribe of the same channel does nothing: it neither closes the channel nor drops another
// subscriber of the topic.
func TestUnsubscribeTwice(t *testing.T) {
	t.Parallel()

	const topic = "unsubscribe-twice"

	ch := Subscribe(topic)
	other := Subscribe(topic)

	t.Cleanup(func() { Unsubscribe(topic, other) })

	Unsubscribe(topic, ch)
	Unsubscribe(topic, ch)

	go Publish(topic, "msg")

	select {
	case got := <-other:
		if got != "msg" {
			t.Errorf("other subscriber got %v, want msg", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("other subscriber got nothing for 5s after the second Unsubscribe(), want msg")
	}

	select {
	case got, open := <-ch:
		t.Errorf("unsubscribed channel received %v (open %t), want nothing", got, open)
	default:
	}
}

// pubsubReceive reads up to n messages from ch, giving up once none comes for 5s.
func pubsubReceive(ch chan any, n int) []any {
	got := make([]any, 0, n)

	for range n {
		select {
		case msg := <-ch:
			got = append(got, msg)
		case <-time.After(5 * time.Second):
			return got
		}
	}

	return got
}

// Publish sends each message of a topic, in order, to every subscriber of that topic and to no other.
func TestPublish(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		subscribers int
		msgs        []any
	}{
		"no subscribers":   {subscribers: 0, msgs: []any{"lost"}},
		"one subscriber":   {subscribers: 1, msgs: []any{"a", 1, nil}},
		"many subscribers": {subscribers: 3, msgs: []any{"a", "b"}},
		"no messages":      {subscribers: 2, msgs: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			topic, otherTopic := "publish-"+name, "publish-other-"+name

			other := Subscribe(otherTopic)
			t.Cleanup(func() { Unsubscribe(otherTopic, other) })

			subs := make([]chan any, tc.subscribers)
			for i := range subs {
				subs[i] = Subscribe(topic)
				t.Cleanup(func() { Unsubscribe(topic, subs[i]) })
			}

			published := make(chan struct{})

			go func() {
				defer close(published)

				for _, msg := range tc.msgs {
					Publish(topic, msg)
				}
			}()

			// each subscriber reads on its own, as Publish waits for each in turn
			got := make([][]any, len(subs))
			read := make(chan int)

			for i, ch := range subs {
				go func() {
					got[i] = pubsubReceive(ch, len(tc.msgs))
					read <- i
				}()
			}

			for range subs {
				<-read
			}

			select {
			case <-published:
			case <-time.After(5 * time.Second):
				t.Fatal("Publish() still blocked 5s after every subscriber read, want it to return")
			}

			for i := range subs {
				if diff := cmp.Diff(tc.msgs, got[i], cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("subscriber %d messages mismatch (-want +got):\n%s", i, diff)
				}
			}

			select {
			case msg := <-other:
				t.Errorf("subscriber of %s received %v, want nothing", otherTopic, msg)
			default:
			}
		})
	}
}

// Subscribers that come and go while others publish neither race nor block a publisher for good.
func TestPublishConcurrent(t *testing.T) {
	t.Parallel()

	const (
		topic      = "publish-concurrent"
		publishers = 4
		messages   = 50
		churners   = 4
	)

	reader := Subscribe(topic)
	stop := make(chan struct{})
	read := make(chan int)

	go func() {
		n := 0

		for {
			select {
			case <-reader:
				n++
			case <-stop:
				read <- n

				return
			}
		}
	}()

	var wg sync.WaitGroup

	for range churners {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range messages {
				Unsubscribe(topic, Subscribe(topic))
			}
		}()
	}

	for range publishers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range messages {
				Publish(topic, i)
			}
		}()
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("publishers and subscribers still running after 10s, want them done")
	}

	close(stop)
	Unsubscribe(topic, reader)

	if n := <-read; n != publishers*messages {
		t.Errorf("steady subscriber read %d messages, want %d", n, publishers*messages)
	}
}
