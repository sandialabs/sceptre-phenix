package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/api/scorch"
	"phenix/api/scorch/scorchmd"
	"phenix/util/mm"
)

// schedulerInputMM is appsInputMM with the schedulable hosts hosts.
type schedulerInputMM struct {
	appsInputMM

	hosts mm.Hosts
}

func (m *schedulerInputMM) GetClusterHosts(bool, bool) (mm.Hosts, error) {
	m.hostCalls++

	return m.hosts, m.hostErr
}

// useSchedulerInputMM makes fake answer for minimega for the rest of the test.
func useSchedulerInputMM(t *testing.T, fake *schedulerInputMM) {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double
}

// schedulerInputRole may get test-experiment and its config, and list all
// hosts: experiments, configs, then hosts.
func schedulerInputRole() []Policy {
	return []Policy{
		{Resources: []string{"experiments"}, ResourceNames: []string{contractExp}, Verbs: []string{"get"}},
		{Resources: []string{"configs"}, ResourceNames: []string{"Experiment/" + contractExp}, Verbs: []string{"get"}},
		{Resources: []string{"hosts"}, ResourceNames: []string{"*"}, Verbs: []string{"list"}},
	}
}

// schedulerInputRoleWith is schedulerInputRole with its hosts policy for names.
func schedulerInputRoleWith(names ...string) []Policy {
	role := schedulerInputRole()
	role[2].ResourceNames = names

	return role
}

// jsonValue decodes the JSON document data, or fails the test.
func jsonValue(t *testing.T, data []byte) any {
	t.Helper()

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decoding %q: %v", data, err)
	}

	return v
}

