package mm

import (
	"strings"
	"testing"
)

// FuzzBuildTree checks that BuildTree and Evaluate do not panic on any filter,
// and that a filter in double quotes is one term with the lower-case text.
func FuzzBuildTree(f *testing.F) {
	for _, seed := range []string{
		"", " ", "web", "web db", "web and db", "web or db", "not running", "(web or db) and running",
		"((web))", "()", ")", "(", "( web", "web )", "and", "not not", "10.0.0.0/24", "10.0.0.5", "999.1.1.1/99",
		`"running"`, `'busy`, `"a"b"`, "capturing", "busy", "dnb", "shutdown", "quit", "WEB AND NOT DB",
		`"web server"`, `"web`, `""`, `"`, `"not (web)" or db`, `a"b c"d`, `("web server" or db) running`,
	} {
		f.Add(seed)
	}

	vm := searchTestVM()
	vm.Captures = []Capture{{VM: vm.Name}}

	f.Fuzz(func(t *testing.T, filter string) {
		if filter != "" && !strings.Contains(filter, `"`) {
			tree := BuildTree(`"` + filter + `"`)
			if tree == nil || tree.left != nil || tree.right != nil || tree.term != strings.ToLower(filter) {
				t.Errorf("BuildTree(%q) = %s, want the term %q", `"`+filter+`"`, renderSearchTree(tree), strings.ToLower(filter))
			}
		}

		if tree := BuildTree(filter); tree != nil {
			tree.Evaluate(&vm)
			tree.Evaluate(&VM{})
		}
	})
}
