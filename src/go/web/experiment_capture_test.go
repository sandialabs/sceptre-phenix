package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/websocket"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/testing/protocmp"

	expcapture "phenix/api/capture"
	"phenix/api/experiment"
	"phenix/api/livecapture"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/web/capability"
	"phenix/web/lifecycle"
	"phenix/web/proto"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

const experimentCapturesPath = "/api/v1/experiments/test-experiment/captures"

var errBoom = errors.New("boom")

// useCaptureSupport gives the test minimega's support of experiment captures: forced on, or not known, as before
// minimega answers.
func useCaptureSupport(t *testing.T, available bool) {
	t.Helper()

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	mm.SetCaptureFeaturesForced(available)
	t.Cleanup(func() { mm.SetCaptureFeaturesForced(false) })
}

// captureRole may do what verbs name on test-experiment's experiment captures.
func captureRole(verbs ...string) rbac.Role {
	roles := make([]rbac.Role, 0, len(verbs))
	for _, verb := range verbs {
		roles = append(roles, testRole("experiments/captures", verb, "test-experiment"))
	}

	return combinedRole(roles...)
}

// useCapturedExperiment stores test-experiment, running unless stopped, with test-vm on compute1, and returns the
// minimega that runs it with the experiment captures given.
func useCapturedExperiment(t *testing.T, stopped bool, captures ...mm.NamespaceCapture) *testMM {
	t.Helper()

	started := "2024-01-01T00:00:00Z"
	if stopped {
		started = ""
	}

	useTestStore(t, testExperiment(t, started, captureNode("test-vm")))

	return useTestMM(t, &testMM{
		vms:        mm.VMs{{Name: "test-vm", Host: "compute1", Running: true, Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}}},
		nsCaptures: captures,
		startedID:  "4",
	})
}

// mirrors are count Open vSwitch mirrors of the namespace ns on compute1's phenix bridge.
func mirrors(ns string, count int) []mm.BridgeMirror {
	m := make([]mm.BridgeMirror, count)
	for i := range m {
		m[i] = mm.BridgeMirror{Host: "compute1", Bridge: "phenix", Mirror: "m", Output: "o", Namespace: ns, Kind: "capture"}
	}

	return m
}

func TestStartExperimentCapture(t *testing.T) {
	starting := mm.NamespaceCapture{
		ID: "4", State: "starting", Path: "/phenix/images/test-experiment/files/x.pcapng", Format: "pcapng",
	}

	tests := map[string]struct {
		role      rbac.Role
		body      string
		stopped   bool
		supported bool
		mirrors   []mm.BridgeMirror
		want      int
		// the warnings of a start
		wantWarnings []string
		// the audit event's outcome, "" for none
		wantAudit string
	}{
		"starts a capture": {
			role: captureRole("create"), body: `{"name":"x"}`, supported: true, want: http.StatusCreated,
			wantWarnings: []string{}, wantAudit: "success",
		},
		"starts a capture and warns of the mirror budget": {
			role: captureRole("create"), body: `{"name":"x"}`, supported: true, mirrors: mirrors("test-experiment", 17),
			want: http.StatusCreated, wantAudit: "success",
			wantWarnings: []string{"test-experiment uses 18 of 32 mirrors on compute1 / phenix"},
		},
		"refuses a capture past the mirror budget": {
			role: captureRole("create"), body: `{}`, supported: true, mirrors: mirrors("other", 32),
			want: http.StatusConflict, wantAudit: "failure",
		},
		"refuses a filter with a quote": {
			role: captureRole("create"), body: `{"filter":"host \"a\""}`, supported: true,
			want: http.StatusBadRequest, wantAudit: "failure",
		},
		"refuses a VLAN of no experiment": {
			role: captureRole("create"), body: `{"vlans":["EXP_9"]}`, supported: true,
			want: http.StatusBadRequest, wantAudit: "failure",
		},
		"refuses a body that does not decode": {
			role: captureRole("create"), body: `{`, supported: true, want: http.StatusBadRequest,
		},
		"refuses a stopped experiment": {
			role: captureRole("create"), body: `{}`, supported: true, stopped: true,
			want: http.StatusConflict, wantAudit: "failure",
		},
		"refuses without minimega's support": {
			role: captureRole("create"), body: `{}`, want: http.StatusNotImplemented, wantAudit: "failure",
		},
		"refuses a role that may only list": {
			role: captureRole("list", "get"), body: `{}`, supported: true, want: http.StatusForbidden, wantAudit: "failure",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureSupport(t, tc.supported)

			fake := useCapturedExperiment(t, tc.stopped)
			fake.mirrors, fake.nsStarted = tc.mirrors, []mm.NamespaceCapture{starting}

			server := serveAs(t, "alice", tc.role).URL
			events := captureAudit(t)
			broadcasts := brokerBroadcasts(t, "experiment/capture")

			resp := do(t, http.MethodPost, server+experimentCapturesPath, tc.body)
			body := readBody(t, resp)

			if resp.StatusCode != tc.want {
				t.Fatalf("POST %s %s = %d, want %d: %s", experimentCapturesPath, tc.body, resp.StatusCode, tc.want, body)
			}

			wantStarts := int32(0)
			if tc.want == http.StatusCreated {
				wantStarts = 1
			}

			if n := fake.nsStarts.Load(); n != wantStarts {
				t.Errorf("POST %s %s started %d experiment captures, want %d", experimentCapturesPath, tc.body, n, wantStarts)
			}

			if tc.wantAudit != "" {
				wantAudit(t, events(), "capture-start", tc.wantAudit, map[string]any{
					"user.name": "alice", "phenix.experiment.name": "test-experiment",
				})
			}

			if tc.want != http.StatusCreated {
				return
			}

			var got proto.StartExperimentCaptureResponse
			if err := protojson.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("POST %s = %s, not a started capture: %v", experimentCapturesPath, body, err)
			}

			if got.GetCapture().GetId() != "4" || got.GetCapture().GetFile() != "x.pcapng" || !got.GetCapture().GetRunning() {
				t.Errorf("POST %s capture = %v, want running capture 4 in x.pcapng", experimentCapturesPath, got.GetCapture())
			}

			if diff := cmp.Diff(tc.wantWarnings, got.GetWarnings(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("POST %s warnings mismatch (-want +got):\n%s", experimentCapturesPath, diff)
			}

			wantAudit(t, events(), "capture-start", "success", map[string]any{
				"phenix.capture.id": "4", "file.path": "x.pcapng",
			})

			waitBroadcasts(t, broadcasts, []string{"experiment/capture test-experiment/4 start"})
		})
	}
}

