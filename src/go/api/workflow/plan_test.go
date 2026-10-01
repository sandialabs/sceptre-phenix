package workflow_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// planCase is one row of a [workflow.NewPlan] table. A non-nil wantErr lists
// substrings of the expected error, which wraps wantIs, or
// [workflow.ErrConflict] when wantIs is nil.
type planCase struct {
	name     string
	spec     workflow.Spec
	mapped   []types.Experiment
	existing []string
	want     workflow.Plan
	wantErr  []string
	wantIs   error
}

// runPlanCases runs each case through [workflow.NewPlan] with branch "main".
func runPlanCases(t *testing.T, cases []planCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workflow.NewPlan(tc.spec, "main", tc.mapped, tc.existing)

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("NewPlan() unexpected error: %v", err)
				}

				if got != tc.want {
					t.Errorf("NewPlan() = %+v, want %+v", got, tc.want)
				}

				return
			}

			wantIs := tc.wantIs
			if wantIs == nil {
				wantIs = workflow.ErrConflict
			}

			if !errors.Is(err, wantIs) {
				t.Fatalf("NewPlan() error = %v, want %v", err, wantIs)
			}

			for _, part := range tc.wantErr {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("NewPlan() error = %q, want it to contain %q", err, part)
				}
			}

			if got != (workflow.Plan{}) {
				t.Errorf("NewPlan() plan = %+v, want the zero plan with an error", got)
			}
		})
	}
}

// planExp returns an experiment named name that is running when running is
// true. NewPlan does not read annotations, so none are set.
func planExp(name string, running bool) types.Experiment {
	exp := types.Experiment{Metadata: store.ConfigMetadata{Name: name}}

	if running {
		exp.Status = &v1.ExperimentStatus{StartTimeF: "2024-01-01T00:00:00Z"}
	}

	return exp
}

// experimentNames returns the name of each experiment, in order.
func experimentNames(exps []types.Experiment) []string {
	names := make([]string, 0, len(exps))

	for _, exp := range exps {
		names = append(names, exp.Metadata.Name)
	}

	return names
}

// unmappedPlanCases returns the rows of a branch that no experiment is
// mapped to.
func unmappedPlanCases() []planCase {
	return []planCase{
		{
			name: "auto missing",
			spec: workflow.Spec{},
			want: workflow.Plan{Action: workflow.ActionNone, Reason: "no experiment mapped and auto.create not set"},
		},
		{
			name:     "auto.create empty",
			spec:     workflow.Spec{Auto: &workflow.Auto{Update: boolPtr(true), Restart: boolPtr(false)}},
			existing: []string{"foo"},
			want:     workflow.Plan{Action: workflow.ActionNone, Reason: "no experiment mapped and auto.create not set"},
		},
		{
			name: "restart defaults to true",
			spec: workflow.Spec{Auto: &workflow.Auto{Create: "foo"}},
			want: workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo"},
		},
		{
			name:     "restart true, other experiments exist",
			spec:     workflow.Spec{Auto: &workflow.Auto{Create: "foo", Restart: boolPtr(true)}},
			existing: []string{"bar"},
			want:     workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo"},
		},
		{
			name:     "existing match is case-sensitive",
			spec:     workflow.Spec{Auto: &workflow.Auto{Create: "foo"}},
			existing: []string{"Foo"},
			want:     workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo"},
		},
		{
			name: "restart false",
			spec: workflow.Spec{Auto: &workflow.Auto{Create: "foo", Restart: boolPtr(false)}},
			want: workflow.Plan{Action: workflow.ActionCreate, Experiment: "foo"},
		},
		{
			name: "update false does not block a create",
			spec: workflow.Spec{Auto: &workflow.Auto{Create: "foo", Update: boolPtr(false)}},
			want: workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo"},
		},
		{
			name:     "auto.create names an existing unmapped experiment",
			spec:     workflow.Spec{Auto: &workflow.Auto{Create: "foo"}},
			existing: []string{"bar", "foo"},
			wantErr:  []string{`workflow conflict: experiment "foo" already exists and is not mapped to workflow branch "main"`},
		},
		{
			name:     "conflict wins over restart false",
			spec:     workflow.Spec{Auto: &workflow.Auto{Create: "foo", Restart: boolPtr(false)}},
			existing: []string{"foo"},
			wantErr:  []string{`experiment "foo" already exists`},
		},
		{
			name: "auto.create uses every character a config name may hold",
			spec: workflow.Spec{Auto: &workflow.Auto{Create: "Az09_@.-"}},
			want: workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "Az09_@.-"},
		},
		{
			name:    "auto.create holds a space and a bang",
			spec:    workflow.Spec{Auto: &workflow.Auto{Create: "bad name!"}},
			wantErr: []string{`invalid workflow config: auto.create "bad name!" is not a valid experiment name`},
			wantIs:  workflow.ErrInvalidSpec,
		},
		{
			name:    "auto.create holds a slash",
			spec:    workflow.Spec{Auto: &workflow.Auto{Create: "a/b"}},
			wantErr: []string{`invalid workflow config: auto.create "a/b" is not a valid experiment name`},
			wantIs:  workflow.ErrInvalidSpec,
		},
		{
			name:    "auto.create holds a letter that is not ASCII",
			spec:    workflow.Spec{Auto: &workflow.Auto{Create: "café"}},
			wantErr: []string{`invalid workflow config: auto.create "café" is not a valid experiment name`},
			wantIs:  workflow.ErrInvalidSpec,
		},
		{
			name:    "auto.create is the reserved name all",
			spec:    workflow.Spec{Auto: &workflow.Auto{Create: "all"}},
			wantErr: []string{`invalid workflow config: auto.create "all" is not a valid experiment name`},
			wantIs:  workflow.ErrInvalidSpec,
		},
		{
			name:     "the reserved name in another letter case, even when it exists",
			spec:     workflow.Spec{Auto: &workflow.Auto{Create: "ALL", Restart: boolPtr(false)}},
			existing: []string{"ALL"},
			wantErr:  []string{`invalid workflow config: auto.create "ALL" is not a valid experiment name`},
			wantIs:   workflow.ErrInvalidSpec,
		},
		{
			name: "auto.create holds the reserved name and more",
			spec: workflow.Spec{Auto: &workflow.Auto{Create: "all-hands"}},
			want: workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "all-hands"},
		},
	}
}

