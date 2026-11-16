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

// scriptSpec returns an experiment of one VM, host-01, changed by change.
func scriptSpec(change func(*v1.Node)) *v1.ExperimentSpec {
	snapshot := false

	node := &v1.Node{
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
	}

	if change != nil {
		change(node)
	}

	return &v1.ExperimentSpec{
		ExperimentNameF: "tags-exp",
		TopologyF:       &v1.TopologySpec{NodesF: []*v1.Node{node}},
	}
}

// renderScript returns the minimega script lines of spec.
func renderScript(t *testing.T, spec *v1.ExperimentSpec) []string {
	t.Helper()

	var script bytes.Buffer

	if err := tmpl.GenerateFromTemplate("minimega_script.tmpl", spec, &script); err != nil {
		t.Fatalf("GenerateFromTemplate(minimega_script.tmpl) error = %v", err)
	}

	return strings.Split(script.String(), "\n")
}

// minimegaScript returns the minimega script lines of one VM, host-01, with labels.
func minimegaScript(t *testing.T, labels map[string]string) []string {
	t.Helper()

	return renderScript(t, scriptSpec(func(n *v1.Node) { n.LabelsF = labels }))
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
		`.preprocess false vm config tags "a \"quoted\" \\ key" "plain"`,
		`.preprocess false vm config tags "label_1_quote" "Run \"setup\" first"`,
		`.preprocess false vm config tags "label_2_backslash" "C:\\temp\\"`,
		`.preprocess false vm config tags "label_3_tab" "Name\tValue"`,
		`.preprocess false vm config tags "label_4_crlf" "First line\r\nSecond line"`,
		`.preprocess false vm config tags "label_5_newline" "First line\nSecond line"`,
		"vm launch kvm host-01",
	}

	lines := minimegaScript(t, labels)

	first := slices.IndexFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, ".preprocess false vm config tags ")
	})
	if first < 0 || first+len(want) > len(lines) {
		t.Fatalf("the script has no tags followed by the launch:\n%s", strings.Join(lines, "\n"))
	}

	if diff := cmp.Diff(want, lines[first:first+len(want)]); diff != "" {
		t.Errorf("minimega script tag lines mismatch (-want +got):\n%s", diff)
	}
}
