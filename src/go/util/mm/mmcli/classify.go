package mmcli

import (
	"errors"
	"strings"
)

// transientSubstrings are matched (case-insensitively) against an error's text.
// These represent conditions that commonly clear on a retry, especially when a
// command is fanned out to remote nodes over minimega's mesh (meshage).
var transientSubstrings = []string{ //nolint:gochecknoglobals // lookup table
	"broken pipe",
	"use of closed network connection",
	"connection refused",
	"connection reset",
	"server disconnected", // miniclient EOF
	"i/o timeout",
	"meshage", // generic meshage transport errors
	"timeout",
}

// permanentSubstrings are matched (case-insensitively) and ALWAYS win over a
// transient match. These are genuine logic/usage errors that will never clear
// on retry. Notably "cannot mesh send yourself" contains no transient token but
// must never be retried.
var permanentSubstrings = []string{ //nolint:gochecknoglobals // lookup table
	"cannot mesh send yourself",
	"vm not found",
	"vm not running",
	"namespace must be active",
	"invalid command",
	"expected", // minicli syntax errors ("expected ...")
	"no such handler",
}

// isPermanentErr reports whether err is a permanent error that must not be
// retried.
func isPermanentErr(err error) bool {
	if err == nil {
		return false
	}

	s := strings.ToLower(err.Error())

	for _, p := range permanentSubstrings {
		if strings.Contains(s, p) {
			return true
		}
	}

	return false
}

// IsTransientErr reports whether err is transient -- i.e. worth re-polling
// rather than treating as a hard failure. Permanent matches short-circuit to
// false so they are never mistaken for a recoverable blip. It is exported so
// callers outside this package (the mm package's C2/response polling loops) can
// make the same transient-vs-permanent distinction.
func IsTransientErr(err error) bool {
	if err == nil || isPermanentErr(err) {
		return false
	}

	if errors.Is(err, ErrTimeout) {
		return true
	}

	s := strings.ToLower(err.Error())

	for _, t := range transientSubstrings {
		if strings.Contains(s, t) {
			return true
		}
	}

	return false
}
