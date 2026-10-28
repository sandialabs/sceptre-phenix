package web

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	bdoc "phenix/types/builder"
)

// TestBuilderPublishRefusesDeviceSetupErrors refuses a device setting that
// makes the experiment fail to start, names it, and exports the topology
// with it as a publish blocker.
func TestBuilderPublishRefusesDeviceSetupErrors(t *testing.T) {
	const (
		refusal = "phenix cannot publish topology ntp: "
		problem = `the ntp-server label of "clock" is "eth9", which names no interface of the device. Thus the ` +
			`experiment fails to start. Set the label to the name of an interface with an IP address`
	)

	// The refusal message and the export name the problem as a sentence.
	sentence := "T" + problem[1:]

	document := bdoc.NewDocument("ntp")
	addExportHost(document, "clock", map[string]any{
		"name": "eth0", "vlan": "EXP", "proto": "static", "address": "10.0.0.5", "mask": 24,
	})
	document.Nodes[0].Device.Spec["labels"] = map[string]any{"ntp-server": "eth9"}

	harness := newBuilderHarness(t)
	draft := createBuilderPublishDraft(t, harness, document)

	_, publishRefusal := publishBuilderDraftAs(t, harness, draft, nil,
		`{"mode":"topology","topology":{"name":"ntp","action":"create"}}`, http.StatusUnprocessableEntity)

	if want := refusal + sentence; publishRefusal.Message != want {
		t.Errorf("publish message = %q, want %q", publishRefusal.Message, want)
	}

	if publishRefusal.Code != string(bdoc.CodePublishBlocked) {
		t.Errorf("publish refusal code = %q, want %q", publishRefusal.Code, bdoc.CodePublishBlocked)
	}

	want := []bdoc.Issue{{
		Code: bdoc.CodeDeviceNTPServerInvalid, Severity: bdoc.SeverityError, Message: problem,
		Path: "nodes[0].device.spec.labels.ntp-server", NodeID: bdoc.DeviceNodeID("clock"),
		EdgeID: "", NetworkID: "", Field: "spec.labels.ntp-server",
	}}
	if diff := cmp.Diff(want, publishRefusal.Issues); diff != "" {
		t.Errorf("publish refusal issues mismatch (-want +got):\n%s", diff)
	}

	response, _ := exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, document, "ntp", nil))

	want[0].Message = sentence
	if diff := cmp.Diff(want, response.PublishBlockers); diff != "" {
		t.Errorf("POST /builder/export/topology publish blockers mismatch (-want +got):\n%s", diff)
	}

	if harness.configWrites != 0 {
		t.Errorf("the export and the refused publication wrote %d configs, want none", harness.configWrites)
	}
}

// Past the first few, the refusal counts the device settings it does not
// name.
func TestProjectionProblemsCountsDeviceSettings(t *testing.T) {
	err := &bdoc.DeviceSetupError{Problems: []string{"a", "b", "c", "d", "e"}}

	got, named := projectionProblems(err)
	if want := "A. B. C. 2 more settings stop the experiment from starting"; !named || got != want {
		t.Errorf("projectionProblems(%v) = %q, %t, want %q, true", err, got, named, want)
	}
}
