package tmpl_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"phenix/tmpl"
	v1 "phenix/types/version/v1"
)

// minimegaScript returns the lines of the minimega script of an experiment
// with one VM, host-01, that holds labels.
func minimegaScript(t *testing.T, labels map[string]string) []string {
	t.Helper()

	snapshot := false

	spec := &v1.ExperimentSpec{
		ExperimentNameF: "notes-exp",
		TopologyF: &v1.TopologySpec{
			NodesF: []*v1.Node{
				{
					TypeF: "VirtualMachine",
					GeneralF: &v1.General{
						HostnameF: "host-01",
						VMTypeF:   "kvm",
						SnapshotF: &snapshot,
					},
					HardwareF: &v1.Hardware{
						OSTypeF: "linux",
						DrivesF: []*v1.Drive{{ImageF: "ubuntu.qc2"}},
					},
					LabelsF: labels,
				},
			},
		},
	}

	var script bytes.Buffer

	if err := tmpl.GenerateFromTemplate("minimega_script.tmpl", spec, &script); err != nil {
		t.Fatalf("GenerateFromTemplate() error = %v", err)
	}

	return strings.Split(script.String(), "\n")
}

// TestMinimegaScriptNoteTag proves a VM note of several lines, which a node
// holds as a label, becomes one vm config tags line, its line breaks written
// as \n, before the VM is launched.
func TestMinimegaScriptNoteTag(t *testing.T) {
	lines := minimegaScript(t, map[string]string{
		"__notes_2026-10-09T11:34:56.789Z": "Reset the password\nbefore each run.",
	})

	const want = `vm config tags "__notes_2026-10-09T11:34:56.789Z" "Reset the password\nbefore each run."`

	tag := slices.Index(lines, want)
	if tag < 0 {
		t.Fatalf("the script has no line %q:\n%s", want, strings.Join(lines, "\n"))
	}

	for i, line := range lines {
		if strings.HasPrefix(line, "before each run") {
			t.Errorf("line %d of the script continues the note: %q", i+1, line)
		}
	}

	if launch := "vm launch kvm host-01"; tag+1 >= len(lines) || lines[tag+1] != launch {
		t.Errorf("the line after the note tag is not %q:\n%s", launch, strings.Join(lines, "\n"))
	}
}

// TestMinimegaScriptTagEscapes proves the key and the value of each label are
// written as minimega reads a double-quoted argument back (lexQuote and
// lexEscape in minimega's minicli package): a backslash, a double quote, a
// tab, a carriage return and a line feed each as its escape, so the argument
// keeps its text and each tag stays one line.
func TestMinimegaScriptTagEscapes(t *testing.T) {
	labels := map[string]string{
		"__notes_1_quote":     `Run "setup" first`,
		"__notes_2_backslash": `C:\temp\`,
		"__notes_3_tab":       "Name\tValue",
		"__notes_4_crlf":      "First line\r\nSecond line",
		"__notes_5_newline":   "First line\nSecond line",
		`a "quoted" \ key`:    "plain",
	}

	// In key order, as the template ranges over the labels.
	want := []string{
		`vm config tags "__notes_1_quote" "Run \"setup\" first"`,
		`vm config tags "__notes_2_backslash" "C:\\temp\\"`,
		`vm config tags "__notes_3_tab" "Name\tValue"`,
		`vm config tags "__notes_4_crlf" "First line\r\nSecond line"`,
		`vm config tags "__notes_5_newline" "First line\nSecond line"`,
		`vm config tags "a \"quoted\" \\ key" "plain"`,
		"vm launch kvm host-01",
	}

	lines := minimegaScript(t, labels)

	first := slices.IndexFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, "vm config tags ")
	})
	if first < 0 || first+len(want) > len(lines) {
		t.Fatalf("the script has no tags followed by the launch:\n%s", strings.Join(lines, "\n"))
	}

	if got := lines[first : first+len(want)]; !slices.Equal(got, want) {
		t.Errorf("tag lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
