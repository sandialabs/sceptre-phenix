package web

import (
	"context"
	"net/http"
	"time"
)

// How long a request that changes VMs gives minimega, instead of the 10-minute
// wait and 30-minute command defaults (see mmcli.RunContext and mm.Context).
//
//nolint:gochecknoglobals // tests shorten them
var (
	// one VM's power action, disc, capture, settings or port forward.
	vmChangeTimeout = 5 * time.Minute
	// several VMs at once, such as a subnet's captures or a PATCH of the VMs.
	vmsChangeTimeout = 10 * time.Minute
)

// changeContext bounds the request's context by timeout, for a handler whose minimega commands may give up when the
// client leaves, as nothing has changed until they are sent.
func changeContext(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), timeout)
}

// committedChangeContext is changeContext for a handler that must not stop half way when the client leaves, such as
// one changing several VMs: only the deadline ends it.
func committedChangeContext(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
}

// screenshotContext keeps change's deadline but not its cancellation, for the screenshot a finished change answers
// and broadcasts with: taken even once the client left, but never past the change's deadline.
func screenshotContext(change context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(change)

	if deadline, ok := change.Deadline(); ok {
		return context.WithDeadline(detached, deadline)
	}

	return context.WithCancel(detached)
}
