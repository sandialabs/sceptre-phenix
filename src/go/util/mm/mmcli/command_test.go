package mmcli

import "testing"

// TestCommandStringOmitsHostColumn: minicli reports the responding node outside
// the tabular header, so asking for it as a column either errors (`cc
// commands`) or silently prefix-matches `hostname` (`vm info`, `cc clients`).
func TestCommandStringOmitsHostColumn(t *testing.T) {
	cmd := NewNamespacedCommand("ns")
	cmd.Command = "cc commands"
	cmd.Columns = []string{"host", "id", "responses"}
	cmd.Filters = []string{"id=7"}

	want := `.record false namespace "ns" .columns "id","responses" .filter id=7 cc commands`
	if got := cmd.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}

	cmd.Columns = []string{"host"}

	want = `.record false namespace "ns" .filter id=7 cc commands`
	if got := cmd.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
