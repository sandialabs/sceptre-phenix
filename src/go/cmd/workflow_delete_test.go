package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// The random suffixes of two staging leftovers of a branch name.
const (
	deleteTokenA = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	deleteTokenB = "234567ABCDEFGHIJKLMNOPQRST"
)

const reqDelete = "DELETE /api/v1/workflow/apply/lab"

// fakeDelete describes how a fake phenix server answers a workflow delete.
// onDelete runs first. A non-zero status fails the delete with cause, and a
// non-empty raw is the body of a 200. Otherwise the answer is result with the
// values of the request; ignoreDryRun sets dryRun to false. hang waits for
// the client to go away, and drop closes the connection without an answer.
type fakeDelete struct {
	options      map[string]any
	onDelete     func()
	status       int
	cause        string
	raw          string
	result       workflow.DeleteResult
	ignoreDryRun bool
	hang         bool
	drop         bool
}

// newFakeDeleter starts a fake phenix server that answers as d describes.
func newFakeDeleter(t *testing.T, d fakeDelete) *fakeServer {
	t.Helper()

	return newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/options" {
			writeJSON(t, w, http.StatusOK, d.options)

			return
		}

		if r.Method != http.MethodDelete {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		if d.onDelete != nil {
			d.onDelete()
		}

		switch {
		case d.drop:
			dropConnection(t, w)
		case d.hang:
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case d.status != 0:
			writeJSON(t, w, d.status, map[string]any{"message": "unable to delete phenix workflow deployment", "cause": d.cause})
		case d.raw != "":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, d.raw)
		default:
			result := d.result
			result.Branch = strings.TrimPrefix(r.URL.Path, "/api/v1/workflow/apply/")
			result.DryRun = r.URL.Query().Get("dryRun") == boolStringTrue && !d.ignoreDryRun
			result.InjectsDir = r.URL.Query().Get("injects")

			writeJSON(t, w, http.StatusOK, result)
		}
	})
}

// deleteServerOptions returns the options of a phenix server that supports
// workflow delete.
func deleteServerOptions(injectsBase string) map[string]any {
	opts := serverOptions(injectsBase)
	opts[optionWorkflowDelete] = true

	return opts
}

// testDeleteOptions returns the options of a plain delete through socket, with
// localInjects as the local injects setting and out for the dry run lines.
func testDeleteOptions(socket, localInjects string, out *bytes.Buffer) deleteOptions {
	return deleteOptions{
		DryRun:              false,
		Force:               false,
		Socket:              socket,
		InjectsBase:         localInjects,
		InjectsBaseExplicit: false,
		TopologiesBase:      "",
		Out:                 out,
	}
}

// deleteQuery returns the query of the delete request fake received, and
// fails the test when there is not exactly one.
func deleteQuery(t *testing.T, fake *fakeServer) url.Values {
	t.Helper()

	var queries []url.Values

	for _, req := range fake.recorded() {
		if req.method == http.MethodDelete {
			queries = append(queries, req.query)
		}
	}

	if len(queries) != 1 {
		t.Fatalf("received %d delete requests, want 1", len(queries))
	}

	return queries[0]
}

// newDeleteBase returns a new injects base, with its symbolic links resolved,
// that holds entries, each a directory with one file.
func newDeleteBase(t *testing.T, entries ...string) string {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		writeWorkflowTree(t, base, map[string]string{entry + "/motd": "hello\n"})
	}

	return base
}

// dirEntries returns the names of the entries in dir, sorted.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// runningConflict returns the cause that the phenix server gives when the
// experiment mapped to lab runs and the delete has no force.
func runningConflict(t *testing.T) string {
	t.Helper()

	running := types.Experiment{
		Metadata: store.ConfigMetadata{Name: "lab"},
		Status:   &v1.ExperimentStatus{StartTimeF: "2024-01-01T00:00:00Z"},
	}

	_, err := workflow.NewDeletePlan("lab", []types.Experiment{running}, false)
	if err == nil {
		t.Fatal("NewDeletePlan(running, no force) error = nil, want ErrRunning")
	}

	return err.Error()
}

