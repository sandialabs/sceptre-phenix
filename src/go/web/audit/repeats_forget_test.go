package audit

import (
	"errors"
	"testing"
	"time"
)

// Once a success window passes, the next event forgets every key whose
// window has passed with nothing skipped; a key with skipped events is kept,
// with its count, until it is unused for repeatForget.
func TestRepeatsForget(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start
	limit := NewRepeats(5*time.Minute, time.Minute, 1024, func() time.Time { return now })

	for _, vm := range []string{"vm1", "vm2", "vm3"} {
		limit.Success(nil, VNCAttach, "vnc attached", Fields{User: "alice", VM: vm})
	}

	limit.Success(nil, VNCAttach, "vnc attached", Fields{User: "alice", VM: "vm1"})
	limit.Failure(nil, VNCAttach, "vnc attach failed", Fields{User: "alice", VM: "vm4"}, errors.New("vm not found"))

	if n := len(limit.counts); n != 4 {
		t.Fatalf("remembered %d keys, want 4", n)
	}

	cases := []struct {
		at   time.Duration
		want int
	}{
		{at: 5 * time.Minute, want: 2},                // vm1 skipped one; the new vm5
		{at: 5*time.Minute + 59*time.Second, want: 2}, // no sweep until the next window
		{at: 70 * time.Minute, want: 1},               // every key unused for an hour; vm5 again
	}

	for _, c := range cases {
		now = start.Add(c.at)

		limit.Success(nil, VNCAttach, "vnc attached", Fields{User: "alice", VM: "vm5"})

		if n := len(limit.counts); n != c.want {
			t.Errorf("after %v remembered %d keys, want %d", c.at, n, c.want)
		}
	}
}
