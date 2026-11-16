package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"phenix/api/runtimesettings"
)

const deprecationsDocPath = "../../../docs/content/deprecations.md"

// deprecationTestRun runs the subcommand of a root command "phenix" with
// args, and returns its output, its error output and its error.
func deprecationTestRun(sub *cobra.Command, args ...string) (string, string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(sub)
	root.SetArgs(args)

	var stdout, stderr bytes.Buffer

	root.SetOut(&stdout)
	root.SetErr(&stderr)

	err := root.Execute()

	return stdout.String(), stderr.String(), err
}

func TestWarnDeprecated(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	warnDeprecated(&buf, "phenix old", "phenix new")

	want := "phenix: warning: phenix old is deprecated and will be removed in a future release; use phenix new instead.\n"
	if diff := cmp.Diff(want, buf.String()); diff != "" {
		t.Errorf("warnDeprecated mismatch (-want +got):\n%s", diff)
	}
}

// A deprecated flag still works, warns at each use, and the help does not show it.
func TestDeprecateFlag(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		args       []string
		wantOut    string
		wantStderr string
	}{
		{name: "not used", args: []string{"show"}, wantOut: "new=,old=false\n"},
		{
			name: "string flag", args: []string{"show", "--new", "a"}, wantOut: "new=a,old=false\n",
			wantStderr: "phenix: warning: phenix show -n/--new is deprecated and will be removed in a future release; " +
				"use phenix show --newer instead.\n",
		},
		{
			name: "bool flag", args: []string{"show", "--old"}, wantOut: "new=,old=true\n",
			wantStderr: "phenix: warning: phenix show --old is deprecated and will be removed in a future release; " +
				"use phenix show --newer instead.\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := &cobra.Command{
				Use: "show",
				RunE: func(cmd *cobra.Command, _ []string) error {
					cmd.Printf("new=%s,old=%t\n", mustGet(cmd.Flags().GetString, "new"), mustGet(cmd.Flags().GetBool, "old"))

					return nil
				},
			}
			cmd.Flags().StringP("new", "n", "", "a string")
			cmd.Flags().Bool("old", false, "a bool")
			cmd.Flags().String("newer", "", "the replacement")
			deprecateFlag(cmd, "new", "--newer")
			deprecateFlag(cmd, "old", "--newer")

			out, stderr, err := deprecationTestRun(cmd, tt.args...)
			if err != nil {
				t.Fatalf("phenix %q error = %v", tt.args, err)
			}

			if diff := cmp.Diff(tt.wantOut, out); diff != "" {
				t.Errorf("phenix %q output mismatch (-want +got):\n%s", tt.args, diff)
			}

			if diff := cmp.Diff(tt.wantStderr, stderr); diff != "" {
				t.Errorf("phenix %q stderr mismatch (-want +got):\n%s", tt.args, diff)
			}

			if usage := cmd.Flags().FlagUsages(); strings.Contains(usage, "--new ") || strings.Contains(usage, "--old") {
				t.Errorf("flag usages show a deprecated flag:\n%s", usage)
			}
		})
	}
}

// A deprecated command still runs after the warning, and the help of its
// parent does not show it.
func TestDeprecateCommand(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{
		Use:     "old <name>",
		Aliases: []string{"o"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("ran " + args[0])

			return nil
		},
	}
	deprecateCommand(cmd, "phenix new <name>")

	out, stderr, err := deprecationTestRun(cmd, "o", "x")
	if err != nil {
		t.Fatalf("phenix o x error = %v", err)
	}

	if out != "ran x\n" {
		t.Errorf("phenix o x output = %q, want %q", out, "ran x\n")
	}

	want := "phenix: warning: phenix old <name> is deprecated and will be removed in a future release; " +
		"use phenix new <name> instead.\n"
	if diff := cmp.Diff(want, stderr); diff != "" {
		t.Errorf("phenix o x stderr mismatch (-want +got):\n%s", diff)
	}

	if cmd.IsAvailableCommand() {
		t.Error("the deprecated command is available in the help, want it hidden")
	}
}

