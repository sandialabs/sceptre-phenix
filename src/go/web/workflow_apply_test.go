package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/golang/mock/gomock"
	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/workflow"
	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// workflowErrorBody is the JSON body of an error response.
type workflowErrorBody struct {
	Cause    string            `json:"cause"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata"`
}

// applyWorkflowErrorCase is an apply request that must fail. contentType is
// sent as is, so "" sends none. setup declares the only store calls the
// request may make; nil means none.
type applyWorkflowErrorCase struct {
	name        string
	deny        bool
	query       string
	contentType string
	body        io.Reader
	setup       func(m *store.MockStore)
	wantStatus  int
	wantMessage string
	wantCause   []string
}

// applyWorkflowResultCase is an apply request that must succeed with want. An
// empty contentType sends YAML. setup declares the only store calls the
// request may make; nil means none.
type applyWorkflowResultCase struct {
	name        string
	query       string
	contentType string
	body        string
	setup       func(m *store.MockStore)
	want        workflow.Plan
}

// workflowTestRole returns the global-admin role that the unix-socket router
// installs or, when allow is false, a role without policies.
func workflowTestRole(allow bool) rbac.Role {
	if !allow {
		// Role.Allowed dereferences Spec, so a role without policies still needs
		// a non-nil Spec.
		return rbac.Role{Spec: &v1.RoleSpec{}}
	}

	return rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{
					Resources:     []string{"*", "*/*"},
					ResourceNames: []string{"*", "*/*"},
					Verbs:         []string{"*"},
				},
			},
		},
	}
}

// workflowTestYAML returns a YAML workflow config with the given spec body,
// whose lines must be indented by two spaces.
func workflowTestYAML(spec string) string {
	return "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata: {}\nspec:\n" + spec
}

// workflowTestExperiment returns a stored experiment config named name. A
// non-empty branch maps it to that workflow branch, and running gives it a
// start time.
func workflowTestExperiment(name, branch string, running bool) store.Config {
	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
	}

	if branch != "" {
		c.Metadata.Annotations = store.Annotations{workflow.BranchAnnotation: branch}
	}

	if running {
		c.Status = map[string]any{"startTime": "2024-01-01T00:00:00Z"}
	}

	return c
}

// workflowTestTopology returns the stored topology topo: the linux VM vm1 on
// VLANs EXP_1 and EXP_2, and the external node ext1.
func workflowTestTopology() store.Config {
	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Topology",
		Metadata: store.ConfigMetadata{Name: "topo"},
		Spec: map[string]any{
			"nodes": []any{
				map[string]any{
					"type":     "VirtualMachine",
					"general":  map[string]any{"hostname": "vm1"},
					"hardware": map[string]any{"os_type": "linux"},
					"network": map[string]any{
						"interfaces": []any{
							map[string]any{"name": "eth0", "vlan": "EXP_1"},
							map[string]any{"name": "eth1", "vlan": "EXP_2"},
						},
					},
				},
				map[string]any{
					"type":     "VirtualMachine",
					"external": true,
					"general":  map[string]any{"hostname": "ext1"},
				},
			},
		},
	}
}

// workflowTestBadTopology returns the stored topology topo with its VM named
// all, the hostname minimega reserves, which no experiment can use.
func workflowTestBadTopology() store.Config {
	cfg := workflowTestTopology()
	nodes, _ := cfg.Spec["nodes"].([]any)
	vm, _ := nodes[0].(map[string]any)
	vm["general"] = map[string]any{"hostname": "all"}

	return cfg
}

// workflowTestScenario returns the stored scenario scn, annotated for the
// topology topo, with the given apps.
func workflowTestScenario(apps ...map[string]any) store.Config {
	return store.Config{
		Version: "phenix.sandia.gov/v2",
		Kind:    "Scenario",
		Metadata: store.ConfigMetadata{
			Name:        "scn",
			Annotations: store.Annotations{"topology": "topo"},
		},
		Spec: map[string]any{"apps": apps},
	}
}

// workflowTestKey returns the config that [store.NewConfig] builds for name,
// such as "topology/topo", which is what the handler passes to the store.
func workflowTestKey(name string) *store.Config {
	key, _ := store.NewConfig(name)

	return key
}

// useBridgeMode sets the server's bridge mode to mode until the test ends.
func useBridgeMode(t *testing.T, mode common.BridgingMode) {
	t.Helper()

	original := common.BridgeMode
	t.Cleanup(func() { common.BridgeMode = original }) //nolint:reassign // restore the bridge mode

	common.BridgeMode = mode //nolint:reassign // exercise the given bridge mode
}

// expectWorkflowList expects exactly one listing of experiments, answered
// with configs.
func expectWorkflowList(m *store.MockStore, configs ...store.Config) {
	m.EXPECT().List("Experiment").Return(store.Configs(configs), nil)
}

// expectWorkflowRead expects exactly one read of the config named name and
// answers it with cfg.
func expectWorkflowRead(m *store.MockStore, name string, cfg store.Config) {
	m.EXPECT().Get(gomock.Eq(workflowTestKey(name))).SetArg(0, cfg).Return(nil)
}

// expectWorkflowReadFails expects exactly one read of the config named name
// and fails it with [store.ErrNotExist].
func expectWorkflowReadFails(m *store.MockStore, name string) {
	m.EXPECT().Get(gomock.Eq(workflowTestKey(name))).Return(store.ErrNotExist)
}

// applyWorkflowRequest builds an apply request for the branch "main" with the
// given raw query, Content-Type (none when empty) and body. Like the auth
// middleware, it puts role and the user "test-user" in the request context.
func applyWorkflowRequest(role rbac.Role, rawQuery, contentType string, body io.Reader) *http.Request {
	target := "/api/v1/workflow/apply/main"
	if rawQuery != "" {
		target += "?" + rawQuery
	}

	req := httptest.NewRequest(http.MethodPost, target, body)
	req = mux.SetURLVars(req, map[string]string{"branch": "main"})

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	return req.WithContext(ctx)
}

// serveApplyWorkflow serves req with ApplyWorkflow behind weberror's error
// handler, as the router does, and returns the recorded response.
// ApplyWorkflow sets BRANCH_NAME in the process environment, so the variable
// is restored when the test ends.
func serveApplyWorkflow(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("BRANCH_NAME", "")

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(ApplyWorkflow).ServeHTTP(rec, req)

	return rec
}

// decodeWorkflowError checks that rec is an error response with the given
// status and returns its body.
func decodeWorkflowError(t *testing.T, rec *httptest.ResponseRecorder, status int) workflowErrorBody {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, rec.Code, rec.Body.String())
	}

	var body workflowErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body %q: %v", rec.Body.String(), err)
	}

	return body
}

// assertWorkflowError checks that rec is an error response with the given
// status and message, and that its cause contains each of causes.
func assertWorkflowError(t *testing.T, rec *httptest.ResponseRecorder, status int, message string, causes ...string) {
	t.Helper()

	body := decodeWorkflowError(t, rec, status)

	if body.Message != message {
		t.Errorf("expected message %q, got %q", message, body.Message)
	}

	for _, want := range causes {
		if !strings.Contains(body.Cause, want) {
			t.Errorf("expected cause %q to contain %q", body.Cause, want)
		}
	}
}