func TestRunWorkflowDelete(t *testing.T) {
	leftoverA := ".lab.tmp-" + deleteTokenA
	leftoverB := ".lab.old-" + deleteTokenB

	tests := []struct {
		name      string
		entries   []string
		force     bool
		result    workflow.DeleteResult
		wantQuery url.Values // without injects
		kept      []string
		wantLogs  []string // info and warning messages, in order
	}{
		{
			name:      "experiment, injects and leftovers",
			entries:   []string{"lab", "lab-a", leftoverA, leftoverB, ".lab-a.tmp-" + deleteTokenA},
			result:    workflow.DeleteResult{Experiment: "lab"},
			wantQuery: url.Values{},
			kept:      []string{".lab-a.tmp-" + deleteTokenA, "lab-a"},
			wantLogs: []string{
				"experiment deleted", "injects directory deleted", "staging leftover deleted",
				"staging leftover deleted", "workflow delete complete",
			},
		},
		{
			name:      "force stops the experiment first",
			entries:   []string{"lab"},
			force:     true,
			result:    workflow.DeleteResult{Experiment: "lab", Stopped: true},
			wantQuery: url.Values{"force": {boolStringTrue}},
			kept:      []string{},
			wantLogs:  []string{"experiment stopped", "experiment deleted", "injects directory deleted", "workflow delete complete"},
		},
		{
			name:      "no experiment",
			entries:   []string{"lab", leftoverA},
			wantQuery: url.Values{},
			kept:      []string{},
			wantLogs: []string{
				"no experiment is mapped to the branch name", "injects directory deleted",
				"staging leftover deleted", "workflow delete complete",
			},
		},
		{
			name:      "experiment only",
			entries:   []string{"lab-a"},
			result:    workflow.DeleteResult{Experiment: "lab"},
			wantQuery: url.Values{},
			kept:      []string{"lab-a"},
			wantLogs:  []string{"experiment deleted", "workflow delete complete"},
		},
		{
			name:      "nothing to delete",
			entries:   []string{"lab-a"},
			wantQuery: url.Values{},
			kept:      []string{"lab-a"},
			wantLogs:  []string{"no experiment is mapped to the branch name", "nothing to delete for branch name lab"},
		},
		{
			name:      "injects used by other experiments",
			entries:   []string{"lab", leftoverA},
			result:    workflow.DeleteResult{Experiment: "lab", InjectsUsedBy: []string{"other-a", "other-b"}},
			wantQuery: url.Values{},
			kept:      []string{"lab"},
			wantLogs: []string{
				"experiment deleted", "injects directory kept: other experiments inject files from it",
				"staging leftover deleted", "workflow delete complete",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newDeleteBase(t, tt.entries...)
			fake := newFakeDeleter(t, fakeDelete{options: deleteServerOptions(base), result: tt.result})
			logs := captureLogs(t)

			var out bytes.Buffer

			opts := testDeleteOptions(fake.socket, t.TempDir(), &out)
			opts.Force = tt.force

			if err := runWorkflowDelete(t.Context(), opts, "lab"); err != nil {
				t.Fatalf("runWorkflowDelete(lab) error = %v", err)
			}

			checkRequests(t, fake, reqOptions, reqDelete)

			wantQuery := tt.wantQuery
			wantQuery.Set("injects", filepath.Join(base, "lab"))

			if diff := cmp.Diff(wantQuery, deleteQuery(t, fake)); diff != "" {
				t.Errorf("delete query mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.kept, dirEntries(t, base), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("entries of the injects base mismatch (-want +got):\n%s", diff)
			}

			got := []string{}

			for _, record := range logs.records(t) {
				if record["level"] == "INFO" || record["level"] == "WARN" {
					got = append(got, fmt.Sprint(record["msg"]))
				}
			}

			if diff := cmp.Diff(tt.wantLogs, got); diff != "" {
				t.Errorf("info and warning messages mismatch (-want +got):\n%s", diff)
			}

			if out.Len() != 0 {
				t.Errorf("output = %q, want none outside a dry run", out.String())
			}

			// A second run deletes nothing more and succeeds.
			if err := runWorkflowDelete(t.Context(), opts, "lab"); err != nil {
				t.Fatalf("second runWorkflowDelete(lab) error = %v", err)
			}

			if diff := cmp.Diff(tt.kept, dirEntries(t, base), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("entries of the injects base after a second run mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunWorkflowDeleteLogFields(t *testing.T) {
	leftover := ".lab.tmp-" + deleteTokenA
	base := newDeleteBase(t, "lab", leftover)
	fake := newFakeDeleter(t, fakeDelete{
		options: deleteServerOptions(base),
		result:  workflow.DeleteResult{Experiment: "exp-lab", Stopped: true, InjectsUsedBy: []string{"a", "b"}},
	})
	logs := captureLogs(t)

	opts := testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{})
	opts.Force = true

	if err := runWorkflowDelete(t.Context(), opts, "lab"); err != nil {
		t.Fatalf("runWorkflowDelete(lab) error = %v", err)
	}

	checkFields(t, logs.first(t, "experiment stopped"), map[string]string{"level": "INFO", "exp": "exp-lab", "name": "lab"})
	checkFields(t, logs.first(t, "experiment deleted"), map[string]string{"level": "INFO", "exp": "exp-lab", "name": "lab"})
	checkFields(t, logs.first(t, "staging leftover deleted"), map[string]string{
		"level": "INFO", "path": filepath.Join(base, leftover),
	})
	checkFields(t, logs.first(t, "injects directory kept: other experiments inject files from it"), map[string]string{
		"level":       "WARN",
		"dir":         filepath.Join(base, "lab"),
		"experiments": "a, b",
		"hint":        "delete those experiments or remove their injections from that directory, then run phenix workflow delete lab again",
	})
}

func TestRunWorkflowDeleteDryRun(t *testing.T) {
	leftover := ".lab.old-" + deleteTokenB

	tests := []struct {
		name    string
		entries []string
		force   bool
		result  workflow.DeleteResult
		want    []string
		wantLog string
	}{
		{
			name:    "running experiment with force",
			entries: []string{"lab", leftover, "lab-a"},
			force:   true,
			result:  workflow.DeleteResult{Experiment: "lab", Stopped: true},
			want: []string{
				"would stop experiment lab",
				"would delete experiment lab",
				"would delete directory <base>/lab",
				"would delete staging leftover <base>/" + leftover,
			},
			wantLog: "dry run complete; nothing was changed",
		},
		{
			name:    "stopped experiment",
			entries: []string{"lab"},
			result:  workflow.DeleteResult{Experiment: "exp"},
			want:    []string{"would delete experiment exp", "would delete directory <base>/lab"},
			wantLog: "dry run complete; nothing was changed",
		},
		{
			name:    "injects used by other experiments",
			entries: []string{"lab", leftover},
			result:  workflow.DeleteResult{Experiment: "lab", InjectsUsedBy: []string{"a", "b"}},
			want: []string{
				"would delete experiment lab",
				"would keep directory <base>/lab: the experiments a, b inject files from it",
				"would delete staging leftover <base>/" + leftover,
			},
			wantLog: "dry run complete; nothing was changed",
		},
		{
			name:    "nothing to delete",
			entries: []string{"lab-a"},
			want:    []string{},
			wantLog: "nothing to delete for branch name lab",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newDeleteBase(t, tt.entries...)
			before := treeSnapshot(t, base)
			fake := newFakeDeleter(t, fakeDelete{options: deleteServerOptions(base), result: tt.result})
			logs := captureLogs(t)

			var out bytes.Buffer

			opts := testDeleteOptions(fake.socket, t.TempDir(), &out)
			opts.DryRun, opts.Force = true, tt.force

			if err := runWorkflowDelete(t.Context(), opts, "lab"); err != nil {
				t.Fatalf("runWorkflowDelete(lab) error = %v", err)
			}

			wantQuery := url.Values{"dryRun": {boolStringTrue}, "injects": {filepath.Join(base, "lab")}}
			if tt.force {
				wantQuery.Set("force", boolStringTrue)
			}

			if diff := cmp.Diff(wantQuery, deleteQuery(t, fake)); diff != "" {
				t.Errorf("delete query mismatch (-want +got):\n%s", diff)
			}

			got := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			if out.Len() == 0 {
				got = []string{}
			}

			want := make([]string, 0, len(tt.want))
			for _, line := range tt.want {
				want = append(want, strings.ReplaceAll(line, "<base>", base))
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("dry run output mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(before, treeSnapshot(t, base)); diff != "" {
				t.Errorf("the dry run changed the injects base (-before +after):\n%s", diff)
			}

			checkFields(t, logs.first(t, tt.wantLog), map[string]string{"level": "INFO", "name": "lab"})
			checkNotLogged(t, logs, "experiment deleted", "injects directory deleted", "staging leftover deleted")
		})
	}
}

func TestRunWorkflowDeleteServerRefuses(t *testing.T) {
	tests := []struct {
		name       string
		dryRun     bool
		status     int
		cause      string
		wantInText []string
	}{
		{
			name:       "running experiment without force",
			status:     http.StatusConflict,
			cause:      runningConflict(t),
			wantInText: []string{"409 Conflict", "-f", "nothing was deleted"},
		},
		{
			name:       "running experiment without force, dry run",
			dryRun:     true,
			status:     http.StatusConflict,
			cause:      runningConflict(t),
			wantInText: []string{"409 Conflict", "-f", "nothing was deleted"},
		},
		{
			name:       "more than one experiment mapped",
			status:     http.StatusConflict,
			cause:      `workflow conflict: 2 experiments are mapped to workflow branch "lab" (a, b); at most one is allowed`,
			wantInText: []string{"409 Conflict", "(a, b)", "nothing was deleted"},
		},
		{
			name:       "delete failed",
			status:     http.StatusInternalServerError,
			cause:      "deleting experiment lab: disk busy",
			wantInText: []string{"500 Internal Server Error", "disk busy", "the injects were not deleted"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newDeleteBase(t, "lab", ".lab.tmp-"+deleteTokenA)
			before := treeSnapshot(t, base)
			fake := newFakeDeleter(t, fakeDelete{options: deleteServerOptions(base), status: tt.status, cause: tt.cause})

			var out bytes.Buffer

			opts := testDeleteOptions(fake.socket, t.TempDir(), &out)
			opts.DryRun = tt.dryRun

			err := runWorkflowDelete(t.Context(), opts, "lab")

			var apiErr *workflowAPIError
			if !errors.As(err, &apiErr) || apiErr.Status != tt.status {
				t.Fatalf("runWorkflowDelete(lab) error = %v, want a %d from the server", err, tt.status)
			}

			for _, part := range tt.wantInText {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("runWorkflowDelete(lab) error = %q, want it to contain %q", err, part)
				}
			}

			wantDelete := reqDelete
			if tt.dryRun {
				wantDelete += " dryRun"
			}

			checkRequests(t, fake, reqOptions, wantDelete)

			if diff := cmp.Diff(before, treeSnapshot(t, base)); diff != "" {
				t.Errorf("a refused delete changed the injects base (-before +after):\n%s", diff)
			}

			if out.Len() != 0 {
				t.Errorf("output = %q, want none", out.String())
			}
		})
	}
}

func TestRunWorkflowDeleteStopsBeforeTheDelete(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		options  map[string]any // nil for the options of a current server with the base
		setup    func(t *testing.T, base string, opts *deleteOptions)
		wantReqs []string
		wantIs   error
		wantText string
	}{
		{name: "empty name", branch: "", wantIs: workflow.ErrInvalidName},
		{name: "path as name", branch: "../lab", wantIs: workflow.ErrInvalidName},
		{name: "name with a separator", branch: "topologies/lab", wantIs: workflow.ErrInvalidName},
		{name: "reserved name", branch: "phenix-injects", wantText: `the name "phenix-injects" is reserved`},
		{name: "reserved name in another letter case", branch: "PHENIX.YML", wantText: `the name "PHENIX.YML" is reserved`},
		{
			name:     "no socket",
			branch:   "lab",
			setup:    func(_ *testing.T, _ string, opts *deleteOptions) { opts.Socket = "" },
			wantText: "no phenix unix socket configured",
		},
		{
			name:     "older server",
			branch:   "lab",
			options:  serverOptions("/phenix/injects"),
			wantReqs: []string{reqOptions},
			wantText: "does not support workflow delete; upgrade the phenix server",
		},
		{
			name:     "option not a bool",
			branch:   "lab",
			options:  map[string]any{optionWorkflowDryRun: true, optionWorkflowDelete: "true"},
			wantReqs: []string{reqOptions},
			wantText: "does not support workflow delete; upgrade the phenix server",
		},
		{
			name:     "relative server injects directory",
			branch:   "lab",
			options:  deleteServerOptions("injects"),
			wantReqs: []string{reqOptions},
			wantText: `the phenix server reports the relative injects directory "injects"; pass --base-dir.injects`,
		},
		{
			name:   "working directory inside the injects directory",
			branch: "lab",
			setup: func(t *testing.T, base string, _ *deleteOptions) {
				t.Helper()

				t.Chdir(filepath.Join(base, "lab"))
			},
			wantReqs: []string{reqOptions},
			wantIs:   workflow.ErrUnsafeDestination,
		},
		{
			name:   "injects directory is a file",
			branch: "lab",
			setup: func(t *testing.T, base string, _ *deleteOptions) {
				t.Helper()

				if err := os.RemoveAll(filepath.Join(base, "lab")); err != nil {
					t.Fatal(err)
				}

				writeWorkflowTree(t, base, map[string]string{"lab": "not a directory"})
			},
			wantReqs: []string{reqOptions},
			wantIs:   workflow.ErrUnsafeDestination,
		},
		{
			name:   "injects base is a topology directory",
			branch: "lab",
			setup: func(t *testing.T, base string, _ *deleteOptions) {
				t.Helper()

				writeWorkflowTree(t, base, map[string]string{workflowConfigName: wfWorkflowYAML})
			},
			wantReqs: []string{reqOptions},
			wantText: "marks a topology directory; check --base-dir.injects",
		},
		{
			name:   "injects base inside a topology directory",
			branch: "lab",
			setup: func(t *testing.T, base string, opts *deleteOptions) {
				t.Helper()

				writeWorkflowTree(t, base, map[string]string{
					"topo/" + workflowConfigName:           wfWorkflowYAML,
					"topo/" + injectsDirName + "/lab/motd": "hello\n",
				})

				opts.InjectsBase, opts.InjectsBaseExplicit = filepath.Join(base, "topo", injectsDirName), true
			},
			wantReqs: []string{reqOptions},
			wantText: "which holds phenix.yml, a name that marks a topology directory; check --base-dir.injects",
		},
		{
			name:   "injects directory is a topology directory",
			branch: "lab",
			setup: func(t *testing.T, base string, _ *deleteOptions) {
				t.Helper()

				writeWorkflowTree(t, base, map[string]string{"lab/" + configsDirName + "/topology.yml": wfTopologyYAML})
			},
			wantReqs: []string{reqOptions},
			wantText: "marks a topology directory; check --base-dir.injects",
		},
		{
			name:   "injects directory holds a topology directory",
			branch: "lab",
			setup: func(t *testing.T, base string, _ *deleteOptions) {
				t.Helper()

				writeWorkflowTree(t, base, map[string]string{"lab/helloworld/" + workflowConfigName: wfWorkflowYAML})
			},
			wantReqs: []string{reqOptions},
			wantText: "marks a topology directory; check --base-dir.injects",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newDeleteBase(t, "lab", ".lab.tmp-"+deleteTokenA)

			options := tt.options
			if options == nil {
				options = deleteServerOptions(base)
			}

			fake := newFakeDeleter(t, fakeDelete{options: options, result: workflow.DeleteResult{Experiment: "lab"}})
			opts := testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{})

			if tt.setup != nil {
				tt.setup(t, base, &opts)
			}

			before := treeSnapshot(t, base)

			err := runWorkflowDelete(t.Context(), opts, tt.branch)
			if err == nil {
				t.Fatalf("runWorkflowDelete(%q) error = nil, want an error", tt.branch)
			}

			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("runWorkflowDelete(%q) error = %v, want errors.Is(err, %v)", tt.branch, err, tt.wantIs)
			}

			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("runWorkflowDelete(%q) error = %q, want it to contain %q", tt.branch, err, tt.wantText)
			}

			checkRequests(t, fake, tt.wantReqs...)

			if diff := cmp.Diff(before, treeSnapshot(t, base)); diff != "" {
				t.Errorf("a refused delete changed the injects base (-before +after):\n%s", diff)
			}
		})
	}
}

