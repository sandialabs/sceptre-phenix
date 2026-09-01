package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// AnyRevision is used with record updates and deletes to indicate that no
	// revision precondition should be enforced.
	AnyRevision int64 = 0

	maxRecordNamespaceLen = 128
	maxRecordKeyLen       = 512

	recordKeySeparator = "/"
)

var (
	// ErrRecordNotExist is returned (wrapped in a RecordNotExistError) when a
	// record does not exist in the store.
	ErrRecordNotExist = errors.New("record does not exist")

	// ErrRecordExist is returned (wrapped in a RecordExistError) when a record
	// already exists in the store.
	ErrRecordExist = errors.New("record already exists")

	// ErrRecordConflict is returned (wrapped in a RecordConflictError) when the
	// revision of a stored record does not match the expected revision.
	ErrRecordConflict = errors.New("record revision conflict")

	// ErrInvalidRecordNamespace is returned when a record namespace is empty or
	// contains characters that could escape or collide with other namespaces.
	ErrInvalidRecordNamespace = errors.New("invalid record namespace")

	// ErrInvalidRecordKey is returned when a record key or key prefix is empty or
	// contains characters that could escape or collide with other keys.
	ErrInvalidRecordKey = errors.New("invalid record key")
)

type (
	// Record is a generic, non-config value persisted in the store. Records are
	// opaque to the store: callers are responsible for encoding and decoding the
	// value. Revision is a store assigned, monotonically increasing value used
	// for optimistic concurrency control.
	Record struct {
		Namespace string    `json:"namespace" yaml:"namespace"`
		Key       string    `json:"key"       yaml:"key"`
		Value     []byte    `json:"value"     yaml:"value"`
		Revision  int64     `json:"revision"  yaml:"revision"`
		Created   time.Time `json:"created"   yaml:"created"`
		Updated   time.Time `json:"updated"   yaml:"updated"`
	}

	// Records is a list of records, ordered by key.
	Records []Record

	// RecordNotExistError is returned when a record does not exist. It unwraps to
	// ErrRecordNotExist.
	RecordNotExistError struct {
		Namespace string
		Key       string
	}

	// RecordExistError is returned when a record already exists. It unwraps to
	// ErrRecordExist.
	RecordExistError struct {
		Namespace string
		Key       string
	}

	// RecordConflictError is returned when the revision of a stored record does
	// not match the expected revision. It unwraps to ErrRecordConflict.
	RecordConflictError struct {
		Namespace string
		Key       string
		Expected  int64
		Actual    int64
	}

	// InvalidRecordError is returned when a namespace, key, or key prefix fails
	// validation. It unwraps to ErrInvalidRecordNamespace or ErrInvalidRecordKey.
	InvalidRecordError struct {
		Namespace string
		Key       string
		Reason    string

		err error
	}
)

// NewRecordNotExistError returns a not-found error for the given namespace and key.
func NewRecordNotExistError(namespace, key string) *RecordNotExistError {
	return &RecordNotExistError{Namespace: namespace, Key: key}
}

// NewRecordExistError returns an already-exists error for the given namespace and key.
func NewRecordExistError(namespace, key string) *RecordExistError {
	return &RecordExistError{Namespace: namespace, Key: key}
}

// NewRecordConflictError returns a conflict error describing the expected and
// actual revisions for the given namespace and key.
func NewRecordConflictError(namespace, key string, expected, actual int64) *RecordConflictError {
	return &RecordConflictError{Namespace: namespace, Key: key, Expected: expected, Actual: actual}
}

// ValidateRecordNamespace ensures the given namespace is safe to use as an
// isolated key space in every store implementation.
func ValidateRecordNamespace(namespace string) error {
	if namespace == "" {
		return newInvalidNamespaceError(namespace, "namespace must not be empty")
	}

	if len(namespace) > maxRecordNamespaceLen {
		return newInvalidNamespaceError(namespace, fmt.Sprintf("namespace must be at most %d bytes", maxRecordNamespaceLen))
	}

	if !isRecordNameStart(rune(namespace[0])) {
		return newInvalidNamespaceError(namespace, "namespace must start with an alphanumeric character")
	}

	for _, r := range namespace {
		if !isRecordNameRune(r) {
			return newInvalidNamespaceError(namespace, fmt.Sprintf("namespace contains invalid character %q", r))
		}
	}

	return nil
}

// ValidateRecordKey ensures the given key is non-empty and safe to use as a
// record key in every store implementation.
func ValidateRecordKey(key string) error {
	if key == "" {
		return newInvalidKeyError(key, "key must not be empty")
	}

	return validateRecordKeyOrPrefix(key, false)
}

