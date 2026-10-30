package freshness

import (
	"sync/atomic"
	"time"
)

// HeaderSeq carries the sequence number taken as a REST request started.
const HeaderSeq = "X-Phenix-Seq"

var lastSeq atomic.Uint64 //nolint:gochecknoglobals // the process's sequence

// Seq returns the next number in a process-wide, strictly increasing sequence (X-Phenix-Seq and each websocket message's
// seq; see docs/content/api.md). It is a hybrid logical clock in microseconds, max(last+1, now), comparable only within
// one process, and stays below 2^53 so JavaScript numbers hold it exactly.
func Seq() uint64 {
	for {
		now := uint64(time.Now().UnixMicro())
		last := lastSeq.Load()
		next := max(last+1, now)

		if lastSeq.CompareAndSwap(last, next) {
			return next
		}
	}
}
