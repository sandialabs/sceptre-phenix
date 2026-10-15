package job

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"phenix/util/plog"
)

// The registry's defaults, which the options change.
const (
	// DefaultRetention is how long a finished job is kept.
	DefaultRetention = time.Hour
	// DefaultMaxFinished is the most finished jobs kept; the oldest go first.
	DefaultMaxFinished = 200
	// DefaultPerOwner is the most jobs running at once for one owner.
	DefaultPerOwner = 4
	// DefaultGlobal is the most jobs running at once in all.
	DefaultGlobal = 16
	// DefaultQueuedPerOwner is the most jobs of one owner waiting to run;
	// Start refuses more with ErrQueueFull.
	DefaultQueuedPerOwner = 32
	// DefaultQueuedGlobal is the most jobs waiting to run in all.
	DefaultQueuedGlobal = 256
	// NotifyInterval is the least time between two notifications of a job's
	// progress.
	NotifyInterval = 250 * time.Millisecond
)

const (
	sweepInterval = time.Minute
	idBytes       = 8
	artifactPerm  = 0o750
)

// artifactDirName is the name of a job's folder in the artifact folder.
var artifactDirName = regexp.MustCompile(`^j[0-9a-f]{16}$`)

// Default is the registry of the phenix UI server.
var Default = NewRegistry() //nolint:gochecknoglobals // the server's registry

// Notifier is told of each change to a job, with a snapshot of it, and of each
// job the registry forgets (removed). It is called without the registry's
// lock held, possibly from several goroutines at once, so a later call may
// carry an older snapshot: compare the jobs' revisions.
type Notifier func(j Job, removed bool)

// Option configures a Registry.
type Option func(*Registry)

// WithNotifier has the registry tell notify of every change.
func WithNotifier(notify Notifier) Option {
	return func(r *Registry) { r.notify = notify }
}

// WithRetention keeps finished jobs for d.
func WithRetention(d time.Duration) Option {
	return func(r *Registry) { r.retention = d }
}

// WithMaxFinished keeps at most n finished jobs.
func WithMaxFinished(n int) Option {
	return func(r *Registry) { r.maxFinished = n }
}

// WithLimits runs at most perOwner jobs at once for each owner, and global in
// all; the others wait, queued.
func WithLimits(perOwner, global int) Option {
	return func(r *Registry) { r.perOwner, r.global = perOwner, global }
}

// WithQueueLimits has at most perOwner jobs of each owner, and global in all,
// wait to run; Start refuses more with ErrQueueFull.
func WithQueueLimits(perOwner, global int) Option {
	return func(r *Registry) { r.queuedPerOwner, r.queuedGlobal = perOwner, global }
}

// WithClock has the registry read the time from now, for the jobs' times and
// their retention.
func WithClock(now func() time.Time) Option {
	return func(r *Registry) { r.now = now }
}

// WithArtifactDir keeps the jobs' artifacts in folders of dir, one for each
// job, named after it.
func WithArtifactDir(dir string) Option {
	return func(r *Registry) { r.artifactDir = dir }
}

// WithNotifyInterval sends a job's progress at most every d.
func WithNotifyInterval(d time.Duration) Option {
	return func(r *Registry) { r.interval = d }
}

// Filter selects jobs to list. Empty fields match every job.
type Filter struct {
	Owner      string
	Experiment string
	Kind       string
	// only jobs queued or running
	Active bool
}

// Stats counts a registry's jobs.
type Stats struct {
	Queued   int `json:"queued"`
	Running  int `json:"running"`
	Finished int `json:"finished"`
}

// Registry holds jobs in memory. It is safe for concurrent use.
type Registry struct {
	mu sync.Mutex

	jobs map[string]*entry
	// queued jobs, oldest first
	queue     []*entry
	running   int
	runningBy map[string]int
	seq       uint64

	notify      Notifier
	retention   time.Duration
	maxFinished int
	perOwner    int
	global      int
	// the most jobs waiting to run, for each owner and in all
	queuedPerOwner int
	queuedGlobal   int
	now            func() time.Time
	artifactDir    string
	interval       time.Duration

	wg       sync.WaitGroup
	sweeping sync.Once
	done     chan struct{}
	stopping sync.Once
}

