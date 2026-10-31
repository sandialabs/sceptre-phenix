package printer

import (
	"bytes"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/termcolor"
)

func TestPrintTableOfVMsOptionallyIncludesTaps(t *testing.T) {
	vm := mm.VM{
		Name: "router",
		Taps: []string{"mega_tap101", "mega_tap102"},
	}

	var output bytes.Buffer
	PrintTableOfVMs(&output, termcolor.New(false), false, vm)

	if strings.Contains(output.String(), "mega_tap101") {
		t.Fatalf("tap names included without taps option:\n%s", output.String())
	}

	output.Reset()
	PrintTableOfVMs(&output, termcolor.New(false), true, vm)

	if !strings.Contains(output.String(), "mega_tap101, mega_tap102") {
		t.Fatalf("tap names missing from single VM table:\n%s", output.String())
	}

	output.Reset()
	PrintTableOfVMs(&output, termcolor.New(false), true, vm, mm.VM{Name: "server", Taps: []string{"mega_tap103"}})

	if !strings.Contains(output.String(), "mega_tap103") {
		t.Fatalf("tap names missing from multiple VM table:\n%s", output.String())
	}
}

func TestPrintTableOfExperimentsMarksAnInterruptedStart(t *testing.T) {
	tests := []struct {
		name, started, interrupted string
		want                       bool
	}{
		{"running and interrupted", "2024-01-01T00:00:00Z", "minimega could not list the VMs", true},
		{"running", "2024-01-01T00:00:00Z", "", false},
		{"stopped", "", "minimega could not list the VMs", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})
			exp.Status.SetStartTime(test.started)
			exp.Status.SetInterrupted(test.interrupted)

			var output bytes.Buffer
			PrintTableOfExperiments(&output, termcolor.New(false), *exp)

			if got := strings.Contains(output.String(), "interrupted)"); got != test.want {
				t.Errorf("experiment table marks the start interrupted = %t, want %t:\n%s", got, test.want, output.String())
			}
		})
	}
}