// assertWorkflowValidationError checks that rec is a 400 schema validation
// error: its raw validator text contains wantRaw, a pointer or key phenix
// owns, its message is the first explained line, and the explained lines
// give a line number exactly when wantLine is true.
func assertWorkflowValidationError(t *testing.T, rec *httptest.ResponseRecorder, wantRaw string, wantLine bool) {
	t.Helper()

	var (
		body      = decodeWorkflowError(t, rec, http.StatusBadRequest)
		explained = body.Metadata["validation"]
		raw       = body.Metadata["validation-raw"]
	)

	if !strings.Contains(raw, wantRaw) {
		t.Errorf("metadata.validation-raw = %q, want it to contain %q", raw, wantRaw)
	}

	first, _, _ := strings.Cut(explained, "\n")
	if first == "" || body.Message != first {
		t.Errorf("message = %q, want the first explained line of %q", body.Message, explained)
	}

	if got := strings.Contains(explained, "(line "); got != wantLine {
		t.Errorf("metadata.validation = %q: line number shown = %t, want %t", explained, got, wantLine)
	}
}

// assertWorkflowResult checks that rec is a 200 JSON response whose body is
// exactly want with the given dry-run flag.
func assertWorkflowResult(t *testing.T, rec *httptest.ResponseRecorder, want workflow.Plan, dryRun bool) {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != mimeJSON {
		t.Fatalf("expected Content-Type %q, got %q", mimeJSON, got)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}

	wantBody := map[string]any{
		"action":     string(want.Action),
		"experiment": want.Experiment,
		"reason":     want.Reason,
		"dryRun":     dryRun,
	}

	if !reflect.DeepEqual(got, wantBody) {
		t.Fatalf("expected body %v, got %v", wantBody, got)
	}
}

// runApplyWorkflowErrors serves each case with a strict store mock and checks
// its error response.
func runApplyWorkflowErrors(t *testing.T, cases []applyWorkflowErrorCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := installValidationTestStore(t)
			if tc.setup != nil {
				tc.setup(m)
			}

			req := applyWorkflowRequest(workflowTestRole(!tc.deny), tc.query, tc.contentType, tc.body)
			assertWorkflowError(t, serveApplyWorkflow(t, req), tc.wantStatus, tc.wantMessage, tc.wantCause...)
		})
	}
}

// runApplyWorkflowResults serves each case with a strict store mock and
// checks that it responds with its plan and the given dry-run flag.
func runApplyWorkflowResults(t *testing.T, dryRun bool, cases []applyWorkflowResultCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := installValidationTestStore(t)
			if tc.setup != nil {
				tc.setup(m)
			}

			contentType := cmp.Or(tc.contentType, mimeYAML)
			req := applyWorkflowRequest(workflowTestRole(true), tc.query, contentType, strings.NewReader(tc.body))
			assertWorkflowResult(t, serveApplyWorkflow(t, req), tc.want, dryRun)
		})
	}
}

// TestWorkflowWebError checks the status each workflow API error maps to.
func TestWorkflowWebError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "conflict", err: fmt.Errorf("%w: two experiments", workflow.ErrConflict), want: http.StatusConflict},
		{name: "plan changed", err: fmt.Errorf("%w: expected update", workflow.ErrPlanChanged), want: http.StatusConflict},
		{name: "invalid config", err: fmt.Errorf("%w: bad action", workflow.ErrInvalidSpec), want: http.StatusBadRequest},
		{name: "unresolved reference", err: fmt.Errorf("%w: topology", workflow.ErrUnresolved), want: http.StatusBadRequest},
		{name: "anything else", err: errors.New("disk full"), want: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := workflowWebError(tc.err, "summary")

			if got.Status != tc.want || got.Message != "summary" || got.Cause != tc.err.Error() {
				t.Errorf(
					"workflowWebError() = {%d %q %q}, want {%d %q %q}",
					got.Status, got.Message, got.Cause, tc.want, "summary", tc.err.Error(),
				)
			}
		})
	}
}

// TestApplyWorkflowRejectsRequest checks requests that are refused before
// experiments are listed: the store mock expects no calls.
func TestApplyWorkflowRejectsRequest(t *testing.T) {
	var (
		createFoo = workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n")
		parseMsg  = "unable to parse phenix workflow config"
		typeMsg   = "must use application/json or application/x-yaml when providing phenix workflow config"
	)

	runApplyWorkflowErrors(t, []applyWorkflowErrorCase{
		{
			name:        "role without workflow create",
			deny:        true,
			contentType: mimeYAML,
			body:        strings.NewReader(createFoo),
			wantStatus:  http.StatusForbidden,
			wantMessage: "applying phenix workflow is not allowed for user test-user",
		},
		{
			name:        "no Content-Type",
			body:        strings.NewReader(createFoo),
			wantStatus:  http.StatusBadRequest,
			wantMessage: typeMsg,
		},
		{
			name:        "Content-Type with a charset",
			contentType: "application/json; charset=utf-8",
			body:        strings.NewReader(`{"spec": {}}`),
			wantStatus:  http.StatusBadRequest,
			wantMessage: typeMsg,
		},
		{
			name:        "malformed YAML",
			contentType: mimeYAML,
			body:        strings.NewReader("spec: ["),
			wantStatus:  http.StatusBadRequest,
			wantMessage: parseMsg,
		},
		{
			name:        "malformed JSON",
			contentType: mimeJSON,
			body:        strings.NewReader("{"),
			wantStatus:  http.StatusBadRequest,
			wantMessage: parseMsg,
		},
		{
			name:        "unreadable YAML body",
			contentType: mimeYAML,
			body:        iotest.ErrReader(errors.New("read failed")),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "unable to parse request",
			wantCause:   []string{"read failed"},
		},
		{
			name:        "unreadable JSON body",
			contentType: mimeJSON,
			body:        iotest.ErrReader(errors.New("read failed")),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "unable to read request data",
			wantCause:   []string{"read failed"},
		},
		{
			// An unquoted date is a YAML timestamp. After the JSON round trip it
			// passes the schema as a string, but it does not decode into the
			// string field.
			name:        "topology is a YAML timestamp",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: 2024-01-01\n")),
			wantStatus:  http.StatusBadRequest,
			wantMessage: parseMsg,
			wantCause:   []string{"topology", "time.Time"},
		},
	})
}

