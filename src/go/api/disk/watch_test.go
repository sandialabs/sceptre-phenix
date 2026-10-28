package disk

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/go-cmp/cmp"
)

// Watch follows the folders GetImages searches, including ones made after it
// starts, and not the ones the listing leaves out.
func TestWatchSeesFolders(t *testing.T) {
	t.Parallel()

	const debounce = 50 * time.Millisecond

	for name, symlinked := range map[string]bool{"files directory": false, "files directory is a symlink": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, folder := range []string{"win/10", "exp/files", ".hidden"} {
				if err := os.MkdirAll(filepath.Join(dir, folder), 0o750); err != nil {
					t.Fatalf("os.MkdirAll(%s) error = %v", folder, err)
				}
			}

			watched := dir
			if symlinked {
				watched = filepath.Join(t.TempDir(), "images")
				if err := os.Symlink(dir, watched); err != nil {
					t.Fatalf("os.Symlink(%s, %s) error = %v", dir, watched, err)
				}
			}

			changes := make(chan struct{}, 10)

			if err := Watch(t.Context(), watched, debounce, func() { changes <- struct{}{} }); err != nil {
				t.Fatalf("Watch(%s) error = %v", watched, err)
			}

			reported := func(what string) {
				t.Helper()

				select {
				case <-changes:
				case <-time.After(5 * time.Second):
					t.Fatalf("Watch() reported no change within 5s of %s", what)
				}
			}

			writeFile(t, filepath.Join(dir, "win", "10", "a.qc2"), "a")
			reported("writing an image in a folder")

			if err := os.MkdirAll(filepath.Join(dir, "new", "a", "b"), 0o750); err != nil {
				t.Fatalf("os.MkdirAll(new/a/b) error = %v", err)
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
				t.Error("Watch() reported a change in a folder the listing leaves out")
			case <-time.After(4 * debounce):
			}

			if err := os.Rename(filepath.Join(dir, "new"), filepath.Join(dir, "moved")); err != nil {
				t.Fatalf("os.Rename(new, moved) error = %v", err)
			}

			reported("renaming a folder")

			writeFile(t, filepath.Join(dir, "moved", "a", "b", "x.qc2"), "moved")
			reported("writing an image in a renamed folder")

			if err := os.RemoveAll(filepath.Join(dir, "moved")); err != nil {
				t.Fatalf("os.RemoveAll(moved) error = %v", err)
			}

			reported("removing a folder")
		})
	}
}

func TestWatchDebouncesImageChanges(t *testing.T) {
	t.Parallel()

	const debounce = 100 * time.Millisecond

	dir := t.TempDir()
	changes := make(chan struct{}, 10)

	if err := Watch(t.Context(), dir, debounce, func() { changes <- struct{}{} }); err != nil {
		t.Fatalf("Watch(%s) error = %v", dir, err)
	}

	for i := range 5 {
		writeFile(t, filepath.Join(dir, "upload.qc2"), string(rune('a'+i)))
	}

	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch() reported no change within 5s of writing an image")
	}

	writeFile(t, filepath.Join(dir, "notes.txt"), "not an image")

	// a further report would come one debounce period after the event behind
	// it; the second period leaves room for the timer and scheduling to lag
	select {
	case <-changes:
		t.Error("Watch() reported the burst of writes more than once, or a file that is not an image")
	case <-time.After(2 * debounce):
	}
}

// Watch fails for a folder it cannot watch.
func TestWatchMissingFolder(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "gone")

	err := Watch(t.Context(), dir, time.Millisecond, func() { t.Error("Watch() of a missing folder reported a change") })
	if !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "watching "+dir) {
		t.Errorf("Watch(%s) error = %v, want one naming the folder and wrapping %v", dir, err, fs.ErrNotExist)
	}
}

