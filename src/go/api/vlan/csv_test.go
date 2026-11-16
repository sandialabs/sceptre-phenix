package vlan

import "testing"

// csvText puts an apostrophe before a cell that a spreadsheet would read as
// a formula, and keeps every other cell.
func TestCSVText(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"":            "",
		"SCADA":       "SCADA",
		"a=b":         "a=b",
		"=SUM(A1)":    "'=SUM(A1)",
		"+1":          "'+1",
		"-1":          "'-1",
		"@cmd":        "'@cmd",
		"\tx":         "'\tx",
		"\rx":         "'\rx",
		"=a;compute1": "'=a;compute1",
		"compute1;=b": "compute1;=b",
	} {
		if got := csvText(in); got != want {
			t.Errorf("csvText(%q) = %q, want %q", in, got, want)
		}
	}
}