// TestApplyWorkflowSchemaErrors checks that a workflow config that fails the
// Workflow schema gets a 400 with explained validation lines before anything
// is listed, stopped or written: the store mock expects no calls. A non-empty
// wantExplained is a pattern for the whole of metadata.validation.
func TestApplyWorkflowSchemaErrors(t *testing.T) {
	tests := []struct {
		name          string
		contentType   string
		body          string
		wantRaw       string
		wantLine      bool
		wantExplained string
	}{
		{
			name:        "misspelled key",
			contentType: mimeYAML,
			body:        workflowTestYAML("  auto:\n    restrat: false\n"),
			wantRaw:     `"restrat"`,
			wantLine:    true,
		},
		{
			name:        "wrong type",
			contentType: mimeYAML,
			body:        workflowTestYAML("  auto:\n    update: \"yes\"\n"),
			wantRaw:     `"/spec/auto/update"`,
			wantLine:    true,
		},
		{
			name:        "defaultBridge over the schema's limit",
			contentType: mimeYAML,
			body:        workflowTestYAML("  defaultBridge: " + strings.Repeat("b", experiment.MaxBridgeNameLength+1) + "\n"),
			wantRaw:     `"/spec/defaultBridge"`,
			wantLine:    true,
		},
		{
			name:        "wrong kind",
			contentType: mimeYAML,
			body:        "apiVersion: phenix.sandia.gov/v0\nkind: Topology\nmetadata: {}\nspec:\n  topology: topo\n",
			wantRaw:     "Workflow",
			wantLine:    true,
		},
		{
			name:        "wrong apiVersion",
			contentType: mimeYAML,
			body:        "apiVersion: phenix.sandia.gov/v1\nkind: Workflow\nmetadata: {}\nspec:\n  topology: topo\n",
			wantRaw:     "phenix.sandia.gov/v0",
			wantLine:    true,
		},
		{
			// The spec: keys lost one level of indentation. The parsed document
			// keeps them, so the schema names the first stray key and its line.
			// auto sorts before spec, so it is reported, not the null spec.
			name:          "spec keys at the top level",
			contentType:   mimeYAML,
			body:          "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata: {}\nspec:\nauto:\n  update: true\ntopology: topo\n",
			wantRaw:       `"auto"`,
			wantLine:      true,
			wantExplained: `auto (line 5): *"auto"*`,
		},
		{
			// spec: with nothing under it is null, which the schema refuses:
			// spec: {} applies every default.
			name:          "spec null",
			contentType:   mimeYAML,
			body:          workflowTestYAML(""),
			wantRaw:       `"/spec"`,
			wantLine:      true,
			wantExplained: "spec (line 4): *",
		},
		{
			// The same indentation mistake with stray keys that all sort after
			// spec. kin-openapi checks keys in sorted order and stops at the
			// first error, so the null spec is reported alone, with its line.
			name:          "spec null with stray keys after it",
			contentType:   mimeYAML,
			body:          "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata: {}\nspec:\ntopology: helloworld\nvlans:\n  EXP: 101\n",
			wantRaw:       `"/spec"`,
			wantLine:      true,
			wantExplained: "spec (line 4): *",
		},
		{
			name:        "single-line JSON has no line numbers",
			contentType: mimeJSON,
			body:        `{"apiVersion":"phenix.sandia.gov/v0","kind":"Workflow","metadata":{},"spec":{"topologyy":"topo"}}`,
			wantRaw:     `"topologyy"`,
			wantLine:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			installValidationTestStore(t)

			req := applyWorkflowRequest(workflowTestRole(true), "", tc.contentType, strings.NewReader(tc.body))
			rec := serveApplyWorkflow(t, req)
			assertWorkflowValidationError(t, rec, tc.wantRaw, tc.wantLine)

			if tc.wantExplained == "" {
				return
			}

			if got := decodeWorkflowError(t, rec, http.StatusBadRequest).Metadata["validation"]; !matchLine(got, tc.wantExplained) {
				t.Errorf("metadata.validation = %q, want %q", got, tc.wantExplained)
			}
		})
	}
}

// TestApplyWorkflowPlanErrors checks a failed listing, the two conflicts the
// plan refuses with 409 and an auto.create it refuses with 400. Only the
// listing is allowed, so nothing is created, stopped or written.
func TestApplyWorkflowPlanErrors(t *testing.T) {
	var (
		createFoo = workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n")
		applyMsg  = "unable to apply phenix workflow config"
	)

	runApplyWorkflowErrors(t, []applyWorkflowErrorCase{
		{
			name:        "listing experiments fails",
			contentType: mimeYAML,
			body:        strings.NewReader(createFoo),
			setup: func(m *store.MockStore) {
				m.EXPECT().List("Experiment").Return(nil, errors.New("store unavailable"))
			},
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "unable to get list of experiments",
			wantCause:   []string{"store unavailable"},
		},
		{
			name:        "two experiments mapped to the branch",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: topo\n")),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, workflowTestExperiment("foo", "main", true), workflowTestExperiment("bar", "main", false))
			},
			wantStatus:  http.StatusConflict,
			wantMessage: applyMsg,
			wantCause:   []string{"workflow conflict", "foo", "bar"},
		},
		{
			name:        "auto.create names an unmapped experiment",
			contentType: mimeYAML,
			body:        strings.NewReader(createFoo),
			setup:       func(m *store.MockStore) { expectWorkflowList(m, workflowTestExperiment("foo", "", false)) },
			wantStatus:  http.StatusConflict,
			wantMessage: applyMsg,
			wantCause:   []string{"workflow conflict", "foo"},
		},
		{
			name:        "auto.create names an experiment mapped to another branch",
			contentType: mimeYAML,
			body:        strings.NewReader(createFoo),
			setup:       func(m *store.MockStore) { expectWorkflowList(m, workflowTestExperiment("foo", "other", true)) },
			wantStatus:  http.StatusConflict,
			wantMessage: applyMsg,
			wantCause:   []string{"workflow conflict", "foo"},
		},
		{
			name:        "dry run whose auto.create is not a valid experiment name",
			query:       "dryRun=true",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  auto:\n    create: bad name!\n  topology: topo\n")),
			setup:       func(m *store.MockStore) { expectWorkflowList(m) },
			wantStatus:  http.StatusBadRequest,
			wantMessage: applyMsg,
			wantCause:   []string{`invalid workflow config: auto.create "bad name!" is not a valid experiment name`},
		},
	})
}

// TestApplyWorkflowBridgeChecks checks that a default bridge the plan cannot
// give its experiment is refused with 400, in a dry run and in a real apply,
// before the running experiment mapped to the branch is touched. The store
// mock allows only the listing, so a topology read, a stop or any write fails
// the test. Without this check the experiment config hooks would reject the
// bridge only after the experiment was stopped and its new spec written.
func TestApplyWorkflowBridgeChecks(t *testing.T) {
	var (
		fooRunning = workflowTestExperiment("foo", "main", true)
		longName   = workflowTestExperiment(strings.Repeat("x", experiment.MaxBridgeNameLength+1), "main", true)
		barOnBr0   = workflowTestExperiment("bar", "", true)
		inUse      = `experiment "bar" already uses default bridge "br0"`
		restartBr0 = workflowTestYAML("  topology: topo\n  defaultBridge: br0\n")
	)

	barOnBr0.Spec = map[string]any{"defaultBridge": "br0"}

	tests := []struct {
		name      string
		mode      common.BridgingMode
		query     string
		body      string
		configs   []store.Config
		wantCause string
	}{
		{
			name:      "restart onto a bridge another experiment uses",
			mode:      common.BridgeModeManual,
			body:      restartBr0,
			configs:   []store.Config{fooRunning, barOnBr0},
			wantCause: inUse,
		},
		{
			name:      "dry run of a restart onto a bridge another experiment uses",
			mode:      common.BridgeModeManual,
			query:     "dryRun=true",
			body:      restartBr0,
			configs:   []store.Config{fooRunning, barOnBr0},
			wantCause: inUse,
		},
		{
			name:      "dry run of a create onto a bridge another experiment uses",
			mode:      common.BridgeModeManual,
			query:     "dryRun=true",
			body:      workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n  defaultBridge: br0\n"),
			configs:   []store.Config{barOnBr0},
			wantCause: inUse,
		},
		{
			name:    "auto bridge mode and a running experiment with a long name",
			mode:    common.BridgeModeAuto,
			body:    workflowTestYAML("  topology: topo\n"),
			configs: []store.Config{longName},
			wantCause: fmt.Sprintf(
				"experiment name %q must be %d characters or less when the bridge mode is auto",
				longName.Metadata.Name, experiment.MaxBridgeNameLength,
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useBridgeMode(t, tc.mode)
			expectWorkflowList(installValidationTestStore(t), tc.configs...)

			req := applyWorkflowRequest(workflowTestRole(true), tc.query, mimeYAML, strings.NewReader(tc.body))
			assertWorkflowError(
				t, serveApplyWorkflow(t, req), http.StatusBadRequest, "unable to prepare phenix workflow config", tc.wantCause,
			)
		})
	}
}

