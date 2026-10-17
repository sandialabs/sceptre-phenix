package freshness

import (
	"net/http"
	"time"
)

const (
	// HeaderObservedAt is when the oldest runtime data in a response was read
	// from minimega (RFC 3339, UTC). Its age is measured against the
	// response's Date header, so the client's clock does not matter.
	HeaderObservedAt = "X-Phenix-Observed-At"
	// HeaderStale is "true" when a response serves runtime data past its
	// normal bound, such as earlier data while minimega was busy.
	HeaderStale = "X-Phenix-Stale"
)

// Observation describes one read of runtime state, typically from minimega.
type Observation struct {
	// At is when the read started.
	At time.Time
	// Stale is set when the data is older than its normal bound, such as an
	// earlier read served while minimega is busy.
	Stale bool
	// Known is false when the read failed and the data is partial or only
	// what was configured.
	Known bool
	// Err is why the read failed, if it did.
	Err error
}

// Observed is a successful read that started at at.
func Observed(at time.Time) Observation {
	return Observation{At: at, Stale: false, Known: true, Err: nil}
}

// Unobserved is a response part that read nothing at runtime (a stopped
// experiment's VMs, say): it has no time and leaves the others' flags as they
// are, so combining starts from it.
func Unobserved() Observation {
	return Observation{At: time.Time{}, Stale: false, Known: true, Err: nil}
}

// Older combines two observations of parts of one response: the older At
// (ignoring a zero one), stale if either is, known only if both are, and the
// first error.
func (o Observation) Older(p Observation) Observation {
	combined := o

	if o.At.IsZero() || (!p.At.IsZero() && p.At.Before(o.At)) {
		combined.At = p.At
	}

	combined.Stale = o.Stale || p.Stale
	combined.Known = o.Known && p.Known

	if combined.Err == nil {
		combined.Err = p.Err
	}

	return combined
}

// SetObserved reports an observation in a response's headers: its time in
// X-Phenix-Observed-At, if it has one, and X-Phenix-Stale when it is stale.
// Call it before the response's header is written.
func SetObserved(w http.ResponseWriter, o Observation) {
	if !o.At.IsZero() {
		w.Header().Set(HeaderObservedAt, FormatTime(o.At))
	}

	if o.Stale {
		w.Header().Set(HeaderStale, "true")
	}
}

// FormatTime formats an observation time as headers and bodies carry it:
// RFC 3339 in UTC, with fractional seconds.
func FormatTime(at time.Time) string {
	return at.UTC().Format(time.RFC3339Nano)
}
