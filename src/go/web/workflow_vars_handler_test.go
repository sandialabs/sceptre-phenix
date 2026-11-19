package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	gocmp "github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// varsTestPart is one part of a multipart test request: its form name, its
// Content-Type (none when empty) and its text.
type varsTestPart struct {
	name, contentType, body string
}

// varsTestBody returns the multipart body that holds parts, and the
// Content-Type that names its boundary.
func varsTestBody(t *testing.T, parts ...varsTestPart) ([]byte, string) {
	t.Helper()

	var (
		buf    bytes.Buffer
		writer = multipart.NewWriter(&buf)
	)

	for _, p := range parts {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="`+p.name+`"`)

		if p.contentType != "" {
			header.Set("Content-Type", p.contentType)
		}

		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatalf("creating the %s part: %v", p.name, err)
		}

		if _, err := io.WriteString(part, p.body); err != nil {
			t.Fatalf("writing the %s part: %v", p.name, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("closing the multipart writer: %v", err)
	}

	return buf.Bytes(), writer.FormDataContentType()
}

// varsTestVariables returns vars as the text of a variables part.
func varsTestVariables(t *testing.T, vars map[string]string) string {
	t.Helper()

	data, err := json.Marshal(vars)
	if err != nil {
		t.Fatalf("encoding the variables: %v", err)
	}

	return string(data)
}

// varsTestRequest returns a multipart body with config, of the Content-Type
// typ, and vars, and the Content-Type of the body.
func varsTestRequest(t *testing.T, typ, config string, vars map[string]string) ([]byte, string) {
	t.Helper()

	return varsTestBody(t,
		varsTestPart{name: configPart, contentType: typ, body: config},
		varsTestPart{name: variablesPart, contentType: mimeJSON, body: varsTestVariables(t, vars)},
	)
}

// varsUpsertRequest builds a configs request for the branch main with query
// appended to the path. Its context carries an allow-all role.
func varsUpsertRequest(t *testing.T, query string, body []byte, typ string) *http.Request {
	t.Helper()

	return validationTestRequest(
		t, http.MethodPost, "/api/v1/workflow/configs/main"+query, typ, body, map[string]string{"branch": "main"},
	)
}

// varsTopology returns a topology config named name, with the given
// includeTopologies line, or none when it is empty.
func varsTopology(name, includes string) string {
	text := "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: " + name + "\nspec:\n"
	if includes != "" {
		text += "  includeTopologies: " + includes + "\n"
	}

	return text + "  nodes: []\n"
}

// decodeVarsBody decodes the JSON body of rec after it checks that its status is 200.
func decodeVarsBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the body %q: %v", rec.Body.String(), err)
	}

	return got
}

// captureWarnings records the warning records logged during the test.
func captureWarnings(t *testing.T) func() []map[string]any {
	t.Helper()

	var (
		mu      sync.Mutex
		records []map[string]any
	)

	name := "warnings-" + t.Name()

	plog.AddHandler(name, slog.NewJSONHandler(writerFunc(func(p []byte) (int, error) {
		var record map[string]any
		if err := json.Unmarshal(p, &record); err == nil && record["level"] == slog.LevelWarn.String() {
			mu.Lock()
			records = append(records, record)
			mu.Unlock()
		}

		return len(p), nil
	}), &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { plog.RemoveHandler(name) })

	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(records)
	}
}

// warningRecord is what a test compares of a warning record of the server log.
type warningRecord struct {
	Msg      string  `json:"msg"`
	Branch   string  `json:"branch"`
	File     string  `json:"file"`
	Line     float64 `json:"line"`
	Variable string  `json:"variable"`
}

// variableWarnings returns the warning records that name a variable.
func variableWarnings(records []map[string]any) []warningRecord {
	var out []warningRecord

	for _, record := range records {
		if _, ok := record["variable"]; !ok {
			continue
		}

		line, _ := record["line"].(float64)
		msg, _ := record["msg"].(string)
		branch, _ := record["branch"].(string)
		file, _ := record["file"].(string)
		variable, _ := record["variable"].(string)

		out = append(out, warningRecord{Msg: msg, Branch: branch, File: file, Line: line, Variable: variable})
	}

	return out
}

// TestWorkflowUpsertConfigVariables sends configs dry runs in the multipart
// format and checks that the request variables are filled in before the name
// is read, and the used names and warnings of the response.
func TestWorkflowUpsertConfigVariables(t *testing.T) {
	keptU := "Topology/main-topo line 6: the request has no variable U and the placeholder has no default; " +
		"phenix kept the text ${U}"

	// PAD makes the variables part exactly maxVariablesPart bytes long.
	pad := strings.Repeat("p", maxVariablesPart-len(`{"PAD":"","SITE":"lab"}`))

	tests := []struct {
		name   string
		typ    string
		config string
		vars   map[string]string
		key    string
		want   map[string]any
	}{
		{
			name:   "a variables part of exactly 1 MiB",
			typ:    mimeYAML,
			config: varsTopology("${SITE}-topo", ""),
			vars:   map[string]string{"SITE": "lab", "PAD": pad},
			key:    "topology/lab-topo",
			want: map[string]any{
				"action": "create", "kind": "Topology", "name": "lab-topo", "dryRun": true,
				"pending": "Topology/lab-topo", "variables": []any{"SITE"},
			},
		},
		{
			name:   "YAML config with a variable in its name",
			typ:    mimeYAML,
			config: varsTopology("${SITE}-topo", ""),
			vars:   map[string]string{"SITE": "lab", "UNUSED": "x"},
			key:    "topology/lab-topo",
			want: map[string]any{
				"action": "create", "kind": "Topology", "name": "lab-topo", "dryRun": true,
				"pending": "Topology/lab-topo", "variables": []any{"SITE"},
			},
		},
		{
			name:   "JSON config",
			typ:    mimeJSON,
			config: `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"${SITE}"},"spec":{"nodes":[]}}`,
			vars:   map[string]string{"SITE": "lab"},
			key:    "topology/lab",
			want: map[string]any{
				"action": "create", "kind": "Topology", "name": "lab", "dryRun": true,
				"pending": "Topology/lab", "variables": []any{"SITE"},
			},
		},
		{
			name:   "default without a value",
			typ:    mimeYAML,
			config: varsTopology("${SITE:dflt}-topo", ""),
			vars:   map[string]string{},
			key:    "topology/dflt-topo",
			want: map[string]any{
				"action": "create", "kind": "Topology", "name": "dflt-topo", "dryRun": true, "pending": "Topology/dflt-topo",
			},
		},
		{
			name:   "request value over the default",
			typ:    mimeYAML,
			config: varsTopology("${SITE:dflt}-topo", ""),
			vars:   map[string]string{"SITE": "lab"},
			key:    "topology/lab-topo",
			want: map[string]any{
				"action": "create", "kind": "Topology", "name": "lab-topo", "dryRun": true,
				"pending": "Topology/lab-topo", "variables": []any{"SITE"},
			},
		},
		{
			name:   "a value that holds the branch name",
			typ:    mimeYAML,
			config: varsTopology("${SITE}", ""),
			vars:   map[string]string{"SITE": "${BRANCH_NAME}-x"},
			key:    "topology/main-x",
			want: map[string]any{
				"action": "create", "kind": "Topology", "name": "main-x", "dryRun": true,
				"pending": "Topology/main-x", "variables": []any{"SITE"},
			},
		},
		{
			name:   "only used names, sorted",
			typ:    mimeYAML,
			config: varsTopology("${B}-${A}", ""),
			vars:   map[string]string{"A": "a", "B": "b", "C": "c"},
			key:    "topology/b-a",
			want: map[string]any{
				"action": "create", "kind": "Topology", "name": "b-a", "dryRun": true,
				"pending": "Topology/b-a", "variables": []any{"A", "B"},
			},
		},
		{
			// The pending ref shows the includes as the server read them.
			name:   "escapes, defaults and an undefined placeholder",
			typ:    mimeYAML,
			config: varsTopology("${BRANCH_NAME}-topo", `["${INC}", "$${ESC}", "${U}", "${D:dflt}", "$${BRANCH_NAME}"]`),
			vars:   map[string]string{"INC": "net"},
			key:    "topology/main-topo",
			want: map[string]any{
				"action": "create", "kind": "Topology", "name": "main-topo", "dryRun": true,
				"pending": "Topology/main-topo?" +
					url.Values{"include": {"net", "${ESC}", "${U}", "dflt", "${BRANCH_NAME}"}}.Encode(),
				"variables": []any{"INC"},
				"warnings":  []any{keptU},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := installValidationTestStore(t)
			expectUpsertExists(m, workflowTestKey(tt.key), false)

			body, typ := varsTestRequest(t, tt.typ, tt.config, tt.vars)
			rec := httptest.NewRecorder()

			if err := WorkflowUpsertConfig(rec, varsUpsertRequest(t, "?dryRun=true", body, typ)); err != nil {
				t.Fatalf("WorkflowUpsertConfig(?dryRun=true) error = %v", err)
			}

			if diff := gocmp.Diff(tt.want, decodeVarsBody(t, rec)); diff != "" {
				t.Errorf("WorkflowUpsertConfig(?dryRun=true) body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWorkflowUpsertConfigVariablesStored checks that a real upsert stores
// the config with its variables filled in, keeps its 201 answer, and logs its
// warning, which names the variable and never a value.
func TestWorkflowUpsertConfigVariablesStored(t *testing.T) {
	m := installValidationTestStore(t)
	key := workflowTestKey("topology/lab-topo")
	expectUpsertExists(m, key, false)

	// The handler clears the spec of the config it stored once it is stored.
	var includes string

	m.EXPECT().Create(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		includes = fmt.Sprint(c.Spec["includeTopologies"])

		return nil
	})

	warnings := captureWarnings(t)

	body, typ := varsTestRequest(t, mimeYAML, varsTopology("${SITE}-topo", `["${INC}-${BRANCH_NAME}", "${U}"]`),
		map[string]string{"SITE": "lab", "INC": "secret-net"})
	rec := httptest.NewRecorder()

	if err := WorkflowUpsertConfig(rec, varsUpsertRequest(t, "", body, typ)); err != nil {
		t.Fatalf("WorkflowUpsertConfig() error = %v", err)
	}

	if rec.Code != http.StatusCreated || rec.Body.Len() != 0 {
		t.Errorf("WorkflowUpsertConfig() = %d %q, want 201 and no body", rec.Code, rec.Body.String())
	}

	if want := "[secret-net-main ${U}]"; includes != want {
		t.Errorf("stored includeTopologies = %s, want %s", includes, want)
	}

	want := []warningRecord{{
		Msg: "Topology/lab-topo line 6: the request has no variable U and the placeholder has no default; " +
			"phenix kept the text ${U}",
		Branch: "main", File: "Topology/lab-topo", Line: 6, Variable: "U",
	}}

	records := warnings()
	if diff := gocmp.Diff(want, variableWarnings(records)); diff != "" {
		t.Errorf("warning records mismatch (-want +got):\n%s", diff)
	}

	if text := fmt.Sprint(records); strings.Contains(text, "secret-net") {
		t.Errorf("a warning record holds a value: %s", text)
	}
}

// varsIncludeFile writes a topology file with a VM for each hostname and
// returns its path. The hostname of VM i is on line 9 + 7*i.
func varsIncludeFile(t *testing.T, hostnames ...string) string {
	t.Helper()

	text := "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: inc\nspec:\n  nodes:\n"
	var textSb366 strings.Builder
	for _, hostname := range hostnames {
		textSb366.WriteString("  - type: VirtualMachine\n    general:\n      hostname: \"" + hostname + "\"\n    hardware:\n" +
			"      os_type: linux\n      drives:\n      - image: x.qc2\n")
	}
	text += textSb366.String()

	path := filepath.Join(t.TempDir(), "include.yml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

// expectIncludingTopology answers every read of the store: the topology topo,
// which includes path, and no other config, and every listing with no
// experiments.
func expectIncludingTopology(m *store.MockStore, path string) {
	topo := workflowTestTopology()
	topo.Spec["nodes"] = []any{map[string]any{
		"type": "VirtualMachine", "general": map[string]any{"hostname": "vm1"},
		"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "x.qc2"}}},
	}}
	topo.Spec["includeTopologies"] = []any{path}

	m.EXPECT().List("Experiment").Return(store.Configs(nil), nil).AnyTimes()
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.Kind == "Topology" && c.Metadata.Name == "topo" {
			*c = topo

			return nil
		}

		return store.ErrNotExist
	}).AnyTimes()
}

// TestApplyWorkflowVariables checks apply requests in the multipart format
// whose workflow config uses variables: the plan, the used names and the
// warnings of a dry run and of a real apply.
func TestApplyWorkflowVariables(t *testing.T) {
	keptU := "the workflow config line 5: the request has no variable U and the placeholder has no default; " +
		"phenix kept the text ${U}"

	tests := []struct {
		name   string
		query  string
		typ    string
		config string
		vars   map[string]string
		setup  func(m *store.MockStore)
		want   map[string]any
	}{
		{
			name:   "dry run of a create",
			query:  "dryRun=true",
			typ:    mimeYAML,
			config: workflowTestYAML("  auto:\n    create: ${EXP}\n    restart: false\n  topology: ${TOPO}\n"),
			vars:   map[string]string{"EXP": "foo", "TOPO": "topo", "UNUSED": "x"},
			setup: func(m *store.MockStore) {
				expectWorkflowList(m)
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
			},
			want: map[string]any{
				"action": "create", "experiment": "foo", "reason": "", "dryRun": true, "variables": []any{"EXP", "TOPO"},
			},
		},
		{
			name:  "JSON workflow config",
			query: "dryRun=true",
			typ:   mimeJSON,
			config: `{"apiVersion":"phenix.sandia.gov/v0","kind":"Workflow","metadata":{},` +
				`"spec":{"auto":{"create":"${EXP}","restart":false},"topology":"topo"}}`,
			vars: map[string]string{"EXP": "foo"},
			setup: func(m *store.MockStore) {
				expectWorkflowList(m)
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
			},
			want: map[string]any{
				"action": "create", "experiment": "foo", "reason": "", "dryRun": true, "variables": []any{"EXP"},
			},
		},
		{
			name:   "real apply that changes nothing",
			typ:    mimeYAML,
			config: workflowTestYAML("  topology: ${U}\n"),
			vars:   map[string]string{},
			setup:  func(m *store.MockStore) { expectWorkflowList(m) },
			want: map[string]any{
				"action": "none", "experiment": "", "reason": "no experiment mapped and auto.create not set",
				"dryRun": false, "warnings": []any{keptU},
			},
		},
		{
			name:   "real apply with a used variable",
			query:  "expect=none",
			typ:    mimeYAML,
			config: workflowTestYAML("  topology: ${TOPO}\n"),
			vars:   map[string]string{"TOPO": "topo"},
			setup:  func(m *store.MockStore) { expectWorkflowList(m) },
			want: map[string]any{
				"action": "none", "experiment": "", "reason": "no experiment mapped and auto.create not set",
				"dryRun": false, "variables": []any{"TOPO"},
			},
		},
		{
			name:   "auto.create of the branch name and a variable",
			query:  "dryRun=true",
			typ:    mimeYAML,
			config: workflowTestYAML("  auto:\n    create: ${BRANCH_NAME}-${EXP}\n    restart: false\n  topology: topo\n"),
			vars:   map[string]string{"EXP": "${BRANCH_NAME}"},
			setup: func(m *store.MockStore) {
				expectWorkflowList(m)
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
			},
			want: map[string]any{
				"action": "create", "experiment": "main-main", "reason": "", "dryRun": true, "variables": []any{"EXP"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := installValidationTestStore(t)
			tt.setup(m)

			body, typ := varsTestRequest(t, tt.typ, tt.config, tt.vars)
			rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), tt.query, typ, bytes.NewReader(body)))

			if diff := gocmp.Diff(tt.want, decodeVarsBody(t, rec)); diff != "" {
				t.Errorf("ApplyWorkflow(%s) body mismatch (-want +got):\n%s", tt.query, diff)
			}
		})
	}
}

// TestApplyWorkflowIncludedFileGetsVariables checks that a topology file that
// the topology includes by a path gets the request value over the server
// environment, the server environment with a warning, and an empty string
// with a warning, in the dry run and in the real create.
func TestApplyWorkflowIncludedFileGetsVariables(t *testing.T) {
	t.Setenv("HOST", "env-host")
	t.Setenv("ENV_HOST", "env-only")

	path := varsIncludeFile(t, "${HOST}", "${ENV_HOST}", "web${NONE}", "${SITE}")

	vars := map[string]string{"HOST": "req-host", "SITE": "${BRANCH_NAME}-site", "EXP": "foo", "UNUSED": "secret"}
	config := workflowTestYAML("  auto:\n    create: ${EXP}\n    restart: false\n  topology: topo\n")

	t.Run("dry run", func(t *testing.T) {
		expectIncludingTopology(installValidationTestStore(t), path)

		warnings := captureWarnings(t)

		body, typ := varsTestRequest(t, mimeYAML, config, vars)
		rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), "dryRun=true", typ, bytes.NewReader(body)))

		want := map[string]any{
			"action": "create", "experiment": "foo", "reason": "", "dryRun": true,
			"variables": []any{"EXP", "HOST", "SITE"},
			"warnings": []any{
				path + " line 16: the request has no variable ENV_HOST; phenix used the value from the server environment",
				path + " line 23: no variable NONE in the request or in the server environment, " +
					"and the placeholder has no default; phenix used an empty string",
			},
		}

		if diff := gocmp.Diff(want, decodeVarsBody(t, rec)); diff != "" {
			t.Errorf("ApplyWorkflow(dryRun=true) body mismatch (-want +got):\n%s", diff)
		}

		wantRecords := []warningRecord{
			{Msg: want["warnings"].([]any)[0].(string), Branch: "main", File: path, Line: 16, Variable: "ENV_HOST"},
			{Msg: want["warnings"].([]any)[1].(string), Branch: "main", File: path, Line: 23, Variable: "NONE"},
		}

		records := warnings()
		if diff := gocmp.Diff(wantRecords, variableWarnings(records)); diff != "" {
			t.Errorf("warning records mismatch (-want +got):\n%s", diff)
		}

		if text := fmt.Sprint(records); strings.Contains(text, "env-only") || strings.Contains(text, "req-host") {
			t.Errorf("a warning record holds a value: %s", text)
		}
	})

	t.Run("real create", func(t *testing.T) {
		m := installValidationTestStore(t)
		expectIncludingTopology(m, path)

		var created *store.Config

		m.EXPECT().Create(gomock.Any()).DoAndReturn(func(c *store.Config) error {
			created = c

			return errors.New("stop here")
		})

		body, typ := varsTestRequest(t, mimeYAML, config, vars)
		rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), "expect=create", typ, bytes.NewReader(body)))

		assertWorkflowError(t, rec, http.StatusInternalServerError, "unable to create new experiment", "stop here")

		if created == nil {
			t.Fatal("ApplyWorkflow(expect=create) created no experiment")
		}

		topology := fmt.Sprint(created.Spec["topology"])
		for _, want := range []string{"hostname:req-host ", "hostname:env-only ", "hostname:web ", "hostname:main-site "} {
			if !strings.Contains(topology, want) {
				t.Errorf("stored experiment topology = %s, want it to hold %q", topology, want)
			}
		}
	})
}

// TestApplyWorkflowIncludedFileLineBreak checks that a request value or a
// default with a line break that an included topology file uses refuses the
// dry run and the real apply before anything changes.
func TestApplyWorkflowIncludedFileLineBreak(t *testing.T) {
	config := workflowTestYAML("  auto:\n    create: foo\n    restart: false\n  topology: topo\n")

	tests := []struct {
		name, query, hostname string
		vars                  map[string]string
	}{
		{name: "value, dry run", query: "dryRun=true", hostname: "${INC_HOST}", vars: map[string]string{"INC_HOST": "a\nb"}},
		{name: "value, real apply", query: "expect=create", hostname: "${INC_HOST}", vars: map[string]string{"INC_HOST": "a\nb"}},
		{name: "default, dry run", query: "dryRun=true", hostname: "${INC_HOST:d\ne}", vars: map[string]string{}},
		{name: "default, real apply", query: "expect=create", hostname: "${INC_HOST:d\ne}", vars: map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectIncludingTopology(installValidationTestStore(t), varsIncludeFile(t, tt.hostname))

			body, typ := varsTestRequest(t, mimeYAML, config, tt.vars)
			rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), tt.query, typ, bytes.NewReader(body)))

			assertWorkflowError(t, rec, http.StatusBadRequest, "unable to prepare phenix workflow config", "variable holds a line break")
		})
	}
}

// TestApplyWorkflowPlainIncludedFile checks that a plain apply fills in an
// included topology file from the server environment with no warning and no
// used names, as before the multipart format.
func TestApplyWorkflowPlainIncludedFile(t *testing.T) {
	t.Setenv("ENV_HOST", "env-only")

	expectIncludingTopology(installValidationTestStore(t), varsIncludeFile(t, "${ENV_HOST}", "web${NONE}"))

	warnings := captureWarnings(t)

	rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), "dryRun=true", mimeYAML,
		strings.NewReader(workflowTestYAML("  auto:\n    create: foo\n    restart: false\n  topology: topo\n"))))

	want := map[string]any{"action": "create", "experiment": "foo", "reason": "", "dryRun": true}
	if diff := gocmp.Diff(want, decodeVarsBody(t, rec)); diff != "" {
		t.Errorf("ApplyWorkflow(dryRun=true) body mismatch (-want +got):\n%s", diff)
	}

	if records := variableWarnings(warnings()); len(records) != 0 {
		t.Errorf("warning records = %v, want none", records)
	}
}

// TestWorkflowVariablesRejectsRequests sends each bad multipart request to
// both workflow routes and expects a 400 before any store call, whose text
// names no value.
func TestWorkflowVariablesRejectsRequests(t *testing.T) {
	const config = "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: ${A}\nspec:\n  nodes: []\n"

	vars := func(text string) varsTestPart {
		return varsTestPart{name: variablesPart, contentType: mimeJSON, body: text}
	}

	cfg := varsTestPart{name: configPart, contentType: mimeYAML, body: config}
	valueLineBreak := "line 4: the value of the request variable A holds a line break"

	tests := []struct {
		name      string
		parts     []varsTestPart
		raw       string // a body that is not built from parts
		message   string // "" for a fill error, whose message names the route's file
		wantCause string
	}{
		{name: "no config part", parts: []varsTestPart{vars(`{}`)}, message: "the request has no config part"},
		{
			name: "two config parts", parts: []varsTestPart{cfg, cfg, vars(`{}`)},
			message: "the request has more than one config part",
		},
		{
			name:    "config part of another type",
			parts:   []varsTestPart{{name: configPart, contentType: "text/plain", body: config}, vars(`{}`)},
			message: `the config part must use application/json or application/x-yaml, not "text/plain"`,
		},
		{
			name:    "config part with no type",
			parts:   []varsTestPart{{name: configPart, body: config}, vars(`{}`)},
			message: `the config part must use application/json or application/x-yaml, not ""`,
		},
		{name: "no variables part", parts: []varsTestPart{cfg}, message: "the request has no variables part"},
		{
			name: "two variables parts", parts: []varsTestPart{cfg, vars(`{}`), vars(`{}`)},
			message: "the request has more than one variables part",
		},
		{
			name: "variables not an object", parts: []varsTestPart{cfg, vars(`["secret"]`)},
			message: "invalid variables part", wantCause: "the variables must be a JSON object of strings",
		},
		{
			name: "a value that is not a string", parts: []varsTestPart{cfg, vars(`{"A":12345}`)},
			message: "invalid variables part", wantCause: `the value of the variable "A" is not a JSON string`,
		},
		{
			name: "an invalid name", parts: []varsTestPart{cfg, vars(`{"1A":"secret"}`)},
			message: "invalid variables part", wantCause: `"1A" is not a valid variable name`,
		},
		{
			name: "a duplicate name", parts: []varsTestPart{cfg, vars(`{"A":"secret","A":"secret2"}`)},
			message: "invalid variables part", wantCause: `the variable "A" is set more than once`,
		},
		{
			name: "BRANCH_NAME", parts: []varsTestPart{cfg, vars(`{"BRANCH_NAME":"secret"}`)},
			message: "invalid variables part", wantCause: "the branch in the path sets the branch name",
		},
		{
			name:    "an extra part",
			parts:   []varsTestPart{cfg, vars(`{}`), {name: "extra", contentType: mimeJSON, body: "{}"}},
			message: `the request has the unknown part "extra"; send only the config and variables parts`,
		},
		{
			name:    "a variables part larger than 1 MiB",
			parts:   []varsTestPart{cfg, vars(`{"A":"` + strings.Repeat("s", maxVariablesPart) + `"}`)},
			message: "the variables part is larger than 1 MiB",
		},
		{name: "no boundary", raw: "secret", message: "unable to read the multipart request"},
		{name: "a body with no part", raw: "secret\r\n", message: "the request has no config part"},
		{
			name: "a default with a line break", parts: []varsTestPart{
				{name: configPart, contentType: mimeYAML, body: config + "x: ${D:se\ncret}\n"}, vars(`{"A":"a"}`),
			},
			wantCause: "line 7: the default of D holds a line break",
		},
		{
			name:      "a used value with \r",
			parts:     []varsTestPart{cfg, vars(`{"A":"se\rcret"}`)},
			wantCause: valueLineBreak,
		},
		{
			name:      "a used value with \n",
			parts:     []varsTestPart{cfg, vars(`{"A":"se\ncret"}`)},
			wantCause: valueLineBreak,
		},
		{
			name:      "a used value with \u0085",
			parts:     []varsTestPart{cfg, vars(`{"A":"se\u0085cret"}`)},
			wantCause: valueLineBreak,
		},
		{
			name:      "a used value with \u2028",
			parts:     []varsTestPart{cfg, vars(`{"A":"se\u2028cret"}`)},
			wantCause: valueLineBreak,
		},
		{
			name:      "a used value with \u2029",
			parts:     []varsTestPart{cfg, vars(`{"A":"se\u2029cret"}`)},
			wantCause: valueLineBreak,
		},
	}

	routes := []struct {
		name string
		file string
		send func(t *testing.T, body []byte, typ string) *httptest.ResponseRecorder
	}{
		{
			name: "configs",
			file: requestConfigFile,
			send: func(t *testing.T, body []byte, typ string) *httptest.ResponseRecorder {
				t.Helper()

				rec := httptest.NewRecorder()
				weberror.ErrorHandler(WorkflowUpsertConfig).ServeHTTP(rec, varsUpsertRequest(t, "?dryRun=true", body, typ))

				return rec
			},
		},
		{
			name: "apply",
			file: workflowConfigFile,
			send: func(t *testing.T, body []byte, typ string) *httptest.ResponseRecorder {
				t.Helper()

				return serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), "dryRun=true", typ, bytes.NewReader(body)))
			},
		},
	}

	for _, route := range routes {
		for _, tt := range tests {
			t.Run(route.name+", "+tt.name, func(t *testing.T) {
				installValidationTestStore(t) // each is refused before any store call

				body, typ := []byte(tt.raw), "multipart/form-data"
				if strings.HasSuffix(tt.raw, "\n") {
					typ += "; boundary=xyz"
				}

				if tt.raw == "" {
					body, typ = varsTestBody(t, tt.parts...)
				}

				message := tt.message
				if message == "" {
					message = "unable to fill in the variables of " + route.file
				}

				cause := tt.wantCause
				if tt.message == "" {
					cause = route.file + " " + cause
				}

				rec := route.send(t, body, typ)
				assertWorkflowError(t, rec, http.StatusBadRequest, message, cause)

				if strings.Contains(rec.Body.String(), "secret") {
					t.Errorf("the error quotes a value: %s", rec.Body.String())
				}
			})
		}
	}
}

// TestWorkflowVariablesRequestsDoNotMix sends requests for different branches
// with different variables at the same moment, and checks that each gets its
// own values, used names and warnings.
func TestWorkflowVariablesRequestsDoNotMix(t *testing.T) {
	path := varsIncludeFile(t, "${HOST}", "web${OTHER_A}${OTHER_B}")

	m := installValidationTestStore(t)
	expectIncludingTopology(m, path)

	const rounds = 20

	configs := func(t *testing.T, branch string) (string, error) {
		t.Helper()

		// MISSING_<branch> has no value, so each warning names the request's own.
		suffix := strings.ToUpper(strings.ReplaceAll(branch, "-", "_"))
		body, typ := varsTestRequest(t, mimeYAML, varsTopology("${SITE}-${BRANCH_NAME}", `["${MISSING_`+suffix+`}"]`),
			map[string]string{"SITE": "site-" + branch})
		req := validationTestRequest(t, http.MethodPost, "/api/v1/workflow/configs/"+branch+"?dryRun=true",
			typ, body, map[string]string{"branch": branch})
		rec := httptest.NewRecorder()

		if err := WorkflowUpsertConfig(rec, req); err != nil {
			return "", err
		}

		var got struct {
			Name     string   `json:"name"`
			Warnings []string `json:"warnings"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			return "", err
		}

		return got.Name + " " + strings.Join(got.Warnings, ";"), nil
	}

	want := func(branch string) string {
		suffix := strings.ToUpper(strings.ReplaceAll(branch, "-", "_"))

		return "site-" + branch + "-" + branch + " Topology/site-" + branch + "-" + branch +
			" line 6: the request has no variable MISSING_" + suffix +
			" and the placeholder has no default; phenix kept the text ${MISSING_" + suffix + "}"
	}

	// apply sends HOST=all for lab-a, a hostname that Prepare refuses, and
	// OTHER_<x> for the other branch only, so a mixed value or warning shows.
	apply := func(t *testing.T, branch string) string {
		t.Helper()

		host, other := "vm-"+branch, "OTHER_A"
		if branch == "lab-a" {
			host, other = "all", "OTHER_B"
		}

		body, typ := varsTestRequest(t, mimeYAML, workflowTestYAML("  auto:\n    create: foo\n    restart: false\n  topology: topo\n"),
			map[string]string{"HOST": host, other: "x"})
		req := applyWorkflowRequest(workflowTestRole(true), "dryRun=true", typ, bytes.NewReader(body))
		req = withPathValues(req, map[string]string{"branch": branch})
		rec := serveApplyWorkflow(t, req)

		return fmt.Sprintf("%d %s", rec.Code, rec.Body.String())
	}

	checkApply := func(branch, got string) string {
		if branch == "lab-a" {
			if !strings.HasPrefix(got, "400 ") || !strings.Contains(got, "hostname 'all' is reserved") {
				return "want a 400 for the hostname all"
			}

			return ""
		}

		wantWarning := "no variable OTHER_B in the request"
		if !strings.HasPrefix(got, "200 ") || !strings.Contains(got, wantWarning) || strings.Contains(got, "OTHER_A in the request") ||
			!strings.Contains(got, `"variables":["HOST","OTHER_A"]`) {
			return "want a 200 with the variables HOST and OTHER_A and a warning for OTHER_B only"
		}

		return ""
	}

	for _, site := range []string{"configs", "apply"} {
		var wg sync.WaitGroup

		for _, branch := range []string{"lab-a", "lab-b", "lab-c"} {
			wg.Add(1)

			go func() {
				defer wg.Done()

				for range rounds {
					var (
						got     string
						err     error
						problem string
					)

					if site == "configs" {
						if got, err = configs(t, branch); err == nil && got != want(branch) {
							problem = "want " + want(branch)
						}
					} else {
						got = apply(t, branch)
						problem = checkApply(branch, got)
					}

					if err != nil || problem != "" {
						t.Errorf("%s(%s) = %s, %v; %s", site, branch, got, err, problem)

						return
					}
				}
			}()
		}

		wg.Wait()
	}
}

