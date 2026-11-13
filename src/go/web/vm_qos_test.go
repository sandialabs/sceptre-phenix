package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/web/weberror"
)

// useQoSMinimega makes the fake minimega report VM a of exp with two
// interfaces, the second with a loss and a delay, and no other VM. It fails
// each command that starts with fail, unless fail is empty, and returns the
// commands it received.
func useQoSMinimega(t *testing.T, fail string) func() []mmtest.Command {
	t.Helper()

	return mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if fail != "" && strings.HasPrefix(cmd.Base, fail) {
			return []*minicli.Response{mmtest.Error("head", "boom")}
		}

		if cmd.Base != "vm info" {
			return nil
		}

		header := []string{"name", "vlan", "qos"}
		if !strings.Contains(cmd.Raw, "name=a") {
			return []*minicli.Response{mmtest.Tabular("head", header)}
		}

		return []*minicli.Response{mmtest.Tabular("head", header,
			[]string{"a", "[EXP_1 (101), EXP_2 (102)]", "[1: loss 0.5 delay 100ms]"})}
	})
}

// GET .../qos lists each interface of the VM with its link quality, for a role
// that may patch the VM of a running experiment.
func TestGetVMQoS(t *testing.T) {
	tests := []struct {
		name       string
		vm         string
		running    bool
		role       string // the VM the role may patch
		wantStatus int
	}{
		{name: "a running VM", vm: "a", running: true, role: "exp/a", wantStatus: http.StatusOK},
		{name: "a stopped experiment", vm: "a", role: "exp/a", wantStatus: http.StatusConflict},
		{name: "a VM the topology lacks", vm: "nope", running: true, role: "exp/*", wantStatus: http.StatusNotFound},
		{name: "a VM minimega lacks", vm: "b", running: true, role: "exp/*", wantStatus: http.StatusNotFound},
		{name: "a role that may not patch the VM", vm: "a", running: true, role: "exp/b", wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useVMExperiment(t, tc.running, "a", "b")
			useQoSMinimega(t, "")

			server := serveAs(t, "alice", testRole("vms", "patch", tc.role)).URL
			resp := do(t, http.MethodGet, server+"/api/v1/experiments/exp/vms/"+tc.vm+"/qos", "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("GET vms/%s/qos = %d, want %d: %s", tc.vm, resp.StatusCode, tc.wantStatus, body)
			}

			if resp.StatusCode != http.StatusOK {
				return
			}

			var got vmQoSResponse
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decoding GET vms/a/qos: %v", err)
			}

			want := vmQoSResponse{Interfaces: []mm.InterfaceQoS{
				{Interface: 0},
				{Interface: 1, Loss: "0.5", Delay: "100ms"},
			}}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("GET vms/a/qos mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// PUT .../interfaces/{iface}/qos refuses link quality minimega cannot apply,
// before it sends anything, and replaces the interface's link quality.
func TestSetVMInterfaceQoS(t *testing.T) {
	tests := []struct {
		name       string
		index      string
		body       string
		running    bool
		role       string
		fail       string // the minimega command that fails
		wantStatus int
		wantSent   []string // the commands sent after the VM is read
	}{
		{
			name: "a loss and a delay", index: "1", body: `{"loss":"0.5","delay":"100ms"}`, running: true,
			wantStatus: http.StatusOK,
			wantSent:   []string{"clear qos a 1", "qos add a 1 loss 0.5", "qos add a 1 delay 100ms", "vm info"},
		},
		{
			name: "a rate", index: "0", body: `{"rate":"10mbit","loss":""}`, running: true, wantStatus: http.StatusOK,
			wantSent: []string{"clear qos a 0", "qos add a 0 rate 10 mbit", "vm info"},
		},
		{name: "no settings", index: "0", body: `{}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "a rate and a loss", index: "0", body: `{"rate":"1gbit","loss":"1"}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "a loss of 100", index: "0", body: `{"loss":"100"}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "a delay that is not a duration", index: "0", body: `{"delay":"soon"}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "a rate without a unit", index: "0", body: `{"rate":"10"}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "a malformed body", index: "0", body: `{"loss":`, running: true, wantStatus: http.StatusBadRequest},
		{name: "an index that is not a number", index: "x", body: `{"loss":"1"}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "a negative index", index: "-1", body: `{"loss":"1"}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "an index past the last interface", index: "2", body: `{"loss":"1"}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "a stopped experiment", index: "0", body: `{"loss":"1"}`, wantStatus: http.StatusConflict},
		{
			name: "a role that may not patch the VM", index: "0", body: `{"loss":"1"}`, running: true, role: "exp/b",
			wantStatus: http.StatusForbidden,
		},
		{name: "a delay in minutes", index: "0", body: `{"delay":"1m"}`, running: true, wantStatus: http.StatusBadRequest},
		{name: "a rate of zero", index: "0", body: `{"rate":"0kbit"}`, running: true, wantStatus: http.StatusBadRequest},
		{
			name: "minimega refuses a setting", index: "0", body: `{"loss":"1"}`, running: true, fail: "qos add",
			wantStatus: http.StatusBadRequest, wantSent: []string{"clear qos a 0", "qos add a 0 loss 1", "clear qos a 0"},
		},
		{
			name: "minimega refuses a setting and restores the previous ones", index: "1", body: `{"rate":"1gbit"}`,
			running: true, fail: "qos add a 1 rate", wantStatus: http.StatusBadRequest,
			wantSent: []string{
				"clear qos a 1", "qos add a 1 rate 1 gbit",
				"clear qos a 1", "qos add a 1 loss 0.5", "qos add a 1 delay 100ms",
			},
		},
		{
			name: "minimega cannot clear", index: "0", body: `{"loss":"1"}`, running: true, fail: "clear qos",
			wantStatus: http.StatusInternalServerError, wantSent: []string{"clear qos a 0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useVMExperiment(t, tc.running, "a", "b")
			received := useQoSMinimega(t, tc.fail)
			events := captureAudit(t)

			role := tc.role
			if role == "" {
				role = "exp/a"
			}

			server := serveAs(t, "alice", testRole("vms", "patch", role)).URL
			resp := do(t, http.MethodPut, server+"/api/v1/experiments/exp/vms/a/interfaces/"+tc.index+"/qos", tc.body)
			body := readBody(t, resp)

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("PUT interfaces/%s/qos %s = %d, want %d: %s", tc.index, tc.body, resp.StatusCode, tc.wantStatus, body)
			}

			sent := qosCommands(received())

			if diff := cmp.Diff(tc.wantSent, sent, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("PUT interfaces/%s/qos %s commands mismatch (-want +got):\n%s", tc.index, tc.body, diff)
			}

			switch resp.StatusCode {
			case http.StatusOK:
				var got mm.InterfaceQoS
				if err := json.Unmarshal([]byte(body), &got); err != nil {
					t.Fatalf("decoding PUT interfaces/%s/qos: %v", tc.index, err)
				}

				// the interface as minimega reports it after the change
				if tc.index == "1" && got != (mm.InterfaceQoS{Interface: 1, Loss: "0.5", Delay: "100ms"}) {
					t.Errorf("PUT interfaces/1/qos = %+v, want the interface as minimega reports it", got)
				}

				wantAudit(t, events(), "vm-update", "success", map[string]any{"phenix.vm.name": "a", "phenix.vm.interface": tc.index})
			case http.StatusForbidden:
				wantDenied(t, events(), "vm-update")
			case http.StatusInternalServerError:
				wantAudit(t, events(), "vm-update", "failure", map[string]any{"phenix.vm.name": "a"})
			case http.StatusBadRequest:
				if tc.fail != "" {
					wantAudit(t, events(), "vm-update", "failure", map[string]any{"phenix.vm.name": "a"})

					if !strings.Contains(body, "boom") {
						t.Errorf("PUT interfaces/%s/qos %s body = %s, want minimega's message", tc.index, tc.body, body)
					}
				}
			}
		})
	}
}

// DELETE .../interfaces/{iface}/qos clears the interface's link quality.
func TestClearVMInterfaceQoS(t *testing.T) {
	tests := []struct {
		name       string
		index      string
		running    bool
		fail       string
		wantStatus int
		wantSent   []string
	}{
		{name: "an interface", index: "1", running: true, wantStatus: http.StatusNoContent, wantSent: []string{"clear qos a 1"}},
		{name: "an index past the last interface", index: "5", running: true, wantStatus: http.StatusBadRequest},
		{name: "an index that is not a number", index: "all", running: true, wantStatus: http.StatusBadRequest},
		{name: "a stopped experiment", index: "0", wantStatus: http.StatusConflict},
		{
			name: "minimega fails", index: "0", running: true, fail: "clear qos",
			wantStatus: http.StatusInternalServerError, wantSent: []string{"clear qos a 0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useVMExperiment(t, tc.running, "a")
			received := useQoSMinimega(t, tc.fail)

			server := serveAs(t, "alice", testRole("vms", "patch", "exp/a")).URL
			resp := do(t, http.MethodDelete, server+"/api/v1/experiments/exp/vms/a/interfaces/"+tc.index+"/qos", "")

			if body := readBody(t, resp); resp.StatusCode != tc.wantStatus {
				t.Fatalf("DELETE interfaces/%s/qos = %d, want %d: %s", tc.index, resp.StatusCode, tc.wantStatus, body)
			}

			sent := qosCommands(received())

			if diff := cmp.Diff(tc.wantSent, sent, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("DELETE interfaces/%s/qos commands mismatch (-want +got):\n%s", tc.index, diff)
			}
		})
	}
}

// qosCommands returns the commands sent after the first, which reads the VM,
// leaving out the minimega arguments that reading a VM may look up.
func qosCommands(received []mmtest.Command) []string {
	sent := slices.DeleteFunc(mmtest.Bases(received), func(base string) bool { return base == "args" })
	if len(sent) > 0 && sent[0] == "vm info" {
		sent = sent[1:]
	}

	return sent
}

// broadcastVMUpdate logs, and sends nothing, when it cannot read the VM or
// cannot encode it.
func TestBroadcastVMUpdateErrors(t *testing.T) {
	tests := []struct {
		name    string
		vm      string
		wantLog string
	}{
		{name: "a VM it cannot read", vm: "", wantLog: "reading changed VM"},
		{name: "a VM it cannot encode", vm: "bad\xff", wantLog: "marshaling changed VM"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := namedExperiment(t, "exp", "", diskVM("bad\xff", "a.qc2"))
			useTestStore(t, c)
			useTestMM(t, new(testMM))
			logs := captureLogs(t)

			exp, err := experiment.Get("exp")
			if err != nil {
				t.Fatalf("experiment.Get(exp) error = %v", err)
			}

			broadcastVMUpdate(exp, tc.vm)

			if got := logs()["ERROR"]; !slices.Contains(got, tc.wantLog) {
				t.Errorf("broadcastVMUpdate(exp, %q) error logs = %q, want %q", tc.vm, got, tc.wantLog)
			}
		})
	}
}

// writeJSONBody answers 500 for a payload that does not encode.
func TestWriteJSONBodyError(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()

	var webErr *weberror.WebError
	if err := writeJSONBody(w, func() {}); !errors.As(err, &webErr) || webErr.Status != http.StatusInternalServerError {
		t.Errorf("writeJSONBody(a func) error = %v, want a 500 WebError", err)
	}

	if w.Body.Len() != 0 {
		t.Errorf("writeJSONBody(a func) wrote %q, want nothing", w.Body.String())
	}
}
