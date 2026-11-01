package web

import (
	"net/http"
	"testing"
)

// The experiment, VM and disk lists page with pageNum and perPage as the experiment VM and file lists do,
// returning everything without them, and refuse a page that is not a whole number of at least 1.
func TestListsRefuseBadPages(t *testing.T) {
	useTestExperiment(t)

	server := serveAs(t, "alice", adminRole()).URL

	for _, path := range []string{"/api/v1/experiments", "/api/v1/vms", "/api/v1/disks"} {
		for _, query := range []string{"pageNum=0&perPage=10", "pageNum=1&perPage=0", "pageNum=x&perPage=10"} {
			url := path + "?" + query
			if resp := do(t, http.MethodGet, server+url, ""); resp.StatusCode != http.StatusBadRequest {
				t.Errorf("GET %s = %d, want %d: %s", url, resp.StatusCode, http.StatusBadRequest, readBody(t, resp))
			}
		}
	}
}
