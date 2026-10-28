package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/encoding/protojson"
	gproto "google.golang.org/protobuf/proto"
	"pgregory.net/rapid"

	"phenix/util/mm"
	"phenix/web/cache"
	"phenix/web/proto"
	"phenix/web/rbac"
)

// experimentViewerRole may list and get the test experiment and list its VMs.
func experimentViewerRole() rbac.Role {
	return combinedRole(
		testRole("experiments", "list", "test-experiment"),
		testRole("experiments", "get", "test-experiment"),
		testRole("vms", "list", "test-experiment/*"),
	)
}

// getAPI gets url and decodes a 200 response into msg, returning the status.
func getAPI(t *testing.T, url string, msg gproto.Message) int {
	t.Helper()

	resp := do(t, http.MethodGet, url, "")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading GET %s: %v", url, err)
	}

	if resp.StatusCode == http.StatusOK {
		if err := protojson.Unmarshal(body, msg); err != nil {
			t.Fatalf("GET %s = %s, not a %T: %v", url, body, msg, err)
		}
	}

	return resp.StatusCode
}

// Experiment endpoints list VMs as minimega reports them; vms=false counts from the topology instead,
// except for one experiment's screenshots. A starting experiment lists topology VMs without waiting on minimega.
func TestExperimentEndpointsListVMs(t *testing.T) {
	running := mm.VMs{{Name: "test-vm", Running: true, State: "RUNNING", Networks: []string{"EXP_1 (101)"}}}

	for _, tt := range []struct {
		name     string
		list     bool
		query    string
		vms      mm.VMs // as minimega reports them
		starting bool

		wantListings int32
		wantVMs      int
		wantState    string // of the VM listed, if one is
		wantCount    uint32
		wantPercent  float64
	}{
		{name: "experiment", vms: running, wantListings: 1, wantVMs: 1, wantState: "RUNNING", wantCount: 1},
		{name: "experiment without VMs", query: "?vms=false", vms: running, wantCount: 1},
		// minimega reports no VMs, so there are none to take screenshots of
		{name: "experiment screenshots", query: "?vms=false&screenshot=200", wantListings: 1},
		{name: "list", list: true, vms: running, wantListings: 1, wantVMs: 1, wantState: "RUNNING", wantCount: 1},
		{name: "list without VMs", list: true, query: "?vms=false", vms: running, wantCount: 1},
		// the list takes no screenshots, so one asked for changes nothing
		{name: "list ignores screenshots", list: true, query: "?vms=false&screenshot=200", vms: running, wantCount: 1},
		// the VM as the topology configures it, with no state from minimega
		{
			name: "list while starting", list: true, vms: running, starting: true,
			wantVMs: 1, wantCount: 1, wantPercent: 0.25,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useTestExperiment(t)

			fake := useTestMM(t, &testMM{vms: tt.vms})

			if tt.starting {
				if err := cache.LockExperimentForStarting("test-experiment"); err != nil {
					t.Fatalf("cache.LockExperimentForStarting(test-experiment) error = %v", err)
				}

				startProgress.Store("test-experiment", tt.wantPercent)

				t.Cleanup(func() {
					startProgress.Delete("test-experiment")
					cache.UnlockExperiment("test-experiment")
				})
			}

			var (
				server = serveAs(t, "alice", experimentViewerRole()).URL
				exp    = new(proto.Experiment)
				path   = "/api/v1/experiments/test-experiment" + tt.query
			)

			if tt.list {
				var list proto.ExperimentList

				path = "/api/v1/experiments" + tt.query
				if code := getAPI(t, server+path, &list); code != http.StatusOK {
					t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
				}

				if n := len(list.GetExperiments()); n != 1 {
					t.Fatalf("GET %s listed %d experiments, want 1: %v", path, n, list.GetExperiments())
				}

				exp = list.GetExperiments()[0]
			} else if code := getAPI(t, server+path, exp); code != http.StatusOK {
				t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
			}

			if n := fake.vmListings.Load(); n != tt.wantListings {
				t.Errorf("GET %s listed minimega's VMs %d times, want %d", path, n, tt.wantListings)
			}

			if !exp.GetRunning() {
				t.Errorf("GET %s running = false, want true", path)
			}

			if got := exp.GetVmCount(); got != tt.wantCount {
				t.Errorf("GET %s VM count = %d, want %d", path, got, tt.wantCount)
			}

			if got := len(exp.GetVms()); got != tt.wantVMs {
				t.Errorf("GET %s listed %d VMs, want %d", path, got, tt.wantVMs)
			} else if tt.wantVMs == 1 {
				if got := exp.GetVms()[0].GetState(); got != tt.wantState {
					t.Errorf("GET %s VM state = %q, want %q", path, got, tt.wantState)
				}
			}

			if got := exp.GetPercent(); got != tt.wantPercent {
				t.Errorf("GET %s percent = %v, want %v", path, got, tt.wantPercent)
			}
		})
	}
}

