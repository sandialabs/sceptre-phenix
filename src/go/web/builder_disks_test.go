package web

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	"phenix/api/disk"
	bdoc "phenix/types/builder"
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

			results[i], errs[i] = lister.images(t.Context())
		}()

		if i == 0 {
			<-entered
		}
	}

	waitBlockedIn(t, "(*builderDiskLister).images", "[select", requests)
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

	images, err := lister.images(t.Context())
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
		if _, err := lister.images(t.Context()); err == nil {
			t.Error("images() of a failing listing = nil error, want an error")
		}
	}

	if got := calls.Load(); got != 3 {
		t.Errorf("the images were listed %d times, want twice more: once the listing expired, and again after it failed", got)
	}

	fail.Store(false)

	for range 2 {
		images, err := lister.images(t.Context())
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

// waitBlockedIn waits until n goroutines are blocked inside function, in
// the given state of a goroutine dump, such as "[select".
func waitBlockedIn(t *testing.T, function, state string, n int) {
	t.Helper()

	if got := waitGoroutines(function, state, func(blocked int) bool { return blocked >= n }); got < n {
		t.Fatalf("%d goroutines blocked in %s, want at least %d", got, function, n)
	}
}

// waitGoroutines counts the goroutines in state inside function until done
// accepts the count, for at most 10 seconds. It returns the last count.
func waitGoroutines(function, state string, done func(int) bool) int {
	buf := make([]byte, 1<<20)
	count := 0

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); runtime.Gosched() {
		count = 0

		for stack := range strings.SplitSeq(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, state) && strings.Contains(stack, function) {
				count++
			}
		}

		if done(count) {
			break
		}
	}

	return count
}

// builderHungDisks is a disk image source that lists base.qc2 only after
// release is called, and counts the listings it started in calls. The test
// releases it when it ends.
type builderHungDisks struct {
	calls   atomic.Int32
	release func()
	source  func() ([]disk.Details, error)
}

// newBuilderHungDisks returns a source that waits until it is released.
func newBuilderHungDisks(t *testing.T) *builderHungDisks {
	t.Helper()

	var (
		hung     = &builderHungDisks{calls: atomic.Int32{}, release: nil, source: nil}
		released = make(chan struct{})
		once     sync.Once
	)

	hung.release = func() { once.Do(func() { close(released) }) }
	hung.source = func() ([]disk.Details, error) {
		hung.calls.Add(1)
		<-released

		return []disk.Details{{Name: "base.qc2"}}, nil
	}

	t.Cleanup(hung.release)

	return hung
}

// withBuilderDiskWait sets how long a request waits for a disk listing.
func withBuilderDiskWait(wait time.Duration) builderOption {
	return func(api *builderAPI) { api.disks.wait = wait }
}

// TestBuilderDiskListerStopsWaiting asserts a request stops waiting for a
// listing that does not end when its context ends or the wait passes. The
// listing goes on alone, is not started again while it runs, and is then
// kept for the next requests.
func TestBuilderDiskListerStopsWaiting(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	for _, tt := range []struct {
		name  string
		ctx   context.Context //nolint:containedctx // the context of the request under test
		wait  time.Duration
		cause error
	}{
		{name: "the wait passes", ctx: t.Context(), wait: 20 * time.Millisecond, cause: context.DeadlineExceeded},
		{name: "the request ends", ctx: canceled, wait: time.Hour, cause: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hung := newBuilderHungDisks(t)
			lister := newBuilderDiskLister(hung.source, time.Now)
			lister.wait = tt.wait

			for i := range 3 {
				images, err := lister.images(tt.ctx)
				if !errors.Is(err, errBuilderDiskListWait) || !errors.Is(err, tt.cause) {
					t.Errorf("images() call %d of a hung listing error = %v, want %v and %v", i, err, errBuilderDiskListWait, tt.cause)
				}

				if images != nil {
					t.Errorf("images() call %d of a hung listing = %v, want nil", i, images)
				}
			}

			lister.mu.Lock()
			listing := lister.running
			lister.mu.Unlock()

			if listing == nil {
				t.Fatal("no listing runs after the requests stopped waiting, want the hung one")
			}

			hung.release()
			<-listing.done

			if got := hung.calls.Load(); got != 1 {
				t.Errorf("3 images() calls during a hung listing listed the images %d times, want once", got)
			}

			if got := waitGoroutines("(*builderDiskLister).list", "goroutine ", func(n int) bool { return n == 0 }); got != 0 {
				t.Errorf("%d listing goroutines remain after the listing ended, want none", got)
			}

			images, err := lister.images(t.Context())
			if err != nil {
				t.Errorf("images() after the late listing error: %v", err)
			}

			if diff := cmp.Diff([]disk.Details{{Name: "base.qc2"}}, images); diff != "" {
				t.Errorf("images() after the late listing mismatch (-want +got):\n%s", diff)
			}

			if got := hung.calls.Load(); got != 1 {
				t.Errorf("after the late listing, the images were listed %d times, want the late listing reused", got)
			}
		})
	}
}

