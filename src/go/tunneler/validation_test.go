package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// Each tunneler command accepts only its documented number of arguments.
func TestTunnelerCommandArgumentCounts(t *testing.T) {
	tests := []struct {
		name    string
		command cobra.PositionalArgs
		args    []string
		wantErr bool
	}{
		{name: "list", command: listCmd.Args, args: nil},
		{name: "list extra", command: listCmd.Args, args: []string{"unexpected"}, wantErr: true},
		{name: "activate", command: activateCmd.Args, args: []string{"1"}},
		{name: "activate missing", command: activateCmd.Args, args: nil, wantErr: true},
		{name: "activate extra", command: activateCmd.Args, args: []string{"1", "2"}, wantErr: true},
		{name: "deactivate", command: deactivateCmd.Args, args: []string{"1"}},
		{name: "deactivate missing", command: deactivateCmd.Args, args: nil, wantErr: true},
		{name: "deactivate extra", command: deactivateCmd.Args, args: []string{"1", "2"}, wantErr: true},
		{name: "move", command: moveCmd.Args, args: []string{"1", "2"}},
		{name: "move missing", command: moveCmd.Args, args: []string{"1"}, wantErr: true},
		{name: "move extra", command: moveCmd.Args, args: []string{"1", "2", "3"}, wantErr: true},
		{name: "serve", command: serveCmd.Args, args: []string{"https://phenix.example.test"}},
		{name: "serve missing", command: serveCmd.Args, args: nil, wantErr: true},
		{name: "serve extra", command: serveCmd.Args, args: []string{"https://a.example.test", "https://b.example.test"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.command(&cobra.Command{Use: test.name}, test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("Args(%q) = %v, want error %t", test.args, err, test.wantErr)
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
