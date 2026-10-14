package web

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

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
		t.Fatal(err)
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

	resp, body := getRaw(t, experimentViewerRole(), "/api/v1/experiments/test-experiment/vms")

	var list proto.VMList
	if err := protojson.Unmarshal(body, &list); resp.StatusCode != http.StatusOK || err != nil {
		t.Fatalf("GET vms: %d, %v: %s", resp.StatusCode, err, body)
	}

	if !list.GetRuntimeUnknown() || len(list.GetVms()) != 1 || list.GetVms()[0].GetName() != "test-vm" {
		t.Errorf("VM list = %v, want test-vm listed as configured with runtime_unknown", &list)
	}

	if got := resp.Header.Get("X-Phenix-Observed-At"); got != freshness.FormatTime(at) || list.GetObservedAt() != got {
		t.Errorf("observed at header %q, body %q; want %s", got, list.GetObservedAt(), freshness.FormatTime(at))
	}

	resp, body = getRaw(t, experimentViewerRole(), "/api/v1/experiments")

	var exps proto.ExperimentList
	if err := protojson.Unmarshal(body, &exps); resp.StatusCode != http.StatusOK || err != nil {
		t.Fatalf("GET experiments: %d, %v: %s", resp.StatusCode, err, body)
	}

	if len(exps.GetExperiments()) != 1 || !exps.GetExperiments()[0].GetVmsUnknown() ||
		len(exps.GetExperiments()[0].GetVms()) != 1 {
		t.Errorf("experiments = %v, want test-experiment's VM listed with vms_unknown", &exps)
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
		t.Fatalf("GET experiments with an unreadable store: %d, want 500", resp.StatusCode)
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
		t.Fatalf("GET files when the listing fails: %d %s, want 502", resp.StatusCode, body)
	}
}
