package scorch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/scorch/scorchmd"
	v2 "phenix/types/version/v2"
	"phenix/util/plog"
	"phenix/util/polltest"
)

// userFakeComponent puts phenix-scorch-component-fake first on PATH. For each
// stage it saves its arguments and its STDIN in dir, as <stage>.args and
// <stage>.json, and then runs body.
func userFakeComponent(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	script := fmt.Sprintf(
		"#!/bin/sh\n/bin/cat > \"%[1]s/$1.json.tmp\"\n/bin/mv \"%[1]s/$1.json.tmp\" \"%[1]s/$1.json\"\n"+
			"printf '%%s\\n' \"$@\" > \"%[1]s/$1.args.tmp\"\n/bin/mv \"%[1]s/$1.args.tmp\" \"%[1]s/$1.args\"\n%[2]s\n",
		dir, body,
	)

	if err := os.WriteFile(filepath.Join(dir, "phenix-scorch-component-fake"), []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake component: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return dir
}

// userComponentInput returns the arguments and the experiment that the fake
// component got for stage, once it saves them.
func userComponentInput(t *testing.T, dir string, stage Action) ([]string, map[string]any) {
	t.Helper()

	var args, body []byte

	polltest.Until(t, fmt.Sprintf("the fake component saves its %s input", stage), func() bool {
		var errArgs, errBody error

		args, errArgs = os.ReadFile(filepath.Join(dir, string(stage)+".args"))
		body, errBody = os.ReadFile(filepath.Join(dir, string(stage)+".json"))

		return errArgs == nil && errBody == nil
	})

	var exp map[string]any
	if err := json.Unmarshal(body, &exp); err != nil {
		t.Fatalf("decoding the experiment the fake component got: %v", err)
	}

	return strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"), exp
}

// userComponentsOf returns the components of the scorch app in exp, an
// experiment decoded from JSON.
func userComponentsOf(exp map[string]any) any {
	spec, _ := exp["spec"].(map[string]any)
	scenario, _ := spec["scenario"].(map[string]any)
	apps, _ := scenario["apps"].([]any)

	for _, a := range apps {
		if app, _ := a.(map[string]any); app["name"] == "scorch" {
			meta, _ := app["metadata"].(map[string]any)

			return meta["components"]
		}
	}

	return nil
}

// userComponentOptions are the options of component probe of type fake in an
// experiment whose scorch app configures probe and one other component.
func userComponentOptions(opts ...Option) []Option {
	exp := builtinExperiment()
	exp.Spec.Scenario().(*v2.ScenarioSpec).AppsF = []*v2.ScenarioApp{{NameF: "scorch", MetadataF: map[string]any{
		"components": []any{
			map[string]any{"name": "probe", "type": "fake", "metadata": map[string]any{"old": "value"}},
			map[string]any{"name": "other", "type": "fake", "metadata": map[string]any{"keep": true}},
		},
	}}}

	return append([]Option{
		Experiment(*exp), Name("probe"), Type("fake"), RunID(1), CurrentLoop(2), LoopCount(3),
		Metadata(scorchmd.ComponentMetadata{"new": "value"}),
	}, opts...)
}

// A user component runs its program for each stage with the stage, its name,
// run, loop and count, and gives it the experiment with the component's
// metadata as the run has it.
func TestUserComponentStages(t *testing.T) { //nolint:paralleltest // sets PATH
	dir := userFakeComponent(t, "")

	var u UserComponent
	_ = u.Init(userComponentOptions()...)

	wantComponents := []any{
		map[string]any{"name": "probe", "type": "fake", "metadata": map[string]any{"new": "value"}},
		map[string]any{"name": "other", "type": "fake", "metadata": map[string]any{"keep": true}},
	}

	for stage, run := range map[Action]func(context.Context) error{ //nolint:exhaustive // done and loop run no program
		ActionConfigure: u.Configure, ActionStart: u.Start, ActionStop: u.Stop, ActionCleanup: u.Cleanup,
	} {
		if err := run(context.Background()); err != nil {
			t.Errorf("UserComponent.%s() error = %v", stage, err)

			continue
		}

		args, exp := userComponentInput(t, dir, stage)

		if diff := cmp.Diff([]string{string(stage), "probe", "1", "2", "3"}, args); diff != "" {
			t.Errorf("UserComponent.%s() arguments mismatch (-want +got):\n%s", stage, diff)
		}

		if diff := cmp.Diff(wantComponents, userComponentsOf(exp)); diff != "" {
			t.Errorf("UserComponent.%s() components mismatch (-want +got):\n%s", stage, diff)
		}
	}
}

// A user component fails when its context is done, and when its program fails.
func TestUserComponentErrors(t *testing.T) { //nolint:paralleltest // sets PATH
	userFakeComponent(t, "exit 3")

	var u UserComponent
	_ = u.Init(userComponentOptions()...)

	var exit *exec.ExitError
	if err := u.Start(context.Background()); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("UserComponent.Start(a program that fails) error = %v, want exit status 3", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := u.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("UserComponent.Start(context done) error = %v, want %v", err, context.Canceled)
	}
}

// userStageLogs returns, for each message that a user component program
// writes to STDOUT, the message as scorch logs it.
func userStageLogs(t *testing.T) <-chan string {
	t.Helper()

	logs := make(chan string, 16)
	name := "test-user-component-" + t.Name()

	plog.AddHandler(name, plog.NewUIHandler("INFO", func(_ time.Time, _, logType, msg string) {
		if logType == string(plog.TypePhenixApp) && strings.Contains(msg, "component=probe") {
			logs <- msg
		}
	}))
	t.Cleanup(func() { plog.RemoveHandler(name) })

	return logs
}

// userWaitLog waits for a log entry that holds msg.
func userWaitLog(t *testing.T, logs <-chan string, msg string) {
	t.Helper()

	timeout := time.After(time.Minute)

	for {
		select {
		case got := <-logs:
			if strings.Contains(got, msg) {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for the log entry %q", msg)
		}
	}
}

// A background user component returns before its program ends, and its end
// stage removes it from the background components. Each program logs that it
// is done, so the test waits for each background run to end.
//
//nolint:paralleltest // sets PATH and replaces the background component state
func TestUserComponentInBackground(t *testing.T) {
	dir := userFakeComponent(t, `while [ ! -e "$(dirname "$0")/$1.go" ]; do /bin/sleep 0.01; done; echo "$1 done"`)
	logs := userStageLogs(t)

	var u UserComponent
	_ = u.Init(userComponentOptions(Background())...)

	for _, stages := range [][2]Action{{ActionConfigure, ActionCleanup}, {ActionStart, ActionStop}} {
		begin, end := u.Configure, u.Cleanup
		if stages[0] == ActionStart {
			begin, end = u.Start, u.Stop
		}

		if err := begin(context.Background()); err != nil {
			t.Errorf("background UserComponent.%s() error = %v, want nil", stages[0], err)
		}

		name := "exp/" + string(stages[0]) + "/probe"
		if _, ok := backgrounded[stages[0]][name]; !ok {
			t.Errorf("after background UserComponent.%s(), backgrounded[%s] has no %s", stages[0], stages[0], name)
		}

		for _, stage := range stages {
			if err := os.WriteFile(filepath.Join(dir, string(stage)+".go"), nil, 0o600); err != nil {
				t.Fatalf("letting the %s program end: %v", stage, err)
			}
		}

		userWaitLog(t, logs, string(stages[0])+" done")

		if err := end(context.Background()); err != nil {
			t.Errorf("background UserComponent.%s() error = %v, want nil", stages[1], err)
		}

		userWaitLog(t, logs, string(stages[1])+" done")

		if _, ok := backgrounded[stages[0]][name]; ok {
			t.Errorf("after UserComponent.%s(), backgrounded[%s] keeps %s", stages[1], stages[0], name)
		}
	}
}

// genUserExperiment draws an experiment decoded from JSON whose scenario apps
// have components of a few names, and values of other types where the apps
// and components go.
func genUserExperiment() *rapid.Generator[map[string]any] {
	name := rapid.SampledFrom([]string{"probe", "other", ""})
	component := rapid.OneOf(
		rapid.Custom(func(t *rapid.T) any {
			return map[string]any{"name": name.Draw(t, "name"), "metadata": map[string]any{"n": rapid.Int().Draw(t, "n")}}
		}),
		rapid.Just[any]("component"),
		rapid.Just[any](map[string]any{"name": 5.0}),
	)
	app := rapid.OneOf(
		rapid.Custom(func(t *rapid.T) any {
			return map[string]any{"metadata": map[string]any{"components": rapid.SliceOfN(component, 0, 4).Draw(t, "components")}}
		}),
		rapid.Just[any](map[string]any{"metadata": "none"}),
		rapid.Just[any](nil),
	)

	return rapid.Custom(func(t *rapid.T) map[string]any {
		return map[string]any{"spec": map[string]any{"scenario": map[string]any{"apps": rapid.SliceOfN(app, 0, 3).Draw(t, "apps")}}}
	})
}

// userCloneJSON returns a deep copy of v, a value decoded from JSON.
func userCloneJSON(v any) any {
	switch v := v.(type) {
	case map[string]any:
		c := maps.Clone(v)
		for k, e := range c {
			c[k] = userCloneJSON(e)
		}

		return c
	case []any:
		c := slices.Clone(v)
		for i, e := range c {
			c[i] = userCloneJSON(e)
		}

		return c
	default:
		return v
	}
}

// userCheckMetadata returns an error unless after is before with the metadata
// of the first component called name in each app set to md.
func userCheckMetadata(before, after map[string]any, name string, md scorchmd.ComponentMetadata) error {
	want := userCloneJSON(before).(map[string]any) //nolint:forcetypeassert // a clone of a map

	spec, _ := want["spec"].(map[string]any)
	scenario, _ := spec["scenario"].(map[string]any)
	apps, _ := scenario["apps"].([]any)

	for _, a := range apps {
		app, _ := a.(map[string]any)
		meta, _ := app["metadata"].(map[string]any)
		cmps, _ := meta["components"].([]any)

		i := slices.IndexFunc(cmps, func(c any) bool {
			m, ok := c.(map[string]any)

			return ok && reflect.DeepEqual(m["name"], name)
		})
		if i >= 0 {
			cmps[i].(map[string]any)["metadata"] = md //nolint:forcetypeassert // IndexFunc found a map
		}
	}

	if diff := cmp.Diff(want, after); diff != "" {
		return fmt.Errorf("setComponentMetadata(%s) mismatch (-want +got):\n%s", name, diff)
	}

	return nil
}

func TestSetComponentMetadata(t *testing.T) {
	t.Parallel()

	md := scorchmd.ComponentMetadata{"new": "value"}

	for name, exp := range map[string]map[string]any{
		"no spec":         {},
		"no scenario":     {"spec": map[string]any{}},
		"apps not a list": {"spec": map[string]any{"scenario": map[string]any{"apps": "scorch"}}},
		"components": {"spec": map[string]any{"scenario": map[string]any{"apps": []any{
			"scorch",
			map[string]any{"metadata": map[string]any{"components": "probe"}},
			map[string]any{"metadata": map[string]any{"components": []any{
				"probe",
				map[string]any{"name": "other"},
				map[string]any{"name": "probe", "metadata": map[string]any{"old": 1}},
				map[string]any{"name": "probe", "metadata": map[string]any{"old": 2}},
			}}},
		}}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := userCloneJSON(exp).(map[string]any) //nolint:forcetypeassert // a clone of a map
			setComponentMetadata(exp, "probe", md)

			if err := userCheckMetadata(before, exp, "probe", md); err != nil {
				t.Error(err)
			}
		})
	}
}

// For any experiment, setComponentMetadata sets the metadata of the first
// component of each app with the name, and changes nothing else.
func TestSetComponentMetadataProperties(t *testing.T) {
	t.Parallel()

	md := scorchmd.ComponentMetadata{"new": "value"}

	rapid.Check(t, func(t *rapid.T) {
		exp := genUserExperiment().Draw(t, "exp")
		name := rapid.SampledFrom([]string{"probe", "other", ""}).Draw(t, "target")

		before := userCloneJSON(exp).(map[string]any) //nolint:forcetypeassert // a clone of a map
		setComponentMetadata(exp, name, md)

		if err := userCheckMetadata(before, exp, name, md); err != nil {
			t.Fatal(err)
		}
	})
}
