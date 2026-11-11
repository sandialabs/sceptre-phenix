package mm

import (
	"fmt"
	"strings"
)

// caseInsensitiveNTFS are `disk inject` mount options that make NTFS path
// lookups case-insensitive, as on Windows.
const caseInsensitiveNTFS = `"-t lowntfs-3g -o ignore_case"`

// InjectCommands returns the minimega commands that inject the src:dst pairs
// into disk, given as image:partition. Disks of Windows nodes are mounted
// case-insensitively, and minimega accepts only one pair per command that has
// mount options.
func InjectCommands(disk, osType string, pairs ...string) []string {
	if !isWindows(osType) {
		return []string{fmt.Sprintf("disk inject %s files %s", disk, strings.Join(pairs, " "))}
	}

	cmds := make([]string, len(pairs))

	for i, pair := range pairs {
		cmds[i] = fmt.Sprintf("disk inject %s options %s files %s", disk, caseInsensitiveNTFS, pair)
	}

	return cmds
}

// DeleteCommand returns the minimega command that deletes the comma-separated
// paths from disk, given as image:partition.
func DeleteCommand(disk, osType, paths string) string {
	if isWindows(osType) {
		return fmt.Sprintf(
			"disk inject %s options %s delete files %s",
			disk,
			caseInsensitiveNTFS,
			paths,
		)
	}

	return fmt.Sprintf("disk inject %s delete files %s", disk, paths)
}

func isWindows(osType string) bool {
	return strings.EqualFold(osType, "windows")
}
