package cmd

// The helpers of the workflow tests: a fake phenix server over a unix
// socket, a log capture, and topology directories on disk.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/util/plog"
)

// Minimal valid configs and a workflow config. The fake phenix server reads
// only a config's kind and name, but valid configs keep the test directories
// realistic.
const (
	wfTopologyYAML = "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: foo\nspec:\n  nodes: []\n"
	wfScenarioYAML = "apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: foo\n  annotations:\n    topology: foo\n" +
		"spec:\n  apps: []\n"
	wfWorkflowYAML = "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata: {}\n" +
		"spec:\n  auto:\n    create: foo\n  topology: foo\n  scenario: foo\n"
)

const (
	reqOptions = "GET /api/v1/options"

	wfExplainedLine  = `spec (line 6): property "restrat" is unsupported (at auto.restrat)`
	cfgExplainedLine = `nodes[0] "host-a" (line 12): property "image" is missing (at hardware.drives[0].image)`
	cfgHintLine      = `  hint: "image:" is on line 10 under nodes[0].hardware, ` +
		`but the schema expects it at nodes[0].hardware.drives[0].image`
)

// applyTestNow is the fixed clock of the workflow apply tests.
func applyTestNow() time.Time {
	return time.Date(2026, time.September, 23, 12, 0, 0, 0, time.UTC)
}

// testApplyOptions returns the options of a plain apply through socket, with
// localInjects as the local injects setting and a fixed clock.
func testApplyOptions(socket, localInjects string) applyOptions {
	return applyOptions{
		Name:                "",
		Config:              workflowConfigName,
		ConfigExplicit:      false,
		DryRun:              false,
		Force:               false,
		Socket:              socket,
		InjectsBase:         localInjects,
		TopologiesBase:      "",
		InjectsBaseExplicit: false,
		Now:                 applyTestNow,
	}
}

// writeWorkflowTree writes files, keyed by slash-separated relative paths,
// under dir.
func writeWorkflowTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))

		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}

		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
}

// newTopologyDir writes files into a new directory called name and returns
// its path. Git discovery stops at its parent, so the tags never pick up a
// commit from an enclosing repository.
func newTopologyDir(t *testing.T, name string, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}

	writeWorkflowTree(t, dir, files)

	return dir
}

// fullTopology returns the files of a topology directory with all three
// inputs. Its configs are Topology/foo and Scenario/foo.
func fullTopology() map[string]string {
	return map[string]string{
		"phenix-injects/scripts/hello.sh": "echo hello\n",
		"phenix-configs/scenario.yml":     wfScenarioYAML,
		"phenix-configs/topology.yml":     wfTopologyYAML,
		workflowConfigName:                wfWorkflowYAML,
	}
}

// scenarioNamed returns a scenario config named name, followed by extra.
func scenarioNamed(name, extra string) string {
	return strings.Replace(wfScenarioYAML, "name: foo", "name: "+name, 1) + extra
}

// underDir returns files with each slash-separated path put under dir.
func underDir(dir string, files map[string]string) map[string]string {
	moved := make(map[string]string, len(files))

	for rel, content := range files {
		moved[dir+"/"+rel] = content
	}

	return moved
}

// mergedFiles returns the union of the given file sets.
func mergedFiles(sets ...map[string]string) map[string]string {
	all := map[string]string{}

	for _, set := range sets {
		maps.Copy(all, set)
	}

	return all
}

// treeSnapshot describes every entry under root by its path relative to
// root: its type and permission bits, and the content of a regular file or
// the target of a symbolic link. Symbolic links are not followed.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()

	snapshot := map[string]string{}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		desc := info.Mode().String()

		switch {
		case info.Mode().IsRegular():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			desc += " " + string(body)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}

			desc += " -> " + target
		}

		snapshot[rel] = desc

		return nil
	})
	if err != nil {
		t.Fatalf("describing %s: %v", root, err)
	}

	return snapshot
}

// assertEmptyDir fails the test unless dir is an empty directory.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("%s holds %d entries (err = %v), want none", dir, len(entries), err)
	}
}

// recordedRequest is one request received by a fakeServer.
type recordedRequest struct {
	method      string
	path        string
	query       url.Values
	contentType string
	body        string
}

// fakeServer stands in for the phenix web server on a unix socket. It records
// every request before passing it to its handler.
type fakeServer struct {
	socket string

	mu       sync.Mutex
	requests []recordedRequest
}

