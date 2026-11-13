package main

import (
	"errors"
	"os"
	"os/user"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"pgregory.net/rapid"

	"phenix/cmd"
)

// newRenderTestTree builds a small command tree: a root with two global flags, one of them with
// a documented default, a command with a subcommand, a runnable command and a hidden one.
func newRenderTestTree() *cobra.Command {
	root := &cobra.Command{Use: "app", Short: "An app"}
	root.PersistentFlags().String("level", "info", "log level")
	root.PersistentFlags().String("store", "/home/someone/store.db", "store path")
	_ = root.PersistentFlags().SetAnnotation("store", cmd.DocDefaultAnnotation, []string{"/etc/store.db as root"})

	alpha := &cobra.Command{Use: "alpha", Short: "Alpha things", Long: "\n  Alpha does\n    <many> things.\n"}
	beta := &cobra.Command{
		Use:     "beta NAME",
		Short:   "Run beta",
		Example: "\n  app alpha beta one\n",
		Run:     func(*cobra.Command, []string) {},
	}
	beta.Flags().Int("count", 1, "how many")
	alpha.AddCommand(beta)

	zeta := &cobra.Command{Use: "zeta", Short: "Zeta", Run: func(*cobra.Command, []string) {}}
	secret := &cobra.Command{Use: "secret", Short: "Hidden", Hidden: true, Run: func(*cobra.Command, []string) {}}

	root.AddCommand(zeta, alpha, secret)

	return root
}

func renderPages(t *testing.T, root *cobra.Command) map[string]page {
	t.Helper()

	pages, err := render(root)
	if err != nil {
		t.Fatalf("render() = %v, want no error", err)
	}

	byFile := make(map[string]page, len(pages))
	for _, p := range pages {
		byFile[p.File] = p
	}

	return byFile
}

func TestRenderPages(t *testing.T) {
	t.Parallel()

	pages, err := render(newRenderTestTree())
	if err != nil {
		t.Fatalf("render() = %v, want no error", err)
	}

	type entry struct{ File, Title string }

	got := make([]entry, 0, len(pages))
	for _, p := range pages {
		got = append(got, entry{p.File, p.Title})
	}

	want := []entry{{indexFile, "Overview"}, {"alpha.md", "app alpha"}, {"zeta.md", "app zeta"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("render() pages mismatch (-want +got):\n%s", diff)
	}
}

func TestRenderCommandPage(t *testing.T) {
	t.Parallel()

	got := renderPages(t, newRenderTestTree())["alpha.md"].Body

	want := "# app alpha\n\n" + generatedNote + "\n\n" +
		"Every command on this page also accepts the [global flags](index.md#options).\n\n" +
		"## app alpha\n\nAlpha things\n\n" +
		"### Synopsis\n\n```text\nAlpha does\n  <many> things.\n```\n\n" +
		"### Options\n\n```text\n  -h, --help   help for alpha\n```\n\n" +
		"### SEE ALSO\n\n" +
		"* [app](index.md) - An app\n" +
		"* [app alpha beta](alpha.md#app-alpha-beta) - Run beta\n\n" +
		"## app alpha beta\n\nRun beta\n\n" +
		"```text\napp alpha beta NAME [flags]\n```\n\n" +
		"### Examples\n\n```text\napp alpha beta one\n```\n\n" +
		"### Options\n\n```text\n      --count int   how many (default 1)\n  -h, --help        help for beta\n```\n\n" +
		"### SEE ALSO\n\n* [app alpha](alpha.md) - Alpha things\n"

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("render() alpha.md mismatch (-want +got):\n%s", diff)
	}
}

func TestRenderIndexHoldsGlobalFlags(t *testing.T) {
	t.Parallel()

	pages := renderPages(t, newRenderTestTree())

	for _, want := range []string{
		"--level string   log level (default \"info\")",
		"--store string   store path (default: /etc/store.db as root)",
		"* [app alpha](alpha.md) - Alpha things",
	} {
		if !strings.Contains(pages[indexFile].Body, want) {
			t.Errorf("render() index.md does not contain %q:\n%s", want, pages[indexFile].Body)
		}
	}

	for _, file := range []string{"alpha.md", "zeta.md"} {
		if body := pages[file].Body; strings.Contains(body, "--level") || strings.Contains(body, "inherited") {
			t.Errorf("render() %s lists the global flags, want them only on index.md:\n%s", file, body)
		}
	}
}

