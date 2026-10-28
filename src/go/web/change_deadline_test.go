package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"
	"google.golang.org/protobuf/encoding/protojson"

	"phenix/util/mm/mmtest"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/weberror"
)

// shortenChanges gives the requests that change VMs a deadline of d until
// the test ends.
func shortenChanges(t *testing.T, d time.Duration) {
	t.Helper()

	one, many := vmChangeTimeout, vmsChangeTimeout
	vmChangeTimeout, vmsChangeTimeout = d, d

	t.Cleanup(func() { vmChangeTimeout, vmsChangeTimeout = one, many })
}

// vmChange is a request changing VM a of the running experiment exp.
type vmChange struct {
	handler weberror.ErrorHandler
	method  string
	path    string
	body    string
	// the exp route variable, if not "exp"
	exp string
	// the snapshot route variable, for a restore
	snapshot string
}

// vmChanges are requests that change a VM through minimega, from the ones that send a single command to the ones
// of several commands that run to the end once their first is sent.
func vmChanges() map[string]vmChange {
	const vm = "/api/v1/experiments/exp/vms/a"

	return map[string]vmChange{
		"start":         {handler: StartVM, method: http.MethodPost, path: vm + "/start"},
		"eject disc":    {handler: EjectOpticalDisc, method: http.MethodDelete, path: vm + "/cdrom"},
		"stop captures": {handler: StopVMCaptures, method: http.MethodDelete, path: vm + "/captures"},
		"snapshot":      {handler: SnapshotVM, method: http.MethodPost, path: vm + "/snapshots", body: `{"filename":"s1"}`},
		"memory snapshot": {
			handler: CreateVMMemorySnapshot,
			method:  http.MethodPost,
			path:    vm + "/memorySnapshot",
			body:    `{"filename":"m1"}`,
		},
		"restore": {handler: RestoreVM, method: http.MethodPost, path: vm + "/snapshots/s1", snapshot: "s1"},
		"commit":  {handler: CommitVM, method: http.MethodPost, path: vm + "/commit", body: `{"filename":"c1.qc2"}`},
		"tags": {
			handler: UpdateVM, method: http.MethodPatch, path: vm, body: `{"tags":{"k":"v"},"tag_update_mode":"SET"}`,
		},
		"change disc": {handler: ChangeOpticalDisc, method: http.MethodPost, path: vm + "/cdrom?isoPath=/d.iso"},
		"start capture": {
			handler: StartVMCapture, method: http.MethodPost, path: vm + "/captures", body: `{"filename":"a.pcap"}`,
		},
		"redeploy": {handler: RedeployVM, method: http.MethodPost, path: vm + "/redeploy", body: `{}`},
		"capture subnet": {
			handler: StartCaptureSubnet,
			method:  http.MethodPost,
			path:    "/api/v1/experiments/exp/captureSubnet",
			body:    `{"subnet":"10.0.0.0/24"}`,
		},
		"stop subnet captures": {
			handler: StopCaptureSubnet,
			method:  http.MethodPost,
			path:    "/api/v1/experiments/exp/stopCaptureSubnet",
			body:    `{"subnet":"10.0.0.0/24"}`,
		},
	}
}

// serveVMChange serves the request as alice with every permission, in ctx, and returns its answer once the handler
// returns, failing the test if that takes over 5 seconds.
func serveVMChange(ctx context.Context, t *testing.T, change vmChange) *httptest.ResponseRecorder {
	t.Helper()

	ctx = context.WithValue(ctx, middleware.ContextKeyRole, contractAdmin())
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "alice")

	req := httptest.NewRequestWithContext(ctx, change.method, change.path, strings.NewReader(change.body))
	exp := change.exp
	if exp == "" {
		exp = "exp"
	}

	req = mux.SetURLVars(req, map[string]string{"exp": exp, "name": "a", "snapshot": change.snapshot})

	var (
		rec  = httptest.NewRecorder()
		done = make(chan struct{})
	)

	go func() {
		defer close(done)

		change.handler.ServeHTTP(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s %s kept waiting for a stuck minimega", change.method, change.path)
	}

	return rec
}

// sentBesidesHold is what minimega received other than the hold and the
// files directory lookup.
func sentBesidesHold(received func() []mmtest.Command) []string {
	return slices.DeleteFunc(mmtest.Bases(received()), func(base string) bool { return base == "args" || base == "hold" })
}

// A request changing a VM gives up at its deadline while minimega is stuck, answering an error rather than holding
// its handler for the 10-minute wait default, and sends nothing.
func TestVMChangesGiveUpAtTheirDeadline(t *testing.T) {
	for name, change := range vmChanges() {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))
			shortenChanges(t, 50*time.Millisecond)

			received := holdMinimega(t)
			start := time.Now()
			rec := serveVMChange(t.Context(), t, change)

			if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
				t.Errorf("%s %s answered after %s, before its deadline, so not waiting for minimega",
					change.method, change.path, elapsed)
			}

			if rec.Code < http.StatusInternalServerError {
				t.Errorf("%s %s with minimega stuck = %d, want a server error", change.method, change.path, rec.Code)
			}

			if diff := cmp.Diff([]string(nil), sentBesidesHold(received), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s %s minimega commands mismatch (-want +got):\n%s", change.method, change.path, diff)
			}
		})
	}
}