// newFakeServer starts a fakeServer that answers with handler and stops it
// when the test ends.
func newFakeServer(t *testing.T, handler http.HandlerFunc) *fakeServer {
	t.Helper()

	fake := &fakeServer{socket: shortSocketPath(t)}

	listener, err := net.Listen("unix", fake.socket)
	if err != nil {
		t.Fatalf("listening on %s: %v", fake.socket, err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		fake.mu.Lock()
		fake.requests = append(fake.requests, recordedRequest{
			method:      r.Method,
			path:        r.URL.EscapedPath(),
			query:       r.URL.Query(),
			contentType: r.Header.Get("Content-Type"),
			body:        string(body),
		})
		fake.mu.Unlock()

		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}))

	_ = srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)

	return fake
}

// recorded returns a copy of the requests received so far.
func (f *fakeServer) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.requests)
}

// shortSocketPath returns the path of a unix socket in a new temporary
// directory. macOS limits unix socket paths to 104 bytes, and t.TempDir paths
// can be longer.
func shortSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "wf") //nolint:usetesting // unix socket path length limit
	if err != nil {
		t.Fatalf("creating socket directory: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return filepath.Join(dir, "s")
}

// lowerPreflightTimeout bounds each preflight request by d until the test
// ends.
func lowerPreflightTimeout(t *testing.T, d time.Duration) {
	t.Helper()

	orig := preflightRequestTimeout
	t.Cleanup(func() { preflightRequestTimeout = orig })

	preflightRequestTimeout = d
}

// dropConnection closes the connection without answering, as a server that
// stops in the middle of a request does.
func dropConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Error("the response writer cannot take over the connection")

		return
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		t.Errorf("taking over the connection: %v", err)

		return
	}

	_ = conn.Close()
}

// fakePhenix describes how a fake phenix server answers. Every answer is
// canned: the fake models nothing of the server. options is the body of
// GET /api/v1/options, and plan the plan of every workflow apply.
//
// A config dry run is answered with the next of results or, once they are
// used up, with create and a Kind/name pending ref read from the config. A
// config whose body contains reject gets a 400 with explained validation
// lines, and one whose body contains fail a bare 500; with upsertOnly, only
// the real upserts fail that way, as with a config hook. With ignoreDryRun,
// config dry runs get the 201 of an older server that stores the config.
//
// A non-zero planStatus fails the workflow dry run, with planValidation as
// its validation lines when set, and planNotDryRun answers it with dryRun
// false, as a server that ignores dryRun does. A non-zero applyStatus fails
// the real apply. The request described as hangOn (see requestLine) calls
// cancel, when set, and waits for the client to go away, and the one
// described as drop loses its connection, as when the server stops.
type fakePhenix struct {
	options        map[string]any
	plan           workflow.Plan
	results        []workflow.ConfigResult
	reject         string
	fail           string
	upsertOnly     bool
	ignoreDryRun   bool
	planStatus     int
	planValidation string
	planNotDryRun  bool
	applyStatus    int
	hangOn         string
	drop           string
	cancel         context.CancelFunc
}

// newFakePhenix starts a fake phenix server that answers as p describes.
func newFakePhenix(t *testing.T, p fakePhenix) *fakeServer {
	t.Helper()

	var (
		mu      sync.Mutex
		results = slices.Clone(p.results)
	)

	return newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		line := requestLine(r.Method, r.URL.EscapedPath(), query)
		dryRun := query.Get("dryRun") == boolStringTrue
		body, _ := io.ReadAll(r.Body)

		switch {
		case line == p.hangOn:
			if p.cancel != nil {
				p.cancel()
			}

			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case line == p.drop:
			dropConnection(t, w)
		case r.URL.Path == "/api/v1/options":
			writeJSON(t, w, http.StatusOK, p.options)
		case strings.HasPrefix(r.URL.Path, "/api/v1/workflow/configs/"):
			mu.Lock()
			defer mu.Unlock()

			results = p.config(t, w, string(body), dryRun, results)
		case dryRun && p.planStatus != 0:
			fakeFailure(t, w, p.planStatus, p.planValidation)
		case !dryRun && p.applyStatus != 0:
			fakeFailure(t, w, p.applyStatus, "")
		default:
			writeJSON(t, w, http.StatusOK, workflow.Result{Plan: p.plan, DryRun: dryRun && !p.planNotDryRun})
		}
	})
}