// entry is a job and what the registry needs to run it.
type entry struct {
	job Job
	// the index of each item, by name
	index map[string]int
	// orders jobs created at the same time
	seq uint64

	run    RunFunc
	ctx    context.Context //nolint:containedctx // the job's own context, canceled by Cancel
	cancel context.CancelFunc

	// when the last notification was sent, and the one waiting for the
	// throttle, if any
	notified time.Time
	timer    *time.Timer
}

// NewRegistry returns an empty registry with the defaults the options do not
// change.
func NewRegistry(opts ...Option) *Registry {
	r := &Registry{ //nolint:exhaustruct // zero values are ready
		jobs:           make(map[string]*entry),
		runningBy:      make(map[string]int),
		retention:      DefaultRetention,
		maxFinished:    DefaultMaxFinished,
		perOwner:       DefaultPerOwner,
		global:         DefaultGlobal,
		queuedPerOwner: DefaultQueuedPerOwner,
		queuedGlobal:   DefaultQueuedGlobal,
		now:            time.Now,
		interval:       NotifyInterval,
		done:           make(chan struct{}),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Configure applies opts to the registry, as the server does when it starts.
func (r *Registry) Configure(opts ...Option) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, opt := range opts {
		opt(r)
	}
}

// Start adds a job and runs it as soon as the limits allow: at once, or
// queued until a slot frees. It returns the job as added, or ErrQueueFull,
// adding nothing, when the job would have to wait and its owner, or the
// registry, already has as many jobs waiting as the queue limits allow.
func (r *Registry) Start(spec Spec) (Job, error) {
	if spec.Run == nil {
		return Job{}, errors.New("starting job: no work given")
	}

	id, err := newID()
	if err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()

	if !r.canStart(spec.Owner) && !r.canQueue(spec.Owner) {
		r.mu.Unlock()
		cancel()

		return Job{}, ErrQueueFull
	}

	now := r.now()
	r.seq++

	e := &entry{ //nolint:exhaustruct // throttle state starts empty
		job: Job{ //nolint:exhaustruct // Started, Finished, Error and Artifact come later
			ID:         id,
			Kind:       spec.Kind,
			Owner:      spec.Owner,
			Experiment: spec.Experiment,
			Title:      spec.Title,
			Params:     spec.Params,
			Status:     StatusQueued,
			Cancelable: spec.Cancelable,
			Progress:   Progress{Done: 0, Total: 1, Bytes: 0, BytesTotal: spec.BytesTotal},
			Items:      []Item{},
			Created:    now,
			Updated:    now,
			Revision:   1,
		},
		index:  make(map[string]int),
		seq:    r.seq,
		run:    spec.Run,
		ctx:    ctx,
		cancel: cancel,
	}

	for _, name := range spec.Items {
		if _, ok := e.index[name]; ok {
			continue
		}

		e.index[name] = len(e.job.Items)
		e.job.Items = append(e.job.Items, Item{Name: name, Status: ItemPending}) //nolint:exhaustruct // no outcome yet
	}

	if n := len(e.job.Items); n > 0 {
		e.job.Progress.Total = n
	}

	r.jobs[id] = e

	var send []func()

	if r.canStart(spec.Owner) {
		send = append(send, r.begin(e))
	} else {
		r.queue = append(r.queue, e)
		send = append(send, r.snapshotLocked(e))
	}

	out := e.job.clone()

	r.mu.Unlock()

	r.sweeping.Do(func() { go r.sweepLoop() })

	for _, f := range send {
		f()
	}

	return out, nil
}

// Get returns the job with the given ID.
func (r *Registry) Get(id string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.jobs[id]
	if !ok {
		return Job{}, false
	}

	return e.job.clone(), true
}

// List returns the jobs f selects, newest first.
func (r *Registry) List(f Filter) []Job {
	r.mu.Lock()

	entries := make([]*entry, 0, len(r.jobs))

	for _, e := range r.jobs {
		j := &e.job

		switch {
		case f.Owner != "" && j.Owner != f.Owner,
			f.Experiment != "" && j.Experiment != f.Experiment,
			f.Kind != "" && j.Kind != f.Kind,
			f.Active && j.Status.Terminal():
			continue
		}

		entries = append(entries, e)
	}

	slices.SortFunc(entries, func(a, b *entry) int {
		if c := b.job.Created.Compare(a.job.Created); c != 0 {
			return c
		}

		return cmp.Compare(b.seq, a.seq)
	})

	jobs := make([]Job, len(entries))
	for i, e := range entries {
		jobs[i] = e.job.clone()
	}

	r.mu.Unlock()

	return jobs
}

// Cancel asks a job to stop. A queued job is canceled at once; a running one
// ends when its work notices, and items without an outcome then are
// canceled. It returns ErrFinished for a job that has finished and
// ErrNotCancelable for one that cannot stop.
func (r *Registry) Cancel(id string) (Job, error) {
	r.mu.Lock()

	e, ok := r.jobs[id]

	switch {
	case !ok:
		r.mu.Unlock()

		return Job{}, ErrNotFound
	case e.job.Status.Terminal():
		out := e.job.clone()
		r.mu.Unlock()

		return out, ErrFinished
	case !e.job.Cancelable:
		out := e.job.clone()
		r.mu.Unlock()

		return out, ErrNotCancelable
	}

	e.job.CancelRequested = true

	var send []func()

	if e.job.Status == StatusQueued {
		r.queue = slices.DeleteFunc(r.queue, func(q *entry) bool { return q == e })
		send = r.finishLocked(e, context.Canceled)
	} else {
		r.touch(e)
		send = append(send, r.snapshotLocked(e))
	}

	e.cancel()

	out := e.job.clone()

	r.mu.Unlock()

	for _, f := range send {
		f()
	}

	return out, nil
}

// Remove forgets a finished job and deletes its artifact. It returns
// ErrActive for a job that is queued or running.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()

	e, ok := r.jobs[id]

	switch {
	case !ok:
		r.mu.Unlock()

		return ErrNotFound
	case !e.job.Status.Terminal():
		r.mu.Unlock()

		return ErrActive
	}

	send := r.forgetLocked(e)

	r.mu.Unlock()

	send()

	return nil
}

