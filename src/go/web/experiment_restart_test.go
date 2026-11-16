package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// restartMM answers each minimega command with an empty success, and fails a namespace clear while failClear is set
// and a VM launch while failLaunch is set.
type restartMM struct {
	failClear, failLaunch atomic.Bool
}

func (m *restartMM) handle(cmd mmtest.Command) []*minicli.Response {
	switch {
	case m.failClear.Load() && strings.HasPrefix(cmd.Base, "clear namespace"):
		return []*minicli.Response{mmtest.Error("head", "minimega unreachable")}
	case m.failLaunch.Load() && strings.HasPrefix(cmd.Base, "vm launch"):
		return []*minicli.Response{mmtest.Error("head", "no room for the VMs")}
	default:
		return nil
	}
}

// waitGone waits until no goroutine runs inside function, or fails the test.
func waitGone(t *testing.T, function string) {
	t.Helper()

	if got := waitGoroutines(function, "goroutine ", func(n int) bool { return n == 0 }); got != 0 {
		t.Errorf("%d goroutines still run %s", got, function)
	}
}

// restartMessage checks that the error body raw has a message that starts with want and holds each of its
// details.warnings.
func restartMessage(raw, want string) error {
	var body struct {
		Message string `json:"message"`
		Details struct {
			Warnings []string `json:"warnings"`
		} `json:"details"`
	}

	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return fmt.Errorf("the error body %s is not JSON: %w", raw, err)
	}

	if !strings.HasPrefix(body.Message, want) {
		return fmt.Errorf("message = %q, want it to start with %q", body.Message, want)
	}

	for _, w := range body.Details.Warnings {
		if !strings.Contains(body.Message, w) {
			return fmt.Errorf("message = %q, want it to hold the warning %q", body.Message, w)
		}
	}

	return nil
}

// restartAnswer is the part of a restart answer that the tests compare.
type restartAnswer struct {
	// Shape is "experiment", "result", "job" or "error".
	Shape string
	// Warnings counts warnings, or details.warnings of an error.
	Warnings int
	Code     weberror.Code
	Retry    string
}

// readRestartAnswer reads the answer of a restart.
func readRestartAnswer(t *testing.T, status int, raw string) restartAnswer {
	t.Helper()

	var body struct {
		Kind       string          `json:"kind"`
		Experiment json.RawMessage `json:"experiment"`
		Warnings   []string        `json:"warnings"`
		Code       weberror.Code   `json:"code"`
		Metadata   struct {
			Retry string `json:"retry"`
		} `json:"metadata"`
		Details struct {
			Warnings []string `json:"warnings"`
		} `json:"details"`
	}

	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("restart answer %s is not a JSON object: %v", raw, err)
	}

	switch {
	case status >= http.StatusBadRequest:
		return restartAnswer{
			Shape: "error", Warnings: len(body.Details.Warnings), Code: body.Code, Retry: body.Metadata.Retry,
		}
	case status == http.StatusAccepted && body.Kind != "":
		return restartAnswer{Shape: "job"}
	case body.Experiment != nil:
		return restartAnswer{Shape: "result", Warnings: len(body.Warnings)}
	default:
		return restartAnswer{Shape: "experiment"}
	}
}

