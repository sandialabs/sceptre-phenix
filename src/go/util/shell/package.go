package shell

import (
	"context"
)

var DefaultShell Shell = new(shell)

func FindCommandsWithPrefix(prefix string) []string {
	return DefaultShell.FindCommandsWithPrefix(prefix)
}

func CommandExists(cmd string) bool {
	return DefaultShell.CommandExists(cmd)
}

func ProcessExists(pid int) bool {
	return DefaultShell.ProcessExists(pid)
}

func ExecCommand(ctx context.Context, opts ...Option) ([]byte, []byte, error) {
	return DefaultShell.ExecCommand(ctx, opts...)
}
