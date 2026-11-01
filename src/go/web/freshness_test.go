package web

import (
	"errors"
	"net/http"
	"testing"

	"phenix/store"
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

// failingListStore is the test store, failing to list configs.
type failingListStore struct {
	*testStore
}

func (failingListStore) List(...string) (store.Configs, error) {
	return nil, errors.New("store unreadable")
}

// A store that cannot be read is an error, not an empty experiment list.
func TestGetExperimentsStoreFailure(t *testing.T) {
	role := testRole("experiments", "list", "*")
	server := serveAs(t, "alice", role).URL

	s, _ := store.DefaultStore.(*testStore)
	store.DefaultStore = failingListStore{testStore: s} //nolint:reassign // useTestStore restores it

	resp := do(t, http.MethodGet, server+"/api/v1/experiments", "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("GET /api/v1/experiments with an unreadable store = %d, want %d",
			resp.StatusCode, http.StatusInternalServerError)
	}
}
