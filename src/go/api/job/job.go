package job

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"
)

// Status is where a job is in its life.
type Status string

// A job is queued until a slot frees, then running, then ends in one of the
// other statuses.
const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	// StatusPartial is a job some of whose items succeeded and others did not.
	StatusPartial  Status = "partial"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

// Terminal reports whether a job in status s has finished.
func (s Status) Terminal() bool {
	return s != StatusQueued && s != StatusRunning
}

// ItemStatus is the outcome of one item of a job.
type ItemStatus string

// An item is pending until its job starts it, running, then ends in one of
// the other statuses.
const (
	ItemPending   ItemStatus = "pending"
	ItemRunning   ItemStatus = "running"
	ItemSucceeded ItemStatus = "succeeded"
	ItemFailed    ItemStatus = "failed"
	// ItemSkipped is an item not acted on, for a reason that is not a
	// failure, such as permission.
	ItemSkipped  ItemStatus = "skipped"
	ItemCanceled ItemStatus = "canceled"
)

// Terminal reports whether an item in status s has its outcome.
func (s ItemStatus) Terminal() bool {
	return s != ItemPending && s != ItemRunning
}

// Item is one of the things a job acts on, such as a VM or a file, and its
// outcome.
type Item struct {
	Name   string     `json:"name"`
	Status ItemStatus `json:"status"`
	// why the item failed or was skipped
	Error string `json:"error,omitempty"`
	// whether trying the item again may succeed (see Retryable)
	Retryable bool `json:"retryable,omitempty"`
	// what the item's action returned, such as the VM it left
	Result json.RawMessage `json:"result,omitempty"`
}

// Progress counts a job's finished items, and the bytes it has handled when
// it knows them.
type Progress struct {
	Done       int   `json:"done"`
	Total      int   `json:"total"`
	Bytes      int64 `json:"bytes,omitempty"`
	BytesTotal int64 `json:"bytesTotal,omitempty"`
}

// Artifact is the file a job produced for its owner to download.
type Artifact struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// the file lacks some of what was asked for
	Incomplete bool `json:"incomplete,omitempty"`
	// where the server keeps the file
	Path string `json:"-"`
}

// Job is a snapshot of a background operation. The Registry hands out copies,
// which callers may keep and change.
type Job struct {
	// "j" and 16 hex digits
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// the user who started the job
	Owner string `json:"owner"`
	// the experiment the job acts on, if any
	Experiment string            `json:"experiment,omitempty"`
	Title      string            `json:"title"`
	Params     map[string]string `json:"params,omitempty"`

	Status          Status `json:"status"`
	Cancelable      bool   `json:"cancelable"`
	CancelRequested bool   `json:"cancelRequested,omitempty"`

	Progress Progress `json:"progress"`
	Items    []Item   `json:"items"`
	// how many items a shortened copy of the job, such as a websocket
	// message, leaves out
	ItemsOmitted int `json:"itemsOmitted,omitempty"`
	// why the job failed, when no item tells
	Error    string    `json:"error,omitempty"`
	Artifact *Artifact `json:"artifact,omitempty"`

	Created  time.Time  `json:"created"`
	Updated  time.Time  `json:"updated"`
	Started  *time.Time `json:"started,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`
	// increases with every change, so a client can drop older snapshots
	Revision uint64 `json:"revision"`
}

// clone returns a deep copy of j.
func (j Job) clone() Job {
	out := j

	out.Params = maps.Clone(j.Params)
	out.Items = slices.Clone(j.Items)

	for i := range out.Items {
		out.Items[i].Result = slices.Clone(out.Items[i].Result)
	}

	if j.Artifact != nil {
		artifact := *j.Artifact
		out.Artifact = &artifact
	}

	if j.Started != nil {
		started := *j.Started
		out.Started = &started
	}

	if j.Finished != nil {
		finished := *j.Finished
		out.Finished = &finished
	}

	return out
}

// Counts returns how many of the job's items succeeded, failed, were skipped
// and were canceled.
func (j Job) Counts() (int, int, int, int) {
	var succeeded, failed, skipped, canceled int

	for _, item := range j.Items {
		switch item.Status {
		case ItemSucceeded:
			succeeded++
		case ItemFailed:
			failed++
		case ItemSkipped:
			skipped++
		case ItemCanceled:
			canceled++
		case ItemPending, ItemRunning:
		}
	}

	return succeeded, failed, skipped, canceled
}

// RunFunc does a job's work, reporting through r. It should return once ctx
// is done. A non-nil error fails the items it left without an outcome.
type RunFunc func(ctx context.Context, r *Run) error

// Spec describes a job to start.
type Spec struct {
	Kind       string
	Owner      string
	Experiment string
	Title      string
	Params     map[string]string
	// the names of the job's items, in order; duplicates are dropped
	Items      []string
	BytesTotal int64
	Cancelable bool
	Run        RunFunc
}
