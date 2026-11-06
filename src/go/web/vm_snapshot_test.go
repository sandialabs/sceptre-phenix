package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"
	gwebsocket "github.com/gorilla/websocket"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// startedBy counts the goroutines that a function of the named handler started and that still run.
func startedBy(handler string) int {
	buf := make([]byte, 1<<20)

	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]

			break
		}

		buf = make([]byte, 2*len(buf))
	}

	return bytes.Count(buf, []byte("created by phenix/web."+handler+" "))
}

// A commit or memory snapshot that fails before it reports progress leaves no progress goroutine behind.
func TestVMChangesThatFailEarlyLeaveNoProgressGoroutine(t *testing.T) {
	changes := vmChanges()

	for name, handler := range map[string]string{"commit": "CommitVM", "memory snapshot": "CreateVMMemorySnapshot"} {
		t.Run(name, func(t *testing.T) {
			change := changes[name]

			// no experiment is stored, so the change fails before its first progress report
			useTestStore(t)
			useTestMM(t, new(testMM))

			if rec := serveVMChange(t.Context(), t, change); rec.Code < http.StatusBadRequest {
				t.Fatalf("%s %s of a missing experiment = %d, want an error", change.method, change.path, rec.Code)
			}

			left := startedBy(handler)
			for deadline := time.Now().Add(2 * time.Second); left != 0 && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)

				left = startedBy(handler)
			}

			if diff := cmp.Diff(0, left); diff != "" {
				t.Errorf("%s goroutines left after it returned mismatch (-want +got):\n%s", handler, diff)
			}
		})
	}
}

// vmSnapshotServe serves change as alice with role, waiting for its answer.
func vmSnapshotServe(t *testing.T, change vmChange, role rbac.Role) *httptest.ResponseRecorder {
	t.Helper()

	ctx := context.WithValue(t.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "alice")

	body := change.body
	req := httptest.NewRequestWithContext(ctx, change.method, change.path, strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"exp": "exp", "name": "a", "snapshot": change.snapshot})

	rec := httptest.NewRecorder()
	change.handler.ServeHTTP(rec, req)

	return rec
}

// vmSnapshotChanges are the snapshot routes' requests for VM a of exp, by name.
func vmSnapshotChanges() map[string]vmChange {
	changes := vmChanges()

	return map[string]vmChange{
		"list":            {handler: GetVMSnapshots, method: http.MethodGet, path: "/api/v1/experiments/exp/vms/a/snapshots"},
		"snapshot":        changes["snapshot"],
		"restore":         changes["restore"],
		"commit":          changes["commit"],
		"memory snapshot": changes["memory snapshot"],
	}
}

// vmSnapshotBroadcasts runs the broker for the rest of the test with a client
// that may see everything, and returns a function listing the "<type> <action>"
// of the VM broadcasts it has got so far.
func vmSnapshotBroadcasts(t *testing.T) func() []string {
	t.Helper()

	list := brokerBroadcasts(t, "experiment/vm/")

	return func() []string {
		broadcasts := list()
		got := make([]string, 0, len(broadcasts))

		for _, b := range broadcasts {
			fields := strings.Fields(b)
			got = append(got, fields[0]+" "+fields[len(fields)-1])
		}

		return got
	}
}

// brokerBroadcasts runs the broker for the rest of the test with a client that
// may see everything, and returns a function listing the "<type> <name>
// <action>" of the broadcasts of a type starting with prefix it has got so far.
func brokerBroadcasts(t *testing.T, prefix string) func() []string {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		broker.Start(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		broker.ServeWS(w, r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyRole, adminRole())))
	}))
	t.Cleanup(server.Close)

	conn, resp, err := gwebsocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing the broker: %v", err)
	}

	resp.Body.Close()
	t.Cleanup(func() { conn.Close() })

	var (
		mu     sync.Mutex
		got    []string
		probed atomic.Bool
	)

	go func() {
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}

			// the broker may send several messages in one frame
			for dec := json.NewDecoder(bytes.NewReader(b)); ; {
				var m struct {
					Resource struct {
						Type   string `json:"type"`
						Name   string `json:"name"`
						Action string `json:"action"`
					} `json:"resource"`
				}

				if dec.Decode(&m) != nil {
					break
				}

				// a probe only shows the client registered; it may arrive late
				if m.Resource.Type == "experiment/vm/probe" {
					probed.Store(true)
				} else if strings.HasPrefix(m.Resource.Type, prefix) {
					mu.Lock()
					got = append(got, m.Resource.Type+" "+m.Resource.Name+" "+m.Resource.Action)
					mu.Unlock()
				}
			}
		}
	}()

	list := func() []string {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(got)
	}

	// the client registers at some point: probe until it gets a broadcast
	polltest.Until(t, "the broker client getting broadcasts", func() bool {
		broker.Broadcast(nil, bt.NewResource("experiment/vm/probe", "exp/a", "probe"), nil)
		time.Sleep(5 * time.Millisecond)

		return probed.Load()
	})

	return list
}