// ValidateRecordPrefix ensures the given key prefix is safe to use for prefix
// scans. An empty prefix is allowed and matches every key in a namespace.
func ValidateRecordPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}

	return validateRecordKeyOrPrefix(prefix, true)
}

// ValidateRecordDeletePrefix ensures the given key prefix is safe to use for a
// bounded prefix deletion. Unlike prefix scans, an empty prefix is rejected so
// an entire namespace cannot be deleted accidentally.
func ValidateRecordDeletePrefix(prefix string) error {
	if prefix == "" {
		return newInvalidKeyError(prefix, "delete prefix must not be empty")
	}

	return validateRecordKeyOrPrefix(prefix, true)
}

// CopyRecordValue returns a copy of the given value so callers and stores never
// share the same backing array.
func CopyRecordValue(value []byte) []byte {
	if value == nil {
		return nil
	}

	out := make([]byte, len(value))
	copy(out, value)

	return out
}

func newInvalidNamespaceError(namespace, reason string) *InvalidRecordError {
	return &InvalidRecordError{Namespace: namespace, Key: "", Reason: reason, err: ErrInvalidRecordNamespace}
}

func newInvalidKeyError(key, reason string) *InvalidRecordError {
	return &InvalidRecordError{Namespace: "", Key: key, Reason: reason, err: ErrInvalidRecordKey}
}

func validateRecordKeyOrPrefix(key string, isPrefix bool) error {
	if len(key) > maxRecordKeyLen {
		return newInvalidKeyError(key, fmt.Sprintf("key must be at most %d bytes", maxRecordKeyLen))
	}

	if !utf8.ValidString(key) {
		return newInvalidKeyError(key, "key must be valid UTF-8")
	}

	if strings.HasPrefix(key, recordKeySeparator) {
		return newInvalidKeyError(key, "key must not start with '/'")
	}

	if !isPrefix && strings.HasSuffix(key, recordKeySeparator) {
		return newInvalidKeyError(key, "key must not end with '/'")
	}

	if strings.Contains(key, recordKeySeparator+recordKeySeparator) {
		return newInvalidKeyError(key, "key must not contain empty segments")
	}

	for _, r := range key {
		if r < ' ' || r == 0x7f {
			return newInvalidKeyError(key, "key must not contain control characters")
		}
	}

	for segment := range strings.SplitSeq(key, recordKeySeparator) {
		if segment == "." || segment == ".." {
			return newInvalidKeyError(key, "key must not contain relative path segments")
		}
	}

	return nil
}

func isRecordNameStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isRecordNameRune(r rune) bool {
	return isRecordNameStart(r) || r == '-' || r == '_' || r == '.'
}

// Clone returns a deep copy of the record.
func (r Record) Clone() Record {
	clone := r
	clone.Value = CopyRecordValue(r.Value)

	return clone
}

func (e *RecordNotExistError) Error() string {
	return fmt.Sprintf("%v: namespace %q, key %q", ErrRecordNotExist, e.Namespace, e.Key)
}

// Unwrap allows [errors.Is](err, ErrRecordNotExist) to succeed.
func (e *RecordNotExistError) Unwrap() error {
	return ErrRecordNotExist
}

func (e *RecordExistError) Error() string {
	return fmt.Sprintf("%v: namespace %q, key %q", ErrRecordExist, e.Namespace, e.Key)
}

// Unwrap allows [errors.Is](err, ErrRecordExist) to succeed.
func (e *RecordExistError) Unwrap() error {
	return ErrRecordExist
}

func (e *RecordConflictError) Error() string {
	return fmt.Sprintf(
		"%v: namespace %q, key %q (expected revision %d, actual revision %d)",
		ErrRecordConflict, e.Namespace, e.Key, e.Expected, e.Actual,
	)
}

// Unwrap allows [errors.Is](err, ErrRecordConflict) to succeed.
func (e *RecordConflictError) Unwrap() error {
	return ErrRecordConflict
}

func (e *InvalidRecordError) Error() string {
	if e.Namespace != "" {
		return fmt.Sprintf("%v: namespace %q: %s", e.err, e.Namespace, e.Reason)
	}

	return fmt.Sprintf("%v: key %q: %s", e.err, e.Key, e.Reason)
}

// Unwrap allows [errors.Is] to match ErrInvalidRecordNamespace or ErrInvalidRecordKey.
func (e *InvalidRecordError) Unwrap() error {
	return e.err
}
