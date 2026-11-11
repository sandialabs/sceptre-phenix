package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/api/experiment"
	"phenix/api/scorch"
	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/store"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/testenv"
)

// utilInputMesh answers the commands that read the cluster hosts of the
// headnode head and the mesh peers compute1 and compute2, with their disk
// usage, and the minimega version that a SCORCH run records. The peers do
// not answer when silent is set.
type utilInputMesh struct {
	silent bool
}

func (f utilInputMesh) handle(cmd mmtest.Command) []*minicli.Response {
	header := []string{"name", "cpus", "memtotal", "vms"}

	switch {
	case cmd.Base == "version":
		return []*minicli.Response{mmtest.Text("head", "minimega 3.4.0 abc 2026-10-01")}
	case cmd.Base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular("head", header, []string{"head", "8", "8192", "0"})}
	case cmd.Base == "mesh list peers":
		return []*minicli.Response{mmtest.Text("head", "compute1,compute2")}
	case cmd.Base == "mesh send all host" && f.silent:
		return nil
	case cmd.Base == "mesh send all host":
		return []*minicli.Response{
			mmtest.Tabular("compute1", header, []string{"compute1", "16", "65536", "0"}),
			mmtest.Tabular("compute2", header, []string{"compute2", "16", "65536", "0"}),
		}
	case strings.HasPrefix(cmd.Base, "mesh send all shell "):
		return []*minicli.Response{mmtest.Text("compute1", "0=11% 1=12%\n"), mmtest.Text("compute2", "0=21% 1=22%\n")}
	case strings.HasPrefix(cmd.Base, "shell "):
		return []*minicli.Response{mmtest.Text("head", "0=1% 1=2%\n")}
	}

	return nil
}

// utilInputUseMesh points minimega at a fake mesh f until the test ends.
func utilInputUseMesh(t *testing.T, f utilInputMesh) {
	t.Helper()

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)
	utilCmdUseMM(t, mm.Minimega{})
	mmtest.Use(t, f.handle)
}

// utilInputStored returns the stored config of the experiment exp.
func utilInputStored(t *testing.T) store.Config {
	t.Helper()

	c, _ := store.NewConfig("experiment/exp")
	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(experiment/exp) error = %v", err)
	}

	return *c
}

