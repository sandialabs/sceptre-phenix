package main

import (
	"net/http"
	"testing"
)

// TestWebServerBoundsHeaderReads checks that the web UI's server limits how
// long a client may take to send its request headers.
func TestWebServerBoundsHeaderReads(t *testing.T) {
	server := newWebServer(http.NotFoundHandler())

	if server.ReadHeaderTimeout != webReadHeaderTimeout {
		t.Errorf("newWebServer().ReadHeaderTimeout = %v, want %v", server.ReadHeaderTimeout, webReadHeaderTimeout)
	}
}
