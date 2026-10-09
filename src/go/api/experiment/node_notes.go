package experiment

import (
	"strings"
	"time"

	ifaces "phenix/types/interfaces"
)

const (
	// NoteLabelPrefix starts the key of every node label that holds a VM note.
	// minimega holds a node's labels as its VM's tags, and the Notes section of
	// the web UI's VM Labels dialog (VMLabelsModal.vue) and State of Health show
	// the tags with such keys as the VM's notes.
	NoteLabelPrefix = "__notes_"

	// noteKeyTimeLayout is the time after NoteLabelPrefix in a note's key: UTC
	// to the millisecond, as JavaScript's Date.toISOString writes it when the
	// Labels dialog names a note it adds. Keys of this form sort in time order,
	// which is the order the dialog and State of Health list notes in.
	noteKeyTimeLayout = "2006-01-02T15:04:05.000Z"
)

// noteLabels returns the labels that add notes to a node that holds labels,
// as VM notes: one label per note, keyed NoteLabelPrefix and the time at plus
// one millisecond per note before it, so the keys sort in note order. A note is
// left out when it is empty, when a label already has its key, which is never
// overwritten, or when the node already holds a note with the same text: an
// experiment made from a config that carries its notes already does not repeat
// them. Each note the node holds stands for one note of notes, so a text that
// notes repeats is left out only as many times as the node holds it. It
// returns nil when no note is added.
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
// the node's labels as VM notes (see noteLabels), keyed from the time at.
// minimega sets a node's labels as its VM's tags when the experiment starts,
// so the notes show in the VM Labels dialog and in State of Health. Only the
// creation of an experiment calls it, so the notes edited in that dialog,
// which it saves in the labels, are kept when the experiment is updated or
// started.
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
