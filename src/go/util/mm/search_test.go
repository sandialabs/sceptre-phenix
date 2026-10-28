package mm

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// renderSearchTree writes a filter tree as an s-expression. A leaf term with a
// space or a parenthesis is in double quotes. A leaf that does not use the
// default search fields shows its fields after "@".
func renderSearchTree(node *ExpressionTree) string {
	if node == nil {
		return "<nil>"
	}

	if node.left == nil && node.right == nil {
		term := node.term
		if strings.ContainsAny(term, " ()") {
			term = `"` + term + `"`
		}

		if slices.Equal(node.searchFields, defaultSearchFields) {
			return term
		}

		return term + "@" + strings.Join(node.searchFields, ",")
	}

	parts := []string{node.term}

	if node.left != nil {
		parts = append(parts, renderSearchTree(node.left))
	}

	if node.right != nil {
		parts = append(parts, renderSearchTree(node.right))
	}

	return "(" + strings.Join(parts, " ") + ")"
}

func TestBuildTree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filter string
		want   string
	}{
		{"web", "web"},
		{"WEB", "web"},
		{"web db", "(and web db)"},
		{"web db router", "(and (and web db) router)"},
		{"web and db", "(and web db)"},
		{"web AND db", "(and web db)"},
		{"web or db", "(or web db)"},
		{"web or db or router", "(or (or web db) router)"},
		{"web and db and router", "(and (and web db) router)"},
		{"not db and web", "(and (not db) web)"},
		{"web and db or running", "(or (and web db) running@State)"},
		{"web or db and running", "(or web (and db running@State))"},
		{"web or not db and running", "(or web (and (not db) running@State))"},
		{"not web or db", "(or (not web) db)"},
		{"web db or x", "(or (and web db) x)"},
		{"x or web db", "(or x (and web db))"},
		{"web not db", "(and web (not db))"},
		{"(web)", "web"},
		{"((web))", "web"},
		{"(web compute1)", "(and web compute1)"},
		{"(web)(db)", "(and web db)"},
		{"web (db or x)", "(and web (or db x))"},
		{"(db or x) web", "(and (or db x) web)"},
		{"web or (db or x)", "(or web (or db x))"},
		{"not (web or db) running", "(and (not (or web db)) running@State)"},
		{"not running", "(not running@State)"},
		{"not not running", "(not (not running@State))"},
		{"running and not web", "(and running@State (not web))"},
		{"(web or db) and running", "(and (or web db) running@State)"},
		{"( web or db ) and running", "(and (or web db) running@State)"},
		{"running and (web or db)", "(and running@State (or web db))"},
		{"web and (db or not running)", "(and web (or db (not running@State)))"},
		{"not (web and db)", "(not (and web db))"},
		{"10.0.0.0/24", "10.0.0.0/24@IPv4"},
		{"10.0.0.5", "10.0.0.5@IPv4"},
		{"error", "error@State"},
		{"quit", "quit@State"},
		{"running", "running@State"},
		{"shutdown", "shutdown@State"},
		{"paused", "paused@State"},
		{"runningvm", "runningvm"},
		{"capturing", "capturing@Captures"},
		{"busy", "busy@Busy"},
		{"dnb", "dnb@DoNotBoot"},
		{`"running"`, "running"},
		{`'busy'`, "busy"},
		{`"10.0.0.0/24" and dnb`, "(and 10.0.0.0/24 dnb@DoNotBoot)"},
		{`"web server"`, `"web server"`},
		{`"Web Server"`, `"web server"`},
		{`"web  server "`, `"web  server "`},
		{`"web server" db`, `(and "web server" db)`},
		{`db "web server"`, `(and db "web server")`},
		{`"web server" or running`, `(or "web server" running@State)`},
		{`not "web server"`, `(not "web server")`},
		{`("web server")`, `"web server"`},
		{`("web server" or db) running`, `(and (or "web server" db) running@State)`},
		{`web"db server"`, `(and web "db server")`},
		{`"db server"web`, `(and "db server" web)`},
		{`"not web"`, `"not web"`},
		{`"(web) or db"`, `"(web) or db"`},
		{`"and"`, "and"},
		{`"running" "busy"`, "(and running busy)"},
		{`"is running"`, `"is running"`},
		{`"it's"`, "it's"},
		{`"10.0.0.0/24 db"`, `"10.0.0.0/24 db"`},
	}

	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			t.Parallel()

			got := renderSearchTree(BuildTree(tt.filter))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("BuildTree(%q) mismatch (-want +got):\n%s", tt.filter, diff)
			}
		})
	}
}