// POST /experiments/{name}/restart needs both lifecycle permissions, refuses a dry run, the name all and a stopped
// experiment, and checks the disks before the stop. A stop that leaves the VMs answers as the stop route does, and a
// start that fails leaves the experiment stopped. A success answers as a start does, with a StartResult also for a
// forced stop and for warnings, and is audited.
func TestRestartExperiment(t *testing.T) {
	const started = "2024-01-01T00:00:00Z"

	var (
		both = combinedRole(testRole("experiments/stop", "update", "up"), testRole("experiments/start", "update", "up"))
		fake = &restartMM{}
	)

	received := mmtest.Use(t, fake.handle)

	// the note logger of each start ends up to a second after its start (see followStart)
	t.Cleanup(func() { waitGone(t, "phenix/web.followStart.func") })

	tests := []struct {
		name       string
		path       string
		body       string
		role       rbac.Role
		started    string
		broken     bool // a VM boots from an image that qemu cannot open
		failClear  bool
		failLaunch bool

		wantStatus  int
		want        restartAnswer
		wantRunning bool
		wantCleared bool // the stop sent minimega a namespace clear
		wantAudit   string
		wantMessage string
	}{
		{
			name: "stop permission only", role: testRole("experiments/stop", "update", "up"),
			wantStatus: http.StatusForbidden, want: restartAnswer{Shape: "error", Code: "forbidden"}, wantRunning: true,
			wantAudit: "denied",
		},
		{
			name: "start permission only", role: testRole("experiments/start", "update", "up"),
			wantStatus: http.StatusForbidden, want: restartAnswer{Shape: "error", Code: "forbidden"}, wantRunning: true,
			wantAudit: "denied",
		},
		{
			name: "a dry run", body: `{"dryRun": true}`,
			wantStatus: http.StatusBadRequest, want: restartAnswer{Shape: "error", Code: "invalid_request"}, wantRunning: true,
		},
		{
			name: "an unknown option", body: `{"force": true, "dry": true}`,
			wantStatus: http.StatusBadRequest, want: restartAnswer{Shape: "error", Code: "invalid_request"}, wantRunning: true,
		},
		{
			name: "the name all", path: "/api/v1/experiments/all/restart",
			wantStatus: http.StatusBadRequest, want: restartAnswer{Shape: "error", Code: "invalid_request"}, wantRunning: true,
		},
		{
			name: "a stopped experiment", started: "-",
			wantStatus: http.StatusConflict, want: restartAnswer{Shape: "error", Code: "conflict"}, wantAudit: "failure",
		},
		{
			name: "a broken disk", broken: true,
			wantStatus: http.StatusBadRequest, want: restartAnswer{Shape: "error", Code: weberror.CodeBrokenDisks},
			wantRunning: true, wantAudit: "failure",
			wantMessage: "unable to restart experiment up, which is still running",
		},
		{
			name: "VMs that survive the stop", failClear: true,
			wantStatus: http.StatusBadGateway, want: restartAnswer{Shape: "error", Code: "unavailable", Retry: "force"},
			wantRunning: true, wantCleared: true, wantAudit: "failure",
		},
		{
			name: "a start that fails", failLaunch: true,
			wantStatus: http.StatusBadRequest, want: restartAnswer{Shape: "error", Code: "invalid_request"},
			wantCleared: true, wantAudit: "failure",
			wantMessage: "experiment up is stopped: the restart stopped it, but its start failed",
		},
		{
			name: "a start that fails after a forced stop over VMs that survive", body: `{"force": true}`,
			failClear: true, failLaunch: true,
			wantStatus: http.StatusBadRequest, want: restartAnswer{Shape: "error", Warnings: 1, Code: "invalid_request"},
			wantCleared: true, wantAudit: "failure",
			wantMessage: "experiment up is stopped: the restart stopped it, but its start failed. The stop warned: " +
				"phenix marked experiment up stopped, but minimega could not kill its VMs",
		},
		{
			name: "no options", wantStatus: http.StatusOK, want: restartAnswer{Shape: "experiment"},
			wantRunning: true, wantCleared: true, wantAudit: "success",
		},
		{
			name: "minimega errors as warnings", body: `{"treatMinimegaErrorsAsWarnings": true}`,
			wantStatus: http.StatusOK, want: restartAnswer{Shape: "result"},
			wantRunning: true, wantCleared: true, wantAudit: "success",
		},
		{
			name: "a forced stop that kills the VMs", body: `{"force": true}`,
			wantStatus: http.StatusOK, want: restartAnswer{Shape: "result"},
			wantRunning: true, wantCleared: true, wantAudit: "success",
		},
		{
			name: "a forced stop over VMs that survive", body: `{"force": true, "keepInjectionSnapshots": true}`,
			failClear: true, wantStatus: http.StatusOK, want: restartAnswer{Shape: "result", Warnings: 1},
			wantRunning: true, wantCleared: true, wantAudit: "success",
		},
		{
			name: "warnings of the stop and the start", body: `{"force": true, "treatMinimegaErrorsAsWarnings": true}`,
			failClear: true, failLaunch: true, wantStatus: http.StatusOK, want: restartAnswer{Shape: "result", Warnings: 2},
			wantRunning: true, wantCleared: true, wantAudit: "success",
		},
		{
			name: "the scheduled runs", body: `{"honorRunPeriodically": true}`,
			wantStatus: http.StatusAccepted, want: restartAnswer{Shape: "job"},
			wantRunning: true, wantCleared: true, wantAudit: "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := started
			if tt.started == "-" {
				start = ""
			}

			var nodes []map[string]any
			if tt.broken {
				nodes = append(nodes, snapshotVM("vm1", loopImage(t)))
			}

			// the post-start work of a start ends before the test store goes
			t.Cleanup(func() { waitGone(t, "phenix/api/experiment.finishStart") })

			useTestStore(t, namedExperiment(t, "up", start, nodes...))
			useJobs(t)
			experimentStopClearCancelers(t, "up")
			t.Cleanup(func() { cancelPeriodicJob("up") })

			fake.failClear.Store(tt.failClear)
			fake.failLaunch.Store(tt.failLaunch)
			t.Cleanup(func() { fake.failClear.Store(false); fake.failLaunch.Store(false) })

			role, path := tt.role, tt.path
			if role.Spec == nil {
				role = both
			}

			if path == "" {
				path = "/api/v1/experiments/up/restart"
			}

			before := len(received())
			events := captureAudit(t)

			resp := do(t, http.MethodPost, serveAs(t, "alice", role).URL+path, tt.body)
			wantDocumentedBody(t, http.MethodPost, path, resp)

			raw := readBody(t, resp)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("POST %s %s = %d, want %d: %s", path, tt.body, resp.StatusCode, tt.wantStatus, raw)
			}

			if diff := cmp.Diff(tt.want, readRestartAnswer(t, resp.StatusCode, raw)); diff != "" {
				t.Errorf("POST %s %s answer mismatch (-want +got):\n%s", path, tt.body, diff)
			}

			if tt.wantMessage != "" {
				if err := restartMessage(raw, tt.wantMessage); err != nil {
					t.Errorf("POST %s %s: %v", path, tt.body, err)
				}
			}

			now := storedStartTime(t, "up")
			if running := now != ""; running != tt.wantRunning {
				t.Errorf("after POST %s %s the experiment runs = %t, want %t", path, tt.body, running, tt.wantRunning)
			}

			restarted := now != "" && now != start
			if wantRestarted := tt.wantStatus < http.StatusBadRequest; restarted != wantRestarted {
				t.Errorf("after POST %s %s start time = %q, want a new one %t", path, tt.body, now, wantRestarted)
			}

			cleared := mmtest.Count(received()[before:], "clear namespace") > 0
			if cleared != tt.wantCleared {
				t.Errorf("POST %s %s cleared the namespace = %t, want %t", path, tt.body, cleared, tt.wantCleared)
			}

			switch got := events(); tt.wantAudit {
			case "denied":
				wantDenied(t, got, "experiment-restart")
			case "failure":
				wantAudit(t, got, "experiment-restart", "failure", map[string]any{"phenix.experiment.name": "up"})
			case "success":
				wantAudit(t, got, "experiment-restart", "success", map[string]any{"phenix.experiment.name": "up"})
			default:
				if n := countAudit(got, "experiment-restart", "failure", nil) +
					countAudit(got, "experiment-restart", "success", nil); n != 0 {
					t.Errorf("POST %s %s logged %d experiment-restart events, want none", path, tt.body, n)
				}
			}
		})
	}
}

