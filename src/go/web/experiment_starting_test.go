package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/web/proto"
	"phenix/web/weberror"
)

// markedExperiment is the stored config of the stopped experiment name, whose status holds the start marker m.
func markedExperiment(t *testing.T, name string, m ifaces.StartMarker) store.Config {
	t.Helper()

	c := namedExperiment(t, name, "")
	c.Status = map[string]any{"starting": map[string]any{"since": m.Since, "pid": m.PID, "host": m.Host}}

	return c
}

// An experiment that another process starts, as its live start marker records, is starting with a progress of 0 in
// the experiment list and on the experiment's own page. A stale marker leaves the experiment stopped.
func TestExperimentStartingElsewhere(t *testing.T) {
	var (
		live  = ifaces.StartMarker{Since: time.Now().UTC().Format(time.RFC3339), PID: 7, Host: "elsewhere"}
		stale = ifaces.StartMarker{
			Since: time.Now().UTC().Add(-experiment.StartMarkerMaxAge - time.Minute).Format(time.RFC3339),
			PID:   7, Host: "elsewhere",
		}
	)

	useTestStore(t, markedExperiment(t, "far", live), markedExperiment(t, "stale", stale), namedExperiment(t, "idle", ""))
	useTestMM(t, new(testMM))

	server := serveAs(t, "alice", combinedRole(
		testRole("experiments", "list", "*"), testRole("experiments", "get", "*"), testRole("vms", "list", "*"),
	)).URL

	type state struct {
		Status  string
		Percent float64
	}

	var list proto.ExperimentList
	if code := getAPI(t, server+"/api/v1/experiments?vms=false", &list); code != http.StatusOK {
		t.Fatalf("GET /experiments = %d, want %d", code, http.StatusOK)
	}

	got := make(map[string]state)
	for _, exp := range list.GetExperiments() {
		got[exp.GetName()] = state{Status: exp.GetStatus(), Percent: exp.GetPercent()}
	}

	want := map[string]state{"far": {Status: "starting"}, "stale": {Status: "stopped"}, "idle": {Status: "stopped"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET /experiments statuses mismatch (-want +got):\n%s", diff)
	}

	for name, status := range map[string]string{"far": "starting", "stale": "", "idle": ""} {
		exp := new(proto.Experiment)
		if code := getAPI(t, server+"/api/v1/experiments/"+name+"?vms=false", exp); code != http.StatusOK {
			t.Fatalf("GET /experiments/%s = %d, want %d", name, code, http.StatusOK)
		}

		if exp.GetStatus() != status {
			t.Errorf("GET /experiments/%s status = %q, want %q", name, exp.GetStatus(), status)
		}
	}
}

// A start that another process runs answers 409 with the process that runs it; other start errors stay 400.
func TestStartWebErrorStartingElsewhere(t *testing.T) {
	t.Parallel()

	marker := ifaces.StartMarker{Since: "2026-11-02T10:00:00Z", PID: 7, Host: "elsewhere"}
	starting := fmt.Errorf("starting: %w", &experiment.StartingError{Name: "exp", Marker: marker})

	for err, want := range map[error]int{starting: http.StatusConflict, errors.New("minimega is down"): http.StatusBadRequest} {
		if got := startWebError(err, "unable to start experiment %s", "exp").Status; got != want {
			t.Errorf("startWebError(%v) status = %d, want %d", err, got, want)
		}
	}
}

// recoverExperiments clears a start marker that this process left before it restarted, and keeps a live marker of
// another host.
//
//nolint:paralleltest // replaces the store and minimega
func TestRecoverExperimentsClearsStaleStartMarkers(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname() error = %v", err)
	}

	var (
		now  = time.Now().UTC().Format(time.RFC3339)
		own  = ifaces.StartMarker{Since: now, PID: os.Getpid(), Host: host}
		live = ifaces.StartMarker{Since: now, PID: 7, Host: "elsewhere"}
	)

	useTestStore(t, markedExperiment(t, "left", own), markedExperiment(t, "far", live))
	useTestMM(t, new(testMM))

	recoverExperiments()

	got := make(map[string]ifaces.StartMarker)

	for _, name := range []string{"left", "far"} {
		exp, err := experiment.Get(name)
		if err != nil {
			t.Fatalf("experiment.Get(%s) error = %v", name, err)
		}

		got[name] = exp.Status.Starting()
	}

	if diff := cmp.Diff(map[string]ifaces.StartMarker{"left": {}, "far": live}, got); diff != "" {
		t.Errorf("start markers after recoverExperiments() mismatch (-want +got):\n%s", diff)
	}
}

// A start that failed because minimega gives a pinned VLAN ID to another alias has the code vlan_in_use and the ID.
func TestStartWebErrorVLANInUse(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("reading minimega script: %w", &experiment.VLANInUseError{
		ID: 102, Alias: "EXP", Err: errors.New("vlan already in use"),
	})

	got := startWebError(err, "unable to start experiment %s", "exp")
	if got.Status != http.StatusBadRequest || got.Code != weberror.CodeVLANInUse {
		t.Errorf("startWebError(%v) = %d %q, want %d %q", err, got.Status, got.Code, http.StatusBadRequest,
			weberror.CodeVLANInUse)
	}

	if diff := cmp.Diff(map[string]any{"vlan": 102}, got.Details); diff != "" {
		t.Errorf("startWebError(%v) details mismatch (-want +got):\n%s", err, diff)
	}
}