// Both VM lists page alike: the count covers every VM, and an invalid page is a 400.
func TestVMListPaging(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{vms: mm.VMs{{Name: "test-vm", Running: true, State: "RUNNING"}}})

	var (
		server = serveAs(t, "alice", experimentViewerRole()).URL
		exp    proto.Experiment
		list   proto.VMList
	)

	for _, tt := range []struct {
		name string
		path string
		msg  gproto.Message
		page func() (vms, total int)
	}{
		{
			name: "experiment",
			path: "/api/v1/experiments/test-experiment",
			msg:  &exp,
			page: func() (int, int) { return len(exp.GetVms()), int(exp.GetVmCount()) },
		},
		{
			name: "VM list",
			path: "/api/v1/experiments/test-experiment/vms",
			msg:  &list,
			page: func() (int, int) { return len(list.GetVms()), int(list.GetTotal()) },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			second := tt.path + "?pageNum=2&perPage=1"
			if code := getAPI(t, server+second, tt.msg); code != http.StatusOK {
				t.Fatalf("GET %s = %d, want %d", second, code, http.StatusOK)
			}

			vms, total := tt.page()
			if vms != 0 {
				t.Errorf("GET %s listed %d VMs, want 0", second, vms)
			}

			if total != 1 {
				t.Errorf("GET %s total = %d, want 1", second, total)
			}

			invalid := tt.path + "?pageNum=0&perPage=10"
			if code := getAPI(t, server+invalid, tt.msg); code != http.StatusBadRequest {
				t.Errorf("GET %s = %d, want %d", invalid, code, http.StatusBadRequest)
			}
		})
	}
}

// The experiment's VMs filter by label, which must be key=value, and the VM
// list offers the labels to filter by.
func TestVMListLabels(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{vms: mm.VMs{{
		Name: "test-vm", Running: true, State: "RUNNING",
		Tags: map[string]string{"role": "web", "__notes_1": "a note"},
	}}})

	server := serveAs(t, "alice", experimentViewerRole()).URL

	for _, tt := range []struct {
		query string
		code  int
		vms   int
	}{
		{"?label=role=web", http.StatusOK, 1},
		{"?label=role=db&label=role=web", http.StatusOK, 1},
		{"?label=role=db", http.StatusOK, 0},
		{"?label=__notes_1=a+note", http.StatusOK, 0},
		{"?label=role", http.StatusBadRequest, 0},
	} {
		var exp proto.Experiment

		path := "/api/v1/experiments/test-experiment" + tt.query
		if code := getAPI(t, server+path, &exp); code != tt.code {
			t.Errorf("GET %s = %d, want %d", path, code, tt.code)

			continue
		}

		if got := len(exp.GetVms()); got != tt.vms {
			t.Errorf("GET %s listed %d VMs, want %d", path, got, tt.vms)
		}
	}

	var list proto.VMList

	path := "/api/v1/experiments/test-experiment/vms"
	if code := getAPI(t, server+path, &list); code != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
	}

	got := make([]string, 0, len(list.GetLabels()))
	for _, l := range list.GetLabels() {
		got = append(got, l.GetKey()+"="+l.GetValue())
	}

	if diff := cmp.Diff([]string{"role=web"}, got); diff != "" {
		t.Errorf("GET %s labels mismatch (-want +got):\n%s", path, diff)
	}
}

