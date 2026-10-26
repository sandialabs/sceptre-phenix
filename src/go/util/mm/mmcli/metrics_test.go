package mmcli

import (
	"context"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"pgregory.net/rapid"

	"phenix/util/metrics"
	"phenix/util/metrics/metricstest"
	"phenix/util/mm/mmtest"
)

func TestCommandLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command, want string
	}{
		{"vm info", "vm info"},
		{"vm start secret-vm", "vm start"},
		{"vm secret-vm", "vm"},
		{"mesh send all vm info", "mesh send"},
		{"namespace secret-exp vm launch kvm secret-vm", "vm launch"},
		{`.columns "name","state" vm info`, "vm info"},
		{".filter name=secret-vm .record false vm kill secret-vm", "vm kill"},
		{"router secret-vm commit", "router"},
		{"shell rm -rf /secret", "shell"},
		{"read /secret/file.mm", "read"},
		{"secret-command arg", metrics.Other},
		{"namespace secret-exp", metrics.Other},
		{".record false", metrics.Other},
		{"", metrics.Other},
	}

	for _, tt := range tests {
		if got := commandLabel(tt.command); got != tt.want {
			t.Errorf("commandLabel(%q) = %q, want %q", tt.command, got, tt.want)
		}
	}
}

// Whatever a command holds, its label is a command or subcommand named in subcommands, or "other".
func TestCommandLabelIsBounded(t *testing.T) {
	t.Parallel()

	labels := map[string]bool{metrics.Other: true}

	for command, subs := range subcommands {
		labels[command] = true

		for _, sub := range subs {
			labels[command+" "+sub] = true
		}
	}

	words := rapid.SampledFrom([]string{"vm", "info", "namespace", ".columns", "cc", "mesh", "send", "secret", "a b", ""})

	rapid.Check(t, func(t *rapid.T) {
		command := strings.Join(rapid.SliceOf(rapid.OneOf(words, rapid.String())).Draw(t, "words"), " ")

		if got := commandLabel(command); !labels[got] {
			t.Fatalf("commandLabel(%q) = %q, which is not a command named in subcommands", command, got)
		}
	})
}

// Each command counts once by its label and outcome, with its latency when it
// was sent.
func TestRunRecordsCommandMetrics(t *testing.T) {
	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case "vm kill secret-vm":
			return []*minicli.Response{{Host: "fake", Error: "vm not found: secret-vm"}}
		case "break":
			return mmtest.Disconnect()
		}

		return reply("ok")
	})

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		run     func()
		command string
		outcome string
		timed   bool
	}{
		{"answered", func() { _ = ErrorResponse(Run(command("vm info"))) }, "vm info", metrics.OK, true},
		{"refused", func() { _ = ErrorResponse(Run(command("vm kill secret-vm"))) }, "vm kill", metrics.CommandError, true},
		{"lost", func() { _ = ErrorResponse(Run(command("break"))) }, metrics.Other, metrics.CommandLost, true},
		{
			"not sent", func() { _ = ErrorResponse(RunContext(canceled, command("vm start secret-vm"))) },
			"vm start", metrics.CommandNotSent, false,
		},
		{"dedicated", func() { _ = ErrorResponse(RunDedicated(command("disk info x"))) }, "disk info", metrics.OK, true},
	}

	for _, tt := range tests {
		var (
			counted = map[string]string{"command": tt.command, "outcome": tt.outcome}
			timed   = map[string]string{"command": tt.command}

			countBefore = metricstest.Value(t, "phenix_minimega_commands_total", counted)
			timedBefore = metricstest.Value(t, "phenix_minimega_command_duration_seconds", timed)
		)

		tt.run()

		if got := metricstest.Value(t, "phenix_minimega_commands_total", counted) - countBefore; got != 1 {
			t.Errorf("%s: phenix_minimega_commands_total %v moved by %v, want 1", tt.name, counted, got)
		}

		want := 0.0
		if tt.timed {
			want = 1
		}

		if got := metricstest.Value(t, "phenix_minimega_command_duration_seconds", timed) - timedBefore; got != want {
			t.Errorf("%s: phenix_minimega_command_duration_seconds %v moved by %v, want %v", tt.name, timed, got, want)
		}
	}
}