// utilInputProgram puts the program prefix+name first on PATH until the test
// ends. It saves its stdin and its arguments in the returned directory, in
// <args joined by "-">.json and .args, and then runs body.
func utilInputProgram(t *testing.T, prefix, name, body string) string {
	t.Helper()

	testenv.RequireCommands(t, "sh", "cat", "printf", "mv")

	dir := t.TempDir()
	script := fmt.Sprintf(
		"#!/bin/sh\nf=\"%[1]s/$(IFS=-; echo \"$*\")\"\ncat > \"$f.json\"\nprintf '%%s\\n' \"$@\" > \"$f.args\"\n%[2]s\n",
		dir, body,
	)

	if err := os.WriteFile(filepath.Join(dir, prefix+name), []byte(script), 0o700); err != nil {
		t.Fatalf("writing %s%s: %v", prefix, name, err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return dir
}

// utilInputSaved returns what the program of utilInputProgram saved in dir
// for the call with args.
func utilInputSaved(t *testing.T, dir string, args ...string) ([]byte, []string) {
	t.Helper()

	base := dir + string(filepath.Separator) + strings.Join(args, "-")

	stdin, err := os.ReadFile(base + ".json")
	if err != nil {
		t.Fatalf("reading the stdin of the call %q: %v", args, err)
	}

	argText, err := os.ReadFile(base + ".args")
	if err != nil {
		t.Fatalf("reading the arguments of the call %q: %v", args, err)
	}

	return stdin, strings.Split(strings.TrimSuffix(string(argText), "\n"), "\n")
}

// utilInputBytesDiff returns "" when out is stdin and a newline, as phenix util
// scheduler-json and scorch-json print it, else a diff of their decoded data,
// or of their text when the data is the same.
func utilInputBytesDiff(t *testing.T, stdin []byte, out string) string {
	t.Helper()

	want := string(stdin) + "\n"
	if out == want {
		return ""
	}

	if diff := cmp.Diff(utilInputDecode(t, want), utilInputDecode(t, out)); diff != "" {
		return diff
	}

	return cmp.Diff(want, out)
}

// utilInputDecode decodes the JSON or YAML document doc.
func utilInputDecode(t *testing.T, doc string) any {
	t.Helper()

	var v any
	if err := yaml.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("decoding %q: %v", doc, err)
	}

	return v
}

// phenix util scheduler-json prints the bytes that a user scheduler gets on
// stdin when phenix schedules the experiment, and stores nothing, also for
// an experiment stored without schedules. -o yaml prints the same data.
func TestUtilCmdSchedulerJSON(t *testing.T) { //nolint:paralleltest // replaces the phenix store, minimega and PATH
	cfg := vmCmdExperiment(t, "exp", false)
	delete(cfg.Spec, "schedules")

	expCmdUseStore(t, cfg)
	utilInputUseMesh(t, utilInputMesh{})

	dir := utilInputProgram(t, "phenix-scheduler-", "saver", `echo '{}'`)

	before := utilInputStored(t)

	out, err := utilCmdRun("scheduler-json", "exp")
	if err != nil {
		t.Fatalf("phenix util scheduler-json exp error = %v", err)
	}

	if diff := cmp.Diff(before, utilInputStored(t)); diff != "" {
		t.Errorf("phenix util scheduler-json exp changed the stored experiment (-want +got):\n%s", diff)
	}

	yamlOut, err := utilCmdRun("scheduler-json", "exp", "-o", "yaml")
	if err != nil {
		t.Fatalf("phenix util scheduler-json exp -o yaml error = %v", err)
	}

	if diff := cmp.Diff(utilInputDecode(t, out), utilInputDecode(t, yamlOut)); diff != "" {
		t.Errorf("phenix util scheduler-json exp -o yaml mismatch with -o json (-want +got):\n%s", diff)
	}

	if err := expCmdRun("schedule", "exp", "saver"); err != nil {
		t.Fatalf("phenix experiment schedule exp saver error = %v", err)
	}

	stdin, _ := utilInputSaved(t, dir)

	if diff := utilInputBytesDiff(t, stdin, out); diff != "" {
		t.Errorf("phenix util scheduler-json exp is not the stdin of the user scheduler (-want +got):\n%s", diff)
	}
}

// phenix util scheduler-json returns a humanized error, and prints nothing,
// for a missing experiment, for mesh peers that do not answer and when the
// host read does not end in time.
func TestUtilCmdSchedulerJSONErrors(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	tests := []struct {
		name      string
		ctx       context.Context //nolint:containedctx // the context of the command
		exp       string
		silent    bool
		humanized string
	}{
		{name: "missing experiment", ctx: context.Background(), exp: "missing", humanized: "Unable to get the missing experiment"},
		{
			name: "silent peers", ctx: context.Background(), exp: "exp", silent: true,
			humanized: "Unable to read the cluster hosts: the mesh peers did not answer",
		},
		{name: "deadline", ctx: expired, exp: "exp", humanized: "Unable to read the cluster hosts within 2m0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expCmdUseStore(t, vmCmdExperiment(t, "exp", false))
			utilInputUseMesh(t, utilInputMesh{silent: tt.silent})

			out, _, err := utilCmdOutputs(tt.ctx, "scheduler-json", tt.exp)
			if msg := expCmdCheckErr(err, "", tt.humanized); msg != "" {
				t.Errorf("phenix util scheduler-json %s %s", tt.exp, msg)
			}

			if out != "" {
				t.Errorf("phenix util scheduler-json %s output = %q, want none", tt.exp, out)
			}
		})
	}
}