func TestNewPlanUnmapped(t *testing.T) {
	runPlanCases(t, unmappedPlanCases())
}

// mappedPlanCases returns the rows of a branch that one experiment is mapped
// to.
func mappedPlanCases() []planCase {
	stopped := []types.Experiment{planExp("foo", false)}
	running := []types.Experiment{planExp("foo", true)}

	return []planCase{
		{
			name:   "update false, stopped",
			spec:   workflow.Spec{Auto: &workflow.Auto{Update: boolPtr(false)}},
			mapped: stopped,
			want:   workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "auto.update is false"},
		},
		{
			name:   "update false, running, restart true",
			spec:   workflow.Spec{Auto: &workflow.Auto{Update: boolPtr(false), Restart: boolPtr(true)}},
			mapped: running,
			want:   workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "auto.update is false"},
		},
		{
			name:   "update false, stopped, restart false",
			spec:   workflow.Spec{Auto: &workflow.Auto{Update: boolPtr(false), Restart: boolPtr(false)}},
			mapped: stopped,
			want:   workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "auto.update is false"},
		},
		{
			name:   "running, restart false",
			spec:   workflow.Spec{Auto: &workflow.Auto{Restart: boolPtr(false)}},
			mapped: running,
			want:   workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "running and auto.restart is false"},
		},
		{
			name:   "running, update true, restart false",
			spec:   workflow.Spec{Auto: &workflow.Auto{Update: boolPtr(true), Restart: boolPtr(false)}},
			mapped: running,
			want:   workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "running and auto.restart is false"},
		},
		{
			name:   "running, defaults",
			spec:   workflow.Spec{},
			mapped: running,
			want:   workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo"},
		},
		{
			name:   "running, update and restart true",
			spec:   workflow.Spec{Auto: &workflow.Auto{Update: boolPtr(true), Restart: boolPtr(true)}},
			mapped: running,
			want:   workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo"},
		},
		{
			name:   "stopped, defaults",
			spec:   workflow.Spec{},
			mapped: stopped,
			want:   workflow.Plan{Action: workflow.ActionUpdateAndStart, Experiment: "foo"},
		},
		{
			name:   "status without a start time is stopped",
			spec:   workflow.Spec{},
			mapped: []types.Experiment{{Metadata: store.ConfigMetadata{Name: "foo"}, Status: &v1.ExperimentStatus{}}},
			want:   workflow.Plan{Action: workflow.ActionUpdateAndStart, Experiment: "foo"},
		},
		{
			name:   "stopped, restart false",
			spec:   workflow.Spec{Auto: &workflow.Auto{Restart: boolPtr(false)}},
			mapped: stopped,
			want:   workflow.Plan{Action: workflow.ActionUpdate, Experiment: "foo"},
		},
		{
			name:     "mapped experiment is also listed as existing",
			spec:     workflow.Spec{Auto: &workflow.Auto{Create: "foo"}},
			mapped:   stopped,
			existing: []string{"foo"},
			want:     workflow.Plan{Action: workflow.ActionUpdateAndStart, Experiment: "foo"},
		},
		{
			name:     "auto.create is ignored once an experiment is mapped",
			spec:     workflow.Spec{Auto: &workflow.Auto{Create: "bar"}},
			mapped:   running,
			existing: []string{"bar", "foo"},
			want:     workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo"},
		},
		{
			name:   "an auto.create that is not a valid experiment name is ignored once an experiment is mapped",
			spec:   workflow.Spec{Auto: &workflow.Auto{Create: "bad name!"}},
			mapped: stopped,
			want:   workflow.Plan{Action: workflow.ActionUpdateAndStart, Experiment: "foo"},
		},
		{
			name:   "the reserved name is ignored once an experiment is mapped",
			spec:   workflow.Spec{Auto: &workflow.Auto{Create: "all"}},
			mapped: running,
			want:   workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo"},
		},
	}
}

