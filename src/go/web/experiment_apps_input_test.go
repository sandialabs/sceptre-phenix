package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/web/rbac"
)

// appsInputMM is the test minimega with a schedulable host, failing the host
// query with hostErr and leaving the VMs unknown with vmErr when they are set.
type appsInputMM struct {
	testMM

	hostErr, vmErr error
	hostCalls      int
}

func (m *appsInputMM) GetClusterHosts(bool, bool) (mm.Hosts, error) {
	m.hostCalls++

	return mm.Hosts{{Name: "compute1", Schedulable: true}}, m.hostErr
}

func (m *appsInputMM) GetVMInfoObserved(opts ...mm.Option) (mm.VMs, freshness.Observation) {
	vms := m.GetVMInfo(opts...)
	if m.vmErr != nil {
		return vms, freshness.Observation{Err: m.vmErr}
	}

	return vms, freshness.Observed(time.Now())
}

// useAppsInputMM makes fake answer for minimega for the rest of the test.
func useAppsInputMM(t *testing.T, fake *appsInputMM) {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double
}

// policyRole is a role of the policies.
func policyRole(policies []Policy) rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Policies: policySpecs(policies)}}
}

// appsInputRole may get test-experiment and its config, and list all of its
// VMs: experiments, vms, then configs.
func appsInputRole() []Policy {
	return []Policy{
		{Resources: []string{"experiments"}, ResourceNames: []string{contractExp}, Verbs: []string{"get"}},
		{Resources: []string{"vms"}, ResourceNames: []string{contractExp + "/*"}, Verbs: []string{"list"}},
		{Resources: []string{"configs"}, ResourceNames: []string{"Experiment/" + contractExp}, Verbs: []string{"get"}},
	}
}

// appsInputRoleWith is appsInputRole with its vms policy for names.
func appsInputRoleWith(names ...string) []Policy {
	role := appsInputRole()
	role[1].ResourceNames = names

	return role
}

