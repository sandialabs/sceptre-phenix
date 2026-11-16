package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"
)

// outputJSONTestKind has a MarshalJSON method, as disk.Kind does.
type outputJSONTestKind int

func (k outputJSONTestKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[outputJSONTestKind]string{1: "VM"}[k])
}

// outputJSONTestRow has only json tags, as the types of the API do.
type outputJSONTestRow struct {
	VMName   string             `json:"vmName"`
	Kind     outputJSONTestKind `json:"kind"`
	Running  bool               `json:"running"`
	Text     string             `json:"text"`
	Modified time.Time          `json:"modified"`
	Tags     []string           `json:"tags"`
	Size     *int64             `json:"size,omitempty"`
}

func TestWriteJSONKeyedYAML(t *testing.T) {
	t.Parallel()

	modified := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		data any
		want string
	}{
		{
			name: "json names, json values and the field order",
			data: []outputJSONTestRow{{VMName: "web", Kind: 1, Running: true, Text: "true", Modified: modified, Tags: []string{"a"}}},
			want: "- vmName: web\n  kind: VM\n  running: true\n  text: \"true\"\n  modified: \"2026-11-06T08:00:00Z\"\n" +
				"  tags:\n    - a\n",
		},
		{name: "nil slice", data: []outputJSONTestRow(nil), want: "[]\n"},
		{name: "empty object", data: map[string]int{}, want: "{}\n"},
		{name: "text with two lines", data: map[string]string{"msg": "a\nb"}, want: "msg: |-\n    a\n    b\n"},
		{name: "text with a newline at an end", data: map[string]string{"msg": "\na"}, want: "msg: \"\\na\"\n"},
		{name: "text with a tab", data: map[string]string{"msg": "a\tb"}, want: "msg: \"a\\tb\"\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			if err := writeJSONKeyedYAML(&buf, tt.data); err != nil {
				t.Fatalf("writeJSONKeyedYAML() error = %v", err)
			}

			if diff := cmp.Diff(tt.want, buf.String()); diff != "" {
				t.Errorf("writeJSONKeyedYAML() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The YAML of writeJSONKeyedYAML holds the same data as the JSON of
// writeJSONOutput, for any text, numbers and lists.
func TestWriteJSONKeyedYAMLProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		data := rapid.MapOf(rapid.String(), rapid.OneOf(
			rapid.Map(rapid.String(), func(s string) any { return s }),
			rapid.Map(rapid.Int64(), func(n int64) any { return n }),
			rapid.Map(rapid.Bool(), func(b bool) any { return b }),
			rapid.Map(rapid.SliceOf(rapid.String()), func(s []string) any { return s }),
		)).Draw(t, "data")

		var jsonOut, yamlOut bytes.Buffer

		if err := writeJSONOutput(&jsonOut, data); err != nil {
			t.Fatalf("writeJSONOutput() error = %v", err)
		}

		if err := writeJSONKeyedYAML(&yamlOut, data); err != nil {
			t.Fatalf("writeJSONKeyedYAML() error = %v", err)
		}

		if diff := outputJSONYAMLDiff(jsonOut.String(), yamlOut.String()); diff != "" {
			t.Fatalf("YAML and JSON mismatch (-json +yaml):\n%s\nyaml:\n%s", diff, yamlOut.String())
		}
	})
}

// outputJSONYAMLDiff returns how the data of the YAML yamlOut differs from
// the data of the JSON jsonOut, or "" when they hold the same data.
func outputJSONYAMLDiff(jsonOut, yamlOut string) string {
	var fromJSON, fromYAML any

	if err := json.Unmarshal([]byte(jsonOut), &fromJSON); err != nil {
		return "the JSON does not decode: " + err.Error()
	}

	if err := yaml.Unmarshal([]byte(yamlOut), &fromYAML); err != nil {
		return "the YAML does not decode: " + err.Error()
	}

	// JSON gives float64 numbers and YAML gives ints, so compare as JSON
	b, err := json.Marshal(fromYAML)
	if err != nil {
		return "the YAML data does not encode as JSON: " + err.Error()
	}

	fromYAML = nil
	if err := json.Unmarshal(b, &fromYAML); err != nil {
		return "the YAML data does not decode as JSON: " + err.Error()
	}

	return cmp.Diff(fromJSON, fromYAML)
}

// outputRun runs the command sub of a root command "phenix" with args, and
// returns its output and its error.
func outputRun(sub *cobra.Command, args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(sub)
	root.SetArgs(args)

	var stdout bytes.Buffer

	root.SetOut(&stdout)
	root.SetErr(io.Discard)

	_, err := root.ExecuteC()

	return stdout.String(), err
}

// checkOutputFormats runs a command with -o table, json and yaml through run,
// and checks that the default output is the table, that the JSON decodes to
// want, and that the YAML holds the same data as the JSON. It returns the
// table.
func checkOutputFormats[T any](t *testing.T, name string, run func(args ...string) (string, error), want T) string {
	t.Helper()

	def, err := run()
	if err != nil {
		t.Fatalf("%s error = %v", name, err)
	}

	table, err := run("-o", formatTable)
	if err != nil {
		t.Fatalf("%s -o table error = %v", name, err)
	}

	if def != table {
		t.Errorf("%s = %q, want the table of -o table %q", name, def, table)
	}

	jsonOut, err := run("-o", FormatJSON)
	if err != nil {
		t.Fatalf("%s -o json error = %v", name, err)
	}

	var got T
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("%s -o json = %q, which does not decode: %v", name, jsonOut, err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("%s -o json mismatch (-want +got):\n%s", name, diff)
	}

	yamlOut, err := run("-o", FormatYAML)
	if err != nil {
		t.Fatalf("%s -o yaml error = %v", name, err)
	}

	if diff := outputJSONYAMLDiff(jsonOut, yamlOut); diff != "" {
		t.Errorf("%s -o yaml and -o json mismatch (-json +yaml):\n%s", name, diff)
	}

	if strings.HasPrefix(strings.TrimSpace(yamlOut), "{") || strings.HasPrefix(strings.TrimSpace(yamlOut), "[{") {
		t.Errorf("%s -o yaml = %q, want block style", name, yamlOut)
	}

	return table
}
