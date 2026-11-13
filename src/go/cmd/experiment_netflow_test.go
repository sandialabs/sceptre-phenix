package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/util/common"
)

// runSocketCmd runs cmd with args against the server at socket, and returns its output and error.
func runSocketCmd(t *testing.T, socket string, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	original := common.UnixSocket
	common.UnixSocket = socket //nolint:reassign // point the command at the fake server

	t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

// netflowTestID is the ID of the netflow capture of the fake server.
const netflowTestID = "20261028T153012.345Z"

// fakeNetflowServer serves the netflow routes of exp1: the capture runs once it was started.
func fakeNetflowServer(t *testing.T) *fakeServer {
	t.Helper()

	var running atomic.Bool

	return newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/experiments/exp1/netflow":
			running.Store(true)

			w.WriteHeader(http.StatusNoContent)
		case "DELETE /api/v1/experiments/exp1/netflow":
			running.Store(false)

			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/experiments/exp1/netflow":
			if !running.Load() {
				w.WriteHeader(http.StatusNotFound)
				writeJSONBody(w, map[string]string{"message": "no netflow capture is running"})

				return
			}

			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/experiments/exp1/netflow/captures":
			flows := int64(3)
			writeJSONBody(w, map[string]any{"captures": []experiment.NetflowCapture{{
				ID: netflowTestID, Started: time.Date(2026, 10, 28, 15, 30, 12, 0, time.UTC), State: "stopped",
				Size: 2048, Flows: &flows,
			}}})
		case "GET /api/v1/experiments/exp1/netflow/captures/" + netflowTestID:
			_, _ = w.Write([]byte("format " + r.URL.Query().Get("format")))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
}

func TestExperimentNetflow(t *testing.T) {
	fake := fakeNetflowServer(t)

	steps := []struct {
		args []string
		want string
	}{
		{args: []string{"status", "exp1"}, want: "is not running"},
		{args: []string{"start", "exp1"}, want: "Started the netflow capture of experiment exp1"},
		{args: []string{"status", "exp1"}, want: "is running"},
		{args: []string{"stop", "exp1"}, want: "Stopped the netflow capture of experiment exp1"},
		{args: []string{"captures", "exp1"}, want: netflowTestID},
		{args: []string{"download", "exp1", netflowTestID, "--format", "csv"}, want: "format csv"},
	}

	for _, step := range steps {
		out, err := runSocketCmd(t, fake.socket, newExperimentNetflowCmd(), step.args...)
		if err != nil || !strings.Contains(out, step.want) {
			t.Errorf("netflow %q = %q, %v, want it to contain %q", step.args, out, err, step.want)
		}
	}

	local := filepath.Join(t.TempDir(), "flows.txt")

	if _, err := runSocketCmd(t, fake.socket, newExperimentNetflowCmd(), "download", "exp1", netflowTestID, "-O", local); err != nil {
		t.Fatalf("netflow download -o %s error = %v", local, err)
	}

	if b, _ := os.ReadFile(local); string(b) != "format text" {
		t.Errorf("netflow download -o wrote %q, want %q", b, "format text")
	}

	want := []recordedRequest{
		{method: http.MethodGet, path: "/api/v1/experiments/exp1/netflow"},
		{method: http.MethodPost, path: "/api/v1/experiments/exp1/netflow"},
		{method: http.MethodGet, path: "/api/v1/experiments/exp1/netflow"},
		{method: http.MethodDelete, path: "/api/v1/experiments/exp1/netflow"},
		{method: http.MethodGet, path: "/api/v1/experiments/exp1/netflow/captures"},
		{
			method: http.MethodGet, path: "/api/v1/experiments/exp1/netflow/captures/" + netflowTestID,
			query: url.Values{"format": {"csv"}},
		},
		{
			method: http.MethodGet, path: "/api/v1/experiments/exp1/netflow/captures/" + netflowTestID,
			query: url.Values{"format": {"text"}},
		},
	}
	if diff := cmp.Diff(want, fake.recorded(), requestFields); diff != "" {
		t.Errorf("netflow requests mismatch (-want +got):\n%s", diff)
	}
}

func TestExperimentNetflowErrors(t *testing.T) {
	fake := fakeNetflowServer(t)

	if _, err := runSocketCmd(t, fake.socket, newExperimentNetflowCmd(),
		"download", "exp1", netflowTestID, "--format", "pcap"); !errors.Is(err, experiment.ErrInvalidNetflowFormat) {
		t.Errorf("netflow download --format pcap error = %v, want %v", err, experiment.ErrInvalidNetflowFormat)
	}

	if _, err := runSocketCmd(t, fake.socket, newExperimentNetflowCmd(),
		"download", "exp1", "../latest"); !errors.Is(err, experiment.ErrInvalidNetflowCapture) {
		t.Errorf("netflow download ../latest error = %v, want %v", err, experiment.ErrInvalidNetflowCapture)
	}

	if got := fake.recorded(); len(got) != 0 {
		t.Errorf("netflow commands with invalid arguments sent %d requests, want none", len(got))
	}

	_, err := runSocketCmd(t, fake.socket, newExperimentNetflowCmd(), "start", "exp2")
	if status := apiStatus(err); status != http.StatusInternalServerError {
		t.Errorf("netflow start exp2 error = %v (status %d), want the server's %d", err, status, http.StatusInternalServerError)
	}

	socket := shortSocketPath(t)

	_, err = runSocketCmd(t, socket, newExperimentNetflowCmd(), "status", "exp1")
	if !errors.Is(err, errServerUnreachable) || !strings.Contains(err.Error(), "phenix ui is not running") {
		t.Errorf("netflow status without a server error = %v, want %v saying phenix ui is not running", err, errServerUnreachable)
	}
}
