package editor

import "testing"

// TestModified checks that any difference in the edited bytes counts as a
// change, and that identical bytes do not.
func TestModified(t *testing.T) {
	tests := []struct {
		name        string
		old, edited []byte
		want        bool
	}{
		{name: "same", old: []byte("a: 1\n"), edited: []byte("a: 1\n"), want: false},
		{name: "nil and empty", old: nil, edited: []byte{}, want: false},
		{name: "changed", old: []byte("a: 1\n"), edited: []byte("a: 2\n"), want: true},
		{name: "trailing newline", old: []byte("a: 1"), edited: []byte("a: 1\n"), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := modified(tt.old, tt.edited); got != tt.want {
				t.Errorf("modified(%q, %q) = %t, want %t", tt.old, tt.edited, got, tt.want)
			}
		})
	}
}
