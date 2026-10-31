package vm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A copy that ends while a slow subscriber has not taken its progress ends cleanly, closing the
// subscription only once progress stops being sent on it.
func TestCopyEndsWhileProgressIsPending(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")

	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 64<<10), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", src, err)
	}

	for i := range 50 {
		c := newCopier(time.Millisecond)
		s := c.subscribe()
		read := make(chan struct{})

		go func() {
			defer close(read)

			for range s {
				time.Sleep(2 * time.Millisecond)
			}
		}()

		if err := c.copy(context.Background(), src, filepath.Join(dir, "dst")); err != nil {
			t.Fatalf("copier.copy(src, dst) error = %v (copy %d of 50)", err, i+1)
		}

		select {
		case <-read:
		case <-time.After(2 * time.Second):
			t.Fatalf("copier.copy(src, dst) left its subscription open (copy %d of 50)", i+1)
		}
	}
}

// A new image takes the time in place of any earlier one, and keeps a qcow2 or qcow extension, as the copy it names
// keeps the format of the image.
func TestNewDiskName(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 31, 12, 30, 45, 0, time.UTC)

	tests := map[string]struct {
		base string
		want string
	}{
		"qc2":             {base: "kali.qc2", want: "kali_20261031123045.qc2"},
		"qcow2 in folder": {base: "win/win10.qcow2", want: "win/win10_20261031123045.qcow2"},
		"qcow version 1":  {base: "old.qcow", want: "old_20261031123045.qcow"},
		"earlier time":    {base: "kali_20191117102805.qcow", want: "kali_20261031123045.qcow"},
		"raw":             {base: "kali.img", want: "kali_20261031123045.img.qc2"},
		"no extension":    {base: "kali", want: "kali_20261031123045.qc2"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := newDiskName(tt.base, now); got != tt.want {
				t.Errorf("newDiskName(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}
