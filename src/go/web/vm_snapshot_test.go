package web

import (
	"bytes"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// startedBy counts the goroutines that a function of the named handler started and that still run.
func startedBy(handler string) int {
	buf := make([]byte, 1<<20)

	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]

			break
		}

		buf = make([]byte, 2*len(buf))
	}

	return bytes.Count(buf, []byte("created by phenix/web."+handler+" "))
}

// A commit or memory snapshot that fails before it reports progress leaves no progress goroutine behind.
func TestVMChangesThatFailEarlyLeaveNoProgressGoroutine(t *testing.T) {
	changes := vmChanges()

	for name, handler := range map[string]string{"commit": "CommitVM", "memory snapshot": "CreateVMMemorySnapshot"} {
		t.Run(name, func(t *testing.T) {
			change := changes[name]

			// no experiment is stored, so the change fails before its first progress report
			useTestStore(t)
			useTestMM(t, new(testMM))

			if rec := serveVMChange(t.Context(), t, change); rec.Code < http.StatusBadRequest {
				t.Fatalf("%s %s of a missing experiment = %d, want an error", change.method, change.path, rec.Code)
			}

			left := startedBy(handler)
			for deadline := time.Now().Add(2 * time.Second); left != 0 && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)

				left = startedBy(handler)
			}

			if diff := cmp.Diff(0, left); diff != "" {
				t.Errorf("%s goroutines left after it returned mismatch (-want +got):\n%s", handler, diff)
			}
		})
	}
}
