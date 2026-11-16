package file

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
		{"  WEB  ", "web"},
		{"web db", "(and web db)"},
		{"web and db", "(and web db)"},
		{"web or db", "(or web db)"},
		{"not web", "(not web)"},
		{"not not web", "(not (not web))"},
		{"(web or db) and pcap", "(and (or web db) pcap)"},
		{"pcap and (web or not db)", "(and pcap (or web (not db)))"},
		{"web db pcap", "(and (and web db) pcap)"},
		{"web or db or pcap", "(or (or web db) pcap)"},
		{"not db and web", "(and (not db) web)"},
		{"web and db or pcap", "(or (and web db) pcap)"},
		{"web or db and pcap", "(or web (and db pcap))"},
		{"web db or pcap", "(or (and web db) pcap)"},
		{"web not db", "(and web (not db))"},
		{"(web)", "web"},
		{"(web eth0)", "(and web eth0)"},
		{"web (db or pcap)", "(and web (or db pcap))"},
		{"(>100)", ">100@Size"},
		{"(> 2024-01-15)", ">2024-01-15@Date"},
		{">100", ">100@Size"},
		{"> 1 kb", ">1kb@Size"},
		{">= 2 mb", ">=2mb@Size"},
		{"<1gb", "<1gb@Size"},
		{"> 5 mb", ">5mb@Size"},
		{">5 bigfile", "(and >5@Size bigfile)"},
		{"> 5 kbytes", "(and >5@Size kbytes)"},
		{">5mbx", ">5mbx"},
		{"name>5", "name>5"},
		{"web>10mb", "web>10mb"},
		{"x>2024-01", "x>2024-01"},
		{"(>5mb)", ">5mb@Size"},
		{"web > 10", "(and web >10@Size)"},
		{">2024-01", ">2024-01@Date"},
		{"> 2024-01", ">2024-01@Date"},
		{"> 2024-01-15", ">2024-01-15@Date"},
		{"< 2024-01-15", "<2024-01-15@Date"},
		{"= 2024-01-15 10", "=2024-01-15_10@Date"},
		{"> 2024-01-15 10:30", ">2024-01-15_10:30@Date"},
		{"web > 2024-01-15 db", "(and (and web >2024-01-15@Date) db)"},
		{"=2024-01-15", "=2024-01-15@Date"},
		{"=2024-01-15 10", "=2024-01-15_10@Date"},
		{">=2024-01-15 10:30", ">=2024-01-15_10:30@Date"},
		{"<= 2024-01-15 10:30:00", "<=2024-01-15_10:30:00@Date"},
		{"web and >= 2024-01-15 10:30", "(and web >=2024-01-15_10:30@Date)"},
		{"packet", "packet@Name,Category"},
		{"ELF", "elf@Name,Category"},
		{"vm1", "vm1@Name,Category"},
		{`"vm"`, "vm"},
		{`'packet' or db`, "(or packet db)"},
		{`"web eth0"`, `"web eth0"`},
		{`"Web Eth0"`, `"web eth0"`},
		{`"web  eth0 "`, `"web  eth0 "`},
		{`"web eth0" pcap`, `(and "web eth0" pcap)`},
		{`not "web eth0"`, `(not "web eth0")`},
		{`("web eth0" or db) > 1 kb`, `(and (or "web eth0" db) >1kb@Size)`},
		{`web"db eth0"`, `(and web "db eth0")`},
		{`"(draft)"`, `"(draft)"`},
		{`"not web"`, `"not web"`},
		{`"or"`, "or"},
		{`"packet capture"`, `"packet capture"`},
		{`"> 1 kb"`, `"> 1 kb"`},
		{`">100"`, ">100"},
		{`"= 2024-01-15 10"`, `"= 2024-01-15 10"`},
		{`"2024-01-15 10" > 2024-01`, `(and "2024-01-15 10" >2024-01@Date)`},
		{`> 2024-01-15 10 "a b"`, `(and >2024-01-15_10@Date "a b")`},
		{`"it's"`, "it's"},
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
		"", " ", "   ", "and", "OR", "not", "and or", "not not", ")", "web )", "()", "not )", "web ) or db",
		"(", "( web", "(web", "web (", "(web or db", "web or db)", "web and", "web or", "and web", "web not",
		"(not)", "web ()", "(web or)", "> 2024-01-15 and", "(>100",
		`"`, `""`, `"web`, `web "`, `"web eth0`, `"web" "db`, `web ""`, `"web")`, `("web"`, `> 1 kb "a`,
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

	if tree.Evaluate(&File{Name: "web.pcap"}) {
		t.Error("nil ExpressionTree.Evaluate() = true, want false")
	}
}

