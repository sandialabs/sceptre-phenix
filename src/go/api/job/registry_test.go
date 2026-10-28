package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/polltest"
)

// fakeClock is a clock tests move by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// recorder keeps every notification.
type recorder struct {
	mu   sync.Mutex
	jobs []Job
	gone []string
}

func (n *recorder) notify(j Job, removed bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if removed {
		n.gone = append(n.gone, j.ID)

		return
	}

	n.jobs = append(n.jobs, j)
}

func (n *recorder) of(id string) []Job {
	n.mu.Lock()
	defer n.mu.Unlock()

	var out []Job

	for _, j := range n.jobs {
		if j.ID == id {
			out = append(out, j)
		}
	}

	return out
}

func newTestRegistry(t *testing.T, opts ...Option) *Registry {
	t.Helper()

	r := NewRegistry(opts...)
	t.Cleanup(func() { r.ShutdownContext(context.Background(), 0) })

	return r
}

// wait returns the job once it has finished.
func wait(t *testing.T, r *Registry, id string) Job {
	t.Helper()

	var j Job

	polltest.Until(t, "job "+id+" finishes", func() bool {
		var ok bool
		if j, ok = r.Get(id); !ok {
			t.Fatalf("job %s is gone", id)
		}

		return j.Status.Terminal()
	})

	return j
}

// waitStatus waits for the job to reach status.
func waitStatus(t *testing.T, r *Registry, id string, status Status) {
	t.Helper()

	polltest.Until(t, fmt.Sprintf("job %s is %s", id, status), func() bool {
		j, _ := r.Get(id)

		return j.Status == status
	})
}

// outcomes runs the items through run as the map says: "ok", "fail" or
// "skip"; items left out get no outcome.
func outcomes(results map[string]string, err error) RunFunc {
	return func(_ context.Context, run *Run) error {
		for name, result := range results {
			run.ItemStarted(name)

			switch result {
			case "ok":
				run.ItemSucceeded(name, map[string]string{"name": name})
			case "fail":
				run.ItemFailed(name, errors.New(name+" broke"))
			case "skip":
				run.ItemSkipped(name, ErrForbidden.Error())
			}
		}

		return err
	}
}

