package file

import (
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
)

// fileListing is minimega's answer to `file list` from host.
func fileListing(host string, names ...string) *minicli.Response {
	rows := make([][]string, 0, len(names))

	for _, name := range names {
		rows = append(rows, []string{"", name, "1", "2024-01-01T00:00:00Z"})
	}

	return mmtest.Tabular(host, []string{"dir", "name", "size", "modified"}, rows...)
}

func TestDeleteExistingFiles(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		answers map[string][]*minicli.Response // by command; anything else succeeds with no output
		names   []string
		want    []string
		wantErr string // pattern the error matches, "" for none
	}{
		"deletes only the copies found, each where it is": {
			answers: map[string][]*minicli.Response{
				"mesh send all file list /head_foo_vm*": {
					fileListing("compute1", "head_foo_vm1_snapshot", "head_foo_vm10_snapshot", "head_foo_vm1_snapshot.bak"),
					fileListing("compute2", "head_foo_vm2_snapshot"),
					fileListing("compute3"),
				},
				"file list /head_foo_vm*": {fileListing("head", "head_foo_vm1_snapshot")},
			},
			names: []string{"head_foo_vm1_snapshot", "head_foo_vm2_snapshot", "head_foo_vm3_snapshot"},
			want: []string{
				"mesh send all file list /head_foo_vm*",
				"file list /head_foo_vm*",
				"mesh send compute1 file delete head_foo_vm1_snapshot",
				"file delete head_foo_vm1_snapshot",
				"mesh send compute2 file delete head_foo_vm2_snapshot",
			},
		},
		"deletes a file from every node having it in one command": {
			answers: map[string][]*minicli.Response{
				"mesh send all file list /exp/a*": {fileListing("n2", "exp/a"), fileListing("n1", "exp/a")},
			},
			names: []string{"/exp/a"},
			want: []string{
				"mesh send all file list /exp/a*",
				"file list /exp/a*",
				"mesh send n1,n2 file delete exp/a",
			},
		},
		"deletes everywhere when a node cannot list": {
			answers: map[string][]*minicli.Response{
				"mesh send all file list /head_foo_vm*": {
					fileListing("compute1"),
					{Host: "compute2", Error: "timed out"},
				},
			},
			names: []string{"head_foo_vm1_snapshot", "head_foo_vm2_snapshot"},
			want: []string{
				"mesh send all file list /head_foo_vm*",
				"mesh send all file delete head_foo_vm1_snapshot",
				"file delete head_foo_vm1_snapshot",
				"mesh send all file delete head_foo_vm2_snapshot",
				"file delete head_foo_vm2_snapshot",
			},
		},
		"deletes everywhere when a listing has no name column": {
			answers: map[string][]*minicli.Response{
				"file list /snap*": {mmtest.Tabular("head", []string{"dir"}, []string{"snap"})},
			},
			names: []string{"snap"},
			want: []string{
				"mesh send all file list /snap*",
				"file list /snap*",
				"mesh send all file delete snap",
				"file delete snap",
			},
		},
		"deletes everywhere when the directory cannot be globbed": {
			names: []string{"ex p/a"},
			want:  []string{"mesh send all file delete ex p/a", "file delete ex p/a"},
		},
		"reports a failed delete, naming the file": {
			answers: map[string][]*minicli.Response{
				"file list /snap*": {fileListing("head", "snap")},
				"file delete snap": {{Host: "head", Error: "permission denied"}},
			},
			names:   []string{"snap"},
			want:    []string{"mesh send all file list /snap*", "file list /snap*", "file delete snap"},
			wantErr: `(?s)^deleting file snap from headnode: .*permission denied`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				return tc.answers[cmd.Base]
			})

			err := DeleteExistingFiles(tc.names)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("DeleteExistingFiles(%q) error = %v, want nil", tc.names, err)
			case tc.wantErr != "" && (err == nil || !regexp.MustCompile(tc.wantErr).MatchString(err.Error())):
				t.Errorf("DeleteExistingFiles(%q) error = %v, want one matching %q", tc.names, err, tc.wantErr)
			}

			cmds := received()

			if diff := cmp.Diff(tc.want, mmtest.Bases(cmds)); diff != "" {
				t.Errorf("DeleteExistingFiles(%q) commands mismatch (-want +got):\n%s", tc.names, diff)
			}

			for _, cmd := range cmds {
				if cmd.Namespace != "" {
					t.Errorf("DeleteExistingFiles(%q) sent %q in namespace %q, want none", tc.names, cmd.Base, cmd.Namespace)
				}
			}
		})
	}
}

