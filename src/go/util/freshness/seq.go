package freshness

import (
	"sync/atomic"
	"time"
)

// HeaderSeq carries the sequence number taken as a REST request started.
const HeaderSeq = "X-Phenix-Seq"

var lastSeq atomic.Uint64 //nolint:gochecknoglobals // the process's sequence

// Seq returns the next number in a process-wide, strictly increasing
// sequence. It orders everything one phenix process tells its clients, over
// REST (X-Phenix-Seq) and websockets (the seq of each message): data tagged S
// reflects every change whose message carries a seq of S or less.
//
// It is a hybrid logical clock in microseconds, max(last+1, now), so it
// normally keeps increasing across restarts, but it is only comparable within
// one process: clients reset what they track when their websocket reconnects.
// Values stay below 2^53, so JavaScript numbers hold them exactly.
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
