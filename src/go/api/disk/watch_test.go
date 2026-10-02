package disk

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Watch follows the folders GetImages searches, including ones made after it
// starts, and not the ones the listing leaves out.
func TestWatchSeesFolders(t *testing.T) {
	const debounce = 50 * time.Millisecond

	for name, symlinked := range map[string]bool{"files directory": false, "files directory is a symlink": true} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, folder := range []string{"win/10", "exp/files", ".hidden"} {
				if err := os.MkdirAll(filepath.Join(dir, folder), 0o750); err != nil {
					t.Fatal(err)
				}
			}

			watched := dir
			if symlinked {
				watched = filepath.Join(t.TempDir(), "images")
				if err := os.Symlink(dir, watched); err != nil {
					t.Fatal(err)
				}
			}

			changes := make(chan struct{}, 10)

			if err := Watch(t.Context(), watched, debounce, func() { changes <- struct{}{} }); err != nil {
				t.Fatal(err)
			}

			reported := func(what string) {
				t.Helper()

				select {
				case <-changes:
				case <-time.After(5 * time.Second):
					t.Fatalf("no change reported after %s", what)
				}
			}

			writeFile(t, filepath.Join(dir, "win", "10", "a.qc2"), "a")
			reported("writing an image in a folder")

			if err := os.MkdirAll(filepath.Join(dir, "new", "a", "b"), 0o750); err != nil {
				t.Fatal(err)
			}

			writeFile(t, filepath.Join(dir, "new", "a", "b", "x.qc2"), "x")
			reported("making folders holding an image")

			writeFile(t, filepath.Join(dir, "new", "a", "b", "x.qc2"), "changed")
			reported("writing an image in a folder made after the watch started")

			writeFile(t, filepath.Join(dir, "exp", "files", "s.qc2"), "s")
			writeFile(t, filepath.Join(dir, ".hidden", "h.qc2"), "h")

			// a report would come one debounce period after the write behind it;
			// the further periods leave room for the timer and scheduling to lag
			select {
			case <-changes:
				t.Fatal("reported a change in a folder the listing leaves out")
			case <-time.After(4 * debounce):
			}

			if err := os.Rename(filepath.Join(dir, "new"), filepath.Join(dir, "moved")); err != nil {
				t.Fatal(err)
			}

			reported("renaming a folder")

			writeFile(t, filepath.Join(dir, "moved", "a", "b", "x.qc2"), "moved")
			reported("writing an image in a renamed folder")

			if err := os.RemoveAll(filepath.Join(dir, "moved")); err != nil {
				t.Fatal(err)
			}

			reported("removing a folder")
		})
	}
}