func TestCommandSafe(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]bool{
		"exp/files/vm_eth0.pcap": true,
		"head_foo_vm1_snapshot":  true,
		"a b":                    false,
		"a*":                     false,
		"a?":                     false,
		"a[1]":                   false,
		"a{b,c}":                 false,
		`a\b`:                    false,
		`a"b`:                    false,
		"a'b":                    false,
		"a`b":                    false,
		"a#b":                    false,
		"a\tb":                   false,
		"a\nb":                   false,
		"a\x00b":                 false,
		"a\u0085b":               false,
		"a\u00a0b":               false,
		"a\u3000b":               false,
	} {
		if got := CommandSafe(path); got != want {
			t.Errorf("CommandSafe(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestListingPattern(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		dir   string
		names []string
		want  string
		ok    bool
	}{
		{".", []string{"h_foo_a_snapshot", "h_foo_b_snapshot"}, "/h_foo_*", true},
		{".", []string{"h_foo_a_snapshot"}, "/h_foo_a_snapshot*", true},
		{".", []string{"abc", "xyz"}, "/", true},
		{".", []string{"h_f[o]o_a", "h_f[o]o_b"}, "/h_f*", true},
		{".", []string{"*a", "*b"}, "/", true},
		{"exp/files", []string{"exp/files/a1", "exp/files/a2"}, "/exp/files/a*", true},
		{"ex p", []string{"ex p/a"}, "", false},
		{"exp", []string{"exp/.a", "exp/.b"}, "/exp/.*", true},
		{"exp", []string{"exp/..a", "exp/..b"}, "/exp/..*", true},
		{".", []string{"..a", "..b"}, "/..*", true},
	} {
		got, ok := listingPattern(tc.dir, tc.names)
		if got != tc.want || ok != tc.ok {
			t.Errorf("listingPattern(%q, %q) = %q, %t; want %q, %t", tc.dir, tc.names, got, ok, tc.want, tc.ok)
		}
	}
}

// genDeleteBase draws a file name with glob and command-line syntax, never "." or "..",
// which a cleaned path has no base of.
func genDeleteBase() *rapid.Generator[string] {
	return rapid.StringMatching(`[ab_.][ab_*?\[{ \\.]{0,4}`).Filter(func(s string) bool { return s != "." && s != ".." })
}

// The listing pattern of names in one directory is the directory itself or a glob whose only
// syntax is a trailing "*", and it lists every name; a directory holding glob syntax is refused.
func TestListingPatternProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		dir := rapid.SampledFrom([]string{".", "exp", "exp/files", "e*p", "a b"}).Draw(t, "dir")
		bases := rapid.SliceOfN(genDeleteBase(), 1, 4).Draw(t, "bases")

		names := make([]string, len(bases))
		for i, base := range bases {
			names[i] = strings.TrimPrefix(path.Join("/", dir, base), "/")
		}

		pattern, ok := listingPattern(dir, names)
		if wantOK := !strings.ContainsAny(dir, globMeta); ok != wantOK {
			t.Fatalf("listingPattern(%q, %q) = %q, %t; want ok %t", dir, names, pattern, ok, wantOK)
		}

		if !ok {
			return
		}

		if i := strings.IndexAny(strings.TrimSuffix(pattern, "*"), globMeta); i >= 0 {
			t.Fatalf("listingPattern(%q, %q) = %q, has glob syntax before its end", dir, names, pattern)
		}

		for _, name := range names {
			listed, err := path.Match(pattern, "/"+name)
			if err != nil || !listed && pattern != path.Join("/", dir) {
				t.Fatalf("listingPattern(%q, %q) = %q, does not list %q", dir, names, pattern, name)
			}
		}

		if len(names) == 1 && !strings.ContainsAny(names[0], globMeta) && pattern != "/"+names[0]+"*" {
			t.Fatalf("listingPattern(%q, %q) = %q, want %q", dir, names, pattern, "/"+names[0]+"*")
		}
	})
}
