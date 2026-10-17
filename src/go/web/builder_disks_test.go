package web

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/api/disk"
)

// TestBuilderDiskListerSharesOneListing shares one listing among concurrent
// requests and those within [builderDiskListTTL], and never reuses a failure.
func TestBuilderDiskListerSharesOneListing(t *testing.T) {
	var (
		calls   atomic.Int32
		fail    atomic.Bool
		seconds atomic.Int64
		entered = make(chan struct{})
		release = make(chan struct{})
	)

	lister := newBuilderDiskLister(func() ([]disk.Details, error) {
		// The first listing runs until every other request waits for it.
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}

		if fail.Load() {
			return nil, errors.New("minimega is not running")
		}

		return []disk.Details{{Name: "base.qc2"}}, nil
	}, func() time.Time { return time.Unix(seconds.Load(), 0) })

	const requests = 8

	var (
		group   sync.WaitGroup
		results = make([][]disk.Details, requests)
		errs    = make([]error, requests)
	)

	for i := range requests {
		group.Add(1)

		go func() {
			defer group.Done()

			results[i], errs[i] = lister.images()
		}()

		if i == 0 {
			<-entered
		}
	}

	waitBlockedIn(t, "(*builderDiskLister).images", requests)
	close(release)
	group.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("%d concurrent images() calls listed the images %d times, want once", requests, got)
	}

	want := []disk.Details{{Name: "base.qc2"}}

	for i := range requests {
		if errs[i] != nil {
			t.Errorf("concurrent images() call %d error: %v", i, errs[i])
		}

		if diff := cmp.Diff(want, results[i]); diff != "" {
			t.Errorf("concurrent images() call %d mismatch (-want +got):\n%s", i, diff)
		}
	}

	seconds.Store(int64((builderDiskListTTL - time.Second) / time.Second))

	images, err := lister.images()
	if err != nil {
		t.Errorf("images() within %s error: %v", builderDiskListTTL, err)
	}

	if diff := cmp.Diff(want, images); diff != "" {
		t.Errorf("images() within %s mismatch (-want +got):\n%s", builderDiskListTTL, diff)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("after images() within %s, the images were listed %d times, want the first listing reused", builderDiskListTTL, got)
	}

	seconds.Store(int64(builderDiskListTTL / time.Second))
	fail.Store(true)

	for range 2 {
		if _, err := lister.images(); err == nil {
			t.Error("images() of a failing listing = nil error, want an error")
		}
	}

	if got := calls.Load(); got != 3 {
		t.Errorf("the images were listed %d times, want twice more: once the listing expired, and again after it failed", got)
	}

	fail.Store(false)

	for range 2 {
		images, err := lister.images()
		if err != nil {
			t.Errorf("images() after a failure error: %v", err)
		}

		if diff := cmp.Diff(want, images); diff != "" {
			t.Errorf("images() after a failure mismatch (-want +got):\n%s", diff)
		}
	}

	if got := calls.Load(); got != 4 {
		t.Errorf("the images were listed %d times, want once more after the failure, then reused", got)
	}
}

// waitBlockedIn waits until n goroutines are blocked on a channel receive inside function.
func waitBlockedIn(t *testing.T, function string, n int) {
	t.Helper()

	buf := make([]byte, 1<<20)

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); runtime.Gosched() {
		blocked := 0

		for stack := range strings.SplitSeq(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, "[chan receive") && strings.Contains(stack, function) {
				blocked++
			}
		}

		if blocked >= n {
			return
		}
	}

	t.Fatalf("fewer than %d goroutines blocked in %s", n, function)
}
