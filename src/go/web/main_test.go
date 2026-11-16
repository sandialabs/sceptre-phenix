package web

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
		// go-cache stops a cache's janitor only when the cache is collected
		goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
		// the forward package starts its worker in init, for the process
		goleak.IgnoreTopFunction("phenix/web/forward.runForwards"),
	)
}
