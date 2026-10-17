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
//
//nolint:paralleltest // shortens the progress interval
func TestCopyEndsWhileProgressIsPending(t *testing.T) {
	saved := copyProgressInterval
	copyProgressInterval = time.Millisecond

	t.Cleanup(func() { copyProgressInterval = saved })

	dir := t.TempDir()
	src := filepath.Join(dir, "src")

	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 64<<10), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := range 50 {
		c := newCopier()
		s := c.subscribe()
		read := make(chan struct{})

		go func() {
			defer close(read)

			for range s {
				time.Sleep(2 * time.Millisecond)
			}
		}()

		if err := c.copy(context.Background(), src, filepath.Join(dir, "dst")); err != nil {
			t.Fatalf("copy %d: %v", i, err)
		}

		select {
		case <-read:
		case <-time.After(2 * time.Second):
			t.Fatalf("copy %d left its subscription open", i)
		}
	}
}
