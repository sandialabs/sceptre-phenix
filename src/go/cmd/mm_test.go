package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"phenix/util/common"
	"phenix/util/plog"
)

func TestMMCommandAlias(t *testing.T) {
	root := &cobra.Command{Use: "phenix"}
	root.AddCommand(newMMCmd())

	command, args, err := root.Find([]string{"minimega", "vm", "info"})
	if err != nil {
		t.Fatalf("expected alias to resolve: %v", err)
	}

	if command.Name() != "mm" {
		t.Fatalf("expected alias to resolve to %q, got %q", "mm", command.Name())
	}

	if want := []string{"vm", "info"}; !slices.Equal(args, want) {
		t.Fatalf("expected args %q, got %q", want, args)
	}
}

func TestMMCommandFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		attach    bool
		namespace string
		remaining []string
	}{
		{
			name:   "attach shorthand",
			args:   []string{"-a"},
			attach: true,
		},
		{
			name:      "namespace shorthand",
			args:      []string{"-n", "exp", "vm", "info"},
			namespace: "exp",
			remaining: []string{"vm", "info"},
		},
		{
			name:      "combined shorthands",
			args:      []string{"-a", "-n", "exp"},
			attach:    true,
			namespace: "exp",
		},
		{
			name:      "long flags",
			args:      []string{"--attach", "--namespace", "exp"},
			attach:    true,
			namespace: "exp",
		},
		{
			name:      "minimega flags after first argument pass through",
			args:      []string{"cc", "exec", "ls", "-a"},
			remaining: []string{"cc", "exec", "ls", "-a"},
		},
		{
			name:      "namespace then minimega flags",
			args:      []string{"-n", "exp", "cc", "exec", "grep", "-n", "root", "/etc/passwd"},
			namespace: "exp",
			remaining: []string{"cc", "exec", "grep", "-n", "root", "/etc/passwd"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := newMMCmd()

			if err := cmd.ParseFlags(test.args); err != nil {
				t.Fatalf("parsing flags: %v", err)
			}

			if attach := MustGetBool(cmd.Flags(), "attach"); attach != test.attach {
				t.Errorf("expected attach %t, got %t", test.attach, attach)
			}

			if namespace := MustGetString(cmd.Flags(), "namespace"); namespace != test.namespace {
				t.Errorf("expected namespace %q, got %q", test.namespace, namespace)
			}

			if remaining := cmd.Flags().Args(); !slices.Equal(remaining, test.remaining) {
				t.Errorf("expected remaining args %q, got %q", test.remaining, remaining)
			}
		})
	}
}

