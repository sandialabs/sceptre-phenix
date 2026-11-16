package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/spf13/cobra"

	"phenix/version"
)

// TestVersionCmd checks that phenix version writes the label, commit and date
// of the build to the command writer.
func TestVersionCmd(t *testing.T) { //nolint:paralleltest // sets the build info in version
	commit, date := version.Commit, version.Date
	t.Cleanup(func() { version.Commit, version.Date = commit, date }) //nolint:reassign // restore build info

	version.Commit, version.Date = "0123456789abcdef", "2026-01-02T03:04:05Z" //nolint:reassign // known build info

	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newVersionCmd())
	root.SetArgs([]string{"version"})

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(io.Discard)

	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("phenix version error = %v", err)
	}

	want := version.Label() + " (commit 0123456789abcdef) 2026-01-02T03:04:05Z\n"
	if got := output.String(); got != want {
		t.Errorf("phenix version = %q, want %q", got, want)
	}
}
