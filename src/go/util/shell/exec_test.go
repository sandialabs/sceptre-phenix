package shell_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/util/shell"
	"phenix/util/testenv"
)

// ExecCommand sends each line of a stream to its channel, without the line
// ending, and also returns the lines joined.
func TestExecCommandCopiesStreamedBytes(t *testing.T) {
	testenv.RequireCommands(t, "sh")

	tests := []struct {
		name       string
		script     string
		streamOpt  func(chan []byte) shell.Option
		want       []string
		wantStdout string
		wantStderr string
	}{
		{
			name:       "stdout",
			script:     "printf 'alpha\nbravo\ncharl\n'",
			streamOpt:  shell.StreamStdout,
			want:       []string{"alpha", "bravo", "charl"},
			wantStdout: "alphabravocharl",
		},
		{
			name:       "stderr",
			script:     "printf 'alpha\nbravo\ncharl\n' >&2",
			streamOpt:  shell.StreamStderr,
			want:       []string{"alpha", "bravo", "charl"},
			wantStderr: "alphabravocharl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := make(chan []byte, len(tt.want))

			stdout, stderr, err := shell.ExecCommand(
				t.Context(),
				shell.Command("sh"),
				shell.Args("-c", tt.script),
				tt.streamOpt(stream),
			)
			if err != nil {
				t.Fatalf("ExecCommand() error = %v, want nil", err)
			}

			var got []string
			for line := range stream {
				got = append(got, string(line))
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("streamed lines mismatch (-want +got):\n%s", diff)
			}

			if string(stdout) != tt.wantStdout || string(stderr) != tt.wantStderr {
				t.Errorf("ExecCommand() = %q, %q, want %q, %q", stdout, stderr, tt.wantStdout, tt.wantStderr)
			}
		})
	}
}

// ExecCommand gives the command its stdin and extra environment, returns its
// output, and reports a command that does not start or exits non-zero.
func TestExecCommandResult(t *testing.T) {
	testenv.RequireCommands(t, "sh", "cat")

	tests := []struct {
		name       string
		opts       []shell.Option
		wantStdout string
		wantStderr string
		wantErr    string // substring of the error; empty for no error
		wantExit   int    // exit code in the error; 0 for none
		wantErrIs  error
	}{
		{
			name:       "stdout and stderr",
			opts:       []shell.Option{shell.Command("sh"), shell.Args("-c", "printf 'out\n'; printf 'err\n' >&2")},
			wantStdout: "out",
			wantStderr: "err",
		},
		{
			name: "stdin and env",
			opts: []shell.Option{
				shell.Command("sh"),
				shell.Args("-c", `cat; printf '%s\n' "$PHENIX_SHELL_TEST"`),
				shell.Stdin([]byte("in\n")),
				shell.Env("PHENIX_SHELL_TEST=env"),
			},
			wantStdout: "inenv",
		},
		{
			name:       "split bytes keeps line endings",
			opts:       []shell.Option{shell.Command("sh"), shell.Args("-c", "printf 'a\nb'"), shell.SplitBytes()},
			wantStdout: "a\nb",
		},
		{
			name:       "non-zero exit",
			opts:       []shell.Option{shell.Command("sh"), shell.Args("-c", "printf 'out\n'; printf 'err\n' >&2; exit 3")},
			wantStdout: "out",
			wantStderr: "err",
			wantErr:    "waiting for command to complete",
			wantExit:   3,
		},
		{
			name:      "missing command",
			opts:      []shell.Option{shell.Command("phenix-shell-test-no-such-command")},
			wantErr:   "starting command",
			wantErrIs: exec.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := shell.ExecCommand(t.Context(), tt.opts...)

			if string(stdout) != tt.wantStdout || string(stderr) != tt.wantStderr {
				t.Errorf("ExecCommand() = %q, %q, want %q, %q", stdout, stderr, tt.wantStdout, tt.wantStderr)
			}

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ExecCommand() error = %v, want nil", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ExecCommand() error = %v, want one that contains %q", err, tt.wantErr)
			}

			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("ExecCommand() error = %v, want one that wraps %v", err, tt.wantErrIs)
			}

			if tt.wantExit != 0 {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != tt.wantExit {
					t.Errorf("ExecCommand() error = %v, want an exit error with code %d", err, tt.wantExit)
				}
			}
		})
	}
}

