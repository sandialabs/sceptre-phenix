package mm

import "time"

// ForgetCluster drops what is reused from minimega's earlier answers (the
// headnode's name, the cluster hosts, their disk usage and the images behind
// snapshot disks), so the next calls ask minimega again.
func ForgetCluster() {
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

// VMInfoWaiting returns how many callers wait for the namespace's GetVMInfo
// run while later calls can still join it, or 0 when there is none.
func VMInfoWaiting(ns string) int {
	return vmInfoFlights.Waiting(ns + "\x00")
}