// watchNewTree returns a watchedTree of a new folder, which its watcher watches, closing the watcher when the test ends.
func watchNewTree(t *testing.T) *watchedTree {
	t.Helper()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks() error = %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify.NewWatcher() error = %v", err)
	}

	t.Cleanup(func() { _ = watcher.Close() })

	if err := watcher.Add(root); err != nil {
		t.Fatalf("watcher.Add(%s) error = %v", root, err)
	}

	return &watchedTree{watcher: watcher, dir: root, root: root, dirs: map[string]bool{root: true}, full: false, noSpace: false}
}

// watchFolderNames returns the watched folders of tree relative to its root, sorted.
func watchFolderNames(t *testing.T, tree *watchedTree) []string {
	t.Helper()

	names := make([]string, 0, len(tree.dirs))

	for p := range tree.dirs {
		rel, err := filepath.Rel(tree.root, p)
		if err != nil {
			t.Fatalf("filepath.Rel(%s, %s) error = %v", tree.root, p, err)
		}

		names = append(names, rel)
	}

	slices.Sort(names)

	return names
}

// changed reports the events that may change the disk list, and follows folders made, moved in and removed.
func TestWatchedTreeChanged(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		// setup makes the folders under root before the event
		setup   []string
		watched []string
		event   func(root string) fsnotify.Event
		want    bool
		// wantWatched are the watched folders after the event, relative to root
		wantWatched []string
	}{
		"an image written": {
			event: func(root string) fsnotify.Event { return fsnotify.Event{Name: root + "/a.qc2", Op: fsnotify.Write} },
			want:  true, wantWatched: []string{"."},
		},
		"another file written": {
			event:       func(root string) fsnotify.Event { return fsnotify.Event{Name: root + "/notes.txt", Op: fsnotify.Write} },
			wantWatched: []string{"."},
		},
		"an image in a folder the listing leaves out": {
			event: func(root string) fsnotify.Event {
				return fsnotify.Event{Name: root + "/exp/files/a.qc2", Op: fsnotify.Create}
			},
			wantWatched: []string{"."},
		},
		"an event outside the files directory": {
			event:       func(string) fsnotify.Event { return fsnotify.Event{Name: "a.qc2", Op: fsnotify.Write} },
			wantWatched: []string{"."},
		},
		"a folder made, holding folders": {
			setup: []string{"win/10", "win/.hidden"},
			event: func(root string) fsnotify.Event { return fsnotify.Event{Name: root + "/win", Op: fsnotify.Create} },
			want:  true, wantWatched: []string{".", "win", "win/10"},
		},
		"a folder made the listing leaves out": {
			setup:       []string{".trash"},
			event:       func(root string) fsnotify.Event { return fsnotify.Event{Name: root + "/.trash", Op: fsnotify.Create} },
			wantWatched: []string{"."},
		},
		"a watched folder removed": {
			setup: []string{"win/10", "win10"}, watched: []string{"win", "win/10", "win10"},
			event: func(root string) fsnotify.Event { return fsnotify.Event{Name: root + "/win", Op: fsnotify.Remove} },
			want:  true, wantWatched: []string{".", "win10"},
		},
		"a watched folder moved away": {
			setup: []string{"win"}, watched: []string{"win"},
			event: func(root string) fsnotify.Event { return fsnotify.Event{Name: root + "/win", Op: fsnotify.Rename} },
			want:  true, wantWatched: []string{"."},
		},
		"a file removed": {
			event: func(root string) fsnotify.Event { return fsnotify.Event{Name: root + "/a.iso", Op: fsnotify.Remove} },
			want:  true, wantWatched: []string{"."},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree := watchNewTree(t)

			for _, folder := range tt.setup {
				if err := os.MkdirAll(filepath.Join(tree.root, folder), 0o750); err != nil {
					t.Fatalf("os.MkdirAll(%s) error = %v", folder, err)
				}
			}

			for _, folder := range tt.watched {
				if !tree.watch(filepath.Join(tree.root, folder)) {
					t.Fatalf("watch(%s) = false, want true", folder)
				}
			}

			event := tt.event(tree.root)
			if got := tree.changed(event); got != tt.want {
				t.Errorf("changed(%v) = %t, want %t", event, got, tt.want)
			}

			if diff := cmp.Diff(tt.wantWatched, watchFolderNames(t, tree)); diff != "" {
				t.Errorf("watched folders after changed(%v) mismatch (-want +got):\n%s", event, diff)
			}
		})
	}
}