func TestRunWorkflowDeleteInjectsBase(t *testing.T) {
	tests := []struct {
		name     string
		server   bool // the server reports the injects base
		explicit bool // --base-dir.injects is on the command line
		relative bool // the local setting is relative to the working directory
		want     string
	}{
		{name: "server setting", server: true, want: "server"},
		{name: "command line flag over the server setting", server: true, explicit: true, want: "local"},
		{name: "local setting when the server has none", want: "local"},
		{name: "relative command line flag", server: true, explicit: true, relative: true, want: "local"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newDeleteBase(t, "server/lab", "local/lab")

			options := map[string]any{optionWorkflowDryRun: true, optionWorkflowDelete: true}
			if tt.server {
				options[optionInjectsBase] = filepath.Join(root, "server")
			}

			fake := newFakeDeleter(t, fakeDelete{options: options, result: workflow.DeleteResult{Experiment: "lab"}})

			local := filepath.Join(root, "local")
			if tt.relative {
				t.Chdir(root)

				local = "local"
			}

			opts := testDeleteOptions(fake.socket, local, &bytes.Buffer{})
			opts.InjectsBaseExplicit = tt.explicit

			if err := runWorkflowDelete(t.Context(), opts, "lab"); err != nil {
				t.Fatalf("runWorkflowDelete(lab) error = %v", err)
			}

			if got, want := deleteQuery(t, fake).Get("injects"), filepath.Join(root, tt.want, "lab"); got != want {
				t.Errorf("injects query = %q, want %q", got, want)
			}

			for _, dir := range []string{"server", "local"} {
				_, err := os.Stat(filepath.Join(root, dir, "lab"))
				if removed := errors.Is(err, fs.ErrNotExist); removed != (dir == tt.want) {
					t.Errorf("%s/lab removed = %t, want %t (err = %v)", dir, removed, dir == tt.want, err)
				}
			}
		})
	}
}

func TestRunWorkflowDeleteRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}

	base := newDeleteBase(t, "lab")
	fake := newFakeDeleter(t, fakeDelete{options: deleteServerOptions(base), result: workflow.DeleteResult{Experiment: "lab"}})

	if err := os.Chmod(base, 0o555); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(base, 0o755) })

	err := runWorkflowDelete(t.Context(), testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{}), "lab")
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("runWorkflowDelete(lab) error = %v, want errors.Is(err, fs.ErrPermission)", err)
	}

	for _, part := range []string{"run phenix as a user who can write to " + base, "already deleted: experiment lab"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("runWorkflowDelete(lab) error = %q, want it to contain %q", err, part)
		}
	}

	checkRequests(t, fake, reqOptions, reqDelete)
}

func TestRunWorkflowDeleteDryRunTimeout(t *testing.T) {
	lowerPreflightTimeout(t, 100*time.Millisecond)

	base := newDeleteBase(t, "lab")
	fake := newFakeDeleter(t, fakeDelete{options: deleteServerOptions(base), hang: true})

	opts := testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{})
	opts.DryRun = true

	err := runWorkflowDelete(t.Context(), opts, "lab")
	if !errors.Is(err, errServerUnreachable) {
		t.Fatalf("runWorkflowDelete(lab) error = %v, want errors.Is(err, errServerUnreachable)", err)
	}

	if _, err := os.Stat(filepath.Join(base, "lab")); err != nil {
		t.Errorf("the injects directory after a failed dry run: %v, want it kept", err)
	}
}