func TestNewPlanMapped(t *testing.T) {
	runPlanCases(t, mappedPlanCases())
}

// multipleMappedPlanCases returns the rows of a branch that more than one
// experiment is mapped to.
func multipleMappedPlanCases() []planCase {
	two := []types.Experiment{planExp("foo", true), planExp("bar", false)}

	return []planCase{
		{
			name:    "defaults",
			spec:    workflow.Spec{},
			mapped:  two,
			wantErr: []string{`workflow conflict: 2 experiments are mapped to workflow branch "main" (bar, foo); at most one is allowed`},
		},
		{
			name:    "update false",
			spec:    workflow.Spec{Auto: &workflow.Auto{Update: boolPtr(false)}},
			mapped:  two,
			wantErr: []string{"(bar, foo)"},
		},
		{
			name:    "auto.create set, restart false",
			spec:    workflow.Spec{Auto: &workflow.Auto{Create: "baz", Restart: boolPtr(false)}},
			mapped:  []types.Experiment{planExp("foo", true), planExp("bar", false), planExp("baz", false)},
			wantErr: []string{"3 experiments", "(bar, baz, foo)"},
		},
	}
}

func TestNewPlanMultipleMapped(t *testing.T) {
	runPlanCases(t, multipleMappedPlanCases())
}

// TestNewPlanNameCheckMatchesSchema runs every auto.create name of the plan
// rows through the config schema, as the metadata.name of a minimal
// Experiment config, and checks that NewPlan refuses a name exactly when the
// schema does. The reserved name all is the one exception: the schema
// accepts it and only experiment.Create refuses it, in any letter case.
func TestNewPlanNameCheckMatchesSchema(t *testing.T) {
	var names []string

	for _, tc := range slices.Concat(unmappedPlanCases(), mappedPlanCases(), multipleMappedPlanCases()) {
		if name := tc.spec.ExperimentName(); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	for _, name := range names {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			// With no experiment mapped and none existing, the name is all
			// that NewPlan can refuse.
			_, planErr := workflow.NewPlan(workflow.Spec{Auto: &workflow.Auto{Create: name}}, "main", nil, nil)
			if planErr != nil && !errors.Is(planErr, workflow.ErrInvalidSpec) {
				t.Fatalf("NewPlan() error = %v, want nil or ErrInvalidSpec", planErr)
			}

			cfg := store.Config{
				Version:  "phenix.sandia.gov/v1",
				Kind:     "Experiment",
				Metadata: store.ConfigMetadata{Name: name},
				Spec:     map[string]any{"scenario": "foo"},
			}

			schemaErr := types.ValidateConfig(cfg)
			if schemaErr != nil && !errors.Is(schemaErr, types.ErrValidationFailed) {
				t.Fatalf("ValidateConfig() error = %v, want nil or ErrValidationFailed", schemaErr)
			}

			planRefuses, schemaRefuses := planErr != nil, schemaErr != nil

			if strings.EqualFold(name, "all") {
				if !planRefuses || schemaRefuses {
					t.Errorf("reserved name: NewPlan refuses = %v, schema refuses = %v; want true and false", planRefuses, schemaRefuses)
				}

				return
			}

			if planRefuses != schemaRefuses {
				t.Errorf("NewPlan refuses = %v (%v), schema refuses = %v (%v); want the same verdict",
					planRefuses, planErr, schemaRefuses, schemaErr)
			}
		})
	}

	// The rows hold names that each side accepts and names that each side
	// refuses, so the comparison is not empty.
	if len(names) < 4 {
		t.Errorf("found %d auto.create names in the plan rows, want at least 4", len(names))
	}
}

