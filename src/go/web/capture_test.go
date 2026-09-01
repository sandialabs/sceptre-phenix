package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/gorilla/mux"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// captureHandlerTestMM is a test double for mm.MM used by the StopVMCaptures
// handler tests. Embedding the (nil) mm.MM interface means any method call
// this test doesn't stub will panic, which is fine since these tests only
// ever exercise the capture-related methods.
type captureHandlerTestMM struct {
	mm.MM

	vmInfo   mm.VMs
	captures []mm.Capture

	stopCalls int
	stopErr   error
}

func (m *captureHandlerTestMM) GetVMInfo(...mm.Option) mm.VMs {
	return m.vmInfo
}

func (m *captureHandlerTestMM) GetVMCaptures(...mm.Option) []mm.Capture {
	return m.captures
}

func (m *captureHandlerTestMM) StopVMCapture(...mm.Option) error {
	m.stopCalls++

	return m.stopErr
}

// installCaptureHandlerTestMM installs the given fake as mm.DefaultMM for the
// duration of the test, restoring the original afterward.
func installCaptureHandlerTestMM(t *testing.T, fake *captureHandlerTestMM) {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double
}

// installCaptureHandlerTestExperiment builds a store mock backing a running
// experiment (i.e. one with a non-empty status start time) named
// "test-experiment" with a single VM named "test-vm" that has two network
// interfaces on VLANs "EXP_1" and "EXP_2".
func installCaptureHandlerTestExperiment(t *testing.T) {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	c := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name: "test-experiment",
		},
		Spec: map[string]any{
			"experimentName": "test-experiment",
			"baseDir":        t.TempDir(),
			"topology": map[string]any{
				"nodes": []map[string]any{
					{
						"type": "VirtualMachine",
						"general": map[string]any{
							"hostname":    "test-vm",
							"do_not_boot": false,
							"snapshot":    false,
						},
						"hardware": map[string]any{
							"vcpus":   2,
							"memory":  512,
							"os_type": "linux",
							"drives": []map[string]any{
								{
									"image":            "test.qc2",
									"inject_partition": 1,
								},
							},
						},
						"network": map[string]any{
							"interfaces": []map[string]any{
								{"name": "IF0", "vlan": "EXP_1"},
								{"name": "IF1", "vlan": "EXP_2"},
							},
						},
					},
				},
			},
		},
		Status: map[string]any{
			"startTime": "2024-01-01T00:00:00Z",
		},
	}

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		*cfg = c

		return nil
	}).AnyTimes()

	store.DefaultStore = m //nolint:reassign // monkey patching for test
}

// stopVMCapturesTestRequest builds a DELETE request for the StopVMCaptures
// handler targeting "test-experiment"/"test-vm", with the given raw query
// string (e.g. "iface=1"), pre-populated with mux vars and a permissive
// rbac.Role in its context so the handler's role check passes.
func stopVMCapturesTestRequest(rawQuery string) *http.Request {
	url := "/api/v1/experiments/test-experiment/vms/test-vm/captures"
	if rawQuery != "" {
		url += "?" + rawQuery
	}

	req := httptest.NewRequest(http.MethodDelete, url, nil)
	req = mux.SetURLVars(req, map[string]string{"exp": "test-experiment", "name": "test-vm"})

	role := rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{
					Resources:     []string{"vms/captures"},
					ResourceNames: []string{"*/*"},
					Verbs:         []string{"delete"},
				},
			},
		},
	}

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	return req.WithContext(ctx)
}

// twoInterfaceVMInfo returns mm.VMs describing a single running VM
// ("test-vm") with two network interfaces, matching the topology built by
// installCaptureHandlerTestExperiment.
func twoInterfaceVMInfo() mm.VMs {
	return mm.VMs{
		{
			Name:     "test-vm",
			Running:  true,
			Networks: []string{"EXP_1 (101)", "EXP_2 (102)"},
		},
	}
}

func TestStopVMCapturesStopsAllWithoutIfaceQuery(t *testing.T) {
	installCaptureHandlerTestExperiment(t)

	fake := &captureHandlerTestMM{
		vmInfo: twoInterfaceVMInfo(),
		captures: []mm.Capture{
			{VM: "test-vm", Interface: 0, Filepath: "/tmp/0.pcap"},
			{VM: "test-vm", Interface: 1, Filepath: "/tmp/1.pcap"},
		},
	}
	installCaptureHandlerTestMM(t, fake)

	rec := httptest.NewRecorder()
	StopVMCaptures(rec, stopVMCapturesTestRequest(""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	if fake.stopCalls != 1 {
		t.Fatalf("expected StopVMCapture to be called once, got %d calls", fake.stopCalls)
	}
}

func TestStopVMCapturesStopsOnlyRequestedInterface(t *testing.T) {
	installCaptureHandlerTestExperiment(t)

	fake := &captureHandlerTestMM{
		vmInfo: twoInterfaceVMInfo(),
		captures: []mm.Capture{
			{VM: "test-vm", Interface: 0, Filepath: "/tmp/0.pcap"},
			{VM: "test-vm", Interface: 1, Filepath: "/tmp/1.pcap"},
		},
	}
	installCaptureHandlerTestMM(t, fake)

	rec := httptest.NewRecorder()
	StopVMCaptures(rec, stopVMCapturesTestRequest("iface=1"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	if fake.stopCalls != 1 {
		t.Fatalf("expected StopVMCapture to be called once, got %d calls", fake.stopCalls)
	}
}

// TestStopVMCapturesUnrequestedInterfaceNotCaptured verifies that requesting
// to stop a capture on an interface that isn't currently being captured
// fails without calling into minimega, so it can't be confused with stopping
// all captures.
func TestStopVMCapturesUnrequestedInterfaceNotCaptured(t *testing.T) {
	installCaptureHandlerTestExperiment(t)

	fake := &captureHandlerTestMM{
		vmInfo:   twoInterfaceVMInfo(),
		captures: []mm.Capture{{VM: "test-vm", Interface: 1, Filepath: "/tmp/1.pcap"}},
	}
	installCaptureHandlerTestMM(t, fake)

	rec := httptest.NewRecorder()
	StopVMCaptures(rec, stopVMCapturesTestRequest("iface=0"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	if fake.stopCalls != 0 {
		t.Fatalf(
			"expected StopVMCapture to not be called when the requested interface isn't captured, got %d calls",
			fake.stopCalls,
		)
	}
}

func TestStopVMCapturesRejectsNonIntegerIface(t *testing.T) {
	installCaptureHandlerTestExperiment(t)

	fake := &captureHandlerTestMM{vmInfo: twoInterfaceVMInfo()}
	installCaptureHandlerTestMM(t, fake)

	rec := httptest.NewRecorder()
	StopVMCaptures(rec, stopVMCapturesTestRequest("iface=not-a-number"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	if fake.stopCalls != 0 {
		t.Fatalf("expected StopVMCapture to not be called, got %d calls", fake.stopCalls)
	}
}

// TestStopVMCapturesForbidden verifies the role check still applies to the
// per-interface stop, not just the stop-all path.
func TestStopVMCapturesForbidden(t *testing.T) {
	req := stopVMCapturesTestRequest("iface=0")

	// Replace the permissive role installed by stopVMCapturesTestRequest with
	// one that has no policies (a non-nil Spec is required; Role.Allowed
	// dereferences Spec.Policies directly rather than treating a nil Spec as
	// "no policies").
	noPolicies := rbac.Role{Spec: &v1.RoleSpec{}}

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, noPolicies)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	StopVMCaptures(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
