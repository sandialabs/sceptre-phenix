package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		j, ok := r.Get(id)
		if !ok {
			t.Fatalf("job %s is gone", id)
		}

		if j.Status.Terminal() {
			return j
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatalf("job %s did not finish", id)

	return Job{}
}

// waitStatus waits for the job to reach status.
func waitStatus(t *testing.T, r *Registry, id string, status Status) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if j, _ := r.Get(id); j.Status == status {
			return
		}

		time.Sleep(time.Millisecond)
	}

	j, _ := r.Get(id)
	t.Fatalf("job %s is %s, want %s", id, j.Status, status)
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
			item:    map[string]ItemStatus{"b": ItemSkipped},
		},
		"all fail": {
			items:   []string{"a", "b"},
			results: map[string]string{"a": "fail", "b": "fail"},
			status:  StatusFailed,
			jobErr:  "every item failed",
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
				t.Fatal(err)
			}

			j := wait(t, r, started.ID)

			if j.Status != tc.status || j.Error != tc.jobErr {
				t.Errorf("job is %s (%q), want %s (%q)", j.Status, j.Error, tc.status, tc.jobErr)
			}

			if j.Progress.Done != j.Progress.Total {
				t.Errorf("progress %d/%d, want all done", j.Progress.Done, j.Progress.Total)
			}

			if j.Finished == nil || j.Started == nil {
				t.Error("finished job lacks its start or finish time")
			}

			for _, item := range j.Items {
				if want, ok := tc.item[item.Name]; ok && item.Status != want {
					t.Errorf("item %s is %s, want %s", item.Name, item.Status, want)
				}
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
			run.ItemFailed("b", fmt.Errorf("locked: %w", ErrRetryable))
			run.ItemSkipped("c", "you don't have permission")
			run.ItemSucceeded("unknown", nil)

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(started.Items) != 3 || started.Progress.Total != 3 {
		t.Fatalf("items %v (total %d), want a, b and c once each", started.Items, started.Progress.Total)
	}

	j := wait(t, r, started.ID)

	want := []Item{
		{Name: "a", Status: ItemSucceeded, Result: []byte(`{"name":"a"}`)},
		{Name: "b", Status: ItemFailed, Error: "locked: temporary failure", Retryable: true},
		{Name: "c", Status: ItemSkipped, Error: "you don't have permission"},
	}

	for i, item := range j.Items {
		w := want[i]
		if item.Name != w.Name || item.Status != w.Status || item.Error != w.Error ||
			item.Retryable != w.Retryable || string(item.Result) != string(w.Result) {
			t.Errorf("item %d = %+v, want %+v", i, item, w)
		}
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
		t.Fatal(err)
	}

	<-first

	canceling, err := r.Cancel(started.ID)
	if err != nil {
		t.Fatal(err)
	}

	if !canceling.CancelRequested || canceling.Status != StatusRunning {
		t.Errorf("canceling job is %s, cancel requested %t", canceling.Status, canceling.CancelRequested)
	}

	close(release)

	j := wait(t, r, started.ID)

	if j.Status != StatusCanceled {
		t.Errorf("status %s, want canceled", j.Status)
	}

	for name, want := range map[int]ItemStatus{0: ItemSucceeded, 1: ItemCanceled, 2: ItemCanceled} {
		if j.Items[name].Status != want {
			t.Errorf("item %s is %s, want %s", j.Items[name].Name, j.Items[name].Status, want)
		}
	}

	if _, err := r.Cancel(started.ID); !errors.Is(err, ErrFinished) {
		t.Errorf("canceling a finished job: %v, want ErrFinished", err)
	}

	if _, err := r.Cancel("jnope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("canceling an unknown job: %v, want ErrNotFound", err)
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
		t.Fatal(err)
	}

	if _, err := r.Cancel(started.ID); !errors.Is(err, ErrNotCancelable) {
		t.Errorf("Cancel = %v, want ErrNotCancelable", err)
	}

	if err := r.Remove(started.ID); !errors.Is(err, ErrActive) {
		t.Errorf("removing a running job: %v, want ErrActive", err)
	}

	close(release)

	if j := wait(t, r, started.ID); j.Status != StatusSucceeded {
		t.Errorf("status %s, want succeeded", j.Status)
	}

	if err := r.Remove(started.ID); err != nil {
		t.Errorf("removing a finished job: %v", err)
	}

	if _, ok := r.Get(started.ID); ok {
		t.Error("removed job is still listed")
	}

	if err := r.Remove(started.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing it again: %v, want ErrNotFound", err)
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
			t.Fatal(err)
		}

		return j
	}

	a1 := start("alice", "a1")
	a2 := start("alice", "a2")
	b1 := start("bob", "b1")
	c1 := start("carol", "c1")
	a3 := start("alice", "a3")

	for j, want := range map[*Job]Status{&a1: StatusRunning, &a2: StatusQueued, &b1: StatusRunning, &c1: StatusQueued, &a3: StatusQueued} {
		if j.Status != want {
			t.Errorf("%s started %s, want %s", j.Title, j.Status, want)
		}
	}

	if s := r.Stats(); s.Running != 2 || s.Queued != 3 {
		t.Errorf("stats %+v, want 2 running and 3 queued", s)
	}

	if _, err := r.Cancel(a3.ID); err != nil {
		t.Fatal(err)
	}

	if j, _ := r.Get(a3.ID); j.Status != StatusCanceled || j.Started != nil {
		t.Errorf("canceled queued job is %s, started %v", j.Status, j.Started)
	}

	// alice's next job waits for alice's slot, carol's for the global one
	close(release["a1"])
	waitStatus(t, r, a2.ID, StatusRunning)

	if j, _ := r.Get(c1.ID); j.Status != StatusQueued {
		t.Errorf("c1 is %s while two jobs run, want queued", j.Status)
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
			t.Fatal(err)
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
		got := make([]string, 0, len(tc.want))
		for _, j := range r.List(tc.filter) {
			got = append(got, j.ID)
		}

		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: listed %v, want %v", name, got, tc.want)
		}
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
		t.Fatal(err)
	}

	j := wait(t, r, started.ID)
	if j.Artifact == nil || j.Artifact.Size != 3 || j.Artifact.Name != "out.zip" {
		t.Fatalf("artifact %+v, want out.zip of 3 bytes", j.Artifact)
	}

	clock.Advance(DefaultRetention - time.Second)
	r.Sweep()

	if _, ok := r.Get(started.ID); !ok {
		t.Fatal("job forgotten before its retention ended")
	}

	clock.Advance(time.Second)
	r.Sweep()

	if _, ok := r.Get(started.ID); ok {
		t.Fatal("job kept after its retention")
	}

	if _, err := os.Stat(filepath.Join(dir, started.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("artifact folder left behind: %v", err)
	}

	// three finished jobs, two kept
	ids := make([]string, 0, 3)

	for range 3 {
		j, err := r.Start(Spec{Kind: "test", Owner: "alice", Run: withArtifact})
		if err != nil {
			t.Fatal(err)
		}

		wait(t, r, j.ID)
		clock.Advance(time.Second)

		ids = append(ids, j.ID)
	}

	if _, ok := r.Get(ids[0]); ok {
		t.Error("oldest finished job kept beyond the most kept")
	}

	// sent once the finishing job's lock is released
	want := fmt.Sprint([]string{started.ID, ids[0]})
	deadline := time.Now().Add(5 * time.Second)

	for {
		n.mu.Lock()
		got := fmt.Sprint(n.gone)
		n.mu.Unlock()

		if got == want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("removal notifications for %v, want %v", got, want)
		}

		time.Sleep(time.Millisecond)
	}
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
		t.Fatal(err)
	}

	close(release)

	j := wait(t, r, started.ID)
	if j.Progress.Bytes != 1000 {
		t.Errorf("bytes %d, want 1000", j.Progress.Bytes)
	}

	got := n.of(started.ID)

	// running, at most one progress update within the hour, then succeeded
	if len(got) < 2 || len(got) > 3 {
		t.Fatalf("%d notifications, want running, at most one progress and succeeded", len(got))
	}

	if got[0].Status != StatusRunning || got[len(got)-1].Status != StatusSucceeded {
		t.Errorf("first notification %s, last %s", got[0].Status, got[len(got)-1].Status)
	}

	for i := 1; i < len(got); i++ {
		if got[i].Revision <= got[i-1].Revision {
			t.Errorf("revision %d follows %d", got[i].Revision, got[i-1].Revision)
		}
	}

	if last := got[len(got)-1]; last.Progress.Bytes != 1000 {
		t.Errorf("final notification has %d bytes, want 1000", last.Progress.Bytes)
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
		t.Fatal(err)
	}

	defer close(release)

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		got := n.of(started.ID)
		if last := got[len(got)-1]; last.Progress.Bytes == 2 {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Error("the held back progress update was never sent")
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
		t.Fatal(err)
	}

	j := wait(t, r, started.ID)

	j.Items[0].Status = ItemFailed
	j.Items[0].Result[0] = 'x'
	j.Params["action"] = "stop"
	j.Finished = nil

	again, _ := r.Get(started.ID)
	if again.Items[0].Status != ItemSucceeded || again.Items[0].Result[0] != '{' ||
		again.Params["action"] != "start" || again.Finished == nil {
		t.Errorf("changing a snapshot changed the registry: %+v", again)
	}

	listed := r.List(Filter{})
	listed[0].Items[0].Name = "b"

	if again, _ := r.Get(started.ID); again.Items[0].Name != "a" {
		t.Error("changing a listed job changed the registry")
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
		t.Fatal(err)
	}

	j := wait(t, r, started.ID)

	succeeded, failed, _, _ := j.Counts()
	if succeeded != 100 || failed != 100 || j.Progress.Bytes != 200 || j.Status != StatusPartial {
		t.Errorf("%d succeeded, %d failed, %d bytes, %s; want 100, 100, 200, partial", succeeded, failed, j.Progress.Bytes, j.Status)
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
		t.Fatal(err)
	}

	if j := wait(t, r, started.ID); j.Status != StatusFailed || j.Error == "" {
		t.Errorf("job is %s (%q), want failed with an error", j.Status, j.Error)
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
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, held.ID)); err == nil || time.Now().After(deadline) {
			break
		}

		time.Sleep(time.Millisecond)
	}

	for _, name := range []string{"j0123456789abcdef", "jABCDEF0123456789", "notes"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := r.RemoveStaleArtifacts()
	if err != nil || removed != 1 {
		t.Fatalf("RemoveStaleArtifacts = %d, %v; want 1", removed, err)
	}

	for name, want := range map[string]bool{
		held.ID: true, "j0123456789abcdef": false, "jABCDEF0123456789": true, "notes": true,
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		if got := err == nil; got != want {
			t.Errorf("%s kept: %t, want %t", name, got, want)
		}
	}
}

// A job that would have to wait is refused, adding nothing, once its owner or
// everyone has as many jobs waiting as the queue limits allow; one that can
// run at once is not.
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

	for _, tc := range []struct {
		owner string
		want  Status
	}{
		{"alice", StatusRunning},
		{"alice", StatusQueued},
		{"alice", ""}, // alice has a job waiting
		{"bob", StatusRunning},
		{"bob", StatusQueued},
		{"carol", ""}, // two jobs are waiting
	} {
		j, err := start(tc.owner)

		switch {
		case tc.want == "" && !errors.Is(err, ErrQueueFull):
			t.Errorf("%s's job: %v, want ErrQueueFull", tc.owner, err)
		case tc.want != "" && (err != nil || j.Status != tc.want):
			t.Errorf("%s's job: %s, %v, want %s", tc.owner, j.Status, err, tc.want)
		}
	}

	if s := r.Stats(); s.Running != 2 || s.Queued != 2 {
		t.Errorf("stats %+v, want 2 running and 2 queued", s)
	}
}