func TestFinalStatus(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		items   []string
		results map[string]string
		err     error
		status  Status
		jobErr  string
		item    map[string]ItemStatus
	}{
		"all succeed": {
			items:   []string{"a", "b"},
			results: map[string]string{"a": "ok", "b": "ok"},
			status:  StatusSucceeded,
			item:    map[string]ItemStatus{"a": ItemSucceeded, "b": ItemSucceeded},
		},
		"some fail": {
			items:   []string{"a", "b"},
			results: map[string]string{"a": "ok", "b": "fail"},
			status:  StatusPartial,
			item:    map[string]ItemStatus{"a": ItemSucceeded, "b": ItemFailed},
		},
		"one skipped": {
			items:   []string{"a", "b"},
			results: map[string]string{"a": "ok", "b": "skip"},
			status:  StatusPartial,
			item:    map[string]ItemStatus{"a": ItemSucceeded, "b": ItemSkipped},
		},
		"all fail": {
			items:   []string{"a", "b"},
			results: map[string]string{"a": "fail", "b": "fail"},
			status:  StatusFailed,
			jobErr:  "every item failed",
			item:    map[string]ItemStatus{"a": ItemFailed, "b": ItemFailed},
		},
		"run error without items": {
			err:    errors.New("disk full"),
			status: StatusFailed,
			jobErr: "disk full",
		},
		"no items, no error": {
			status: StatusSucceeded,
		},
		"run error leaves items": {
			items:   []string{"a", "b"},
			results: map[string]string{"a": "ok"},
			err:     errors.New("stopped early"),
			status:  StatusPartial,
			jobErr:  "stopped early",
			item:    map[string]ItemStatus{"a": ItemSucceeded, "b": ItemFailed},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newTestRegistry(t)

			started, err := r.Start(Spec{Kind: "test", Owner: "alice", Items: tc.items, Run: outcomes(tc.results, tc.err)})
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			j := wait(t, r, started.ID)

			if j.Status != tc.status {
				t.Errorf("finished job Status = %s, want %s", j.Status, tc.status)
			}

			if j.Error != tc.jobErr {
				t.Errorf("finished job Error = %q, want %q", j.Error, tc.jobErr)
			}

			if j.Progress.Done != j.Progress.Total {
				t.Errorf("finished job Progress = %d/%d, want all done", j.Progress.Done, j.Progress.Total)
			}

			if j.Started == nil || j.Finished == nil {
				t.Errorf("finished job Started = %v, Finished = %v, want both set", j.Started, j.Finished)
			}

			got := make(map[string]ItemStatus, len(j.Items))
			for _, item := range j.Items {
				got[item.Name] = item.Status
			}

			if diff := cmp.Diff(tc.item, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("finished job item statuses mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestItemOutcomes(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)

	started, err := r.Start(Spec{
		Kind: "test", Owner: "alice", Items: []string{"a", "b", "a", "c"},
		Run: func(_ context.Context, run *Run) error {
			run.ItemSucceeded("a", []byte(`{"name":"a"}`))
			run.ItemFailed("b", fmt.Errorf("locked: %w", temporaryError{temporary: true}))
			run.ItemSkipped("c", "you do not have permission")
			run.ItemSucceeded("unknown", nil)

			return nil
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	names := make([]string, 0, len(started.Items))
	for _, item := range started.Items {
		names = append(names, item.Name)
	}

	if diff := cmp.Diff([]string{"a", "b", "c"}, names); diff != "" {
		t.Errorf("Start(items a, b, a, c) item names mismatch (-want +got):\n%s", diff)
	}

	if started.Progress.Total != 3 {
		t.Errorf("Start(items a, b, a, c) Progress.Total = %d, want 3", started.Progress.Total)
	}

	j := wait(t, r, started.ID)

	want := []Item{
		{Name: "a", Status: ItemSucceeded, Result: []byte(`{"name":"a"}`)},
		{Name: "b", Status: ItemFailed, Error: "locked: busy", Retryable: true},
		{Name: "c", Status: ItemSkipped, Error: "you do not have permission"},
	}

	if diff := cmp.Diff(want, j.Items); diff != "" {
		t.Errorf("finished job items mismatch (-want +got):\n%s", diff)
	}
}

func TestCancel(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)

	var (
		first   = make(chan struct{})
		release = make(chan struct{})
	)

	started, err := r.Start(Spec{
		Kind: "test", Owner: "alice", Items: []string{"a", "b", "c"}, Cancelable: true,
		Run: func(ctx context.Context, run *Run) error {
			run.ItemSucceeded("a", nil)
			run.ItemStarted("b")
			close(first)
			<-ctx.Done()
			<-release

			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-first

	canceling, err := r.Cancel(started.ID)
	if err != nil {
		t.Fatalf("Cancel(running job) error = %v", err)
	}

	if canceling.Status != StatusRunning {
		t.Errorf("Cancel(running job) Status = %s, want %s", canceling.Status, StatusRunning)
	}

	if !canceling.CancelRequested {
		t.Error("Cancel(running job) CancelRequested = false, want true")
	}

	close(release)

	j := wait(t, r, started.ID)

	if j.Status != StatusCanceled {
		t.Errorf("canceled job Status = %s, want %s", j.Status, StatusCanceled)
	}

	got := make(map[string]ItemStatus, len(j.Items))
	for _, item := range j.Items {
		got[item.Name] = item.Status
	}

	want := map[string]ItemStatus{"a": ItemSucceeded, "b": ItemCanceled, "c": ItemCanceled}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("canceled job item statuses mismatch (-want +got):\n%s", diff)
	}

	if _, err := r.Cancel(started.ID); !errors.Is(err, ErrFinished) {
		t.Errorf("Cancel(finished job) error = %v, want %v", err, ErrFinished)
	}

	if _, err := r.Cancel("jnope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel(%q) error = %v, want %v", "jnope", err, ErrNotFound)
	}
}

func TestCancelNotCancelable(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)
	release := make(chan struct{})

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: func(context.Context, *Run) error {
		<-release

		return nil
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if _, err := r.Cancel(started.ID); !errors.Is(err, ErrNotCancelable) {
		t.Errorf("Cancel(job that cannot be canceled) error = %v, want %v", err, ErrNotCancelable)
	}

	if err := r.Remove(started.ID); !errors.Is(err, ErrActive) {
		t.Errorf("Remove(running job) error = %v, want %v", err, ErrActive)
	}

	close(release)

	if j := wait(t, r, started.ID); j.Status != StatusSucceeded {
		t.Errorf("finished job Status = %s, want %s", j.Status, StatusSucceeded)
	}

	if err := r.Remove(started.ID); err != nil {
		t.Errorf("Remove(finished job) error = %v, want nil", err)
	}

	if _, ok := r.Get(started.ID); ok {
		t.Error("Get(removed job) ok = true, want false")
	}

	if err := r.Remove(started.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove(removed job) error = %v, want %v", err, ErrNotFound)
	}
}

// Jobs over the limits wait, queued, and start in the order they came as
// slots free; a queued job canceled never runs.
func TestLimitsQueueJobs(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t, WithLimits(1, 2))

	release := map[string]chan struct{}{}

	start := func(owner, name string) Job {
		t.Helper()

		ch := make(chan struct{})
		release[name] = ch

		j, err := r.Start(Spec{Kind: "test", Owner: owner, Title: name, Cancelable: true, Run: func(context.Context, *Run) error {
			<-ch

			return nil
		}})
		if err != nil {
			t.Fatalf("Start(%s's job %s) error = %v", owner, name, err)
		}

		return j
	}

	a1 := start("alice", "a1")
	a2 := start("alice", "a2")
	b1 := start("bob", "b1")
	c1 := start("carol", "c1")
	a3 := start("alice", "a3")

	got := map[string]Status{}
	for _, j := range []Job{a1, a2, b1, c1, a3} {
		got[j.Title] = j.Status
	}

	want := map[string]Status{
		"a1": StatusRunning, "a2": StatusQueued, "b1": StatusRunning, "c1": StatusQueued, "a3": StatusQueued,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Start() statuses by title mismatch (-want +got):\n%s", diff)
	}

	if _, err := r.Cancel(a3.ID); err != nil {
		t.Fatalf("Cancel(queued job) error = %v", err)
	}

	if j, _ := r.Get(a3.ID); j.Status != StatusCanceled {
		t.Errorf("canceled queued job Status = %s, want %s", j.Status, StatusCanceled)
	} else if j.Started != nil {
		t.Errorf("canceled queued job Started = %v, want nil", j.Started)
	}

	// alice's next job waits for alice's slot, carol's for the global one
	close(release["a1"])
	waitStatus(t, r, a2.ID, StatusRunning)

	if j, _ := r.Get(c1.ID); j.Status != StatusQueued {
		t.Errorf("c1 Status while two jobs run = %s, want %s", j.Status, StatusQueued)
	}

	close(release["b1"])
	waitStatus(t, r, c1.ID, StatusRunning)

	close(release["a2"])
	close(release["c1"])

	wait(t, r, c1.ID)
	wait(t, r, a2.ID)
}

func TestListFilters(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	r := newTestRegistry(t, WithClock(clock.Now))
	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	start := func(owner, exp, kind string, block bool) Job {
		t.Helper()

		j, err := r.Start(Spec{Kind: kind, Owner: owner, Experiment: exp, Run: func(context.Context, *Run) error {
			if block {
				<-release
			}

			return nil
		}})
		if err != nil {
			t.Fatalf("Start(%s %s job in %s) error = %v", owner, kind, exp, err)
		}

		clock.Advance(time.Second)

		return j
	}

	done := start("alice", "exp1", "vms.action", false)
	wait(t, r, done.ID)

	active := start("alice", "exp2", "files.archive", true)
	bobs := start("bob", "exp1", "vms.action", true)

	for name, tc := range map[string]struct {
		filter Filter
		want   []string
	}{
		"all, newest first": {Filter{}, []string{bobs.ID, active.ID, done.ID}},
		"owner":             {Filter{Owner: "alice"}, []string{active.ID, done.ID}},
		"experiment":        {Filter{Experiment: "exp1"}, []string{bobs.ID, done.ID}},
		"kind":              {Filter{Kind: "files.archive"}, []string{active.ID}},
		"active":            {Filter{Active: true}, []string{bobs.ID, active.ID}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := make([]string, 0, len(tc.want))
			for _, j := range r.List(tc.filter) {
				got = append(got, j.ID)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("List(%+v) IDs mismatch (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

// Finished jobs are forgotten after the retention, or beyond the most kept,
// and their artifacts deleted.
func TestRetention(t *testing.T) {
	t.Parallel()

	var (
		clock = newFakeClock()
		dir   = t.TempDir()
		n     = new(recorder)
		r     = newTestRegistry(t, WithClock(clock.Now), WithArtifactDir(dir), WithNotifier(n.notify), WithMaxFinished(2))
	)

	withArtifact := func(context.Context, *Run) error { return nil }

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: func(_ context.Context, run *Run) error {
		folder, err := run.Dir()
		if err != nil {
			return err
		}

		path := filepath.Join(folder, "out.zip")
		if err := os.WriteFile(path, []byte("zip"), 0o600); err != nil {
			return err
		}

		return run.SetArtifact(path, "out.zip", false)
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	j := wait(t, r, started.ID)

	wantArtifact := &Artifact{Name: "out.zip", Size: 3}
	if diff := cmp.Diff(wantArtifact, j.Artifact, cmpopts.IgnoreFields(Artifact{}, "Path")); diff != "" {
		t.Errorf("finished job Artifact mismatch (-want +got):\n%s", diff)
	}

	clock.Advance(DefaultRetention - time.Second)
	r.Sweep()

	if _, ok := r.Get(started.ID); !ok {
		t.Error("Get(job within its retention) ok = false, want true")
	}

	clock.Advance(time.Second)
	r.Sweep()

	if _, ok := r.Get(started.ID); ok {
		t.Error("Get(job past its retention) ok = true, want false")
	}

	if _, err := os.Stat(filepath.Join(dir, started.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("os.Stat(artifact folder of a forgotten job) error = %v, want %v", err, os.ErrNotExist)
	}

	// three finished jobs, two kept
	ids := make([]string, 0, 3)

	for range 3 {
		j, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: withArtifact})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		wait(t, r, j.ID)
		clock.Advance(time.Second)

		ids = append(ids, j.ID)
	}

	if _, ok := r.Get(ids[0]); ok {
		t.Error("Get(oldest of three finished jobs, two kept) ok = true, want false")
	}

	// sent once the finishing job's lock is released
	want := []string{started.ID, ids[0]}

	polltest.Until(t, fmt.Sprintf("removal notifications for %v", want), func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()

		return slices.Equal(n.gone, want)
	})
}

// Progress is sent at most every interval, while status changes and the
// final state always are; revisions only grow.
func TestNotificationsThrottleProgress(t *testing.T) {
	t.Parallel()

	var (
		n       = new(recorder)
		r       = newTestRegistry(t, WithNotifier(n.notify), WithNotifyInterval(time.Hour))
		release = make(chan struct{})
	)

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", BytesTotal: 1000, Run: func(_ context.Context, run *Run) error {
		for range 100 {
			run.AddBytes(10)
		}

		<-release

		return nil
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	close(release)

	j := wait(t, r, started.ID)
	if j.Progress.Bytes != 1000 {
		t.Errorf("finished job Progress.Bytes = %d, want 1000", j.Progress.Bytes)
	}

	got := n.of(started.ID)

	// running, at most one progress update within the hour, then succeeded
	if len(got) < 2 || len(got) > 3 {
		t.Fatalf("notifications = %d, want 2 or 3 (running, at most one progress, succeeded)", len(got))
	}

	if got[0].Status != StatusRunning {
		t.Errorf("first notification Status = %s, want %s", got[0].Status, StatusRunning)
	}

	last := got[len(got)-1]
	if last.Status != StatusSucceeded {
		t.Errorf("last notification Status = %s, want %s", last.Status, StatusSucceeded)
	}

	if last.Progress.Bytes != 1000 {
		t.Errorf("last notification Progress.Bytes = %d, want 1000", last.Progress.Bytes)
	}

	for i := 1; i < len(got); i++ {
		if got[i].Revision <= got[i-1].Revision {
			t.Errorf("notification Revision = %d after %d, want it to grow", got[i].Revision, got[i-1].Revision)
		}
	}
}

// A throttled progress update is sent once the interval passes.
func TestThrottledProgressIsSentLater(t *testing.T) {
	t.Parallel()

	var (
		n       = new(recorder)
		r       = newTestRegistry(t, WithNotifier(n.notify), WithNotifyInterval(20*time.Millisecond))
		release = make(chan struct{})
	)

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: func(_ context.Context, run *Run) error {
		run.AddBytes(1)
		run.AddBytes(1)
		<-release

		return nil
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	defer close(release)

	polltest.Until(t, "the held back progress update is sent", func() bool {
		got := n.of(started.ID)

		return len(got) > 0 && got[len(got)-1].Progress.Bytes == 2
	})
}

// Snapshots are copies: changing one changes nothing in the registry.
func TestSnapshotsAreCopies(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t, WithArtifactDir(t.TempDir()))

	started, err := r.Start(Spec{
		Kind: "test", Owner: "alice", Params: map[string]string{"action": "start"}, Items: []string{"a"},
		Run: func(ctx context.Context, run *Run) error {
			if err := outcomes(map[string]string{"a": "ok"}, nil)(ctx, run); err != nil {
				return err
			}

			folder, err := run.Dir()
			if err != nil {
				return err
			}

			path := filepath.Join(folder, "out.zip")
			if err := os.WriteFile(path, []byte("zip"), 0o600); err != nil {
				return err
			}

			return run.SetArtifact(path, "out.zip", false)
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	j := wait(t, r, started.ID)
	if j.Started == nil || j.Finished == nil || j.Artifact == nil {
		t.Fatalf("finished job: Started = %v, Finished = %v, Artifact = %v; want all set", j.Started, j.Finished, j.Artifact)
	}

	wantStarted, wantFinished := *j.Started, *j.Finished

	j.Items[0].Status = ItemFailed
	j.Items[0].Result[0] = 'x'
	j.Params["action"] = "stop"
	*j.Started = time.Time{}
	*j.Finished = time.Time{}
	j.Artifact.Name = "x"

	again, _ := r.Get(started.ID)

	if again.Items[0].Status != ItemSucceeded {
		t.Errorf("Get() after changing a snapshot: Items[0].Status = %s, want %s", again.Items[0].Status, ItemSucceeded)
	}

	if want := `{"name":"a"}`; string(again.Items[0].Result) != want {
		t.Errorf("Get() after changing a snapshot: Items[0].Result = %s, want %s", again.Items[0].Result, want)
	}

	if again.Params["action"] != "start" {
		t.Errorf("Get() after changing a snapshot: Params[action] = %q, want %q", again.Params["action"], "start")
	}

	if again.Started == nil || !again.Started.Equal(wantStarted) {
		t.Errorf("Get() after changing a snapshot: Started = %v, want %v", again.Started, wantStarted)
	}

	if again.Finished == nil || !again.Finished.Equal(wantFinished) {
		t.Errorf("Get() after changing a snapshot: Finished = %v, want %v", again.Finished, wantFinished)
	}

	if again.Artifact == nil || again.Artifact.Name != "out.zip" {
		t.Errorf("Get() after changing a snapshot: Artifact = %v, want name %q", again.Artifact, "out.zip")
	}

	listed := r.List(Filter{})
	listed[0].Items[0].Name = "b"

	if again, _ := r.Get(started.ID); again.Items[0].Name != "a" {
		t.Errorf("Get() after changing a listed job: Items[0].Name = %q, want %q", again.Items[0].Name, "a")
	}
}

// Items reported from many goroutines at once are all counted (run with
// -race).
func TestConcurrentItems(t *testing.T) {
	t.Parallel()

	n := new(recorder)
	r := newTestRegistry(t, WithNotifier(n.notify), WithNotifyInterval(time.Millisecond))

	names := make([]string, 200)
	for i := range names {
		names[i] = fmt.Sprintf("vm%d", i)
	}

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Items: names, Run: func(_ context.Context, run *Run) error {
		var wg sync.WaitGroup

		for i, name := range names {
			wg.Add(1)

			go func() {
				defer wg.Done()

				run.ItemStarted(name)
				run.AddBytes(1)

				if i%2 == 0 {
					run.ItemSucceeded(name, nil)
				} else {
					run.ItemFailed(name, errors.New("no"))
				}

				_ = r.List(Filter{})
			}()
		}

		wg.Wait()

		return nil
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	j := wait(t, r, started.ID)

	type outcome struct {
		Succeeded, Failed int
		Bytes             int64
		Status            Status
	}

	succeeded, failed, _, _ := j.Counts()
	got := outcome{Succeeded: succeeded, Failed: failed, Bytes: j.Progress.Bytes, Status: j.Status}
	want := outcome{Succeeded: 100, Failed: 100, Bytes: 200, Status: StatusPartial}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("finished job outcome mismatch (-want +got):\n%s", diff)
	}
}

// A panicking job fails rather than taking the server down.
func TestPanicFailsTheJob(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: func(context.Context, *Run) error {
		panic("boom")
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	j := wait(t, r, started.ID)

	if j.Status != StatusFailed {
		t.Errorf("panicking job Status = %s, want %s", j.Status, StatusFailed)
	}

	if j.Error == "" {
		t.Error(`panicking job Error = "", want the panic`)
	}
}

// Only folders named like jobs the registry does not hold are removed.
func TestRemoveStaleArtifacts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := newTestRegistry(t, WithArtifactDir(dir))
	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	held, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: func(_ context.Context, run *Run) error {
		if _, err := run.Dir(); err != nil {
			return err
		}

		<-release

		return nil
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	polltest.Until(t, "the running job's folder exists", func() bool {
		_, err := os.Stat(filepath.Join(dir, held.ID))

		return err == nil
	})

	for _, name := range []string{"j0123456789abcdef", "jABCDEF0123456789", "notes"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o750); err != nil {
			t.Fatalf("os.Mkdir(%s) error = %v", name, err)
		}
	}

	removed, err := r.RemoveStaleArtifacts()
	if err != nil {
		t.Errorf("RemoveStaleArtifacts() error = %v, want nil", err)
	}

	if removed != 1 {
		t.Errorf("RemoveStaleArtifacts() = %d, want 1", removed)
	}

	want := map[string]bool{held.ID: true, "j0123456789abcdef": false, "jABCDEF0123456789": true, "notes": true}
	got := make(map[string]bool, len(want))

	for name := range want {
		_, err := os.Stat(filepath.Join(dir, name))
		got[name] = err == nil
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RemoveStaleArtifacts() folders kept mismatch (-want +got):\n%s", diff)
	}
}

// A job that would have to wait is refused once its owner, or everyone, has the most jobs waiting; one that
// can run now is not.
func TestQueueLimitsRefuseJobs(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t, WithLimits(1, 2), WithQueueLimits(1, 2))
	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	start := func(owner string) (Job, error) {
		return r.Start(Spec{Kind: "test", Owner: owner, Cancelable: true, Run: func(ctx context.Context, _ *Run) error {
			select {
			case <-release:
			case <-ctx.Done():
			}

			return nil
		}})
	}

	// in order: each job counts against the limits of those after it
	for _, tc := range []struct {
		desc  string
		owner string
		want  Status
	}{
		{"alice's first job", "alice", StatusRunning},
		{"alice's second job", "alice", StatusQueued},
		{"alice's third job, one of hers waiting", "alice", ""},
		{"bob's first job", "bob", StatusRunning},
		{"bob's second job", "bob", StatusQueued},
		{"carol's job, two waiting", "carol", ""},
	} {
		j, err := start(tc.owner)

		switch {
		case tc.want == "" && !errors.Is(err, ErrQueueFull):
			t.Errorf("Start(%s) error = %v, want %v", tc.desc, err, ErrQueueFull)
		case tc.want != "" && err != nil:
			t.Errorf("Start(%s) error = %v, want nil", tc.desc, err)
		case tc.want != "" && j.Status != tc.want:
			t.Errorf("Start(%s) Status = %s, want %s", tc.desc, j.Status, tc.want)
		}
	}
}

// registryStepClock is a clock one nanosecond later at every reading, so the
// registry's times order its jobs as they started and finished.
type registryStepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *registryStepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(time.Nanosecond)

	return c.now
}

// registryModelJob is what the registry should hold of a job registryMachine started.
type registryModelJob struct {
	owner      string
	cancelable bool
	status     Status
	release    chan error
}

// registryMachine starts, finishes, cancels and removes jobs in a registry
// with small limits, and keeps a model of each job's status.
type registryMachine struct {
	r *Registry

	perOwner, global, queuedPerOwner, queuedGlobal, maxFinished int

	jobs map[string]*registryModelJob
	// every job started, oldest first; the queued ones; the finished ones kept, in the order they finished
	started, queue, finished []string
}

func (m *registryMachine) count(owner string, status Status) int {
	var n int

	for _, j := range m.jobs {
		if j.status == status && (owner == "" || j.owner == owner) {
			n++
		}
	}

	return n
}

func (m *registryMachine) canStart(owner string) bool {
	return m.count("", StatusRunning) < m.global && m.count(owner, StatusRunning) < m.perOwner
}

// finish ends the job in status, starts the queued jobs the limits now allow,
// oldest first, and forgets the jobs that finished first beyond maxFinished.
func (m *registryMachine) finish(id string, status Status) {
	m.jobs[id].status = status
	m.queue = slices.DeleteFunc(m.queue, func(q string) bool { return q == id })
	m.finished = append(m.finished, id)

	m.queue = slices.DeleteFunc(m.queue, func(q string) bool {
		if !m.canStart(m.jobs[q].owner) {
			return false
		}

		m.jobs[q].status = StatusRunning

		return true
	})

	for len(m.finished) > m.maxFinished {
		delete(m.jobs, m.finished[0])
		m.finished = m.finished[1:]
	}
}

// awaitEnd waits for the registry to finish the job, or forget it.
func (m *registryMachine) awaitEnd(t *rapid.T, id string) {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); runtime.Gosched() {
		if j, ok := m.r.Get(id); !ok || j.Status.Terminal() {
			return
		}
	}

	t.Fatalf("job %s still active 5s after its work was told to end", id)
}

func (m *registryMachine) start(t *rapid.T) {
	owner := rapid.SampledFrom([]string{"alice", "bob", "carol"}).Draw(t, "owner")
	cancelable := rapid.Bool().Draw(t, "cancelable")
	release := make(chan error, 1)

	j, err := m.r.Start(Spec{Owner: owner, Cancelable: cancelable, Run: func(ctx context.Context, _ *Run) error {
		select {
		case err := <-release:
			return err
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // the registry tells cancellation apart
		}
	}})

	queued := 0
	for _, q := range m.queue {
		if m.jobs[q].owner == owner {
			queued++
		}
	}

	want := StatusRunning

	switch {
	case m.canStart(owner):
	case len(m.queue) < m.queuedGlobal && queued < m.queuedPerOwner:
		want = StatusQueued
	default:
		if !errors.Is(err, ErrQueueFull) {
			t.Fatalf("Start(%s's job) = %s, %v; want %v", owner, j.Status, err, ErrQueueFull)
		}

		return
	}

	if err != nil || j.Status != want {
		t.Fatalf("Start(%s's job) = %s, %v; want %s, nil", owner, j.Status, err, want)
	}

	m.jobs[j.ID] = &registryModelJob{owner: owner, cancelable: cancelable, status: want, release: release}
	m.started = append(m.started, j.ID)

	if want == StatusQueued {
		m.queue = append(m.queue, j.ID)
	}
}

// end lets a running job's work return, with an error or not.
func (m *registryMachine) end(t *rapid.T) {
	var running []string

	for _, id := range m.started {
		if j, ok := m.jobs[id]; ok && j.status == StatusRunning {
			running = append(running, id)
		}
	}

	if len(running) == 0 {
		t.Skip("no job running")
	}

	id := rapid.SampledFrom(running).Draw(t, "running job")
	status := StatusSucceeded

	var err error
	if rapid.Bool().Draw(t, "fails") {
		status, err = StatusFailed, errors.New("broke")
	}

	m.jobs[id].release <- err
	m.awaitEnd(t, id)
	m.finish(id, status)
}

func (m *registryMachine) cancel(t *rapid.T) {
	if len(m.started) == 0 {
		t.Skip("no job started")
	}

	id := rapid.SampledFrom(m.started).Draw(t, "job")
	j, ok := m.jobs[id]

	var want error

	switch {
	case !ok:
		want = ErrNotFound
	case j.status.Terminal():
		want = ErrFinished
	case !j.cancelable:
		want = ErrNotCancelable
	}

	if _, err := m.r.Cancel(id); !errors.Is(err, want) {
		t.Fatalf("Cancel(%s) error = %v, want %v", id, err, want)
	}

	if want == nil {
		m.awaitEnd(t, id)
		m.finish(id, StatusCanceled)
	}
}

func (m *registryMachine) remove(t *rapid.T) {
	if len(m.started) == 0 {
		t.Skip("no job started")
	}

	id := rapid.SampledFrom(m.started).Draw(t, "job")
	j, ok := m.jobs[id]

	var want error

	switch {
	case !ok:
		want = ErrNotFound
	case !j.status.Terminal():
		want = ErrActive
	}

	if err := m.r.Remove(id); !errors.Is(err, want) {
		t.Fatalf("Remove(%s) error = %v, want %v", id, err, want)
	}

	if want == nil {
		delete(m.jobs, id)
		m.finished = slices.DeleteFunc(m.finished, func(f string) bool { return f == id })
	}
}

// check compares every job the registry holds, newest first, with the model.
func (m *registryMachine) check(t *rapid.T) {
	var want []string

	for _, id := range slices.Backward(m.started) {
		if j, ok := m.jobs[id]; ok {
			want = append(want, id+" "+string(j.status))
		}
	}

	jobs := m.r.List(Filter{})
	got := make([]string, 0, len(jobs))

	for _, j := range jobs {
		got = append(got, j.ID+" "+string(j.Status))
	}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("List() mismatch (-want +got):\n%s", diff)
	}
}

// Jobs run as soon as the limits allow, oldest first, are refused once the
// queue is full, and are forgotten once more than the most kept have finished.
func TestRegistryProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		m := &registryMachine{
			perOwner:       rapid.IntRange(1, 2).Draw(t, "per owner"),
			global:         rapid.IntRange(1, 3).Draw(t, "global"),
			queuedPerOwner: rapid.IntRange(0, 2).Draw(t, "queued per owner"),
			queuedGlobal:   rapid.IntRange(0, 3).Draw(t, "queued global"),
			maxFinished:    rapid.IntRange(0, 3).Draw(t, "max finished"),
			jobs:           make(map[string]*registryModelJob),
		}

		clock := &registryStepClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		m.r = NewRegistry(
			WithLimits(m.perOwner, m.global), WithQueueLimits(m.queuedPerOwner, m.queuedGlobal),
			WithMaxFinished(m.maxFinished), WithClock(clock.Now),
		)

		defer m.r.ShutdownContext(context.Background(), 0)

		t.Repeat(map[string]func(*rapid.T){
			"start":  m.start,
			"end":    m.end,
			"cancel": m.cancel,
			"remove": m.remove,
			"":       m.check,
		})
	})
}

// registrySettings are the fields of a Registry that its options set.
type registrySettings struct {
	MaxFinished, PerOwner, Global, QueuedPerOwner, QueuedGlobal int
	Retention, Interval                                         time.Duration
	ArtifactDir                                                 string
	// whether a notifier is set, and the time the clock reads
	Notify bool
	Now    time.Time
}

func registrySettingsOf(r *Registry) registrySettings {
	r.mu.Lock()
	defer r.mu.Unlock()

	return registrySettings{
		MaxFinished: r.maxFinished, PerOwner: r.perOwner, Global: r.global,
		QueuedPerOwner: r.queuedPerOwner, QueuedGlobal: r.queuedGlobal,
		Retention: r.retention, Interval: r.interval, ArtifactDir: r.artifactDir,
		Notify: r.notify != nil, Now: r.now(),
	}
}

// NewRegistry starts from the defaults, and each option, given to NewRegistry
// or later to Configure, changes only its own setting.
func TestRegistryOptions(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	defaults := registrySettings{
		MaxFinished: DefaultMaxFinished, PerOwner: DefaultPerOwner, Global: DefaultGlobal,
		QueuedPerOwner: DefaultQueuedPerOwner, QueuedGlobal: DefaultQueuedGlobal,
		Retention: DefaultRetention, Interval: NotifyInterval, Now: clock.Now(),
	}

	for name, tc := range map[string]struct {
		opts   []Option
		change func(*registrySettings)
	}{
		"none": {change: func(*registrySettings) {}},
		"notifier": {
			opts:   []Option{WithNotifier(func(Job, bool) {})},
			change: func(s *registrySettings) { s.Notify = true },
		},
		"max finished": {opts: []Option{WithMaxFinished(3)}, change: func(s *registrySettings) { s.MaxFinished = 3 }},
		"limits": {
			opts:   []Option{WithLimits(1, 2)},
			change: func(s *registrySettings) { s.PerOwner, s.Global = 1, 2 },
		},
		"queue limits": {
			opts:   []Option{WithQueueLimits(0, 5)},
			change: func(s *registrySettings) { s.QueuedPerOwner, s.QueuedGlobal = 0, 5 },
		},
		"artifact dir": {opts: []Option{WithArtifactDir("/jobs")}, change: func(s *registrySettings) { s.ArtifactDir = "/jobs" }},
		"notify interval": {
			opts:   []Option{WithNotifyInterval(time.Second)},
			change: func(s *registrySettings) { s.Interval = time.Second },
		},
		"the last of two wins": {
			opts:   []Option{WithMaxFinished(3), WithMaxFinished(0)},
			change: func(s *registrySettings) { s.MaxFinished = 0 },
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := defaults
			tc.change(&want)

			created := NewRegistry(append([]Option{WithClock(clock.Now)}, tc.opts...)...)
			if diff := cmp.Diff(want, registrySettingsOf(created)); diff != "" {
				t.Errorf("NewRegistry(%s) settings mismatch (-want +got):\n%s", name, diff)
			}

			configured := NewRegistry(WithClock(clock.Now))
			configured.Configure(tc.opts...)

			if diff := cmp.Diff(want, registrySettingsOf(configured)); diff != "" {
				t.Errorf("Configure(%s) settings mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// Limits given to Configure apply to the jobs started after it.
func TestConfigureChangesLimits(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)
	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	r.Configure(WithLimits(1, 1), WithQueueLimits(1, 1))

	var got []string

	for range 3 {
		j, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: func(context.Context, *Run) error {
			<-release

			return nil
		}})
		if err != nil {
			got = append(got, err.Error())

			continue
		}

		got = append(got, string(j.Status))
	}

	want := []string{string(StatusRunning), string(StatusQueued), ErrQueueFull.Error()}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Start() three times after Configure(limits 1, 1) mismatch (-want +got):\n%s", diff)
	}
}

func TestStartWithoutWork(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)

	if j, err := r.Start(Spec{Kind: "test", Owner: "alice"}); err == nil {
		t.Errorf("Start(no Run) = %+v, nil; want an error", j)
	}

	if jobs := r.List(Filter{}); len(jobs) != 0 {
		t.Errorf("List() after Start(no Run) = %d jobs, want none", len(jobs))
	}
}

// Jobs created at the same time list newest first, and the oldest of jobs
// that finished at the same time is the first forgotten.
func TestJobsAtTheSameTimeOrderByStart(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	r := newTestRegistry(t, WithClock(clock.Now), WithMaxFinished(2))

	ids := make([]string, 0, 3)

	for range 3 {
		j, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: outcomes(nil, nil)})
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		wait(t, r, j.ID)

		ids = append(ids, j.ID)
	}

	got := make([]string, 0, 2)
	for _, j := range r.List(Filter{}) {
		got = append(got, j.ID)
	}

	if diff := cmp.Diff([]string{ids[2], ids[1]}, got); diff != "" {
		t.Errorf("List() of three jobs at the same time, two kept, mismatch (-want +got):\n%s", diff)
	}
}

// Many owners starting jobs at once never run more than the limits allow,
// and every job runs in the end (run with -race).
func TestConcurrentStartsKeepTheLimits(t *testing.T) {
	t.Parallel()

	const perOwner, global = 2, 3

	var (
		mu                sync.Mutex
		running, peak     int
		runningBy, peakBy = map[string]int{}, map[string]int{}
		release           = make(chan struct{})
	)

	r := newTestRegistry(t, WithLimits(perOwner, global))

	work := func(owner string) RunFunc {
		return func(context.Context, *Run) error {
			mu.Lock()
			running++
			runningBy[owner]++
			peak, peakBy[owner] = max(peak, running), max(peakBy[owner], runningBy[owner])
			mu.Unlock()

			<-release

			mu.Lock()
			running--
			runningBy[owner]--
			mu.Unlock()

			return nil
		}
	}

	var (
		wg  sync.WaitGroup
		ids = make(chan string, 30)
	)

	for i := range 30 {
		owner := []string{"alice", "bob", "carol"}[i%3]

		wg.Add(1)

		go func() {
			defer wg.Done()

			j, err := r.Start(Spec{Kind: "test", Owner: owner, Run: work(owner)})
			if err != nil {
				t.Errorf("Start(%s's job) error = %v", owner, err)

				return
			}

			ids <- j.ID
		}()
	}

	wg.Wait()
	close(ids)

	if active := r.List(Filter{Active: true}); len(active) != 30 {
		t.Errorf("List(active) after 30 starts = %d jobs, want 30", len(active))
	}

	close(release)

	for id := range ids {
		if j := wait(t, r, id); j.Status != StatusSucceeded {
			t.Errorf("job %s Status = %s, want %s", id, j.Status, StatusSucceeded)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if peak > global {
		t.Errorf("most jobs running at once = %d, want at most %d", peak, global)
	}

	for owner, n := range peakBy {
		if n > perOwner {
			t.Errorf("most of %s's jobs running at once = %d, want at most %d", owner, n, perOwner)
		}
	}
}

// RemoveStaleArtifacts does nothing without an artifact folder, and reports
// a folder it cannot read and a stale folder it cannot delete.
func TestRemoveStaleArtifactsErrors(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		dir string
		// a read-only folder holding a stale folder, in place of dir
		readOnly bool
		wantErr  bool
	}{
		"none configured": {},
		"missing":         {dir: filepath.Join(t.TempDir(), "missing")},
		"a file":          {dir: file, wantErr: true},
		"read-only":       {readOnly: true, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := tc.dir

			if tc.readOnly {
				if os.Geteuid() == 0 {
					t.Skip("root deletes from a read-only folder")
				}

				dir = t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, "j0123456789abcdef"), 0o750); err != nil {
					t.Fatal(err)
				}

				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}

				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			}

			r := newTestRegistry(t, WithArtifactDir(dir))

			removed, err := r.RemoveStaleArtifacts()
			if removed != 0 || (err != nil) != tc.wantErr {
				t.Errorf("RemoveStaleArtifacts() in %s folder = %d, %v; want 0, error %t", name, removed, err, tc.wantErr)
			}
		})
	}
}

