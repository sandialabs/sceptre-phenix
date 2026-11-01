package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
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

func TestTapHost(t *testing.T) {
	t.Parallel()

	hosts := mm.Hosts{{Name: "compute2", VMs: 1}, {Name: "head", VMs: 3}, {Name: "compute1", VMs: 1}}

	if got, err := tapHost(hosts); got != "compute1" || err != nil {
		t.Errorf("tapHost(%v) = %q, %v; want compute1, the first by name of those with fewest VMs", hosts, got, err)
	}

	if got, err := tapHost(nil); err == nil {
		t.Errorf("tapHost(nil) = %q, want an error", got)
	}
}

// genTapHosts draws host lists with few distinct names and VM counts, so ties
// are common.
func genTapHosts() *rapid.Generator[mm.Hosts] {
	return rapid.Custom(func(t *rapid.T) mm.Hosts {
		return rapid.SliceOfN(rapid.Custom(func(t *rapid.T) mm.Host {
			return mm.Host{
				Name: rapid.SampledFrom([]string{"head", "compute1", "compute2", "compute3"}).Draw(t, "name"),
				VMs:  rapid.IntRange(0, 3).Draw(t, "vms"),
			}
		}), 1, 6).Draw(t, "hosts")
	})
}

// The tap host has the fewest VMs, and the same hosts in any order give the
// same host.
func TestTapHostProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		hosts := genTapHosts().Draw(t, "hosts")

		got, err := tapHost(hosts)
		if err != nil {
			t.Fatalf("tapHost(%v) error = %v", hosts, err)
		}

		fewest := slices.MinFunc(hosts, func(a, b mm.Host) int { return a.VMs - b.VMs }).VMs
		if !slices.ContainsFunc(hosts, func(h mm.Host) bool { return h.Name == got && h.VMs == fewest }) {
			t.Fatalf("tapHost(%v) = %q, want a host with %d VMs", hosts, got, fewest)
		}

		perm := rapid.Permutation(hosts).Draw(t, "order")

		if again, _ := tapHost(perm); again != got {
			t.Fatalf("tapHost(%v) = %q, but %q for the same hosts in order %v", hosts, got, again, perm)
		}
	})
}

// namespaceHostsMM fakes the experiment's hosts; other mm.MM methods panic.
type namespaceHostsMM struct {
	mm.MM

	hosts mm.Hosts
}

func (m namespaceHostsMM) GetNamespaceHosts(string) (mm.Hosts, error) {
	return m.hosts, nil
}

func TestTapCreateFailsWithoutHosts(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM
	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = namespaceHostsMM{} //nolint:reassign // install test double

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "tap-test"},
		Spec: map[string]any{
			"experimentName": "tap-test",
			"scenario": map[string]any{"apps": []map[string]any{{
				"name":     "tap",
				"metadata": map[string]any{"taps": []map[string]any{{"vlan": "EXP", "ip": "10.0.0.254/24"}}},
			}}},
		},
	})
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig() error = %v", err)
	}

	var tapApp Tap

	err = tapApp.PostStart(context.Background(), exp)
	if err == nil || !strings.Contains(err.Error(), "no experiment hosts") {
		t.Errorf("PostStart() with no experiment hosts error = %v, want no experiment hosts", err)
	}
}