// watch adds a folder it can watch, goes on past one it cannot, and stops once it watches maxWatchedDirs folders.
func TestWatchedTreeWatch(t *testing.T) {
	t.Parallel()

	tree := watchNewTree(t)
	folder := filepath.Join(tree.root, "win")

	if err := os.Mkdir(folder, 0o750); err != nil {
		t.Fatalf("os.Mkdir(win) error = %v", err)
	}

	if !tree.watch(filepath.Join(tree.root, "gone")) || tree.dirs[filepath.Join(tree.root, "gone")] {
		t.Errorf("watch(missing folder) = false or watched it, want true and not watched")
	}

	if !tree.watch(folder) || !tree.dirs[folder] {
		t.Errorf("watch(win) = false or did not watch it, want true and watched")
	}

	for i := len(tree.dirs); i < maxWatchedDirs; i++ {
		tree.dirs[fmt.Sprintf("/f/%d", i)] = true
	}

	other := filepath.Join(tree.root, "other")
	for range 2 {
		if tree.watch(other) || tree.dirs[other] || !tree.full {
			t.Errorf("watch(other) of a full tree = true or watched it, full = %t; want false, not watched, full", tree.full)
		}
	}
}

// watchFolders watches the folders under a folder that the listing searches, and stops once the tree is full.
func TestWatchedTreeWatchFolders(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		folder string
		// room is how many more folders the tree may watch, or 0 for no limit
		room int
		want []string
	}{
		"the files directory": {folder: ".", want: []string{".", "a", "a/b", "exp", "z"}},
		"a folder in it":      {folder: "a", want: []string{".", "a", "a/b"}},
		"a missing folder":    {folder: "gone", want: []string{"."}},
		"a full tree":         {folder: ".", room: 2, want: []string{".", "a", "a/b"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree := watchNewTree(t)

			for _, folder := range []string{"a/b", "exp/files", "exp/tmp", ".hidden", "z"} {
				if err := os.MkdirAll(filepath.Join(tree.root, folder), 0o750); err != nil {
					t.Fatalf("os.MkdirAll(%s) error = %v", folder, err)
				}
			}

			var filler []string

			if tt.room > 0 {
				for i := len(tree.dirs); i < maxWatchedDirs-tt.room; i++ {
					filler = append(filler, fmt.Sprintf("/f/%d", i))
					tree.dirs[filler[len(filler)-1]] = true
				}
			}

			tree.watchFolders(filepath.Join(tree.root, tt.folder))

			for _, p := range filler {
				delete(tree.dirs, p)
			}

			if diff := cmp.Diff(tt.want, watchFolderNames(t, tree)); diff != "" {
				t.Errorf("watchFolders(%s) watched mismatch (-want +got):\n%s", tt.folder, diff)
			}
		})
	}
}

// watchRun runs tree, reading events and errors from the channels it returns in place of its watcher's, so the test
// can send and close them. The third channel closes once run returns.
func watchRun(
	ctx context.Context, tree *watchedTree, debounce time.Duration, onChange func(),
) (chan fsnotify.Event, chan error, <-chan struct{}) {
	events, errs, done := make(chan fsnotify.Event), make(chan error), make(chan struct{})
	tree.watcher.Events, tree.watcher.Errors = events, errs

	go func() {
		defer close(done)

		tree.run(ctx, debounce, onChange)
	}()

	return events, errs, done
}

// watchWait fails the test unless ch receives or closes within 5 seconds.
func watchWait[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s within 5s", what)
	}
}

