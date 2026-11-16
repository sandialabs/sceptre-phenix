package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"
)

// outputTestRow is a row that the output helper tests print.
type outputTestRow struct {
	Name  string `json:"name"  yaml:"name"`
	Count int    `json:"count" yaml:"count"`
}

// outputTestCommand returns a command that prints rows with an output flag
// of the formats table, json and yaml, made with opts.
func outputTestCommand(rows []outputTestRow, opts ...outputOption) *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:           "show",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return out.print(cmd, rows, outputViews{
				formatTable: func(w io.Writer) error {
					for _, r := range rows {
						_, _ = io.WriteString(w, r.Name+"\n")
					}

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML}, opts...)

	return cmd
}

// outputTestRun runs cmd with args, and returns its output and its error output.
func outputTestRun(cmd *cobra.Command, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{}, args...)) // cobra reads os.Args for nil args

	err := cmd.Execute()

	return stdout.String(), stderr.String(), err
}

func TestOutputFlag(t *testing.T) {
	t.Parallel()

	rows := []outputTestRow{{Name: "a", Count: 1}, {Name: "b", Count: 2}}

	const (
		jsonRows = "[\n  {\n    \"name\": \"a\",\n    \"count\": 1\n  },\n  {\n    \"name\": \"b\",\n    \"count\": 2\n  }\n]\n"
		yamlRows = "- name: a\n  count: 1\n- name: b\n  count: 2\n"
		table    = "a\nb\n"
		warning  = "phenix: warning: show -f/--format is deprecated and will be removed in a future release; " +
			"use show -o/--output instead.\n"
	)

	all := []outputOption{withJSONFlag(), withFormatFlag()}

	tests := []struct {
		name       string
		args       []string
		wantOut    string
		wantStderr string
		wantErr    error
		wantText   string // text in the error, for an error from pflag
	}{
		{name: "default", wantOut: table},
		{name: "table", args: []string{"-o", "table"}, wantOut: table},
		{name: "json", args: []string{"-o", "json"}, wantOut: jsonRows},
		{name: "yaml", args: []string{"--output=yaml"}, wantOut: yamlRows},
		{name: "last -o wins", args: []string{"-o", "json", "-o", "yaml"}, wantOut: yamlRows},
		{name: "--json", args: []string{"--json"}, wantOut: jsonRows},
		{name: "--json and -o json", args: []string{"--json", "-o", "json"}, wantOut: jsonRows},
		{name: "--json=false", args: []string{"--json=false", "-o", "yaml"}, wantOut: yamlRows},
		{name: "deprecated -f", args: []string{"-f", "yaml"}, wantOut: yamlRows, wantStderr: warning},
		{name: "deprecated --format", args: []string{"--format=json"}, wantOut: jsonRows, wantStderr: warning},
		{
			name: "unknown format", args: []string{"-o", "xml"}, wantErr: errOutputFormat,
			wantText: `unknown output format "xml": use one of table, json, yaml`,
		},
		{
			name: "--json with -o yaml", args: []string{"--json", "-o", "yaml"}, wantErr: errOutputConflict,
			wantText: "--json asks for json and --output asks for yaml",
		},
		{
			name: "-o yaml with --json", args: []string{"-o", "yaml", "--json"}, wantErr: errOutputConflict,
			wantText: "--output asks for yaml and --json asks for json",
		},
		{
			name: "--json, -o json, then -o yaml", args: []string{"--json", "-o", "json", "-o", "yaml"},
			wantErr: errOutputConflict, wantText: "--json asks for json and --output asks for yaml",
		},
		{
			name: "-f with -o", args: []string{"-f", "json", "-o", "yaml"}, wantErr: errOutputConflict,
			wantText: "--format asks for json and --output asks for yaml", wantStderr: warning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, stderr, err := outputTestRun(outputTestCommand(rows, all...), tt.args...)

			if tt.wantErr != nil {
				// pflag does not wrap the error of a flag value, so check its text.
				if err == nil || !strings.Contains(err.Error(), tt.wantText) {
					t.Errorf("show %q error = %v, want one with %q", tt.args, err, tt.wantText)
				}
			} else if err != nil {
				t.Fatalf("show %q error = %v", tt.args, err)
			}

			if tt.wantErr == nil {
				if diff := cmp.Diff(tt.wantOut, out); diff != "" {
					t.Errorf("show %q output mismatch (-want +got):\n%s", tt.args, diff)
				}
			}

			if diff := cmp.Diff(tt.wantStderr, stderr); diff != "" {
				t.Errorf("show %q stderr mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// The errors of an output flag wrap errOutputFormat and errOutputConflict.
func TestOutputFlagSetErrors(t *testing.T) {
	t.Parallel()

	o := addOutputFlag(&cobra.Command{Use: "show"}, formatTable, []string{formatTable, FormatJSON})

	if err := o.set(outputFlagName, "xml"); !errors.Is(err, errOutputFormat) {
		t.Errorf("set(output, xml) error = %v, want %v", err, errOutputFormat)
	}

	if err := o.set("json", FormatJSON); err != nil {
		t.Fatalf("set(json, json) error = %v", err)
	}

	if err := o.set(outputFlagName, formatTable); !errors.Is(err, errOutputConflict) {
		t.Errorf("set(output, table) after --json error = %v, want %v", err, errOutputConflict)
	}

	if got := o.format(); got != FormatJSON {
		t.Errorf("format() = %q, want %q", got, FormatJSON)
	}
}

// -o takes each format of the command and no other text, and the format is
// the last one given.
func TestOutputFlagValuesProperty(t *testing.T) {
	t.Parallel()

	formats := []string{formatTable, FormatJSON, FormatYAML, formatCSV}

	rapid.Check(t, func(t *rapid.T) {
		o := addOutputFlag(&cobra.Command{Use: "show"}, formatTable, formats)

		values := rapid.SliceOfN(rapid.OneOf(rapid.SampledFrom(formats), rapid.String()), 1, 4).Draw(t, "values")

		want := formatTable

		for _, v := range values {
			err := o.set(outputFlagName, v)

			if slices.Contains(formats, v) {
				if err != nil {
					t.Fatalf("set(output, %q) error = %v", v, err)
				}

				want = v
			} else if !errors.Is(err, errOutputFormat) {
				t.Fatalf("set(output, %q) error = %v, want %v", v, err, errOutputFormat)
			}
		}

		if got := o.format(); got != want {
			t.Fatalf("format() after %q = %q, want %q", values, got, want)
		}
	})
}

// The help shows -o with its formats and default, and --json, but not the
// deprecated -f; -o completes the formats.
func TestOutputFlagHelpAndCompletion(t *testing.T) {
	t.Parallel()

	cmd := outputTestCommand(nil, withJSONFlag(), withFormatFlag())
	usage := cmd.Flags().FlagUsages()

	for _, want := range []string{"-o, --output string", "Output format (table, json, yaml)", `(default "table")`, "--json"} {
		if !strings.Contains(usage, want) {
			t.Errorf("flag usages do not have %q:\n%s", want, usage)
		}
	}

	if strings.Contains(usage, "--format") {
		t.Errorf("flag usages show the deprecated --format:\n%s", usage)
	}

	complete, ok := cmd.GetFlagCompletionFunc(outputFlagName)
	if !ok {
		t.Fatal("-o has no completion")
	}

	got, directive := complete(cmd, nil, "j")
	if diff := cmp.Diff([]string{FormatJSON}, got); diff != "" || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("-o completion of j = %q, %v; want [json], NoFileComp (diff %s)", got, directive, diff)
	}
}

func TestRenderOutput(t *testing.T) {
	t.Parallel()

	var nilRows []outputTestRow

	tests := []struct {
		name    string
		format  string
		data    any
		views   outputViews
		want    string
		wantErr error
	}{
		{name: "nil list as JSON", format: FormatJSON, data: nilRows, want: "[]\n"},
		{name: "nil list as YAML", format: FormatYAML, data: nilRows, want: "[]\n"},
		{name: "map as JSON", format: FormatJSON, data: map[string]int{"b": 2, "a": 1}, want: "{\n  \"a\": 1,\n  \"b\": 2\n}\n"},
		{name: "struct as YAML", format: FormatYAML, data: outputTestRow{Name: "x", Count: 3}, want: "name: x\ncount: 3\n"},
		{
			name: "view replaces YAML", format: FormatYAML, data: 1,
			views: outputViews{FormatYAML: func(w io.Writer) error { _, err := io.WriteString(w, "one\n"); return err }},
			want:  "one\n",
		},
		{name: "format without a view", format: formatCSV, data: 1, wantErr: errOutputFormat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			err := renderOutput(&buf, tt.format, tt.data, tt.views)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("renderOutput(%s) error = %v, want %v", tt.format, err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, buf.String()); diff != "" {
				t.Errorf("renderOutput(%s) mismatch (-want +got):\n%s", tt.format, diff)
			}
		})
	}
}

// JSON and YAML output decode to the data, and a failing writer gives an error.
func TestRenderOutputRoundTrip(t *testing.T) {
	t.Parallel()

	rows := []outputTestRow{{Name: "a\"<b>", Count: -1}}

	for format, unmarshal := range map[string]func([]byte, any) error{FormatJSON: json.Unmarshal, FormatYAML: yaml.Unmarshal} {
		var buf bytes.Buffer
		if err := renderOutput(&buf, format, rows, nil); err != nil {
			t.Fatalf("renderOutput(%s) error = %v", format, err)
		}

		var got []outputTestRow
		if err := unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("decoding the %s output: %v\n%s", format, err, buf.String())
		}

		if diff := cmp.Diff(rows, got); diff != "" {
			t.Errorf("renderOutput(%s) round trip mismatch (-want +got):\n%s", format, diff)
		}

		if err := renderOutput(jobFailWriter{}, format, rows, nil); err == nil {
			t.Errorf("renderOutput(%s) to a failing writer error = nil, want an error", format)
		}
	}
}

// addOutputFlag and its alias options panic for a format that the command does not make.
func TestOutputFlagPanicsForUnknownDefault(t *testing.T) {
	t.Parallel()

	for name, add := range map[string]func(){
		"default": func() { addOutputFlag(&cobra.Command{Use: "x"}, formatCSV, []string{formatTable}) },
		"--pretty": func() {
			addOutputFlag(&cobra.Command{Use: "x"}, formatTable, []string{formatTable}, withPrettyFlag(formatMarkdown))
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("addOutputFlag with an unknown %s format did not panic", name)
				}
			}()

			add()
		}()
	}
}