func TestMapped(t *testing.T) {
	annotated := func(name string, annotations store.Annotations) types.Experiment {
		return types.Experiment{Metadata: store.ConfigMetadata{Name: name, Annotations: annotations}}
	}

	exps := []types.Experiment{
		annotated("unannotated", nil),
		annotated("other-keys", store.Annotations{"topology": "topo"}),
		annotated("a", store.Annotations{workflow.BranchAnnotation: "main"}),
		annotated("on-dev", store.Annotations{workflow.BranchAnnotation: "dev"}),
		annotated("empty", store.Annotations{workflow.BranchAnnotation: ""}),
		annotated("b", store.Annotations{workflow.BranchAnnotation: "main", "topology": "topo"}),
	}

	cases := []struct {
		name   string
		exps   []types.Experiment
		branch string
		want   []string
	}{
		{name: "nil input", exps: nil, branch: "main", want: []string{}},
		{name: "keeps every match in order", exps: exps, branch: "main", want: []string{"a", "b"}},
		{name: "another branch", exps: exps, branch: "dev", want: []string{"on-dev"}},
		{name: "no match", exps: exps, branch: "release", want: []string{}},
		{name: "branch match is case-sensitive", exps: exps, branch: "Main", want: []string{}},
		{name: "empty branch matches only an empty annotation", exps: exps, branch: "", want: []string{"empty"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := workflow.Mapped(tc.exps, tc.branch)
			if mapped == nil {
				t.Fatalf("Mapped(%q) = nil, want a non-nil slice", tc.branch)
			}

			if got := experimentNames(mapped); !slices.Equal(got, tc.want) {
				t.Errorf("Mapped(%q) = %v, want %v", tc.branch, got, tc.want)
			}
		})
	}
}

func TestResultJSON(t *testing.T) {
	cases := []struct {
		name   string
		result workflow.Result
		want   string
	}{
		{
			name: "restart",
			result: workflow.Result{
				Plan:   workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo", Reason: ""},
				DryRun: false,
			},
			want: `{"action":"restart","experiment":"foo","reason":"","dryRun":false}`,
		},
		{
			name: "dry run of a none plan",
			result: workflow.Result{
				Plan:   workflow.Plan{Action: workflow.ActionNone, Experiment: "", Reason: "auto.update is false"},
				DryRun: true,
			},
			want: `{"action":"none","experiment":"","reason":"auto.update is false","dryRun":true}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatalf("json.Marshal() error: %v", err)
			}

			if string(body) != tc.want {
				t.Errorf("json.Marshal() = %s, want %s", body, tc.want)
			}

			var decoded workflow.Result
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("json.Unmarshal() error: %v", err)
			}

			if decoded != tc.result {
				t.Errorf("round trip = %+v, want %+v", decoded, tc.result)
			}
		})
	}
}

func TestConfigResultJSON(t *testing.T) {
	cases := []struct {
		name   string
		result workflow.ConfigResult
		want   string
	}{
		{
			name:   "create dry run",
			result: workflow.ConfigResult{Action: workflow.ActionCreate, Kind: "Topology", Name: "main-topo", DryRun: true},
			want:   `{"action":"create","kind":"Topology","name":"main-topo","dryRun":true}`,
		},
		{
			name:   "update dry run",
			result: workflow.ConfigResult{Action: workflow.ActionUpdate, Kind: "Scenario", Name: "main-scenario", DryRun: true},
			want:   `{"action":"update","kind":"Scenario","name":"main-scenario","dryRun":true}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatalf("json.Marshal() error: %v", err)
			}

			if string(body) != tc.want {
				t.Errorf("json.Marshal() = %s, want %s", body, tc.want)
			}

			var decoded workflow.ConfigResult
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("json.Unmarshal() error: %v", err)
			}

			if decoded != tc.result {
				t.Errorf("round trip = %+v, want %+v", decoded, tc.result)
			}
		})
	}
}