func TestStackPopEmpty(t *testing.T) {
	t.Parallel()

	stack := new(Stack)
	if got := stack.Pop(); got != nil {
		t.Errorf("empty Stack.Pop() = %v, want nil", got)
	}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()

	file := File{
		Name:     "Web_eth0.pcap",
		Size:     1500,
		dateTime: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
	}

	tests := []struct {
		filter string
		want   bool
	}{
		{"web", true},
		{"ETH0", true},
		{"db", false},
		{`"web"`, true},

		{">1000", true},
		{">1500", false},
		{"<2000", true},
		{"<1500", false},
		{"=1500", true},
		{"=1499", false},
		{">=1500", true},
		{">=1501", false},
		{"<=1500", true},
		{"<=1499", false},
		{"> 1000", true},
		{"> 1 kb", true},
		{">1kb", true},
		{"<2kb", true},
		{"<1kb", false},
		{"=1500b", true},
		{"= 1500 b", true},
		{"< 1 mb", true},
		{">1mb", false},
		{"<1gb", true},
		{">1gb", false},
		{"=>1500", false},
		{"<>1500", false},
		{">99999999999999999999", false},
		{"> 99999999999999999999 kb", false},

		{">2024-01", false},
		{">2023-12", true},
		{"> 2023-12", true},
		{"<2024-01", false},
		{"< 2024-01", false},
		{"<2024-02", true},
		{"=2024-01", true},
		{"=2024-02", false},
		{">=2024-01", true},
		{"<=2023-12", false},
		{"<=2024-01", true},
		{">2024-01-14", true},
		{">2024-01-15", false},
		{"> 2024-01-15", false},
		{"> 2024-01-14", true},
		{"< 2024-01-16", true},
		{"< 2024-01-15", false},
		{"= 2024-01-15", true},
		{"= 2024-01-14", false},
		{">= 2024-01-15", true},
		{"<= 2024-01-15", true},
		{"<= 2024-01-14", false},
		{">2024-01-15 09", true},
		{">2024-01-15 10", false},
		{">2024-01-15 10:29", true},
		{">2024-01-15 10:30", false},
		{"> 2024-01-15 10:30", false},
		{">2024-01-15 10:30:44", true},
		{">2024-01-15 10:30:45", false},
		{"<2024-01-15 10:30", false},
		{"<=2024-01-15 10:30", true},
		{"<2024-01-15 10:30:45", false},
		{"<=2024-01-15 10:30:45", true},
		{">=2024-01-15 10:30:45", true},
		{">=2024-01-15 10:30:46", false},
		{">2024-01-16", false},
		{"<2024-01-16", true},
		{"<2024-01-15", false},
		{"=2024-01-15", true},
		{"=2024-01-14", false},
		{">=2024-01-15", true},
		{">=2024-01-16", false},
		{"<=2024-01-15", true},
		{"<=2024-01-14", false},
		{"=2024-01-15 10", true},
		{"=2024-01-15 11", false},
		{"=2024-01-15 10:30", true},
		{"=2024-01-15 10:31", false},
		{"<= 2024-01-15 10:30", true},
		{">= 2024-01-15 10:31", false},
		{"> 2024-01-15 11:00", false},
		{"< 2024-01-15 11:00", true},
		{"=2024-01-15 10:30:45", true},
		{"=2024-01-15 10:30:44", false},
		{"<2024-01-15 10:30:46", true},
		{"=2024-13", false},
		{"=2024-01-15-01", false},
		{"=2024-01-15 10:30:45:00", false},
		{"==2024-01-15", false},

		{"web and >1000", true},
		{"web and >2000", false},
		{"db or >1000", true},
		{"db or >2000", false},
		{"not >2000", true},
		{"not web", false},
		{"(db or web) and <=2024-01-15", true},
		{"web >1000", true},
		{"web >2000", false},
		{"(web)", true},
		{"(web eth0)", true},
		{"(db)", false},
		{"not db and web", true},
		{"not web and db", false},
		{"db and web or >1000", true},
		{"db web or <1000", false},
		{"db (web or >1000)", false},
		{"web (db or >1000)", true},
		{"web or db and <1000", true},
	}

	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			t.Parallel()

			tree := BuildTree(tt.filter)
			if tree == nil {
				t.Fatalf("BuildTree(%q) = nil, want a tree", tt.filter)
			}

			if got := tree.Evaluate(&file); got != tt.want {
				t.Errorf("BuildTree(%q).Evaluate(%+v) = %t, want %t", tt.filter, file, got, tt.want)
			}
		})
	}
}