func TestBuildTreeMalformed(t *testing.T) {
	t.Parallel()

	for _, filter := range []string{
		"", " ", "   ", "and", "OR", "not", "and and", "or or or", "not not",
		")", "web )", "()", "not )", "web ) or db",
		"(", "( web", "(web", "web (", "(web or db", "web or db)", "((web)", "(web))",
		"web and", "web or", "and web", "or web", "web and or db", "web not", "not and web",
		"(not)", "web ()", "() web", "(and web)", "(web or)", "web and )",
		`"`, `""`, `"web`, `web "`, `"web server`, `web "db server`, `"web" "db`, `web ""`, `"" or web`,
		`"web" and`, `("web"`, `"web")`, `"a"b"`,
	} {
		t.Run(filter, func(t *testing.T) {
			t.Parallel()

			if got := BuildTree(filter); got != nil {
				t.Errorf("BuildTree(%q) = %s, want nil", filter, renderSearchTree(got))
			}
		})
	}
}

// createTree rejects postfix input that postfix never gives, such as an
// operator with no operands or operands with no operator.
func TestCreateTreeMalformed(t *testing.T) {
	t.Parallel()

	for _, postFix := range [][]string{nil, {"not"}, {"and"}, {"web", "or"}, {"web", "db"}} {
		t.Run(strings.Join(postFix, " "), func(t *testing.T) {
			t.Parallel()

			if got, err := createTree(postFix); !errors.Is(err, errMalformedFilter) {
				t.Errorf("createTree(%q) = %s, %v, want error %v", postFix, renderSearchTree(got), err, errMalformedFilter)
			}
		})
	}
}

func TestEvaluateNilTree(t *testing.T) {
	t.Parallel()

	var tree *ExpressionTree

	if tree.Evaluate(&VM{Name: "web"}) {
		t.Error("nil ExpressionTree.Evaluate() = true, want false")
	}
}

func TestStackPopEmpty(t *testing.T) {
	t.Parallel()

	stack := new(Stack)
	if got := stack.Pop(); got != nil {
		t.Errorf("empty Stack.Pop() = %v, want nil", got)
	}

	stack.Push("a")
	stack.Push("b")

	var got []any
	for !stack.IsEmpty() {
		got = append(got, stack.Pop())
	}

	if diff := cmp.Diff([]any{"b", "a"}, got); diff != "" {
		t.Errorf("Stack.Pop() order mismatch (-want +got):\n%s", diff)
	}
}