func TestMinimegaCommand(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		args      []string
		want      string
	}{
		{
			name: "single argument sent as-is",
			args: []string{`vm config tag color "#f00"`},
			want: `vm config tag color "#f00"`,
		},
		{
			name:      "single argument with namespace",
			namespace: "exp",
			args:      []string{"vm info"},
			want:      "namespace exp vm info",
		},
		{
			name: "multiple arguments",
			args: []string{"vm", "info"},
			want: "vm info",
		},
		{
			name:      "multiple arguments with namespace",
			namespace: "exp",
			args:      []string{"vm", "info"},
			want:      "namespace exp vm info",
		},
		{
			name: "argument with whitespace",
			args: []string{"cc", "exec", "echo hello"},
			want: `cc exec "echo hello"`,
		},
		{
			name: "empty argument",
			args: []string{"vm", "config", "tag", "key", ""},
			want: `vm config tag key ""`,
		},
		{
			name: "comment leader",
			args: []string{"vm", "config", "tag", "color", "#f00"},
			want: `vm config tag color "#f00"`,
		},
		{
			name: "quotes and backslash",
			args: []string{"cc", "exec", "it's", `a"b\c`},
			want: `cc exec "it's" "a\"b\\c"`,
		},
		{
			name: "control characters",
			args: []string{"cc", "exec", "a\tb\nc\rd"},
			want: `cc exec "a\tb\nc\rd"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := minimegaCommand(test.namespace, test.args); got != test.want {
				t.Errorf("expected %q, got %q", test.want, got)
			}
		})
	}
}

// TestMinimegaCommandParses checks that minimega's own command parser, which
// libminimega shares, reads back the arguments phenix sends unchanged.
func TestMinimegaCommandParses(t *testing.T) {
	minicli.Reset()
	t.Cleanup(minicli.Reset)

	noop := func(*minicli.Command, chan<- minicli.Responses) {}

	for _, pattern := range []string{"echo [args]...", "namespace <name> (command)", "vm info"} {
		minicli.MustRegister(&minicli.Handler{Patterns: []string{pattern}, Call: noop})
	}

	argSets := [][]string{
		{"plain", "-a", "--flag=value", "/path/to/file"},
		{""},
		{"with space", " leading", "trailing "},
		{"#f00", "a#b"},
		{"it's", `"quoted"`, `back\slash`, `a"b'c\d`},
		{"tab\there", "new\nline", "carriage\rreturn"},
		{"ünïcödé", "naïve café"},
	}

	for _, args := range argSets {
		command := minimegaCommand("", append([]string{"echo"}, args...))

		c, err := minicli.Compile(command)
		if err != nil {
			t.Errorf("compiling %q: %v", command, err)

			continue
		}

		if got := c.ListArgs["args"]; !slices.Equal(got, args) {
			t.Errorf("sent %q as %q, minimega parsed %q", args, command, got)
		}
	}

	// Quoting the whole command, as phenix used to when adding a namespace,
	// leaves minimega a single word that matches no command.
	if _, err := minicli.Compile(`namespace exp "vm info"`); err == nil {
		t.Error("expected quoted nested command to fail to compile")
	}

	for _, args := range [][]string{{"vm info"}, {"vm", "info"}} {
		command := minimegaCommand("exp", args)

		c, err := minicli.Compile(command)
		if err != nil {
			t.Errorf("compiling %q: %v", command, err)

			continue
		}

		if c.StringArgs["name"] != "exp" || c.Subcommand == nil {
			t.Errorf("expected %q to run a nested command in namespace exp", command)
		}
	}
}

func TestMinimegaCompletions(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		suggestions []string
		want        []string
		directive   cobra.ShellCompDirective
	}{
		{
			name:        "top-level commands",
			line:        "",
			suggestions: []string{" vlans ", " vm "},
			want:        []string{"vlans", "vm"},
			directive:   cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:        "partial word",
			line:        "vm in",
			suggestions: []string{"vm info ", "vm inject "},
			want:        []string{"info", "inject"},
			directive:   cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:        "next word",
			line:        "vm ",
			suggestions: []string{"vm info ", "vm start "},
			want:        []string{"info", "start"},
			directive:   cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:        "directory",
			line:        "cc send /tm",
			suggestions: []string{"cc send /tmp/"},
			want:        []string{"/tmp/"},
			directive:   cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace,
		},
		{
			name:        "directory and file",
			line:        "cc send /tm",
			suggestions: []string{"cc send /tmp/", "cc send /tmp.cfg "},
			want:        []string{"/tmp/", "/tmp.cfg"},
			directive:   cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace,
		},
		{
			name:        "unrelated suggestion",
			line:        "vm in",
			suggestions: []string{"other info "},
			directive:   cobra.ShellCompDirectiveNoFileComp,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, directive := minimegaCompletions(test.line, test.suggestions)

			if !slices.Equal(got, test.want) {
				t.Errorf("expected completions %q, got %q", test.want, got)
			}

			if directive != test.directive {
				t.Errorf("expected directive %d, got %d", test.directive, directive)
			}
		})
	}
}

