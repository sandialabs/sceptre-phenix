package types_test

import (
	"errors"
	"strings"
	"testing"

	"phenix/types"
)

// The bounds the v1 and v2 schemas put on a node's general.notes.
const (
	maxNotes      = 100
	maxNoteLength = 4096
)

// noteVersions are the schema versions whose nodes take general.notes.
func noteVersions() []string {
	return []string{"v1", "v2"}
}

// vmNodeWithNotes returns a minimega node whose general.notes is notes.
func vmNodeWithNotes(notes any) map[string]any {
	return map[string]any{
		"type":    "VirtualMachine",
		"general": map[string]any{"hostname": "host-01", "notes": notes},
		"hardware": map[string]any{
			"os_type": "linux",
			"drives":  []any{map[string]any{"image": "ubuntu.qc2"}},
		},
	}
}

// externalNodeWithNotes returns an external node whose general.notes is notes.
func externalNodeWithNotes(notes any) map[string]any {
	return map[string]any{
		"external": true,
		"type":     "HIL",
		"general":  map[string]any{"hostname": "plc-01", "notes": notes},
	}
}

// repeatedNotes returns count notes.
func repeatedNotes(count int) []any {
	notes := make([]any, count)

	for i := range notes {
		notes[i] = "note"
	}

	return notes
}

func TestNodeNotesSchemaAccepts(t *testing.T) {
	cases := map[string][]any{
		"one note":                {"Domain controller."},
		"a note of several lines": {"Reset the password\nbefore each run."},
		"the most notes":          repeatedNotes(maxNotes),
		"the longest note":        {strings.Repeat("x", maxNoteLength)},
		"no notes":                {},
	}

	for _, ver := range noteVersions() {
		for name, notes := range cases {
			t.Run(ver+"/"+name, func(t *testing.T) {
				if err := types.ValidateSchema("minimega_node", ver, vmNodeWithNotes(notes)); err != nil {
					t.Errorf("minimega_node: ValidateSchema() error = %v", err)
				}

				if err := types.ValidateSchema("external_node", ver, externalNodeWithNotes(notes)); err != nil {
					t.Errorf("external_node: ValidateSchema() error = %v", err)
				}

				topology := map[string]any{
					"nodes": []any{vmNodeWithNotes(notes), externalNodeWithNotes(notes)},
				}

				if err := types.ValidateSchema("Topology", ver, topology); err != nil {
					t.Errorf("Topology: ValidateSchema() error = %v", err)
				}
			})
		}
	}
}

func TestNodeNotesSchemaRejects(t *testing.T) {
	cases := []struct {
		name        string
		notes       any
		wantPointer []string
		wantField   string
	}{
		{
			name:        "a note that is not text",
			notes:       []any{"Domain controller.", 42},
			wantPointer: []string{"general", "notes", "1"},
			wantField:   "type",
		},
		{
			name:        "an empty note",
			notes:       []any{"Domain controller.", ""},
			wantPointer: []string{"general", "notes", "1"},
			wantField:   "minLength",
		},
		{
			name:        "a note that is too long",
			notes:       []any{strings.Repeat("x", maxNoteLength+1)},
			wantPointer: []string{"general", "notes", "0"},
			wantField:   "maxLength",
		},
		{
			name:        "too many notes",
			notes:       repeatedNotes(maxNotes + 1),
			wantPointer: []string{"general", "notes"},
			wantField:   "maxItems",
		},
		{
			name:        "notes that are not a list",
			notes:       "Domain controller.",
			wantPointer: []string{"general", "notes"},
			wantField:   "type",
		},
	}

	for _, ver := range noteVersions() {
		for _, tc := range cases {
			t.Run(ver+"/"+tc.name, func(t *testing.T) {
				err := types.ValidateSchema("minimega_node", ver, vmNodeWithNotes(tc.notes))
				assertSchemaError(t, err, tc.wantPointer, tc.wantField)

				err = types.ValidateSchema("external_node", ver, externalNodeWithNotes(tc.notes))
				assertSchemaError(t, err, tc.wantPointer, tc.wantField)

				// In a topology, a node is one of the two node schemas, so the
				// error names the node rather than the note.
				topology := map[string]any{"nodes": []any{vmNodeWithNotes(tc.notes)}}

				err = types.ValidateSchema("Topology", ver, topology)
				if !errors.Is(err, types.ErrValidationFailed) {
					t.Errorf("Topology: ValidateSchema() error = %v, want ErrValidationFailed", err)
				}
			})
		}
	}
}
