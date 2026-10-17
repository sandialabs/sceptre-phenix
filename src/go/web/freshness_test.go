package web

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"

	"phenix/store"
	"phenix/util/file"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/web/proto"
	"phenix/web/rbac"
)

// observingMM is the test minimega, also reporting how its VM info was read.
type observingMM struct {
	testMM

	obs freshness.Observation
}

func (m *observingMM) GetVMInfoObserved(...mm.Option) (mm.VMs, freshness.Observation) {
	m.vmListings.Add(1)

	return m.vms, m.obs
}

// getRaw gets url as alice with role, returning the response and its body.
func getRaw(t *testing.T, role rbac.Role, path string) (*http.Response, []byte) {
	t.Helper()

	resp := do(t, http.MethodGet, serveAs(t, "alice", role).URL+path, "")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading GET %s: %v", path, err)
	}

	return resp, body
}

// When minimega does not answer for a running experiment's VMs, the VM lists
// say so and list the VMs as configured, rather than as none.
func TestVMListsReportUnknownRuntime(t *testing.T) {
	useTestExperiment(t)

	at := time.Now().Add(-time.Second)
	useTestMM(t, &testMM{}) // restores minimega after the test

	mm.DefaultMM = &observingMM{ //nolint:reassign // install test double
		obs: freshness.Observation{At: at, Known: false, Err: errors.New("compute1: timed out")},
	}

	const vmsPath = "/api/v1/experiments/test-experiment/vms"

	resp, body := getRaw(t, experimentViewerRole(), vmsPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d: %s", vmsPath, resp.StatusCode, http.StatusOK, body)
	}

	var list proto.VMList
	if err := protojson.Unmarshal(body, &list); err != nil {
		t.Fatalf("GET %s = %s, not a VM list: %v", vmsPath, body, err)
	}

	if !list.GetRuntimeUnknown() {
		t.Errorf("GET %s runtime_unknown = false, want true", vmsPath)
	}

	if diff := cmp.Diff([]string{"test-vm"}, vmNames(list.GetVms())); diff != "" {
		t.Errorf("GET %s VM names mismatch (-want +got):\n%s", vmsPath, diff)
	}

	want := freshness.FormatTime(at)

	if got := resp.Header.Get("X-Phenix-Observed-At"); got != want {
		t.Errorf("GET %s X-Phenix-Observed-At = %q, want %q", vmsPath, got, want)
	}

	if got := list.GetObservedAt(); got != want {
		t.Errorf("GET %s observed_at = %q, want %q", vmsPath, got, want)
	}

	const expsPath = "/api/v1/experiments"

	resp, body = getRaw(t, experimentViewerRole(), expsPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d: %s", expsPath, resp.StatusCode, http.StatusOK, body)
	}

	var exps proto.ExperimentList
	if err := protojson.Unmarshal(body, &exps); err != nil {
		t.Fatalf("GET %s = %s, not an experiment list: %v", expsPath, body, err)
	}

	if n := len(exps.GetExperiments()); n != 1 {
		t.Fatalf("GET %s listed %d experiments, want 1: %v", expsPath, n, &exps)
	}

	exp := exps.GetExperiments()[0]

	if !exp.GetVmsUnknown() {
		t.Errorf("GET %s vms_unknown = false, want true", expsPath)
	}

	if diff := cmp.Diff([]string{"test-vm"}, vmNames(exp.GetVms())); diff != "" {
		t.Errorf("GET %s VM names mismatch (-want +got):\n%s", expsPath, diff)
	}
}

// vmNames returns each VM's name.
func vmNames(vms []*proto.VM) []string {
	names := make([]string, 0, len(vms))
	for _, vm := range vms {
		names = append(names, vm.GetName())
	}

	return names
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

// undecodableStore is the test store, also holding an experiment that cannot
// be decoded.
type undecodableStore struct {
	*testStore
}

func (s undecodableStore) List(kinds ...string) (store.Configs, error) {
	configs, _ := s.testStore.List(kinds...)

	return append(configs, store.Config{
		Version:  "phenix.sandia.gov/v0",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "broken"},
	}), nil
}

// A list missing experiments that could not be read is served, marked stale.
func TestGetExperimentsPartialIsStale(t *testing.T) {
	useTestExperiment(t)
	server := serveAs(t, "alice", experimentViewerRole()).URL

	s, _ := store.DefaultStore.(*testStore)
	store.DefaultStore = undecodableStore{testStore: s} //nolint:reassign // useTestStore restores it

	const path = "/api/v1/experiments?vms=false"

	resp := do(t, http.MethodGet, server+path, "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d: %s", path, resp.StatusCode, http.StatusOK, body)
	}

	var exps proto.ExperimentList
	if err := protojson.Unmarshal([]byte(body), &exps); err != nil {
		t.Fatalf("GET %s = %s, not an experiment list: %v", path, body, err)
	}

	names := make([]string, 0, len(exps.GetExperiments()))
	for _, exp := range exps.GetExperiments() {
		names = append(names, exp.GetName())
	}

	if diff := cmp.Diff([]string{"test-experiment"}, names); diff != "" {
		t.Errorf("GET %s experiment names mismatch (-want +got):\n%s", path, diff)
	}

	if got := resp.Header.Get(freshness.HeaderStale); got != "true" {
		t.Errorf("GET %s %s = %q, want true", path, freshness.HeaderStale, got)
	}
}

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