// An event that may change the list, or an error from the watcher (events may be lost), reports a change; other
// events do not. run ends when the watcher closes either channel.
func TestWatchedTreeRun(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		send   func(root string, events chan<- fsnotify.Event, errs chan<- error)
		report bool
		// closeErrors ends run by closing the errors channel, not the events channel
		closeErrors bool
	}{
		"an image written": {
			send: func(root string, events chan<- fsnotify.Event, _ chan<- error) {
				events <- fsnotify.Event{Name: root + "/a.qc2", Op: fsnotify.Write}
			},
			report: true,
		},
		"a watcher error": {
			send:   func(_ string, _ chan<- fsnotify.Event, errs chan<- error) { errs <- errors.New("queue overflow") },
			report: true, closeErrors: true,
		},
		"another file written": {
			send: func(root string, events chan<- fsnotify.Event, _ chan<- error) {
				events <- fsnotify.Event{Name: root + "/notes.txt", Op: fsnotify.Write}
			},
			closeErrors: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const debounce = 10 * time.Millisecond

			tree := watchNewTree(t)
			changes := make(chan struct{}, 10)

			events, errs, done := watchRun(t.Context(), tree, debounce, func() { changes <- struct{}{} })
			tt.send(tree.root, events, errs)

			if tt.report {
				watchWait(t, changes, "change reported after "+name)
			} else {
				select {
				case <-changes:
					t.Errorf("run reported a change after %s, want none", name)
				case <-time.After(50 * debounce):
				}
			}

			if tt.closeErrors {
				close(errs)
			} else {
				close(events)
			}

			watchWait(t, done, "end of run after its watcher closed a channel")
		})
	}
}

// run ends when its context ends, and reports no change after: not one still in its debounce
// period, and not one that comes after the context ended.
func TestWatchedTreeRunEndsWithItsContext(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cancelFirst bool
	}{
		"change before the context ends": {cancelFirst: false},
		"change after the context ends":  {cancelFirst: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// cancel must come within the debounce period of the event, even on a busy machine
			const debounce = time.Second

			tree := watchNewTree(t)
			ctx, cancel := context.WithCancel(t.Context())
			changes := make(chan struct{}, 10)

			events, _, done := watchRun(ctx, tree, debounce, func() { changes <- struct{}{} })

			if tt.cancelFirst {
				cancel()
			}

			// after cancel, run can take the event or end first
			select {
			case events <- fsnotify.Event{Name: tree.root + "/a.qc2", Op: fsnotify.Write}:
			case <-done:
			}

			cancel()

			watchWait(t, done, "end of run after its context ended")

			select {
			case <-changes:
				t.Error("run reported a change after its context ended, want none")
			case <-time.After(debounce + 200*time.Millisecond):
			}
		})
	}
}

// While onChange runs, further changes queue at most one more run.
func TestWatchedTreeRunQueuesOneMoreChange(t *testing.T) {
	t.Parallel()

	const debounce = 10 * time.Millisecond

	tree := watchNewTree(t)
	calls := make(chan struct{}, 10)
	release := make(chan struct{})

	events, _, _ := watchRun(t.Context(), tree, debounce, func() {
		calls <- struct{}{}

		<-release
	})

	write := fsnotify.Event{Name: tree.root + "/a.qc2", Op: fsnotify.Write}
	events <- write

	watchWait(t, calls, "first change reported")

	// each event starts the debounce period again; waiting well past it lets the timer fire
	for range 2 {
		events <- write

		time.Sleep(50 * debounce)
	}

	close(release)

	watchWait(t, calls, "second change reported")

	select {
	case <-calls:
		t.Error("run reported a third change, want changes during a run to queue only one more")
	case <-time.After(50 * debounce):
	}
}

// unwatch stops watching a folder and the folders under it, and no folder that only shares its name's start.
func TestWatchedTreeUnwatch(t *testing.T) {
	t.Parallel()

	tree := watchNewTree(t)

	for _, folder := range []string{"win", "win/10", "win/10/x", "win10"} {
		tree.dirs[filepath.Join(tree.root, folder)] = true
	}

	tree.unwatch(filepath.Join(tree.root, "win"))

	if diff := cmp.Diff([]string{".", "win10"}, watchFolderNames(t, tree)); diff != "" {
		t.Errorf("watched folders after unwatch(win) mismatch (-want +got):\n%s", diff)
	}
}
