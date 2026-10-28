package cmd

import (
	"maps"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"
)

// A changed, added or removed setting needs a restart unless the config
// watcher applies it live: the log settings, ui.logs.level and ui.users.
func TestRestartRequiredChanges(t *testing.T) {
	t.Parallel()

	before := map[string]any{
		"store.endpoint":     "bolt:///etc/phenix/store.bdb",
		"ui.listen-endpoint": "0.0.0.0:3000",
		"ui.features":        []any{"vm-mount"},
		"log.level":          "info",
		"log.system.path":    "/var/log/phenix/phenix.log",
		"ui.logs.level":      "",
		"ui.users":           []any{"admin:secret:global-admin"},
		"base-dir.phenix":    "/phenix",
	}

	tests := map[string]struct {
		change func(map[string]any)
		want   []string
	}{
		"nothing": {change: func(map[string]any) {}},
		"live settings": {change: func(m map[string]any) {
			m["log.level"] = "debug"
			m["log.system.path"] = "/tmp/p.log"
			m["ui.logs.level"] = "warn"
			m["ui.users"] = nil
		}},
		"store and listen endpoint": {
			change: func(m map[string]any) { m["store.endpoint"] = "bolt:///tmp/s.bdb"; m["ui.listen-endpoint"] = ":3080" },
			want:   []string{"store.endpoint", "ui.listen-endpoint"},
		},
		"list value":   {change: func(m map[string]any) { m["ui.features"] = []any{} }, want: []string{"ui.features"}},
		"added key":    {change: func(m map[string]any) { m["ui.jwt-signing-key"] = "x" }, want: []string{"ui.jwt-signing-key"}},
		"removed key":  {change: func(m map[string]any) { delete(m, "base-dir.phenix") }, want: []string{"base-dir.phenix"}},
		"removed live": {change: func(m map[string]any) { delete(m, "ui.users") }},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			after := maps.Clone(before)

			tc.change(after)

			if diff := cmp.Diff(tc.want, restartRequiredChanges(before, after)); diff != "" {
				t.Errorf("restartRequiredChanges() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func genRestartSettings() *rapid.Generator[map[string]any] {
	keys := rapid.SampledFrom([]string{
		"log.level", "log.system.max-size", "ui.logs.level", "ui.users", "ui.base-path",
		"store.endpoint", "base-dir.phenix", "bridge-mode", "logs", "ui.logs.minimega-path",
	})

	return rapid.MapOf(keys, rapid.Map(rapid.IntRange(0, 3), func(i int) any { return i }))
}

// Whatever the settings, the result is sorted, names no live setting, and
// holds exactly the other keys whose values differ.
func TestRestartRequiredChangesProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		before := genRestartSettings().Draw(t, "before")
		after := genRestartSettings().Draw(t, "after")

		got := restartRequiredChanges(before, after)

		if !slices.IsSorted(got) {
			t.Fatalf("restartRequiredChanges() = %v, not sorted", got)
		}

		for _, key := range []string{"log.level", "log.system.max-size", "ui.logs.level", "ui.users"} {
			if slices.Contains(got, key) {
				t.Fatalf("restartRequiredChanges() = %v, includes live setting %s", got, key)
			}
		}

		for _, key := range []string{"ui.base-path", "store.endpoint", "base-dir.phenix", "bridge-mode", "logs", "ui.logs.minimega-path"} {
			old, inBefore := before[key]
			now, inAfter := after[key]
			changed := inBefore != inAfter || old != now

			if changed != slices.Contains(got, key) {
				t.Fatalf("restartRequiredChanges() = %v; %s changed %t (%v -> %v)", got, key, changed, old, now)
			}
		}

		if len(restartRequiredChanges(after, after)) != 0 {
			t.Fatalf("restartRequiredChanges(after, after) is not empty")
		}
	})
}

// The baseline stays the startup settings: a reverted key needs no restart,
// and a pending key is listed again when another key changes.
func TestRestartBaselinePending(t *testing.T) {
	t.Parallel()

	var baseline restartBaseline

	if got := baseline.pending(map[string]any{"store.endpoint": "B"}); got != nil {
		t.Errorf("pending() before record = %v, want nil", got)
	}

	baseline.record(map[string]any{"store.endpoint": "A", "log.level": "info"})

	steps := []struct {
		current map[string]any
		want    []string
	}{
		{map[string]any{"store.endpoint": "B", "log.level": "info"}, []string{"store.endpoint"}},
		{map[string]any{"store.endpoint": "B", "log.level": "debug"}, []string{"store.endpoint"}},
		{map[string]any{"store.endpoint": "A", "log.level": "debug"}, nil},
	}

	for i, step := range steps {
		if diff := cmp.Diff(step.want, baseline.pending(step.current)); diff != "" {
			t.Errorf("step %d: pending() mismatch (-want +got):\n%s", i, diff)
		}
	}
}

// flatSettings gives every setting by its dotted key, as the environment sets
// it. phenix ui records them at the start, and each later config change logs
// the keys that wait for a restart, by name only.
func TestRestartSettingsWatchAndWarn(t *testing.T) { //nolint:paralleltest // sets the environment and uiStartSettings
	uiStartSettings.mu.Lock()
	saved := uiStartSettings.start
	uiStartSettings.mu.Unlock()

	t.Cleanup(func() { uiStartSettings.record(saved) })

	const msg = "config change takes effect only when phenix ui restarts"

	logs := captureLogs(t)

	t.Setenv("PHENIX_STORE_ENDPOINT", "bolt:///tmp/restart-a.bdb")
	t.Setenv("PHENIX_LOG_SYSTEM_PATH", "/tmp/restart-a.log")

	flat := flatSettings()

	for key, want := range map[string]string{
		"store.endpoint": "bolt:///tmp/restart-a.bdb", "log.system.path": "/tmp/restart-a.log",
	} {
		if got := flat[key]; got != want {
			t.Errorf("flatSettings()[%q] = %v, want %q", key, got, want)
		}
	}

	for key, value := range flat {
		if _, nested := value.(map[string]any); nested {
			t.Errorf("flatSettings()[%q] = %v, a map that is not flattened", key, value)
		}
	}

	steps := []struct {
		name string
		do   func()
		want []any // the keys of each warning logged so far
	}{
		{name: "before phenix ui starts", do: func() { uiStartSettings.record(nil); warnRestartSettings() }},
		{name: "unchanged", do: func() { watchRestartSettings(); warnRestartSettings() }},
		{name: "live setting", do: func() {
			t.Setenv("PHENIX_LOG_SYSTEM_PATH", "/tmp/restart-b.log")
			warnRestartSettings()
		}},
		{name: "store endpoint", do: func() {
			t.Setenv("PHENIX_STORE_ENDPOINT", "bolt:///tmp/restart-b.bdb")
			warnRestartSettings()
		}, want: []any{"store.endpoint"}},
		{name: "reverted", do: func() {
			t.Setenv("PHENIX_STORE_ENDPOINT", "bolt:///tmp/restart-a.bdb")
			warnRestartSettings()
		}, want: []any{"store.endpoint"}},
	}

	for _, step := range steps {
		step.do()

		records := logs.all(t, msg)

		got := make([]any, 0, len(records))
		for _, record := range records {
			got = append(got, record["keys"])
		}

		if diff := cmp.Diff(step.want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("%s: warnings mismatch (-want +got):\n%s", step.name, diff)
		}
	}
}