func searchTestVM() VM {
	return VM{
		Name:     "Web-Server",
		Host:     "Compute1",
		Disk:     "/phenix/images/Ubuntu.qc2",
		IPv4:     []string{"bogus", "10.0.0.5"},
		Networks: []string{"EXP_MGMT", "OT"},
		Tags:     map[string]string{"role": "Frontend"},
		State:    "RUNNING",
	}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()

	base := searchTestVM()

	with := func(change func(*VM)) VM {
		vm := searchTestVM()
		change(&vm)

		return vm
	}

	tests := []struct {
		name   string
		filter string
		vm     VM
		want   bool
	}{
		{"name substring", "web", base, true},
		{"name ignores case", "SERVER", base, true},
		{"host", "compute1", base, true},
		{"disk", "ubuntu", base, true},
		{"network", "mgmt", base, true},
		{"second network", "ot", with(func(vm *VM) { vm.Name = "x" }), true},
		{"tag value", "frontend", base, true},
		{"no field matches", "nothing", base, false},
		{"empty VM", "web", VM{}, false},
		{"cidr contains address", "10.0.0.0/24", base, true},
		{"cidr does not contain address", "10.1.0.0/16", base, false},
		{"cidr with no addresses", "10.0.0.0/24", with(func(vm *VM) { vm.IPv4 = nil }), false},
		{"invalid cidr", "10.0.0.0/33", base, false},
		{"bare address matches", "10.0.0.5", base, true},
		{"bare address is exact", "10.0.0.50", with(func(vm *VM) { vm.IPv4 = []string{"10.0.0.5"} }), false},
		{"bare address other", "10.0.0.6", base, false},
		{"bare address prefix", "10.0.0.5", with(func(vm *VM) { vm.IPv4 = []string{"10.0.0.50"} }), false},
		{"bare address with no addresses", "10.0.0.5", with(func(vm *VM) { vm.IPv4 = nil }), false},
		{"bare address and state", "10.0.0.5 running", base, true},
		{"state running", "running", base, true},
		{"state paused", "paused", base, false},
		{"state paused match", "paused", with(func(vm *VM) { vm.State = "PAUSED" }), true},
		{"state error", "error", with(func(vm *VM) { vm.State = "ERROR" }), true},
		{"state quit", "quit", with(func(vm *VM) { vm.State = "QUIT" }), true},
		{"state shutdown is quit", "shutdown", with(func(vm *VM) { vm.State = "QUIT" }), true},
		{"state shutdown on running vm", "shutdown", base, false},
		{"busy false", "busy", base, false},
		{"busy true", "busy", with(func(vm *VM) { vm.Busy = true }), true},
		{"dnb false", "dnb", base, false},
		{"dnb true", "dnb", with(func(vm *VM) { vm.DoNotBoot = true }), true},
		{"capturing false", "capturing", base, false},
		{"capturing true", "capturing", with(func(vm *VM) { vm.Captures = []Capture{{VM: "web"}} }), true},
		{"quoted state searches names", `"running"`, base, false},
		{"quoted state matches name", `"running"`, with(func(vm *VM) { vm.Name = "running-vm" }), true},
		{"quoted busy matches host", `'busy'`, with(func(vm *VM) { vm.Host = "busy-host" }), true},
		{"quoted space matches name", `"web server"`, with(func(vm *VM) { vm.Name = "My Web Server" }), true},
		{"quoted space needs the space", `"web server"`, base, false},
		{"unquoted words match apart", "web server", base, true},
		{"quoted space matches tag", `"front end"`, with(func(vm *VM) { vm.Tags = map[string]string{"role": "Front End"} }), true},
		{"quoted space matches disk", `"my images"`, with(func(vm *VM) { vm.Disk = "/my images/a.qc2" }), true},
		{"quoted operator words", `"not web"`, with(func(vm *VM) { vm.Name = "not web" }), true},
		{"quoted parentheses", `"(db)"`, with(func(vm *VM) { vm.Name = "web (db)" }), true},
		{"quoted state words search names", `"is running"`, base, false},
		{"not quoted space", `not "web server"`, base, true},
		{"quoted space and state", `"web-server" running`, base, true},
		{"implicit and both", "web compute1", base, true},
		{"implicit and one", "web compute2", base, false},
		{"and both", "web and running", base, true},
		{"and left false", "db and running", base, false},
		{"and right false", "web and paused", base, false},
		{"or left", "web or db", base, true},
		{"or right", "db or web", base, true},
		{"or neither", "db or paused", base, false},
		{"not true", "not web", base, false},
		{"not false", "not db", base, true},
		{"not not", "not not web", base, true},
		{"parentheses", "(db or web) and running", base, true},
		{"parentheses false", "(db or router) and running", base, false},
		{"not parentheses", "not (web and running)", base, false},
		{"nested", "web and (db or not paused)", base, true},
		{"cidr and state", "10.0.0.0/8 and running", base, true},
		{"not binds before and", "not web and db", base, false},
		{"and binds before or", "db and web or running", base, true},
		{"and binds before or on the right", "running or db and zz", base, true},
		{"or of two and groups", "db and web or zz and running", base, false},
		{"implicit and before or", "db web or zz", base, false},
		{"implicit and after or", "zz or web running", base, true},
		{"group of one term", "(web)", base, true},
		{"group of one missing term", "(db)", base, false},
		{"group with implicit and", "(web compute1)", base, true},
		{"implicit and before group", "db (web or zz)", base, false},
		{"implicit and before group true", "web (db or running)", base, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tree := BuildTree(tt.filter)
			if tree == nil {
				t.Fatalf("BuildTree(%q) = nil, want a tree", tt.filter)
			}

			if got := tree.Evaluate(&tt.vm); got != tt.want {
				t.Errorf("BuildTree(%q).Evaluate(%+v) = %t, want %t", tt.filter, tt.vm, got, tt.want)
			}
		})
	}
}