// waitBroadcasts waits until list has got want, failing the test with the difference if it does not within a few
// seconds.
func waitBroadcasts(t *testing.T, list func() []string, want []string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for len(list()) < len(want) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if diff := cmp.Diff(want, list()); diff != "" {
		t.Errorf("broadcasts mismatch (-want +got):\n%s", diff)
	}
}

// The experiment stop hook sends one stop event for all the experiment captures, only when minimega has them.
func TestExperimentStopStopsExperimentCaptures(t *testing.T) {
	broadcasts := brokerBroadcasts(t, "experiment/capture")

	useCaptureSupport(t, false)
	experimentCapturesStopped("stop", "old-minimega")

	useCaptureSupport(t, true)
	experimentCapturesStopped("stop", "test-experiment")

	waitBroadcasts(t, broadcasts, []string{"experiment/capture test-experiment/* stop"})
}

func TestGetAndStopExperimentCapture(t *testing.T) {
	running := mm.NamespaceCapture{
		ID: "3", State: "running", Path: "/phenix/images/test-experiment/files/c.pcapng", Format: "pcapng",
		VLANs: []string{"EXP_1"}, Start: "2026-11-05T14:22:33Z", Size: 2048,
		CaptureCounters: mm.CaptureCounters{Packets: 10, KDrops: 1, Lag: 80 * time.Millisecond},
		Streams: []mm.CaptureStream{
			{Host: "compute1", Bridge: "phenix", State: "running", CaptureCounters: mm.CaptureCounters{
				Packets: 6, Skew: 300 * time.Microsecond,
			}},
			{Host: "compute2", Bridge: "phenix", State: "running", CaptureCounters: mm.CaptureCounters{
				Packets: 4, Skew: -612 * time.Millisecond,
			}},
		},
	}

	want := &proto.ExperimentCapture{
		Id: "3", State: "running", Running: true, Path: running.Path, File: "c.pcapng", Format: "pcapng",
		Vlans: []string{"EXP_1"}, Start: "2026-11-05T14:22:33Z", Size: 2048,
		Counters: &proto.CaptureCounters{Packets: 10, Kdrops: 1, LagMs: 80},
		Streams: []*proto.CaptureStream{
			{Host: "compute1", Bridge: "phenix", State: "running", Counters: &proto.CaptureCounters{Packets: 6, SkewMs: 0.3}},
			{
				Host: "compute2", Bridge: "phenix", State: "running",
				Counters: &proto.CaptureCounters{Packets: 4, SkewMs: -612}, SkewWarning: true,
			},
		},
	}

	tests := map[string]struct {
		method, id string
		role       rbac.Role
		want       int
		wantBody   *proto.ExperimentCapture
		wantStops  int32
		wantAudit  string
	}{
		"get a capture": {method: http.MethodGet, id: "3", role: captureRole("get"), want: http.StatusOK, wantBody: want},
		"get an unknown capture": {
			method: http.MethodGet, id: "9", role: captureRole("get"), want: http.StatusNotFound,
		},
		"get refused to a role that may only list": {
			method: http.MethodGet, id: "3", role: captureRole("list"), want: http.StatusForbidden,
		},
		"stop a capture": {
			method: http.MethodDelete, id: "3", role: captureRole("delete"), want: http.StatusOK, wantStops: 1,
			wantAudit: "success",
		},
		"stop an unknown capture": {
			method: http.MethodDelete, id: "9", role: captureRole("delete"), want: http.StatusNotFound, wantAudit: "failure",
		},
		"stop refused to a role that may only get": {
			method: http.MethodDelete, id: "3", role: captureRole("get"), want: http.StatusForbidden, wantAudit: "failure",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureSupport(t, true)

			fake := useCapturedExperiment(t, false, running)
			server := serveAs(t, "alice", tc.role).URL
			events := captureAudit(t)

			path := experimentCapturesPath + "/" + tc.id

			resp := do(t, tc.method, server+path, "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.want {
				t.Fatalf("%s %s = %d, want %d: %s", tc.method, path, resp.StatusCode, tc.want, body)
			}

			if n := fake.nsStops.Load(); n != tc.wantStops {
				t.Errorf("%s %s stopped %d experiment captures, want %d", tc.method, path, n, tc.wantStops)
			}

			if tc.wantAudit != "" {
				wantAudit(t, events(), "capture-stop", tc.wantAudit, map[string]any{
					"user.name": "alice", "phenix.experiment.name": "test-experiment", "phenix.capture.id": tc.id,
				})
			}

			if tc.want != http.StatusOK {
				return
			}

			var got proto.ExperimentCapture
			if err := protojson.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("%s %s = %s, not an experiment capture: %v", tc.method, path, body, err)
			}

			if tc.method == http.MethodDelete {
				if got.GetState() != "stopped" || got.GetRunning() {
					t.Errorf("DELETE %s = state %q running %t, want the stopped capture", path, got.GetState(), got.GetRunning())
				}

				return
			}

			if diff := cmp.Diff(tc.wantBody, &got, protocmp.Transform()); diff != "" {
				t.Errorf("%s %s mismatch (-want +got):\n%s", tc.method, path, diff)
			}
		})
	}
}