func TestSuggestMinimega(t *testing.T) {
	var (
		mu    sync.Mutex
		lines []string
	)

	base := fakeMinimega(t, func(req miniclient.Request) []miniclient.Response {
		mu.Lock()
		lines = append(lines, req.Suggest)
		mu.Unlock()

		return []miniclient.Response{{Suggest: []string{req.Suggest + "info "}}}
	})

	for _, line := range []string{"", "vm "} {
		if _, err := suggestMinimega(base, line); err != nil {
			t.Fatalf("suggesting %q: %v", line, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// minimega rejects an empty suggestion request, so a blank line is sent.
	if want := []string{" ", "vm "}; !slices.Equal(lines, want) {
		t.Errorf("expected suggestion requests %q, got %q", want, lines)
	}

	if _, err := suggestMinimega(filepath.Join(base, "missing"), "vm "); err == nil {
		t.Error("expected an error when minimega is not running")
	}
}

func TestRunMinimegaCommand(t *testing.T) {
	tests := []struct {
		name      string
		responses []miniclient.Response
		output    string
		errs      []string
		logged    []string
	}{
		{
			name: "success",
			responses: []miniclient.Response{
				{Resp: minicli.Responses{{Host: "mm1"}}, Rendered: "vm table"},
			},
			output: "vm table\n",
		},
		{
			name: "minimega error",
			responses: []miniclient.Response{
				{Resp: minicli.Responses{{Host: "mm1", Error: "vm not found"}}},
			},
			errs:   []string{"mm1: vm not found"},
			logged: []string{"host=mm1", `error="vm not found"`},
		},
		{
			name: "output and errors across responses",
			responses: []miniclient.Response{
				{Resp: minicli.Responses{{Host: "mm1"}}, Rendered: "first"},
				{Resp: minicli.Responses{{Host: "mm2", Error: "bad"}, {Host: "mm3", Error: "worse"}}},
			},
			output: "first\n",
			errs:   []string{"mm2: bad", "mm3: worse"},
			logged: []string{"host=mm2 error=bad", "host=mm3 error=worse"},
		},
		{
			name:   "lost connection",
			errs:   []string{"lost connection to minimega"},
			logged: []string{"lost connection to minimega"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer

			plog.NewPhenixHandler(&logs)
			t.Cleanup(func() { plog.NewPhenixHandler(os.Stderr) })

			base := fakeMinimega(t, func(miniclient.Request) []miniclient.Response {
				return test.responses
			})

			mm, err := miniclient.Dial(base)
			if err != nil {
				t.Fatalf("dialing fake minimega: %v", err)
			}

			defer func() { _ = mm.Close() }()

			var out bytes.Buffer

			err = runMinimegaCommand(mm, "vm info", &out)

			if out.String() != test.output {
				t.Errorf("expected output %q, got %q", test.output, out.String())
			}

			if len(test.errs) == 0 {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}

				return
			}

			if err == nil {
				t.Fatal("expected an error")
			}

			for _, want := range test.errs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected error to contain %q, got %q", want, err)
				}
			}

			for _, want := range test.logged {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("expected phenix log to contain %q, got %q", want, logs.String())
				}
			}
		})
	}
}

func TestMMCommandRun(t *testing.T) {
	var (
		mu       sync.Mutex
		commands []string
	)

	base := fakeMinimega(t, func(req miniclient.Request) []miniclient.Response {
		mu.Lock()
		commands = append(commands, req.Command)
		mu.Unlock()

		if strings.Contains(req.Command, "missing") {
			return []miniclient.Response{{Resp: minicli.Responses{{Host: "mm1", Error: "vm not found"}}}}
		}

		return []miniclient.Response{{Resp: minicli.Responses{{Host: "mm1"}}, Rendered: "ok"}}
	})

	setMinimegaBase(t, base)

	run := func(args ...string) (string, error) {
		cmd := newMMCmd()

		var out bytes.Buffer

		cmd.SetArgs(args)
		cmd.SetOut(&out)
		cmd.SetErr(new(bytes.Buffer))

		err := cmd.Execute()

		return out.String(), err
	}

	out, err := run("-n", "exp", "vm info")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	if out != "ok\n" {
		t.Errorf("expected output %q, got %q", "ok\n", out)
	}

	if _, err := run("-n", "exp", "vm", "start", "missing"); err == nil {
		t.Fatal("expected a minimega error to fail the command")
	}

	mu.Lock()
	want := []string{"namespace exp vm info", "namespace exp vm start missing"}
	got := slices.Clone(commands)
	mu.Unlock()

	if !slices.Equal(got, want) {
		t.Errorf("expected commands %q, got %q", want, got)
	}

	setMinimegaBase(t, filepath.Join(base, "missing"))

	if _, err := run("vm", "info"); err == nil {
		t.Fatal("expected an error when minimega is not running")
	}
}