// searchFilterNode is a filter expression that renderSearchNode writes as
// text and that eval evaluates without the parser.
type searchFilterNode struct {
	op          string
	atom        string
	left, right *searchFilterNode
}

func (n *searchFilterNode) eval(vm *VM) bool {
	switch n.op {
	case "not":
		return !n.right.eval(vm)
	case "and":
		return n.left.eval(vm) && n.right.eval(vm)
	case "or":
		return n.left.eval(vm) || n.right.eval(vm)
	default:
		if text, ok := strings.CutPrefix(n.atom, `"`); ok {
			return vmHasText(vm, strings.ToLower(strings.TrimSuffix(text, `"`)))
		}

		return BuildTree(n.atom).Evaluate(vm)
	}
}

// vmHasText reports whether the name, a network, the host, the disk or a tag
// of vm has text in it, with no change to case of text.
func vmHasText(vm *VM, text string) bool {
	fields := append([]string{vm.Name, vm.Host, vm.Disk}, vm.Networks...)
	for _, tag := range vm.Tags {
		fields = append(fields, tag)
	}

	return slices.ContainsFunc(fields, func(field string) bool {
		return strings.Contains(strings.ToLower(field), text)
	})
}

func drawSearchNode(t *rapid.T, depth int) *searchFilterNode {
	kind := "atom"
	if depth > 0 {
		kind = rapid.SampledFrom([]string{"atom", "not", "and", "or"}).Draw(t, "kind")
	}

	switch kind {
	case "not":
		return &searchFilterNode{op: kind, right: drawSearchNode(t, depth-1)}
	case "and", "or":
		return &searchFilterNode{op: kind, left: drawSearchNode(t, depth-1), right: drawSearchNode(t, depth-1)}
	default:
		return &searchFilterNode{atom: rapid.SampledFrom([]string{
			"web", "db", "compute1", "mgmt", "frontend", "ubuntu", "WEB",
			"running", "quit", "shutdown", "paused", "error",
			"busy", "dnb", "capturing", `"running"`, `'busy'`,
			`"web server"`, `"Web 1"`, `"server (db)"`, `"not web"`, `"or"`, `"it's"`, `"exp_mgmt ot"`,
			"10.0.0.0/24", "192.168.0.0/16", "10.0.0.0/33", "10.0.0.5", "172.16.0.1",
		}).Draw(t, "atom")}
	}
}

