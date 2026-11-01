package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// An entry with a role parses into its fields; one without fails, naming
// only its position and, when it has a password field, its name.
func TestParseDefaultUser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		entry   string
		want    DefaultUser
		wantErr string
	}{
		"user": {
			entry: "alice:pw:Global Admin",
			want:  DefaultUser{Name: "alice", Password: "pw", Role: "Global Admin", Resources: []string{}},
		},
		"with resources": {
			entry: "bob:pw:vm-viewer:exp1:exp2",
			want:  DefaultUser{Name: "bob", Password: "pw", Role: "vm-viewer", Resources: []string{"exp1", "exp2"}},
		},
		"empty fields": {entry: "::", want: DefaultUser{Name: "", Password: "", Role: "", Resources: []string{}}},
		"no role": {
			entry:   "carol:secret",
			want:    DefaultUser{Name: "carol"},
			wantErr: `default user 3 ("carol") is not name:password:role[:resource...]`,
		},
		"one field": {entry: "secret", want: DefaultUser{}, wantErr: `default user 3 ("") is not name:password:role[:resource...]`},
		"empty":     {entry: "", want: DefaultUser{}, wantErr: `default user 3 ("") is not name:password:role[:resource...]`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDefaultUser(3, tc.entry)

			if gotErr := errText(err); gotErr != tc.wantErr {
				t.Errorf("ParseDefaultUser(%q) error = %q, want %q", tc.entry, gotErr, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ParseDefaultUser(%q) mismatch (-want +got):\n%s", tc.entry, diff)
			}
		})
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// Any entry of three or more colon-separated fields parses back into them,
// and a shorter one fails naming only its position and name.
func TestParseDefaultUserProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		entry := rapid.OneOf(rapid.String(), rapid.StringMatching(`[a-z:]{0,12}`)).Draw(t, "entry")
		fields := strings.Split(entry, ":")

		got, err := ParseDefaultUser(1, entry)

		if len(fields) < 3 {
			if err == nil {
				t.Fatalf("ParseDefaultUser(%q) = %+v, want an error", entry, got)
			}

			var name string
			if len(fields) == 2 {
				name = fields[0]
			}

			want := fmt.Sprintf("default user 1 (%q) is not name:password:role[:resource...]", name)
			if err.Error() != want || got.Name != name || got.Password != "" {
				t.Fatalf("ParseDefaultUser(%q) = %+v, %q, want only the name %q", entry, got, err, name)
			}

			return
		}

		if err != nil {
			t.Fatalf("ParseDefaultUser(%q) error = %v", entry, err)
		}

		back := strings.Join(append([]string{got.Name, got.Password, got.Role}, got.Resources...), ":")
		if back != entry {
			t.Fatalf("ParseDefaultUser(%q) = %+v, which joins back to %q", entry, got, back)
		}
	})
}
