package builder

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

// TestPruneHostPins keeps the pins whose hostname is exactly a node's, and
// removes the others, sorted by hostname.
func TestPruneHostPins(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		schedules   map[string]string
		hostnames   []string
		wantKept    map[string]string
		wantRemoved []HostPin
	}{
		{
			name:        "unchanged",
			schedules:   map[string]string{"plc-1": "compute1", "hmi": "compute2"},
			hostnames:   []string{"plc-1", "hmi", "historian"},
			wantKept:    map[string]string{"plc-1": "compute1", "hmi": "compute2"},
			wantRemoved: nil,
		},
		{
			name:        "renamed",
			schedules:   map[string]string{"plc-1": "compute1", "hmi": "compute2"},
			hostnames:   []string{"plc-1b", "hmi"},
			wantKept:    map[string]string{"hmi": "compute2"},
			wantRemoved: []HostPin{{Hostname: "plc-1", Host: "compute1", RenamedTo: ""}},
		},
		{
			name:      "removed",
			schedules: map[string]string{"plc-2": "compute3", "plc-1": "compute1", "hmi": "compute2"},
			hostnames: []string{"hmi"},
			wantKept:  map[string]string{"hmi": "compute2"},
			wantRemoved: []HostPin{
				{Hostname: "plc-1", Host: "compute1", RenamedTo: ""},
				{Hostname: "plc-2", Host: "compute3", RenamedTo: ""},
			},
		},
		{
			name:      "every pin removed",
			schedules: map[string]string{"plc-1": "compute1"},
			hostnames: nil,
			wantKept:  map[string]string{},
			wantRemoved: []HostPin{
				{Hostname: "plc-1", Host: "compute1", RenamedTo: ""},
			},
		},
		{
			name:      "case differs",
			schedules: map[string]string{"PLC-1": "compute1", "hmi": "compute2", "Historian": "compute3"},
			hostnames: []string{"plc-1", "HMI", "Historian"},
			wantKept:  map[string]string{"Historian": "compute3"},
			wantRemoved: []HostPin{
				{Hostname: "PLC-1", Host: "compute1", RenamedTo: ""},
				{Hostname: "hmi", Host: "compute2", RenamedTo: ""},
			},
		},
		{
			name:        "no pins",
			schedules:   map[string]string{},
			hostnames:   []string{"plc-1"},
			wantKept:    map[string]string{},
			wantRemoved: nil,
		},
		{
			name:        "nil pins",
			schedules:   nil,
			hostnames:   []string{"plc-1"},
			wantKept:    nil,
			wantRemoved: nil,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			kept, removed := PruneHostPins(test.schedules, test.hostnames)

			if diff := cmp.Diff(test.wantKept, kept); diff != "" {
				t.Errorf("PruneHostPins(%v, %q) kept mismatch (-want +got):\n%s", test.schedules, test.hostnames, diff)
			}

			if diff := cmp.Diff(test.wantRemoved, removed); diff != "" {
				t.Errorf("PruneHostPins(%v, %q) removed mismatch (-want +got):\n%s", test.schedules, test.hostnames, diff)
			}
		})
	}
}

// TestPruneHostPinsKeepsUnchangedSchedules returns the schedules themselves
// when no pin is removed, so an update writes them as they were.
func TestPruneHostPinsKeepsUnchangedSchedules(t *testing.T) {
	t.Parallel()

	schedules := map[string]string{"plc-1": "compute1"}

	kept, _ := PruneHostPins(schedules, []string{"plc-1"})
	kept["hmi"] = "compute2"

	if _, ok := schedules["hmi"]; !ok {
		t.Errorf("PruneHostPins(%v, [plc-1]) returned a copy, want the schedules themselves", schedules)
	}
}

// renamesDevice returns a device node with the ID and hostname given.
func renamesDevice(id, hostname string) builder.Node {
	return builder.Node{
		ID: id, Kind: builder.NodeKindDevice, Label: hostname,
		Device: &builder.Device{Hostname: hostname, Spec: map[string]any{}, Interfaces: nil},
	}
}

// renamesDocument returns a document with the nodes given.
func renamesDocument(nodes ...builder.Node) *builder.Document {
	document := builder.NewDocument("renames")
	document.Nodes = nodes

	return document
}

// TestDeviceRenames finds the new hostname of a device by its node ID: the
// ID it has in the earlier document, else the ID an import gives it.
func TestDeviceRenames(t *testing.T) {
	t.Parallel()

	const randomID = "6f1c2b8e-3d4a-4c5b-9e7f-0a1b2c3d4e5f"

	published := renamesDocument(renamesDevice(randomID, "plc-1"), renamesDevice(builder.DeviceNodeID("hmi"), "hmi"))

	for _, test := range []struct {
		name     string
		previous *builder.Document
		current  *builder.Document
		hostname string
		want     string
	}{
		{
			name:     "renamed since the earlier document",
			previous: published,
			current:  renamesDocument(renamesDevice(randomID, "plc-1b")),
			hostname: "plc-1",
			want:     "plc-1b",
		},
		{
			name:     "renamed in case only",
			previous: published,
			current:  renamesDocument(renamesDevice(randomID, "PLC-1")),
			hostname: "plc-1",
			want:     "PLC-1",
		},
		{
			name:     "not renamed",
			previous: published,
			current:  renamesDocument(renamesDevice(randomID, "plc-1")),
			hostname: "plc-1",
			want:     "",
		},
		{
			name:     "deleted",
			previous: published,
			current:  renamesDocument(renamesDevice(builder.DeviceNodeID("hmi"), "hmi")),
			hostname: "plc-1",
			want:     "",
		},
		{
			name:     "imported and renamed, no earlier document",
			previous: nil,
			current:  renamesDocument(renamesDevice(builder.DeviceNodeID("hmi"), "hmi-2")),
			hostname: "hmi",
			want:     "hmi-2",
		},
		{
			name:     "imported in another case and renamed",
			previous: nil,
			current:  renamesDocument(renamesDevice(builder.DeviceNodeID("HMI"), "hmi-2")),
			hostname: "hmi",
			want:     "hmi-2",
		},
		{
			name:     "not in the earlier document, imported and renamed",
			previous: published,
			current:  renamesDocument(renamesDevice(builder.DeviceNodeID("historian"), "historian-2")),
			hostname: "historian",
			want:     "historian-2",
		},
		{
			name:     "added in the editor, no earlier document",
			previous: nil,
			current:  renamesDocument(renamesDevice(randomID, "plc-1b")),
			hostname: "plc-1",
			want:     "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			renames := NewDeviceRenames(test.previous, test.current)

			if got := renames.RenamedTo(test.hostname); got != test.want {
				t.Errorf("RenamedTo(%q) = %q, want %q", test.hostname, got, test.want)
			}
		})
	}
}
