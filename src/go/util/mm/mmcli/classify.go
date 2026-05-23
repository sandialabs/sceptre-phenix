package mmcli

import (
	"errors"
	"strings"
)

// transientSubstrings are matched (case-insensitively) against an error's text.
// These represent conditions that can clear on a retry.
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
// on retry.
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

// IsTransientErr reports whether err is transient rather than treating as a hard failure.
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
