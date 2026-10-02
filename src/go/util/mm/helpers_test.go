package mm

import (
	"reflect"
	"regexp"
	"runtime"
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

// joinedRun matches, in a [runtime.Stack] dump, a goroutine blocked in
// flightGroup.do waiting for the result of another caller's run. A caller that
// joins a run sends no minimega command, so the dump is the only place to see
// it. This depends on the dump's text format, which the runtime does not
// promise to keep: a "goroutine N [chan receive]:" header followed by the
// blocked function's frame, named as [runtime.FuncForPC] names it.
var joinedRun = regexp.MustCompile(`(?m)^goroutine \d+ \[chan receive[^\]]*\]:\n` +
	regexp.QuoteMeta(runtime.FuncForPC(reflect.ValueOf((*flightGroup[struct{}]).do).Pointer()).Name()) + `\(`)

// waitForJoined waits until n callers, in any flightGroup, are waiting for the
// result of another caller's run, failing the test after a few seconds. It
// counts every goroutine in the process, so tests using it must not run in
// parallel with others that join runs.
func waitForJoined(t *testing.T, n int) {
	t.Helper()

	var (
		buf      = make([]byte, 64<<10)
		deadline = time.Now().Add(5 * time.Second)
	)

	for {
		size := runtime.Stack(buf, true)
		if size == len(buf) {
			buf = make([]byte, 2*len(buf))

			continue
		}

		got := len(joinedRun.FindAll(buf[:size], -1))
		if got == n {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%d callers joined a run, want %d", got, n)
		}

		time.Sleep(time.Millisecond)
	}
}
