package job

import (
	"context"
	"errors"
	"net"
)

var (
	// ErrNotFound is returned for a job the registry does not hold.
	ErrNotFound = errors.New("job not found")
	// ErrFinished is returned when canceling a job that has finished.
	ErrFinished = errors.New("job has finished")
	// ErrActive is returned when removing a job that is queued or running.
	ErrActive = errors.New("job is still queued or running")
	// ErrNotCancelable is returned when canceling a job that cannot stop.
	ErrNotCancelable = errors.New("this job cannot be canceled")
	// ErrQueueFull is returned when starting a job that would have to wait
	// while its owner, or everyone, already has the most jobs waiting.
	ErrQueueFull = errors.New("too many jobs are waiting to run; try again once some finish")
	// ErrShuttingDown is returned when starting a job while the server shuts
	// down.
	ErrShuttingDown = errors.New("the phēnix server is shutting down; try again once it is back")
	// ErrForbidden is the reason an item the user may not act on is skipped.
	ErrForbidden = errors.New("you don't have permission")
)

// Retryable reports whether trying again may succeed where err failed: err is
// or wraps [context.DeadlineExceeded], a network timeout, or an error whose
// Temporary method returns true. Other errors, ErrForbidden among them, are
// not.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, ErrForbidden) {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var temporary interface{ Temporary() bool }

	return errors.As(err, &temporary) && temporary.Temporary()
}
