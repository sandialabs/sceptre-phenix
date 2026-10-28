package vm_test

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/vm"
	"phenix/util/file"
	"phenix/util/mm"
)

var registerCCPatterns sync.Once //nolint:gochecknoglobals // minicli keeps its handlers in package state

// ccWords returns the words that minimega gives miniccc for the cc command
// text, after minicli splits it and the minimega preprocessor expands each
// word. A NUL marks a word that minimega changed. For exec, it also drops the
// leading stdin=, stdout= and stderr= words, as minimega does.
func ccWords(verb, text string) ([]string, error) {
	registerCCPatterns.Do(func() {
		minicli.MustRegister(&minicli.Handler{
			Patterns: []string{"cc <exec,> <command>...", "cc <recv,> <file>..."},
			Call:     func(*minicli.Command, chan<- minicli.Responses) {},
		})
	})

	c, err := minicli.Compile("cc " + verb + " " + text)
	if err != nil {
		return nil, fmt.Errorf("compiling cc %s %s: %w", verb, text, err)
	}

	key := "command"
	if verb == "recv" {
		key = "file"
	}

	words := slices.Clone(c.ListArgs[key])

	for i, w := range words {
		w = os.Expand(w, func(name string) string { return "\x00env:" + name })

		if u, err := url.Parse(w); err == nil && slices.Contains([]string{"file", "http", "https", "tar"}, u.Scheme) {
			w = "\x00url:" + w
		}

		words[i] = w
	}

	if verb == "exec" {
		for len(words) > 0 {
			k, _, ok := strings.Cut(words[0], "=")
			if !ok || (k != "stdin" && k != "stdout" && k != "stderr") {
				break
			}

			words = words[1:]
		}
	}

	return words, nil
}

// The text of an accepted command reaches miniccc as exactly the words given.
// A refused command wraps ErrExecCommand.
func FuzzExecCommandText(f *testing.F) {
	for _, seed := range [][2]string{
		{"hostname", ""}, {"ls", "-la"}, {"sh", "-c 'ps aux | grep x'"}, {"echo", "$HOME"}, {"echo", "${x}"},
		{"stdout=x", "y"}, {"cat", "file:/etc/passwd"}, {"echo", `it's "x"`}, {"echo", "a\nb\tc"},
		{"", "x"}, {"echo", "a\x00b"}, {"echo", "\xff"}, {"a=b", "c"}, {"echo", "#1"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, program, arg string) {
		argv := []string{program, arg}

		text, err := vm.ExecCommandText(argv)
		if err != nil {
			if !errors.Is(err, vm.ErrExecCommand) {
				t.Fatalf("ExecCommandText(%q) error = %v, want %v", argv, err, vm.ErrExecCommand)
			}

			return
		}

		got, err := ccWords("exec", text)
		if err != nil {
			t.Fatalf("ExecCommandText(%q) = %q, which minicli refuses: %v", argv, text, err)
		}

		if diff := cmp.Diff(argv, got); diff != "" {
			t.Fatalf("ExecCommandText(%q) = %q, argv mismatch (-want +got):\n%s", argv, text, diff)
		}
	})
}

// A path that ValidateGuestPath accepts to receive reaches miniccc unchanged,
// and its received copy stays in the response folder.
func FuzzValidateGuestPath(f *testing.F) {
	for _, seed := range []string{
		"/etc/hosts", "/var/log/my file.log", "/../etc/shadow", "/a/./b", "/a/*", "relative", "/", "//",
		"/tmp/$HOME", "C:\\Users\\x", "/a\\b", "/tmp/it's", "/a//b/", "file:/x", "/a\x00b",
	} {
		f.Add(seed)
	}

	dir := filepath.Join(f.TempDir(), "exp", "miniccc_responses", "1", "uuid")

	f.Fuzz(func(t *testing.T, p string) {
		if err := vm.ValidateGuestPath(p, true); err != nil {
			if !errors.Is(err, vm.ErrGuestPath) {
				t.Fatalf("ValidateGuestPath(%q, true) error = %v, want %v", p, err, vm.ErrGuestPath)
			}

			return
		}

		received := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path.Clean(p), "/")))
		if received == dir || !file.WithinDir(dir, received) {
			t.Fatalf("ValidateGuestPath(%q, true) = nil, but its copy %s leaves %s", p, received, dir)
		}

		word, err := mm.CCExecCommand(p)
		if err != nil {
			t.Fatalf("CCExecCommand(%q) error = %v for a path that ValidateGuestPath accepts", p, err)
		}

		if got, err := ccWords("recv", word); err != nil || !slices.Equal(got, []string{p}) {
			t.Fatalf("cc recv %s gives miniccc %q, %v, want [%q]", word, got, err, p)
		}

		if err := vm.ValidateGuestPath(p, false); err != nil {
			t.Fatalf("ValidateGuestPath(%q, false) = %v, want nil for a path it accepts to receive", p, err)
		}
	})
}