// TestBuilderDiskListerSurvivesAPanic asserts a source that panics fails
// its listing, and the next request lists again.
func TestBuilderDiskListerSurvivesAPanic(t *testing.T) {
	var calls atomic.Int32

	lister := newBuilderDiskLister(func() ([]disk.Details, error) {
		if calls.Add(1) == 1 {
			panic("minimega went away")
		}

		return []disk.Details{{Name: "base.qc2"}}, nil
	}, time.Now)

	if _, err := lister.images(t.Context()); !errors.Is(err, errBuilderDiskListAborted) {
		t.Errorf("images() of a listing that panicked error = %v, want %v", err, errBuilderDiskListAborted)
	}

	images, err := lister.images(t.Context())
	if err != nil {
		t.Errorf("images() after a listing that panicked error: %v", err)
	}

	if diff := cmp.Diff([]disk.Details{{Name: "base.qc2"}}, images); diff != "" {
		t.Errorf("images() after a listing that panicked mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderRoutesGoOnWithoutTheDiskListing asserts the publish dry run,
// package resolve and the preflight disks check answer as they do when the
// images cannot be listed, when the listing does not end in time.
func TestBuilderRoutesGoOnWithoutTheDiskListing(t *testing.T) {
	const wait = 20 * time.Millisecond

	t.Run("publish dry run", func(t *testing.T) {
		hung := newBuilderHungDisks(t)
		harness, draft := imagedPreview(t, func(api *builderAPI) {
			withBuilderDisks(hung.source)(api)
			withBuilderDiskWait(wait)(api)
		})

		recorder, preview := previewBuilderDraft(t, harness, draft, nil,
			`{"mode":"topology","topology":{"name":"imaged","action":"update"},"dryRun":true}`)
		if recorder.Code != http.StatusOK || preview.Changes == nil {
			t.Fatalf("dry run of imaged = %d %s, want %d with changes", recorder.Code, recorder.Body, http.StatusOK)
		}

		for _, image := range preview.Changes.Images {
			if image.OnServer != nil {
				t.Errorf("dry run of imaged: image %s onServer = %t, want null", image.Name, *image.OnServer)
			}
		}

		if len(preview.Changes.Images) == 0 {
			t.Error("dry run of imaged: no images, want each image of the topology")
		}
	})

	t.Run("package resolve", func(t *testing.T) {
		hung := newBuilderHungDisks(t)
		harness := newBuilderResolveHarnessWith(t, hung.source)
		harness.reroute(withBuilderDisks(hung.source), withBuilderDiskWait(wait),
			withBuilderApps(func() []string { return []string{"pkg-app", "ntp"} }))

		status, dependencies := resolveBuilderPackage(t, harness, nil, builderResolvePackage(t, harness))
		if status != http.StatusOK {
			t.Fatalf("POST /builder/package/resolve: status = %d, want %d", status, http.StatusOK)
		}

		for _, name := range []string{"image//phenix/images/" + builderPackageImage, "image/gone.qc2"} {
			got := dependencies[name]
			if got.Status != builderDependencyUnknown || !strings.HasPrefix(got.Detail, builderDisksUnlisted) {
				t.Errorf("POST /builder/package/resolve: %s = %s %q, want %s %q",
					name, got.Status, got.Detail, builderDependencyUnknown, builderDisksUnlisted)
			}
		}
	})

	t.Run("preflight disks check", func(t *testing.T) {
		var hostReads atomic.Int32

		hung := newBuilderHungDisks(t)
		harness, draft := preflightFixture(t, &hostReads)
		harness.reroute(withBuilderPreflightSources(preflightSources(&hostReads)),
			withBuilderDisks(hung.source), withBuilderDiskWait(wait))

		report := preflightReport(t, postBuilderPreflight(harness, draft, nil, `{"checks":["disks"]}`))
		disks := preflightResultOf(t, report, bapi.PreflightDisks)

		want := bapi.PreflightResult{
			Name:    bapi.PreflightDisks,
			Status:  bapi.PreflightUnavailable,
			Summary: "The server did not list its disk images in time.",
			Issues: []bdoc.Issue{
				bdoc.NewIssue(bdoc.CodePreflightUnavailable, "", "the server did not list its disk images in time"),
			},
		}
		if diff := cmp.Diff(want, disks); diff != "" {
			t.Errorf("POST preflight: disks check mismatch (-want +got):\n%s", diff)
		}
	})
}