// The audit event of a restart has its options.
func TestRestartExperimentAuditFields(t *testing.T) {
	t.Cleanup(func() { waitGone(t, "phenix/web.followStart.func") })

	useTestStore(t, namedExperiment(t, "up", "2024-01-01T00:00:00Z"))
	useJobs(t)
	experimentStopClearCancelers(t, "up")
	useStartMM(t)

	events := captureAudit(t)
	role := combinedRole(testRole("experiments/stop", "update", "up"), testRole("experiments/start", "update", "up"))

	resp := do(t, http.MethodPost, serveAs(t, "alice", role).URL+"/api/v1/experiments/up/restart",
		`{"force": true, "keepInjectionSnapshots": true, "honorRunPeriodically": false, "vlanMin": 100}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST restart = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	event := wantAudit(t, events(), "experiment-restart", "success", map[string]any{
		"phenix.experiment.name": "up", "phenix.experiment.force": true,
		"phenix.experiment.keep_injection_snapshots": true, "phenix.start.honor_run_periodically": false,
		"phenix.start.vlan_min": 100, "event.category": "host", "event.type": []string{"change"},
	})

	if d, _ := event["phenix.duration"].(string); event != nil && !strings.HasSuffix(d, "s") {
		t.Errorf("experiment-restart phenix.duration = %v, want a duration", event["phenix.duration"])
	}
}

// uniqueTexts keeps the first of each text, in order, and gives an empty list for none.
func TestUniqueTexts(t *testing.T) {
	tests := []struct {
		name  string
		lists [][]string
		want  []string
	}{
		{name: "none", want: []string{}},
		{name: "empty lists", lists: [][]string{nil, {}}, want: []string{}},
		{name: "no repeats", lists: [][]string{{"a"}, {"b", "c"}}, want: []string{"a", "b", "c"}},
		{name: "repeats", lists: [][]string{{"a", "b"}, {"b"}, {"a", "c", "c"}}, want: []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uniqueTexts(tt.lists...)
			if diff := cmp.Diff(tt.want, got); diff != "" || got == nil {
				t.Errorf("uniqueTexts(%q) mismatch (-want +got):\n%s", tt.lists, diff)
			}
		})
	}
}

// restartStartError keeps a 409 as it is, as the start of another caller. Any other failed start says that the
// experiment is stopped, adds the stop warnings to the message and lists them in details.warnings.
func TestRestartStartError(t *testing.T) {
	const stopped = "experiment up is stopped: the restart stopped it, but its start failed"

	cause := errors.New("no room")
	two := []string{"VMs left in minimega", "snapshots kept"}

	tests := []struct {
		name         string
		err          error
		warnings     []string
		wantStatus   int
		wantMessage  string
		wantWarnings any // details.warnings, nil when absent
	}{
		{
			name: "a conflict", err: weberror.NewWebError(cause, "unable to start experiment up").SetStatus(http.StatusConflict),
			warnings: two, wantStatus: http.StatusConflict, wantMessage: "unable to start experiment up",
		},
		{
			name: "a failed start without warnings", err: weberror.NewWebError(cause, "unable to start experiment up"),
			wantStatus: http.StatusBadRequest, wantMessage: stopped,
		},
		{
			name: "a failed start with warnings", err: weberror.NewWebError(cause, "unable to start experiment up"),
			warnings: two, wantStatus: http.StatusBadRequest,
			wantMessage:  stopped + ". The stop warned: VMs left in minimega; snapshots kept",
			wantWarnings: two,
		},
		{
			name: "an error that is not a web error", err: cause,
			wantStatus: http.StatusInternalServerError, wantMessage: stopped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var werr *weberror.WebError
			if err := restartStartError(tt.err, "up", tt.warnings); !errors.As(err, &werr) {
				t.Fatalf("restartStartError(%v) = %v, want a *weberror.WebError", tt.err, err)
			}

			if werr.Status != tt.wantStatus || werr.Message != tt.wantMessage {
				t.Errorf("restartStartError(%v) = %d %q, want %d %q", tt.err, werr.Status, werr.Message, tt.wantStatus,
					tt.wantMessage)
			}

			if diff := cmp.Diff(tt.wantWarnings, werr.Details["warnings"]); diff != "" {
				t.Errorf("restartStartError(%v) details.warnings mismatch (-want +got):\n%s", tt.err, diff)
			}
		})
	}
}