// deprecateArgs records each form, and warn names the form and its replacement.
func TestDeprecateArgs(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "run"}
	first := deprecateArgs(cmd, "<b> <a>", "<a> <b>")
	deprecateArgs(cmd, "<c>", "<a> <b>")

	if got, want := cmd.Annotations[annotationDeprecatedArgs], "<b> <a>\t<a> <b>\n<c>\t<a> <b>"; got != want {
		t.Errorf("annotation = %q, want %q", got, want)
	}

	var stderr bytes.Buffer

	cmd.SetErr(&stderr)
	first.warn("")
	first.warn("run x y")

	want := "phenix: warning: run <b> <a> is deprecated and will be removed in a future release; use run <a> <b> instead.\n" +
		"phenix: warning: run <b> <a> is deprecated and will be removed in a future release; use run x y instead.\n"
	if diff := cmp.Diff(want, stderr.String()); diff != "" {
		t.Errorf("warn mismatch (-want +got):\n%s", diff)
	}
}

// registeredDeprecations returns each deprecated part of the phenix command
// line that the deprecation helpers marked, with its replacement.
func registeredDeprecations(root *cobra.Command) map[string]string {
	found := map[string]string{}

	var walk func(*cobra.Command)

	walk = func(cmd *cobra.Command) {
		if replacement, ok := cmd.Annotations[annotationDeprecated]; ok {
			found[deprecatedCommandForm(cmd)] = replacement
		}

		if forms := cmd.Annotations[annotationDeprecatedArgs]; forms != "" {
			for line := range strings.SplitSeq(forms, "\n") {
				old, replacement, _ := strings.Cut(line, "\t")
				found[cmd.CommandPath()+" "+old] = cmd.CommandPath() + " " + replacement
			}
		}

		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if replacement, ok := f.Annotations[annotationDeprecated]; ok {
				found[cmd.CommandPath()+" "+flagLabel(f)] = cmd.CommandPath() + " " + replacement[0]
			}
		})

		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}

	walk(root)

	return found
}

// docs/content/deprecations.md lists each deprecated part of the command line
// that the helpers mark, with its replacement, and no other part.
func TestDeprecationsDocumented(t *testing.T) { //nolint:paralleltest // reading the flags of rootCmd changes them
	documented := map[string]string{}

	for _, row := range docTable(t, deprecationsDocPath, "Deprecated Command Line Parts", 3) {
		documented[firstSpan(row[0])] = firstSpan(row[1])
	}

	if diff := cmp.Diff(registeredDeprecations(rootCmd), documented); diff != "" {
		t.Errorf("%s does not match the deprecated command line parts (-registered +docs):\n%s", deprecationsDocPath, diff)
	}
}

// docs/content/deprecations.md lists each deprecated settings key and its
// environment variable, with their replacements, and no other key.
func TestDeprecatedSettingsDocumented(t *testing.T) {
	t.Parallel()

	registered := map[string]string{}

	for _, s := range deprecatedSettings() {
		registered[s.key] = s.replacement
		registered[runtimesettings.EnvVar(s.key)] = runtimesettings.EnvVar(s.replacement)
	}

	documented := map[string]string{}

	for _, row := range docTable(t, deprecationsDocPath, "Deprecated Settings", 3) {
		old, replacement := codeSpan.FindAllStringSubmatch(row[0], -1), codeSpan.FindAllStringSubmatch(row[1], -1)
		if len(old) != len(replacement) {
			t.Errorf("%s: row %q names %d deprecated keys and %d replacements, want the same number",
				deprecationsDocPath, row, len(old), len(replacement))

			continue
		}

		for i := range old {
			documented[old[i][1]] = replacement[i][1]
		}
	}

	if diff := cmp.Diff(registered, documented); diff != "" {
		t.Errorf("%s does not match the deprecated settings (-registered +docs):\n%s", deprecationsDocPath, diff)
	}
}

// warnDeprecatedSetting names the deprecated key, its environment variable
// and its replacement, and writes nothing for another key.
func TestWarnDeprecatedSetting(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		key  string
		want string
	}{
		{
			key: legacyKeyUnixSocketGID,
			want: "phenix: warning: the setting unix-socket-gid (PHENIX_UNIX_SOCKET_GID) is deprecated and will be " +
				"removed in a future release; use ui.unix-socket-gid (PHENIX_UI_UNIX_SOCKET_GID) instead.\n",
		},
		{key: keyUnixSocketGID, want: ""},
	} {
		var buf bytes.Buffer

		warnDeprecatedSetting(&buf, tt.key)

		if diff := cmp.Diff(tt.want, buf.String()); diff != "" {
			t.Errorf("warnDeprecatedSetting(%s) mismatch (-want +got):\n%s", tt.key, diff)
		}
	}
}
