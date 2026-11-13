package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// sniffLen is the most bytes at the start of some data that isTextData looks
// at.
const sniffLen = 512

// isStdoutPath reports whether the --output-file value path names stdout:
// "" or "-".
func isStdoutPath(path string) bool {
	return path == "" || path == "-"
}

// isTerminalFile reports whether w is a file that is a terminal.
func isTerminalFile(w io.Writer) bool {
	f, ok := w.(*os.File)

	return ok && term.IsTerminal(int(f.Fd())) //nolint:gosec // G115 file descriptors fit in an int
}

// refuseTerminal returns an error when w is a terminal. what names the binary
// data that the command writes, such as "a PNG image".
func refuseTerminal(w io.Writer, what string) error {
	if !isTerminalFile(w) {
		return nil
	}

	return fmt.Errorf(
		"refusing to write %s to a terminal: use --output-file, or send stdout to a file or a program", what,
	)
}

// isTextData reports whether head, the start of some data, is text and not
// binary data such as a packet capture.
func isTextData(head []byte) bool {
	return strings.HasPrefix(http.DetectContentType(head), "text/")
}

// writeOutput runs write on stdout when path is "" or "-". Otherwise it runs write on a new file in the folder of
// path, and renames that file to path once write succeeds, replacing any file there. After a failure, no file is left.
func writeOutput(cmd *cobra.Command, path string, write func(io.Writer) error) error {
	if isStdoutPath(path) {
		return write(cmd.OutOrStdout())
	}

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("%s is a folder: give the path of a file", path)
	}

	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".partial-*")
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}

	partial := f.Name()

	err = write(f)

	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("writing %s: %w", path, closeErr)
	}

	if err == nil {
		if err = os.Rename(partial, path); err != nil { //nolint:gosec // G703 the user's own output path
			err = fmt.Errorf("writing %s: %w", path, err)
		}
	}

	if err != nil {
		_ = os.Remove(partial) //nolint:gosec // G703 the file this function made

		return err
	}

	return nil
}

// writeBinaryOutput writes as writeOutput does, but refuses to write to stdout
// when stdout is a terminal. what names the binary data, such as "a PNG
// image".
func writeBinaryOutput(cmd *cobra.Command, path, what string, write func(io.Writer) error) error {
	if isStdoutPath(path) {
		if err := refuseTerminal(cmd.OutOrStdout(), what); err != nil {
			return err
		}
	}

	return writeOutput(cmd, path, write)
}