// TestApplyWorkflowPrepareFailures checks that a workflow config naming a
// topology or scenario that cannot be resolved, or a default bridge that is
// too long, is rejected with 400 before the running experiment mapped to the
// branch is touched. The store mock allows only the listing and the reads
// that preparing makes, so a stop (whose first store call reads
// experiment/foo) or any write fails the test. The restart row of
// TestApplyWorkflowExecutesPlan shows the same experiment is stopped when the
// config resolves.
func TestApplyWorkflowPrepareFailures(t *testing.T) {
	var (
		running    = workflowTestExperiment("foo", "main", true)
		prepareMsg = "unable to prepare phenix workflow config"
		fromBase   = workflowTestScenario(map[string]any{"name": "app1", "fromScenario": "base"})
	)

	undecodable := workflowTestTopology()
	undecodable.Version = "phenix.sandia.gov/v7"

	runApplyWorkflowErrors(t, []applyWorkflowErrorCase{
		{
			name:        "topology does not exist",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: gone\n")),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, running)
				expectWorkflowReadFails(m, "topology/gone")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`topology "gone"`, "config does not exist"},
		},
		{
			name:        "no topology in the config or the experiment",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  vlans:\n    EXP_1: 101\n")),
			setup:       func(m *store.MockStore) { expectWorkflowList(m, running) },
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{"no topology"},
		},
		{
			name:        "topology is not a config name",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: a/b\n")),
			setup:       func(m *store.MockStore) { expectWorkflowList(m, running) },
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`topology "a/b"`},
		},
		{
			name:        "topology does not decode",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: topo\n")),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, running)
				expectWorkflowRead(m, "topology/topo", undecodable)
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{"unknown version v7"},
		},
		{
			name:        "scenario does not exist",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: topo\n  scenario: gone\n")),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, running)
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
				expectWorkflowReadFails(m, "scenario/gone")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`scenario "gone"`},
		},
		{
			name:        "scenario does not merge",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: topo\n  scenario: scn\n")),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, running)
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
				expectWorkflowRead(m, "scenario/scn", fromBase)
				expectWorkflowReadFails(m, "scenario/base")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{"scenario base doesn't exist"},
		},
		{
			// Two-byte characters pass the schema's maxLength, which counts
			// characters, but not the bridge name limit, which counts bytes and
			// which Validate applies before experiments are even listed.
			name:        "defaultBridge over the limit in bytes only",
			contentType: mimeYAML,
			body: strings.NewReader(workflowTestYAML(
				"  defaultBridge: " + strings.Repeat("\u00e9", experiment.MaxBridgeNameLength/2+2) + "\n",
			)),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "invalid phenix workflow config",
			wantCause:   []string{"invalid workflow config"},
		},
	})
}

// TestApplyWorkflowRefusesBadTopology checks that a stored topology whose
// nodes an experiment could not use is refused with 400, on a dry run and on
// a real apply, before anything is created, stopped or written. The store
// mock allows only the listing and one read of the topology: a create would
// read the topology again inside experiment.Create, an update would read the
// experiment to write it, and a restart would first read it to stop it.
func TestApplyWorkflowRefusesBadTopology(t *testing.T) {
	var (
		createFoo  = workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n")
		topoOnly   = workflowTestYAML("  topology: topo\n")
		updateFoo  = workflowTestYAML("  auto:\n    restart: false\n  topology: topo\n")
		prepareMsg = "unable to prepare phenix workflow config"
		wantCause  = []string{`topology "topo" cannot be used in an experiment`, "hostname 'all' is reserved"}
	)

	listAndReadBad := func(configs ...store.Config) func(m *store.MockStore) {
		return func(m *store.MockStore) {
			expectWorkflowList(m, configs...)
			expectWorkflowRead(m, "topology/topo", workflowTestBadTopology())
		}
	}

	runApplyWorkflowErrors(t, []applyWorkflowErrorCase{
		{
			name:        "dry run of a create",
			query:       "dryRun=true",
			contentType: mimeYAML,
			body:        strings.NewReader(createFoo),
			setup:       listAndReadBad(),
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   wantCause,
		},
		{
			name:        "dry run of a restart",
			query:       "dryRun=true",
			contentType: mimeYAML,
			body:        strings.NewReader(topoOnly),
			setup:       listAndReadBad(workflowTestExperiment("foo", "main", true)),
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   wantCause,
		},
		{
			name:        "real apply of a create",
			contentType: mimeYAML,
			body:        strings.NewReader(createFoo),
			setup:       listAndReadBad(),
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   wantCause,
		},
		{
			name:        "real apply of an update to a stopped experiment",
			contentType: mimeYAML,
			body:        strings.NewReader(updateFoo),
			setup:       listAndReadBad(workflowTestExperiment("foo", "main", false)),
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   wantCause,
		},
		{
			name:        "real apply of a restart of a running experiment",
			contentType: mimeYAML,
			body:        strings.NewReader(topoOnly),
			setup:       listAndReadBad(workflowTestExperiment("foo", "main", true)),
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   wantCause,
		},
	})
}

// TestApplyWorkflowPendingConfigs checks the pending query parameter with bare
// Kind/name refs. A dry run names the configs its client upserts later as
// pending, so a topology directory whose configs are not stored yet passes
// without looking them up. A real apply, which runs after the client's upsert, takes
// no pending refs and still needs every config stored. For a create it also
// checks the scenario's topology annotation, which a dry run cannot check from
// a bare Kind/name ref, and refuses before experiment.Create: the mock allows
// no second topology read, which experiment.Create would make.
func TestApplyWorkflowPendingConfigs(t *testing.T) {
	var (
		createBoth      = workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n  scenario: scn\n")
		createExpanded  = workflowTestYAML("  auto:\n    create: foo\n  topology: ${BRANCH_NAME}-topo\n")
		createBranchScn = workflowTestYAML("  auto:\n    create: foo\n  topology: ${BRANCH_NAME}-topo\n  scenario: scn\n")
		prepareMsg      = "unable to prepare phenix workflow config"
		pendingMsg      = "pending is only allowed with dryRun=true"
	)

	// A directory first deployed under the branch main names its topology
	// after the branch, main-topo, while its scenario's topology annotation
	// still names topo, as workflowTestScenario's does, or is missing.
	unannotated := workflowTestScenario(map[string]any{"name": "app1"})
	unannotated.Metadata.Annotations = nil

	// readBranchConfigs allows the listing and one read each of main-topo
	// and of the scenario scn, answered with scn.
	readBranchConfigs := func(scn store.Config) func(m *store.MockStore) {
		return func(m *store.MockStore) {
			expectWorkflowList(m)
			expectWorkflowRead(m, "topology/main-topo", workflowTestTopology())
			expectWorkflowRead(m, "scenario/scn", scn)
		}
	}

	runApplyWorkflowResults(t, true, []applyWorkflowResultCase{
		{
			// A first deploy under a new branch name: the topology is named
			// after the branch, and the scenario is pending too. A bare ref
			// describes nothing about the scenario, so its topology annotation
			// is not checked; the real apply below checks it.
			name:  "first deploy under a new branch",
			query: "dryRun=true&pending=Topology/main-topo&pending=Scenario/scn",
			body:  createBranchScn,
			setup: func(m *store.MockStore) { expectWorkflowList(m) },
			want:  workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
		},
		{
			// A topology directory deployed for the first time: only the
			// listing is allowed, so nothing is read.
			name:  "fresh directory",
			query: "dryRun=true&pending=Topology/topo&pending=Scenario/scn",
			body:  createBoth,
			setup: func(m *store.MockStore) { expectWorkflowList(m) },
			want:  workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
		},
		{
			// The config dry run answers with the name the server expanded,
			// and the apply dry run compares it as is.
			name:  "ref with the name the server expanded",
			query: "dryRun=true&pending=Topology/main-topo",
			body:  createExpanded,
			setup: func(m *store.MockStore) { expectWorkflowList(m) },
			want:  workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
		},
		{
			name:  "running experiment moving to a topology not stored yet",
			query: "dryRun=true&pending=Topology/new-topo",
			body:  workflowTestYAML("  topology: new-topo\n"),
			setup: func(m *store.MockStore) { expectWorkflowList(m, workflowTestExperiment("foo", "main", true)) },
			want:  workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo", Reason: ""},
		},
		{
			name:  "pending names only other configs",
			query: "dryRun=true&pending=Image/base",
			body:  workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n"),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m)
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
			},
			want: workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
		},
	})

	runApplyWorkflowErrors(t, []applyWorkflowErrorCase{
		{
			name:        "fresh directory without pending",
			query:       "dryRun=true",
			contentType: mimeYAML,
			body:        strings.NewReader(createBoth),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m)
				expectWorkflowReadFails(m, "topology/topo")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`topology "topo"`, "config does not exist"},
		},
		{
			// Refs are not expanded, so a client must send the names that the
			// config dry runs returned.
			name:        "ref with an unexpanded name",
			query:       "dryRun=true&pending=" + url.QueryEscape("Topology/${BRANCH_NAME}-topo"),
			contentType: mimeYAML,
			body:        strings.NewReader(createExpanded),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m)
				expectWorkflowReadFails(m, "topology/main-topo")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`topology "main-topo"`, "config does not exist"},
		},
		{
			name:        "pending on a real apply",
			query:       "pending=Topology/topo",
			contentType: mimeYAML,
			body:        strings.NewReader(createBoth),
			wantStatus:  http.StatusBadRequest,
			wantMessage: pendingMsg,
		},
		{
			name:        "pending with dryRun=false",
			query:       "dryRun=false&pending=Topology/topo",
			contentType: mimeYAML,
			body:        strings.NewReader(createBoth),
			wantStatus:  http.StatusBadRequest,
			wantMessage: pendingMsg,
		},
		{
			name:        "pending that is not a config name",
			query:       "dryRun=true&pending=topo",
			contentType: mimeYAML,
			body:        strings.NewReader(createBoth),
			setup:       func(m *store.MockStore) { expectWorkflowList(m) },
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`pending config "topo"`},
		},
		{
			// The real apply that follows the upsert looks every config up.
			name:        "real apply with the topology still missing",
			query:       "expect=createAndStart",
			contentType: mimeYAML,
			body:        strings.NewReader(createBoth),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m)
				expectWorkflowReadFails(m, "topology/topo")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`topology "topo"`, "config does not exist"},
		},
		{
			// The real apply of the first deploy above, after the upsert.
			name:        "real apply of a first deploy whose scenario names another topology",
			query:       "expect=createAndStart",
			contentType: mimeYAML,
			body:        strings.NewReader(createBranchScn),
			setup:       readBranchConfigs(workflowTestScenario(map[string]any{"name": "app1"})),
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`scenario "scn" is annotated for topology "topo", not "main-topo"`},
		},
		{
			name:        "real apply of a first deploy whose scenario has no topology annotation",
			query:       "expect=createAndStart",
			contentType: mimeYAML,
			body:        strings.NewReader(createBranchScn),
			setup:       readBranchConfigs(unannotated),
			wantStatus:  http.StatusBadRequest,
			wantMessage: prepareMsg,
			wantCause:   []string{`scenario "scn" has no topology annotation`},
		},
	})
}