// A double-quoted term is one term that keeps its spaces, and it matches the
// file name only. Words in it are not keywords, sizes or dates.
func TestEvaluateQuoted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filter string
		name   string
		want   bool
	}{
		{`"web eth0"`, "Web Eth0.pcap", true},
		{`"web eth0"`, "web_eth0.pcap", false},
		{"web eth0", "web_eth0.pcap", true},
		{`"web eth0" pcap`, "web eth0.pcap", true},
		{`"web eth0" or db`, "db.elf", true},
		{`not "web eth0"`, "web eth0.pcap", false},
		{`"(draft)"`, "notes (draft).txt", true},
		{`"not web"`, "not web.txt", true},
		{`"not web"`, "db.txt", false},
		{`"> 1 kb"`, "size > 1 kb.txt", true},
		{`"> 1 kb"`, "big.txt", false},
		{`"2024-01-15 10"`, "log 2024-01-15 10.txt", true},
		{`"it's"`, "it's.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.filter+" "+tt.name, func(t *testing.T) {
			t.Parallel()

			file := File{Name: tt.name, Size: 5000, dateTime: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)}

			tree := BuildTree(tt.filter)
			if tree == nil {
				t.Fatalf("BuildTree(%q) = nil, want a tree", tt.filter)
			}

			if got := tree.Evaluate(&file); got != tt.want {
				t.Errorf("BuildTree(%q).Evaluate(%+v) = %t, want %t", tt.filter, file, got, tt.want)
			}
		})
	}
}

func TestEvaluateCategory(t *testing.T) {
	t.Parallel()

	pcap := File{Name: "vm1_eth0.pcap", Categories: []string{"Packet Capture"}}
	elf := File{Name: "web.elf", Categories: []string{"ELF Memory Snapshot"}}
	disk := File{Name: "snap.qc2", Categories: []string{"VM Disk Snapshot"}}

	tests := []struct {
		name   string
		filter string
		file   File
		want   bool
	}{
		{"vm term in name", "vm1", pcap, true},
		{"vm term not in name or category", "vm2", pcap, false},
		{"packet category", "packet", pcap, true},
		{"packet category not set", "packet", elf, false},
		{"elf category", "elf", elf, true},
		{"elf term in name", "elf", File{Name: "web.elf"}, true},
		{"vm category", "vm", disk, true},
		{"vm category and name", "vm", pcap, true},
		{"vm term not in category", "vm1", disk, false},
		{"category ignores case", "ELF", elf, true},
		{"quoted term searches only the name", `"packet"`, pcap, false},
		{"quoted space searches only the name", `"packet capture"`, pcap, false},
		{"quoted space matches name", `"my capture"`, File{Name: "My Capture.pcap"}, true},
		{"other term searches only the name", "snapshot", disk, false},
		{"no categories", "packet", File{Name: "x.pcap"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := BuildTree(tt.filter).Evaluate(&tt.file); got != tt.want {
				t.Errorf("BuildTree(%q).Evaluate(%+v) = %t, want %t", tt.filter, tt.file, got, tt.want)
			}
		})
	}
}

