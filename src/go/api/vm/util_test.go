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
