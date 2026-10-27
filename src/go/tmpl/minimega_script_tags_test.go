package tmpl_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/tmpl"
	v1 "phenix/types/version/v1"
)

// minimegaScript returns the minimega script lines of one VM, host-01, with labels.
func minimegaScript(t *testing.T, labels map[string]string) []string {
	t.Helper()

	snapshot := false

	spec := &v1.ExperimentSpec{
		ExperimentNameF: "tags-exp",
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
		t.Fatalf("GenerateFromTemplate(minimega_script.tmpl) error = %v", err)
	}

	return strings.Split(script.String(), "\n")
}

// TestMinimegaScriptTagEscapes checks that label keys and values are escaped as
// minimega's minicli reads a double-quoted argument, so each tag stays one line.
func TestMinimegaScriptTagEscapes(t *testing.T) {
	labels := map[string]string{
		`a "quoted" \ key`:  "plain",
		"label_1_quote":     `Run "setup" first`,
		"label_2_backslash": `C:\temp\`,
		"label_3_tab":       "Name\tValue",
		"label_4_crlf":      "First line\r\nSecond line",
		"label_5_newline":   "First line\nSecond line",
	}

	// In key order, as the template ranges over the labels.
	want := []string{
		`vm config tags "a \"quoted\" \\ key" "plain"`,
		`vm config tags "label_1_quote" "Run \"setup\" first"`,
		`vm config tags "label_2_backslash" "C:\\temp\\"`,
		`vm config tags "label_3_tab" "Name\tValue"`,
		`vm config tags "label_4_crlf" "First line\r\nSecond line"`,
		`vm config tags "label_5_newline" "First line\nSecond line"`,
		"vm launch kvm host-01",
	}

	lines := minimegaScript(t, labels)

	first := slices.IndexFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, "vm config tags ")
	})
	if first < 0 || first+len(want) > len(lines) {
		t.Fatalf("the script has no tags followed by the launch:\n%s", strings.Join(lines, "\n"))
	}

	if diff := cmp.Diff(want, lines[first:first+len(want)]); diff != "" {
		t.Errorf("minimega script tag lines mismatch (-want +got):\n%s", diff)
	}
}
