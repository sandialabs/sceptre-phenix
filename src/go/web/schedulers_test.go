package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/scheduler"
	"phenix/util/shell"
)

// schedulersShell finds the commands it holds on the PATH. Other shell.Shell methods panic.
type schedulersShell struct {
	shell.Shell

	names []string
}

func (s schedulersShell) FindCommandsWithPrefix(string) []string { return s.names }

// GET /schedulers lists the built-in schedulers, then the user schedulers on
// the PATH, for a role that may schedule experiments.
func TestGetSchedulers(t *testing.T) {
	original := shell.DefaultShell
	t.Cleanup(func() { shell.DefaultShell = original }) //nolint:reassign // restore test double

	shell.DefaultShell = schedulersShell{names: []string{"custom"}} //nolint:reassign // install test double

	tests := []struct {
		name       string
		names      []string // the experiments the role may schedule
		wantStatus int
	}{
		{name: "every experiment", names: []string{"*"}, wantStatus: http.StatusOK},
		{name: "one experiment", names: []string{"exp"}, wantStatus: http.StatusOK},
		{name: "no schedule permission", wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			role := testRole("experiments/schedule", "create", tc.names...)
			if tc.names == nil {
				role = testRole("experiments/schedule", "get", "*")
			}

			resp := do(t, http.MethodGet, serveAs(t, "alice", role).URL+"/api/v1/schedulers", "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("GET /schedulers = %d, want %d: %s", resp.StatusCode, tc.wantStatus, body)
			}

			if resp.StatusCode != http.StatusOK {
				return
			}

			var got schedulersResponse
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decoding GET /schedulers: %v", err)
			}

			// each built-in scheduler says what it does (see scheduler tests)
			for i, info := range got.Schedulers {
				if info.Builtin == (info.Description == "") {
					t.Errorf("%s: builtin %t with description %q", info.Name, info.Builtin, info.Description)
				}

				got.Schedulers[i].Description = ""
			}

			want := schedulersResponse{Schedulers: []scheduler.Info{
				{Name: "isolate-experiment", Builtin: true},
				{Name: "round-robin", Builtin: true},
				{Name: "subnet-compute", Builtin: true},
				{Name: "custom", Builtin: false},
			}}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("GET /schedulers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
