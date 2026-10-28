package audit

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// docsPath is the page whose Audited Actions table lists every action.
const docsPath = "../../../../docs/content/logging.md"

// backticked matches the code spans of a table cell.
var backticked = regexp.MustCompile("`([^`]+)`")

// declaredActions returns the Action constants audit.go declares, by name.
func declaredActions(t *testing.T) map[string]Action {
	t.Helper()

	f, err := parser.ParseFile(token.NewFileSet(), "audit.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing audit.go: %v", err)
	}

	actions := make(map[string]Action)

	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}

		for _, spec := range gen.Specs {
			vs, _ := spec.(*ast.ValueSpec)
			if typ, ok := vs.Type.(*ast.Ident); !ok || typ.Name != "Action" {
				continue
			}

			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("audit.go: %s is not a string literal", name.Name)
				}

				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("audit.go: unquoting %s: %v", name.Name, err)
				}

				actions[name.Name] = Action(value)
			}
		}
	}

	return actions
}

// spread returns the cell's code spans, one per action: a single span applies
// to every action, and otherwise there must be one for each.
func spread(cell string, n int) ([]string, bool) {
	matches := backticked.FindAllStringSubmatch(cell, -1)

	spans := make([]string, 0, len(matches))
	for _, m := range matches {
		spans = append(spans, m[1])
	}

	switch len(spans) {
	case n:
		return spans, true
	case 1:
		out := make([]string, n)
		for i := range out {
			out[i] = spans[0]
		}

		return out, true
	default:
		return nil, false
	}
}

// documentedActions returns the actions the Audited Actions table of the
// logging page lists, with their category and type.
func documentedActions(t *testing.T) map[Action]kind {
	t.Helper()

	f, err := os.Open(docsPath)
	if err != nil {
		t.Fatalf("opening %s: %v", docsPath, err)
	}

	defer func() { _ = f.Close() }()

	var (
		documented = make(map[Action]kind)
		inSection  bool
		inRows     bool // past the table's header
		scanner    = bufio.NewScanner(f)
	)

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "#"):
			inSection, inRows = line == "### Audited Actions", false

			continue
		case inSection && strings.HasPrefix(line, "| :"):
			inRows = true

			continue
		case !inRows || !strings.HasPrefix(line, "| `"):
			continue
		}

		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 4 {
			t.Errorf("%s: row %q has %d cells, want 4", docsPath, line, len(cells))

			continue
		}

		names, _ := spread(cells[0], len(backticked.FindAllString(cells[0], -1)))
		categories, okCategory := spread(cells[1], len(names))
		types, okType := spread(cells[2], len(names))

		if !okCategory || !okType {
			t.Errorf("%s: row %q gives neither one category and type nor one per action", docsPath, line)

			continue
		}

		for i, name := range names {
			if _, ok := documented[Action(name)]; ok {
				t.Errorf("%s: action %s is listed twice", docsPath, name)
			}

			documented[Action(name)] = kind{category: categories[i], typ: types[i]}
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("reading %s: %v", docsPath, err)
	}

	return documented
}

// Every Action constant has an ECS category and type, and the Audited Actions
// table in docs/content/logging.md lists each of them with those, and no
// other action.
func TestActionsMatchDocs(t *testing.T) {
	declared := make(map[Action]kind)

	for name, a := range declaredActions(t) {
		k, ok := kinds[a]
		if !ok {
			t.Errorf("audit.%s (%q) has no ECS category and type in kinds", name, a)
		}

		declared[a] = k
	}

	for a := range kinds {
		if _, ok := declared[a]; !ok {
			t.Errorf("kinds holds %q, which no Action constant declares", a)
		}
	}

	if len(declared) == 0 {
		t.Fatal("audit.go declares no Action constants")
	}

	opt := cmp.AllowUnexported(kind{})

	if diff := cmp.Diff(declared, documentedActions(t), opt); diff != "" {
		t.Errorf("%s Audited Actions table mismatch (-audit.go +docs):\n%s", docsPath, diff)
	}
}

func TestSpread(t *testing.T) {
	for _, tc := range []struct {
		cell   string
		n      int
		want   []string
		wantOK bool
	}{
		{" `file` ", 3, []string{"file", "file", "file"}, true},
		{" `start`, `end` ", 2, []string{"start", "end"}, true},
		{" `start`, `end` ", 3, nil, false},
		{" none ", 1, nil, false},
	} {
		got, ok := spread(tc.cell, tc.n)
		if ok != tc.wantOK {
			t.Errorf("spread(%q, %d) ok = %t, want %t", tc.cell, tc.n, ok, tc.wantOK)
		}

		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("spread(%q, %d) mismatch (-want +got):\n%s", tc.cell, tc.n, diff)
		}
	}
}
