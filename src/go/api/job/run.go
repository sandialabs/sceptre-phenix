package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Run is what a job's work reports through. It is safe for concurrent use.
type Run struct {
	r *Registry
	e *entry
}

// ID returns the job's ID.
func (run *Run) ID() string {
	return run.e.job.ID
}

// ItemStarted marks the named item running.
func (run *Run) ItemStarted(name string) {
	run.item(name, func(item *Item) { item.Status = ItemRunning })
}

// ItemSucceeded marks the named item succeeded, with result (JSON, or a
// value to marshal) as what its action returned, if not nil.
func (run *Run) ItemSucceeded(name string, result any) {
	var raw json.RawMessage

	switch v := result.(type) {
	case nil:
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		var err error
		if raw, err = json.Marshal(v); err != nil {
			raw = nil
		}
	}

	run.item(name, func(item *Item) {
		item.Status = ItemSucceeded
		item.Error = ""
		item.Retryable = false
		item.Result = raw
	})
}

// ItemFailed marks the named item failed with err, which tells whether trying
// it again may succeed (see Retryable).
func (run *Run) ItemFailed(name string, err error) {
	if err == nil {
		err = errors.New("failed")
	}

	run.item(name, func(item *Item) {
		item.Status = ItemFailed
		item.Error = err.Error()
		item.Retryable = Retryable(err)
	})
}

// ItemSkipped marks the named item skipped for reason.
func (run *Run) ItemSkipped(name, reason string) {
	run.item(name, func(item *Item) {
		item.Status = ItemSkipped
		item.Error = reason
		item.Retryable = false
	})
}

// AddBytes counts n more bytes handled.
func (run *Run) AddBytes(n int64) {
	run.progress(func(p *Progress) { p.Bytes += n })
}

// Dir returns the job's folder in the registry's artifact folder, creating it.
func (run *Run) Dir() (string, error) {
	run.r.mu.Lock()
	base := run.r.artifactDir
	run.r.mu.Unlock()

	if base == "" {
		return "", errors.New("no folder configured for job artifacts")
	}

	dir := filepath.Join(base, run.e.job.ID)

	if err := os.MkdirAll(dir, artifactPerm); err != nil {
		return "", fmt.Errorf("creating job artifact folder: %w", err)
	}

	return dir, nil
}

// SetArtifact records the file at path as the job's artifact, downloaded as
// name. incomplete says it lacks some of what was asked for.
func (run *Run) SetArtifact(path, name string, incomplete bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("reading job artifact: %w", err)
	}

	run.r.mu.Lock()

	run.e.job.Artifact = &Artifact{Name: name, Size: info.Size(), Incomplete: incomplete, Path: path}
	run.r.touch(run.e)
	send := run.r.snapshotLocked(run.e)

	run.r.mu.Unlock()

	send()

	return nil
}

// item changes the named item, which the job must have, and tells the
// notifier through the throttle.
func (run *Run) item(name string, change func(*Item)) {
	run.r.mu.Lock()

	i, ok := run.e.index[name]
	if !ok || run.e.job.Status.Terminal() {
		run.r.mu.Unlock()

		return
	}

	item := &run.e.job.Items[i]
	change(item)

	run.e.job.Progress.Done = countTerminal(run.e.job.Items)
	run.r.touch(run.e)
	send := run.r.progressLocked(run.e)

	run.r.mu.Unlock()

	send()
}

// progress changes the job's progress, and tells the notifier through the
// throttle.
func (run *Run) progress(change func(*Progress)) {
	run.r.mu.Lock()

	if run.e.job.Status.Terminal() {
		run.r.mu.Unlock()

		return
	}

	change(&run.e.job.Progress)
	run.r.touch(run.e)
	send := run.r.progressLocked(run.e)

	run.r.mu.Unlock()

	send()
}
