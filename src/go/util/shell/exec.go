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
	"sync/atomic"
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

type shell struct {
	// killDelay replaces the package killDelay when it is more than zero.
	killDelay time.Duration
}

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

// ExecCommand runs the command that opts name in a new process group. It
// returns the stdout and stderr of the command. When ctx is done, it sends
// SIGTERM to the group, so that the command can stop gracefully. Then,
// killDelay later, it sends SIGKILL to the group. A process that left the
// group can keep the output pipes open. Thus ExecCommand stops the read of the
// output killDelay after the command exits or after the SIGKILL, and then its
// error wraps [exec.ErrWaitDelay].
func (s shell) ExecCommand(ctx context.Context, opts ...Option) ([]byte, []byte, error) {
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

	pipes, err := newOutputPipes()
	if err != nil {
		return nil, nil, err
	}

	defer pipes.close()

	var signaled atomic.Bool

	cmd := exec.CommandContext(ctx, o.cmd, o.args...) //nolint:gosec // G204 callers name the command

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} //nolint:exhaustruct // only a new process group
	cmd.Cancel = sync.OnceValue(func() error {
		err := signalGroup(cmd.Process.Pid, syscall.SIGTERM)
		signaled.Store(err == nil)

		return err
	})
	cmd.WaitDelay = s.delay()

	cmd.Stdin = stdIn
	cmd.Stdout = pipes.stdoutW
	cmd.Stderr = pipes.stderrW

	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, o.env...)

	err = cmd.Start()

	pipes.closeWriters()

	if err != nil {
		return nil, nil, fmt.Errorf("starting command: %w", err)
	}

	var (
		errs                 error
		stdoutErr, stderrErr error
		wg                   sync.WaitGroup
		read                 = make(chan struct{})
		waited               = make(chan error, 1)
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		stdoutBytes, stdoutErr = readStream(pipes.stdoutR, o.splitter, o.stdout, "STDOUT")
	}()

	wg.Add(1)

	go func() {
		defer wg.Done()

		stderrBytes, stderrErr = readStream(pipes.stderrR, bufio.ScanLines, o.stderr, "STDERR")
	}()

	go func() {
		wg.Wait()
		close(read)
	}()

	go func() { waited <- cmd.Wait() }()

	cut, err := await(ctx, cmd, waited, read, s.delay(), pipes.closeReaders)

	for _, e := range []error{stdoutErr, stderrErr} {
		if e != nil && (!cut || !errors.Is(e, os.ErrClosed)) {
			errs = multierror.Append(errs, e)
		}
	}

	if err != nil {
		errs = multierror.Append(errs, fmt.Errorf("waiting for command to complete: %w", err))
	}

	if ctxErr := ctx.Err(); signaled.Load() && ctxErr != nil && !errors.Is(errs, ctxErr) {
		errs = multierror.Append(errs, ctxErr)
	}

	if cut {
		errs = multierror.Append(errs, fmt.Errorf("reading output: %w", exec.ErrWaitDelay))
	}

	return stdoutBytes, stderrBytes, errs
}

// delay returns the time from SIGTERM to SIGKILL, and from the end of the
// command to the end of the read of its output.
func (s shell) delay() time.Duration {
	if s.killDelay > 0 {
		return s.killDelay
	}

	return killDelay
}

// await returns after the command exits and read is closed, with the error of
// cmd.Wait from waited. When ctx is done, it sends SIGTERM to the process group of
// cmd, and delay later SIGKILL. If read is not closed delay after the command
// exits or after the SIGKILL, it calls stopReads and cut is true.
func await(
	ctx context.Context, cmd *exec.Cmd, waited <-chan error, read <-chan struct{}, delay time.Duration, stopReads func(),
) (bool, error) {
	var (
		err       error
		cut       bool
		done      = ctx.Done()
		killTimer = newStoppedTimer()
		cutTimer  = newStoppedTimer()
		cutArmed  bool
	)

	defer killTimer.Stop()
	defer cutTimer.Stop()

	for waited != nil || read != nil {
		select {
		case <-done:
			done = nil

			// exec calls Cancel only while the process runs; the group can outlive it
			_ = cmd.Cancel()

			killTimer.Reset(delay)
		case <-killTimer.C:
			_ = signalGroup(cmd.Process.Pid, syscall.SIGKILL)

			// give the reads time to get what the killed processes wrote
			cutTimer.Reset(delay)
			cutArmed = true
		case err = <-waited:
			waited = nil

			if !cutArmed {
				cutTimer.Reset(delay)
				cutArmed = true
			}
		case <-read:
			read = nil
		case <-cutTimer.C:
			if read != nil {
				cut = true

				stopReads()
			}
		}
	}

	return cut, err
}

// newStoppedTimer returns a timer that does not fire until a Reset.
func newStoppedTimer() *time.Timer {
	t := time.NewTimer(time.Hour)
	t.Stop()

	return t
}

// signalGroup sends sig to the process group of pid. It returns
// [os.ErrProcessDone] when no process of the group is left.
func signalGroup(pid int, sig syscall.Signal) error {
	err := syscall.Kill(-pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}

	if err != nil {
		return fmt.Errorf("sending %v to process group %d: %w", sig, pid, err)
	}

	return nil
}

// outputPipes are the pipes for the stdout and stderr of a command. The
// command gets the write ends, and ExecCommand reads the read ends. Unlike
// [exec.Cmd.StdoutPipe], cmd.Wait does not close them, so ExecCommand can
// wait for the command while it reads.
type outputPipes struct {
	stdoutR, stdoutW, stderrR, stderrW *os.File
}

func newOutputPipes() (*outputPipes, error) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("creating STDOUT pipe: %w", err)
	}

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()

		return nil, fmt.Errorf("creating STDERR pipe: %w", err)
	}

	return &outputPipes{stdoutR: stdoutR, stdoutW: stdoutW, stderrR: stderrR, stderrW: stderrW}, nil
}

// closeWriters closes the write ends. The started command keeps its copies.
func (p *outputPipes) closeWriters() {
	_ = p.stdoutW.Close()
	_ = p.stderrW.Close()
}

// closeReaders closes the read ends, which stops a read that waits on them.
func (p *outputPipes) closeReaders() {
	_ = p.stdoutR.Close()
	_ = p.stderrR.Close()
}

func (p *outputPipes) close() {
	p.closeWriters()
	p.closeReaders()
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
