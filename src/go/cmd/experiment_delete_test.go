package cmd

import (
	"errors"
	"strings"
	"testing"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
)

// deleteFailStore is a BoltDB whose Update fails for the configs named in updateFail, and whose Delete fails for the
// configs named in deleteFail.
type deleteFailStore struct {
	*store.BoltDB

	updateFail, deleteFail map[string]bool
}

func (s *deleteFailStore) Update(c *store.Config) error {
	if s.updateFail[c.Metadata.Name] {
		return errors.New("store is read-only")
	}

	return s.BoltDB.Update(c) //nolint:wrapcheck // the store's own error
}

func (s *deleteFailStore) Delete(c *store.Config) error {
	if s.deleteFail[c.Metadata.Name] {
		return errors.New("store is read-only")
	}

	return s.BoltDB.Delete(c) //nolint:wrapcheck // the store's own error
}

// TestExpCmdDeleteFailures checks that experiment delete keeps an experiment
// that it cannot stop, continues with the other experiments after a failure,
// and then returns an error that names each failure.
func TestExpCmdDeleteFailures(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name       string
		args       []string
		updateFail []string
		deleteFail []string
		humanized  string   // the one failure, "" for more than one
		wantParts  []string // the lines of an error with more than one failure
		wantState  map[string]string
	}{
		{
			name: "a stop that fails", args: []string{"delete", "running", "--force"}, updateFail: []string{"running"},
			humanized: "Unable to stop the running experiment before deleting it",
			wantState: map[string]string{"running": "running", "stopped": "stopped"},
		},
		{
			name: "a delete that fails", args: []string{"delete", "stopped"}, deleteFail: []string{"stopped"},
			humanized: "Unable to delete the stopped experiment",
			wantState: map[string]string{"running": "running", "stopped": "stopped"},
		},
		{
			name: "all after a failed delete", args: []string{"delete", "all"}, deleteFail: []string{"stopped"},
			humanized: "Unable to delete the stopped experiment",
			wantState: map[string]string{"other": "deleted", "running": "running", "stopped": "stopped"},
		},
		{
			name: "all with two failures", args: []string{"delete", "all", "--force"},
			updateFail: []string{"running"}, deleteFail: []string{"stopped"},
			wantParts: []string{
				"unable to delete 2 experiments:\n  ",
				"\n  Unable to stop the running experiment before deleting it (search error logs for ",
				"\n  Unable to delete the stopped experiment (search error logs for ",
			},
			wantState: map[string]string{"other": "deleted", "running": "running", "stopped": "stopped"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &deleteFailStore{BoltDB: storetest.NewBolt(t), updateFail: map[string]bool{}, deleteFail: map[string]bool{}}
			for _, name := range tt.updateFail {
				db.updateFail[name] = true
			}

			for _, name := range tt.deleteFail {
				db.deleteFail[name] = true
			}

			storetest.Use(t, db)

			for _, cfg := range []store.Config{
				vmCmdExperiment(t, "stopped", false), vmCmdExperiment(t, "running", true), vmCmdExperiment(t, "other", false),
			} {
				if err := db.Create(&cfg); err != nil {
					t.Fatalf("store.Create(%s) error = %v", cfg.Metadata.Name, err)
				}
			}

			mmtest.Use(t, vmCmdMinimega{}.handle)

			err := expCmdRun(tt.args...)

			switch {
			case tt.humanized != "":
				if msg := expCmdCheckErr(err, "", tt.humanized); msg != "" {
					t.Errorf("experiment %q %s", tt.args, msg)
				}
			case err == nil:
				t.Errorf("experiment %q error = nil, want one with %q", tt.args, tt.wantParts)
			default:
				for _, part := range tt.wantParts {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("experiment %q error = %q, want it to contain %q", tt.args, err, part)
					}
				}
			}

			for name, want := range tt.wantState {
				if got := expCmdState(t, name); got != want {
					t.Errorf("experiment %q left %s %s, want %s", tt.args, name, got, want)
				}
			}
		})
	}
}

// TestExpCmdDeleteLogsTheSteps checks that experiment delete deletes through
// experiment.Delete: it logs the time of each step, and an experiment whose
// files it could not all delete is gone with an error that says so.
func TestExpCmdDeleteLogsTheSteps(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		fail      map[string]string
		humanized string
	}{
		{name: "deleted"},
		{
			name:      "files not all deleted",
			fail:      map[string]string{"mesh send all file delete stopped/miniccc_responses": "mesh unreachable"},
			humanized: "The stopped experiment was deleted, but its files were not all deleted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := storetest.NewBolt(t)
			storetest.Use(t, db)

			cfg := vmCmdExperiment(t, "stopped", false)
			if err := db.Create(&cfg); err != nil {
				t.Fatalf("store.Create(stopped) error = %v", err)
			}

			mmtest.Use(t, vmCmdMinimega{fail: tt.fail}.handle)

			logs := plogtest.Capture(t)

			err := expCmdRun("delete", "stopped")
			if msg := expCmdCheckErr(err, "", tt.humanized); msg != "" {
				t.Errorf("experiment delete stopped %s", msg)
			}

			if got := expCmdState(t, "stopped"); got != "deleted" {
				t.Errorf("experiment delete stopped left it %s, want deleted", got)
			}

			records := logs.Records(t, plogtest.Message("experiment deleted"))
			if len(records) != 1 || records[0]["total"] == nil || records[0]["files"] == nil {
				t.Errorf("experiment delete stopped logged %v, want one experiment deleted record with the step times", records)
			}
		})
	}
}