// TestWorkflowVariablesAudited checks that the audit records of both routes
// name the used variables, sorted, and never a value.
func TestWorkflowVariablesAudited(t *testing.T) {
	vars := map[string]string{"B": "secret-b", "A": "secret-a", "UNUSED": "secret-u"}

	t.Run("configs", func(t *testing.T) {
		m := installValidationTestStore(t)
		expectUpsertExists(m, workflowTestKey("topology/secret-b-secret-a"), false)

		events := captureAudit(t)

		body, typ := varsTestRequest(t, mimeYAML, varsTopology("${B}-${A}", ""), vars)
		if err := WorkflowUpsertConfig(httptest.NewRecorder(), varsUpsertRequest(t, "?dryRun=true", body, typ)); err != nil {
			t.Fatalf("WorkflowUpsertConfig(?dryRun=true) error = %v", err)
		}

		wantAudit(t, events(), "config-create", "success", map[string]any{
			"phenix.workflow.branch": "main", "phenix.workflow.variables": "A,B",
		})

		// The config name holds the values, and only the name field may.
		for _, event := range events() {
			delete(event, "phenix.config.name")

			if text := fmt.Sprint(event); strings.Contains(text, "secret-u") || strings.Contains(text, "secret-a") {
				t.Errorf("an audit event holds a value: %s", text)
			}
		}
	})

	t.Run("apply", func(t *testing.T) {
		m := installValidationTestStore(t)
		expectWorkflowList(m)
		expectWorkflowReadFails(m, "topology/secret-bsecret-a")

		events := captureAudit(t)

		body, typ := varsTestRequest(t, mimeYAML, workflowTestYAML("  topology: ${B}${A}\n"), vars)
		serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), "dryRun=true", typ, bytes.NewReader(body)))

		wantAudit(t, events(), "workflow-apply", "success", map[string]any{
			"phenix.workflow.branch": "main", "phenix.workflow.variables": "A,B",
		})

		if text := fmt.Sprint(events()); strings.Contains(text, "secret") {
			t.Errorf("an audit event holds a value: %s", text)
		}
	})

	t.Run("real apply", func(t *testing.T) {
		expectWorkflowList(installValidationTestStore(t))

		events := captureAudit(t)

		body, typ := varsTestRequest(t, mimeYAML, workflowTestYAML("  topology: ${B}${A}\n"), vars)
		serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), "expect=none", typ, bytes.NewReader(body)))

		wantAudit(t, events(), "workflow-apply", "success", map[string]any{
			"phenix.workflow.branch": "main", "phenix.workflow.variables": "A,B",
		})
	})

	t.Run("plain request", func(t *testing.T) {
		m := installValidationTestStore(t)
		expectWorkflowList(m)

		events := captureAudit(t)

		serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), "dryRun=true", mimeYAML,
			strings.NewReader(workflowTestYAML("  topology: ${B}\n"))))

		wantAudit(t, events(), "workflow-apply", "success", map[string]any{"phenix.workflow.variables": nil})
	})
}

