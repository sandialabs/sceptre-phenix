package file

import (
	"strings"
	"testing"
	"time"
)

// FuzzBuildTree checks that BuildTree and Evaluate do not panic on any filter,
// and that a filter in double quotes is one term with the lower-case text.
func FuzzBuildTree(f *testing.F) {
	for _, seed := range []string{
		"", " ", "web", "web db", "web and db", "not web", "(web or db) and pcap", "((web))", "()", ")", "(",
		">100", "> 1 kb", ">= 2 mb", "<1gb", "=>5", ">99999999999999999999 kb", "=b", ">kb",
		">2024-01", "> 2024-01-15", "=2024-01-15 10", "<= 2024-01-15 10:30:00", "=2024-01-15-01-01 1:2:3:4",
		`"vm"`, `'packet`, "packet", "elf", "vm1", "-sp-32-sp-", ">-sp-32-sp-2024-01",
		`"web eth0"`, `"web`, `""`, `"`, `"> 1 kb" or db`, `a"b c"d`, `"2024-01-15 10" > 2024-01`,
	} {
		f.Add(seed)
	}

	file := File{Name: "vm_eth0.pcap", Size: 1500, dateTime: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)}

	f.Fuzz(func(t *testing.T, filter string) {
		if filter != "" && !strings.Contains(filter, `"`) {
			tree := BuildTree(`"` + filter + `"`)
			if tree == nil || tree.left != nil || tree.right != nil || tree.term != strings.ToLower(filter) {
				t.Errorf("BuildTree(%q) = %s, want the term %q", `"`+filter+`"`, renderSearchTree(tree), strings.ToLower(filter))
			}
		}

		if tree := BuildTree(filter); tree != nil {
			tree.Evaluate(&file)
			tree.Evaluate(&File{})
		}
	})
}
