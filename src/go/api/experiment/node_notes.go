package experiment

import (
	"strings"
	"time"

	ifaces "phenix/types/interfaces"
)

const (
	// NoteLabelPrefix starts the key of every node label that holds a VM note.
	// minimega holds the labels of a node as the tags of its VM. The Notes
	// section of the VM Labels dialog of the web UI (VMLabelsModal.vue) and
	// State of Health show the tags with such keys as the notes of the VM.
	NoteLabelPrefix = "__notes_"

	// noteKeyTimeLayout is the time after NoteLabelPrefix in the key of a
	// note: UTC to the millisecond, as JavaScript Date.toISOString writes it
	// when the Labels dialog names a note it adds. Keys of this form sort in
	// time order. The dialog and State of Health list notes in that order.
	noteKeyTimeLayout = "2006-01-02T15:04:05.000Z"
)

// noteLabels returns the labels that add notes to a node that holds labels, as
// VM notes. It makes one label per note. The key is NoteLabelPrefix plus the
// time at, plus one millisecond per earlier note, so the keys sort in note
// order. It leaves out a note when:
//   - the note is empty
//   - a label already has its key (it never overwrites a label)
//   - the node already holds a note with the same text, so an experiment made
//     from a config that already carries its notes does not repeat them
//
// Each note the node holds stands for one note of notes. Thus a text that
// notes repeats is left out only as many times as the node holds it. It
// returns nil when it adds no note.
func noteLabels(labels map[string]string, notes []string, at time.Time) map[string]string {
	if len(notes) == 0 {
		return nil
	}

	// How many notes with each text the node holds.
	held := make(map[string]int)

	for key, value := range labels {
		if strings.HasPrefix(key, NoteLabelPrefix) {
			held[value]++
		}
	}

	var (
		added = make(map[string]string, len(notes))
		start = at.UTC().Truncate(time.Millisecond)
	)

	for i, note := range notes {
		if note == "" {
			continue
		}

		if held[note] > 0 {
			held[note]--

			continue
		}

		key := NoteLabelPrefix + start.Add(time.Duration(i)*time.Millisecond).Format(noteKeyTimeLayout)

		if _, ok := labels[key]; ok {
			continue
		}

		added[key] = note
	}

	if len(added) == 0 {
		return nil
	}

	return added
}

// seedNodeNotes copies the notes (general.notes) of every node of topo into
// the labels of the node as VM notes (see noteLabels), keyed from the time at.
// minimega sets the labels of a node as the tags of its VM when the experiment
// starts, so the notes show in the VM Labels dialog and in State of Health.
// Only the creation of an experiment calls it. Thus an update or a start of
// the experiment keeps the notes edited in that dialog, which the dialog saves
// in the labels.
func seedNodeNotes(topo ifaces.TopologySpec, at time.Time) {
	for _, node := range topo.Nodes() {
		general := node.General()
		if general == nil {
			continue
		}

		for key, note := range noteLabels(node.Labels(), general.Notes(), at) {
			node.AddLabel(key, note)
		}
	}
}