// utilInputScorchExperiment returns the stored config of the running
// experiment exp, whose scorch app runs the component probe of type fake in
// the start stage of run 0 and in the stop stage of its loop, which has two
// passes. Run 1 has the tap component mark before probe.
func utilInputScorchExperiment(t *testing.T) store.Config {
	t.Helper()

	cfg := vmCmdExperiment(t, "exp", true)
	cfg.Spec["scenario"] = map[string]any{"apps": []any{map[string]any{
		"name": "scorch",
		"metadata": map[string]any{
			"components": []any{
				map[string]any{
					"name": "probe", "type": "fake",
					"metadata": map[string]any{"msg": "{{X}} and {{Y}}", "n": "{{N}}"},
				},
				map[string]any{"name": "other", "type": "fake", "metadata": map[string]any{"keep": true}},
				map[string]any{"name": "mark", "type": "tap", "metadata": map[string]any{}},
			},
			"runs": []any{
				map[string]any{
					"replace": map[string]any{"{{X}}": []any{"fixed"}},
					"start":   []any{"probe"},
					"loop": map[string]any{
						"count":   2,
						"replace": map[string]any{"{{Y}}": []any{"inner"}, "{{N}}": []any{7}},
						"stop":    []any{"probe"},
					},
				},
				map[string]any{"start": []any{"mark", "probe"}},
			},
		},
	}}}

	return cfg
}

// utilInputScorchArgs returns the arguments that the stderr of phenix util
// scorch-json names.
func utilInputScorchArgs(stderr string) []string {
	for line := range strings.SplitSeq(stderr, "\n") {
		if args, ok := strings.CutPrefix(line, "Arguments: "); ok {
			return strings.Fields(args)
		}
	}

	return nil
}

// phenix util scorch-json prints the bytes that the program of a SCORCH
// component gets on stdin in a SCORCH run, at the run, loop and count that
// its flags select, prints the arguments of the program on stderr, and
// stores nothing.
func TestUtilCmdScorchJSON(t *testing.T) { //nolint:paralleltest // replaces the phenix store, minimega and PATH
	expCmdUseStore(t, utilInputScorchExperiment(t))
	utilInputUseMesh(t, utilInputMesh{})
	expCmdNoServer(t)

	dir := utilInputProgram(t, "phenix-scorch-component-", "fake", "")

	tests := []struct {
		args     []string
		wantArgs []string
	}{
		{args: nil, wantArgs: []string{"start", "probe", "0", "0", "0"}},
		{args: []string{"--loop", "1", "--count", "1"}, wantArgs: []string{"stop", "probe", "0", "1", "1"}},
		{args: []string{"--run", "0", "--stage", "stop"}, wantArgs: []string{"stop", "probe", "0", "1", "0"}},
	}

	before := utilInputStored(t)
	outs := make([]string, len(tests))

	for i, tt := range tests {
		args := append([]string{"scorch-json", "exp", "probe"}, tt.args...)

		out, stderr, err := utilCmdOutputs(context.Background(), args...)
		if err != nil {
			t.Fatalf("phenix util %s error = %v", strings.Join(args, " "), err)
		}

		if diff := cmp.Diff(tt.wantArgs, utilInputScorchArgs(stderr)); diff != "" {
			t.Errorf("phenix util %s arguments mismatch (-want +got):\n%s", strings.Join(args, " "), diff)
		}

		outs[i] = out
	}

	if diff := cmp.Diff(before, utilInputStored(t)); diff != "" {
		t.Errorf("phenix util scorch-json changed the stored experiment (-want +got):\n%s", diff)
	}

	if err := expCmdRun("scorch", "exp", "--run", "0"); err != nil {
		t.Fatalf("phenix experiment scorch exp --run 0 error = %v", err)
	}

	for i, tt := range tests {
		stdin, args := utilInputSaved(t, dir, tt.wantArgs...)

		if diff := utilInputBytesDiff(t, stdin, outs[i]); diff != "" {
			t.Errorf("phenix util scorch-json exp probe %q is not the stdin of the component (-want +got):\n%s", tt.args, diff)
		}

		if diff := cmp.Diff(tt.wantArgs, args); diff != "" {
			t.Errorf("the component got arguments that differ from the test case (-want +got):\n%s", diff)
		}
	}
}