// TestApplyWorkflowRejectsQuery checks invalid dryRun and expect values and an
// expect that differs from the plan. An invalid dryRun is refused before
// experiments are listed; expect is checked after the plan is prepared and
// before anything is carried out, so the store mock allows only the listing
// and the reads that preparing makes.
func TestApplyWorkflowRejectsQuery(t *testing.T) {
	var (
		body       = workflowTestYAML("  topology: topo\n")
		applyMsg   = "unable to apply phenix workflow config"
		restartFoo = func(m *store.MockStore) {
			expectWorkflowList(m, workflowTestExperiment("foo", "main", true))
			expectWorkflowRead(m, "topology/topo", workflowTestTopology())
		}
	)

	runApplyWorkflowErrors(t, []applyWorkflowErrorCase{
		{
			name:        "dryRun is not a bool",
			query:       "dryRun=maybe",
			contentType: mimeYAML,
			body:        strings.NewReader(body),
			wantStatus:  http.StatusBadRequest,
			wantMessage: `invalid dryRun value "maybe"`,
			wantCause:   []string{"invalid syntax"},
		},
		{
			name:        "dryRun without a value",
			query:       "dryRun",
			contentType: mimeYAML,
			body:        strings.NewReader(body),
			wantStatus:  http.StatusBadRequest,
			wantMessage: `invalid dryRun value ""`,
		},
		{
			name:        "expect is not an action",
			query:       "expect=rebuild",
			contentType: mimeYAML,
			body:        strings.NewReader(body),
			wantStatus:  http.StatusBadRequest,
			wantMessage: `invalid expect value "rebuild"`,
			wantCause:   []string{`unknown workflow action "rebuild"`},
		},
		{
			// The running experiment would be restarted, so nothing may be
			// stopped when the client expected an update.
			name:        "expect differs from the plan",
			query:       "expect=update",
			contentType: mimeYAML,
			body:        strings.NewReader(body),
			setup:       restartFoo,
			wantStatus:  http.StatusConflict,
			wantMessage: applyMsg,
			wantCause:   []string{"workflow plan changed"},
		},
		{
			name:        "a dry run checks expect too",
			query:       "dryRun=true&expect=updateAndStart",
			contentType: mimeYAML,
			body:        strings.NewReader(body),
			setup:       restartFoo,
			wantStatus:  http.StatusConflict,
			wantMessage: applyMsg,
			wantCause:   []string{"workflow plan changed"},
		},
	})
}

// TestApplyWorkflowDryRunCreates checks dry runs for a branch with no mapped
// experiment. Each responds with the plan and makes only the listing and the
// reads that preparing the plan needs.
func TestApplyWorkflowDryRunCreates(t *testing.T) {
	listAndReadTopology := func(m *store.MockStore) {
		expectWorkflowList(m)
		expectWorkflowRead(m, "topology/topo", workflowTestTopology())
	}

	jsonCreate := `{"apiVersion":"phenix.sandia.gov/v0","kind":"Workflow","metadata":{},` +
		`"spec":{"auto":{"create":"foo"},"topology":"topo"}}`

	runApplyWorkflowResults(t, true, []applyWorkflowResultCase{
		{
			name:  "nothing mapped and no auto.create",
			query: "dryRun=true",
			body:  workflowTestYAML("  topology: topo\n"),
			setup: func(m *store.MockStore) { expectWorkflowList(m, workflowTestExperiment("bar", "", true)) },
			want: workflow.Plan{
				Action: workflow.ActionNone, Experiment: "", Reason: "no experiment mapped and auto.create not set",
			},
		},
		{
			name:  "create",
			query: "dryRun=true",
			body:  workflowTestYAML("  auto:\n    create: foo\n    restart: false\n  topology: topo\n"),
			setup: listAndReadTopology,
			want:  workflow.Plan{Action: workflow.ActionCreate, Experiment: "foo", Reason: ""},
		},
		{
			name:  "createAndStart ignores experiments mapped to other branches",
			query: "dryRun=true",
			body:  workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n"),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, workflowTestExperiment("bar", "other", true))
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
			},
			want: workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
		},
		{
			name:  "auto.create expands BRANCH_NAME",
			query: "dryRun=true",
			body:  workflowTestYAML("  auto:\n    create: ${BRANCH_NAME}-exp\n  topology: topo\n"),
			setup: listAndReadTopology,
			want:  workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "main-exp", Reason: ""},
		},
		{
			name:        "JSON config",
			query:       "dryRun=true",
			contentType: mimeJSON,
			body:        jsonCreate,
			setup:       listAndReadTopology,
			want:        workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
		},
		{
			name:  "create with a scenario",
			query: "dryRun=true",
			body:  workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n  scenario: scn\n"),
			setup: func(m *store.MockStore) {
				listAndReadTopology(m)
				expectWorkflowRead(m, "scenario/scn", workflowTestScenario(map[string]any{"name": "app1"}))
			},
			want: workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""},
		},
	})
}