// Sweep forgets the jobs that finished longer ago than the retention, and
// deletes their artifacts. The registry sweeps on its own every minute.
func (r *Registry) Sweep() {
	r.mu.Lock()

	var (
		cutoff = r.now().Add(-r.retention)
		send   []func()
	)

	for _, e := range r.jobs {
		if e.job.Finished != nil && !e.job.Finished.After(cutoff) {
			send = append(send, r.forgetLocked(e))
		}
	}

	r.mu.Unlock()

	for _, f := range send {
		f()
	}
}

// Stats counts the registry's jobs.
func (r *Registry) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()

	return Stats{
		Queued:   len(r.queue),
		Running:  r.running,
		Finished: len(r.jobs) - len(r.queue) - r.running,
	}
}

// RemoveStaleArtifacts deletes the job folders in the artifact folder that no
// job the registry holds owns, as a restart leaves, and returns how many it
// deleted. Other files there are left alone.
func (r *Registry) RemoveStaleArtifacts() (int, error) {
	r.mu.Lock()
	dir := r.artifactDir
	r.mu.Unlock()

	if dir == "" {
		return 0, nil
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("reading job artifact folder: %w", err)
	}

	var removed int

	for _, entry := range entries {
		name := entry.Name()

		if !artifactDirName.MatchString(name) {
			continue
		}

		if _, ok := r.Get(name); ok {
			continue
		}

		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("removing stale job artifacts: %w", err)
		}

		removed++
	}

	return removed, nil
}

// Shutdown cancels every job, waits for the running ones to end, and stops
// the sweeper.
func (r *Registry) Shutdown() {
	r.stopping.Do(func() { close(r.done) })

	r.mu.Lock()

	ids := make([]string, 0, len(r.jobs))

	for id, e := range r.jobs {
		if !e.job.Status.Terminal() {
			ids = append(ids, id)
		}
	}

	r.mu.Unlock()

	for _, id := range ids {
		r.forceCancel(id)
	}

	r.wg.Wait()
}

// forceCancel cancels a job whether or not it is cancelable, for Shutdown.
func (r *Registry) forceCancel(id string) {
	r.mu.Lock()

	e, ok := r.jobs[id]
	if !ok || e.job.Status.Terminal() {
		r.mu.Unlock()

		return
	}

	e.job.Cancelable = true

	r.mu.Unlock()

	_, _ = r.Cancel(id)
}