// Stopping an experiment capture ends its stream grants; its WebShark grants go on, to show its file.
func TestStopExperimentCaptureRevokesStreamGrants(t *testing.T) {
	useCaptureSupport(t, true)
	useCaptureGrants(t)

	useCapturedExperiment(t, false, mm.NamespaceCapture{ID: "3", State: "running"})

	mint := func(token string, ops ...capability.Op) capability.Grant {
		return captureGrants.Mint(token, capability.Grant{
			User: "alice", Target: capability.Target{Exp: "test-experiment", Capture: "3", Live: true},
			Ops: ops, Expires: time.Now().Add(time.Hour),
		})
	}

	stream, view := mint("pcs_stream", capability.OpStream), mint("view.pcapng", viewOps...)

	server := serveAs(t, "alice", captureRole("delete")).URL

	if resp := do(t, http.MethodDelete, server+experimentCapturesPath+"/3", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE %s/3 = %d, want %d: %s", experimentCapturesPath, resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	select {
	case <-stream.Done:
	default:
		t.Error("the capture's stream grant after the stop is not revoked, want it revoked")
	}

	select {
	case <-view.Done:
		t.Error("the capture's WebShark grant after the stop is revoked, want it kept")
	default:
	}
}

func TestGetExperimentCapturesOfExperimentCaptures(t *testing.T) {
	listed := mm.NamespaceCapture{
		ID: "1", State: "stopped", Path: "/x/test-experiment/files/a.pcap", Format: "pcap",
	}

	tests := map[string]struct {
		supported bool
		stopped   bool
		nsErr     error
		mirrorErr error
		want      *proto.CaptureList
	}{
		"with minimega's support": {
			supported: true,
			want: &proto.CaptureList{
				ExperimentCaptures: []*proto.ExperimentCapture{{
					Id: "1", State: "stopped", Path: listed.Path, File: "a.pcap", Format: "pcap", Counters: &proto.CaptureCounters{},
				}},
				Support: &proto.CaptureSupport{Available: true},
				Budget: []*proto.MirrorBudget{
					{Host: "compute1", Bridge: "phenix", Total: 17, Experiment: 17, Limit: 32, Level: "half"},
				},
			},
		},
		// a budget that cannot be read does not fail the list
		"with a budget that cannot be read": {
			supported: true, mirrorErr: errBoom,
			want: &proto.CaptureList{
				ExperimentCaptures: []*proto.ExperimentCapture{{
					Id: "1", State: "stopped", Path: listed.Path, File: "a.pcap", Format: "pcap", Counters: &proto.CaptureCounters{},
				}},
				Support:     &proto.CaptureSupport{Available: true},
				BudgetError: "listing the Open vSwitch mirrors of compute1: boom",
			},
		},
		// experiment captures that cannot be listed do not fail the list
		"with experiment captures that cannot be listed": {
			supported: true, nsErr: errBoom,
			want: &proto.CaptureList{
				Support: &proto.CaptureSupport{Available: true},
				Budget: []*proto.MirrorBudget{
					{Host: "compute1", Bridge: "phenix", Total: 17, Experiment: 17, Limit: 32, Level: "half"},
				},
				ExperimentCapturesError: "listing the experiment captures of test-experiment: boom",
			},
		},
		"without minimega's support": {
			want: &proto.CaptureList{
				Support: &proto.CaptureSupport{Reason: "requires minimega 3.4.0 or later; minimega did not answer"},
			},
		},
		// minimega would make the namespace of a stopped experiment again
		"of a stopped experiment": {
			supported: true, stopped: true, want: &proto.CaptureList{Support: &proto.CaptureSupport{Available: true}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureSupport(t, tc.supported)

			fake := useCapturedExperiment(t, tc.stopped, listed)
			fake.mirrors, fake.mirrorErr, fake.nsErr = mirrors("test-experiment", 17), tc.mirrorErr, tc.nsErr

			server := serveAs(t, "alice", captureRole("list")).URL

			resp := do(t, http.MethodGet, server+experimentCapturesPath, "")
			body := readBody(t, resp)

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %d, want %d: %s", experimentCapturesPath, resp.StatusCode, http.StatusOK, body)
			}

			got := new(proto.CaptureList)
			if err := protojson.Unmarshal([]byte(body), got); err != nil {
				t.Fatalf("GET %s = %s, not a capture list: %v", experimentCapturesPath, body, err)
			}

			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("GET %s mismatch (-want +got):\n%s", experimentCapturesPath, diff)
			}
		})
	}
}