// config answers a config dry run or upsert as the fake describes, and
// returns the canned results it has not used yet.
func (p fakePhenix) config(
	t *testing.T,
	w http.ResponseWriter,
	body string,
	dryRun bool,
	results []workflow.ConfigResult,
) []workflow.ConfigResult {
	t.Helper()

	failing := !dryRun || !p.upsertOnly

	switch {
	case dryRun && p.ignoreDryRun:
		w.WriteHeader(http.StatusCreated)
	case failing && p.reject != "" && strings.Contains(body, p.reject):
		writeJSON(t, w, http.StatusBadRequest, map[string]any{
			"cause":   "validating config: config validation failed: raw validator text",
			"message": cfgExplainedLine,
			"metadata": map[string]string{
				"validation":     cfgExplainedLine + "\n" + cfgHintLine,
				"validation-raw": "validating config: config validation failed: raw validator text",
			},
		})
	case failing && p.fail != "" && strings.Contains(body, p.fail):
		w.WriteHeader(http.StatusInternalServerError)
	case !dryRun:
		w.WriteHeader(http.StatusCreated)
	case len(results) > 0:
		writeJSON(t, w, http.StatusOK, results[0])

		return results[1:]
	default:
		writeJSON(t, w, http.StatusOK, fakeConfigResult(t, body))
	}

	return results
}

// fakeConfigResult answers the dry run of a config the fake accepts: create,
// with the Kind/name the config declares as its pending ref.
func fakeConfigResult(t *testing.T, body string) workflow.ConfigResult {
	t.Helper()

	var cfg store.Config

	if err := yaml.Unmarshal(neutralizePlaceholders([]byte(body)), &cfg); err != nil {
		t.Errorf("the fake phenix server cannot read a config: %v", err)
	}

	return workflow.ConfigResult{
		Action:  workflow.ActionCreate,
		Kind:    cfg.Kind,
		Name:    cfg.Metadata.Name,
		DryRun:  true,
		Pending: cfg.Kind + "/" + cfg.Metadata.Name,
	}
}

// fakeFailure writes an error envelope with status. A non-empty validation
// becomes the explained validation lines, and its first line the message.
func fakeFailure(t *testing.T, w http.ResponseWriter, status int, validation string) {
	t.Helper()

	body := map[string]any{"cause": "fake cause", "message": "fake message"}

	if validation != "" {
		first, _, _ := strings.Cut(validation, "\n")
		body["message"] = first
		body["metadata"] = map[string]string{"validation": validation, "validation-raw": "fake cause"}
	}

	writeJSON(t, w, status, body)
}

