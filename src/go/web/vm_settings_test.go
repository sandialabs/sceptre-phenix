package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/web/cache"
)

// savedSettingsJSON is the saved settings of VMs a and b of the experiment
// exp: a on host node1 and b on none. c has none.
const savedSettingsJSON = `{"a":{"cpus":1,"ram":512,"disk":"a.qc2","inject_partition":1,"snapshot":true,"dnb":false,"host":"node1"},` +
	`"b":{"cpus":2,"ram":1024,"disk":"b.qc2","snapshot":false,"dnb":true,"host":""}}`

// useSettingsExperiment stores the experiment exp, running when running is
// set, with VMs a, b and c, each changed since the experiment was created and
// placed on node9, and the saved settings annotation.
func useSettingsExperiment(t *testing.T, running bool, annotation string) {
	t.Helper()

	nodes := make([]map[string]any, 0, 3)
	for _, name := range []string{"a", "b", "c"} {
		node := diskVM(name, "changed.qc2")
		node["hardware"].(map[string]any)["vcpus"] = 8
		node["general"].(map[string]any)["do_not_boot"] = false
		nodes = append(nodes, node)
	}

	started := ""
	if running {
		started = "2024-01-01T00:00:00Z"
	}

	c := namedExperiment(t, "exp", started, nodes...)
	c.Spec["schedules"] = map[string]any{"a": "node9", "b": "node9", "c": "node9"}

	if annotation != "" {
		c.Metadata.Annotations = store.Annotations{experiment.VMDefaultsAnnotation: annotation}
	}

	useTestStore(t, c)
	useTestMM(t, new(testMM))
}

// storedExperiment reads the experiment name back from the test store.
func storedExperiment(t *testing.T, name string) *types.Experiment {
	t.Helper()

	exp, err := experiment.Get(name)
	if err != nil {
		t.Fatalf("experiment.Get(%s) error = %v", name, err)
	}

	return exp
}