// The VM interface captures are listed when the experiment captures cannot be.
func TestGetExperimentCapturesKeepsVMCaptures(t *testing.T) {
	useCaptureSupport(t, true)

	fake := useCapturedExperiment(t, false)
	fake.captures = []mm.Capture{{VM: "test-vm", Interface: 0, Filepath: "/x/test-experiment/files/vm.pcap"}}
	fake.nsErr = errBoom

	resp := do(t, http.MethodGet, serveAs(t, "alice", adminRole()).URL+experimentCapturesPath, "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d: %s", experimentCapturesPath, resp.StatusCode, http.StatusOK, body)
	}

	got := new(proto.CaptureList)
	if err := protojson.Unmarshal([]byte(body), got); err != nil {
		t.Fatalf("GET %s = %s, not a capture list: %v", experimentCapturesPath, body, err)
	}

	if len(got.GetCaptures()) != 1 || got.GetExperimentCapturesError() == "" {
		t.Errorf("GET %s = %d VM captures, experimentCapturesError %q; want 1 VM capture and an error",
			experimentCapturesPath, len(got.GetCaptures()), got.GetExperimentCapturesError())
	}
}

func TestGetExperimentCaptureStream(t *testing.T) {
	useCaptureSupport(t, true)

	fake := useWebShark(t)
	live := useLiveCapture(t, fake)
	fake.nsCaptures = []mm.NamespaceCapture{{ID: "3", State: "running", Path: live.path, Format: "pcap"}}

	header, packet := testPcapHeader(), testPcapRecord(60)
	live.write(t, header, packet)

	reader := combinedRole(captureRole("get"), testRole("experiments/files", "get", "test-experiment"))

	for transport, overWebSocket := range map[string]bool{"a websocket": true, "HTTP": false} {
		t.Run("streams the capture over "+transport, func(t *testing.T) {
			events := captureAudit(t)
			expect := openCaptureStream(t, serveAs(t, "alice", reader).URL+experimentCapturesPath+"/3/stream", overWebSocket)

			expect(header, "the file header")
			expect(packet, "the packet")

			wantAudit(t, events(), "capture-stream", "success", map[string]any{
				"user.name": "alice", "phenix.experiment.name": "test-experiment", "phenix.capture.id": "3",
			})
		})
	}

	t.Run("refuses a websocket while the server stops", func(t *testing.T) {
		lifecycle.Stop()
		t.Cleanup(lifecycle.Start)

		url := "ws" + strings.TrimPrefix(serveAs(t, "alice", reader).URL, "http") + experimentCapturesPath + "/3/stream"

		_, resp, err := websocket.DefaultDialer.DialContext(t.Context(), url, nil)
		if err == nil || resp == nil {
			t.Fatalf("dialing %s while the server stops error = %v, want a refused handshake", url, err)
		}

		resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != lifecycle.RetryAfter {
			t.Errorf("dialing %s while the server stops = %d Retry-After %q, want %d %q", url, resp.StatusCode,
				resp.Header.Get("Retry-After"), http.StatusServiceUnavailable, lifecycle.RetryAfter)
		}
	})

	refusals := map[string]struct {
		role rbac.Role
		id   string
		want int
	}{
		"without the files permission": {role: captureRole("get"), id: "3", want: http.StatusForbidden},
		"without the get permission": {
			role: testRole("experiments/files", "get", "test-experiment"), id: "3", want: http.StatusForbidden,
		},
		"of a capture that does not run": {role: reader, id: "9", want: http.StatusNotFound},
	}

	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)
			path := experimentCapturesPath + "/" + tc.id + "/stream"

			resp := do(t, http.MethodGet, serveAs(t, "alice", tc.role).URL+path, "")
			if resp.StatusCode != tc.want {
				t.Errorf("GET %s = %d, want %d: %s", path, resp.StatusCode, tc.want, readBody(t, resp))
			}

			if countAudit(events(), "capture-stream", "failure", map[string]any{"phenix.capture.id": tc.id}) == 0 {
				t.Errorf("GET %s audited no capture-stream failure, want one", path)
			}
		})
	}
}

