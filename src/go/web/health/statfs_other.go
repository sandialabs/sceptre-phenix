//go:build !(linux || darwin)

package health

import "errors"

// Statfs is not supported on this platform.
func Statfs(string) (DiskStat, error) {
	return DiskStat{}, errors.New("phenix cannot read disk usage on this platform") //nolint:exhaustruct // error
}
