package experiment

import (
	"errors"
	"testing"
	"time"

	"phenix/store/storetest"
	"phenix/util/plog/plogtest"
)

// Delete logs the time of each step and the total once the config is gone, also when it could not delete all the
// files, and logs nothing when it deletes nothing. The files step includes the time of the file deletes.
func TestDeleteLogsTheTimeOfEachStep(t *testing.T) { //nolint:paralleltest // replaces package globals
	const delay = 50 * time.Millisecond

	tests := []struct {
		name    string
		status  map[string]any
		fileErr error
		wantErr error
		wantLog bool
	}{
		{name: "deleted", wantLog: true},
		{name: "files not all deleted", fileErr: errors.New("mesh unreachable"), wantErr: ErrDeleteCleanup, wantLog: true},
		{name: "running", status: map[string]any{"startTime": stopTestStart}, wantErr: ErrDeleteRunning},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)
			useStartMM(t, &startMM{})

			files := useDeletedFiles(t)
			files.err, files.delay = tc.fileErr, delay

			storeValidExperiment(t, "exp", tc.status, nil)

			logs := plogtest.Capture(t)

			if err := Delete("exp"); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Delete(exp) error = %v, want one that wraps %v", err, tc.wantErr)
			}

			records := logs.Take(t, plogtest.Message(deleteStepsMessage))
			if !tc.wantLog {
				if len(records) != 0 {
					t.Errorf("Delete(exp) logged %v, want no %q record", records, deleteStepsMessage)
				}

				return
			}

			if len(records) != 1 {
				t.Fatalf("Delete(exp) logged %d %q records, want 1", len(records), deleteStepsMessage)
			}

			r := records[0]
			if r["exp"] != "exp" || r["level"] != "INFO" {
				t.Errorf("Delete(exp) record exp = %v, level = %v, want exp, INFO", r["exp"], r["level"])
			}

			got := map[string]time.Duration{}

			for _, key := range []string{"store", "files", "base-dir", "hooks", "total"} {
				text, _ := r[key].(string)

				d, err := time.ParseDuration(text)
				if err != nil {
					t.Errorf("Delete(exp) record %s = %v, want a duration", key, r[key])
				}

				got[key] = d
			}

			// the first file delete fails before the snapshots, so a failed delete waits once
			wantFiles := 2 * delay
			if tc.fileErr != nil {
				wantFiles = delay
			}

			if got["files"] < wantFiles {
				t.Errorf("Delete(exp) record files = %v, want at least %v", got["files"], wantFiles)
			}

			if got["store"] >= delay {
				t.Errorf("Delete(exp) record store = %v, want less than %v: the file deletes are not part of it", got["store"], delay)
			}

			// each time is rounded to the millisecond
			if sum := got["store"] + got["files"] + got["base-dir"] + got["hooks"]; sum > got["total"]+4*time.Millisecond {
				t.Errorf("Delete(exp) step times add up to %v, more than the total %v", sum, got["total"])
			}
		})
	}
}