// vmSnapshotWaitBroadcasts waits until list has got want, failing the test
// with the difference if it does not within a few seconds.
func vmSnapshotWaitBroadcasts(t *testing.T, list func() []string, want []string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for len(list()) < len(want) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if diff := cmp.Diff(want, list(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("VM broadcasts mismatch (-want +got):\n%s", diff)
	}
}

// vmSnapshotFiles lists an experiment's snapshots, or fails with err. Other
// file.ClusterFiles methods panic.
type vmSnapshotFiles struct {
	file.ClusterFiles

	snapshots []string
	err       error
}

func (f *vmSnapshotFiles) GetExperimentSnapshots(string) ([]string, error) {
	return f.snapshots, f.err
}

// useVMSnapshotFiles makes the cluster list snapshots, or fail with err, for
// the rest of the test.
func useVMSnapshotFiles(t *testing.T, err error, snapshots ...string) {
	t.Helper()

	original := file.DefaultClusterFiles
	file.DefaultClusterFiles = &vmSnapshotFiles{snapshots: snapshots, err: err} //nolint:reassign // test double

	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore test double
}

// vmSnapshotMM is the test minimega, also running mesh shell commands, which
// it records.
type vmSnapshotMM struct {
	*testMM

	mu     sync.Mutex
	shells []string
}

func (m *vmSnapshotMM) MeshShell(host, cmd string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.shells = append(m.shells, host+": "+cmd)

	return nil
}

// MeshShellResponse fails, as qemu-img does for a disk that is not there.
func (m *vmSnapshotMM) MeshShellResponse(_ context.Context, host, cmd string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.shells = append(m.shells, host+": "+cmd)

	return "", errors.New("qemu-img: Could not open the disk")
}

// vmSnapshotRunning is VM a of exp as minimega lists it, running.
func vmSnapshotRunning() mm.VMs {
	return mm.VMs{{Name: "a", UUID: "uuid-a", Running: true, State: "RUNNING"}}
}

// useVMSnapshotMM makes the test minimega serve the VMs of exp for the rest
// of the test.
func useVMSnapshotMM(t *testing.T, vms mm.VMs) *vmSnapshotMM {
	t.Helper()

	fake := &vmSnapshotMM{testMM: &testMM{vms: vms}}

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double

	return fake
}

// The snapshot routes refuse a role without their permission, auditing the
// refusal of each change.
func TestVMSnapshotRoutesRefuse(t *testing.T) {
	denied := map[string]string{
		"list": "", "snapshot": "vm-snapshot", "restore": "vm-restore", "commit": "vm-commit",
		"memory snapshot": "vm-memory-snapshot",
	}

	for name, change := range vmSnapshotChanges() {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			rec := vmSnapshotServe(t, change, testRole("vms", "*", "exp/a"))
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s without its permission = %d, want %d", change.method, change.path, rec.Code, http.StatusForbidden)
			}

			if got := errorMessage(t, rec.Code, rec.Body.String()); got != "forbidden" {
				t.Errorf("%s %s without its permission = %q, want forbidden", change.method, change.path, got)
			}

			if action := denied[name]; action != "" {
				wantDenied(t, events(), action)
			} else if got := events(); len(got) != 0 {
				t.Errorf("%s %s refused: audit events = %v, want none", change.method, change.path, got)
			}
		})
	}
}