// renderSearchNode writes n with parentheses only where the operator
// precedence needs them, and sometimes with more. It sometimes writes "and"
// as two terms with no operator between them. It returns the text and the
// precedence of its outer operator.
func renderSearchNode(t *rapid.T, n *searchFilterNode) (string, int) {
	wrap := func(text string, inner, outer int) string {
		if inner < outer || rapid.Bool().Draw(t, "parentheses") {
			return "(" + text + ")"
		}

		return text
	}

	var (
		text string
		prec int
	)

	switch n.op {
	case "not":
		right, rightPrec := renderSearchNode(t, n.right)
		text, prec = "not "+wrap(right, rightPrec, precedenceNot), precedenceNot
	case "and", "or":
		prec = precedence(n.op)
		left, leftPrec := renderSearchNode(t, n.left)
		right, rightPrec := renderSearchNode(t, n.right)

		operator := " " + n.op + " "
		if n.op == "and" && rapid.Bool().Draw(t, "implicit") {
			operator = " "
		}

		text = wrap(left, leftPrec, prec) + operator + wrap(right, rightPrec, prec)
	default:
		return n.atom, precedenceNot + 1
	}

	return text, prec
}

func drawSearchVM(t *rapid.T) *VM {
	pick := func(label string, values ...string) string {
		return rapid.SampledFrom(values).Draw(t, label)
	}

	list := func(label string, values ...string) []string {
		return rapid.SliceOfN(rapid.SampledFrom(values), 0, 3).Draw(t, label)
	}

	vm := &VM{
		Name:      pick("name", "web-1", "DB-2", "running-vm", "busy", "", "Web Server (DB)", "web 1", "it's not web or db"),
		Host:      pick("host", "compute1", "compute2"),
		Disk:      pick("disk", "ubuntu.qc2", "kali.qc2", ""),
		IPv4:      list("ipv4", "10.0.0.5", "192.168.1.20", "172.16.0.1", "bogus", ""),
		Networks:  list("networks", "EXP_MGMT", "ot", "web"),
		State:     pick("state", "RUNNING", "QUIT", "PAUSED", "ERROR", "BUILDING", "running"),
		Busy:      rapid.Bool().Draw(t, "busy"),
		DoNotBoot: rapid.Bool().Draw(t, "dnb"),
	}

	tagKeys := rapid.SampledFrom([]string{"role", "zone"})
	tagValues := rapid.SampledFrom([]string{"frontend", "db"})
	vm.Tags = rapid.MapOfN(tagKeys, tagValues, 0, 2).Draw(t, "tags")

	if rapid.Bool().Draw(t, "capturing") {
		vm.Captures = []Capture{{VM: vm.Name}}
	}

	return vm
}

// A filter evaluates as its model, which gives "not" the highest precedence,
// then "and", then "or". "not" before the filter in parentheses gives the
// opposite result. A filter with an unclosed parenthesis, an unclosed quote
// or a trailing operator does not parse.
func TestEvaluateMatchesModel(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		node := drawSearchNode(t, 4)
		filter, _ := renderSearchNode(t, node)
		vm := drawSearchVM(t)

		tree := BuildTree(filter)
		if tree == nil {
			t.Fatalf("BuildTree(%q) = nil, want a tree", filter)
		}

		want := node.eval(vm)
		if got := tree.Evaluate(vm); got != want {
			t.Fatalf("BuildTree(%q).Evaluate(%+v) = %t, want %t", filter, *vm, got, want)
		}

		negated := "not (" + filter + ")"
		if got := BuildTree(negated).Evaluate(vm); got != !want {
			t.Fatalf("BuildTree(%q).Evaluate(%+v) = %t, want %t", negated, *vm, got, !want)
		}

		for _, malformed := range []string{"(" + filter, filter + ")", filter + " and", "or " + filter, filter + ` "`, `"` + filter} {
			if got := BuildTree(malformed); got != nil {
				t.Fatalf("BuildTree(%q) = %s, want nil", malformed, renderSearchTree(got))
			}
		}
	})
}
