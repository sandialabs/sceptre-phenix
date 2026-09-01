package builder

import (
	"errors"
	"fmt"
	"strings"

	"phenix/store"
)

var (
	// ErrNotFound is returned when a draft, snapshot, or published document does
	// not exist.
	ErrNotFound = errors.New("builder: not found")

	// ErrConflict is returned when an optimistic concurrency check fails,
	// either because the caller's revision is stale or because the draft changed
	// between reading and writing.
	ErrConflict = errors.New("builder: revision conflict")

	// ErrTooLarge is returned when a document or draft history would exceed the
	// limits in limits.go. It is always returned before any durable metadata
	// update.
	ErrTooLarge = errors.New("builder: payload too large")

	// ErrCorrupt is returned when stored content cannot be reassembled: missing,
	// reordered, truncated, oversized, or otherwise damaged chunks.
	ErrCorrupt = errors.New("builder: corrupt stored content")

	// ErrInvalid is returned when a request is malformed (missing owner or
	// actor, unknown publish mode, out of range cursor, ...).
	ErrInvalid = errors.New("builder: invalid request")

	// ErrCleanup is returned when the durable part of an operation succeeded but
	// removing content that is no longer referenced failed. Operations returning
	// a value still return it alongside a cleanup error.
	ErrCleanup = errors.New("builder: cleanup failed")
)

type (
	// NotFoundError identifies the missing object. It unwraps to [ErrNotFound].
	NotFoundError struct {
		Kind string
		ID   string
	}

	// ConflictError reports the expected and actual revisions of a draft. An
	// actual revision of zero means the revision could not be determined. It
	// unwraps to [ErrConflict].
	ConflictError struct {
		Kind     string
		ID       string
		Expected int64
		Actual   int64
		Reason   string
	}

	// TooLargeError reports which limit was exceeded. It unwraps to
	// [ErrTooLarge].
	TooLargeError struct {
		What  string
		Size  int64
		Limit int64
	}

	// CorruptError describes why stored content could not be reassembled. It
	// unwraps to [ErrCorrupt].
	CorruptError struct {
		Kind   string
		ID     string
		Reason string
	}

	// ValidationError describes a malformed request. It unwraps to [ErrInvalid]
	// and, when the request was rejected because of an underlying failure (for
	// example a document that does not decode or validate), to that cause as
	// well.
	ValidationError struct {
		Field  string
		Reason string
		Cause  error
	}

	// CleanupError aggregates failures encountered while removing content that
	// is no longer referenced. It unwraps to [ErrCleanup], and its individual
	// causes remain reachable with [errors.Is] and [errors.As].
	CleanupError struct {
		Op     string
		Errors []error
	}
)

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%v: %s %q", ErrNotFound, e.Kind, e.ID)
}

// Unwrap allows [errors.Is](err, ErrNotFound) to succeed.
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

func (e *ConflictError) Error() string {
	msg := fmt.Sprintf("%v: %s %q (expected revision %d, actual revision %d)", ErrConflict, e.Kind, e.ID, e.Expected, e.Actual)
	if e.Reason != "" {
		msg += ": " + e.Reason
	}

	return msg
}

// Unwrap allows [errors.Is](err, ErrConflict) to succeed.
func (e *ConflictError) Unwrap() error { return ErrConflict }

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("%v: %s is %d bytes, limit is %d bytes", ErrTooLarge, e.What, e.Size, e.Limit)
}

// Unwrap allows [errors.Is](err, ErrTooLarge) to succeed.
func (e *TooLargeError) Unwrap() error { return ErrTooLarge }

func (e *CorruptError) Error() string {
	return fmt.Sprintf("%v: %s %q: %s", ErrCorrupt, e.Kind, e.ID, e.Reason)
}

// Unwrap allows [errors.Is](err, ErrCorrupt) to succeed.
func (e *CorruptError) Unwrap() error { return ErrCorrupt }

func (e *ValidationError) Error() string {
	msg := fmt.Sprintf("%v: %s: %s", ErrInvalid, e.Field, e.Reason)
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}

	return msg
}

// Unwrap returns [ErrInvalid] together with the underlying cause, so callers can
// match the validation failure itself or the error that produced it.
func (e *ValidationError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrInvalid}
	}

	return []error{ErrInvalid, e.Cause}
}

func (e *CleanupError) Error() string {
	reasons := make([]string, 0, len(e.Errors))
	for _, err := range e.Errors {
		reasons = append(reasons, err.Error())
	}

	return fmt.Sprintf("%v: %s: %s", ErrCleanup, e.Op, strings.Join(reasons, "; "))
}

// Unwrap returns [ErrCleanup] together with every underlying cause, so callers
// can match the cleanup failure itself or any cause it aggregates.
func (e *CleanupError) Unwrap() []error {
	return append([]error{ErrCleanup}, e.Errors...)
}

func newNotFoundError(kind, id string) *NotFoundError {
	return &NotFoundError{Kind: kind, ID: id}
}

func newValidationError(field, reason string) *ValidationError {
	return &ValidationError{Field: field, Reason: reason, Cause: nil}
}

// newValidationCause reports a malformed request caused by err, keeping err
// reachable with [errors.Is] and [errors.As].
func newValidationCause(field, reason string, err error) *ValidationError {
	return &ValidationError{Field: field, Reason: reason, Cause: err}
}

func newCorruptError(kind, id, reason string) *CorruptError {
	return &CorruptError{Kind: kind, ID: id, Reason: reason}
}

func newTooLargeError(what string, size, limit int64) *TooLargeError {
	return &TooLargeError{What: what, Size: size, Limit: limit}
}

// cleanupErrors keeps only the failures of a cleanup attempt, so a successful
// cleanup never produces an empty [CleanupError].
func cleanupErrors(errs ...error) []error {
	kept := make([]error, 0, len(errs))

	for _, err := range errs {
		if err != nil {
			kept = append(kept, err)
		}
	}

	return kept
}

func newCleanupError(op string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}

	return &CleanupError{Op: op, Errors: errs}
}

// storeError translates store level errors into this package's typed errors so
// callers never need to know which store implementation is in use.
func storeError(kind, id string, expected int64, err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, store.ErrRecordNotExist):
		return newNotFoundError(kind, id)
	case errors.Is(err, store.ErrRecordExist):
		return &ConflictError{Kind: kind, ID: id, Expected: expected, Actual: 0, Reason: "already exists"}
	case errors.Is(err, store.ErrRecordConflict):
		var conflict *store.RecordConflictError
		if errors.As(err, &conflict) {
			return &ConflictError{Kind: kind, ID: id, Expected: conflict.Expected, Actual: conflict.Actual, Reason: ""}
		}

		return &ConflictError{Kind: kind, ID: id, Expected: expected, Actual: 0, Reason: ""}
	case errors.Is(err, store.ErrInvalidRecordNamespace), errors.Is(err, store.ErrInvalidRecordKey):
		return &ValidationError{Field: kind, Reason: err.Error(), Cause: nil}
	}

	return fmt.Errorf("%s %q: %w", kind, id, err)
}
