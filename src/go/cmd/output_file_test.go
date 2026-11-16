package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
	"github.com/spf13/cobra"
)

// openTestTerminal returns the terminal end of a new pseudo-terminal, which
// closes when the test ends.
func openTestTerminal(t *testing.T) *os.File {
	t.Helper()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}

	t.Cleanup(func() { _, _ = ptmx.Close(), tty.Close() })

	return tty
}

func TestIsTerminalFile(t *testing.T) {
	t.Parallel()

	regular, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("os.Create() error = %v", err)
	}

	t.Cleanup(func() { _ = regular.Close() })

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("os.OpenFile(%s) error = %v", os.DevNull, err)
	}

	t.Cleanup(func() { _ = devNull.Close() })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}

	t.Cleanup(func() { _, _ = r.Close(), w.Close() })

	for _, tt := range []struct {
		name string
		w    io.Writer
		want bool
	}{
		{name: "a buffer", w: new(bytes.Buffer), want: false},
		{name: "a regular file", w: regular, want: false},
		{name: os.DevNull, w: devNull, want: false},
		{name: "a pipe", w: w, want: false},
		{name: "a terminal", w: openTestTerminal(t), want: true},
	} {
		if got := isTerminalFile(tt.w); got != tt.want {
			t.Errorf("isTerminalFile(%s) = %t, want %t", tt.name, got, tt.want)
		}
	}
}

func TestIsTextData(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		head string
		want bool
	}{
		{name: "empty", head: "", want: true},
		{name: "plain text", head: "packets\n", want: true},
		{name: "JSON", head: `{"flows": 3}`, want: true},
		{name: "UTF-8 text", head: "phēnix\n", want: true},
		{name: "pcap", head: "\xd4\xc3\xb2\xa1\x02\x00\x04\x00", want: false},
		{name: "pcapng", head: "\x0a\x0d\x0d\x0a\x1c\x00\x00\x00", want: false},
		{name: "PNG", head: "\x89PNG\r\n\x1a\n", want: false},
		{name: "zip", head: "PK\x03\x04", want: false},
		{name: "ELF", head: "\x7fELF\x02\x01\x01\x00", want: false},
	} {
		if got := isTextData([]byte(tt.head)); got != tt.want {
			t.Errorf("isTextData(%s %q) = %t, want %t", tt.name, tt.head, got, tt.want)
		}
	}
}

// outputFileCmd returns a command whose output is w.
func outputFileCmd(w io.Writer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(w)

	return cmd
}

func TestWriteOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var stdout bytes.Buffer

	cmd := outputFileCmd(&stdout)

	write := func(w io.Writer) error {
		_, err := io.WriteString(w, "new")

		return err
	}

	if err := writeOutput(cmd, "", write); err != nil || stdout.String() != "new" {
		t.Errorf("writeOutput(\"\") = %v with stdout %q, want nil with %q", err, stdout.String(), "new")
	}

	path := filepath.Join(dir, "out.txt")
	writeDiskFile(t, path, "old")

	if err := writeOutput(cmd, path, write); err != nil {
		t.Fatalf("writeOutput(%s) error = %v", path, err)
	}

	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("after writeOutput, %s holds %q, want %q", path, b, "new")
	}

	failed := errors.New("failed")

	if err := writeOutput(cmd, path, func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")

		return failed
	}); !errors.Is(err, failed) {
		t.Errorf("writeOutput(%s) that fails error = %v, want %v", path, err, failed)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("after a failed writeOutput, the folder has %d files, want only out.txt", len(entries))
	}

	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("after a failed writeOutput, %s holds %q, want %q", path, b, "new")
	}

	if err := writeOutput(cmd, dir, write); err == nil {
		t.Errorf("writeOutput(%s), a folder, error = nil, want an error", dir)
	}
}

// writeBinaryOutput refuses only a stdout that is a terminal. With a file, it
// writes the file, whatever stdout is.
func TestWriteBinaryOutput(t *testing.T) {
	t.Parallel()

	const data = "\x89PNG"

	tests := []struct {
		name     string
		terminal bool // stdout is a terminal
		path     string
		refused  bool
	}{
		{name: "stdout", path: ""},
		{name: "stdout as -", path: "-"},
		{name: "a terminal", terminal: true, path: "", refused: true},
		{name: "a terminal as -", terminal: true, path: "-", refused: true},
		{name: "a file", path: "out.png"},
		{name: "a file with a terminal", terminal: true, path: "out.png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				buf    bytes.Buffer
				stdout io.Writer = &buf
				path             = tt.path
				called bool
			)

			if tt.terminal {
				stdout = openTestTerminal(t)
			}

			if !isStdoutPath(path) {
				path = filepath.Join(t.TempDir(), path)
			}

			err := writeBinaryOutput(outputFileCmd(stdout), path, "a PNG image", func(w io.Writer) error {
				called = true
				_, err := io.WriteString(w, data)

				return err
			})

			if tt.refused {
				if err == nil || !strings.Contains(err.Error(), "refusing to write a PNG image to a terminal") {
					t.Errorf("writeBinaryOutput(%q) error = %v, want a refusal", tt.path, err)
				}

				if called {
					t.Errorf("writeBinaryOutput(%q) wrote to the terminal, want no write", tt.path)
				}

				return
			}

			if err != nil {
				t.Fatalf("writeBinaryOutput(%q) error = %v", tt.path, err)
			}

			got := buf.String()
			if !isStdoutPath(path) {
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("os.ReadFile(%s) error = %v", path, err)
				}

				got = string(b)
			}

			if got != data {
				t.Errorf("writeBinaryOutput(%q) wrote %q, want %q", tt.path, got, data)
			}
		})
	}
}