// The experiment's VMs filter by the cluster host running them, which must not
// be empty, and the VM list offers the hosts to filter by.
func TestVMListHosts(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{vms: mm.VMs{{Name: "test-vm", Host: "node1", Running: true, State: "RUNNING"}}})

	server := serveAs(t, "alice", experimentViewerRole()).URL

	for _, tt := range []struct {
		query string
		code  int
		vms   int
	}{
		{"?host=node1", http.StatusOK, 1},
		{"?host=node2&host=node1", http.StatusOK, 1},
		{"?host=node2", http.StatusOK, 0},
		{"?host=", http.StatusBadRequest, 0},
	} {
		var exp proto.Experiment

		path := "/api/v1/experiments/test-experiment" + tt.query
		if code := getAPI(t, server+path, &exp); code != tt.code {
			t.Errorf("GET %s = %d, want %d", path, code, tt.code)

			continue
		}

		if got := len(exp.GetVms()); got != tt.vms {
			t.Errorf("GET %s listed %d VMs, want %d", path, got, tt.vms)
		}
	}

	var list proto.VMList

	path := "/api/v1/experiments/test-experiment/vms"
	if code := getAPI(t, server+path, &list); code != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
	}

	if diff := cmp.Diff([]string{"node1"}, list.GetHosts()); diff != "" {
		t.Errorf("GET %s hosts mismatch (-want +got):\n%s", path, diff)
	}

	if got := list.GetRunning(); got != 1 {
		t.Errorf("GET %s running = %d, want 1", path, got)
	}
}

// The VM list names the VMs that still wait for a delayed start, from every
// page, and not the delayed VMs that minimega already started.
func TestVMListDelayed(t *testing.T) {
	delayed := func(hostname string) map[string]any {
		node := stoppedVMNode(hostname)
		node["delay"] = map[string]any{"user": true}

		return node
	}

	useTestStore(t, testExperiment(t, "2024-01-01T00:00:00Z",
		delayed("waiting"), delayed("started"), stoppedVMNode("plain")))
	useTestMM(t, &testMM{vms: mm.VMs{
		{Name: "waiting", State: "BUILDING"},
		{Name: "started", Running: true, State: "RUNNING"},
		{Name: "plain", Running: true, State: "RUNNING"},
	}})

	server := serveAs(t, "alice", experimentViewerRole()).URL

	var list proto.VMList

	path := "/api/v1/experiments/test-experiment/vms?pageNum=1&perPage=1&sortCol=name&sortDir=asc"
	if code := getAPI(t, server+path, &list); code != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
	}

	if diff := cmp.Diff([]string{"waiting"}, list.GetDelayed()); diff != "" {
		t.Errorf("GET %s delayed mismatch (-want +got):\n%s", path, diff)
	}
}

// stoppedVMNode is a VM node of the topology of a stopped test experiment.
func stoppedVMNode(hostname string) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": hostname, "do_not_boot": false},
		"hardware": map[string]any{"os_type": "linux", "drives": []map[string]any{{"image": "test.qc2"}}},
	}
}

// patchedVM is what a bulk update answers for one VM it updated.
type patchedVM struct {
	Name      string
	DoNotBoot bool
}

// patchedItem is what a bulk update answers for one VM it left unchanged.
type patchedItem struct {
	Name, Status, Error string
}

