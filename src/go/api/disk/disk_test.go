package disk

import "testing"

func TestSetExperimentImages(t *testing.T) {
	details := map[string]Details{
		"ubuntu.qcow2":  {Name: "ubuntu.qcow2"},
		"windows.qcow2": {Name: "windows.qcow2"},
	}
	expsByImage := map[string]map[string]struct{}{
		"ubuntu.qcow2":  {"my-exp": {}},
		"missing.qcow2": {"my-exp": {}}, // not in details; must be ignored
	}

	setExperimentImages(details, expsByImage)

	// An image referenced by the experiment has its Experiment field populated
	// (this is the field that previously always showed as "N/A" in the UI).
	if got := details["ubuntu.qcow2"].Experiment; got == nil {
		t.Fatal("expected ubuntu.qcow2 Experiment to be set, got nil")
	} else if *got != "my-exp" {
		t.Errorf("expected ubuntu.qcow2 Experiment to be %q, got %q", "my-exp", *got)
	}

	// Images no experiment references are left untouched.
	if got := details["windows.qcow2"].Experiment; got != nil {
		t.Errorf("expected windows.qcow2 Experiment to remain unset, got %q", *got)
	}

	// Images not already present in the map must not be added.
	if _, ok := details["missing.qcow2"]; ok {
		t.Error("setExperimentImages must not add images that are not already present")
	}
}

func TestSetExperimentImagesListsAllSorted(t *testing.T) {
	// An image referenced by several experiments lists all of them, sorted, so
	// the result is deterministic regardless of experiment iteration order.
	details := map[string]Details{"shared.qcow2": {Name: "shared.qcow2"}}
	expsByImage := map[string]map[string]struct{}{
		"shared.qcow2": {"substation-demo": {}, "power-grid-demo": {}, "alpha": {}},
	}

	setExperimentImages(details, expsByImage)

	got := details["shared.qcow2"].Experiment
	if got == nil {
		t.Fatal("expected shared.qcow2 Experiment to be set, got nil")
	}
	want := "alpha, power-grid-demo, substation-demo"
	if *got != want {
		t.Errorf("expected shared.qcow2 Experiment to be %q, got %q", want, *got)
	}
}