// phenix util scorch-json --runtime prints the bytes that the program of a
// SCORCH component gets on stdin when an external app added the runtime
// details earlier in the same app stage: the input without --runtime, with
// the hosts and the VMs.
func TestUtilCmdScorchJSONRuntime(t *testing.T) { //nolint:paralleltest // replaces the phenix store, minimega and PATH
	expCmdUseStore(t, utilInputScorchExperiment(t))
	utilCmdUseMM(t, utilCmdMM{})

	dir := utilInputProgram(t, "phenix-scorch-component-", "fake", "")

	plain, err := utilCmdRun("scorch-json", "exp", "probe", "-o", "json")
	if err != nil {
		t.Fatalf("phenix util scorch-json exp probe error = %v", err)
	}

	out, err := utilCmdRun("scorch-json", "exp", "probe", "--runtime", "-o", "json")
	if err != nil {
		t.Fatalf("phenix util scorch-json exp probe --runtime error = %v", err)
	}

	exp, err := experiment.Get("exp")
	if err != nil {
		t.Fatalf("experiment.Get(exp) error = %v", err)
	}

	if err := app.PopulateObservedRuntime(t.Context(), exp); err != nil {
		t.Fatalf("app.PopulateObservedRuntime(exp) error = %v", err)
	}

	scorchexe.SetRunStatus(exp, 0)

	err = scorch.ExecuteComponent(t.Context(),
		scorch.Experiment(*exp), scorch.Name("probe"), scorch.Type("fake"), scorch.Stage(scorch.ActionStart),
		scorch.RunID(0), scorch.LoopCount(0), scorch.StartTime(time.Now().UTC().Format(time.RubyDate)),
		scorch.Metadata(scorchmd.ComponentMetadata{"msg": "fixed and {{Y}}", "n": "{{N}}"}),
	)
	if err != nil {
		t.Fatalf("scorch.ExecuteComponent(probe) error = %v", err)
	}

	stdin, _ := utilInputSaved(t, dir, "start", "probe", "0", "0", "0")

	if diff := utilInputBytesDiff(t, stdin, out); diff != "" {
		t.Errorf("phenix util scorch-json exp probe --runtime is not the stdin of the component (-want +got):\n%s", diff)
	}

	withRuntime, _ := utilInputDecode(t, out).(map[string]any)
	want, _ := utilInputDecode(t, plain).(map[string]any)
	want["hosts"], want["vms"] = withRuntime["hosts"], withRuntime["vms"]

	if diff := cmp.Diff(want, withRuntime); diff != "" {
		t.Errorf("phenix util scorch-json --runtime is not the input without it, with hosts and vms (-want +got):\n%s", diff)
	}

	if withRuntime["hosts"] == nil || withRuntime["vms"] == nil {
		t.Errorf("phenix util scorch-json --runtime has hosts %v and vms %v, want both", withRuntime["hosts"], withRuntime["vms"])
	}
}

// phenix util scorch-json uses the values of --replace in place of random
// values, prints the values and the warnings of the input on stderr, and
// only the warnings with -o json.
func TestUtilCmdScorchJSONReplaceAndWarnings(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	expCmdUseStore(t, utilInputScorchExperiment(t))

	out, stderr, err := utilCmdOutputs(context.Background(),
		"scorch-json", "exp", "probe", "--loop", "1", "--replace", `{{X}}="pinned"`, "--replace", "{{N}}=12",
	)
	if err != nil {
		t.Fatalf("phenix util scorch-json --replace error = %v", err)
	}

	exp, _ := utilInputDecode(t, out).(map[string]any)
	if diff := cmp.Diff([]any{
		map[string]any{"name": "probe", "type": "fake", "metadata": map[string]any{"msg": "pinned and inner", "n": 12}},
		map[string]any{"name": "other", "type": "fake", "metadata": map[string]any{"keep": true}},
		map[string]any{"name": "mark", "type": "tap", "metadata": map[string]any{}},
	}, utilInputComponents(exp)); diff != "" {
		t.Errorf("phenix util scorch-json --replace components mismatch (-want +got):\n%s", diff)
	}

	for _, line := range []string{`  {{N}}=12`, `  {{X}}="pinned"`, `  {{Y}}="inner"`, "  PHENIX_LOG_FILE=stderr"} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("phenix util scorch-json --replace stderr has no line %q:\n%s", line, stderr)
		}
	}

	_, stderr, err = utilCmdOutputs(context.Background(), "scorch-json", "exp", "probe", "--run", "1", "-o", "json")
	if err != nil {
		t.Fatalf("phenix util scorch-json --run 1 -o json error = %v", err)
	}

	want := "Warning: in a live run, the components mark can add taps to status.apps.scorch.taps; " +
		"this input does not show them\n"
	if stderr != want {
		t.Errorf("phenix util scorch-json --run 1 -o json stderr = %q, want %q", stderr, want)
	}
}

