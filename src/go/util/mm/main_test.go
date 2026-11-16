package mm

import (
	"testing"

	"go.uber.org/goleak"

	"phenix/util/mm/mmtest"
)

// TestMain fails the run when a goroutine is still alive after the tests.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(mmtest.StopAfter(m),
		// the idle reaper of mmcli's process-wide read-only pool runs until exit
		goleak.IgnoreTopFunction("phenix/util/mm/mmcli.(*connPool).reap"),
	)
}
