package shell_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"phenix/util/shell"
)

func TestExecCommandCopiesStreamedBytes(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	tests := []struct {
		name      string
		script    string
		streamOpt func(chan []byte) shell.Option
		want      []string
	}{
		{
			name:      "stdout",
			script:    "printf 'alpha\nbravo\ncharl\n'",
			streamOpt: shell.StreamStdout,
			want:      []string{"alpha", "bravo", "charl"},
		},
		{
			name:      "stderr",
			script:    "printf 'alpha\nbravo\ncharl\n' >&2",
			streamOpt: shell.StreamStderr,
			want:      []string{"alpha", "bravo", "charl"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := make(chan []byte, len(tt.want))

			_, _, err := shell.ExecCommand(
				context.Background(),
				shell.Command("sh"),
				shell.Args("-c", tt.script),
				tt.streamOpt(stream),
			)
			if err != nil {
				t.Fatalf("ExecCommand returned error: %v", err)
			}

			var got [][]byte
			for line := range stream {
				got = append(got, line)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("expected %d lines, got %d", len(tt.want), len(got))
			}

			for i, want := range tt.want {
				if string(got[i]) != want {
					t.Fatalf("line %d: expected %q, got %q", i, want, string(got[i]))
				}
			}
		})
	}
}

func TestExecCommandHandlesLinesLongerThanDefaultToken(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	// a single line longer than bufio.MaxScanTokenSize (64 KiB) used to stop
	// the scanner, leaving the child blocked writing to a full pipe
	const lineLen = 128 * 1024

	stream := make(chan []byte, 1)

	stdout, _, err := shell.ExecCommand(
		context.Background(),
		shell.Command("sh"),
		shell.Args("-c", "head -c 131072 /dev/zero | tr '\\0' 'x'; echo"),
		shell.StreamStdout(stream),
	)
	if err != nil {
		t.Fatalf("ExecCommand returned error: %v", err)
	}

	if len(stdout) != lineLen {
		t.Fatalf("expected %d bytes on stdout, got %d", lineLen, len(stdout))
	}

	var streamed int
	for line := range stream {
		streamed += len(line)
	}

	if streamed != lineLen {
		t.Fatalf("expected %d streamed bytes, got %d", lineLen, streamed)
	}
}

func TestExecCommandClosesStreamsWhenStartFails(t *testing.T) {
	stdout := make(chan []byte)
	stderr := make(chan []byte)

	done := make(chan struct{})

	go func() {
		defer close(done)

		// both channels must be closed even though the command never starts,
		// so consumers ranging over them still terminate
		for range stdout { //nolint:revive // draining until closed
		}
		for range stderr { //nolint:revive // draining until closed
		}
	}()

	_, _, err := shell.ExecCommand(
		context.Background(),
		shell.Command("/nonexistent-command-for-test"),
		shell.StreamStdout(stdout),
		shell.StreamStderr(stderr),
	)
	if err == nil {
		t.Fatal("expected error starting nonexistent command, got nil")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream channels were not closed after start failure")
	}
}

func TestExecCommandDoesNotHangOnOversizedLine(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	done := make(chan error, 1)

	go func() {
		// a single line larger than the scanner's max token size (1 MiB) fails
		// the scan, but the pipe must still be drained so the child can exit
		_, _, err := shell.ExecCommand(
			context.Background(),
			shell.Command("sh"),
			shell.Args("-c", "head -c 2097152 /dev/zero | tr '\\0' 'x'; echo"),
		)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected scanning error for oversized line, got nil")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("ExecCommand hung on oversized line")
	}
}
