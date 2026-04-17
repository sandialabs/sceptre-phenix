package shell_test

import (
	"context"
	"os/exec"
	"testing"

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