// GET /experiments/{name}/schedule/input answers the bytes that
// experiment.SchedulerInput, which phenix util scheduler-json prints, makes:
// the spec and the schedulable hosts.
func TestGetExperimentSchedulerInput(t *testing.T) {
	useTestExperiment(t)
	useSchedulerInputMM(t, &schedulerInputMM{hosts: mm.Hosts{{Name: "compute1", Schedulable: true}}})

	vars := map[string]string{"name": contractExp}
	req := roleRequest(http.MethodGet, "/experiments/"+contractExp+"/schedule/input", "", vars, policyRole(schedulerInputRole()))

	got, rec := serveHandler(GetExperimentSchedulerInput, req)
	if got.Status != http.StatusOK {
		t.Fatalf("GET schedule/input = %+v, want 200: %s", got, rec.Body)
	}

	if ct := rec.Header().Get("Content-Type"); ct != mimeJSON {
		t.Errorf("GET schedule/input Content-Type = %q, want %s", ct, mimeJSON)
	}

	want, err := experiment.SchedulerInput(t.Context(), contractExp)
	if err != nil {
		t.Fatalf("experiment.SchedulerInput(%s) error = %v", contractExp, err)
	}

	if diff := cmp.Diff(string(want), rec.Body.String()); diff != "" {
		t.Errorf("GET schedule/input is not the output of experiment.SchedulerInput (-want +got):\n%s", diff)
	}

	var body struct {
		Spec  map[string]any `json:"spec"`
		Hosts []struct {
			Name string `json:"name"`
		} `json:"hosts"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding GET schedule/input body: %v", err)
	}

	if body.Spec["experimentName"] != contractExp || len(body.Hosts) != 1 || body.Hosts[0].Name != "compute1" {
		t.Errorf("GET schedule/input = spec %v, hosts %v, want the spec of %s and the host compute1",
			body.Spec["experimentName"], body.Hosts, contractExp)
	}
}

// GET /experiments/{name}/schedule/input refuses, before it reads anything, a
// role without experiments get, configs get or hosts list, and after the read
// a role that may not list each host. It answers 404 for an unknown
// experiment, 500 for a store error, 502 when minimega fails, 503 when the
// mesh peers do not answer and 504 at the time limit.
func TestGetExperimentSchedulerInputErrors(t *testing.T) {
	var (
		errMinimega = errors.New("minimega did not answer")
		errStore    = errors.New("the store failed")
	)

	tests := map[string]struct {
		exp       string
		role      []Policy
		hostErr   error
		storeErr  error
		want      int
		wantHosts int
	}{
		"no experiments get": {exp: contractExp, role: schedulerInputRole()[1:], want: http.StatusForbidden},
		"no configs get": {
			exp: contractExp, role: []Policy{schedulerInputRole()[0], schedulerInputRole()[2]}, want: http.StatusForbidden,
		},
		"no hosts list": {exp: contractExp, role: schedulerInputRole()[:2], want: http.StatusForbidden},
		"hosts list on one host": {
			exp: contractExp, role: schedulerInputRoleWith("compute1"), want: http.StatusForbidden, wantHosts: 1,
		},
		"hosts list but one host": {
			exp: contractExp, role: schedulerInputRoleWith("*", "!compute2"), want: http.StatusForbidden, wantHosts: 1,
		},
		"an unknown experiment": {
			exp: "none", want: http.StatusNotFound,
			role: []Policy{
				{
					Resources: []string{"experiments", "configs", "hosts"}, ResourceNames: []string{"*", "*/*"},
					Verbs: []string{"get", "list"},
				},
			},
		},
		"a store that fails": {
			exp: contractExp, role: schedulerInputRole(), storeErr: errStore, want: http.StatusInternalServerError,
		},
		"no hosts": {
			exp: contractExp, role: schedulerInputRole(), hostErr: errMinimega, want: http.StatusBadGateway, wantHosts: 1,
		},
		"mesh peers that do not answer": {
			exp: contractExp, role: schedulerInputRole(), hostErr: mm.ErrMeshUnavailable, want: http.StatusServiceUnavailable,
			wantHosts: 1,
		},
		"the time limit": {
			exp: contractExp, role: schedulerInputRole(), hostErr: context.DeadlineExceeded, want: http.StatusGatewayTimeout,
			wantHosts: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)

			if tc.storeErr != nil {
				failTestStore(t, "get", "Experiment", tc.storeErr)
			}

			fake := &schedulerInputMM{hosts: mm.Hosts{{Name: "compute1"}, {Name: "compute2"}}}
			fake.hostErr = tc.hostErr
			useSchedulerInputMM(t, fake)

			vars := map[string]string{"name": tc.exp}

			got, rec := serveHandler(
				GetExperimentSchedulerInput,
				roleRequest(http.MethodGet, "/experiments/"+tc.exp+"/schedule/input", "", vars, policyRole(tc.role)),
			)
			if got.Status != tc.want {
				t.Errorf("GET schedule/input of %s = %+v, want %d: %s", tc.exp, got, tc.want, rec.Body)
			}

			if got.Status != http.StatusOK {
				_ = errorMessage(t, got.Status, rec.Body.String())
			}

			if strings.Contains(rec.Body.String(), errMinimega.Error()) {
				t.Errorf("GET schedule/input body %s holds the error text of the fake", rec.Body)
			}

			if fake.hostCalls != tc.wantHosts {
				t.Errorf("GET schedule/input of %s asked minimega %d times for hosts, want %d", tc.exp, fake.hostCalls, tc.wantHosts)
			}
		})
	}
}

// A client that leaves while minimega answers gets nothing written.
func TestGetExperimentSchedulerInputCanceled(t *testing.T) {
	useTestExperiment(t)

	req := roleRequest(http.MethodGet, "/experiments/"+contractExp+"/schedule/input", "", map[string]string{"name": contractExp},
		policyRole(schedulerInputRole()))

	ctx, cancel := context.WithCancel(req.Context())
	cancel()

	fake := &schedulerInputMM{}
	fake.hostErr = ctx.Err()
	useSchedulerInputMM(t, fake)

	got, rec := serveHandler(GetExperimentSchedulerInput, req.WithContext(ctx))
	if got.Status != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("GET schedule/input of a canceled request = %+v, %q, want nothing written", got, rec.Body)
	}
}

// useScorchInputExperiment stores test-experiment, running, with the VM
// test-vm. Its scorch app runs the component probe of type fake in the start
// stage of run 0 and in the stop stage of its loop, which has two passes, and
// the component other in no run. Run 1 has the tap component mark.
func useScorchInputExperiment(t *testing.T) {
	t.Helper()

	cfg := testExperiment(t, "2024-01-01T00:00:00Z", map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": contractVM},
		"hardware": map[string]any{"vcpus": 1, "memory": 512, "os_type": "linux", "drives": []map[string]any{{"image": "test.qc2"}}},
	})

	cfg.Spec["scenario"] = map[string]any{"apps": []any{map[string]any{
		"name": "scorch",
		"metadata": map[string]any{
			"components": []any{
				map[string]any{
					"name": "probe", "type": "fake",
					"metadata": map[string]any{"msg": "{{X}} and {{Y}}", "n": "{{N}}"},
				},
				map[string]any{"name": "other", "type": "fake", "metadata": map[string]any{"keep": true}},
				map[string]any{"name": "mark", "type": "tap", "metadata": map[string]any{}},
			},
			"runs": []any{
				map[string]any{
					"replace": map[string]any{"{{X}}": []any{"fixed"}},
					"start":   []any{"probe"},
					"loop": map[string]any{
						"count":   2,
						"replace": map[string]any{"{{Y}}": []any{"inner"}, "{{N}}": []any{7}},
						"stop":    []any{"probe"},
					},
				},
				map[string]any{"start": []any{"mark", "probe"}},
			},
		},
	}}}

	useTestStore(t, cfg)
}

// scorchInputRole may get test-experiment and its config, and list all of
// its VMs: experiments, configs, then vms.
func scorchInputRole() []Policy {
	return []Policy{
		{Resources: []string{"experiments"}, ResourceNames: []string{contractExp}, Verbs: []string{"get"}},
		{Resources: []string{"configs"}, ResourceNames: []string{"Experiment/" + contractExp}, Verbs: []string{"get"}},
		{Resources: []string{"vms"}, ResourceNames: []string{contractExp + "/*"}, Verbs: []string{"list"}},
	}
}

// scorchInputRequest is a request, by role, for the input of the component
// cmp of test-experiment with query.
func scorchInputRequest(cmp, query string, role []Policy) *http.Request {
	target := "/experiments/" + contractExp + "/scorch/components/" + cmp + "/input"
	if query != "" {
		target += "?" + query
	}

	return roleRequest(http.MethodGet, target, "", map[string]string{"name": contractExp, "cmp": cmp}, policyRole(role))
}

// GET /experiments/{name}/scorch/components/{cmp}/input answers the stdin
// that scorch.UserComponentInput, which phenix util scorch-json prints, makes
// for the same options, with and without runtime.
func TestGetExperimentScorchComponentInput(t *testing.T) {
	run, loop := 0, 1

	tests := map[string]struct {
		query    string
		opts     scorch.InputOptions
		wantVMs  bool
		wantMeta map[string]any
	}{
		"the first place": {
			wantMeta: map[string]any{"msg": "fixed and {{Y}}", "n": "{{N}}"},
		},
		"run, loop, stage and count": {
			query:    "run=0&loop=1&stage=stop&count=1",
			opts:     scorch.InputOptions{Run: &run, Loop: &loop, Stage: scorch.ActionStop, Count: 1},
			wantMeta: map[string]any{"msg": "fixed and inner", "n": float64(7)},
		},
		"replacements": {
			query: "loop=1&replace=" + url.QueryEscape(`{{X}}="pinned"`) + "&replace=" + url.QueryEscape("{{N}}=12"),
			opts: scorch.InputOptions{
				Loop: &loop, Replace: scorchmd.ResolvedReplacements{"{{X}}": "pinned", "{{N}}": float64(12)},
			},
			wantMeta: map[string]any{"msg": "pinned and inner", "n": float64(12)},
		},
		"runtime": {
			query:    "runtime=true",
			opts:     scorch.InputOptions{Runtime: true},
			wantVMs:  true,
			wantMeta: map[string]any{"msg": "fixed and {{Y}}", "n": "{{N}}"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useScorchInputExperiment(t)
			useAppsInputMM(t, &appsInputMM{testMM: testMM{vms: mm.VMs{{Name: contractVM, Host: "compute1", State: "RUNNING"}}}})

			got, rec := serveHandler(GetExperimentScorchComponentInput, scorchInputRequest("probe", tc.query, scorchInputRole()))
			if got.Status != http.StatusOK {
				t.Fatalf("GET probe/input?%s = %+v, want 200: %s", tc.query, got, rec.Body)
			}

			if ct := rec.Header().Get("Content-Type"); ct != mimeJSON {
				t.Errorf("GET probe/input?%s Content-Type = %q, want %s", tc.query, ct, mimeJSON)
			}

			want, err := scorch.UserComponentInput(t.Context(), contractExp, "probe", tc.opts)
			if err != nil {
				t.Fatalf("scorch.UserComponentInput(probe, %+v) error = %v", tc.opts, err)
			}

			if diff := cmp.Diff(jsonValue(t, want.Stdin), jsonValue(t, rec.Body.Bytes())); diff != "" {
				t.Errorf("GET probe/input?%s is not the stdin of scorch.UserComponentInput (-want +got):\n%s", tc.query, diff)
			}

			var body struct {
				Spec struct {
					Scenario struct {
						Apps []struct {
							Metadata struct {
								Components []struct {
									Name     string         `json:"name"`
									Metadata map[string]any `json:"metadata"`
								} `json:"components"`
							} `json:"metadata"`
						} `json:"apps"`
					} `json:"scenario"`
				} `json:"spec"`
				VMs []struct {
					Name string `json:"name"`
				} `json:"vms"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding GET probe/input?%s body: %v", tc.query, err)
			}

			if diff := cmp.Diff(tc.wantMeta, body.Spec.Scenario.Apps[0].Metadata.Components[0].Metadata); diff != "" {
				t.Errorf("GET probe/input?%s metadata of probe mismatch (-want +got):\n%s", tc.query, diff)
			}

			if gotVMs := len(body.VMs) > 0; gotVMs != tc.wantVMs {
				t.Errorf("GET probe/input?%s has VMs %v, want VMs: %t", tc.query, body.VMs, tc.wantVMs)
			}
		})
	}
}