func TestRenderRestoresCommands(t *testing.T) {
	t.Parallel()

	root := newRenderTestTree()

	if _, err := render(root); err != nil {
		t.Fatalf("render() = %v, want no error", err)
	}

	level, store := root.PersistentFlags().Lookup("level"), root.PersistentFlags().Lookup("store")
	if level.Hidden || store.Hidden {
		t.Errorf("after render(), global flags hidden = %t, %t, want false", level.Hidden, store.Hidden)
	}

	if store.DefValue != "/home/someone/store.db" || store.Usage != "store path" {
		t.Errorf("after render(), --store default and usage = %q, %q, want them restored", store.DefValue, store.Usage)
	}

	alpha, _, _ := root.Find([]string{"alpha"})
	if alpha.Long != "\n  Alpha does\n    <many> things.\n" {
		t.Errorf("after render(), alpha.Long = %q, want it restored", alpha.Long)
	}
}

var markdownLink = regexp.MustCompile(`\]\(([^)#]+)(?:#([^)]+))?\)`)

// hostSpecific returns patterns that match the home directory, user name and host name of this
// host only where a flag default shows them: as a path or a URL host. A bare word such as "root"
// or "phenix" can also be in the help text, so it does not count.
func hostSpecific() []*regexp.Regexp {
	const nameEnd = `([^A-Za-z0-9._-]|$)`

	var patterns []string

	if home, err := os.UserHomeDir(); err == nil && home != "/" {
		patterns = append(patterns, regexp.QuoteMeta(home)+nameEnd)
	}

	if u, err := user.Current(); err == nil && u.Username != "" {
		patterns = append(patterns, `/(home|Users)/`+regexp.QuoteMeta(u.Username)+nameEnd)
	}

	if host, err := os.Hostname(); err == nil && host != "" {
		patterns = append(patterns, `://`+regexp.QuoteMeta(host)+nameEnd)
	}

	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		compiled = append(compiled, regexp.MustCompile(p))
	}

	return compiled
}

// render gives the same phenix pages on each run, with no path or URL of this host or user in
// them, links only to generated pages and headings, and the documented store.endpoint default.
//
//nolint:paralleltest // renders the shared phenix command tree
func TestRenderPhenixTree(t *testing.T) {
	first := renderPages(t, cmd.Root())

	if diff := cmp.Diff(first, renderPages(t, cmd.Root())); diff != "" {
		t.Fatalf("render() differs between two runs (-first +second):\n%s", diff)
	}

	leaks := hostSpecific()

	for file, p := range first {
		for _, leak := range leaks {
			if s := leak.FindString(p.Body); s != "" {
				t.Errorf("render() %s contains %q, which depends on the host or user", file, s)
			}
		}

		for _, m := range markdownLink.FindAllStringSubmatch(p.Body, -1) {
			linked, ok := first[m[1]]
			if !ok {
				t.Errorf("render() %s links to %s, which is not a generated page", file, m[1])

				continue
			}

			if m[2] != "" && !hasHeadingSlug(linked.Body, m[2]) {
				t.Errorf("render() %s links to %s#%s, which has no such heading", file, m[1], m[2])
			}
		}
	}

	if !strings.Contains(first[indexFile].Body, "/etc/phenix/store.bdb as root") {
		t.Errorf("render() index.md does not show the documented store.endpoint default:\n%s", first[indexFile].Body)
	}
}

// hasHeadingSlug reports whether markdown has a heading, outside code blocks, whose anchor is anchor.
func hasHeadingSlug(markdown, anchor string) bool {
	inFence := false

	for line := range strings.SplitSeq(markdown, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}

		if heading, ok := strings.CutPrefix(strings.TrimLeft(line, "#"), " "); !inFence && ok &&
			strings.HasPrefix(line, "#") && slug(heading) == anchor {
			return true
		}
	}

	return false
}