// removeArtifact deletes a job's file and its folder, and only warns about
// what it cannot delete.
func TestRemoveArtifact(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		// the artifact path, relative to the artifact folder, if any
		artifact string
		// whether the artifact is a folder that is not empty
		nonEmpty bool
		noDir    bool
		// whether the artifact folder is read-only, so its job folders stay
		readOnly bool
		// what is left, relative to the artifact folder
		want []string
	}{
		"file and folder":    {artifact: "j1/out.zip", want: []string{}},
		"no artifact":        {want: []string{}},
		"artifact gone":      {artifact: "j1/gone.zip", want: []string{}},
		"no artifact folder": {artifact: "j1/out.zip", noDir: true, want: []string{"j1"}},
		"job folder kept":    {readOnly: true, want: []string{"j1"}},
		"artifact not a file": {
			artifact: "other/out.zip", nonEmpty: true, want: []string{"other", "other/out.zip", "other/out.zip/x"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "j1"), 0o750); err != nil {
				t.Fatal(err)
			}

			j := Job{ID: "j1"}

			if tc.artifact != "" {
				path := filepath.Join(dir, tc.artifact)
				j.Artifact = &Artifact{Name: "out.zip", Path: path}

				switch {
				case tc.nonEmpty:
					if err := os.MkdirAll(filepath.Join(path, "x"), 0o750); err != nil {
						t.Fatal(err)
					}
				case filepath.Base(path) != "gone.zip":
					if err := os.WriteFile(path, []byte("zip"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}

			if tc.readOnly {
				if os.Geteuid() == 0 {
					t.Skip("root deletes from a read-only folder")
				}

				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}

				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			}

			folder := dir
			if tc.noDir {
				folder = ""
			}

			removeArtifact(folder, j)

			got := []string{}

			_ = filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
				if rel, _ := filepath.Rel(dir, path); err == nil && rel != "." {
					got = append(got, filepath.ToSlash(rel))
				}

				return nil
			})

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("removeArtifact(%s) left mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// forceCancel leaves alone a job the registry does not hold or that has
// finished.
func TestForceCancelSkipsFinishedJobs(t *testing.T) {
	t.Parallel()

	r := newTestRegistry(t)

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: outcomes(nil, nil)})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	before := wait(t, r, started.ID)

	r.forceCancel(started.ID)
	r.forceCancel("jnope")

	after, _ := r.Get(started.ID)
	if diff := cmp.Diff(before, after); diff != "" {
		t.Errorf("forceCancel(finished job) changed it (-before +after):\n%s", diff)
	}
}

