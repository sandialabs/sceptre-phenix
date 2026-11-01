package web

import (
	"errors"
	"net/http"
	"testing"

	"phenix/util/file"
)

// failingFiles is a cluster whose files cannot be listed.
type failingFiles struct {
	testFiles
}

func (*failingFiles) GetExperimentFiles(string, string) (file.Files, error) {
	return nil, errors.New("compute1: timed out")
}

// A file listing minimega could not complete is a 502, not an empty list.
func TestGetExperimentFilesListingFailure(t *testing.T) {
	useTestFiles(t) // restores the cluster after the test

	file.DefaultClusterFiles = new(failingFiles) //nolint:reassign // install test double

	resp := requestFiles(t, http.MethodGet, "", "", "list")
	if body := readBody(t, resp); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("GET /api/v1/experiments/test-experiment/files when the listing fails = %d, want %d: %s",
			resp.StatusCode, http.StatusBadGateway, body)
	}
}
