package disk

import (
	"maps"
	"slices"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
)

// TestDiskCommandsKeepPathsWhole compiles each disk command with minimega's own
// patterns and checks that every path comes back as one unchanged argument.
func TestDiskCommandsKeepPathsWhole(t *testing.T) {
	minicli.Reset()
	t.Cleanup(minicli.Reset)

	for _, pattern := range []string{
		"disk info <image> [recursive,]",
		"disk snapshot <image> [dst image]",
		"disk <rebase,> <image> [backing file]",
		"disk <set-backing,> <image> [backing file]",
		"disk commit <image>",
		"disk resize <image> <size>",
		"shell <command>...",
	} {
		minicli.MustRegister(&minicli.Handler{Patterns: []string{pattern}})
	}

	paths := []string{
		"/phenix/images/ok.qc2",
		"/phenix/images/placeholder /tmp/secret.qc2",
		`/phenix/images/a "quoted" 'name'.qc2`,
		`/phenix/images/back\slash\.qc2`,
		"/phenix/images/hash # not a comment.qc2",
		"/phenix/images/line\nbreak\r\tand tab.qc2",
	}

	const dst = "/phenix/images/new copy.qcow2"

	for _, p := range paths {
		tests := map[string]struct {
			line       string
			stringArgs map[string]string
			listArgs   []string
		}{
			"info":        {line: infoCommand(p), stringArgs: map[string]string{"image": p}},
			"commit":      {line: commitCommand(p), stringArgs: map[string]string{"image": p}},
			"snapshot":    {line: snapshotCommand(p, dst), stringArgs: map[string]string{"image": p, "dst": dst}},
			"rebase":      {line: rebaseCommand(p, dst, false), stringArgs: map[string]string{"image": p, "backing": dst}},
			"set-backing": {line: rebaseCommand(p, dst, true), stringArgs: map[string]string{"image": p, "backing": dst}},
			"standalone":  {line: rebaseCommand(p, "", false), stringArgs: map[string]string{"image": p}},
			"resize":      {line: resizeCommand(p, "+1G"), stringArgs: map[string]string{"image": p, "size": "+1G"}},
			"clone":       {line: cloneCommand(p, dst), listArgs: []string{"cp", p, dst}},
			"rename":      {line: renameCommand(p, dst), listArgs: []string{"mv", p, dst}},
			"delete":      {line: deleteCommand(p), listArgs: []string{"rm", p}},
		}

		for name, tc := range tests {
			cmd, err := minicli.Compile(tc.line)
			if err != nil {
				t.Fatalf("%s: minicli.Compile(%q) error = %v", name, tc.line, err)
			}

			if tc.listArgs != nil {
				if got := cmd.ListArgs["command"]; !slices.Equal(got, tc.listArgs) {
					t.Errorf("%s: minicli.Compile(%q) command = %q, want %q", name, tc.line, got, tc.listArgs)
				}

				continue
			}

			if !maps.Equal(cmd.StringArgs, tc.stringArgs) {
				t.Errorf("%s: minicli.Compile(%q) args = %q, want %q", name, tc.line, cmd.StringArgs, tc.stringArgs)
			}
		}
	}
}