// utilInputComponents returns the SCORCH components of exp, an experiment
// decoded from JSON.
func utilInputComponents(exp map[string]any) any {
	spec, _ := exp["spec"].(map[string]any)
	scenario, _ := spec["scenario"].(map[string]any)
	apps, _ := scenario["apps"].([]any)
	scorchApp, _ := apps[0].(map[string]any)
	meta, _ := scorchApp["metadata"].(map[string]any)

	return meta["components"]
}

// phenix util scorch-json returns an error, and prints nothing, for a
// component that the scenario does not have, which lists the components of
// the scenario, a built-in component, a place where the run does not run the
// component, an unknown replacement and a bad flag value.
func TestUtilCmdScorchJSONErrors(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	expCmdUseStore(t, utilInputScorchExperiment(t))

	tests := []struct {
		args    []string
		wantErr error
	}{
		{args: []string{"nope"}, wantErr: scorch.ErrComponentNotFound},
		{args: []string{"mark"}, wantErr: scorch.ErrBuiltinComponent},
		{args: []string{"other"}, wantErr: scorch.ErrComponentNotRun},
		{args: []string{"probe", "--loop", "2"}, wantErr: scorch.ErrComponentNotRun},
		{args: []string{"probe", "--count", "1"}, wantErr: scorch.ErrComponentNotRun},
		{args: []string{"probe", "--replace", "{{Y}}=1"}, wantErr: scorch.ErrUnknownReplacement},
		{args: []string{"probe", "--replace", "{{Y}}"}, wantErr: errBadReplace},
		{args: []string{"probe", "--stage", "done"}, wantErr: errUnknownScorchStage},
	}

	for _, tt := range tests {
		args := append([]string{"scorch-json", "exp"}, tt.args...)

		out, err := utilCmdRun(args...)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("phenix util %s error = %v, want %v", strings.Join(args, " "), err, tt.wantErr)
		}

		if out != "" {
			t.Errorf("phenix util %s output = %q, want none", strings.Join(args, " "), out)
		}
	}

	// the names of the components are the content of the error, not its wording
	if _, err := utilCmdRun("scorch-json", "exp", "nope"); err == nil || !strings.Contains(err.Error(), "probe, other, mark") {
		t.Errorf("phenix util scorch-json exp nope error = %v, want one that lists probe, other, mark", err)
	}

	if _, err := utilCmdRun("scorch-json", "missing", "probe"); !vmCmdHumanized(err, "Unable to get the missing experiment") {
		t.Errorf("phenix util scorch-json missing probe error = %v, want %q", err, "Unable to get the missing experiment")
	}
}

// phenix util scorch-json completes the user components of the experiment
// in its first argument.
func TestCompleteScorchComponents(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	expCmdUseStore(t, utilInputScorchExperiment(t), vmCmdExperiment(t, "plain", false))

	noFileComp := cobra.ShellCompDirectiveNoFileComp

	runCompletionTests(t, []completionTestCase{
		{
			name: "completeScorchComponents", fn: completeScorchComponents, args: []string{"exp"},
			want: []string{"probe", "other"}, directive: noFileComp,
		},
		{
			name: "completeScorchComponents", fn: completeScorchComponents, args: []string{"exp"}, toComplete: "o",
			want: []string{"other"}, directive: noFileComp,
		},
		{name: "completeScorchComponents", fn: completeScorchComponents, args: []string{"plain"}, directive: noFileComp},
		{name: "completeScorchComponents", fn: completeScorchComponents, directive: noFileComp},
	})
}