// patchVMs sends a bulk update of the stopped experiment's VMs as a role with
// policy, and returns its status, the VMs it updated and those it did not.
func patchVMs(t *testing.T, policy rbac.Role, body string) (int, []patchedVM, []patchedItem) {
	t.Helper()

	server := serveAs(t, "alice", policy)

	resp := do(t, http.MethodPatch, server.URL+"/api/v1/experiments/stopped/vms", body)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading PATCH /api/v1/experiments/stopped/vms: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		errorMessage(t, resp.StatusCode, string(raw))

		return resp.StatusCode, nil, nil
	}

	var list proto.VMList
	if err := protojson.Unmarshal(raw, &list); err != nil {
		t.Fatalf("PATCH /api/v1/experiments/stopped/vms = %s, not a VM list: %v", raw, err)
	}

	if got, want := int(list.GetTotal()), len(list.GetVms()); got != want {
		t.Errorf("PATCH /api/v1/experiments/stopped/vms %s total = %d, want %d", body, got, want)
	}

	vms := make([]patchedVM, 0, len(list.GetVms()))
	for _, vm := range list.GetVms() {
		vms = append(vms, patchedVM{Name: vm.GetName(), DoNotBoot: vm.GetDoNotBoot()})
	}

	items := make([]patchedItem, 0, len(list.GetErrors()))
	for _, e := range list.GetErrors() {
		items = append(items, patchedItem{Name: e.GetName(), Status: e.GetStatus(), Error: e.GetError()})
	}

	return resp.StatusCode, vms, items
}

// A bulk update answers with the VMs it updated and lists those the role may not patch as skipped and those it
// could not update as failed, carrying on; it answers 403 if the role may patch none, 500 if every VM failed.
func TestUpdateVMsPerItem(t *testing.T) {
	const (
		missing = "unable to find VM vm-x in experiment stopped"
		denied  = "you do not have permission"
	)

	var (
		onlyA = testRole("vms", "patch", "stopped/vm-a")
		all   = testRole("vms", "patch", "stopped/*")
	)

	for name, tc := range map[string]struct {
		role   rbac.Role
		body   string
		status int
		vms    []patchedVM
		items  []patchedItem
	}{
		"one skipped": {
			role:   onlyA,
			body:   `{"total": 5, "vms": [{"name": "vm-b", "dnb": true}, {"name": "vm-a", "dnb": true}]}`,
			status: http.StatusOK,
			vms:    []patchedVM{{Name: "vm-a", DoNotBoot: true}},
			items:  []patchedItem{{Name: "vm-b", Status: "skipped", Error: denied}},
		},
		"one failed": {
			role:   all,
			body:   `{"vms": [{"name": "vm-x", "dnb": true}, {"name": "vm-a", "dnb": true}]}`,
			status: http.StatusOK,
			vms:    []patchedVM{{Name: "vm-a", DoNotBoot: true}},
			items:  []patchedItem{{Name: "vm-x", Status: "failed", Error: missing}},
		},
		"none updated": {
			role:   all,
			body:   `{"vms": [{"name": "vm-x", "dnb": true}]}`,
			status: http.StatusInternalServerError,
		},
		"none allowed": {
			role:   onlyA,
			body:   `{"vms": [{"name": "vm-b", "dnb": true}]}`,
			status: http.StatusForbidden,
		},
		"none named": {
			role:   onlyA,
			body:   `{"vms": []}`,
			status: http.StatusOK,
			vms:    []patchedVM{},
			items:  []patchedItem{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "stopped", "", stoppedVMNode("vm-a"), stoppedVMNode("vm-b")))

			status, vms, items := patchVMs(t, tc.role, tc.body)
			if status != tc.status {
				t.Fatalf("PATCH /api/v1/experiments/stopped/vms %s = %d, want %d", tc.body, status, tc.status)
			}

			if diff := cmp.Diff(tc.vms, vms); diff != "" {
				t.Errorf("PATCH /api/v1/experiments/stopped/vms %s VMs mismatch (-want +got):\n%s", tc.body, diff)
			}

			if diff := cmp.Diff(tc.items, items); diff != "" {
				t.Errorf("PATCH /api/v1/experiments/stopped/vms %s errors mismatch (-want +got):\n%s", tc.body, diff)
			}
		})
	}
}

// bulkSavedVM is what a bulk update saved of one VM of the stopped experiment.
type bulkSavedVM struct {
	Partition   int
	Labels      map[string]string
	Annotations map[string]any
}

