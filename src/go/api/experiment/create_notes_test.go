package experiment_test

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
)

// notesNode returns a topology node, as a stored topology holds it, with the
// given general settings and labels.
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

// TestCreateCopiesNodeNotesToLabels creates an experiment and checks that the
// experiment it stores holds each node's notes as VM notes in its labels.
func TestCreateCopiesNodeNotesToLabels(t *testing.T) {
	const (
		expName  = "notes-exp"
		topoName = "notes-topo"
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	topology := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: topoName},
		Spec: map[string]any{
			"nodes": []any{
				notesNode(
					"host-01",
					map[string]any{"notes": []any{"Domain controller.", "Reset the password\nbefore each run."}},
					map[string]any{"role": "dc"},
				),
				notesNode("host-02", map[string]any{}, map[string]any{"role": "web"}),
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

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	err := experiment.Create(
		context.Background(),
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

	// The key of a VM note, as the VM Labels dialog writes one.
	noteKey := regexp.MustCompile(`^__notes_\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

	labels := host.Labels()
	notes := make([]string, 0, len(labels))

	for _, key := range slices.Sorted(maps.Keys(labels)) {
		if !strings.HasPrefix(key, experiment.NoteLabelPrefix) {
			continue
		}

		if !noteKey.MatchString(key) {
			t.Errorf("note key %q is not __notes_ and a UTC time to the millisecond", key)
		}

		notes = append(notes, labels[key])
	}

	if want := []string{"Domain controller.", "Reset the password\nbefore each run."}; !slices.Equal(notes, want) {
		t.Errorf("notes in the labels of host-01 = %q, want %q", notes, want)
	}

	if labels["role"] != "dc" {
		t.Errorf("label role of host-01 = %q, want dc", labels["role"])
	}

	other := exp.Spec.Topology().FindNodeByName("host-02")
	if other == nil {
		t.Fatal("the stored experiment has no node host-02")
	}

	if want := map[string]string{"role": "web"}; !maps.Equal(other.Labels(), want) {
		t.Errorf("labels of host-02, which has no notes = %v, want %v", other.Labels(), want)
	}
}