// canQueue reports whether a job of owner may wait to run. r.mu is held.
func (r *Registry) canQueue(owner string) bool {
	if len(r.queue) >= r.queuedGlobal {
		return false
	}

	var queued int

	for _, e := range r.queue {
		if e.job.Owner == owner {
			queued++
		}
	}

	return queued < r.queuedPerOwner
}

// canStart reports whether a job of owner may run now. r.mu is held.
func (r *Registry) canStart(owner string) bool {
	return r.running < r.global && r.runningBy[owner] < r.perOwner
}

// begin marks e running and runs its work, returning the notification to
// send. r.mu is held.
func (r *Registry) begin(e *entry) func() {
	now := r.now()

	e.job.Status = StatusRunning
	e.job.Started = &now
	r.touch(e)

	r.running++
	r.runningBy[e.job.Owner]++

	r.wg.Add(1)

	go r.execute(e)

	return r.snapshotLocked(e)
}

// execute runs e's work and finishes e with its result.
func (r *Registry) execute(e *entry) {
	defer r.wg.Done()

	err := r.runSafely(e)

	r.mu.Lock()
	send := r.finishLocked(e, err)
	r.mu.Unlock()

	for _, f := range send {
		f()
	}
}

// runSafely runs e's work, turning a panic into an error.
func (r *Registry) runSafely(e *entry) (err error) {
	defer func() {
		if p := recover(); p != nil {
			plog.Error(plog.TypeSystem, "job panicked", "job", e.job.ID, "kind", e.job.Kind, "panic", p)

			err = fmt.Errorf("job failed unexpectedly: %v", p)
		}
	}()

	return e.run(e.ctx, &Run{r: r, e: e})
}

// finishLocked gives e its final status from its items and the error its work
// returned, starts the queued jobs that may now run, and forgets the oldest
// finished jobs past the limit. It returns the notifications to send. r.mu
// is held.
func (r *Registry) finishLocked(e *entry, err error) []func() {
	wasRunning := e.job.Status == StatusRunning
	j := &e.job

	unfinished := ItemCanceled
	if !j.CancelRequested {
		unfinished = ItemFailed
	}

	for i := range j.Items {
		item := &j.Items[i]
		if item.Status.Terminal() {
			continue
		}

		item.Status = unfinished

		if unfinished == ItemFailed {
			item.Error = "not done"
			if err != nil {
				item.Error = err.Error()
			}
		}
	}

	j.Status, j.Error = finalStatus(*j, err)

	now := r.now()
	j.Finished = &now
	j.Progress.Done = j.Progress.Total

	if len(j.Items) > 0 {
		j.Progress.Done = countTerminal(j.Items)
	}

	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}

	r.touch(e)

	send := make([]func(), 0, 1+len(r.queue))
	send = append(send, r.snapshotLocked(e))

	if wasRunning {
		r.running--

		if r.runningBy[j.Owner]--; r.runningBy[j.Owner] <= 0 {
			delete(r.runningBy, j.Owner)
		}
	}

	e.cancel()

	send = append(send, r.dequeueLocked()...)
	send = append(send, r.trimLocked()...)

	return send
}

// finalStatus is the status a job ends in, and its error, given its items
// and the error its work returned.
func finalStatus(j Job, err error) (Status, string) {
	errText := ""
	if err != nil {
		errText = err.Error()
	}

	if j.CancelRequested {
		return StatusCanceled, ""
	}

	succeeded := countStatus(j.Items, ItemSucceeded)

	switch {
	case len(j.Items) == 0 && err == nil, len(j.Items) > 0 && succeeded == len(j.Items) && err == nil:
		return StatusSucceeded, ""
	case succeeded > 0:
		return StatusPartial, errText
	case errText != "":
		return StatusFailed, errText
	default:
		return StatusFailed, "every item failed"
	}
}

// dequeueLocked starts the oldest queued jobs the limits now allow, and
// returns their notifications. r.mu is held.
func (r *Registry) dequeueLocked() []func() {
	var send []func()

	for i := 0; i < len(r.queue); {
		e := r.queue[i]

		if !r.canStart(e.job.Owner) {
			i++

			continue
		}

		r.queue = slices.Delete(r.queue, i, i+1)
		send = append(send, r.begin(e))
	}

	return send
}