func TestCreateWebSharkCaptureOfExperimentCapture(t *testing.T) {
	useCaptureSupport(t, true)

	fake := useWebShark(t)
	live := useLiveCapture(t, fake)

	saved := string(append(testPcapHeader(), testPcapRecord(60)...))
	useExperimentFilesDir(t, map[string]string{"saved.pcapng": saved})

	fake.nsCaptures = []mm.NamespaceCapture{
		{ID: "2", State: "stopped", Path: filepath.Join(common.PhenixBase, "images", "test-experiment", "files", "saved.pcapng")},
		{ID: "3", State: "running", Path: live.path, Format: "pcap"},
	}

	live.write(t, testPcapHeader())

	reader := combinedRole(captureRole("get"), testRole("experiments/files", "get", "test-experiment"))
	server := serveAs(t, "alice", reader).URL

	t.Run("a running capture opens live", func(t *testing.T) {
		events := captureAudit(t)

		capture := openInWebShark(t, server, `{"capture":"3"}`)
		if !capture.Live || capture.Title != "test-experiment capture 3 (live)" {
			t.Errorf("POST webshark {capture 3} = live %t title %q, want live, \"test-experiment capture 3 (live)\"",
				capture.Live, capture.Title)
		}

		if !strings.HasPrefix(capture.Capture, "live-") {
			t.Errorf("POST webshark {capture 3} capture = %q, want live-<random>.pcap", capture.Capture)
		}

		wantAudit(t, events(), "capture-webshark-open", "success", map[string]any{
			"phenix.capture.id": "3", "phenix.webshark.live": true, "file.path": "live.pcap",
		})
	})

	t.Run("a stopped capture opens as its file", func(t *testing.T) {
		capture := openInWebShark(t, server, `{"capture":"2"}`)
		if capture.Live || capture.Title != "saved.pcapng" {
			t.Errorf("POST webshark {capture 2} = live %t title %q, want saved.pcapng, not live", capture.Live, capture.Title)
		}

		path := "/webshark/captures/" + capture.Capture
		if got := readBody(t, do(t, http.MethodGet, server+path, "")); got != saved {
			t.Errorf("GET %s = %d bytes, want the %d of saved.pcapng", path, len(got), len(saved))
		}
	})

	refusals := map[string]struct {
		role rbac.Role
		body string
		want int
	}{
		"without the get permission": {
			role: testRole("experiments/files", "get", "test-experiment"), body: `{"capture":"3"}`, want: http.StatusForbidden,
		},
		"of an unknown capture":         {role: reader, body: `{"capture":"9"}`, want: http.StatusNotFound},
		"with a path and a capture too": {role: reader, body: `{"capture":"3","path":"a.pcap"}`, want: http.StatusBadRequest},
	}

	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			resp, _ := createSharkCapture(t, serveAs(t, "alice", tc.role).URL, tc.body)
			if resp.StatusCode != tc.want {
				t.Errorf("POST webshark %s = %d, want %d: %s", tc.body, resp.StatusCode, tc.want, readBody(t, resp))
			}
		})
	}
}

// WebShark opens an experiment file that a running experiment capture writes as that capture, live, and a file with
// no extension when its first bytes show a capture.
func TestCreateWebSharkOfExperimentFile(t *testing.T) {
	useCaptureSupport(t, true)

	fake := useWebShark(t)
	t.Cleanup(livecapture.CloseAll)

	header := string(testPcapHeader())
	useExperimentFilesDir(t, map[string]string{"all.pcap": header, "rawcapture": header, "notes": "not a capture"})

	fake.nsCaptures = []mm.NamespaceCapture{{
		ID: "3", State: "running", Format: "pcap",
		Path: filepath.Join(common.PhenixBase, "images", "test-experiment", "files", "all.pcap"),
	}}

	reader := combinedRole(captureRole("get"), testRole("experiments/files", "get", "test-experiment"))
	server := serveAs(t, "alice", reader).URL

	t.Run("a file that an experiment capture writes opens live", func(t *testing.T) {
		capture := openInWebShark(t, server, `{"path":"all.pcap"}`)
		if !capture.Live || capture.Title != "test-experiment capture 3 (live)" {
			t.Errorf("POST webshark {path all.pcap} = live %t title %q, want live, \"test-experiment capture 3 (live)\"",
				capture.Live, capture.Title)
		}
	})

	t.Run("a pcap with no extension opens", func(t *testing.T) {
		capture := openInWebShark(t, server, `{"path":"rawcapture"}`)
		if capture.Live || capture.Title != "rawcapture" {
			t.Errorf("POST webshark {path rawcapture} = live %t title %q, want rawcapture, not live", capture.Live, capture.Title)
		}
	})

	for _, body := range []string{`{"path":"notes"}`, `{"path":"a.log"}`} {
		t.Run("refuses "+body, func(t *testing.T) {
			resp, _ := createSharkCapture(t, server, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("POST webshark %s = %d, want %d: %s", body, resp.StatusCode, http.StatusBadRequest, readBody(t, resp))
			}
		})
	}
}

// A capture token of an experiment capture names it, and streams it.
func TestCaptureTokenOfExperimentCapture(t *testing.T) {
	useCaptureSupport(t, true)

	fake := useWebShark(t)
	live := useLiveCapture(t, fake)
	fake.nsCaptures = []mm.NamespaceCapture{{ID: "3", State: "running", Path: live.path, Format: "pcap"}}

	header := testPcapHeader()
	live.write(t, header)

	reader := combinedRole(captureRole("get"), testRole("experiments/files", "get", "test-experiment"))
	server := serveAs(t, "alice", reader).URL

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/captureTokens",
		`{"operations":["stream"],"capture":"3"}`)

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST captureTokens {stream capture 3} = %d, want %d: %s", resp.StatusCode, http.StatusCreated, body)
	}

	var token captureToken
	if err := json.Unmarshal([]byte(body), &token); err != nil {
		t.Fatalf("POST captureTokens = %s, not a token: %v", body, err)
	}

	if diff := cmp.Diff(captureTokenTarget{Path: "live.pcap", Capture: "3"}, token.Target); diff != "" {
		t.Errorf("POST captureTokens {stream capture 3} target mismatch (-want +got):\n%s", diff)
	}

	expect := openCaptureStream(t, server+"/"+token.URLs["stream"], false)
	expect(header, "the file header")
}

func TestExperimentCaptureErrorStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want int
	}{
		"invalid":     {err: fmt.Errorf("start: %w", expcapture.ErrInvalid), want: http.StatusBadRequest},
		"budget":      {err: fmt.Errorf("start: %w", expcapture.ErrBudget), want: http.StatusConflict},
		"not running": {err: fmt.Errorf("start: %w", expcapture.ErrNotRunning), want: http.StatusConflict},
		"path in use": {err: fmt.Errorf("start: %w", expcapture.ErrPathInUse), want: http.StatusConflict},
		"not found":   {err: fmt.Errorf("stop: %w", expcapture.ErrNotFound), want: http.StatusNotFound},
		"not capturing": {
			err: fmt.Errorf("stream: %w", livecapture.ErrNotCapturing), want: http.StatusNotFound,
		},
		"unsupported": {err: fmt.Errorf("start: %w", mm.ErrCaptureUnsupported), want: http.StatusNotImplemented},
		"no experiment": {
			err: fmt.Errorf("get: %w", store.ErrNotExist), want: http.StatusNotFound,
		},
		"invalid file path": {
			err: fmt.Errorf("file: %w", experiment.ErrInvalidFilePath), want: http.StatusBadRequest,
		},
		"file not found": {err: fmt.Errorf("file: %w", experiment.ErrFileNotFound), want: http.StatusNotFound},
		"other":          {err: errBoom, want: http.StatusInternalServerError},
	}

	for name, tc := range tests {
		if got := experimentCaptureError("exp", "1", tc.err).Status; got != tc.want {
			t.Errorf("experimentCaptureError(%s) status = %d, want %d", name, got, tc.want)
		}
	}
}

// The experiment captures of the contract fixture: 1 runs, and 2 stopped, leaving a.pcap.
const (
	contractRunningCapture = "1"
	contractStoppedCapture = "2"
)

// useContractCaptures has minimega support experiment captures, and list contractRunningCapture and
// contractStoppedCapture in the contract fixture's experiment files at files, for the rest of the test. A start gives
// contractRunningCapture, and a stop stops either; other commands get an empty success.
func useContractCaptures(t *testing.T, files string) {
	t.Helper()

	useCaptureSupport(t, true)

	header := []string{"id", "host", "bridge", "state", "path", "vlans", "filter", "start"}
	row := func(id, state string) []string {
		file := "c" + id + ".pcapng"
		if id == contractStoppedCapture {
			file = "a.pcap"
		}

		return []string{id, "", "", state, filepath.Join(files, file), "", "", "2026-11-05T14:22:33Z"}
	}

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case cmd.Base == "capture pcap namespace":
			return []*minicli.Response{mmtest.Tabular("head", header,
				row(contractRunningCapture, "running"), row(contractStoppedCapture, "stopped"))}
		case strings.HasPrefix(cmd.Base, "capture pcap namespace "):
			return []*minicli.Response{mmtest.Text("head", contractRunningCapture)}
		case strings.HasPrefix(cmd.Base, "capture pcap delete namespace "):
			id := strings.TrimPrefix(cmd.Base, "capture pcap delete namespace ")

			return []*minicli.Response{mmtest.Tabular("head", header, row(id, "stopped"))}
		}

		return nil
	})
}

// experimentCaptureErrorCases are TestContractErrors' experiment capture cases.
// A list of the experiment captures that minimega fails answers 200 with the VM captures and the reason, as
// openapi.yml documents.
func TestContractExperimentCapturesError(t *testing.T) {
	useContractFixture(t)

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "capture pcap namespace" {
			return []*minicli.Response{mmtest.Error("head", "boom")}
		}

		return nil
	})

	server := serveContractAs(t, adminRole(), nil).URL
	path := contractExpPath + "/captures"

	resp := do(t, http.MethodGet, server+path, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d: %s", path, resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	wantDocumentedBody(t, http.MethodGet, path, resp)

	got := new(proto.CaptureList)
	if err := protojson.Unmarshal([]byte(readBody(t, resp)), got); err != nil {
		t.Fatalf("GET %s is not a capture list: %v", path, err)
	}

	if got.GetExperimentCapturesError() == "" || len(got.GetExperimentCaptures()) != 0 {
		t.Errorf("GET %s = experimentCapturesError %q and %d experiment captures, want an error and none",
			path, got.GetExperimentCapturesError(), len(got.GetExperimentCaptures()))
	}
}

func experimentCaptureErrorCases() map[string]contractErrorCase {
	const (
		captures = contractExpPath + "/captures"
		stopped  = "stopped-experiment"
	)

	return map[string]contractErrorCase{
		"experiment capture with a quote in its filter": {
			method: "POST", path: captures, body: `{"filter":"host \"a\""}`, want: http.StatusBadRequest,
		},
		"experiment capture of an unknown VLAN": {
			method: "POST", path: captures, body: `{"vlans":["EXP_9"]}`, want: http.StatusBadRequest,
		},
		"experiment capture of malformed body": {method: "POST", path: captures, body: "{", want: http.StatusBadRequest},
		"experiment capture into the file of a running capture": {
			method: "POST", path: captures, body: `{"name":"c` + contractRunningCapture + `"}`, want: http.StatusConflict,
		},
		"experiment capture past the mirror budget": {
			method: "POST", body: `{}`, want: http.StatusConflict,
			prepare: func(t *testing.T, _ contractFixture) string {
				t.Helper()

				rows := make([][]string, MaxContractCaptures)
				for i := range rows {
					rows[i] = []string{strconv.Itoa(i + 1), "", "", "running"}
				}

				mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
					if cmd.Base == "capture pcap namespace" {
						return []*minicli.Response{mmtest.Tabular("head", []string{"id", "host", "bridge", "state"}, rows...)}
					}

					return nil
				})

				return captures
			},
		},
		"experiment capture of a stopped experiment": {
			method: "POST", body: `{}`, want: http.StatusConflict,
			prepare: func(t *testing.T, _ contractFixture) string {
				t.Helper()

				useTestStore(t, namedExperiment(t, stopped, ""))

				return "/api/v1/experiments/" + stopped + "/captures"
			},
		},
		"experiment capture without minimega's support": {
			method: "POST", body: `{}`, want: http.StatusNotImplemented,
			prepare: func(t *testing.T, _ contractFixture) string {
				t.Helper()

				useCaptureSupport(t, false)
				mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
					if cmd.Base == "version" {
						return []*minicli.Response{mmtest.Text("head", "minimega 3.3.0 abc 2026-01-01")}
					}

					return nil
				})

				return captures
			},
		},
		"experiment capture of unknown experiment": {
			method: "POST", path: "/api/v1/experiments/" + contractOther + "/captures", body: `{}`, want: http.StatusNotFound,
		},
		"unknown experiment capture":         {method: "GET", path: captures + "/9", want: http.StatusNotFound},
		"stop of unknown experiment capture": {method: "DELETE", path: captures + "/9", want: http.StatusNotFound},
		"stream of unknown experiment capture": {
			method: "GET", path: captures + "/9/stream", want: http.StatusNotFound,
		},
		"WebShark of unknown experiment capture": {
			method: "POST", path: contractExpPath + "/webshark", body: `{"capture":"9"}`, want: http.StatusNotFound,
		},
		"WebShark of a path and an experiment capture": {
			method: "POST", path: contractExpPath + "/webshark", body: `{"capture":"1","path":"a.pcap"}`,
			want: http.StatusBadRequest,
		},
	}
}

