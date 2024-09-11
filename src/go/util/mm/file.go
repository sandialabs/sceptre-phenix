package mm

import (
	"fmt"
	"path/filepath"
	"strings"

	"phenix/util/common"
	"phenix/util/plog"
)

var (
	mmFilesDirectory = GetMMFilesDirectory()
)

// Returns the full path relative to the minimega files directory
func GetMMFullPath(path string) string {
	// If there is no leading file separator, assume a relative
	// path to the minimega files directory
	if !strings.HasPrefix(path, "/") {
		return filepath.Join(mmFilesDirectory, path)
	} else {
		return path
	}

}

// Tries to extract the minimega files directory from a process listing
func GetMMFilesDirectory() string {
	defaultMMFilesDirectory := fmt.Sprintf("%s/images", common.PhenixBase)

	args, err := GetMMArgs()
	if err != nil {
		plog.Warn("Could not get mm files directory from minimega")
		return defaultMMFilesDirectory
	}

	path, ok := args["filepath"]
	if !ok {
		plog.Warn("Could not get mm files directory from minimega")
		return defaultMMFilesDirectory
	}

	return path
}
