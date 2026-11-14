package printer

import (
	"bytes"
	"strings"
	"testing"

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
