//go:build !linux

package cmd

import (
	"errors"
)

var errNoFilesystemType = errors.New("only Linux gives the filesystem type")

// filesystemType names the type of the filesystem that holds path, which doctor reads only on Linux.
func filesystemType(string) (string, error) {
	return "", errNoFilesystemType
}