// MaxContractCaptures is how many experiment captures an experiment may run, as the contract tests see it.
const MaxContractCaptures = expcapture.MaxExperimentCaptures

// A VM interface capture starts in the format asked for, and refuses another.
func TestStartVMCaptureFormat(t *testing.T) {
	tests := map[string]struct {
		body       string
		want       int
		wantStarts int32
	}{
		"pcap":           {body: `{"interface":1,"filename":"x","format":"pcap"}`, want: http.StatusNoContent, wantStarts: 1},
		"the default":    {body: `{"interface":1,"filename":"x"}`, want: http.StatusNoContent, wantStarts: 1},
		"another format": {body: `{"interface":1,"filename":"x","format":"erf"}`, want: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureSupport(t, true)

			fake := useVLANCaptureVMs(t, false)
			fake.vms = fake.vms[:1] // minimega's details of VM a alone
			server := serveAs(t, "alice", testRole("vms/captures", "create", "test-experiment/a")).URL

			const path = "/api/v1/experiments/test-experiment/vms/a/captures"

			resp := do(t, http.MethodPost, server+path, tc.body)
			if resp.StatusCode != tc.want {
				t.Errorf("POST %s %s = %d, want %d: %s", path, tc.body, resp.StatusCode, tc.want, readBody(t, resp))
			}

			if n := fake.captureStarts.Load(); n != tc.wantStarts {
				t.Errorf("POST %s %s started %d captures, want %d", path, tc.body, n, tc.wantStarts)
			}
		})
	}
}

func TestStreamFileName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		capture            livecapture.Capture
		wantName, wantMIME string
	}{
		"a VM's pcap": {
			capture:  livecapture.Capture{Exp: "exp1", VM: "vm", Interface: 2, Path: "/f/vm_2.pcap"},
			wantName: "vm-2.pcap", wantMIME: pcapContentType,
		},
		"a VM's pcapng": {
			capture:  livecapture.Capture{Exp: "exp1", VM: "vm", Interface: 0, Path: "/f/vm_0.PCAPNG"},
			wantName: "vm-0.pcapng", wantMIME: pcapngContentType,
		},
		"an experiment capture": {
			capture:  livecapture.Capture{Exp: "exp1", ID: "3", Path: "/f/exp1.pcapng"},
			wantName: "exp1-capture-3.pcapng", wantMIME: pcapngContentType,
		},
	}

	for name, tc := range tests {
		gotName, gotMIME := streamFileName(tc.capture)
		if gotName != tc.wantName || gotMIME != tc.wantMIME {
			t.Errorf("streamFileName(%s) = %q, %q, want %q, %q", name, gotName, gotMIME, tc.wantName, tc.wantMIME)
		}
	}
}

// WebShark refuses an experiment capture it cannot open: a 500 when minimega cannot list the captures, and a 404 for a
// stopped capture that left no file in the experiment's files. Each refusal is audited as failed.
func TestCreateWebSharkCaptureOfExperimentCaptureFails(t *testing.T) {
	tests := map[string]struct {
		nsErr   error
		capture mm.NamespaceCapture
		want    int
	}{
		"minimega cannot list the captures": {
			nsErr: errBoom, capture: mm.NamespaceCapture{ID: "3", State: "running"}, want: http.StatusInternalServerError,
		},
		"a stopped capture outside the experiment's files": {
			capture: mm.NamespaceCapture{ID: "3", State: "stopped", Path: "/elsewhere/x.pcapng"}, want: http.StatusNotFound,
		},
		"a stopped capture whose file is gone": {
			capture: mm.NamespaceCapture{ID: "3", State: "stopped", Path: "gone.pcapng"}, want: http.StatusNotFound,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureSupport(t, true)

			fake := useWebShark(t)
			useExperimentFilesDir(t, nil)
			useTestFiles(t)

			c := tc.capture
			if c.Path == "gone.pcapng" {
				c.Path = filepath.Join(common.PhenixBase, "images", "test-experiment", "files", c.Path)
			}

			fake.nsCaptures, fake.nsErr = []mm.NamespaceCapture{c}, tc.nsErr

			reader := combinedRole(captureRole("get"), testRole("experiments/files", "get", "test-experiment"))
			events := captureAudit(t)

			resp, _ := createSharkCapture(t, serveAs(t, "alice", reader).URL, `{"capture":"3"}`)
			if resp.StatusCode != tc.want {
				t.Fatalf("POST webshark {capture 3} = %d, want %d: %s", resp.StatusCode, tc.want, readBody(t, resp))
			}

			wantAudit(t, events(), "capture-webshark-open", "failure", map[string]any{
				"user.name": "alice", "phenix.capture.id": "3",
			})
		})
	}
}