// TestApplyWorkflowDryRunUpdates checks dry runs for a branch with one mapped
// experiment. Each responds with the plan and makes only the listing and the
// reads that preparing the plan needs, so nothing is stopped or written.
func TestApplyWorkflowDryRunUpdates(t *testing.T) {
	var (
		fooRunning = workflowTestExperiment("foo", "main", true)
		fooStopped = workflowTestExperiment("foo", "main", false)
		annotated  = workflowTestExperiment("foo", "main", false)
		restart    = workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo", Reason: ""}
		topoOnly   = workflowTestYAML("  topology: topo\n")
	)

	annotated.Metadata.Annotations["topology"] = "topo"

	readTopology := func(m *store.MockStore) { expectWorkflowRead(m, "topology/topo", workflowTestTopology()) }

	runApplyWorkflowResults(t, true, []applyWorkflowResultCase{
		{
			name:  "auto.update false",
			query: "dryRun=true",
			body:  workflowTestYAML("  auto:\n    update: false\n"),
			setup: func(m *store.MockStore) { expectWorkflowList(m, fooRunning) },
			want:  workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "auto.update is false"},
		},
		{
			name:  "running and auto.restart false",
			query: "dryRun=true",
			body:  workflowTestYAML("  auto:\n    restart: false\n"),
			setup: func(m *store.MockStore) { expectWorkflowList(m, fooRunning) },
			want:  workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "running and auto.restart is false"},
		},
		{
			name:  "restart",
			query: "dryRun=true",
			body:  topoOnly,
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, fooRunning, workflowTestExperiment("bar", "", false))
				readTopology(m)
			},
			want: restart,
		},
		{
			name:  "updateAndStart takes the topology annotation",
			query: "dryRun=true",
			body:  workflowTestYAML("  {}\n"),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, annotated)
				readTopology(m)
			},
			want: workflow.Plan{Action: workflow.ActionUpdateAndStart, Experiment: "foo", Reason: ""},
		},
		{
			name:  "update",
			query: "dryRun=true",
			body:  workflowTestYAML("  auto:\n    restart: false\n  topology: topo\n"),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, fooStopped)
				readTopology(m)
			},
			want: workflow.Plan{Action: workflow.ActionUpdate, Experiment: "foo", Reason: ""},
		},
		{
			name:  "dryRun=1",
			query: "dryRun=1",
			body:  topoOnly,
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, fooRunning)
				readTopology(m)
			},
			want: restart,
		},
		{
			name:  "expect matching the plan",
			query: "dryRun=true&expect=restart",
			body:  topoOnly,
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, fooRunning)
				readTopology(m)
			},
			want: restart,
		},
	})
}

// TestApplyWorkflowNoneChangesNothing checks that a real apply whose plan is
// none responds with the plan as JSON and makes no store call besides listing
// experiments.
func TestApplyWorkflowNoneChangesNothing(t *testing.T) {
	runApplyWorkflowResults(t, false, []applyWorkflowResultCase{
		{
			name:  "no dryRun parameter",
			body:  workflowTestYAML("  auto:\n    restart: false\n"),
			setup: func(m *store.MockStore) { expectWorkflowList(m, workflowTestExperiment("foo", "main", true)) },
			want:  workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "running and auto.restart is false"},
		},
		{
			name:  "dryRun=false",
			query: "dryRun=false",
			body:  workflowTestYAML("  auto:\n    update: false\n"),
			setup: func(m *store.MockStore) { expectWorkflowList(m, workflowTestExperiment("foo", "main", false)) },
			want:  workflow.Plan{Action: workflow.ActionNone, Experiment: "foo", Reason: "auto.update is false"},
		},
		{
			// spec: {} applies every default; spec: with nothing under it is
			// a schema error.
			name:  "empty spec",
			body:  workflowTestYAML("  {}\n"),
			setup: func(m *store.MockStore) { expectWorkflowList(m) },
			want: workflow.Plan{
				Action: workflow.ActionNone, Experiment: "", Reason: "no experiment mapped and auto.create not set",
			},
		},
		{
			name:  "expect=none",
			query: "expect=none",
			body:  workflowTestYAML("  topology: topo\n"),
			setup: func(m *store.MockStore) { expectWorkflowList(m) },
			want: workflow.Plan{
				Action: workflow.ActionNone, Experiment: "", Reason: "no experiment mapped and auto.create not set",
			},
		},
	})
}

// TestApplyWorkflowExecutesPlan checks that a real apply whose expect matches
// the plan carries out each action with the prepared values, up to a store
// call that the mock fails so that nothing reaches minimega. A create reads
// its topology again inside experiment.Create; an update first reads the
// experiment to write it; a restart first reads it to stop it.
func TestApplyWorkflowExecutesPlan(t *testing.T) {
	var (
		fooStopped      = workflowTestExperiment("foo", "main", false)
		fooRunning      = workflowTestExperiment("foo", "main", true)
		fooWithScenario = workflowTestExperiment("foo", "main", true)
		createMsg       = "unable to create new experiment"
		writeMsg        = "unable to write updated experiment foo"
		createBody      = "  auto:\n    create: foo\n  topology: topo\n"
		updateBody      = "  auto:\n    restart: false\n  topology: topo\n"
	)

	fooWithScenario.Metadata.Annotations["scenario"] = "scn"

	prepared := func(m *store.MockStore, configs ...store.Config) {
		expectWorkflowList(m, configs...)
		expectWorkflowRead(m, "topology/topo", workflowTestTopology())
	}

	createFails := func(m *store.MockStore) {
		prepared(m)
		expectWorkflowReadFails(m, "topology/topo")
	}

	runApplyWorkflowErrors(t, []applyWorkflowErrorCase{
		{
			// The tag reaches the new experiment's annotations before
			// experiment.Create reads the topology; the E2E create checks
			// the stored annotation.
			name:        "create",
			query:       "expect=create&tag=a%3D1",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  auto:\n    create: foo\n    restart: false\n  topology: topo\n")),
			setup:       createFails,
			wantStatus:  http.StatusInternalServerError,
			wantMessage: createMsg,
			wantCause:   []string{"topology doesn't exist"},
		},
		{
			name:        "createAndStart",
			query:       "expect=createAndStart",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML(createBody)),
			setup:       createFails,
			wantStatus:  http.StatusInternalServerError,
			wantMessage: createMsg,
			wantCause:   []string{"topology doesn't exist"},
		},
		{
			name:        "update",
			query:       "expect=update",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML(updateBody)),
			setup: func(m *store.MockStore) {
				prepared(m, fooStopped)
				expectWorkflowReadFails(m, "experiment/foo")
			},
			wantStatus:  http.StatusInternalServerError,
			wantMessage: writeMsg,
			wantCause:   []string{"config does not exist"},
		},
		{
			name:        "updateAndStart",
			query:       "expect=updateAndStart",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: topo\n")),
			setup: func(m *store.MockStore) {
				prepared(m, fooStopped)
				expectWorkflowReadFails(m, "experiment/foo")
			},
			wantStatus:  http.StatusInternalServerError,
			wantMessage: writeMsg,
			wantCause:   []string{"config does not exist"},
		},
		{
			name:        "restart",
			query:       "expect=restart",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: topo\n")),
			setup: func(m *store.MockStore) {
				prepared(m, fooRunning)
				expectWorkflowReadFails(m, "experiment/foo")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "unable to stop experiment foo",
			wantCause:   []string{"config does not exist"},
		},
		{
			// The scenario comes from the running experiment's annotation.
			// Each config is read exactly once. The stop's first store call
			// fails and ends the request, so the topology and scenario reads
			// the mock requires happened before the stop.
			name:        "restart with the scenario annotation",
			query:       "expect=restart",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML("  topology: topo\n")),
			setup: func(m *store.MockStore) {
				prepared(m, fooWithScenario)
				expectWorkflowRead(m, "scenario/scn", workflowTestScenario(map[string]any{"name": "app1"}))
				expectWorkflowReadFails(m, "experiment/foo")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "unable to stop experiment foo",
			wantCause:   []string{"config does not exist"},
		},
		{
			name:        "update written but not reconfigured",
			contentType: mimeYAML,
			body:        strings.NewReader(workflowTestYAML(updateBody)),
			setup: func(m *store.MockStore) {
				prepared(m, fooStopped)
				expectWorkflowRead(m, "experiment/foo", fooStopped)
				m.EXPECT().Update(gomock.Any()).Return(nil)
				// Reconfiguring the experiment first reads it again.
				expectWorkflowReadFails(m, "experiment/foo")
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "unable to reconfigure updated experiment foo",
			wantCause:   []string{"config does not exist"},
		},
	})
}

