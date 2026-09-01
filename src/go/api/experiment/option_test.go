package experiment

import "testing"

func TestStopOptions(t *testing.T) {
	t.Run("deletes injection snapshots by default", func(t *testing.T) {
		opts := newStopOptions()

		if opts.keepInjectionSnapshots {
			t.Fatal("expected injection snapshots not to be kept")
		}
	})

	t.Run("keeps injection snapshots when requested", func(t *testing.T) {
		opts := newStopOptions(StopWithKeepInjectionSnapshots(true))

		if !opts.keepInjectionSnapshots {
			t.Fatal("expected injection snapshots to be kept")
		}
	})
}
