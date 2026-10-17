package tmpl_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"phenix/tmpl"
	v1 "phenix/types/version/v1"
)

// minimegaScript returns the minimega script lines of one VM, host-01, with labels.
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

// TestMinimegaScriptTagEscapes checks that label keys and values are escaped as
// minimega's minicli reads a double-quoted argument, so each tag stays one line.
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