// savedBulkVM reads back what the store holds of the VM name of the stopped experiment.
func savedBulkVM(t *testing.T, name string) bulkSavedVM {
	t.Helper()

	node := storedExperiment(t, "stopped").Spec.Topology().FindNodeByName(name)
	if node == nil {
		t.Fatalf("stored experiment stopped has no VM %s", name)
	}

	saved := bulkSavedVM{Labels: node.Labels(), Annotations: node.Annotations()}
	if p := node.Hardware().Drives()[0].InjectPartition(); p != nil {
		saved.Partition = *p
	}

	return saved
}

// labeledVMNode is a VM node of the stopped experiment with the label role=edge and the annotation note=old.
func labeledVMNode(hostname string) map[string]any {
	node := stoppedVMNode(hostname)
	node["labels"] = map[string]any{"role": "edge"}
	node["annotations"] = map[string]any{"note": "old"}

	return node
}

// A bulk update changes the inject partition, the tags (set or added to the labels) and the annotations of each VM,
// as an update of one VM does.
func TestUpdateVMsChangesPartitionTagsAndAnnotations(t *testing.T) {
	var (
		labels      = map[string]string{"role": "edge"}
		annotations = map[string]any{"note": "old"}
	)

	for name, tc := range map[string]struct {
		body string
		want bulkSavedVM
	}{
		"inject partition": {
			body: `{"vms": [{"name": "vm-a", "inject_partition": 2}]}`,
			want: bulkSavedVM{Partition: 2, Labels: labels, Annotations: annotations},
		},
		"set tags": {
			body: `{"vms": [{"name": "vm-a", "tags": {"zone": "a"}, "tag_update_mode": "SET"}]}`,
			want: bulkSavedVM{Partition: 1, Labels: map[string]string{"zone": "a"}, Annotations: annotations},
		},
		"add tags": {
			body: `{"vms": [{"name": "vm-a", "tags": {"zone": "a"}, "tag_update_mode": "ADD"}]}`,
			want: bulkSavedVM{Partition: 1, Labels: map[string]string{"role": "edge", "zone": "a"}, Annotations: annotations},
		},
		"tags without a mode": {
			body: `{"vms": [{"name": "vm-a", "tags": {"zone": "a"}, "dnb": true}]}`,
			want: bulkSavedVM{Partition: 1, Labels: labels, Annotations: annotations},
		},
		"annotations": {
			body: `{"vms": [{"name": "vm-a", "annotations": {"note": "new", "n": 2}}]}`,
			want: bulkSavedVM{Partition: 1, Labels: labels, Annotations: map[string]any{"note": "new", "n": 2.0}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "stopped", "", labeledVMNode("vm-a")))

			status, vms, items := patchVMs(t, testRole("vms", "patch", "stopped/*"), tc.body)
			if status != http.StatusOK {
				t.Fatalf("PATCH /api/v1/experiments/stopped/vms %s = %d, want %d", tc.body, status, http.StatusOK)
			}

			if len(vms) != 1 || len(items) != 0 {
				t.Errorf("PATCH /api/v1/experiments/stopped/vms %s = VMs %v and errors %v, want vm-a only", tc.body, vms, items)
			}

			if diff := cmp.Diff(tc.want, savedBulkVM(t, "vm-a"), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("PATCH /api/v1/experiments/stopped/vms %s saved vm-a mismatch (-want +got):\n%s", tc.body, diff)
			}
		})
	}
}