func TestSlug(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"phenix experiment create", "phenix-experiment-create"},
		{"phenix vm capture start-subnet", "phenix-vm-capture-start-subnet"},
		{"  Options  ", "options"},
		{"a - b", "a-b"},
		{"phenix (beta)!", "phenix-beta"},
		{"", ""},
		{"under_score", "under_score"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := slug(tc.in); got != tc.want {
				t.Errorf("slug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSlugProperties(t *testing.T) {
	t.Parallel()

	anchor := regexp.MustCompile(`^[a-z0-9_-]*$`)

	rapid.Check(t, func(t *rapid.T) {
		heading := rapid.String().Draw(t, "heading")
		got := slug(heading)

		if !anchor.MatchString(got) || strings.Contains(got, "--") {
			t.Fatalf("slug(%q) = %q, want only [a-z0-9_-] without a repeated -", heading, got)
		}

		if again := slug(got); again != got {
			t.Fatalf("slug(slug(%q)) = %q, want %q", heading, again, got)
		}
	})
}

func TestDedent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"\n  a\n    b\n\n  c  \n\n", "a\n  b\n\nc"},
		{"first\n  second", "first\n  second"},
		{"\t\tx\n\ty", "\tx\ny"},
		{"   \n  only\n", "only"},
		{"\n\n", ""},
		{"  \ta\n  b", "\ta\nb"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := dedent(tc.in); got != tc.want {
				t.Errorf("dedent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDedentProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		lines := rapid.SliceOf(rapid.SampledFrom([]string{"", " ", "a", "  b", "\tc", " d e "})).Draw(t, "lines")
		indent := rapid.SampledFrom([]string{"", " ", "  ", "\t"}).Draw(t, "indent")

		text := dedent(strings.Join(lines, "\n"))

		indented := strings.Split(text, "\n")
		for i := range indented {
			indented[i] = indent + indented[i]
		}

		if got := dedent(strings.Join(indented, "\n")); got != text {
			t.Fatalf("dedent of %q indented by %q = %q, want %q", text, indent, got, text)
		}

		if first, _, _ := strings.Cut(text, "\n"); strings.TrimRight(text, " \t\n") != text ||
			text != "" && strings.TrimSpace(first) == "" {
			t.Fatalf("dedent(%q) = %q, want no blank line first and nothing blank at the end", strings.Join(lines, "\n"), text)
		}
	})
}

func TestTidy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, in, want string }{
		{"labels unlabeled code blocks", "```\nx\n```\n", "```text\nx\n```\n"},
		{"keeps labeled code blocks", "```bash\nx\n```\n", "```bash\nx\n```\n"},
		{"collapses blank lines", "a\n\n\n\nb\n\n\n", "a\n\nb\n"},
		{"keeps blank lines in code", "```\na\n\n\nb\n```", "```text\na\n\n\nb\n```\n"},
		{"drops link list tabs", "* [a](a.md)\t - A\n", "* [a](a.md) - A\n"},
		{"drops trailing spaces", "a  \n```\nb\t\n```\n", "a\n```text\nb\n```\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tidy(tc.in); got != tc.want {
				t.Errorf("tidy(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTidyIsIdempotent(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		lines := rapid.SliceOf(rapid.SampledFrom([]string{
			"", "```", "```go", "text", "* [a](b.md)\t - c", "  x  ", "\t",
		})).Draw(t, "lines")

		once := tidy(strings.Join(lines, "\n"))
		if twice := tidy(once); twice != once {
			t.Fatalf("tidy(tidy(x)) = %q, want tidy(x) = %q", twice, once)
		}
	})
}

// renderFailingWriter fails every write.
type renderFailingWriter struct{}

