package health

import (
	"context"
	"os/exec"
	"strings"
)

// QemuImgVersion is the first line `<path> --version` prints.
func QemuImgVersion(ctx context.Context, path string) (string, error) {
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", err //nolint:wrapcheck // the check names the command
	}

	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")

	return line, nil
}