func TestRunWorkflowDeleteServerUnreachable(t *testing.T) {
	base := newDeleteBase(t, "lab")

	err := runWorkflowDelete(t.Context(), testDeleteOptions(shortSocketPath(t), base, &bytes.Buffer{}), "lab")
	if !errors.Is(err, errServerUnreachable) {
		t.Fatalf("runWorkflowDelete(lab) error = %v, want errors.Is(err, errServerUnreachable)", err)
	}

	if _, err := os.Stat(filepath.Join(base, "lab")); err != nil {
		t.Errorf("the injects directory: %v, want it kept", err)
	}
}

// TestRunWorkflowDeleteFlagExamples runs the examples of the delete help
// that differ in their flags.
func TestRunWorkflowDeleteFlagExamples(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(opts *deleteOptions, other string)
		wantQuery url.Values // without injects
		wantOther bool       // the injects are in the other base
		removed   bool
	}{
		{
			name:      "phenix workflow delete helloworld",
			setup:     func(*deleteOptions, string) {},
			wantQuery: url.Values{},
			removed:   true,
		},
		{
			name:      "phenix workflow delete helloworld -n",
			setup:     func(opts *deleteOptions, _ string) { opts.DryRun = true },
			wantQuery: url.Values{"dryRun": {boolStringTrue}},
		},
		{
			name:      "phenix workflow delete helloworld -f",
			setup:     func(opts *deleteOptions, _ string) { opts.Force = true },
			wantQuery: url.Values{"force": {boolStringTrue}},
			removed:   true,
		},
		{
			name: "phenix --base-dir.injects /srv/injects workflow delete helloworld",
			setup: func(opts *deleteOptions, other string) {
				opts.InjectsBase, opts.InjectsBaseExplicit = other, true
			},
			wantQuery: url.Values{},
			wantOther: true,
			removed:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newDeleteBase(t, "helloworld")
			other := newDeleteBase(t, "helloworld")
			fake := newFakeDeleter(t, fakeDelete{
				options: deleteServerOptions(base),
				result:  workflow.DeleteResult{Experiment: "helloworld"},
			})

			opts := testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{})
			tt.setup(&opts, other)

			if err := runWorkflowDelete(t.Context(), opts, "helloworld"); err != nil {
				t.Fatalf("runWorkflowDelete(helloworld) error = %v", err)
			}

			used := base
			if tt.wantOther {
				used = other
			}

			wantQuery := tt.wantQuery
			wantQuery.Set("injects", filepath.Join(used, "helloworld"))

			if diff := cmp.Diff(wantQuery, deleteQuery(t, fake)); diff != "" {
				t.Errorf("delete query mismatch (-want +got):\n%s", diff)
			}

			if _, err := os.Stat(filepath.Join(used, "helloworld")); errors.Is(err, fs.ErrNotExist) != tt.removed {
				t.Errorf("%s/helloworld removed = %t, want %t", used, !tt.removed, tt.removed)
			}
		})
	}
}

