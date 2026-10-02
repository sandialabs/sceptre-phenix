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
	)

	resp := do(t, http.MethodGet, server+"/api/v1/logs?start="+url.QueryEscape(start), "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	if got := readBody(t, resp); got != "forbidden\n" {
		t.Fatalf("body = %q, want only the forbidden error", got)
	}
}
