package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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
	t.Cleanup(r.Shutdown)

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
			run.ItemSkipped("c", "you don't have permission")
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
		{Name: "c", Status: ItemSkipped, Error: "you don't have permission"},
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

	r := newTestRegistry(t)

	started, err := r.Start(Spec{
		Kind: "test", Owner: "alice", Params: map[string]string{"action": "start"}, Items: []string{"a"},
		Run: outcomes(map[string]string{"a": "ok"}, nil),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	j := wait(t, r, started.ID)

	j.Items[0].Status = ItemFailed
	j.Items[0].Result[0] = 'x'
	j.Params["action"] = "stop"
	j.Finished = nil

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

	if again.Finished == nil {
		t.Error("Get() after changing a snapshot: Finished = nil, want set")
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