// A date filter names a period in the time zone of the file time.
func TestEvaluateDateTimeZone(t *testing.T) {
	t.Parallel()

	file := File{Name: "late.pcap", dateTime: time.Date(2024, 1, 15, 23, 30, 0, 0, time.FixedZone("MST", -7*60*60))}

	for _, tt := range []struct {
		filter string
		want   bool
	}{
		{"=2024-01-15", true},
		{"<2024-01-16", true},
		{">2024-01-15", false},
		{"=2024-01-15 23", true},
		{">=2024-01-16", false},
	} {
		t.Run(tt.filter, func(t *testing.T) {
			t.Parallel()

			if got := BuildTree(tt.filter).Evaluate(&file); got != tt.want {
				t.Errorf("BuildTree(%q).Evaluate(%v) = %t, want %t", tt.filter, file.dateTime, got, tt.want)
			}
		})
	}
}

// A size filter compares the file size with the number times its unit.
func TestEvaluateSizeMatchesModel(t *testing.T) {
	t.Parallel()

	units := map[string]int64{"": 1, "b": 1, "kb": 1_000, "mb": 1_000_000, "gb": 1_000_000_000}
	compare := map[string]func(a, b int64) bool{
		"<":  func(a, b int64) bool { return a < b },
		">":  func(a, b int64) bool { return a > b },
		"=":  func(a, b int64) bool { return a == b },
		"<=": func(a, b int64) bool { return a <= b },
		">=": func(a, b int64) bool { return a >= b },
	}

	rapid.Check(t, func(t *rapid.T) {
		op := rapid.SampledFrom([]string{"<", ">", "=", "<=", ">="}).Draw(t, "op")
		unit := rapid.SampledFrom([]string{"", "b", "kb", "mb", "gb"}).Draw(t, "unit")
		count := rapid.Int64Range(0, 2_000_000).Draw(t, "count")
		opSpace := rapid.SampledFrom([]string{"", " "}).Draw(t, "opSpace")

		unitSpace := ""
		if unit != "" {
			unitSpace = rapid.SampledFrom([]string{"", " "}).Draw(t, "unitSpace")
		}

		limit := count * units[unit]
		size := rapid.OneOf(rapid.Int64Range(0, 3_000_000_000_000_000), rapid.Int64Range(limit-1, limit+1)).Draw(t, "size")
		filter := op + opSpace + strconv.FormatInt(count, 10) + unitSpace + unit

		file := File{Name: "capture.pcap", Size: size}

		want := compare[op](size, limit)
		if got := BuildTree(filter).Evaluate(&file); got != want {
			t.Fatalf("BuildTree(%q).Evaluate(size %d) = %t, want %t", filter, size, got, want)
		}
	})
}

// A date filter compares the file time with the period that the date names:
// "<" is before the period, "<=" before its end, "=" in it, ">=" at or after
// its start and ">" after its end.
func TestEvaluateDateMatchesModel(t *testing.T) {
	t.Parallel()

	layouts := []struct {
		format string
		next   func(time.Time) time.Time
	}{
		{"2006-01", func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }},
		{"2006-01-02", func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }},
		{"2006-01-02 15", func(t time.Time) time.Time { return t.Add(time.Hour) }},
		{"2006-01-02 15:04", func(t time.Time) time.Time { return t.Add(time.Minute) }},
		{"2006-01-02 15:04:05", func(t time.Time) time.Time { return t.Add(time.Second) }},
	}

	rapid.Check(t, func(t *rapid.T) {
		layout := rapid.SampledFrom(layouts).Draw(t, "layout")
		day := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, rapid.IntRange(0, 11_000).Draw(t, "day"))
		clock := time.Duration(rapid.Int64Range(0, 24*int64(time.Hour)-1).Draw(t, "clock"))

		start, err := time.Parse(layout.format, day.Add(clock).Format(layout.format))
		if err != nil {
			t.Fatalf("time.Parse(%q) error: %v", layout.format, err)
		}

		end := layout.next(start)
		at := start.Add(time.Duration(rapid.Int64Range(-3*24*int64(time.Hour), 3*24*int64(time.Hour)).Draw(t, "offset")))
		if rapid.Bool().Draw(t, "boundary") {
			near := rapid.SampledFrom([]time.Time{start, end}).Draw(t, "near")
			at = near.Add(time.Duration(rapid.Int64Range(-2, 2).Draw(t, "seconds")) * time.Second)
		}
		op := rapid.SampledFrom([]string{"<", "<=", "=", ">=", ">"}).Draw(t, "op")
		space := rapid.SampledFrom([]string{"", " "}).Draw(t, "space")

		want := map[string]bool{
			"<":  at.Before(start),
			"<=": at.Before(end),
			"=":  !at.Before(start) && at.Before(end),
			">=": !at.Before(start),
			">":  !at.Before(end),
		}[op]

		filter := op + space + start.Format(layout.format)
		file := File{Name: "capture.pcap", dateTime: at}

		if got := BuildTree(filter).Evaluate(&file); got != want {
			t.Fatalf("BuildTree(%q).Evaluate(%v) = %t, want %t", filter, at, got, want)
		}
	})
}

