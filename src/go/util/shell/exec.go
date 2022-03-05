package shell

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/go-multierror"
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

func (shell) ProcessExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}

	if errors.Is(err, os.ErrProcessDone) {
		return false
	}

	errno, ok := err.(syscall.Errno)
	if !ok {
		return false
	}

	switch errno {
	case syscall.ESRCH:
		return false
	case syscall.EPERM:
		return true
	}

	return false
}

func (shell) ExecCommand(ctx context.Context, opts ...Option) ([]byte, []byte, error) {
	o := newOptions(opts...)

	var (
		stdIn       io.Reader
		stdoutBytes []byte
		stderrBytes []byte
	)

	if o.stdin == nil {
		stdIn = os.Stdin
	} else {
		stdIn = bytes.NewBuffer(o.stdin)
	}

	// Not using `exec.CommandContext` here since we're catching the context being
	// canceled below in order to gracefully terminate the child process. Using
	// `exec.CommandContext` forcefully kills the child process when the context
	// is canceled.
	cmd := exec.Command(o.cmd, o.args...)

	cmd.Stdin = stdIn
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, o.env...)

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting command: %w", err)
	}

	var (
		done = make(chan struct{})
		errs error
		wg   sync.WaitGroup
	)

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

	wg.Add(1)

	go func() {
		defer wg.Done()

		scanner := bufio.NewScanner(stdout)
		scanner.Split(o.splitter)

		for scanner.Scan() {
			bytes := scanner.Bytes()

			stdoutBytes = append(stdoutBytes, bytes...)

			if o.stdout != nil {
				o.stdout <- bytes
			}
		}

		if err := scanner.Err(); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("scanning STDOUT: %w", err))
		}

		if o.stdout != nil {
			close(o.stdout)
		}
	}()

	wg.Add(1)

	go func() {
		defer wg.Done()

		scanner := bufio.NewScanner(stderr)
		scanner.Split(bufio.ScanLines)

		for scanner.Scan() {
			bytes := scanner.Bytes()

			stderrBytes = append(stderrBytes, bytes...)

			if o.stderr != nil {
				o.stderr <- bytes
			}
		}

		if err := scanner.Err(); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("scanning STDERR: %w", err))
		}

		if o.stderr != nil {
			close(o.stderr)
		}
	}()

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("waiting for command to complete: %w", err))
	}

	close(done)

	return stdoutBytes, stderrBytes, errs
}
