package web

import (
	"net/http"
	"time"
)

// readHeaderTimeout bounds how long a client may take to send a request's
// headers, so idle half-open connections do not pile up. Nothing else is
// bounded: request bodies (uploads) and responses (streams, websockets,
// downloads) may take as long as they need.
const readHeaderTimeout = 10 * time.Second

// newHTTPServer returns a server for handler on addr.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
}
