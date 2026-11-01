package cmd

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	busyFilesPercent = 80
	percent          = 100

	limitsFields = 5 // Max open files <soft> <hard>

	filesBusyPhrase    = "of the limit are open"
	unlimitedOpenFiles = -1
	unlimitedText      = "unlimited" // a value in /proc/<pid>/limits that sets no limit
)

// doctorMesh is the nodes of minimega's mesh, which a run of doctor shares.
type doctorMesh struct {
	nodes []string // every node, this node too
}

// parseOpenFilesLimit returns the soft and hard limits of open files in /proc/<pid>/limits, each -1 for unlimited.
func parseOpenFilesLimit(limits string) (int, int, bool) {
	for line := range strings.SplitSeq(limits, "\n") {
		fields := strings.Fields(line)
		if len(fields) < limitsFields || strings.Join(fields[:3], " ") != "Max open files" {
			continue
		}

		values := [2]int{}

		for i, field := range fields[3:5] {
			if field == unlimitedText {
				values[i] = unlimitedOpenFiles

				continue
			}

			n, err := strconv.Atoi(field)
			if err != nil || n < 0 {
				return 0, 0, false
			}

			values[i] = n
		}

		return values[0], values[1], true
	}

	return 0, 0, false
}

// openFilesNow says that n files are open now.
func openFilesNow(n int) string {
	if n == 1 {
		return "1 file is open now"
	}

	return fmt.Sprintf("%d files are open now", n)
}

// plural is n with the words one when n is 1, else with the words many.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return fmt.Sprintf("%d %s", n, many)
}