func TestParseAction(t *testing.T) {
	valid := []struct {
		text string
		want workflow.Action
	}{
		{text: "none", want: workflow.ActionNone},
		{text: "create", want: workflow.ActionCreate},
		{text: "createAndStart", want: workflow.ActionCreateAndStart},
		{text: "update", want: workflow.ActionUpdate},
		{text: "updateAndStart", want: workflow.ActionUpdateAndStart},
		{text: "restart", want: workflow.ActionRestart},
	}

	for _, tc := range valid {
		if string(tc.want) != tc.text {
			t.Errorf("action constant = %q, want the API value %q", tc.want, tc.text)
		}

		got, err := workflow.ParseAction(tc.text)
		if err != nil {
			t.Errorf("ParseAction(%q) unexpected error: %v", tc.text, err)

			continue
		}

		if got != tc.want {
			t.Errorf("ParseAction(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}

	invalid := []string{"", "bogus", "Restart", "RESTART", " restart", "restart ", "create-and-start", "createandstart"}

	for _, text := range invalid {
		got, err := workflow.ParseAction(text)
		if !errors.Is(err, workflow.ErrUnknownAction) {
			t.Errorf("ParseAction(%q) error = %v, want ErrUnknownAction", text, err)

			continue
		}

		if !strings.Contains(err.Error(), strconv.Quote(text)) {
			t.Errorf("ParseAction(%q) error = %q, want it to quote the input", text, err)
		}

		if got != "" {
			t.Errorf("ParseAction(%q) = %q, want the empty action with an error", text, got)
		}
	}
}

func TestCheckExpected(t *testing.T) {
	restart := workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo", Reason: ""}
	none := workflow.Plan{Action: workflow.ActionNone, Experiment: "", Reason: "no experiment mapped and auto.create not set"}

	cases := []struct {
		name    string
		plan    workflow.Plan
		expect  string
		wantErr error
		wantMsg string
	}{
		{name: "no expectation accepts restart", plan: restart, expect: ""},
		{name: "no expectation accepts none", plan: none, expect: ""},
		{name: "same action", plan: restart, expect: "restart"},
		{name: "same none action", plan: none, expect: "none"},
		{
			name:    "experiment started after the dry run",
			plan:    restart,
			expect:  "updateAndStart",
			wantErr: workflow.ErrPlanChanged,
			wantMsg: `workflow plan changed: expected action "updateAndStart", but the plan is now "restart"`,
		},
		{
			name:    "experiment unmapped after the dry run",
			plan:    none,
			expect:  "update",
			wantErr: workflow.ErrPlanChanged,
			wantMsg: `expected action "update", but the plan is now "none"`,
		},
		{
			name:    "unknown action",
			plan:    restart,
			expect:  "bogus",
			wantErr: workflow.ErrUnknownAction,
			wantMsg: `unknown workflow action "bogus"`,
		},
		{
			name:    "action match is case-sensitive",
			plan:    restart,
			expect:  "Restart",
			wantErr: workflow.ErrUnknownAction,
			wantMsg: `"Restart"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := workflow.CheckExpected(tc.plan, tc.expect)

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckExpected() unexpected error: %v", err)
				}

				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckExpected() error = %v, want %v", err, tc.wantErr)
			}

			if errors.Is(err, workflow.ErrUnknownAction) && errors.Is(err, workflow.ErrPlanChanged) {
				t.Errorf("CheckExpected() error = %v wraps both ErrUnknownAction and ErrPlanChanged", err)
			}

			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("CheckExpected() error = %q, want it to contain %q", err, tc.wantMsg)
			}
		})
	}
}