// GET /experiments/{name}/scorch/components/{cmp}/input refuses, before it
// reads anything, a role without experiments get or configs get, or with
// runtime, without vms list; with runtime, it refuses after the read a role
// that may not list each VM. It answers 400 for a bad query value and a place
// that does not run the component, 404 for an unknown experiment or
// component, 500 for a store error and 502 when minimega fails.
func TestGetExperimentScorchComponentInputErrors(t *testing.T) {
	var (
		errMinimega = errors.New("minimega did not answer")
		errStore    = errors.New("the store failed")
	)

	tests := map[string]struct {
		cmp, query string
		role       []Policy
		hostErr    error
		storeErr   error
		noScorch   bool
		want       int
		wantHosts  int
	}{
		"no experiments get": {cmp: "probe", role: scorchInputRole()[1:], want: http.StatusForbidden},
		"no configs get": {
			cmp: "probe", role: []Policy{scorchInputRole()[0], scorchInputRole()[2]}, want: http.StatusForbidden,
		},
		"runtime without vms list": {
			cmp: "probe", query: "runtime=true", role: scorchInputRole()[:2], want: http.StatusForbidden,
		},
		"runtime with vms list but a VM": {
			cmp: "probe", query: "runtime=true", want: http.StatusForbidden, wantHosts: 1,
			role: append(scorchInputRole()[:2], Policy{
				Resources: []string{"vms"}, ResourceNames: []string{contractExp + "/*", "!" + contractExp + "/" + contractVM},
				Verbs: []string{"list"},
			}),
		},
		"run that is not a number":      {cmp: "probe", query: "run=x", role: scorchInputRole(), want: http.StatusBadRequest},
		"loop that is not a number":     {cmp: "probe", query: "loop=1.5", role: scorchInputRole(), want: http.StatusBadRequest},
		"count that is not a number":    {cmp: "probe", query: "count=", role: scorchInputRole(), want: http.StatusBadRequest},
		"unknown stage":                 {cmp: "probe", query: "stage=done", role: scorchInputRole(), want: http.StatusBadRequest},
		"empty stage":                   {cmp: "probe", query: "stage=", role: scorchInputRole(), want: http.StatusBadRequest},
		"replacement without a value":   {cmp: "probe", query: "replace=x", role: scorchInputRole(), want: http.StatusBadRequest},
		"runtime that is not a boolean": {cmp: "probe", query: "runtime=maybe", role: scorchInputRole(), want: http.StatusBadRequest},
		"unknown replacement": {
			cmp: "probe", query: "replace=" + url.QueryEscape("{{Y}}=1"), role: scorchInputRole(), want: http.StatusBadRequest,
		},
		"loop without the component": {cmp: "probe", query: "loop=2", role: scorchInputRole(), want: http.StatusBadRequest},
		"count past the loop":        {cmp: "probe", query: "count=1", role: scorchInputRole(), want: http.StatusBadRequest},
		"component in no run":        {cmp: "other", role: scorchInputRole(), want: http.StatusBadRequest},
		"built-in component":         {cmp: "mark", role: scorchInputRole(), want: http.StatusBadRequest},
		"unknown component":          {cmp: "nope", role: scorchInputRole(), want: http.StatusNotFound},
		"experiment without SCORCH": {
			cmp: "probe", role: scorchInputRole(), noScorch: true, want: http.StatusNotFound,
		},
		"a store that fails": {
			cmp: "probe", role: scorchInputRole(), storeErr: errStore, want: http.StatusInternalServerError,
		},
		"runtime without hosts": {
			cmp: "probe", query: "runtime=true", role: scorchInputRole(), hostErr: errMinimega, want: http.StatusBadGateway,
			wantHosts: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.noScorch {
				useTestExperiment(t)
			} else {
				useScorchInputExperiment(t)
			}

			if tc.storeErr != nil {
				failTestStore(t, "get", "Experiment", tc.storeErr)
			}

			fake := &appsInputMM{testMM: testMM{vms: mm.VMs{{Name: contractVM}}}, hostErr: tc.hostErr}
			useAppsInputMM(t, fake)

			got, rec := serveHandler(GetExperimentScorchComponentInput, scorchInputRequest(tc.cmp, tc.query, tc.role))
			if got.Status != tc.want {
				t.Errorf("GET %s/input?%s = %+v, want %d: %s", tc.cmp, tc.query, got, tc.want, rec.Body)
			}

			if got.Status != http.StatusOK {
				_ = errorMessage(t, got.Status, rec.Body.String())
			}

			if strings.Contains(rec.Body.String(), errMinimega.Error()) {
				t.Errorf("GET %s/input?%s body %s holds the error text of the fake", tc.cmp, tc.query, rec.Body)
			}

			if fake.hostCalls != tc.wantHosts {
				t.Errorf("GET %s/input?%s asked minimega %d times for hosts, want %d", tc.cmp, tc.query, fake.hostCalls, tc.wantHosts)
			}
		})
	}
}

// GET /experiments/{name}/scorch/components/{cmp}/input of an unknown
// experiment answers 404.
func TestGetExperimentScorchComponentInputUnknownExperiment(t *testing.T) {
	useScorchInputExperiment(t)

	role := []Policy{{Resources: []string{"experiments", "configs"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"get"}}}
	req := roleRequest(http.MethodGet, "/experiments/none/scorch/components/probe/input", "",
		map[string]string{"name": "none", "cmp": "probe"}, policyRole(role))

	got, rec := serveHandler(GetExperimentScorchComponentInput, req)
	if got.Status != http.StatusNotFound {
		t.Errorf("GET none/scorch/components/probe/input = %+v, want 404: %s", got, rec.Body)
	}
}
