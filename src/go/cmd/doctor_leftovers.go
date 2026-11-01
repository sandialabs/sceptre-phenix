package cmd

import (
	"errors"
	"io/fs"
	"strings"
)

// doctorInventory is what minimega gives about its namespaces, which a run of doctor shares.
type doctorInventory struct {
	host string // the host name minimega answers from, without the hostname-suffixes
}

// unwrapPathError is the error that a *[fs.PathError] holds, or err.
func unwrapPathError(err error) error {
	var perr *fs.PathError
	if errors.As(err, &perr) {
		return perr.Err
	}

	return err
}

// capitalize makes the first letter of s a capital letter.
func capitalize(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}
