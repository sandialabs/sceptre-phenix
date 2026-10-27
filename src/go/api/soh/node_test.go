package soh

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/mm"
)

func TestVMNodeCarriesTopologyNotes(t *testing.T) {
	v := mm.VM{
		ID:     3,
		Name:   "host-01",
		Type:   "Router",
		OSType: "linux",
		Tags:   map[string]string{"role": "dc", "__notes_2026-10-27T12:00:00.000Z": "VM note."},
		Notes:  []string{"Domain controller.", "Reset the password\nbefore each run."},
	}

	got := vmNode(v, "running")

	want := Node{
		ID:     3,
		Label:  "host-01",
		Image:  "Router",
		Tags:   v.Tags,
		Notes:  v.Notes,
		Status: "running",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("vmNode() mismatch (-want +got):\n%s", diff)
	}
}

func TestNodeJSONOmitsEmptyNotes(t *testing.T) {
	cases := []struct {
		name  string
		notes []string
		want  string
	}{
		{"no notes", nil, `{"id":0,"label":"","image":"","tags":null,"status":"","soh":null}`},
		{
			"notes",
			[]string{"a"},
			`{"id":0,"label":"","image":"","tags":null,"notes":["a"],"status":"","soh":null}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(Node{Notes: tc.notes})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			if diff := cmp.Diff(tc.want, string(got)); diff != "" {
				t.Errorf("json.Marshal() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