// An experiment capture call that fails because its client left answers nothing; another failure answers its status.
func TestAnswerExperimentCaptureError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		canceled bool
		err      error
		want     int // 0 for no answer
	}{
		"the client left":   {canceled: true, err: fmt.Errorf("listing: %w", context.Canceled)},
		"minimega failed":   {err: errBoom, want: http.StatusInternalServerError},
		"capture not found": {err: fmt.Errorf("get: %w", expcapture.ErrNotFound), want: http.StatusNotFound},
		// a canceled request that failed for another reason still answers
		"canceled, but another failure": {canceled: true, err: errBoom, want: http.StatusInternalServerError},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if tc.canceled {
				cancel()
			}

			r := httptest.NewRequestWithContext(ctx, http.MethodGet, experimentCapturesPath+"/3", nil)

			err := answerExperimentCaptureError(r, "test-experiment", "3", tc.err)
			if tc.want == 0 {
				if err != nil {
					t.Errorf("answerExperimentCaptureError(%v) = %v, want nil", tc.err, err)
				}

				return
			}

			var werr *weberror.WebError
			if !errors.As(err, &werr) || werr.Status != tc.want {
				t.Errorf("answerExperimentCaptureError(%v) = %v, want a %d WebError", tc.err, err, tc.want)
			}
		})
	}
}

// experimentCaptureLeavingMM is the test minimega, which cancels the request when it is asked to list the experiment
// captures (or with mirrors set, the Open vSwitch mirrors), as a client that leaves meanwhile does.
type experimentCaptureLeavingMM struct {
	*testMM

	cancel  context.CancelFunc
	mirrors bool
}

func (m experimentCaptureLeavingMM) GetNamespaceCaptures(opts ...mm.Option) ([]mm.NamespaceCapture, error) {
	if m.mirrors {
		return m.testMM.GetNamespaceCaptures(opts...)
	}

	m.cancel()

	return nil, context.Canceled
}

func (m experimentCaptureLeavingMM) GetBridgeMirrors(...mm.Option) ([]mm.BridgeMirror, error) {
	m.cancel()

	return nil, context.Canceled
}

// A list of the experiment captures whose client leaves while minimega lists the captures or the mirrors answers
// nothing.
func TestGetExperimentCapturesWhenTheClientLeaves(t *testing.T) {
	for name, leaveAtMirrors := range map[string]bool{"while listing captures": false, "while listing mirrors": true} {
		t.Run(name, func(t *testing.T) {
			useCaptureSupport(t, true)

			fake := useCapturedExperiment(t, false)

			req := roleRequest(http.MethodGet, experimentCapturesPath, "", map[string]string{"name": "test-experiment"},
				captureRole("list"))

			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()

			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

			leaving := experimentCaptureLeavingMM{testMM: fake, cancel: cancel, mirrors: leaveAtMirrors}
			mm.DefaultMM = leaving //nolint:reassign // test double

			rec := httptest.NewRecorder()

			if err := GetExperimentCaptures(rec, req.WithContext(ctx)); err != nil {
				t.Errorf("GetExperimentCaptures(client left %s) = %v, want nil", name, err)
			}

			if rec.Body.Len() != 0 {
				t.Errorf("GetExperimentCaptures(client left %s) wrote %q, want nothing", name, rec.Body.String())
			}
		})
	}
}

// An experiment capture that minimega lists with a path that is not UTF-8 cannot be sent as JSON: a get of it, or a
// start that makes it, answers 500.
func TestExperimentCaptureOfPathNotUTF8(t *testing.T) {
	notUTF8 := mm.NamespaceCapture{ID: "4", State: "running", Path: "/phenix/images/test-experiment/files/\xff.pcapng"}

	tests := map[string]struct {
		method, path string
		role         rbac.Role
		listed       []mm.NamespaceCapture // before a start
	}{
		"get": {
			method: http.MethodGet, path: experimentCapturesPath + "/4", role: captureRole("get"),
			listed: []mm.NamespaceCapture{notUTF8},
		},
		"start": {method: http.MethodPost, path: experimentCapturesPath, role: captureRole("create")},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureSupport(t, true)

			fake := useCapturedExperiment(t, false, tc.listed...)
			fake.nsStarted = []mm.NamespaceCapture{notUTF8}

			resp := do(t, tc.method, serveAs(t, "alice", tc.role).URL+tc.path, `{"name":"x"}`)
			body := readBody(t, resp)

			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("%s %s = %d, want %d: %s", tc.method, tc.path, resp.StatusCode, http.StatusInternalServerError, body)
			}

			if got := errorMessage(t, resp.StatusCode, body); !strings.Contains(got, "UTF-8") {
				t.Errorf("%s %s = %q, want the UTF-8 error", tc.method, tc.path, got)
			}
		})
	}
}