// registryNotifications waits for want notifications of the job, and returns
// them by revision, since they may arrive out of order.
func registryNotifications(t *testing.T, n *recorder, id string, want int) []Job {
	t.Helper()

	var jobs []Job

	polltest.Until(t, fmt.Sprintf("%d notifications of job %s", want, id), func() bool {
		jobs = n.of(id)

		return len(jobs) >= want
	})

	slices.SortFunc(jobs, func(a, b Job) int {
		switch {
		case a.Revision < b.Revision:
			return -1
		case a.Revision > b.Revision:
			return 1
		default:
			return 0
		}
	})

	return jobs
}

// Without a throttle, every progress change is sent at once.
func TestProgressWithoutThrottleIsSentAtOnce(t *testing.T) {
	t.Parallel()

	n := new(recorder)
	r := newTestRegistry(t, WithNotifier(n.notify), WithNotifyInterval(0))

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: func(_ context.Context, run *Run) error {
		for range 3 {
			run.AddBytes(1)
		}

		return nil
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	wait(t, r, started.ID)

	// running, three progress updates, then succeeded
	jobs := registryNotifications(t, n, started.ID, 5)

	got := make([]int64, 0, len(jobs))
	for _, j := range jobs {
		got = append(got, j.Progress.Bytes)
	}

	if diff := cmp.Diff([]int64{0, 1, 2, 3, 3}, got); diff != "" {
		t.Errorf("notified Progress.Bytes mismatch (-want +got):\n%s", diff)
	}
}

// A change sent at once, such as a cancel, carries the progress the throttle
// held back and replaces its later notification.
func TestCancelSendsHeldBackProgress(t *testing.T) {
	t.Parallel()

	n := new(recorder)
	r := newTestRegistry(t, WithNotifier(n.notify), WithNotifyInterval(time.Hour))
	reported := make(chan struct{})

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Cancelable: true, Run: func(ctx context.Context, run *Run) error {
		run.AddBytes(1)
		run.AddBytes(1)
		close(reported)
		<-ctx.Done()

		return ctx.Err()
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-reported

	if _, err := r.Cancel(started.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	j := wait(t, r, started.ID)

	type note struct {
		Status          Status
		CancelRequested bool
		Bytes           int64
	}

	jobs := registryNotifications(t, n, started.ID, 3)

	got := make([]note, 0, len(jobs))
	for _, j := range jobs {
		got = append(got, note{j.Status, j.CancelRequested, j.Progress.Bytes})
	}

	want := []note{{StatusRunning, false, 0}, {StatusRunning, true, 2}, {StatusCanceled, true, 2}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("notifications of a canceled job mismatch (-want +got):\n%s", diff)
	}

	if j.Status != StatusCanceled {
		t.Errorf("canceled job Status = %s, want %s", j.Status, StatusCanceled)
	}
}

// flush sends nothing once the throttle has nothing to send or the job has
// finished.
func TestFlushSendsNothingWhenNothingIsHeldBack(t *testing.T) {
	t.Parallel()

	n := new(recorder)
	r := newTestRegistry(t, WithNotifier(n.notify))

	started, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: outcomes(nil, nil)})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	wait(t, r, started.ID)

	// running, then succeeded
	before := len(registryNotifications(t, n, started.ID, 2))

	r.mu.Lock()
	e := r.jobs[started.ID]
	r.mu.Unlock()

	r.flush(e)

	r.mu.Lock()
	e.timer = time.NewTimer(time.Hour)
	r.mu.Unlock()

	r.flush(e)

	r.mu.Lock()
	e.timer.Stop()
	r.mu.Unlock()

	if after := len(n.of(started.ID)); after != before {
		t.Errorf("flush() of a finished job sent %d notifications, want none", after-before)
	}
}

func TestCountItems(t *testing.T) {
	t.Parallel()

	items := []Item{
		{Status: ItemPending}, {Status: ItemRunning}, {Status: ItemSucceeded}, {Status: ItemSucceeded},
		{Status: ItemFailed}, {Status: ItemSkipped}, {Status: ItemCanceled},
	}

	for name, tc := range map[string]struct {
		items []Item
		count func([]Item) int
		want  int
	}{
		"succeeded":      {items, func(i []Item) int { return countStatus(i, ItemSucceeded) }, 2},
		"pending":        {items, func(i []Item) int { return countStatus(i, ItemPending) }, 1},
		"terminal":       {items, countTerminal, 5},
		"none, status":   {nil, func(i []Item) int { return countStatus(i, ItemFailed) }, 0},
		"none, terminal": {nil, countTerminal, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.count(tc.items); got != tc.want {
				t.Errorf("count of %s items = %d, want %d", name, got, tc.want)
			}
		})
	}
}