// TestWorkflowVariablesContract checks that openapi.yml documents the
// multipart request of both workflow routes, and that their answers with the
// new fields, through the router, match the documented bodies.
func TestWorkflowVariablesContract(t *testing.T) {
	spec := contractSpec(t)

	for _, path := range []string{"/api/v1/workflow/configs/main", "/api/v1/workflow/apply/main"} {
		_, op := specOperation(spec, http.MethodPost, path)
		if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
			t.Errorf("openapi.yml has no request body for POST %s", path)

			continue
		}

		media := op.RequestBody.Value.Content.Get(mimeMultipart)
		if media == nil || media.Schema == nil || media.Schema.Value == nil {
			t.Errorf("openapi.yml POST %s has no %s request body", path, mimeMultipart)

			continue
		}

		var got []string
		for name := range media.Schema.Value.Properties {
			got = append(got, name)
		}

		slices.Sort(got)

		if diff := gocmp.Diff([]string{configPart, variablesPart}, got); diff != "" {
			t.Errorf("openapi.yml POST %s %s parts mismatch (-want +got):\n%s", path, mimeMultipart, diff)
		}
	}

	useContractFixture(t)
	server := serveContractAs(t, adminRole(), nil).URL

	for _, tc := range []struct {
		path, config string
	}{
		{path: "/api/v1/workflow/configs/main?dryRun=true", config: varsTopology("${SITE}-vars", `["${U}"]`)},
		{path: "/api/v1/workflow/apply/main?dryRun=true", config: workflowTestYAML("  topology: ${SITE}${U}\n")},
		{path: "/api/v1/workflow/apply/main", config: workflowTestYAML("  topology: ${SITE}${U}\n")},
	} {
		t.Run(tc.path, func(t *testing.T) {
			body, typ := varsTestRequest(t, mimeYAML, tc.config, map[string]string{"SITE": "lab"})

			resp := do(t, http.MethodPost, server+tc.path, string(body), "Content-Type", typ)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST %s = %d, want 200: %s", tc.path, resp.StatusCode, readBody(t, resp))
			}

			wantDocumentedBody(t, http.MethodPost, tc.path, resp)
		})
	}
}