// writeJSON writes v as a JSON response with status.
func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()

	body, err := json.Marshal(v)
	if err != nil {
		t.Errorf("encoding fake response: %v", err)

		return
	}

	w.Header().Set("Content-Type", mimeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// serverOptions returns the options a current phenix server reports.
func serverOptions(injectsBase string) map[string]any {
	return map[string]any{optionWorkflowDryRun: true, optionInjectsBase: injectsBase}
}

// requestLine describes a request as "METHOD path", followed by " dryRun"
// for a dry run, " pending=<refs>" when it names pending configs and
// " expect=<action>" when it sets expect.
func requestLine(method, path string, query url.Values) string {
	line := method + " " + path

	if query.Get("dryRun") == boolStringTrue {
		line += " dryRun"
	}

	if pending := query["pending"]; len(pending) > 0 {
		line += " pending=" + strings.Join(pending, ",")
	}

	if expect := query.Get("expect"); expect != "" {
		line += " expect=" + expect
	}

	return line
}

// reqConfigDryRun describes a config dry run for branch.
func reqConfigDryRun(branch string) string {
	return requestLine(http.MethodPost, "/api/v1/workflow/configs/"+branch, url.Values{"dryRun": {boolStringTrue}})
}

// reqConfig describes a config upsert for branch.
func reqConfig(branch string) string {
	return requestLine(http.MethodPost, "/api/v1/workflow/configs/"+branch, nil)
}

// reqDryRun describes a dry run of the workflow config for branch that names
// pending as pending configs.
func reqDryRun(branch string, pending ...string) string {
	return requestLine(http.MethodPost, "/api/v1/workflow/apply/"+branch, url.Values{"dryRun": {boolStringTrue}, "pending": pending})
}

// reqApply describes a real apply for branch that expects action.
func reqApply(branch string, action workflow.Action) string {
	return requestLine(http.MethodPost, "/api/v1/workflow/apply/"+branch, url.Values{"expect": {string(action)}})
}

// fullPreflight returns the requests of the preflight of fullTopology: the
// options, a dry run of each config, topology first, and the dry run of the
// workflow config with both configs pending.
func fullPreflight(branch string) []string {
	return []string{
		reqOptions,
		reqConfigDryRun(branch),
		reqConfigDryRun(branch),
		reqDryRun(branch, "Topology/foo", "Scenario/foo"),
	}
}

// fullRun returns the requests of a run of fullTopology.
func fullRun(branch string, action workflow.Action) []string {
	return append(fullPreflight(branch), reqConfig(branch), reqConfig(branch), reqApply(branch, action))
}

// checkRequests stops the test unless fake received exactly want.
func checkRequests(t *testing.T, fake *fakeServer, want ...string) {
	t.Helper()

	reqs := fake.recorded()
	got := make([]string, 0, len(reqs))

	for _, req := range reqs {
		got = append(got, requestLine(req.method, req.path, req.query))
	}

	if !slices.Equal(got, want) {
		t.Fatalf("requests =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// logCapture collects plog records as JSON lines while a test runs.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// captureLogs records every plog record, at debug level and above, until the
// test ends.
func captureLogs(t *testing.T) *logCapture {
	t.Helper()

	capture := &logCapture{}
	name := "capture-" + t.Name()

	plog.AddHandler(name, slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { plog.RemoveHandler(name) })

	return capture
}

// Write implements [io.Writer] for the JSON handler.
func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.buf.Write(p)
}

// records decodes every record captured so far.
func (c *logCapture) records(t *testing.T) []map[string]any {
	t.Helper()

	c.mu.Lock()
	text := c.buf.String()
	c.mu.Unlock()

	records := []map[string]any{}

	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decoding log record %q: %v", line, err)
		}

		records = append(records, record)
	}

	return records
}

// all returns every captured record whose message is msg.
func (c *logCapture) all(t *testing.T, msg string) []map[string]any {
	t.Helper()

	matched := []map[string]any{}

	for _, record := range c.records(t) {
		if record["msg"] == msg {
			matched = append(matched, record)
		}
	}

	return matched
}

// first returns the first captured record whose message is msg and fails the
// test when there is none.
func (c *logCapture) first(t *testing.T, msg string) map[string]any {
	t.Helper()

	matched := c.all(t, msg)
	if len(matched) == 0 {
		t.Fatalf("no log record %q among %d captured records", msg, len(c.records(t)))
	}

	return matched[0]
}

// messages returns the message of every captured record at level, in order.
func (c *logCapture) messages(t *testing.T, level string) []string {
	t.Helper()

	var msgs []string

	for _, record := range c.records(t) {
		if record["level"] == level {
			msgs = append(msgs, fmt.Sprint(record["msg"]))
		}
	}

	return msgs
}

// checkFields fails the test unless record has every field in want, compared
// as text.
func checkFields(t *testing.T, record map[string]any, want map[string]string) {
	t.Helper()

	for key, value := range want {
		got, ok := record[key]
		if !ok {
			t.Errorf("log record %q has no %q field: %v", record["msg"], key, record)

			continue
		}

		if text := fmt.Sprint(got); text != value {
			t.Errorf("log record %q field %q = %q, want %q", record["msg"], key, text, value)
		}
	}
}

// checkNotLogged fails the test when any record with one of msgs was logged.
func checkNotLogged(t *testing.T, logs *logCapture, msgs ...string) {
	t.Helper()

	for _, msg := range msgs {
		if records := logs.all(t, msg); len(records) != 0 {
			t.Errorf("logged %d %q records, want none", len(records), msg)
		}
	}
}

// actOnRecord is a log handler that calls act when a record whose message is
// msg is logged. plog calls its handlers as it logs, so act runs before the
// run goes on.
type actOnRecord struct {
	msg string
	act func()
}

// actOnLog calls act when the run logs msg, until the test ends.
func actOnLog(t *testing.T, msg string, act func()) {
	t.Helper()

	name := "act-" + t.Name()
	plog.AddHandler(name, actOnRecord{msg: msg, act: act})
	t.Cleanup(func() { plog.RemoveHandler(name) })
}

// Enabled implements [slog.Handler].
func (h actOnRecord) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements [slog.Handler].
func (h actOnRecord) Handle(_ context.Context, r slog.Record) error {
	if r.Message == h.msg {
		h.act()
	}

	return nil
}

// WithAttrs implements [slog.Handler].
func (h actOnRecord) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implements [slog.Handler].
func (h actOnRecord) WithGroup(string) slog.Handler { return h }
