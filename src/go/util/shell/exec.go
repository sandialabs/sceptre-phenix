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
	"golang.org/x/sys/unix"
)

const killDelay = 10 * time.Second

// maxLineSize is the length, in bytes, of the longest line that ExecCommand
// reads from the output of a command. A longer line stops the read of that
// stream with [bufio.ErrTooLong].
const maxLineSize = 1 << 20

type shell struct{}

// FindCommandsWithPrefix returns the names, without prefix, of the regular
// files on PATH whose names start with prefix and that the current user can
// execute, as access(2) with X_OK tells. The first PATH folder wins for a
// duplicate name. It skips empty PATH entries and folders it cannot read.
func (shell) FindCommandsWithPrefix(prefix string) []string {
	var (
		commands []string
		seen     = make(map[string]struct{})
	)

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			name, ok := strings.CutPrefix(entry.Name(), prefix)
			if !ok || name == "" {
				continue
			}

			if _, ok := seen[name]; ok {
				continue
			}

			path := filepath.Join(dir, entry.Name())

			info, err := os.Stat(path) //nolint:gosec // G703 PATH names the folders to search
			if err != nil || !info.Mode().IsRegular() || unix.Access(path, unix.X_OK) != nil {
				continue
			}

			seen[name] = struct{}{}
			commands = append(commands, name)
		}
	}

	return commands
}

// CommandExists tells if cmd names an executable file, as [exec.LookPath] finds
// it. A name with a slash is a path. Other names are found on PATH. A name
// found only through a relative PATH entry does not exist, because exec does
// not run it.
func (shell) CommandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)

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

	// When ctx is canceled, send SIGTERM so that the child process can stop
	// gracefully, and kill it if it still runs after killDelay.
	cmd := exec.CommandContext(ctx, o.cmd, o.args...) //nolint:gosec // G204 callers name the command
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = killDelay

	cmd.Stdin = stdIn
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, o.env...)

	err := cmd.Start()
	if err != nil {
		return nil, nil, fmt.Errorf("starting command: %w", err)
	}

	var (
		errs                 error
		stdoutErr, stderrErr error
		wg                   sync.WaitGroup
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		stdoutBytes, stdoutErr = readStream(stdout, o.splitter, o.stdout, "STDOUT")
	}()

	wg.Add(1)

	go func() {
		defer wg.Done()

		stderrBytes, stderrErr = readStream(stderr, bufio.ScanLines, o.stderr, "STDERR")
	}()

	wg.Wait()

	for _, e := range []error{stdoutErr, stderrErr} {
		if e != nil {
			errs = multierror.Append(errs, e)
		}
	}

	err = cmd.Wait()
	if err != nil {
		errs = multierror.Append(errs, fmt.Errorf("waiting for command to complete: %w", err))
	}

	return stdoutBytes, stderrBytes, errs
}

// readStream reads the tokens that split finds in pipe. It sends a copy of
// each token to stream, when stream is not nil, and closes stream at the end.
// It returns the tokens joined. After a scan error, it reads the rest of pipe,
// so that the command does not block on a full pipe.
func readStream(pipe io.Reader, split bufio.SplitFunc, stream chan []byte, name string) ([]byte, error) {
	if stream != nil {
		defer close(stream)
	}

	var data []byte

	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(nil, maxLineSize)
	scanner.Split(split)

	for scanner.Scan() {
		line := scanner.Bytes()

		data = append(data, line...)

		if stream != nil {
			stream <- bytes.Clone(line)
		}
	}

	err := scanner.Err()
	if err != nil {
		_, _ = io.Copy(io.Discard, pipe)

		return data, fmt.Errorf("scanning %s: %w", name, err)
	}

	return data, nil
}