// searchFilterNode is a filter expression that renderSearchNode writes as
// text and that eval evaluates without the parser.
type searchFilterNode struct {
	op          string
	atom        string
	left, right *searchFilterNode
}

func (n *searchFilterNode) eval(file *File) bool {
	switch n.op {
	case "not":
		return !n.right.eval(file)
	case "and":
		return n.left.eval(file) && n.right.eval(file)
	case "or":
		return n.left.eval(file) || n.right.eval(file)
	default:
		if text, ok := strings.CutPrefix(n.atom, `"`); ok {
			return strings.Contains(strings.ToLower(file.Name), strings.ToLower(strings.TrimSuffix(text, `"`)))
		}

		return BuildTree(n.atom).Evaluate(file)
	}
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
			"web", "eth0", "db", "pcap", "WEB", "vm1", "packet", "elf", `"vm"`,
			`"web eth0"`, `"Notes (Draft)"`, `"> 1 kb"`, `"or not"`, `"packet capture"`, `"it's"`,
			">1000", "<2kb", "> 1 kb", "=1500", "=2024-01-15", "> 2024-01", "<= 2024-01-15 10:30", ">2024-01-15 10",
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

// A filter evaluates as its model, which gives "not" the highest precedence,
// then "and", then "or". "not" before the filter in parentheses gives the
// opposite result. A filter with an unclosed parenthesis, an unclosed quote
// or a trailing operator does not parse.
func TestEvaluateMatchesModel(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		node := drawSearchNode(t, 4)
		filter, _ := renderSearchNode(t, node)
		names := []string{
			"web_eth0.pcap", "DB.elf", "vm1_eth1.pcap", "notes.txt", "", "web eth0.pcap", "notes (draft) > 1 kb.txt", "it's or not",
		}
		categories := []string{"Packet Capture", "ELF Memory Snapshot", "VM Disk Snapshot"}
		minutes := rapid.IntRange(-2000, 2000).Draw(t, "minutes")
		file := &File{
			Name:       rapid.SampledFrom(names).Draw(t, "name"),
			Size:       rapid.SampledFrom([]int64{0, 999, 1000, 1500, 2000, 5000}).Draw(t, "size"),
			Categories: rapid.SliceOfN(rapid.SampledFrom(categories), 0, 2).Draw(t, "categories"),
			dateTime:   time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC).Add(time.Duration(minutes) * time.Minute),
		}

		tree := BuildTree(filter)
		if tree == nil {
			t.Fatalf("BuildTree(%q) = nil, want a tree", filter)
		}

		want := node.eval(file)
		if got := tree.Evaluate(file); got != want {
			t.Fatalf("BuildTree(%q).Evaluate(%+v) = %t, want %t", filter, *file, got, want)
		}

		negated := "not (" + filter + ")"
		if got := BuildTree(negated).Evaluate(file); got != !want {
			t.Fatalf("BuildTree(%q).Evaluate(%+v) = %t, want %t", negated, *file, got, !want)
		}

		for _, malformed := range []string{"(" + filter, filter + ")", filter + " and", "or " + filter, filter + ` "`, `"` + filter} {
			if got := BuildTree(malformed); got != nil {
				t.Fatalf("BuildTree(%q) = %s, want nil", malformed, renderSearchTree(got))
			}
		}
	})
}