func TestMMCommandAttach(t *testing.T) {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		t.Skip("attach reads the terminal when stdin is one")
	}

	base := fakeMinimega(t, func(req miniclient.Request) []miniclient.Response {
		if req.Command == "disconnect me" {
			return nil
		}

		return []miniclient.Response{{Resp: minicli.Responses{{Host: "mm1"}}}}
	})

	setMinimegaBase(t, base)

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "user disconnects", input: "vm info\ndisconnect\n"},
		{name: "end of input", input: "vm info\n"},
		{name: "minimega disconnects", input: "disconnect me\n", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withStdio(t, test.input)

			cmd := newMMCmd()
			cmd.SetArgs([]string{"-a"})
			cmd.SetErr(new(bytes.Buffer))

			err := cmd.Execute()
			if test.wantErr && err == nil {
				t.Fatal("expected losing the minimega connection to fail the command")
			}

			if !test.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// fakeMinimega serves minimega's command socket protocol from a temporary
// base directory, answering each request with the responses from handle. It
// closes the connection when handle returns no responses.
func fakeMinimega(t *testing.T, handle func(miniclient.Request) []miniclient.Response) string {
	t.Helper()

	// Unix socket paths are limited to about 100 bytes, which t.TempDir can
	// exceed on macOS.
	base, err := os.MkdirTemp("", "mm") //nolint:usetesting // t.TempDir paths are too long for a socket
	if err != nil {
		t.Fatalf("creating minimega base directory: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(base) })

	listener, err := net.Listen("unix", filepath.Join(base, "minimega"))
	if err != nil {
		t.Fatalf("listening on minimega socket: %v", err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go serveFakeMinimega(conn, handle)
		}
	}()

	return base
}

func serveFakeMinimega(conn net.Conn, handle func(miniclient.Request) []miniclient.Response) {
	defer func() { _ = conn.Close() }()

	var (
		dec = json.NewDecoder(conn)
		enc = json.NewEncoder(conn)
	)

	for {
		var req miniclient.Request
		if err := dec.Decode(&req); err != nil {
			return
		}

		responses := handle(req)
		if len(responses) == 0 {
			return
		}

		for i, resp := range responses {
			resp.More = i < len(responses)-1

			if err := enc.Encode(resp); err != nil {
				return
			}
		}
	}
}

func setMinimegaBase(t *testing.T, base string) {
	t.Helper()

	orig := common.MinimegaBase
	common.MinimegaBase = base //nolint:reassign // test configuration

	t.Cleanup(func() { common.MinimegaBase = orig }) //nolint:reassign // test configuration
}

// withStdio feeds input to [os.Stdin] and discards [os.Stdout] for the rest
// of the test.
func withStdio(t *testing.T, input string) {
	t.Helper()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdin pipe: %v", err)
	}

	if _, err := stdinW.WriteString(input); err != nil {
		t.Fatalf("writing stdin: %v", err)
	}

	_ = stdinW.Close()

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}

	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, devNull //nolint:reassign // attach reads the process stdin

	t.Cleanup(func() {
		os.Stdin, os.Stdout = origStdin, origStdout //nolint:reassign // restore process stdio
		_ = stdinR.Close()
		_ = devNull.Close()
	})
}