// vmSnapshotBrokenBody is a request body whose read fails.
type vmSnapshotBrokenBody struct{}

func (vmSnapshotBrokenBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// A snapshot, commit or memory snapshot request with a body that cannot be
// read or is not the request is answered with the error, and leaves the VM
// unlocked.
func TestVMSnapshotRoutesRejectBadBodies(t *testing.T) {
	changes := vmSnapshotChanges()

	tests := []struct {
		change, body string
		broken       bool
		wantCode     int
		wantMessage  string
	}{
		{"snapshot", "", true, http.StatusInternalServerError, "connection reset"},
		{"snapshot", "{", false, http.StatusBadRequest, ""},
		{"commit", "", true, http.StatusInternalServerError, "connection reset"},
		{"commit", "{", false, http.StatusBadRequest, ""},
		{"commit", "{}", false, http.StatusBadRequest, "missing 'filename' key"},
		{"commit", "", false, http.StatusBadRequest, "must provide new disk name for commit"},
		{"memory snapshot", "", true, http.StatusInternalServerError, "connection reset"},
		{"memory snapshot", "{", false, http.StatusBadRequest, ""},
		{"memory snapshot", `{"filename":""}`, false, http.StatusBadRequest, "missing 'filename' key"},
		{"memory snapshot", "", false, http.StatusBadRequest, "must provide new disk name for memory snapshot"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s %q broken %t", tc.change, tc.body, tc.broken), func(t *testing.T) {
			change := changes[tc.change]

			ctx := context.WithValue(t.Context(), middleware.ContextKeyRole, adminRole())

			var body io.Reader = strings.NewReader(tc.body)
			if tc.broken {
				body = vmSnapshotBrokenBody{}
			}

			req := httptest.NewRequestWithContext(ctx, change.method, change.path, body)
			req = mux.SetURLVars(req, map[string]string{"exp": "exp", "name": "a"})

			rec := httptest.NewRecorder()
			change.handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("%s %s with body %q = %d, want %d", change.method, change.path, tc.body, rec.Code, tc.wantCode)
			}

			if got := errorMessage(t, rec.Code, rec.Body.String()); tc.wantMessage != "" && got != tc.wantMessage {
				t.Errorf("%s %s with body %q = %q, want %q", change.method, change.path, tc.body, got, tc.wantMessage)
			}

			if err := cache.LockVMForSnapshotting("exp", "a"); err != nil {
				t.Errorf("locking VM a after a bad request: %v, want it unlocked", err)
			} else {
				cache.UnlockVM("exp", "a")
			}
		})
	}
}

// A change to a VM that another change holds is refused with 409.
func TestVMSnapshotRoutesConflict(t *testing.T) {
	if err := cache.LockVMForStarting("exp", "a"); err != nil {
		t.Fatalf("cache.LockVMForStarting(exp, a) error = %v", err)
	}

	t.Cleanup(func() { cache.UnlockVM("exp", "a") })

	for name, change := range vmSnapshotChanges() {
		if name == "list" {
			continue
		}

		t.Run(name, func(t *testing.T) {
			if rec := vmSnapshotServe(t, change, adminRole()); rec.Code != http.StatusConflict {
				t.Errorf("%s %s while VM a starts = %d, want %d: %s",
					change.method, change.path, rec.Code, http.StatusConflict, rec.Body.String())
			}
		})
	}
}