// A bulk update skips, as not permitted, a VM whose new autotunnel annotation needs port forwards the role may not
// create, audits the refusal, and updates the other VMs.
func TestUpdateVMsAutotunnelRequiresForwards(t *testing.T) {
	const body = `{"vms": [` +
		`{"name": "vm-a", "annotations": {"phenix/startup-autotunnel": ["8080"]}},` +
		`{"name": "vm-b", "annotations": {"note": "new"}}]}`

	var (
		patch    = testRole("vms", "patch", "stopped/*")
		forwards = testRole("vms/forwards", "create", "stopped/*")
		tunneled = map[string]any{"phenix/startup-autotunnel": []any{"8080"}}
		noted    = map[string]any{"note": "new"}
	)

	for name, tc := range map[string]struct {
		role  rbac.Role
		items []patchedItem
		wantA map[string]any
	}{
		"with forward permission": {role: combinedRole(patch, forwards), items: []patchedItem{}, wantA: tunneled},
		"without forward permission": {
			role:  patch,
			items: []patchedItem{{Name: "vm-a", Status: "skipped", Error: "you do not have permission"}},
			wantA: map[string]any{"note": "old"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "stopped", "", labeledVMNode("vm-a"), labeledVMNode("vm-b")))

			events := captureAudit(t)

			status, _, items := patchVMs(t, tc.role, body)
			if status != http.StatusOK {
				t.Fatalf("PATCH /api/v1/experiments/stopped/vms = %d, want %d", status, http.StatusOK)
			}

			if diff := cmp.Diff(tc.items, items); diff != "" {
				t.Errorf("PATCH /api/v1/experiments/stopped/vms errors mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantA, savedBulkVM(t, "vm-a").Annotations); diff != "" {
				t.Errorf("PATCH /api/v1/experiments/stopped/vms saved vm-a annotations mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(noted, savedBulkVM(t, "vm-b").Annotations); diff != "" {
				t.Errorf("PATCH /api/v1/experiments/stopped/vms saved vm-b annotations mismatch (-want +got):\n%s", diff)
			}

			denied := map[string]any{
				"phenix.vm.name": "vm-a", "event.reason": "port forwards not allowed", "event.type": []string{"change", "denied"},
			}
			if got, want := countAudit(events(), "vm-update", "failure", denied), len(tc.items); got != want {
				t.Errorf("PATCH /api/v1/experiments/stopped/vms logged %d vm-update denials of vm-a for port forwards, want %d",
					got, want)
			}
		})
	}
}

// A bulk update puts each VM it names either among those to update, when the
// role may patch it, or among those skipped as not permitted, in its order.
func TestPatchableVMsProperty(t *testing.T) {
	names := []string{"vm-a", "vm-b", "vm-c", "vm-d"}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/experiments/stopped/vms", nil)

	rapid.Check(t, func(t *rapid.T) {
		var (
			permitted = rapid.SliceOfDistinct(rapid.SampledFrom(names), rapid.ID).Draw(t, "permitted")
			named     = rapid.SliceOf(rapid.SampledFrom(names)).Draw(t, "named")
			policies  = make([]rbac.Role, 0, len(permitted))
			vms       = make([]*proto.UpdateVMRequest, 0, len(named))
		)

		for _, name := range permitted {
			policies = append(policies, testRole("vms", "patch", "stopped/"+name))
		}

		for _, name := range named {
			vms = append(vms, &proto.UpdateVMRequest{Name: name})
		}

		allowed, skipped := patchableVMs(req, combinedRole(policies...), "stopped", vms)

		var (
			wantAllowed, wantSkipped []string
			gotAllowed               = make([]string, 0, len(allowed))
			gotSkipped               = make([]string, 0, len(skipped))
		)

		for _, name := range named {
			if slices.Contains(permitted, name) {
				wantAllowed = append(wantAllowed, name)
			} else {
				wantSkipped = append(wantSkipped, name)
			}
		}

		for _, vm := range allowed {
			gotAllowed = append(gotAllowed, vm.GetName())
		}

		for _, e := range skipped {
			gotSkipped = append(gotSkipped, e.GetName())

			if e.GetStatus() != "skipped" || e.GetError() != "you do not have permission" {
				t.Fatalf("patchableVMs skipped %s as %q (%s), want skipped for permission", e.GetName(), e.GetStatus(), e.GetError())
			}
		}

		if diff := cmp.Diff(wantAllowed, gotAllowed, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("patchableVMs(%v) allowed mismatch (-want +got):\n%s", named, diff)
		}

		if diff := cmp.Diff(wantSkipped, gotSkipped, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("patchableVMs(%v) skipped mismatch (-want +got):\n%s", named, diff)
		}
	})
}
