package experiment

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	v1 "phenix/types/version/v1"
)

// noteTime returns the time the tests seed notes at: 11:34:56.789 UTC, given
// in another zone and with nanoseconds the keys leave out.
func noteTime() time.Time {
	return time.Date(2026, 10, 9, 12, 34, 56, 789_654_321, time.FixedZone("UTC+1", int(time.Hour.Seconds())))
}

// notesInKeyOrder returns the values of the note labels among labels, in the
// order of their keys, which is the order the VM Labels dialog lists them in.
func notesInKeyOrder(labels map[string]string) []string {
	notes := make([]string, 0, len(labels))

	for _, key := range slices.Sorted(maps.Keys(labels)) {
		if strings.HasPrefix(key, NoteLabelPrefix) {
			notes = append(notes, labels[key])
		}
	}

	return notes
}

func TestNoteLabelsKeysAndOrder(t *testing.T) {
	got := noteLabels(nil, []string{"First note.", "Second\nnote.", "Third note."}, noteTime())

	want := map[string]string{
		"__notes_2026-10-09T11:34:56.789Z": "First note.",
		"__notes_2026-10-09T11:34:56.790Z": "Second\nnote.",
		"__notes_2026-10-09T11:34:56.791Z": "Third note.",
	}

	if !maps.Equal(got, want) {
		t.Fatalf("noteLabels() = %v, want %v", got, want)
	}
}

func TestNoteLabelsKeepOrderAcrossSeconds(t *testing.T) {
	at := time.Date(2026, 10, 9, 11, 34, 56, 998_000_000, time.UTC)
	notes := []string{"one", "two", "three", "four"}

	got := noteLabels(nil, notes, at)

	if _, ok := got["__notes_2026-10-09T11:34:57.000Z"]; !ok {
		t.Errorf("noteLabels() = %v, want a key for 11:34:57.000", got)
	}

	if order := notesInKeyOrder(got); !slices.Equal(order, notes) {
		t.Errorf("notes in key order = %q, want %q", order, notes)
	}
}

func TestNoteLabelsNeverOverwrite(t *testing.T) {
	labels := map[string]string{
		"__notes_2026-10-09T11:34:56.790Z": "Edited in the VM Labels dialog.",
		"role":                             "dc",
	}

	got := noteLabels(labels, []string{"First note.", "Second note.", "Third note."}, noteTime())

	// The second note's key is taken, so that note is left out and the label
	// keeps its value.
	want := map[string]string{
		"__notes_2026-10-09T11:34:56.789Z": "First note.",
		"__notes_2026-10-09T11:34:56.791Z": "Third note.",
	}

	if !maps.Equal(got, want) {
		t.Fatalf("noteLabels() = %v, want %v", got, want)
	}
}

func TestNoteLabelsSkipNotesTheNodeHolds(t *testing.T) {
	labels := map[string]string{
		"__notes_2025-01-01T00:00:00.000Z": "First note.",
		"comment":                          "Second note.",
	}

	got := noteLabels(labels, []string{"First note.", "Second note.", "", "Second note."}, noteTime())

	// Only notes the node held before are left out, so the second "Second
	// note." is added too, and a label that is not a note does not count.
	want := map[string]string{
		"__notes_2026-10-09T11:34:56.790Z": "Second note.",
		"__notes_2026-10-09T11:34:56.792Z": "Second note.",
	}

	if !maps.Equal(got, want) {
		t.Fatalf("noteLabels() = %v, want %v", got, want)
	}
}

func TestNoteLabelsSkipEachHeldNoteOnce(t *testing.T) {
	labels := map[string]string{
		"__notes_2025-01-01T00:00:00.000Z": "TODO",
		"__notes_2025-01-01T00:00:00.001Z": "Check DNS.",
	}

	got := noteLabels(labels, []string{"TODO", "TODO", "Check DNS.", "TODO"}, noteTime())

	// The node holds "TODO" once, so only the first of the three is left out,
	// and "Check DNS." once, so it is left out.
	want := map[string]string{
		"__notes_2026-10-09T11:34:56.790Z": "TODO",
		"__notes_2026-10-09T11:34:56.792Z": "TODO",
	}

	if !maps.Equal(got, want) {
		t.Fatalf("noteLabels() = %v, want %v", got, want)
	}

	// Once the node holds every copy, none is added again.
	maps.Copy(labels, got)

	if again := noteLabels(labels, []string{"TODO", "TODO", "Check DNS.", "TODO"}, noteTime()); again != nil {
		t.Errorf("noteLabels() with every note held = %v, want nil", again)
	}
}

func TestNoteLabelsWithoutNotes(t *testing.T) {
	if got := noteLabels(map[string]string{"role": "dc"}, nil, noteTime()); got != nil {
		t.Errorf("noteLabels(no notes) = %v, want nil", got)
	}

	if got := noteLabels(nil, []string{""}, noteTime()); got != nil {
		t.Errorf("noteLabels(an empty note) = %v, want nil", got)
	}
}

func TestSeedNodeNotes(t *testing.T) {
	withNotes := &v1.Node{
		GeneralF: &v1.General{HostnameF: "host-01", NotesF: []string{"First note.", "Second note."}},
		LabelsF:  map[string]string{"role": "dc"},
	}
	withoutNotes := &v1.Node{
		GeneralF: &v1.General{HostnameF: "host-02"},
		LabelsF:  map[string]string{"role": "web"},
	}
	unlabelled := &v1.Node{GeneralF: &v1.General{HostnameF: "host-03"}}
	noGeneral := &v1.Node{}

	topo := &v1.TopologySpec{NodesF: []*v1.Node{withNotes, withoutNotes, unlabelled, noGeneral}}

	seedNodeNotes(topo, noteTime())

	want := map[string]string{
		"role":                             "dc",
		"__notes_2026-10-09T11:34:56.789Z": "First note.",
		"__notes_2026-10-09T11:34:56.790Z": "Second note.",
	}

	if !maps.Equal(withNotes.LabelsF, want) {
		t.Errorf("labels of a node with notes = %v, want %v", withNotes.LabelsF, want)
	}

	if wantOther := map[string]string{"role": "web"}; !maps.Equal(withoutNotes.LabelsF, wantOther) {
		t.Errorf("labels of a node without notes = %v, want %v", withoutNotes.LabelsF, wantOther)
	}

	if unlabelled.LabelsF != nil {
		t.Errorf("labels of a node without labels or notes = %v, want nil", unlabelled.LabelsF)
	}

	if noGeneral.LabelsF != nil {
		t.Errorf("labels of a node without general = %v, want nil", noGeneral.LabelsF)
	}

	// A second seeding adds nothing: the node holds the notes already.
	seedNodeNotes(topo, noteTime().Add(time.Hour))

	if !maps.Equal(withNotes.LabelsF, want) {
		t.Errorf("labels after a second seeding = %v, want %v", withNotes.LabelsF, want)
	}
}