// GET /experiments/{name}/apps/input answers the experiment with the
// schedulable hosts and its VMs, as phenix util app-json prints it.
func TestGetExperimentAppsInput(t *testing.T) {
	useTestExperiment(t)
	fake := &appsInputMM{testMM: testMM{vms: mm.VMs{{Name: contractVM, Host: "compute1", State: "RUNNING"}}}}
	useAppsInputMM(t, fake)

	role := policyRole(appsInputRole())
	vars := map[string]string{"name": contractExp}

	got, rec := serveHandler(GetExperimentAppsInput, roleRequest(http.MethodGet, "/experiments/"+contractExp+"/apps/input", "", vars, role))
	if got.Status != http.StatusOK {
		t.Fatalf("GET apps/input = %+v, want 200: %s", got, rec.Body)
	}

	if ct := rec.Header().Get("Content-Type"); ct != mimeJSON {
		t.Errorf("GET apps/input Content-Type = %q, want %s", ct, mimeJSON)
	}

	type named struct {
		Name string `json:"name"`
	}

	var body struct {
		Metadata named          `json:"metadata"`
		Spec     map[string]any `json:"spec"`
		Hosts    []named        `json:"hosts"`
		VMs      []named        `json:"vms"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding GET apps/input body: %v", err)
	}

	if body.Metadata.Name != contractExp || body.Spec["experimentName"] != contractExp {
		t.Errorf("GET apps/input experiment = %q (spec %v), want %s", body.Metadata.Name, body.Spec["experimentName"], contractExp)
	}

	if diff := cmp.Diff([]named{{Name: "compute1"}}, body.Hosts); diff != "" {
		t.Errorf("GET apps/input hosts mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]named{{Name: contractVM}}, body.VMs); diff != "" {
		t.Errorf("GET apps/input VMs mismatch (-want +got):\n%s", diff)
	}
}

// GET /experiments/{name}/apps/input refuses, before it reads anything, a role
// without experiments get or without vms list on all the VMs; it answers 404
// for an unknown experiment, 500 for a store error and 502 when minimega fails.
func TestGetExperimentAppsInputErrors(t *testing.T) {
	errMinimega := errors.New("minimega did not answer")

	tests := map[string]struct {
		exp       string
		role      []Policy
		hostErr   error
		vmErr     error
		storeErr  error
		extraVM   string
		want      int
		wantHosts int
	}{
		"no experiments get": {
			exp: contractExp, role: appsInputRole()[1:], want: http.StatusForbidden,
		},
		"no vms list": {
			exp: contractExp, role: slices.Delete(appsInputRole(), 1, 2), want: http.StatusForbidden,
		},
		"no configs get": {
			exp: contractExp, role: appsInputRole()[:2], want: http.StatusForbidden,
		},
		"vms list on one VM": {
			exp: contractExp, role: appsInputRoleWith(contractExp + "/" + contractVM), want: http.StatusForbidden,
		},
		"vms list on all but a VM minimega reports": {
			exp: contractExp, role: appsInputRoleWith(contractExp+"/*", "!"+contractExp+"/secret"), extraVM: "secret",
			want: http.StatusForbidden, wantHosts: 1,
		},
		"vms list on all but a topology VM": {
			exp: contractExp, role: appsInputRoleWith(contractExp+"/*", "!"+contractExp+"/"+contractVM),
			want: http.StatusForbidden, wantHosts: 1,
		},
		"vms list on one-letter VM names": {
			exp: contractExp, role: appsInputRoleWith(contractExp + "/?"), want: http.StatusForbidden, wantHosts: 1,
		},
		"an unknown experiment": {
			exp:  "none",
			want: http.StatusNotFound,
			role: []Policy{
				{Resources: []string{"experiments", "vms", "configs"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"get", "list"}},
			},
		},
		"a store that fails": {
			exp: contractExp, role: appsInputRole(), storeErr: errMinimega, want: http.StatusInternalServerError,
		},
		"no hosts": {
			exp: contractExp, role: appsInputRole(), hostErr: errMinimega, want: http.StatusBadGateway, wantHosts: 1,
		},
		"VMs not reported": {
			exp: contractExp, role: appsInputRole(), vmErr: errMinimega, want: http.StatusBadGateway, wantHosts: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)

			if tc.storeErr != nil {
				failTestStore(t, "get", "Experiment", tc.storeErr)
			}

			fake := &appsInputMM{hostErr: tc.hostErr, vmErr: tc.vmErr}
			if tc.extraVM != "" {
				fake.vms = mm.VMs{{Name: contractVM}, {Name: tc.extraVM}}
			}
			useAppsInputMM(t, fake)

			role := policyRole(tc.role)
			vars := map[string]string{"name": tc.exp}

			got, rec := serveHandler(
				GetExperimentAppsInput,
				roleRequest(http.MethodGet, "/experiments/"+tc.exp+"/apps/input", "", vars, role),
			)
			if got.Status != tc.want {
				t.Errorf("GET apps/input of %s = %+v, want %d: %s", tc.exp, got, tc.want, rec.Body)
			}

			if got.Status != http.StatusOK {
				_ = errorMessage(t, got.Status, rec.Body.String())
			}

			if got.Status == http.StatusBadGateway && strings.Contains(rec.Body.String(), errMinimega.Error()) {
				t.Errorf("GET apps/input 502 body %s holds minimega's error text", rec.Body)
			}

			if fake.hostCalls != tc.wantHosts {
				t.Errorf("GET apps/input of %s asked minimega %d times for hosts, want %d", tc.exp, fake.hostCalls, tc.wantHosts)
			}

			if tc.want == http.StatusForbidden && tc.wantHosts == 0 && fake.vmListings.Load() != 0 {
				t.Errorf("GET apps/input of %s refused, but asked minimega for VMs", tc.exp)
			}
		})
	}
}

// A client that leaves while minimega answers gets nothing written.
func TestGetExperimentAppsInputCanceled(t *testing.T) {
	useTestExperiment(t)

	req := roleRequest(http.MethodGet, "/experiments/"+contractExp+"/apps/input", "", map[string]string{"name": contractExp},
		policyRole(appsInputRole()))

	ctx, cancel := context.WithCancel(req.Context())
	cancel()

	fake := &appsInputMM{hostErr: ctx.Err()}
	useAppsInputMM(t, fake)

	got, rec := serveHandler(GetExperimentAppsInput, req.WithContext(ctx))
	if got.Status != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("GET apps/input of a canceled request = %+v, %q, want nothing written", got, rec.Body)
	}
}
