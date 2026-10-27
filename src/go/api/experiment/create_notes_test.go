package experiment_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
)

// notesNode returns a stored topology node with the given general settings and labels.
func notesNode(hostname string, general, labels map[string]any) map[string]any {
	general["hostname"] = hostname

	return map[string]any{
		"type":    "VirtualMachine",
		"general": general,
		"labels":  labels,
		"hardware": map[string]any{
			"os_type": "linux",
			"drives":  []any{map[string]any{"image": "ubuntu.qc2"}},
		},
	}
}

// TestCreateKeepsNodeNotesOutOfLabels checks that Create keeps notes in general.notes and adds no note labels.
func TestCreateKeepsNodeNotesOutOfLabels(t *testing.T) {
	const (
		expName  = "notes-exp"
		topoName = "notes-topo"
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	notes := []string{"Domain controller.", "Reset the password\nbefore each run."}

	topology := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: topoName},
		Spec: map[string]any{
			"nodes": []any{
				notesNode(
					"host-01",
					map[string]any{"notes": []any{notes[0], notes[1]}},
					map[string]any{"role": "dc"},
				),
			},
		},
	}

	var created *store.Config

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if strings.EqualFold(c.Kind, "Topology") && c.Metadata.Name == topoName {
			*c = topology

			return nil
		}

		return store.ErrNotExist
	}).AnyTimes()
	m.EXPECT().List(gomock.Any()).Return(store.Configs{}, nil).AnyTimes()
	m.EXPECT().Create(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		created = c

		return nil
	}).Times(1)

	storetest.Use(t, m)

	err := experiment.Create(
		t.Context(),
		experiment.CreateWithName(expName),
		experiment.CreateWithTopology(topoName),
		experiment.CreateWithBaseDirectory(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if created == nil {
		t.Fatal("Create() stored no config")
	}

	exp, err := types.DecodeExperimentFromConfig(*created)
	if err != nil {
		t.Fatalf("decoding the stored experiment: %v", err)
	}

	host := exp.Spec.Topology().FindNodeByName("host-01")
	if host == nil {
		t.Fatal("the stored experiment has no node host-01")
	}

	if want := map[string]string{"role": "dc"}; !maps.Equal(host.Labels(), want) {
		t.Errorf("labels of host-01 = %v, want %v", host.Labels(), want)
	}

	if got := host.General().Notes(); !slices.Equal(got, notes) {
		t.Errorf("general.notes of host-01 = %q, want %q", got, notes)
	}
}
