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

const killDelay = 10 * time.Second

type shell struct{}

func (shell) FindCommandsWithPrefix(prefix string) []string {
	var commands []string

	args := strings.Split(os.Getenv("PATH"), ":")
	args = append(args, "-type", "f", "-executable", "-name", prefix+"*")

	cmd := exec.Command("find", args...) //nolint:noctx,gosec // simple find command, Command injection via taint analysis

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	for c := range strings.SplitSeq(string(out), "\n") {
		if c != "" {
			base := filepath.Base(c)
			commands = append(commands, strings.TrimPrefix(base, prefix))
		}
	}

	return commands
}

func (shell) CommandExists(cmd string) bool {
	err := exec.Command("which", cmd).Run() //nolint:noctx // simple which command

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

	var errno syscall.Errno

	ok := errors.As(err, &errno)
	if !ok {
		return false
	}

	switch errno { //nolint:exhaustive // too many errors to list
	case syscall.ESRCH:
		return false
	case syscall.EPERM:
		return true
	default:
		return false
	}
}

//nolint:funlen,noctx // owns process-group cancellation and output streams
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

	// Own the process group so cancellation also stops grandchildren holding
	// output pipes open. The watcher sends TERM, then KILL after the grace period.
	cmd := exec.Command(
		o.cmd,
		o.args...) //nolint:gosec // cancellation is handled for the entire process group below; Subprocess launched with a potential tainted input

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} //nolint:exhaustruct // other platform settings keep their defaults
	cmd.Stdin = stdIn
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, o.env...)

	err := cmd.Start()
	if err != nil {
		if o.stdout != nil {
			close(o.stdout)
		}
		if o.stderr != nil {
			close(o.stderr)
		}
		return nil, nil, fmt.Errorf("starting command: %w", err)
	}

	var (
		done     = make(chan struct{})
		errs     error
		errorsMu sync.Mutex
		wg       sync.WaitGroup
	)

	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

			select {
			case <-done:
				return
			case <-time.After(killDelay):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		}
	}()

	wg.Add(1)

	go func() {
		defer wg.Done()

		scanner := bufio.NewScanner(stdout)
		scanner.Split(o.splitter)

		for scanner.Scan() {
			line := scanner.Bytes()

			stdoutBytes = append(stdoutBytes, line...)

			if o.stdout != nil {
				o.stdout <- bytes.Clone(line)
			}
		}

		err := scanner.Err()
		if err != nil {
			errorsMu.Lock()
			errs = multierror.Append(errs, fmt.Errorf("scanning STDOUT: %w", err))
			errorsMu.Unlock()
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
			line := scanner.Bytes()

			stderrBytes = append(stderrBytes, line...)

			if o.stderr != nil {
				o.stderr <- bytes.Clone(line)
			}
		}

		err := scanner.Err()
		if err != nil {
			errorsMu.Lock()
			errs = multierror.Append(errs, fmt.Errorf("scanning STDERR: %w", err))
			errorsMu.Unlock()
		}

		if o.stderr != nil {
			close(o.stderr)
		}
	}()

	wg.Wait()

	err = cmd.Wait()
	if err != nil {
		errs = multierror.Append(errs, fmt.Errorf("waiting for command to complete: %w", err))
	}

	close(done)
	if ctx.Err() != nil {
		errs = multierror.Append(errs, ctx.Err())
	}
	return stdoutBytes, stderrBytes, errs
}
