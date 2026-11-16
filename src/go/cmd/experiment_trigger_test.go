package cmd

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/app"
)

// triggerTestIsExperiment reports whether name is a stored experiment of the
// trigger argument tests. Two of them have a lifecycle as their name.
func triggerTestIsExperiment(name string) bool {
	return slices.Contains([]string{"exp1", "running", "config"}, name)
}

func TestParseTriggerArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		cancel  bool
		want    triggerRequest
		wantErr string
	}{
		{
			name: "new order",
			args: []string{"exp1", "configure"},
			want: triggerRequest{experiment: "exp1", stage: app.ActionConfigure, apps: []string{}},
		},
		{
			name: "new order with shorthand and apps",
			args: []string{"exp1", "pre", "soh", "tap"},
			want: triggerRequest{experiment: "exp1", stage: app.ActionPreStart, apps: []string{"soh", "tap"}},
		},
		{
			name: "all experiments",
			args: []string{"all", "run"},
			want: triggerRequest{experiment: "all", stage: app.ActionRunning, apps: []string{}},
		},
		{
			name: "old order",
			args: []string{"clean", "exp2", "soh"},
			want: triggerRequest{experiment: "exp2", stage: app.ActionCleanup, apps: []string{"soh"}, legacy: true},
		},
		{
			name: "old order with all",
			args: []string{"running", "all"},
			// an experiment named running exists, so this is its "all" stage
			wantErr: `invalid lifecycle "all"`,
		},
		{
			name: "old order for all when no experiment has the lifecycle's name",
			args: []string{"post", "all"},
			want: triggerRequest{experiment: "all", stage: app.ActionPostStart, apps: []string{}, legacy: true},
		},
		{
			name: "experiment named like a lifecycle",
			args: []string{"config", "cleanup"},
			want: triggerRequest{experiment: "config", stage: app.ActionCleanup, apps: []string{}},
		},
		{
			name: "two lifecycles",
			args: []string{"pre", "post"},
			want: triggerRequest{experiment: "pre", stage: app.ActionPostStart, apps: []string{}},
		},
		{name: "no lifecycle", args: []string{"exp1", "soh"}, wantErr: `invalid lifecycle "soh"`},
		{name: "too few", args: []string{"exp1"}, wantErr: "requires at least 2 arg(s), only received 1"},
		{
			name: "cancel", args: []string{"exp1"}, cancel: true,
			want: triggerRequest{experiment: "exp1", apps: []string{}},
		},
		{
			name: "cancel apps", args: []string{"exp1", "soh", "tap"}, cancel: true,
			want: triggerRequest{experiment: "exp1", apps: []string{"soh", "tap"}},
		},
		{
			name: "cancel of an experiment named like a lifecycle", args: []string{"running", "soh"}, cancel: true,
			want: triggerRequest{experiment: "running", apps: []string{"soh"}},
		},
		{name: "cancel with a lifecycle", args: []string{"exp1", "running"}, cancel: true, wantErr: "--cancel takes no lifecycle"},
		{name: "cancel with a lifecycle first", args: []string{"clean", "exp1"}, cancel: true, wantErr: "--cancel takes no lifecycle"},
		{name: "cancel without experiment", cancel: true, wantErr: "requires at least 1 arg(s), only received 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseTriggerArgs(tt.args, tt.cancel, triggerTestIsExperiment)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseTriggerArgs(%q, %t) error = %v, want one that contains %q", tt.args, tt.cancel, err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseTriggerArgs(%q, %t) error = %v, want nil", tt.args, tt.cancel, err)
			}

			if diff := cmp.Diff(tt.want, got, triggerRequestCmp); diff != "" {
				t.Errorf("parseTriggerArgs(%q, %t) mismatch (-want +got):\n%s", tt.args, tt.cancel, diff)
			}
		})
	}
}

// triggerRequestCmp compares triggerRequests.
var triggerRequestCmp = cmp.Options{cmp.AllowUnexported(triggerRequest{}), cmpopts.EquateEmpty()} //nolint:gochecknoglobals // cmp options

// lifecycleTokens are the lifecycles and their shorthands.
func lifecycleTokens() []string {
	tokens := make([]string, 0, 2*len(app.Actions()))

	for _, a := range app.Actions() {
		tokens = append(tokens, string(a), a.Shorthand())
	}

	return tokens
}

