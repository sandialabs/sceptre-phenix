package util

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"phenix/util/common"
)

var (
	filePathRe       = regexp.MustCompile(`filepath=([^ ]+)`)
	mmFilesDirectory = GetMMFilesDirectory()
)

// Returns the full path relative to the minimega files directory
func GetMMFullPath(path string) string {
	// If there is no leading file seperator, assume a relative
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

	cmd := "ps"
	psPath, err := exec.LookPath(cmd)

	if err != nil {
		return defaultMMFilesDirectory
	}

	cmd = "grep"
	grepPath, err := exec.LookPath(cmd)

	if err != nil {
		return defaultMMFilesDirectory
	}

	psCmd := exec.Command(psPath, "-au")
	psStdout, _ := psCmd.StdoutPipe()
	defer psStdout.Close()

	grepCmd := exec.Command(grepPath, "minimega")
	grepCmd.Stdin = psStdout

	psCmd.Start()

	output, _ := grepCmd.Output()

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Split(bufio.ScanLines)

	// Try to find the minimega files directory
	for scanner.Scan() {

		if !strings.Contains(scanner.Text(), "-filepath=") {
			continue
		}

		filesDirectory := filePathRe.FindAllStringSubmatch(scanner.Text(), -1)
		if len(filesDirectory) == 0 {
			return defaultMMFilesDirectory
		} else {
			return filesDirectory[0][1]
		}
	}

	return defaultMMFilesDirectory

}