func TestWorkflowDeleteCommand(t *testing.T) {
	cmd := newWorkflowDeleteCmd()

	if cmd.Use != "delete <NAME>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "delete <NAME>")
	}

	for _, want := range []struct{ name, shorthand string }{{"dry-run", "n"}, {"force", "f"}} {
		flag := cmd.Flags().Lookup(want.name)
		if flag == nil {
			t.Errorf("flag --%s is missing", want.name)

			continue
		}

		if flag.Shorthand != want.shorthand || flag.DefValue != "false" {
			t.Errorf("flag --%s = -%s default %q, want -%s default false", want.name, flag.Shorthand, flag.DefValue, want.shorthand)
		}
	}

	for _, args := range [][]string{nil, {"a", "b"}} {
		if err := cmd.ValidateArgs(args); err == nil {
			t.Errorf("delete accepted %d arguments", len(args))
		}
	}

	lines := map[string]bool{}
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		lines[strings.TrimSpace(line)] = true
	}

	for _, example := range []string{
		"phenix workflow delete helloworld",
		"phenix workflow delete helloworld -n",
		"phenix workflow delete helloworld -f",
		"phenix --unix-socket /run/phenix.sock workflow delete helloworld",
		"phenix --base-dir.injects /srv/injects workflow delete helloworld",
		"docker exec phenix phenix workflow delete helloworld -f",
	} {
		if !lines[example] {
			t.Errorf("Example is missing %q", example)
		}
	}

	if got := commandAtPath(t, "phenix workflow delete"); got.Use != cmd.Use {
		t.Errorf("phenix workflow delete Use = %q, want %q", got.Use, cmd.Use)
	}
}

