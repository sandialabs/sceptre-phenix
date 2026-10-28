package vm

import (
	"testing"
	"time"
)

// SetSaveTiming sets how often Snapshot polls a memory snapshot and how long
// it waits for one, for the rest of the test.
func SetSaveTiming(t *testing.T, poll, timeout time.Duration) {
	t.Helper()

	oldPoll, oldTimeout := savePoll, saveTimeout
	t.Cleanup(func() { savePoll, saveTimeout = oldPoll, oldTimeout })

	savePoll, saveTimeout = poll, timeout
}