// The snapshot list holds only the VM's own snapshots; a listing that fails
// is answered with its error, or nothing once the client has left.
func TestGetVMSnapshots(t *testing.T) {
	list := vmSnapshotChanges()["list"]

	useVMExperiment(t, false, "a")
	useVMSnapshotFiles(t, nil, "a__s1", "ab__s2", "b__a__s3", "a__s4", "a_s5")

	rec := vmSnapshotServe(t, list, adminRole())
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d: %s", list.path, rec.Code, http.StatusOK, rec.Body.String())
	}

	var got struct {
		Snapshots []string `json:"snapshots"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET %s = %s, not a snapshot list: %v", list.path, rec.Body.String(), err)
	}

	if diff := cmp.Diff([]string{"a__s1", "a__s4"}, got.Snapshots); diff != "" {
		t.Errorf("GET %s snapshots mismatch (-want +got):\n%s", list.path, diff)
	}

	useVMSnapshotFiles(t, errors.New("mesh unreachable"))

	rec = vmSnapshotServe(t, list, adminRole())
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("GET %s with the listing failing = %d, want %d", list.path, rec.Code, http.StatusInternalServerError)
	}

	if got := errorMessage(t, rec.Code, rec.Body.String()); !strings.Contains(got, "mesh unreachable") {
		t.Errorf("GET %s with the listing failing = %q, want the error", list.path, got)
	}

	useVMSnapshotFiles(t, fmt.Errorf("file list: %w", context.Canceled))

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), middleware.ContextKeyRole, adminRole()))
	cancel()

	req := mux.SetURLVars(httptest.NewRequestWithContext(ctx, http.MethodGet, list.path, nil),
		map[string]string{"exp": "exp", "name": "a"})

	left := httptest.NewRecorder()
	list.handler.ServeHTTP(left, req)

	if left.Body.Len() != 0 {
		t.Errorf("GET %s whose client left wrote %q, want nothing", list.path, left.Body.String())
	}
}

// A change that fails is answered with its error, told to the other clients
// and audited as failed: 409 for a VM that is not running, 404 for a missing
// snapshot, and else 500.
func TestVMSnapshotChangesFail(t *testing.T) {
	changes := vmSnapshotChanges()

	tests := map[string]struct {
		change  vmChange
		vms     mm.VMs
		handle  mmtest.Handler
		status  int // 500 when 0
		message string
		want    []string
		action  string
	}{
		"snapshot of a VM that is not running": {
			change:  changes["snapshot"],
			status:  http.StatusConflict,
			message: "VM a is not running",
			want:    []string{"experiment/vm/snapshot creating", "experiment/vm/snapshot errorCreating"},
			action:  "vm-snapshot",
		},
		"restore of a missing snapshot": {
			change: vmChange{
				handler: RestoreVM, method: http.MethodPost, path: "/api/v1/experiments/exp/vms/a/snapshots/a__s9",
				snapshot: "a__s9",
			},
			status:  http.StatusNotFound,
			message: "snapshot not found for VM a",
			want:    []string{"experiment/vm/snapshot restoring", "experiment/vm/snapshot errorRestoring"},
			action:  "vm-restore",
		},
		"commit of a missing disk": {
			change:  changes["commit"],
			vms:     vmSnapshotRunning(),
			message: "qemu-img: Could not open the disk",
			want:    []string{"experiment/vm/commit committing", "experiment/vm/commit errorCommitting"},
			action:  "vm-commit",
		},
		"memory snapshot that fails": {
			change: changes["memory snapshot"],
			vms:    vmSnapshotRunning(),
			handle: func(cmd mmtest.Command) []*minicli.Response {
				switch {
				case strings.HasPrefix(cmd.Base, "vm info"):
					return []*minicli.Response{mmtest.Tabular("head", []string{"host", "name", "id", "state"},
						[]string{"head", "a", "3", "RUNNING"})}
				case strings.Contains(cmd.Base, "query-dump"):
					return []*minicli.Response{mmtest.Text("head", `{"return":{"status":"failed"}}`)}
				}

				return nil
			},
			message: "failed to create memory snapshot",
			want: []string{
				"experiment/vm/memorySnapshot committing", "experiment/vm/memorySnapshot errorCommitting",
			},
			action: "vm-memory-snapshot",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "missing.qc2")))
			useVMSnapshotFiles(t, nil, "a__s1")
			useVMSnapshotMM(t, tc.vms)
			mmtest.Use(t, tc.handle)

			broadcasts := vmSnapshotBroadcasts(t)
			events := captureAudit(t)

			status := tc.status
			if status == 0 {
				status = http.StatusInternalServerError
			}

			rec := vmSnapshotServe(t, tc.change, adminRole())
			if rec.Code != status {
				t.Fatalf("%s %s = %d, want %d: %s", tc.change.method, tc.change.path, rec.Code,
					status, rec.Body.String())
			}

			if got := errorMessage(t, rec.Code, rec.Body.String()); !strings.Contains(got, tc.message) {
				t.Errorf("%s %s = %q, want it to contain %q", tc.change.method, tc.change.path, got, tc.message)
			}

			vmSnapshotWaitBroadcasts(t, broadcasts, tc.want)
			wantAudit(t, events(), tc.action, "failure", map[string]any{"user.name": "alice", "phenix.vm.name": "a"})
		})
	}
}

// A snapshot saves the VM's memory and disk, reporting its progress, moves
// both into the experiment's files and resumes the VM.
func TestSnapshotVM(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))

	fake := useVMSnapshotMM(t, vmSnapshotRunning())

	var polls atomic.Int32

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case strings.HasPrefix(cmd.Base, "vm info"):
			return []*minicli.Response{mmtest.Tabular("compute1", []string{"host", "id"}, []string{"compute1", "3"})}
		case cmd.Base == "vm save":
			status, done := "saving", "50"
			if polls.Add(1) > 1 {
				status, done = "completed", "100"
			}

			return []*minicli.Response{mmtest.Tabular("compute1", []string{"name", "status", "complete (%)"},
				[]string{"a", status, done})}
		}

		return nil
	})

	broadcasts := vmSnapshotBroadcasts(t)
	events := captureAudit(t)

	rec := vmSnapshotServe(t, vmSnapshotChanges()["snapshot"], adminRole())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST snapshots = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	vmSnapshotWaitBroadcasts(t, broadcasts, []string{
		"experiment/vm/snapshot creating", "experiment/vm/snapshot progress", "experiment/vm/snapshot create",
	})

	wantAudit(t, events(), "vm-snapshot", "success", map[string]any{"user.name": "alice", "phenix.vm.name": "a"})

	if diff := cmp.Diff([]string{"vm save a exp_a__s1", "vm save", "vm save", "vm start a"},
		slices.DeleteFunc(mmtest.Bases(received()), func(b string) bool { return strings.HasPrefix(b, "vm info") || b == "args" }),
	); diff != "" {
		t.Errorf("minimega commands mismatch (-want +got):\n%s", diff)
	}

	files := common.PhenixBase + "/images/exp/files"
	want := []string{
		"compute1: mkdir -p " + files,
		"compute1: mv " + common.PhenixBase + "/images/exp_a__s1.state " + files + "/a__s1.state",
		"compute1: mv " + common.PhenixBase + "/images/exp_a__s1.hdd " + files + "/a__s1.hdd",
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()

	if diff := cmp.Diff(want, fake.shells); diff != "" {
		t.Errorf("mesh shell commands mismatch (-want +got):\n%s", diff)
	}
}

// A restore relaunches the VM from the snapshot's state and disk, keeping
// its UUID.
func TestRestoreVM(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))
	useVMSnapshotFiles(t, nil, "a__s1", "a__s2")
	useVMSnapshotMM(t, vmSnapshotRunning())

	received := mmtest.Use(t, nil)
	broadcasts := vmSnapshotBroadcasts(t)
	events := captureAudit(t)

	restore := vmChange{
		handler: RestoreVM, method: http.MethodPost, path: "/api/v1/experiments/exp/vms/a/snapshots/a__s1.state",
		snapshot: "a__s1.state",
	}

	rec := vmSnapshotServe(t, restore, adminRole())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST snapshots/a__s1.state = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	vmSnapshotWaitBroadcasts(t, broadcasts, []string{"experiment/vm/snapshot restoring", "experiment/vm/snapshot restore"})
	wantAudit(t, events(), "vm-restore", "success", map[string]any{"user.name": "alice", "phenix.vm.name": "a"})

	want := []string{
		"vm config clone a", "vm config uuid uuid-a", "vm config state exp/files/a__s1.state",
		"vm config disk exp/files/a__s1.hdd,writeback", "vm kill a", "vm flush a", "vm launch kvm a", "vm launch",
		"vm start a",
	}

	if diff := cmp.Diff(want, slices.DeleteFunc(mmtest.Bases(received()), func(b string) bool { return b == "args" })); diff != "" {
		t.Errorf("minimega commands mismatch (-want +got):\n%s", diff)
	}
}

// A memory snapshot dumps the VM's memory into the experiment's files,
// reporting its progress.
func TestCreateVMMemorySnapshot(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))
	useVMSnapshotMM(t, vmSnapshotRunning())

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case strings.HasPrefix(cmd.Base, "vm info"):
			return []*minicli.Response{mmtest.Tabular("head", []string{"host", "name", "id", "state"},
				[]string{"headnode", "a", "3", "RUNNING"})}
		case strings.Contains(cmd.Base, "query-dump"):
			return []*minicli.Response{mmtest.Text("head", `{"return":{"status":"completed","completed":8,"total":8}}`)}
		}

		return nil
	})

	broadcasts := vmSnapshotBroadcasts(t)
	events := captureAudit(t)

	rec := vmSnapshotServe(t, vmSnapshotChanges()["memory snapshot"], adminRole())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST memorySnapshot = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	vmSnapshotWaitBroadcasts(t, broadcasts, []string{
		"experiment/vm/memorySnapshot committing", "experiment/vm/memorySnapshot progress",
		"experiment/vm/memorySnapshot commit",
	})

	wantAudit(t, events(), "vm-memory-snapshot", "success", map[string]any{
		"user.name": "alice", "phenix.vm.name": "a",
	})

	out := common.PhenixBase + "/images/exp/files/m1.elf"

	// sent to the headnode, so with no mesh send before shell
	if n := mmtest.Count(received(), " shell mkdir -p "+filepath.Dir(out)); n != 1 {
		t.Errorf("minimega mkdir of the files directory commands = %d, want 1: %v", n, mmtest.Bases(received()))
	}

	if n := mmtest.Count(received(), `vm qmp a '{ "execute": "dump-guest-memory", "arguments": { "protocol": "file:`+out); n != 1 {
		t.Errorf("minimega memory dumps to %s = %d, want 1: %v", out, n, mmtest.Bases(received()))
	}
}

