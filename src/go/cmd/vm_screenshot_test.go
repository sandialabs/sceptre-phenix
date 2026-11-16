package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"
)

// A size of 0 is the full size; 1 to 4096 pass as they are; any other size is refused.
func TestScreenshotSizeProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		size := rapid.IntRange(-10, 5000).Draw(t, "size")

		got, err := screenshotSize(size)

		switch {
		case size == 0:
			if err != nil || got != "" {
				t.Fatalf("screenshotSize(0) = %q, %v, want \"\", nil", got, err)
			}
		case size >= 1 && size <= 4096:
			if err != nil || got != strconv.Itoa(size) {
				t.Fatalf("screenshotSize(%d) = %q, %v, want %q, nil", size, got, err, strconv.Itoa(size))
			}
		default:
			if !errors.Is(err, errBadScreenshotSize) {
				t.Fatalf("screenshotSize(%d) error = %v, want %v", size, err, errBadScreenshotSize)
			}
		}
	})
}

// writeScreenshot writes to the --output-file file or to a stdout that is not a terminal. With a terminal and no
// --output-file, it makes a new file in the folder, named as the web UI names a saved screenshot.
func TestWriteScreenshot(t *testing.T) {
	t.Parallel()

	const png = "\x89PNG"

	now := time.Date(2026, 11, 16, 23, 4, 5, 0, time.FixedZone("MST", -7*60*60))

	tests := []struct {
		name     string
		terminal bool   // stdout is a terminal
		output   string // --output-file; a file name is in the folder
		calls    int
		wantOut  string
		wantErr  string
		want     []string // the files in the folder afterward
	}{
		{name: "a terminal", terminal: true, calls: 1, want: []string{"web-20261117-060405.png"}},
		{
			name: "a terminal, twice in one second", terminal: true, calls: 2,
			want: []string{"web-20261117-060405-1.png", "web-20261117-060405.png"},
		},
		{name: "a terminal as -", terminal: true, output: "-", calls: 1, wantErr: "refusing to write a PNG image to a terminal"},
		{name: "a terminal and a file", terminal: true, output: "out.png", calls: 1, want: []string{"out.png"}},
		{name: "stdout", calls: 1, wantOut: png},
		{name: "a file", output: "out.png", calls: 1, want: []string{"out.png"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				buf    bytes.Buffer
				stdout io.Writer = &buf
				dir              = t.TempDir()
				output           = tt.output
			)

			if tt.terminal {
				stdout = openTestTerminal(t)
			}

			if !isStdoutPath(output) {
				output = filepath.Join(dir, output)
			}

			for range tt.calls {
				err := writeScreenshot(outputFileCmd(stdout), output, dir, "web", now, []byte(png))
				if (err == nil) != (tt.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
					t.Fatalf("writeScreenshot() error = %v, want %q", err, tt.wantErr)
				}
			}

			if got := buf.String(); got != tt.wantOut {
				t.Errorf("writeScreenshot() wrote %q to stdout, want %q", got, tt.wantOut)
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}

			got := make([]string, 0, len(entries))

			for _, entry := range entries {
				got = append(got, entry.Name())

				if data, err := os.ReadFile(filepath.Join(dir, entry.Name())); err != nil || string(data) != png {
					t.Errorf("%s holds %q, %v, want %q", entry.Name(), data, err, png)
				}
			}

			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("files after writeScreenshot mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