// trimLocked forgets the oldest finished jobs beyond maxFinished, and returns
// their notifications. r.mu is held.
func (r *Registry) trimLocked() []func() {
	var finished []*entry

	for _, e := range r.jobs {
		if e.job.Status.Terminal() {
			finished = append(finished, e)
		}
	}

	if len(finished) <= r.maxFinished {
		return nil
	}

	slices.SortFunc(finished, func(a, b *entry) int {
		if c := a.job.Finished.Compare(*b.job.Finished); c != 0 {
			return c
		}

		return cmp.Compare(a.seq, b.seq)
	})

	send := make([]func(), 0, len(finished)-r.maxFinished)

	for _, e := range finished[:len(finished)-r.maxFinished] {
		send = append(send, r.forgetLocked(e))
	}

	return send
}

// forgetLocked removes e, and returns the function that deletes its artifact
// and sends the notification. r.mu is held.
func (r *Registry) forgetLocked(e *entry) func() {
	delete(r.jobs, e.job.ID)

	var (
		snapshot = e.job.clone()
		notify   = r.notify
		dir      = r.artifactDir
	)

	return func() {
		removeArtifact(dir, snapshot)

		if notify != nil {
			notify(snapshot, true)
		}
	}
}

// removeArtifact deletes the job's artifact, and its folder in dir.
func removeArtifact(dir string, j Job) {
	if j.Artifact != nil && j.Artifact.Path != "" {
		if err := os.Remove(j.Artifact.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			plog.Warn(plog.TypeSystem, "removing job artifact", "job", j.ID, "err", err)
		}
	}

	if dir != "" {
		if err := os.RemoveAll(filepath.Join(dir, j.ID)); err != nil {
			plog.Warn(plog.TypeSystem, "removing job artifact folder", "job", j.ID, "err", err)
		}
	}
}

// touch records a change to e. r.mu is held.
func (r *Registry) touch(e *entry) {
	e.job.Revision++
	e.job.Updated = r.now()
}

// snapshotLocked returns the function that tells the notifier of e as it is
// now, at once. r.mu is held.
func (r *Registry) snapshotLocked(e *entry) func() {
	if r.notify == nil {
		return func() {}
	}

	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}

	e.notified = time.Now()

	var (
		snapshot = e.job.clone()
		notify   = r.notify
	)

	return func() { notify(snapshot, false) }
}

// progressLocked returns the function that tells the notifier of a progress
// change to e: at once when the last notification is older than the
// interval, and otherwise later, with the job as it is then. r.mu is held.
func (r *Registry) progressLocked(e *entry) func() {
	if r.notify == nil {
		return func() {}
	}

	if e.timer != nil {
		return func() {}
	}

	wait := r.interval - time.Since(e.notified)
	if wait <= 0 {
		return r.snapshotLocked(e)
	}

	e.timer = time.AfterFunc(wait, func() { r.flush(e) })

	return func() {}
}

// flush sends the progress notification of e the throttle held back.
func (r *Registry) flush(e *entry) {
	r.mu.Lock()

	if e.timer == nil || e.job.Status.Terminal() {
		r.mu.Unlock()

		return
	}

	e.timer = nil
	send := r.snapshotLocked(e)

	r.mu.Unlock()

	send()
}

// sweepLoop sweeps every sweepInterval until Shutdown.
func (r *Registry) sweepLoop() {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			r.Sweep()
		}
	}
}

// countStatus counts the items in status.
func countStatus(items []Item, status ItemStatus) int {
	var n int

	for _, item := range items {
		if item.Status == status {
			n++
		}
	}

	return n
}

// countTerminal counts the items with an outcome.
func countTerminal(items []Item) int {
	var n int

	for _, item := range items {
		if item.Status.Terminal() {
			n++
		}
	}

	return n
}

// newID returns a new job ID: "j" and 16 random hex digits.
func newID() (string, error) {
	b := make([]byte, idBytes)

	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating job ID: %w", err)
	}

	return "j" + hex.EncodeToString(b), nil
}
