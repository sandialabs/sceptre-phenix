package mm

import (
	"testing"
	"time"

	"phenix/util/mm/mmtest"
)

// useFakeMinimega points mmcli at the fake minimega (see mmtest.Use), with
// nothing remembered about the cluster from earlier tests.
func useFakeMinimega(t *testing.T, handle mmtest.Handler) func() []mmtest.Command {
	t.Helper()

	received := mmtest.Use(t, handle)

	forgetCluster()
	t.Cleanup(forgetCluster)

	return received
}

// forgetCluster drops the headnode and disk details cached from earlier
// answers.
func forgetCluster() {
	headnode.mu.Lock()
	headnode.name = ""
	headnode.mu.Unlock()

	diskUsageMu.Lock()
	clear(diskUsageCache)
	diskUsageMu.Unlock()

	snapshotDisks.mu.Lock()
	snapshotDisks.entries = nil
	snapshotDisks.mu.Unlock()

	recentHosts.mu.Lock()
	recentHosts.hosts, recentHosts.at = nil, time.Time{}
	recentHosts.mu.Unlock()
}