// TestApplyWorkflowLockedExperiment checks that a real apply fails with 500,
// after preparing and before anything is written, when another request holds
// the experiment's lock.
func TestApplyWorkflowLockedExperiment(t *testing.T) {
	tests := []struct {
		name        string
		lock        func(name string) error
		body        string
		setup       func(m *store.MockStore)
		wantMessage string
		wantCause   string
	}{
		{
			name: "create",
			lock: cache.LockExperimentForCreation,
			body: workflowTestYAML("  auto:\n    create: foo\n  topology: topo\n"),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m)
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
			},
			wantMessage: "unable to create new experiment",
			wantCause:   "experiment foo is locked with status creating",
		},
		{
			name: "update",
			lock: cache.LockExperimentForUpdate,
			body: workflowTestYAML("  auto:\n    restart: false\n  topology: topo\n"),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, workflowTestExperiment("foo", "main", false))
				expectWorkflowRead(m, "topology/topo", workflowTestTopology())
			},
			wantMessage: "unable to update experiment foo",
			wantCause:   "experiment foo is locked with status updating",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(installValidationTestStore(t))

			if err := tc.lock("foo"); err != nil {
				t.Fatalf("locking foo: %v", err)
			}

			t.Cleanup(func() { cache.UnlockExperiment("foo") })

			req := applyWorkflowRequest(workflowTestRole(true), "", mimeYAML, strings.NewReader(tc.body))
			assertWorkflowError(t, serveApplyWorkflow(t, req), http.StatusInternalServerError, tc.wantMessage, tc.wantCause)
		})
	}
}

// TestApplyWorkflowUpdateWritesExperiment checks what a real update writes for
// the experiment mapped to the branch: the prepared topology, scenario, VLAN
// aliases and schedules, and the bridge, deploy mode, VLAN range, GRE mesh and
// tags from the workflow and the request. The workflow's scenario takes its
// app from the stored scenario base through fromScenario. Preparing reads,
// decodes and merges it once; the mock allows each config read exactly once,
// and the experiment is written from the prepared values without another
// topology or scenario read. The store write fails, so nothing is
// reconfigured or started.
func TestApplyWorkflowUpdateWritesExperiment(t *testing.T) {
	type storedVLANs struct {
		Aliases map[string]int `json:"aliases"`
		Min     int            `json:"min"`
		Max     int            `json:"max"`
	}

	type storedApp struct {
		Name     string         `json:"name"`
		Metadata map[string]any `json:"metadata"`
	}

	type storedScenario struct {
		Apps []storedApp `json:"apps"`
	}

	type storedSpec struct {
		DefaultBridge string            `json:"defaultBridge"`
		DeployMode    string            `json:"deployMode"`
		UseGREMesh    bool              `json:"useGREMesh"`
		Schedules     map[string]string `json:"schedules"`
		VLANs         storedVLANs       `json:"vlans"`
		Scenario      storedScenario    `json:"scenario"`
	}

	stored := workflowTestExperiment("foo", "main", false)
	stored.Spec = map[string]any{"vlans": map[string]any{"aliases": map[string]any{"OLD": 5}}}

	base := workflowTestScenario(map[string]any{"name": "app1", "metadata": map[string]any{"from": "base"}})
	base.Metadata.Name = "base"

	var written *store.Config

	m := installValidationTestStore(t)
	expectWorkflowList(m, stored)
	expectWorkflowRead(m, "topology/topo", workflowTestTopology())
	expectWorkflowRead(m, "scenario/scn", workflowTestScenario(map[string]any{"name": "app1", "fromScenario": "base"}))
	expectWorkflowRead(m, "scenario/base", base)
	expectWorkflowRead(m, "experiment/foo", stored)
	m.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		written = c

		return errors.New("disk full")
	})

	body := workflowTestYAML("  auto:\n    restart: false\n  topology: topo\n  scenario: scn\n" +
		"  vlans:\n    EXP_1: 101\n  schedules:\n    vm1: host1\n    ext1: host2\n" +
		"  defaultBridge: br0\n  deployMode: all\n  vlanRange:\n    max: 200\n  useGREMesh: true\n")
	req := applyWorkflowRequest(workflowTestRole(true), "tag=a%3D1&tag=b%3D2", mimeYAML, strings.NewReader(body))
	assertWorkflowError(
		t, serveApplyWorkflow(t, req), http.StatusInternalServerError, "unable to write updated experiment foo", "disk full",
	)

	if written == nil {
		t.Fatal("expected the updated experiment to be written")
	}

	wantAnnotations := store.Annotations{
		workflow.BranchAnnotation: "main",
		workflow.TagsAnnotation:   "a=1,b=2",
		"topology":                "topo",
		"scenario":                "scn",
	}

	if !reflect.DeepEqual(written.Metadata.Annotations, wantAnnotations) {
		t.Errorf("expected annotations %v, got %v", wantAnnotations, written.Metadata.Annotations)
	}

	raw, err := json.Marshal(written.Spec)
	if err != nil {
		t.Fatalf("encoding written spec: %v", err)
	}

	var got storedSpec
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decoding written spec %s: %v", raw, err)
	}

	want := storedSpec{
		DefaultBridge: "br0",
		DeployMode:    "all",
		UseGREMesh:    true,
		Schedules:     map[string]string{"vm1": "host1"},
		VLANs:         storedVLANs{Aliases: map[string]int{"EXP_1": 101, "EXP_2": 0}, Min: 0, Max: 200},
		Scenario:      storedScenario{Apps: []storedApp{{Name: "app1", Metadata: map[string]any{"from": "base"}}}},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected written spec %+v, got %+v", want, got)
	}
}

// configDryRunPending sends body, a YAML config, to the workflow configs
// endpoint as a dry run for the branch main, as the CLI does for each file in
// phenix-configs/, and returns the pending ref its answer carries. The raw
// answer must carry the ref as it is, with no HTML escape, so a curl user can
// copy it: "&", not \u0026.
func configDryRunPending(t *testing.T, body string) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow/configs/main?dryRun=true", strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"branch": "main"})
	req.Header.Set("Content-Type", mimeYAML)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, workflowTestRole(true)))

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(WorkflowUpsertConfig).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("config dry run: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result workflow.ConfigResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decoding config dry run answer %q: %v", rec.Body.String(), err)
	}

	if raw := `"pending":"` + result.Pending + `"`; !strings.Contains(rec.Body.String(), raw) {
		t.Fatalf("config dry run answer %s does not carry %s as it is", rec.Body.String(), raw)
	}

	return result.Pending
}