// vmSnapshotRemoteMM is the test minimega for a cluster whose VMs run on
// nodes other than the headnode.
type vmSnapshotRemoteMM struct {
	*testMM
}

func (vmSnapshotRemoteMM) IsHeadnode(string) bool { return false }

// A memory snapshot to a path that the VM's node cannot write for the
// headnode to get is a bad request, audited as failed, and sends no dump.
func TestCreateVMMemorySnapshotRejectsBadPaths(t *testing.T) {
	tests := map[string]struct {
		filename string
		remote   bool
	}{
		"name that minimega splits":                 {filename: "m 1"},
		"remote VM's dump outside the files folder": {filename: "/tmp/x.elf", remote: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))

			host := "headnode"
			if tc.remote {
				host = "compute1"

				original := mm.DefaultMM
				t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

				mm.DefaultMM = vmSnapshotRemoteMM{testMM: &testMM{vms: vmSnapshotRunning()}} //nolint:reassign // test double
			} else {
				useVMSnapshotMM(t, vmSnapshotRunning())
			}

			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if strings.HasPrefix(cmd.Base, "vm info") {
					return []*minicli.Response{mmtest.Tabular(host, []string{"host", "name", "id", "state"},
						[]string{host, "a", "3", "RUNNING"})}
				}

				return nil
			})

			broadcasts := vmSnapshotBroadcasts(t)
			events := captureAudit(t)

			change := vmSnapshotChanges()["memory snapshot"]
			body, err := json.Marshal(map[string]string{"filename": tc.filename})
			if err != nil {
				t.Fatalf("marshaling body: %v", err)
			}

			change.body = string(body)

			rec := vmSnapshotServe(t, change, adminRole())
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST memorySnapshot %s = %d, want %d: %s", body, rec.Code, http.StatusBadRequest,
					rec.Body.String())
			}

			vmSnapshotWaitBroadcasts(t, broadcasts, []string{
				"experiment/vm/memorySnapshot committing", "experiment/vm/memorySnapshot errorCommitting",
			})
			wantAudit(t, events(), "vm-memory-snapshot", "failure", map[string]any{
				"user.name": "alice", "phenix.vm.name": "a",
			})

			if n := mmtest.Count(received(), "vm qmp"); n != 0 {
				t.Errorf("POST memorySnapshot %s sent %d QMP commands, want 0: %v", body, n, mmtest.Bases(received()))
			}
		})
	}
}
