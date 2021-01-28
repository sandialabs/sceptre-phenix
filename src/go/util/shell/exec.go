package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type shell struct{}

func (shell) FindCommandsWithPrefix(prefix string) []string {
	var commands []string

	args := strings.Split(os.Getenv("PATH"), ":")
	args = append(args, "-type", "f", "-executable", "-name", prefix+"*")

	cmd := exec.Command("find", args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	for _, c := range strings.Split(string(out), "\n") {
		if c != "" {
			base := filepath.Base(c)
			commands = append(commands, strings.TrimPrefix(base, prefix))
		}
	}

	return commands
}

func (shell) CommandExists(cmd string) bool {
	err := exec.Command("which", cmd).Run()
	return err == nil
}

func (shell) ExecCommand(ctx context.Context, opts ...Option) ([]byte, []byte, error) {
	o := newOptions(opts...)

	var (
		stdIn  io.Reader
		stdOut bytes.Buffer
		stdErr bytes.Buffer
	)

	if o.stdin == nil {
		stdIn = os.Stdin
	} else {
		stdIn = bytes.NewBuffer(o.stdin)
	}

	cmd := exec.CommandContext(ctx, o.cmd, o.args...)

	cmd.Stdin = stdIn
	cmd.Stdout = &stdOut
	cmd.Stderr = &stdErr

	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, o.env...)

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting command: %w", err)
	}

	done := make(chan struct{})

	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
			cmd.Process.Signal(syscall.SIGTERM)

			select {
			case <-done:
				return
			case <-time.After(10 * time.Second):
				cmd.Process.Kill()
			}
		}
	}()

	err := cmd.Wait()
	close(done)

	return stdOut.Bytes(), stdErr.Bytes(), err
}
