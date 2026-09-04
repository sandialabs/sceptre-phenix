package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestTunnelerCommandArgumentCounts(t *testing.T) {
	tests := []struct {
		name    string
		command cobra.PositionalArgs
		args    []string
	}{
		{name: "list missing", command: listCmd.Args, args: []string{"unexpected"}},
		{name: "activate missing", command: activateCmd.Args, args: nil},
		{name: "activate extra", command: activateCmd.Args, args: []string{"1", "2"}},
		{name: "deactivate missing", command: deactivateCmd.Args, args: nil},
		{name: "deactivate extra", command: deactivateCmd.Args, args: []string{"1", "2"}},
		{name: "move missing", command: moveCmd.Args, args: []string{"1"}},
		{name: "move extra", command: moveCmd.Args, args: []string{"1", "2", "3"}},
		{name: "serve missing", command: serveCmd.Args, args: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.command(&cobra.Command{Use: test.name}, test.args); err == nil {
				t.Fatal("expected argument validation error")
			}
		})
	}
}

func TestListenerIDValidation(t *testing.T) {
	for _, value := range []string{"", "abc", "0", "-1"} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseListenerID(value); err == nil {
				t.Fatalf("parseListenerID(%q) returned nil error", value)
			}
		})
	}
}

func TestLocalPortValidation(t *testing.T) {
	for _, value := range []string{"", "abc", "0", "-1", "65536"} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseLocalPort(value); err == nil {
				t.Fatalf("parseLocalPort(%q) returned nil error", value)
			}
		})
	}

	for _, value := range []string{"1", "65535"} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseLocalPort(value); err != nil {
				t.Fatalf("parseLocalPort(%q) returned error: %v", value, err)
			}
		})
	}
}