// TestRunWorkflowDeleteRefusesTheTopologiesDirectory deletes the injects
// directory of a name that is the topologies directory or holds it, as with a
// base of / and the name of the directory that holds everything. Each row
// runs once as a real run and once with -n. Neither sends the delete or
// changes the disk.
func TestRunWorkflowDeleteRefusesTheTopologiesDirectory(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		links      map[string]string
		base       string // under the root; with relative, from work/foo as written
		relative   bool
		topologies string // under the root; with relative, as written
		wantDir    string // under the root
	}{
		{
			name: "the directory that holds the topologies directory",
			files: mergedFiles(
				underDir("grand/topologies/bar", fullTopology()),
				map[string]string{"grand/notes": "notes"},
			),
			topologies: "grand/topologies",
			wantDir:    "grand",
		},
		{
			name:       "a topologies directory given through a symbolic link",
			files:      underDir("grand/topologies/bar", fullTopology()),
			links:      map[string]string{"toplink": "grand/topologies"},
			topologies: "toplink",
			wantDir:    "grand",
		},
		{
			name:       "a relative --base-dir.injects and --base-dir.topologies",
			files:      mergedFiles(underDir("work/grand/topologies/bar", fullTopology()), map[string]string{"work/foo/notes": "notes"}),
			base:       "..",
			relative:   true,
			topologies: "../grand/topologies",
			wantDir:    "work/grand",
		},
		{
			name:       "a topologies directory that holds no marker",
			files:      map[string]string{"grand/topologies/notes": "notes"},
			topologies: "grand/topologies",
			wantDir:    "grand",
		},
	}

	for _, tt := range tests {
		for _, dryRun := range []bool{false, true} {
			name := tt.name
			if dryRun {
				name += ", with -n"
			}

			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				writeWorkflowTree(t, root, tt.files)

				for link, target := range tt.links {
					if err := os.Symlink(filepath.Join(root, filepath.FromSlash(target)), filepath.Join(root, link)); err != nil {
						t.Fatalf("linking %s: %v", link, err)
					}
				}

				fake := newFakeDeleter(t, fakeDelete{
					options: deleteServerOptions(root),
					result:  workflow.DeleteResult{Experiment: "grand"},
				})

				opts := testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{})
				opts.DryRun = dryRun
				opts.TopologiesBase = filepath.Join(root, filepath.FromSlash(tt.topologies))

				if tt.relative {
					t.Chdir(filepath.Join(root, "work", "foo"))

					opts.InjectsBase, opts.InjectsBaseExplicit = tt.base, true
					opts.TopologiesBase = tt.topologies
				}

				before := treeSnapshot(t, root)

				err := runWorkflowDelete(t.Context(), opts, "grand")

				want := fmt.Sprintf(
					"refusing to delete %s: it is or holds the topologies directory %s; check --base-dir.injects",
					filepath.Join(root, filepath.FromSlash(tt.wantDir)), opts.TopologiesBase,
				)
				if err == nil || err.Error() != want {
					t.Fatalf("runWorkflowDelete(grand) error = %v, want %q", err, want)
				}

				checkRequests(t, fake, reqOptions)

				if diff := cmp.Diff(before, treeSnapshot(t, root)); diff != "" {
					t.Errorf("a refused delete changed the disk (-before +after):\n%s", diff)
				}
			})
		}
	}
}

// TestRunWorkflowDeleteInterrupted interrupts the run while the server
// handles the delete. The run removes no injects, and when the server fails
// the delete, the error says that the server may still finish it.
func TestRunWorkflowDeleteInterrupted(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantIs   error
		wantText string
	}{
		{name: "the server answers 200", wantIs: context.Canceled},
		{
			name: "the server answers 500", status: http.StatusInternalServerError,
			wantText: "the phenix server may still finish the request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newDeleteBase(t, "lab", ".lab.tmp-"+deleteTokenA)
			before := treeSnapshot(t, base)

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			fake := newFakeDeleter(t, fakeDelete{
				options:  deleteServerOptions(base),
				onDelete: cancel,
				status:   tt.status,
				cause:    "deleting experiment lab: disk busy",
				result:   workflow.DeleteResult{Experiment: "lab"},
			})

			err := runWorkflowDelete(ctx, testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{}), "lab")
			if err == nil {
				t.Fatal("runWorkflowDelete(lab) error = nil, want an error")
			}

			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("runWorkflowDelete(lab) error = %v, want errors.Is(err, %v)", err, tt.wantIs)
			}

			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("runWorkflowDelete(lab) error = %q, want it to contain %q", err, tt.wantText)
			}

			if diff := cmp.Diff(before, treeSnapshot(t, base)); diff != "" {
				t.Errorf("an interrupted delete changed the injects base (-before +after):\n%s", diff)
			}
		})
	}
}

