package disk

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"phenix/util/plog"
)

// maxWatchedDirs caps how many folders Watch watches, each of which uses one
// of the user's inotify watches.
const maxWatchedDirs = 4096

// Watch calls onChange once images under dir stop changing for the debounce
// period after being created, written, renamed or removed (an upload writes
// many times), until ctx is done. It watches dir, which may be a symlink, and
// the folders under it that GetImages searches, including ones created later,
// up to maxWatchedDirs folders. Changes past that limit, and to images outside
// dir, show on the next listing.
func Watch(ctx context.Context, dir string, debounce time.Duration, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating disk image watcher: %w", err)
	}

	root, err := filepath.EvalSymlinks(dir)
	if err == nil {
		err = watcher.Add(root)
	}

	if err != nil {
		_ = watcher.Close()

		return fmt.Errorf("watching %s: %w", dir, err)
	}

	tree := &watchedTree{
		watcher: watcher,
		dir:     dir,
		root:    root,
		dirs:    map[string]bool{root: true},
		full:    false,
		noSpace: false,
	}

	tree.watchFolders(root)

	go tree.run(ctx, debounce, onChange)

	return nil
}

// watchedTree is the folders a Watch watches. Once Watch returns, only the
// goroutine running run uses it.
type watchedTree struct {
	watcher *fsnotify.Watcher

	dir  string          // the files directory as configured
	root string          // dir with symlinks resolved, which events name
	dirs map[string]bool // watched folders, by the path events name them by

	full, noSpace bool // maxWatchedDirs, or the kernel's limit, was reached
}

func (t *watchedTree) run(ctx context.Context, debounce time.Duration, onChange func()) {
	defer func() { _ = t.watcher.Close() }()

	timer := time.NewTimer(debounce)
	timer.Stop()

	for {
		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case event, ok := <-t.watcher.Events:
			if !ok {
				return
			}

			if t.changed(event) {
				timer.Reset(debounce)
			}
		case err, ok := <-t.watcher.Errors:
			if !ok {
				return
			}

			plog.Warn(plog.TypeSystem, "watching disk images", "dir", t.dir, "err", err)

			// events may have been lost, such as when the kernel's queue overflows
			timer.Reset(debounce)
		case <-timer.C:
			onChange()
		}
	}
}

// changed keeps the watched folders up to date with event, and reports
// whether event may change the disk list.
func (t *watchedTree) changed(event fsnotify.Event) bool {
	rel, err := filepath.Rel(t.root, event.Name)
	if err != nil || rel != "." && skipped(rel) {
		return false
	}

	if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 && t.dirs[event.Name] {
		t.unwatch(event.Name)

		return true
	}

	if event.Op&fsnotify.Create != 0 {
		// a folder made, or moved in, may already hold images and folders
		if info, err := os.Lstat(event.Name); err == nil && info.IsDir() {
			t.watchFolders(event.Name)

			return true
		}
	}

	return knownImage(event.Name)
}

// watchFolders watches folder, under root, and the folders under it that
// GetImages searches.
func (t *watchedTree) watchFolders(folder string) {
	_ = filepath.WalkDir(folder, func(p string, entry fs.DirEntry, err error) error {
		// a folder that vanished or cannot be read is searched no further
		if err != nil {
			return fs.SkipDir
		}

		if !entry.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(t.root, p)

		switch {
		case err != nil, rel != "." && skipped(rel), tooDeep(t.dir, rel):
			return fs.SkipDir
		case t.dirs[p]:
			return nil
		case !t.watch(p):
			return fs.SkipAll
		}

		return nil
	})
}

// watch watches folder, returning false once no more folders can be watched.
func (t *watchedTree) watch(folder string) bool {
	if len(t.dirs) >= maxWatchedDirs {
		if !t.full {
			t.full = true

			plog.Warn(
				plog.TypeSystem,
				"disk list stops following changes in further folders; refreshing it shows them",
				"dir", t.dir,
				"maxWatchedFolders", maxWatchedDirs,
			)
		}

		return false
	}

	err := t.watcher.Add(folder)

	switch {
	case err == nil:
		t.dirs[folder] = true
	case errors.Is(err, syscall.ENOSPC):
		if !t.noSpace {
			t.noSpace = true

			plog.Warn(
				plog.TypeSystem,
				"disk list stops following changes in further folders; raise the fs.inotify.max_user_watches sysctl",
				"dir", t.dir,
				"err", err,
			)
		}

		return false
	default:
		plog.Debug(plog.TypeSystem, "not watching disk image folder", "folder", folder, "err", err)
	}

	return true
}

// unwatch stops watching folder and the folders under it.
func (t *watchedTree) unwatch(folder string) {
	prefix := folder + string(filepath.Separator)

	for p := range t.dirs {
		if p == folder || strings.HasPrefix(p, prefix) {
			// the kernel drops the watch of a removed folder itself
			_ = t.watcher.Remove(p)

			delete(t.dirs, p)
		}
	}
}