// finalStatus of a job whose cancel was asked for is canceled, whatever its
// items and work returned.
func TestFinalStatusOfJob(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		job    Job
		err    error
		status Status
		jobErr string
	}{
		"cancel asked, with an error": {
			job: Job{CancelRequested: true, Items: []Item{{Status: ItemSucceeded}}}, err: errors.New("x"),
			status: StatusCanceled,
		},
		"every item skipped": {
			job:    Job{Items: []Item{{Status: ItemSkipped}, {Status: ItemSkipped}}},
			status: StatusFailed, jobErr: "every item failed",
		},
		"items succeeded, then an error": {
			job: Job{Items: []Item{{Status: ItemSucceeded}}}, err: errors.New("late"),
			status: StatusPartial, jobErr: "late",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			status, jobErr := finalStatus(tc.job, tc.err)
			if status != tc.status || jobErr != tc.jobErr {
				t.Errorf("finalStatus(%s) = %s, %q; want %s, %q", name, status, jobErr, tc.status, tc.jobErr)
			}
		})
	}
}

// Job IDs are "j" and 16 hex digits, as artifact folders are named, and
// differ.
func TestNewID(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 1000)

	for range 1000 {
		id, err := newID()
		if err != nil {
			t.Fatalf("newID() error = %v", err)
		}

		if !artifactDirName.MatchString(id) {
			t.Fatalf("newID() = %q, want j and 16 hex digits", id)
		}

		if seen[id] {
			t.Fatalf("newID() = %q twice", id)
		}

		seen[id] = true
	}
}