// assertPendingApplyDryRun sends body, a YAML workflow config, to the apply
// endpoint as a dry run for the branch main with refs as its pending refs, as
// the CLI does after its config dry runs, and checks the answer: the plan
// createAndStart for foo when wantCause is empty, else a 400 whose cause
// contains wantCause.
func assertPendingApplyDryRun(t *testing.T, body string, refs []string, wantCause string) {
	t.Helper()

	query := url.Values{"dryRun": {"true"}, "pending": refs}.Encode()
	rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), query, mimeYAML, strings.NewReader(body)))

	if wantCause == "" {
		assertWorkflowResult(t, rec, workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "foo", Reason: ""}, true)

		return
	}

	assertWorkflowError(t, rec, http.StatusBadRequest, "unable to prepare phenix workflow config", wantCause)
}

// TestApplyWorkflowDryRunChecksPendingReferences runs the server side of a
// first deploy's dry run under the branch main: a config dry run for each
// file in phenix-configs/, then the apply dry run with the pending refs those
// answers carried. The topology is named after the branch, so a scenario, or
// the scenario its app takes settings from, that is still annotated for the
// file's plain topology name is refused before anything is staged or
// upserted. The strict store mock allows only each config dry run's existence
// check, the listing and the read of a fromScenario that is not in the
// directory, so nothing is written and no pending config is read.
func TestApplyWorkflowDryRunChecksPendingReferences(t *testing.T) {
	const topology = "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: ${BRANCH_NAME}-topo\nspec:\n  nodes: []\n"

	scenario := func(name, annotation, from string) string {
		doc := "apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: " + name +
			"\n  annotations:\n    topology: " + annotation + "\nspec:\n  apps:\n  - name: app1\n"
		if from != "" {
			doc += "    fromScenario: " + from + "\n"
		}

		return doc
	}

	body := workflowTestYAML("  auto:\n    create: foo\n  topology: ${BRANCH_NAME}-topo\n  scenario: scn\n")

	tests := []struct {
		name      string
		scn       string
		base      string
		setup     func(m *store.MockStore)
		wantRefs  []string
		wantCause string
	}{
		{
			name: "annotations follow the branch",
			scn:  scenario("scn", "${BRANCH_NAME}-topo", "base"),
			base: scenario("base", "${BRANCH_NAME}-topo", ""),
			wantRefs: []string{
				"Topology/main-topo",
				"Scenario/scn?from=base&topology=main-topo",
				"Scenario/base?topology=main-topo",
			},
		},
		{
			name:      "scenario annotated for the plain topology name",
			scn:       scenario("scn", "topo", "base"),
			base:      scenario("base", "${BRANCH_NAME}-topo", ""),
			wantCause: `scenario "scn" is annotated for topology "topo", not "main-topo"`,
		},
		{
			name:      "fromScenario annotated for the plain topology name",
			scn:       scenario("scn", "${BRANCH_NAME}-topo", "base"),
			base:      scenario("base", "topo", ""),
			wantCause: `merging scenario "scn": experiment/scenario topology mismatch for scenario base`,
		},
		{
			name:      "fromScenario in neither the directory nor the store",
			scn:       scenario("scn", "${BRANCH_NAME}-topo", "retired"),
			base:      scenario("base", "${BRANCH_NAME}-topo", ""),
			setup:     func(m *store.MockStore) { expectWorkflowReadFails(m, "scenario/retired") },
			wantCause: `merging scenario "scn": scenario retired doesn't exist`,
		},
		{
			// The v2 Scenario schema does not close an app, and the real
			// apply's decode matches FromScenario as fromScenario.
			name: "FromScenario key in neither the directory nor the store",
			scn:  strings.Replace(scenario("scn", "${BRANCH_NAME}-topo", "retired"), "fromScenario:", "FromScenario:", 1),
			base: scenario("base", "${BRANCH_NAME}-topo", ""),
			setup: func(m *store.MockStore) {
				expectWorkflowReadFails(m, "scenario/retired")
			},
			wantRefs: []string{
				"Topology/main-topo",
				"Scenario/scn?from=retired&topology=main-topo",
				"Scenario/base?topology=main-topo",
			},
			wantCause: `merging scenario "scn": scenario retired doesn't exist`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BRANCH_NAME", "") // the config dry runs set it

			// Each config dry run checks whether its config is stored: none
			// is. The apply dry run then lists the experiments: there are none.
			m := installValidationTestStore(t)
			expectWorkflowReadFails(m, "topology/main-topo")
			expectWorkflowReadFails(m, "scenario/scn")
			expectWorkflowReadFails(m, "scenario/base")
			expectWorkflowList(m)

			if tc.setup != nil {
				tc.setup(m)
			}

			docs := []string{topology, tc.scn, tc.base}
			refs := make([]string, 0, len(docs))

			for _, doc := range docs {
				refs = append(refs, configDryRunPending(t, doc))
			}

			if tc.wantRefs != nil && !reflect.DeepEqual(refs, tc.wantRefs) {
				t.Fatalf("pending refs = %q, want %q", refs, tc.wantRefs)
			}

			assertPendingApplyDryRun(t, body, refs, tc.wantCause)
		})
	}
}

// TestApplyWorkflowDryRunChecksPendingIncludes runs the server side of a
// first deploy's dry run under the branch main whose topology, named after
// the branch, includes another topology named after the branch. The included
// topology is a file in phenix-configs/, or it is in neither the directory
// nor the store, as a list entry or as a single value. The strict store mock
// allows only each config dry run's existence check, the listing and one read
// of the included topology: by its own config dry run when it is in the
// directory, else by the apply dry run. So nothing is written and no pending
// config is read.
func TestApplyWorkflowDryRunChecksPendingIncludes(t *testing.T) {
	const netTopology = "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: ${BRANCH_NAME}-net\nspec:\n  nodes: []\n"

	topology := func(includes string) string {
		return "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: ${BRANCH_NAME}\nspec:\n  includeTopologies: " +
			includes + "\n  nodes: []\n"
	}

	var (
		body     = workflowTestYAML("  auto:\n    create: foo\n  topology: ${BRANCH_NAME}\n")
		notFound = `decoding topology "main": loading included topology main-net: could not find topology main-net`
	)

	tests := []struct {
		name      string
		docs      []string // the files in phenix-configs/
		wantRefs  []string
		wantCause string
	}{
		{
			name:     "included topology in the directory",
			docs:     []string{topology("[${BRANCH_NAME}-net]"), netTopology},
			wantRefs: []string{"Topology/main?include=main-net", "Topology/main-net"},
		},
		{
			// No file named main-net is in the test's working directory either.
			name:      "included topology in neither the directory nor the store",
			docs:      []string{topology("[${BRANCH_NAME}-net]")},
			wantRefs:  []string{"Topology/main?include=main-net"},
			wantCause: notFound,
		},
		{
			// The v2 Topology schema does not declare includeTopologies, and
			// the real apply's weak decode reads a single value as a list.
			name:      "single include in neither the directory nor the store",
			docs:      []string{topology("${BRANCH_NAME}-net")},
			wantRefs:  []string{"Topology/main?include=main-net"},
			wantCause: notFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BRANCH_NAME", "") // the config dry runs set it

			m := installValidationTestStore(t)
			expectWorkflowReadFails(m, "topology/main")
			expectWorkflowReadFails(m, "topology/main-net")
			expectWorkflowList(m)

			refs := make([]string, 0, len(tc.docs))
			for _, doc := range tc.docs {
				refs = append(refs, configDryRunPending(t, doc))
			}

			if !reflect.DeepEqual(refs, tc.wantRefs) {
				t.Fatalf("pending refs = %q, want %q", refs, tc.wantRefs)
			}

			assertPendingApplyDryRun(t, body, refs, tc.wantCause)
		})
	}
}