// TestRunWorkflowDeleteRemovesALink deletes an injects directory that is a
// symbolic link to a topology directory: the link goes, and its target stays.
func TestRunWorkflowDeleteRemovesALink(t *testing.T) {
	base := newDeleteBase(t)
	target := newDeleteBase(t)
	writeWorkflowTree(t, target, fullTopology())

	if err := os.Symlink(target, filepath.Join(base, "lab")); err != nil {
		t.Fatal(err)
	}

	before := treeSnapshot(t, target)
	fake := newFakeDeleter(t, fakeDelete{options: deleteServerOptions(base), result: workflow.DeleteResult{Experiment: "lab"}})

	if err := runWorkflowDelete(t.Context(), testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{}), "lab"); err != nil {
		t.Fatalf("runWorkflowDelete(lab) error = %v", err)
	}

	if _, err := os.Lstat(filepath.Join(base, "lab")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link after the delete: %v, want it removed", err)
	}

	if diff := cmp.Diff(before, treeSnapshot(t, target)); diff != "" {
		t.Errorf("the delete changed the target of the link (-before +after):\n%s", diff)
	}
}

// TestRunWorkflowDeleteConnectionDropped closes the connection of the delete
// request without an answer. A real delete says that the server may have
// deleted the experiment, and a dry run does not. Both keep the injects.
func TestRunWorkflowDeleteConnectionDropped(t *testing.T) {
	const mayHave = "the phenix server may have deleted the experiment"

	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry run %t", dryRun), func(t *testing.T) {
			base := newDeleteBase(t, "lab")
			before := treeSnapshot(t, base)
			fake := newFakeDeleter(t, fakeDelete{options: deleteServerOptions(base), drop: true})

			opts := testDeleteOptions(fake.socket, t.TempDir(), &bytes.Buffer{})
			opts.DryRun = dryRun

			err := runWorkflowDelete(t.Context(), opts, "lab")
			if !errors.Is(err, errServerUnreachable) {
				t.Fatalf("runWorkflowDelete(lab) error = %v, want errors.Is(err, errServerUnreachable)", err)
			}

			if got := strings.Contains(err.Error(), mayHave); got != !dryRun {
				t.Errorf("runWorkflowDelete(lab) error = %q; says %q = %t, want %t", err, mayHave, got, !dryRun)
			}

			if diff := cmp.Diff(before, treeSnapshot(t, base)); diff != "" {
				t.Errorf("the delete changed the injects base (-before +after):\n%s", diff)
			}
		})
	}
}

// TestRunWorkflowDeleteOtherAnswers checks an answer that is not a delete
// result: a dry run that the server did not do, and a body that is not JSON.
// The run removes no injects.
func TestRunWorkflowDeleteOtherAnswers(t *testing.T) {
	var syntaxErr *json.SyntaxError

	tests := []struct {
		name   string
		dryRun bool
		fake   fakeDelete
		check  func(err error) bool
		want   string
	}{
		{
			name:   "server that ignores dryRun",
			dryRun: true,
			fake:   fakeDelete{ignoreDryRun: true, result: workflow.DeleteResult{Experiment: "lab"}},
			check:  func(err error) bool { return errors.Is(err, errNoDeleteDryRun) },
			want:   "errors.Is(err, errNoDeleteDryRun)",
		},
		{
			name:   "body that is not JSON, dry run",
			dryRun: true,
			fake:   fakeDelete{raw: "<html>"},
			check:  func(err error) bool { return errors.As(err, &syntaxErr) },
			want:   "a *json.SyntaxError",
		},
		{
			name:  "body that is not JSON",
			fake:  fakeDelete{raw: "<html>"},
			check: func(err error) bool { return errors.As(err, &syntaxErr) },
			want:  "a *json.SyntaxError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newDeleteBase(t, "lab", ".lab.tmp-"+deleteTokenA)
			before := treeSnapshot(t, base)

			d := tt.fake
			d.options = deleteServerOptions(base)
			fake := newFakeDeleter(t, d)

			var out bytes.Buffer

			opts := testDeleteOptions(fake.socket, t.TempDir(), &out)
			opts.DryRun = tt.dryRun

			if err := runWorkflowDelete(t.Context(), opts, "lab"); !tt.check(err) {
				t.Fatalf("runWorkflowDelete(lab) error = %v, want %s", err, tt.want)
			}

			if diff := cmp.Diff(before, treeSnapshot(t, base)); diff != "" {
				t.Errorf("the delete changed the injects base (-before +after):\n%s", diff)
			}

			if out.Len() != 0 {
				t.Errorf("output = %q, want none", out.String())
			}
		})
	}
}