func (renderFailingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestGenMarkdownReportsWriteErrors(t *testing.T) {
	t.Parallel()

	c := &cobra.Command{Use: "app", Short: "An app"}

	err := genMarkdown(c, renderFailingWriter{}, func(s string) string { return s })
	if err == nil || !strings.Contains(err.Error(), "rendering app") {
		t.Errorf("genMarkdown(app, a writer that fails) = %v, want an error naming app", err)
	}

	if !c.DisableAutoGenTag {
		t.Error("genMarkdown(app) DisableAutoGenTag = false, want true")
	}
}

// Each command is documented on its top-level command's page, under its own
// heading below the top, and cobra links it by its path.
func TestRenderTargets(t *testing.T) {
	t.Parallel()

	root := newRenderTestTree()

	for _, tc := range []struct {
		path         []string
		target, link string
	}{
		{nil, indexFile, "app.md"},
		{[]string{"alpha"}, "alpha.md", "app_alpha.md"},
		{[]string{"alpha", "beta"}, "alpha.md#app-alpha-beta", "app_alpha_beta.md"},
		{[]string{"zeta"}, "zeta.md", "app_zeta.md"},
	} {
		c, _, err := root.Find(tc.path)
		if err != nil {
			t.Fatalf("Find(%v) error = %v", tc.path, err)
		}

		if got := target(c); got != tc.target {
			t.Errorf("target(%s) = %q, want %q", c.CommandPath(), got, tc.target)
		}

		if got := cobraLink(c); got != tc.link {
			t.Errorf("cobraLink(%s) = %q, want %q", c.CommandPath(), got, tc.link)
		}
	}
}

// visit walks the documented commands depth first, by name, leaving out
// hidden commands and help topics.
func TestVisitOrder(t *testing.T) {
	t.Parallel()

	root := newRenderTestTree()
	root.AddCommand(&cobra.Command{Use: "topic", Short: "A help topic"})

	var got []string

	visit(root, func(c *cobra.Command) { got = append(got, c.CommandPath()) })

	want := []string{"app", "app alpha", "app alpha beta", "app zeta"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("visit(app) order mismatch (-want +got):\n%s", diff)
	}

	if children := available(&cobra.Command{Use: "leaf"}); len(children) != 0 {
		t.Errorf("available(leaf) = %d commands, want none", len(children))
	}
}

// hideFlags hides every flag, and its restore keeps a flag hidden before
// hidden.
func TestHideFlags(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("app", pflag.ContinueOnError)
	fs.String("shown", "", "")
	fs.String("hidden", "", "")
	_ = fs.MarkHidden("hidden")

	hidden := func() map[string]bool {
		got := map[string]bool{}
		fs.VisitAll(func(f *pflag.Flag) { got[f.Name] = f.Hidden })

		return got
	}

	restore := hideFlags(fs)

	if diff := cmp.Diff(map[string]bool{"shown": true, "hidden": true}, hidden()); diff != "" {
		t.Errorf("hideFlags() hidden flags mismatch (-want +got):\n%s", diff)
	}

	restore()

	if diff := cmp.Diff(map[string]bool{"shown": false, "hidden": true}, hidden()); diff != "" {
		t.Errorf("after hideFlags() restore, hidden flags mismatch (-want +got):\n%s", diff)
	}
}

// useDocDefaults uses an annotated flag's documented default, and leaves a
// flag whose annotation is empty as it is.
func TestUseDocDefaults(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "app"}
	root.Flags().String("store", "/home/u/store.db", "store path")
	root.Flags().String("empty", "x", "no default given")
	_ = root.Flags().SetAnnotation("store", cmd.DocDefaultAnnotation, []string{"/etc/store.db"})
	_ = root.Flags().SetAnnotation("empty", cmd.DocDefaultAnnotation, []string{})

	type shown struct{ DefValue, Usage string }

	flags := func() map[string]shown {
		got := map[string]shown{}
		root.Flags().VisitAll(func(f *pflag.Flag) { got[f.Name] = shown{f.DefValue, f.Usage} })

		return got
	}

	original := flags()
	restore := useDocDefaults(root)

	want := map[string]shown{
		"store": {"", "store path (default: /etc/store.db)"},
		"empty": {"x", "no default given"},
	}
	if diff := cmp.Diff(want, flags()); diff != "" {
		t.Errorf("useDocDefaults() flags mismatch (-want +got):\n%s", diff)
	}

	restore()

	if diff := cmp.Diff(original, flags()); diff != "" {
		t.Errorf("after useDocDefaults() restore, flags mismatch (-want +got):\n%s", diff)
	}
}

// useTextBlocks fences a synopsis with more backticks than it holds, and
// leaves a command without one as it is.
func TestUseTextBlocks(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "app", Long: "  Run:\n  ```\n  app\n  ```\n"}
	plain := &cobra.Command{Use: "plain", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(plain)

	restore := useTextBlocks(root)

	if want := "````text\nRun:\n```\napp\n```\n````"; root.Long != want {
		t.Errorf("useTextBlocks() app.Long = %q, want %q", root.Long, want)
	}

	if plain.Long != "" {
		t.Errorf("useTextBlocks() plain.Long = %q, want empty", plain.Long)
	}

	restore()

	if want := "  Run:\n  ```\n  app\n  ```\n"; root.Long != want {
		t.Errorf("after useTextBlocks() restore, app.Long = %q, want %q", root.Long, want)
	}
}