// The new order always parses to what it names; the old order parses to the
// same request, marked legacy, unless an experiment has the lifecycle's name;
// and the command that a legacy request suggests parses to that request.
func TestParseTriggerArgsProperties(t *testing.T) {
	t.Parallel()

	var (
		lifecycle = rapid.SampledFrom(lifecycleTokens())
		name      = rapid.OneOf(
			rapid.SampledFrom([]string{"all", "exp1", "running", "config"}),
			rapid.StringMatching(`[a-z][a-z0-9_-]{0,10}`),
		)
		stored = rapid.SliceOfDistinct(name, rapid.ID[string])
	)

	rapid.Check(t, func(t *rapid.T) {
		names := stored.Draw(t, "stored")
		isExperiment := func(n string) bool { return slices.Contains(names, n) }

		stage := lifecycle.Draw(t, "stage")
		exp := name.Draw(t, "experiment")
		apps := rapid.SliceOf(rapid.StringMatching(`[a-z]{1,6}`)).Draw(t, "apps")
		want, _ := app.ParseAction(stage)

		_, expErr := app.ParseAction(exp)
		expIsLifecycle := expErr == nil

		got, err := parseTriggerArgs(append([]string{exp, stage}, apps...), false, isExperiment)

		wantReq := triggerRequest{experiment: exp, stage: want, apps: apps}
		if err != nil || !cmp.Equal(wantReq, got, triggerRequestCmp) {
			t.Fatalf("parseTriggerArgs(%q %q %q) = %+v, %v; want %+v", exp, stage, apps, got, err, wantReq)
		}

		old, err := parseTriggerArgs(append([]string{stage, exp}, apps...), false, isExperiment)

		if expIsLifecycle || isExperiment(stage) {
			// the new order: exp is the lifecycle, or the error says that it is not one
			if err == nil && old.legacy {
				t.Fatalf("parseTriggerArgs(%q %q) = %+v, want it read in the new order", stage, exp, old)
			}
		} else {
			wantReq := triggerRequest{experiment: exp, stage: want, apps: apps, legacy: true}
			if err != nil || !cmp.Equal(wantReq, old, triggerRequestCmp) {
				t.Fatalf("parseTriggerArgs(%q %q %q) = %+v, %v; want %+v", stage, exp, apps, old, err, wantReq)
			}

			suggested := strings.Fields(strings.TrimPrefix(old.newOrderCommand(), "phenix experiment trigger "))

			again, err := parseTriggerArgs(suggested, false, isExperiment)
			old.legacy = false

			if err != nil || !cmp.Equal(old, again, triggerRequestCmp) {
				t.Fatalf("parseTriggerArgs(%q) = %+v, %v; want %+v", suggested, again, err, old)
			}
		}

		canceled, err := parseTriggerArgs(append([]string{exp}, apps...), true, isExperiment)
		if err == nil && (canceled.experiment != exp || canceled.stage != "") {
			t.Fatalf("parseTriggerArgs(%q %q, cancel) = %+v, want experiment %q and no stage", exp, apps, canceled, exp)
		}
	})
}

// FuzzParseTriggerArgs checks that any arguments parse without a panic to a
// request for one of the first two arguments, with a valid lifecycle.
func FuzzParseTriggerArgs(f *testing.F) {
	for _, seed := range [][2]string{{"exp1", "configure"}, {"run", "all"}, {"pre", "post"}, {"", ""}, {"config", "x"}} {
		f.Add(seed[0], seed[1], "soh", false)
		f.Add(seed[0], seed[1], "", true)
	}

	f.Fuzz(func(t *testing.T, first, second, appName string, cancel bool) {
		args := []string{first, second}
		if appName != "" {
			args = append(args, appName)
		}

		got, err := parseTriggerArgs(args, cancel, triggerTestIsExperiment)
		if err != nil {
			return
		}

		if got.experiment != first && got.experiment != second {
			t.Errorf("parseTriggerArgs(%q, %t) experiment = %q, want one of the first two arguments", args, cancel, got.experiment)
		}

		if cancel != (got.stage == "") || !cancel && !got.stage.Valid() {
			t.Errorf("parseTriggerArgs(%q, %t) stage = %q", args, cancel, got.stage)
		}
	})
}

// experiment trigger --cancel asks the server to stop the triggers of the
// experiment, or of each stored experiment for all, and needs the server.
func TestExpCmdTriggerCancel(t *testing.T) { //nolint:paralleltest // replaces the phenix store and the socket
	tests := []struct {
		name    string
		args    []string
		want    []recordedRequest
		wantOut string
		wantErr string
	}{
		{
			name:    "every app",
			args:    []string{"stopped"},
			want:    []recordedRequest{{method: http.MethodDelete, path: "/api/v1/experiments/stopped/trigger"}},
			wantOut: "Cancel requested for every triggered app of experiment stopped\n",
		},
		{
			name: "named apps",
			args: []string{"stopped", "soh", "tap"},
			want: []recordedRequest{{
				method: http.MethodDelete, path: "/api/v1/experiments/stopped/trigger", query: url.Values{"apps": {"soh,tap"}},
			}},
			wantOut: "Cancel requested for the triggered apps soh, tap of experiment stopped\n",
		},
		{
			name: "all experiments",
			args: []string{"all"},
			want: []recordedRequest{
				{method: http.MethodDelete, path: "/api/v1/experiments/other/trigger"},
				{method: http.MethodDelete, path: "/api/v1/experiments/stopped/trigger"},
			},
			wantOut: "Cancel requested for every triggered app of experiment other\n" +
				"Cancel requested for every triggered app of experiment stopped\n",
		},
		{name: "lifecycle", args: []string{"stopped", "running"}, wantErr: "--cancel takes no lifecycle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expCmdUseStore(t, vmCmdExperiment(t, "other", false), vmCmdExperiment(t, "stopped", false))

			fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			expCmdUseSocket(t, fake.socket)

			out, err := expCmdOutput(append([]string{"trigger", "--cancel"}, tt.args...)...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("experiment trigger --cancel %q error = %v, want one that contains %q", tt.args, err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("experiment trigger --cancel %q error = %v", tt.args, err)
			}

			if out != tt.wantOut {
				t.Errorf("experiment trigger --cancel %q output = %q, want %q", tt.args, out, tt.wantOut)
			}

			got := fake.recorded()
			slices.SortFunc(got, func(a, b recordedRequest) int { return strings.Compare(a.path, b.path) })

			if diff := cmp.Diff(tt.want, got, requestFields); diff != "" {
				t.Errorf("experiment trigger --cancel %q requests mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// Without phenix ui, experiment trigger --cancel says that the server is not
// running.
func TestExpCmdTriggerCancelWithoutServer(t *testing.T) { //nolint:paralleltest // replaces the phenix store and the socket
	expCmdUseStore(t, vmCmdExperiment(t, "stopped", false))
	expCmdNoServer(t)

	err := expCmdRun("trigger", "stopped", "--cancel")
	if !errors.Is(err, errServerUnreachable) || !strings.Contains(err.Error(), "phenix ui is not running") {
		t.Errorf("experiment trigger stopped --cancel error = %v, want one that says phenix ui is not running", err)
	}
}