// resetVMList is the part of a reset response that the tests compare.
type resetVMList struct {
	VMs []struct {
		Name     string `json:"name"`
		CPUs     int    `json:"cpus"`
		DNB      bool   `json:"dnb"`
		Host     string `json:"host"`
		Defaults *struct {
			CPUs int    `json:"cpus"`
			Host string `json:"host"`
		} `json:"defaults"`
	} `json:"vms"`
	Errors []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"errors"`
}

// TestResetVMSettings restores the saved settings of the VMs a reset names,
// or of all the role may patch, skipping those it may not and reporting those
// with no saved settings.
func TestResetVMSettings(t *testing.T) {
	type outcome struct {
		VMs    []string          // the VMs read back, by name
		CPUs   map[string]int    // the stored CPUs of each VM
		Hosts  map[string]string // the stored schedule
		Errors map[string]string // the status of each VM in errors
	}

	unchanged := map[string]int{"a": 8, "b": 8, "c": 8}
	placed := map[string]string{"a": "node9", "b": "node9", "c": "node9"}

	tests := []struct {
		name       string
		body       string
		patch      []string // the VMs the role may patch
		running    bool
		annotation string
		wantStatus int
		want       outcome
	}{
		{
			name: "named VMs", body: `{"vms":["a","b"]}`, patch: []string{"exp/*"}, annotation: savedSettingsJSON,
			wantStatus: http.StatusOK,
			want: outcome{
				VMs: []string{"a", "b"}, CPUs: map[string]int{"a": 1, "b": 2, "c": 8},
				Hosts: map[string]string{"a": "node1", "c": "node9"}, Errors: map[string]string{},
			},
		},
		{
			name: "every VM the role may patch", body: "", patch: []string{"exp/a", "exp/c"}, annotation: savedSettingsJSON,
			wantStatus: http.StatusOK,
			want: outcome{
				VMs: []string{"a"}, CPUs: map[string]int{"a": 1, "b": 8, "c": 8},
				Hosts: map[string]string{"a": "node1", "b": "node9", "c": "node9"}, Errors: map[string]string{"c": "failed"},
			},
		},
		{
			name: "an empty list", body: `{"vms":[]}`, patch: []string{"exp/b"}, annotation: savedSettingsJSON,
			wantStatus: http.StatusOK,
			want: outcome{
				VMs: []string{"b"}, CPUs: map[string]int{"a": 8, "b": 2, "c": 8},
				Hosts: map[string]string{"a": "node9", "c": "node9"}, Errors: map[string]string{},
			},
		},
		{
			name: "skips the VMs the role may not patch", body: `{"vms":["a","b"]}`, patch: []string{"exp/b"},
			annotation: savedSettingsJSON, wantStatus: http.StatusOK,
			want: outcome{
				VMs: []string{"b"}, CPUs: map[string]int{"a": 8, "b": 2, "c": 8},
				Hosts: map[string]string{"a": "node9", "c": "node9"}, Errors: map[string]string{"a": "skipped"},
			},
		},
		{
			name: "a VM the experiment does not have", body: `{"vms":["nope"]}`, patch: []string{"exp/*"},
			annotation: savedSettingsJSON, wantStatus: http.StatusOK,
			want: outcome{CPUs: unchanged, Hosts: placed, Errors: map[string]string{"nope": "failed"}},
		},
		{
			name: "an experiment that saved no settings", body: `{"vms":["a"]}`, patch: []string{"exp/*"},
			wantStatus: http.StatusOK,
			want:       outcome{CPUs: unchanged, Hosts: placed, Errors: map[string]string{"a": "failed"}},
		},
		{
			name: "no named VM the role may patch", body: `{"vms":["a"]}`, patch: []string{"exp/b"},
			annotation: savedSettingsJSON, wantStatus: http.StatusForbidden, want: outcome{CPUs: unchanged, Hosts: placed},
		},
		{
			name: "no VM the role may patch", patch: []string{"other/*"}, annotation: savedSettingsJSON,
			wantStatus: http.StatusForbidden, want: outcome{CPUs: unchanged, Hosts: placed},
		},
		{
			name: "a running experiment", body: `{"vms":["a"]}`, patch: []string{"exp/*"}, running: true,
			annotation: savedSettingsJSON, wantStatus: http.StatusConflict, want: outcome{CPUs: unchanged, Hosts: placed},
		},
		{
			name: "a malformed body", body: `{"vms":`, patch: []string{"exp/*"}, annotation: savedSettingsJSON,
			wantStatus: http.StatusBadRequest, want: outcome{CPUs: unchanged, Hosts: placed},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useSettingsExperiment(t, tc.running, tc.annotation)
			events := captureAudit(t)

			server := serveAs(t, "alice", testRole("vms", "patch", tc.patch...)).URL
			resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/resetSettings", tc.body)
			body := readBody(t, resp)

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("POST resetSettings %s = %d, want %d: %s", tc.body, resp.StatusCode, tc.wantStatus, body)
			}

			exp := storedExperiment(t, "exp")
			got := outcome{CPUs: map[string]int{}, Hosts: exp.Spec.Schedules()}

			for _, node := range exp.Spec.Topology().Nodes() {
				got.CPUs[node.General().Hostname()] = node.Hardware().VCPU()
			}

			if resp.StatusCode == http.StatusOK {
				var list resetVMList
				if err := json.Unmarshal([]byte(body), &list); err != nil {
					t.Fatalf("decoding the reset response: %v", err)
				}

				got.Errors = map[string]string{}

				for _, vm := range list.VMs {
					got.VMs = append(got.VMs, vm.Name)

					if vm.Defaults == nil || vm.Defaults.CPUs != vm.CPUs {
						t.Errorf("POST resetSettings VM %s = %d CPUs with defaults %+v, want the defaults' CPUs",
							vm.Name, vm.CPUs, vm.Defaults)
					}
				}

				for _, e := range list.Errors {
					got.Errors[e.Name] = e.Status
				}
			}

			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("POST resetSettings %s mismatch (-want +got):\n%s", tc.body, diff)
			}

			for _, name := range tc.want.VMs {
				wantAudit(t, events(), "vm-update", "success", map[string]any{"phenix.vm.name": name})
			}

			if tc.want.Errors["a"] == "skipped" {
				wantDenied(t, events(), "vm-update")
			}
		})
	}
}

// A reset names the VM that has no saved settings, and why.
func TestResetVMSettingsNamesTheMissingSettings(t *testing.T) {
	useSettingsExperiment(t, false, savedSettingsJSON)

	server := serveAs(t, "alice", testRole("vms", "patch", "exp/*")).URL
	resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/resetSettings", `{"vms":["c","nope"]}`)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST resetSettings = %d, want 200: %s", resp.StatusCode, body)
	}

	var list resetVMList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decoding the reset response: %v", err)
	}

	want := map[string]string{"c": experiment.ErrNoVMDefaults.Error(), "nope": "VM nope not found in experiment exp"}
	got := map[string]string{}

	for _, e := range list.Errors {
		got[e.Name] = e.Error
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("POST resetSettings errors mismatch (-want +got):\n%s", diff)
	}
}

// A reset refuses an experiment that is starting or stopping.
func TestResetVMSettingsRefusesALockedExperiment(t *testing.T) {
	useSettingsExperiment(t, false, savedSettingsJSON)

	if err := cache.LockExperimentForStarting("exp"); err != nil {
		t.Fatalf("locking exp: %v", err)
	}

	t.Cleanup(func() { cache.UnlockExperiment("exp") })

	server := serveAs(t, "alice", testRole("vms", "patch", "exp/*")).URL

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/resetSettings", "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusConflict {
		t.Errorf("POST resetSettings of a starting experiment = %d, want 409: %s", resp.StatusCode, body)
	}
}

// A reset holds the experiment's update lock while it saves, saves every VM in
// one update, and frees the lock after.
func TestResetVMSettingsLocksTheExperiment(t *testing.T) {
	useSettingsExperiment(t, false, savedSettingsJSON)

	s, ok := store.DefaultStore.(*testStore)
	if !ok {
		t.Fatal("the store is not a testStore")
	}

	var locks []cache.Status

	s.mu.Lock()
	s.fail = func(op, kind string) error {
		if op == "update" && kind == "Experiment" {
			locks = append(locks, cache.IsExperimentLocked("exp"))
		}

		return nil
	}
	s.mu.Unlock()

	server := serveAs(t, "alice", testRole("vms", "patch", "exp/*")).URL

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/resetSettings", `{"vms":["a","b"]}`)
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST resetSettings = %d, want 200: %s", resp.StatusCode, body)
	}

	s.mu.Lock()
	got := slices.Clone(locks)
	s.mu.Unlock()

	if diff := cmp.Diff([]cache.Status{cache.StatusUpdating}, got); diff != "" {
		t.Errorf("experiment lock at each update mismatch (-want +got):\n%s", diff)
	}

	if status := cache.IsExperimentLocked("exp"); status != "" {
		t.Errorf("experiment exp after the reset is locked with status %s, want unlocked", status)
	}
}

// A reset answers 409, and changes nothing, for an experiment that started
// after the handler read it but before it took the lock.
func TestResetVMSettingsRefusesAnExperimentThatStarted(t *testing.T) {
	useSettingsExperiment(t, false, savedSettingsJSON)

	s, ok := store.DefaultStore.(*testStore)
	if !ok {
		t.Fatal("the store is not a testStore")
	}

	s.mu.Lock()
	s.fail = func(op, kind string) error {
		// the caller holds s.mu, so the configs can change here
		if op == "get" && kind == "Experiment" && cache.IsExperimentLocked("exp") != "" {
			for i := range s.configs {
				if s.configs[i].Metadata.Name == "exp" {
					s.configs[i].Status = map[string]any{"startTime": "2024-01-01T00:00:00Z"}
				}
			}
		}

		return nil
	}
	s.mu.Unlock()

	server := serveAs(t, "alice", testRole("vms", "patch", "exp/*")).URL

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/resetSettings", `{"vms":["a"]}`)
	if body := readBody(t, resp); resp.StatusCode != http.StatusConflict {
		t.Errorf("POST resetSettings of an experiment that started = %d, want 409: %s", resp.StatusCode, body)
	}

	if cpus := storedExperiment(t, "exp").Spec.Topology().FindNodeByName("a").Hardware().VCPU(); cpus != 8 {
		t.Errorf("VM a after a refused reset has %d CPUs, want 8", cpus)
	}
}

// A VM's defaults are its saved settings in the VM list and a VM's GET, null for a VM with none, and null for every
// VM when the saved settings do not decode.
func TestVMDefaultsInVMResponses(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
		want       map[string]*int // each VM's default CPUs, nil for null defaults
	}{
		{name: "saved settings", annotation: savedSettingsJSON, want: map[string]*int{"a": ptr(1), "b": ptr(2), "c": nil}},
		{name: "no saved settings", want: map[string]*int{"a": nil, "b": nil, "c": nil}},
		{name: "saved settings that do not decode", annotation: "{", want: map[string]*int{"a": nil, "b": nil, "c": nil}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useSettingsExperiment(t, false, tc.annotation)

			server := serveAs(t, "alice", combinedRole(testRole("vms", "list", "exp/*"), testRole("vms", "get", "exp/*"))).URL

			resp := do(t, http.MethodGet, server+"/api/v1/experiments/exp/vms", "")
			body := readBody(t, resp)

			var list struct {
				VMs []map[string]json.RawMessage `json:"vms"`
			}

			if err := json.Unmarshal([]byte(body), &list); err != nil {
				t.Fatalf("GET vms = %d %s: %v", resp.StatusCode, body, err)
			}

			got := map[string]*int{}

			for _, vm := range list.VMs {
				var name string

				_ = json.Unmarshal(vm["name"], &name)
				got[name] = defaultCPUs(t, vm)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("GET vms defaults mismatch (-want +got):\n%s", diff)
			}

			for name, want := range tc.want {
				resp := do(t, http.MethodGet, server+"/api/v1/experiments/exp/vms/"+name, "")

				var vm map[string]json.RawMessage
				if err := json.Unmarshal([]byte(readBody(t, resp)), &vm); err != nil {
					t.Fatalf("GET vms/%s: %v", name, err)
				}

				if diff := cmp.Diff(want, defaultCPUs(t, vm)); diff != "" {
					t.Errorf("GET vms/%s defaults mismatch (-want +got):\n%s", name, diff)
				}
			}
		})
	}
}

// defaultCPUs returns the CPUs of a VM's defaults, nil when the VM has the key defaults set to null.
func defaultCPUs(t *testing.T, vm map[string]json.RawMessage) *int {
	t.Helper()

	raw, ok := vm["defaults"]
	if !ok {
		t.Fatalf("VM %s has no defaults key", vm["name"])
	}

	var defaults *struct {
		CPUs int `json:"cpus"`
	}

	if err := json.Unmarshal(raw, &defaults); err != nil {
		t.Fatalf("decoding defaults %s: %v", raw, err)
	}

	if defaults == nil {
		return nil
	}

	return &defaults.CPUs
}

// A reset answers 500, and changes no VM, when the experiment cannot be saved,
// and 404 when the experiment is gone once the reset holds the lock.
func TestResetVMSettingsExperimentErrors(t *testing.T) {
	errSave := errors.New("save failed")

	tests := []struct {
		name       string
		fail       func(op, kind string) error
		wantStatus int
	}{
		{
			name: "the experiment cannot be saved",
			fail: func(op, kind string) error {
				if op == "update" && kind == "Experiment" {
					return errSave
				}

				return nil
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "the experiment is gone",
			fail: func(op, kind string) error {
				if op == "get" && kind == "Experiment" && cache.IsExperimentLocked("exp") != "" {
					return store.ErrNotExist
				}

				return nil
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useSettingsExperiment(t, false, savedSettingsJSON)
			events := captureAudit(t)

			s, ok := store.DefaultStore.(*testStore)
			if !ok {
				t.Fatal("the store is not a testStore")
			}

			s.mu.Lock()
			s.fail = tc.fail
			s.mu.Unlock()

			t.Cleanup(func() {
				s.mu.Lock()
				s.fail = nil
				s.mu.Unlock()
			})

			server := serveAs(t, "alice", testRole("vms", "patch", "exp/*")).URL

			resp := do(t, http.MethodPost, server+"/api/v1/experiments/exp/vms/resetSettings", `{"vms":["a"]}`)
			if body := readBody(t, resp); resp.StatusCode != tc.wantStatus {
				t.Fatalf("POST resetSettings = %d, want %d: %s", resp.StatusCode, tc.wantStatus, body)
			}

			wantAudit(t, events(), "vm-update", "failure", map[string]any{"phenix.vm.name": "a"})

			s.mu.Lock()
			s.fail = nil
			s.mu.Unlock()

			if cpus := storedExperiment(t, "exp").Spec.Topology().FindNodeByName("a").Hardware().VCPU(); cpus != 8 {
				t.Errorf("VM a after a failed reset has %d CPUs, want 8", cpus)
			}
		})
	}
}

// settingsNoTopology is an experiment spec without any topology, not even an empty one.
type settingsNoTopology struct {
	*v1.ExperimentSpec
}

func (settingsNoTopology) Topology() ifaces.TopologySpec {
	return nil
}

func TestExperimentVMNamesWithoutATopology(t *testing.T) {
	t.Parallel()

	exp := &types.Experiment{Spec: settingsNoTopology{new(v1.ExperimentSpec)}}

	if got := experimentVMNames(exp); got != nil {
		t.Errorf("experimentVMNames(no topology) = %v, want nil", got)
	}
}

func TestResetSettingsError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want string
	}{
		{err: experiment.ErrNoVMDefaults, want: experiment.ErrNoVMDefaults.Error()},
		{err: &mm.VMNotFoundError{Err: errors.New("gone")}, want: "VM a not found in experiment exp"},
		{err: errors.New("other"), want: "unable to reset the VM settings"},
	}

	for _, tc := range tests {
		if got := resetSettingsError("exp", "a", tc.err); got != tc.want {
			t.Errorf("resetSettingsError(exp, a, %v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