// Canceling the context sends SIGTERM to the command, so that it can stop
// gracefully, and ExecCommand returns the context error.
func TestExecCommandCancelSendsSIGTERM(t *testing.T) {
	testenv.RequireCommands(t, "sh", "sleep")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type result struct {
		stdout []byte
		err    error
	}

	stream := make(chan []byte, 4)
	done := make(chan result, 1)

	go func() {
		stdout, _, err := shell.ExecCommand(
			ctx,
			shell.Command("sh"),
			shell.Args("-c", "trap 'echo stopped; exit 0' TERM; echo ready; while :; do sleep 0.1; done"),
			shell.StreamStdout(stream),
		)

		done <- result{stdout: stdout, err: err}
	}()

	select {
	case line := <-stream:
		if string(line) != "ready" {
			t.Fatalf("first streamed line = %q, want ready", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command streamed nothing for 10s, want ready")
	}

	cancel()

	select {
	case got := <-done:
		if string(got.stdout) != "readystopped" {
			t.Errorf("ExecCommand() stdout = %q, want readystopped from the SIGTERM trap", got.stdout)
		}

		if !errors.Is(got.err, context.Canceled) {
			t.Errorf("ExecCommand() error = %v, want one that wraps context.Canceled", got.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("ExecCommand() did not return 8s after cancel, want it to return after SIGTERM")
	}
}

// ExecCommand delivers whole a line longer than the 64 KiB [bufio.Scanner]
// default, and reports a line longer than its own limit. After that error it
// reads the rest of the output, so the command does not block on a full pipe.
func TestExecCommandLongLines(t *testing.T) {
	testenv.RequireCommands(t, "sh")

	const (
		long    = 100 << 10 // over bufio.MaxScanTokenSize
		tooLong = 2 << 20   // over the ExecCommand line limit
	)

	lines := fmt.Sprintf("printf '%%%ds\\nafter\\n' ''; printf '%%%ds\\nend\\n' ''", long, tooLong)
	longLine := strings.Repeat(" ", long)

	tests := []struct {
		name       string
		script     string
		streamOpt  func(chan []byte) shell.Option
		want       []string
		wantStdout string
		wantStderr string
	}{
		{
			name:       "stdout",
			script:     lines,
			streamOpt:  shell.StreamStdout,
			want:       []string{longLine, "after"},
			wantStdout: longLine + "after",
		},
		{
			name:       "stderr",
			script:     "{ " + lines + "; } >&2",
			streamOpt:  shell.StreamStderr,
			want:       []string{longLine, "after"},
			wantStderr: longLine + "after",
		},
		{
			name:      "stdout and stderr at the same time",
			script:    fmt.Sprintf("printf '%%%ds\\n' '' >&2 & printf '%%%ds\\n' ''; wait", tooLong, tooLong),
			streamOpt: shell.StreamStdout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			type result struct {
				stdout, stderr []byte
				err            error
			}

			stream := make(chan []byte, 4)
			done := make(chan result, 1)

			go func() {
				stdout, stderr, err := shell.ExecCommand(ctx, shell.Command("sh"), shell.Args("-c", tt.script), tt.streamOpt(stream))
				done <- result{stdout: stdout, stderr: stderr, err: err}
			}()

			var got result

			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("ExecCommand() did not return in 5s, want it to read all output so the command exits")
			}

			var streamed []string
			for line := range stream {
				streamed = append(streamed, string(line))
			}

			if diff := cmp.Diff(tt.want, streamed); diff != "" {
				t.Errorf("streamed lines mismatch (-want +got):\n%s", diff)
			}

			if string(got.stdout) != tt.wantStdout || string(got.stderr) != tt.wantStderr {
				t.Errorf("ExecCommand() output lengths = %d, %d, want %d, %d",
					len(got.stdout), len(got.stderr), len(tt.wantStdout), len(tt.wantStderr))
			}

			if !errors.Is(got.err, bufio.ErrTooLong) {
				t.Errorf("ExecCommand() error = %v, want one that wraps bufio.ErrTooLong", got.err)
			}

			var exitErr *exec.ExitError
			if errors.As(got.err, &exitErr) {
				t.Errorf("ExecCommand() error = %v, want no exit error: the command exits 0", got.err)
			}
		})
	}
}

// execResult is the result of an ExecCommand call.
type execResult struct {
	stdout []byte
	err    error
}

// startExec runs script with sh through sh, in the background, with stdout
// streamed. It returns the first line of stdout, a process ID, and the
// channel that gets the result.
func startExec(ctx context.Context, t *testing.T, sh shell.Shell, script string) (int, <-chan execResult) {
	t.Helper()

	stream := make(chan []byte, 16)
	done := make(chan execResult, 1)

	go func() {
		stdout, _, err := sh.ExecCommand(
			ctx, shell.Command("sh"), shell.Args("-c", script), shell.Stdin([]byte{}), shell.StreamStdout(stream),
		)
		done <- execResult{stdout: stdout, err: err}
	}()

	select {
	case line := <-stream:
		pid, err := strconv.Atoi(string(line))
		if err != nil {
			t.Fatalf("first streamed line = %q, want a process ID", line)
		}

		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

		go func() {
			for range stream { //nolint:revive // drains the stream so the reads do not block
			}
		}()

		return pid, done
	case <-time.After(10 * time.Second):
		t.Fatal("command streamed nothing for 10s, want a process ID")
	}

	return 0, nil
}

// processGone returns an error if pid still runs 5s from now. A zombie
// counts as running, so this also waits for the process to be reaped.
func processGone(pid int) error {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !shell.ProcessExists(pid) {
			return nil
		}
	}

	return fmt.Errorf("process %d still runs 5s after ExecCommand returned", pid)
}

// Canceling the context stops the whole process group of the command: a child
// that holds the output pipes open does not keep ExecCommand from returning,
// and no process of the group is left.
func TestExecCommandCancelStopsProcessGroup(t *testing.T) {
	testenv.RequireCommands(t, "sh", "sleep")

	// the child prints its ID once it runs, as bash can lose a signal between fork and exec
	const child = `sh -c 'echo $$; exec sleep 30' &`

	tests := []struct {
		name      string
		script    string
		killDelay time.Duration
	}{
		{
			// SIGTERM to the group is sufficient, so the long delay does not apply.
			name:      "child holds the pipes",
			script:    child + " wait",
			killDelay: 20 * time.Second,
		},
		{
			name:      "child ignores SIGTERM",
			script:    "trap '' TERM; " + child + " wait",
			killDelay: 200 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			pid, done := startExec(ctx, t, shell.WithKillDelay(tt.killDelay), tt.script)

			cancel()

			select {
			case got := <-done:
				if !errors.Is(got.err, context.Canceled) {
					t.Errorf("ExecCommand() error = %v, want one that wraps context.Canceled", got.err)
				}

				if errors.Is(got.err, exec.ErrWaitDelay) {
					t.Errorf("ExecCommand() error = %v, want no exec.ErrWaitDelay: the pipes closed", got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("ExecCommand() did not return 5s after cancel, want it to stop the process group")
			}

			if err := processGone(pid); err != nil {
				t.Error(err)
			}
		})
	}
}

// A process that leaves the process group, and so gets no signal, cannot keep
// ExecCommand from returning: after the kill delay, ExecCommand stops the
// read of the output and returns what it read.
func TestExecCommandStopsReadsAfterKillDelay(t *testing.T) {
	testenv.RequireCommands(t, "sh", "sleep", "setsid")

	const (
		killDelay = 300 * time.Millisecond
		// the process prints its ID after it leaves the group, and stays
		left = `setsid sh -c 'echo $$; exec sleep 30' &`
	)

	tests := []struct {
		name      string
		script    string
		cancel    bool
		wantErrIs []error
	}{
		{
			name:      "canceled",
			script:    left + " wait",
			cancel:    true,
			wantErrIs: []error{context.Canceled, exec.ErrWaitDelay},
		},
		{
			name:      "command exits",
			script:    left,
			wantErrIs: []error{exec.ErrWaitDelay},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			pid, done := startExec(ctx, t, shell.WithKillDelay(killDelay), tt.script)

			if tt.cancel {
				cancel()
			}

			select {
			case got := <-done:
				if want := strconv.Itoa(pid); string(got.stdout) != want {
					t.Errorf("ExecCommand() stdout = %q, want %q", got.stdout, want)
				}

				for _, want := range tt.wantErrIs {
					if !errors.Is(got.err, want) {
						t.Errorf("ExecCommand() error = %v, want one that wraps %v", got.err, want)
					}
				}

				if errors.Is(got.err, os.ErrClosed) {
					t.Errorf("ExecCommand() error = %v, want no os.ErrClosed from the stopped read", got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("ExecCommand() did not return 5s after start, want it to stop the reads after %v", killDelay)
			}

			if !shell.ProcessExists(pid) {
				t.Errorf("ProcessExists(%d) = false, want true: a process outside the group gets no signal", pid)
			}
		})
	}
}

// Without a cancel, ExecCommand reads all the output, also what a child
// writes after the command exits.
func TestExecCommandReadsChildOutput(t *testing.T) {
	testenv.RequireCommands(t, "sh", "sleep")

	stream := make(chan []byte, 4)

	stdout, stderr, err := shell.ExecCommand(
		t.Context(),
		shell.Command("sh"),
		shell.Args("-c", "echo parent; (sleep 0.3; echo child; echo child >&2) &"),
		shell.Stdin([]byte{}),
		shell.StreamStdout(stream),
	)
	if err != nil {
		t.Fatalf("ExecCommand() error = %v, want nil", err)
	}

	var got []string
	for line := range stream {
		got = append(got, string(line))
	}

	if diff := cmp.Diff([]string{"parent", "child"}, got); diff != "" {
		t.Errorf("streamed lines mismatch (-want +got):\n%s", diff)
	}

	if string(stdout) != "parentchild" || string(stderr) != "child" {
		t.Errorf("ExecCommand() = %q, %q, want %q, %q", stdout, stderr, "parentchild", "child")
	}
}
