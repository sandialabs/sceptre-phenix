package util

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"phenix/util/testenv"
)

// An in-place rewrite of the same size that sets the modification time back
// still changes the ETag, because the inode change time changes.
func TestFileETagChangesAfterAnInPlaceRewrite(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "0123456789")

	stat := func(path string) os.FileInfo {
		t.Helper()

		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		return info
	}

	before := stat(path)
	if fileChangeTime(before) == 0 {
		testenv.Skipf(t, "the file system gives no inode change time")
	}

	etag := FileETag(before)

	// the file system clock can be coarse: wait until it gives a later change time
	probe := filepath.Join(filepath.Dir(path), "probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for deadline := time.Now().Add(5 * time.Second); fileChangeTime(stat(probe)) <= fileChangeTime(before); {
		if time.Now().After(deadline) {
			t.Fatal("the inode change time of a new file did not move past the first one")
		}

		time.Sleep(time.Millisecond)

		if err := os.Chmod(probe, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.WriteAt([]byte("9876543210"), 0); err != nil {
		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}

	after := stat(path)
	if fileInode(after) != fileInode(before) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("os.Stat(%q) after the rewrite = inode %d, size %d, mtime %v, want %d, %d, %v", path,
			fileInode(after), after.Size(), after.ModTime(), fileInode(before), before.Size(), before.ModTime())
	}

	if got := FileETag(after); got == etag {
		t.Errorf("FileETag(%q) after an in-place rewrite with the old modification time = %s, want a new ETag", path, got)
	}
}
