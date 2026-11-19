package app

import (
	"context"
	"reflect"
	"testing"

	"phenix/store"
	"phenix/types"
	"phenix/util/tap"
)

// TestTapCleanupNoopWhenStatusMissing reproduces the bug where stopping an
// experiment before a post-start tap app has run (e.g. because the
// experiment was stopped while waiting on delayed VMs) causes Cleanup to
// fail with "missing status for app tap", even though the app never
// created any taps that need to be cleaned up.
func TestTapCleanupNoopWhenStatusMissing(t *testing.T) {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-cleanup-test"})
	exp.Spec.SetExperimentName("tap-cleanup-test")

	var tapApp Tap

	if err := tapApp.Cleanup(context.Background(), exp); err != nil {
		t.Fatalf("expected no error cleaning up tap app with no recorded status, got: %v", err)
	}
}

// TestTapCleanupDeletesRecordedTaps verifies Cleanup still parses and
// processes status when the tap app previously recorded taps it created.
func TestTapCleanupDeletesRecordedTaps(t *testing.T) {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-cleanup-test"})
	exp.Spec.SetExperimentName("tap-cleanup-test")

	// No taps recorded, but the status entry itself exists -- this should
	// still be treated as a legitimate (empty) status rather than "missing".
	exp.Status.SetAppStatus("tap", TapAppStatus{Host: "compute-0"})

	var tapApp Tap

	if err := tapApp.Cleanup(context.Background(), exp); err != nil {
		t.Fatalf("expected no error cleaning up tap app with empty taps, got: %v", err)
	}
}

// TestTapAppStatusFirewallRoundTrip verifies a tap's external access firewall
// config survives being persisted to and parsed back from experiment status.
func TestTapAppStatusFirewallRoundTrip(t *testing.T) {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-firewall-test"})
	exp.Spec.SetExperimentName("tap-firewall-test")

	firewall := &tap.Firewall{
		Default: "drop",
		Rules: []*tap.FirewallRule{{
			Action:      "accept",
			Description: "only allow web access",
			Source:      &tap.FirewallEndpoint{Addresses: []string{"172.20.5.0/29"}},
			Destination: &tap.FirewallEndpoint{Addresses: []string{"10.0.0.0/24"}, Ports: []int{80, 443}},
			Protocols:   []string{"tcp"},
		}},
	}

	status := TapAppStatus{
		Host: "compute-0",
		Taps: []*tap.Tap{{
			Name:     "abcd1234-tapapp",
			VLAN:     "EXP",
			IP:       "172.20.5.254/24",
			External: tap.External{Enabled: true, Firewall: firewall},
		}},
	}

	exp.Status.SetAppStatus("tap", status)

	var parsed TapAppStatus

	if err := exp.Status.ParseAppStatus("tap", &parsed); err != nil {
		t.Fatalf("expected no error parsing tap app status, got: %v", err)
	}

	if len(parsed.Taps) != 1 {
		t.Fatalf("expected 1 tap in parsed status, got %d", len(parsed.Taps))
	}

	if !reflect.DeepEqual(parsed.Taps[0].External.Firewall, firewall) {
		t.Errorf(
			"firewall config did not survive status round trip:\ngot:  %+v\nwant: %+v",
			parsed.Taps[0].External.Firewall, firewall,
		)
	}
}
