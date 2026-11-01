package web

import (
	"net/http"
	"testing"
)

// filesPath is the route of test-experiment's files.
const filesPath = "/api/v1/experiments/test-experiment/files"

// requestFiles sends a request for test-experiment's files, under path, as a
// user who may only take the given verb on them.
func requestFiles(t *testing.T, method, path, body, verb string) *http.Response {
	t.Helper()

	server := serveAs(t, "alice", testRole("experiments/files", verb, "test-experiment")).URL

	return do(t, method, server+filesPath+path, body)
}
