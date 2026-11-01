package web

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestGetLogsForbiddenWritesNoLogs(t *testing.T) {
	var (
		server = serveAs(t, "alice", testRole("experiments", "list")).URL
		start  = time.Now().Add(-time.Minute).Format(time.RFC3339)
		path   = "/api/v1/logs?start=" + url.QueryEscape(start)
	)

	resp := do(t, http.MethodGet, server+path, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET %s without logs access = %d, want %d", path, resp.StatusCode, http.StatusForbidden)
	}

	if got, want := errorMessage(t, resp.StatusCode, readBody(t, resp)), "forbidden"; got != want {
		t.Errorf("GET %s without logs access = %q, want %q", path, got, want)
	}
}
