package lumberjack

import (
	"errors"
	"os"
	"syscall"
)

// osChown is a var so we can mock it out during tests.
var osChown = os.Chown //nolint:gochecknoglobals // mockable

func chown(name string, info os.FileInfo) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err //nolint:wrapcheck // internal error
	}

	_ = f.Close()
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("failed to get system specific file info")
	}

	return osChown(name, int(stat.Uid), int(stat.Gid))
}
