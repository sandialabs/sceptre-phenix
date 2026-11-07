package experiment

import (
	"maps"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/store/storetest"
	"phenix/util/mm/mmtest"
	"phenix/util/notes"
	"phenix/util/plog/plogtest"
)

// startSteps returns the step times of the start records logged since the last call, without the experiment.
func startSteps(t *testing.T, logs *plogtest.Logs) []map[string]any {
	t.Helper()

	records := logs.Take(t, plogtest.Message(startStepsMessage))
	steps := make([]map[string]any, 0, len(records))

	for _, r := range records {
		got := map[string]any{}

		for k, v := range r {
			switch k {
			case "time", "level", "msg", "type", "source":
			default:
				got[k] = v
			}
		}

		steps = append(steps, got)
	}

	return steps
}

// The timer logs each step and the time since the start as the step begins, then the time of each step and the total.
func TestStartTimerLogsEachStep(t *testing.T) {
	logs := plogtest.Capture(t)

	clock := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	timer := newStartTimer("exp", now)
	clock = clock.Add(250 * time.Millisecond) // before the first step: only in the total

	timer.begin("claim")
	clock = clock.Add(1500 * time.Millisecond)
	timer.begin("disk-check")
	clock = clock.Add(8*time.Second + 123456*time.Microsecond)
	timer.done()

	records := logs.Records(t, plogtest.Message("experiment start step"))

	begun := make([][2]any, 0, len(records))
	for _, r := range records {
		begun = append(begun, [2]any{r["step"], r["elapsed"]})
	}

	if diff := cmp.Diff([][2]any{{"claim", "250ms"}, {"disk-check", "1.75s"}}, begun); diff != "" {
		t.Errorf("debug step records mismatch (-want +got):\n%s", diff)
	}

	want := []map[string]any{{"exp": "exp", "claim": "1.5s", "disk-check": "8.123s", "total": "9.873s"}}
	if diff := cmp.Diff(want, startSteps(t, logs)); diff != "" {
		t.Errorf("step times record mismatch (-want +got):\n%s", diff)
	}
}

// A start logs the time of each of its steps; a failed start logs the steps up to the one that failed.
func TestStartLogsItsStepTimes(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name    string
		replies launchReplies
		want    []string
	}{
		{
			name: "started",
			want: []string{
				"claim", "pre-start-apps", "disk-check", "scripts", "snapshot-cleanup", "minimega-script", "launch",
				"vm-hosts-and-vlans", "c2-script", "delayed-vms", "post-start-apps", "record",
			},
		},
		{
			name:    "failed launch",
			replies: launchReplies{"vm launch": []*minicli.Response{mmtest.Error("head", "boom")}},
			want: []string{
				"claim", "pre-start-apps", "disk-check", "scripts", "snapshot-cleanup", "minimega-script", "launch",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)

			storeLaunchExperiment(t, nil, launchNode("a", launchImage(t), nil))
			useDeletedFiles(t)

			replies := defaultLaunchReplies()
			maps.Copy(replies, tc.replies)

			useLaunchMM(t, replies)

			logs := plogtest.Capture(t)

			_ = Start(notes.Context(t.Context(), false), StartWithName("exp"))

			records := startSteps(t, logs)
			if len(records) != 1 {
				t.Fatalf("Start(exp) logged %d step time records, want 1", len(records))
			}

			var got []string
			for _, step := range tc.want {
				if _, ok := records[0][step]; ok {
					got = append(got, step)
				}
			}

			// every step, and nothing else but the experiment and the total
			if diff := cmp.Diff(tc.want, got); diff != "" || len(records[0]) != len(tc.want)+2 {
				t.Errorf("Start(exp) step times = %v, want the steps %v (-want +got):\n%s", records[0], tc.want, diff)
			}
		})
	}
}