// A request changing a VM whose client leaves before its first change is sent gives up, writes nothing and leaves
// the VM as it was.
func TestVMChangesGiveUpWhenTheClientLeavesFirst(t *testing.T) {
	for name, change := range vmChanges() {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))

			received := holdMinimega(t)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if rec := serveVMChange(ctx, t, change); rec.Body.Len() != 0 {
				t.Errorf("%s %s wrote %q (status %d) to a client that left, want nothing",
					change.method, change.path, rec.Body.String(), rec.Code)
			}

			if diff := cmp.Diff([]string(nil), sentBesidesHold(received), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s %s minimega commands mismatch (-want +got):\n%s", change.method, change.path, diff)
			}
		})
	}
}

// patchTwoVMs is a PATCH of the VMs moving vm-00 and vm-01 of the running experiment exp to VLAN EXP.
func patchTwoVMs(exp string) vmChange {
	return vmChange{
		handler: UpdateVMs,
		method:  http.MethodPatch,
		path:    "/api/v1/experiments/" + exp + "/vms",
		body: `{"vms":[{"name":"vm-00","interface":{"index":0,"vlan":"EXP"}},` +
			`{"name":"vm-01","interface":{"index":0,"vlan":"EXP"}}]}`,
		exp: exp,
	}
}

// connected lists the VMs minimega was told to connect to a VLAN, in order.
func connected(received func() []mmtest.Command) []string {
	var vms []string

	for _, base := range mmtest.Bases(received()) {
		if rest, ok := strings.CutPrefix(base, "vm net connect "); ok {
			vms = append(vms, strings.Fields(rest)[0])
		}
	}

	return vms
}

// A PATCH of the VMs whose client left before the first VM changed still changes every VM.
func TestUpdateVMsFinishWhenTheClientLeaves(t *testing.T) {
	exp := routeExperiment(t, "patch", 2, true)
	useTestStore(t, exp)

	cluster := &routeCluster{head: "head", vms: map[string]routeVMs{exp.Metadata.Name: {count: 2, host: "head"}}}
	received := useRouteCluster(t, cluster)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	serveVMChange(ctx, t, patchTwoVMs(exp.Metadata.Name))

	if diff := cmp.Diff([]string{"vm-00", "vm-01"}, connected(received)); diff != "" {
		t.Errorf("VMs connected mismatch (-want +got):\n%s", diff)
	}
}

// A PATCH of the VMs that outlasts its deadline leaves the VMs it has not reached yet as they were, and lists them
// as failed beside the VMs it updated.
func TestUpdateVMsStopAtTheirDeadline(t *testing.T) {
	exp := routeExperiment(t, "patch", 2, true)
	useTestStore(t, exp)
	shortenChanges(t, 50*time.Millisecond)

	cluster := &routeCluster{head: "head", vms: map[string]routeVMs{exp.Metadata.Name: {count: 2, host: "head"}}}
	useRouteCluster(t, cluster)

	// reading vm-00 back outlasts the deadline, which so passes before vm-01 changes
	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "vm info" {
			time.Sleep(200 * time.Millisecond)
		}

		return cluster.reply(cmd)
	})

	rec := serveVMChange(t.Context(), t, patchTwoVMs(exp.Metadata.Name))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH of the VMs past its deadline = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if diff := cmp.Diff([]string{"vm-00"}, connected(received)); diff != "" {
		t.Errorf("VMs connected mismatch (-want +got):\n%s", diff)
	}

	var list proto.VMList
	if err := protojson.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("PATCH of the VMs past its deadline = %s, not a VM list: %v", rec.Body.String(), err)
	}

	failed := make([]string, 0, len(list.GetErrors()))
	for _, e := range list.GetErrors() {
		failed = append(failed, e.GetName()+" "+e.GetStatus())
	}

	if diff := cmp.Diff([]string{"vm-01 failed"}, failed); diff != "" {
		t.Errorf("PATCH of the VMs past its deadline errors mismatch (-want +got):\n%s", diff)
	}
}

func TestScreenshotContextKeepsTheDeadlineButNotTheCancel(t *testing.T) {
	deadline := time.Now().Add(time.Hour)

	change, cancel := context.WithDeadline(context.Background(), deadline)
	cancel()

	ctx, stop := screenshotContext(change)
	defer stop()

	if err := ctx.Err(); err != nil {
		t.Errorf("screenshot context ended with its canceled change: %v", err)
	}

	got, ok := ctx.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Errorf("screenshot context deadline = %v, %t; want %v", got, ok, deadline)
	}

	unbounded, stopUnbounded := screenshotContext(context.Background())
	defer stopUnbounded()

	if _, ok := unbounded.Deadline(); ok {
		t.Error("screenshot context of a change without a deadline has one")
	}
}
