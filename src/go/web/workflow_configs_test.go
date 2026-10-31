package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"

	"phenix/store"
	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// upsertTestTopology is a valid topology whose name expands to main-topo on
// branch main.
const upsertTestTopology = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: ${BRANCH_NAME}-topo
spec:
  nodes: []
`

// upsertTestNodeTopology is a valid topology with a linux VM, host-01, whose
// name expands to main-topo on branch main.
const upsertTestNodeTopology = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: ${BRANCH_NAME}-topo
spec:
  nodes:
  - type: VirtualMachine
    general:
      hostname: host-01
    hardware:
      os_type: linux
      drives:
      - image: ubuntu.qc2
`

// upsertTestIncludeTopology is a topology without nodes of its own that
// includes a topology that is not stored; its name expands to main-topo on
// branch main. The Topology schema does not declare includeTopologies.
const upsertTestIncludeTopology = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: ${BRANCH_NAME}-topo
spec:
  includeTopologies: [not-stored]
  nodes: []
`

// upsertTestBadTopology is a topology that passes its schema but that no
// experiment can use: its VM is named all, the hostname minimega reserves. Its
// name expands to main-topo on branch main.
const upsertTestBadTopology = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: ${BRANCH_NAME}-topo
spec:
  nodes:
  - type: VirtualMachine
    general:
      hostname: all
    hardware:
      os_type: linux
      drives:
      - image: x.qc2
`

// upsertTestUndecodableTopology is a topology that passes its schema, which
// does not declare includeTopologies, but does not decode: a mapping is no
// include name. Its name expands to main-topo on branch main.
const upsertTestUndecodableTopology = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: ${BRANCH_NAME}-topo
spec:
  includeTopologies:
  - name: x
  nodes: []
`

// upsertTestScenarioWithNodes is a valid scenario, named main-scn on branch
// main, whose spec also carries a nodes list that a topology could not use.
const upsertTestScenarioWithNodes = `apiVersion: phenix.sandia.gov/v2
kind: Scenario
metadata:
  name: ${BRANCH_NAME}-scn
spec:
  apps:
  - name: app1
  nodes:
  - general:
      hostname: all
`

// upsertTestExperiment is a valid experiment whose name expands to main-exp on
// branch main. Creating or updating it for real runs the experiment config
// hook, which lists the stored experiments.
const upsertTestExperiment = `apiVersion: phenix.sandia.gov/v1
kind: Experiment
metadata:
  name: ${BRANCH_NAME}-exp
spec:
  topology:
    nodes: []
`

// expectUpsertExists expects the handler's one existence check for key, and
// answers it: found when exists is true, not found otherwise.
func expectUpsertExists(m *store.MockStore, key *store.Config, exists bool) {
	if exists {
		m.EXPECT().Get(gomock.Eq(key)).Return(nil)

		return
	}

	m.EXPECT().Get(gomock.Eq(key)).
		Return(fmt.Errorf("%w: key %s does not exist in bucket %s", store.ErrNotExist, key.Metadata.Name, key.Kind))
}

// upsertTestRequest builds a YAML upsert request for branch main, with query
// appended to the path. Its context carries an allow-all role.
func upsertTestRequest(t *testing.T, query, body string) *http.Request {
	t.Helper()

	return validationTestRequest(
		t,
		http.MethodPost,
		"/api/v1/workflow/configs/main"+query,
		mimeYAML,
		[]byte(body),
		map[string]string{"branch": "main"},
	)
}

// upsertDryRunResult is the decoded body of a config dry run that would
// take action on kind/name, with the pending ref an apply dry run is given.
func upsertDryRunResult(action, kind, name, pending string) map[string]any {
	return map[string]any{"action": action, "kind": kind, "name": name, "dryRun": true, "pending": pending}
}

// upsertWebError is what a client gets of a WebError, and what the tests compare.
type upsertWebError struct {
	Status         int
	Message        string
	Cause          string
	SystemMetadata map[string]string
	UserMetadata   map[string]string
}

func newUpsertWebError(werr *weberror.WebError) upsertWebError {
	return upsertWebError{
		Status:         werr.Status,
		Message:        werr.Message,
		Cause:          werr.Cause,
		SystemMetadata: werr.SystemMetadata,
		UserMetadata:   werr.UserMetadata,
	}
}

func TestWorkflowUpsertConfigDryRun(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		key    string
		exists bool
		want   map[string]any
	}{
		{
			name:   "new topology",
			body:   upsertTestTopology,
			key:    "topology/main-topo",
			exists: false,
			want:   upsertDryRunResult("create", "Topology", "main-topo", "Topology/main-topo"),
		},
		{
			name:   "stored topology",
			body:   upsertTestTopology,
			key:    "topology/main-topo",
			exists: true,
			want:   upsertDryRunResult("update", "Topology", "main-topo", "Topology/main-topo"),
		},
		{
			name:   "topology with a node",
			body:   upsertTestNodeTopology,
			key:    "topology/main-topo",
			exists: false,
			want:   upsertDryRunResult("create", "Topology", "main-topo", "Topology/main-topo"),
		},
		{
			// The include is not loaded: the node check covers the config's
			// own nodes only.
			name:   "topology that includes a topology that is not stored",
			body:   upsertTestIncludeTopology,
			key:    "topology/main-topo",
			exists: false,
			want:   upsertDryRunResult("create", "Topology", "main-topo", "Topology/main-topo?include=not-stored"),
		},
		{
			// A scenario is not decoded as a topology.
			name:   "scenario whose spec carries nodes",
			body:   upsertTestScenarioWithNodes,
			key:    "scenario/main-scn",
			exists: false,
			want:   upsertDryRunResult("create", "Scenario", "main-scn", "Scenario/main-scn?"),
		},
		{
			name:   "new experiment runs no config hook",
			body:   upsertTestExperiment,
			key:    "experiment/main-exp",
			exists: false,
			want:   upsertDryRunResult("create", "Experiment", "main-exp", "Experiment/main-exp"),
		},
		{
			name:   "stored experiment runs no config hook",
			body:   upsertTestExperiment,
			key:    "experiment/main-exp",
			exists: true,
			want:   upsertDryRunResult("update", "Experiment", "main-exp", "Experiment/main-exp"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BRANCH_NAME", "") // the handler sets it; t.Setenv restores it

			// The strict mock allows only the existence check. A real create
			// or update would add Create or Update, config.Update's second
			// Get, or, through the experiment hook, List.
			m := installValidationTestStore(t)
			expectUpsertExists(m, workflowTestKey(tt.key), tt.exists)

			rec := httptest.NewRecorder()

			if err := WorkflowUpsertConfig(rec, upsertTestRequest(t, "?dryRun=true", tt.body)); err != nil {
				t.Fatalf("WorkflowUpsertConfig(?dryRun=true) error = %v", err)
			}

			if rec.Code != http.StatusOK {
				t.Errorf("WorkflowUpsertConfig(?dryRun=true) status = %d, want %d", rec.Code, http.StatusOK)
			}

			if got := rec.Header().Get("Content-Type"); got != mimeJSON {
				t.Errorf("WorkflowUpsertConfig(?dryRun=true) Content-Type = %q, want %q", got, mimeJSON)
			}

			if got := rec.Header().Get("Location"); got != "" {
				t.Errorf("WorkflowUpsertConfig(?dryRun=true) Location = %q, want none", got)
			}

			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding the dry run body %s: %v", rec.Body, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("WorkflowUpsertConfig(?dryRun=true) body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWorkflowUpsertConfigDryRunExplainsValidationErrors sends each invalid
// body for real and as a dry run, and expects the same 400 from both: message,
// cause, metadata.validation and metadata.validation-raw, byte for byte.
func TestWorkflowUpsertConfigDryRunExplainsValidationErrors(t *testing.T) {
	// The workflow endpoint takes JSON and YAML bodies, not uploads.
	bodies := validationTestBodies(t)[:2]

	for _, exists := range []bool{false, true} {
		for _, tt := range bodies {
			t.Run(fmt.Sprintf("%s, exists=%t", tt.name, exists), func(t *testing.T) {
				t.Setenv("BRANCH_NAME", "")

				key := workflowTestKey("topology/demo")

				send := func(query string) error {
					req := validationTestRequest(
						t,
						http.MethodPost,
						"/api/v1/workflow/configs/main"+query,
						tt.contentType,
						tt.body,
						map[string]string{"branch": "main"},
					)

					return WorkflowUpsertConfig(httptest.NewRecorder(), req)
				}

				// The real upsert: one Get even for a stored config, because
				// the schema is checked before config.Update, and its own
				// read, runs.
				realStore := installValidationTestStore(t)
				expectUpsertExists(realStore, key, exists)

				realErr := send("")

				// The dry run, on a fresh strict mock: the same one Get.
				dryStore := installValidationTestStore(t)
				expectUpsertExists(dryStore, key, exists)

				dryErr := send("?dryRun=true")

				if err := validationWebErrorProblem(dryErr, tt.raw, tt.lines); err != nil {
					t.Errorf("WorkflowUpsertConfig(?dryRun=true): %v", err)
				}

				// Every field, unexported ones too, as reflect.DeepEqual compares.
				all := cmp.Exporter(func(reflect.Type) bool { return true })
				if diff := cmp.Diff(realErr, dryErr, all); diff != "" {
					t.Errorf("WorkflowUpsertConfig(?dryRun=true) error mismatch with the real upsert's (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// TestWorkflowUpsertConfigRefusesBadTopology sends topologies that pass their
// schema but that no experiment could use, for real and as a dry run, to a
// stored and to a new config, and expects the same 400 from both: the node
// error's text for a node named all, and the decode error for a topology
// that does not decode. The strict mock allows only the existence check, so
// the real upsert stores nothing and config.Update's own read never happens.
func TestWorkflowUpsertConfigRefusesBadTopology(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantCause string
	}{
		{
			name:      "node named all",
			body:      upsertTestBadTopology,
			wantCause: `topology "main-topo" cannot be used in an experiment: validating node all: hostname 'all' is reserved`,
		},
		{
			name:      "topology that does not decode",
			body:      upsertTestUndecodableTopology,
			wantCause: `decoding topology "main-topo": `,
		},
	}

	for _, tt := range tests {
		for _, exists := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, exists=%t", tt.name, exists), func(t *testing.T) {
				t.Setenv("BRANCH_NAME", "")

				key := workflowTestKey("topology/main-topo")

				wantMessage := "unable to create new config Topology/main-topo"
				if exists {
					wantMessage = "unable to update config Topology/main-topo"
				}

				send := func(query string) error {
					expectUpsertExists(installValidationTestStore(t), key, exists)

					return WorkflowUpsertConfig(httptest.NewRecorder(), upsertTestRequest(t, query, tt.body))
				}

				realErr := send("")
				dryErr := send("?dryRun=true")

				var werr *weberror.WebError
				if !errors.As(dryErr, &werr) {
					t.Fatalf("WorkflowUpsertConfig(?dryRun=true) error = %v, want a *weberror.WebError", dryErr)
				}

				got := upsertWebError{Status: werr.Status, Message: werr.Message}
				want := upsertWebError{Status: http.StatusBadRequest, Message: wantMessage}

				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("WorkflowUpsertConfig(?dryRun=true) error mismatch (-want +got):\n%s", diff)
				}

				if !strings.Contains(werr.Cause, tt.wantCause) {
					t.Errorf("WorkflowUpsertConfig(?dryRun=true) cause = %q, want it to contain %q", werr.Cause, tt.wantCause)
				}

				// Compare what the client gets: wrapped multierror causes are never equal.
				var realWerr *weberror.WebError
				if !errors.As(realErr, &realWerr) {
					t.Fatalf("WorkflowUpsertConfig() error = %v, want a *weberror.WebError", realErr)
				}

				if diff := cmp.Diff(newUpsertWebError(realWerr), newUpsertWebError(werr)); diff != "" {
					t.Errorf("WorkflowUpsertConfig(?dryRun=true) error mismatch with the real upsert's (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// TestWorkflowUpsertConfigDryRunRejectsKindInWrongCase sends a topology whose
// kind is written in lower case. The config schema's kind enum is
// case-sensitive, so the dry run answers 400; were it not, a client that
// upserts "kind: topology" and "kind: Topology" files would store one config
// over the other.
func TestWorkflowUpsertConfigDryRunRejectsKindInWrongCase(t *testing.T) {
	t.Setenv("BRANCH_NAME", "")

	// The strict mock allows only the existence check.
	m := installValidationTestStore(t)
	expectUpsertExists(m, workflowTestKey("topology/demo"), false)

	body := "apiVersion: phenix.sandia.gov/v1\nkind: topology\nmetadata:\n  name: demo\nspec:\n  nodes: []\n"

	err := WorkflowUpsertConfig(httptest.NewRecorder(), upsertTestRequest(t, "?dryRun=true", body))

	err = validationWebErrorProblem(err, `validating config: config validation failed: *"/kind"*`, []string{`kind (line 2): *`})
	if err != nil {
		t.Errorf("WorkflowUpsertConfig(?dryRun=true) of kind topology: %v", err)
	}
}

func TestWorkflowUpsertConfigRejectsBadRequests(t *testing.T) {
	type badRequest struct {
		name      string
		query     string
		body      string
		wantMsg   string
		wantCause string
	}

	tests := []badRequest{
		{
			name:      "dryRun that is not a boolean",
			query:     "?dryRun=maybe",
			body:      upsertTestTopology,
			wantMsg:   `invalid dryRun value "maybe"`,
			wantCause: `strconv.ParseBool: parsing "maybe": invalid syntax`,
		},
		{
			name:      "bare dryRun",
			query:     "?dryRun",
			body:      upsertTestTopology,
			wantMsg:   `invalid dryRun value ""`,
			wantCause: `strconv.ParseBool: parsing "": invalid syntax`,
		},
	}

	unaddressable := []badRequest{
		{
			name:      "workflow config",
			body:      "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata:\n  name: wf\nspec:\n  topology: helloworld\n",
			wantMsg:   "invalid config Workflow/wf",
			wantCause: "invalid config kind provided: Workflow",
		},
		{
			name:      "unknown kind",
			body:      "apiVersion: phenix.sandia.gov/v1\nkind: Topologies\nmetadata:\n  name: demo\nspec:\n  nodes: []\n",
			wantMsg:   "invalid config Topologies/demo",
			wantCause: "invalid config kind provided: Topologies",
		},
		{
			name:      "no kind",
			body:      "apiVersion: phenix.sandia.gov/v1\nmetadata:\n  name: demo\nspec:\n  nodes: []\n",
			wantMsg:   "invalid config /demo",
			wantCause: "invalid config kind provided: ",
		},
		{
			name:      "name containing a slash",
			body:      "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: a/b\nspec:\n  nodes: []\n",
			wantMsg:   "invalid config Topology/a/b",
			wantCause: "invalid config name provided: Topology/a/b",
		},
	}

	tests = slices.Grow(tests, 2*len(unaddressable))

	// A config the store can't address is refused with or without a dry run.
	for _, query := range []string{"?dryRun=true", ""} {
		for _, tt := range unaddressable {
			tt.name = fmt.Sprintf("%s, query %q", tt.name, query)
			tt.query = query
			tests = append(tests, tt)
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BRANCH_NAME", "")
			installValidationTestStore(t) // each is refused before any store call

			err := WorkflowUpsertConfig(httptest.NewRecorder(), upsertTestRequest(t, tt.query, tt.body))

			var werr *weberror.WebError
			if !errors.As(err, &werr) {
				t.Fatalf("WorkflowUpsertConfig(%s) error = %v, want a *weberror.WebError", tt.query, err)
			}

			got := upsertWebError{Status: werr.Status, Message: werr.Message, Cause: werr.Cause}
			want := upsertWebError{Status: http.StatusBadRequest, Message: tt.wantMsg, Cause: tt.wantCause}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("WorkflowUpsertConfig(%s) error mismatch (-want +got):\n%s", tt.query, diff)
			}
		})
	}
}

// A role that may create but not update configs, or the reverse, passes the
// first check and is refused by the check for the named config.
func TestWorkflowUpsertConfigForbidden(t *testing.T) {
	for _, query := range []string{"?dryRun=true", ""} {
		for _, exists := range []bool{false, true} {
			t.Run(fmt.Sprintf("query %q, exists=%t", query, exists), func(t *testing.T) {
				t.Setenv("BRANCH_NAME", "")

				m := installValidationTestStore(t)
				expectUpsertExists(m, workflowTestKey("topology/main-topo"), exists)

				role := configsRole("update")
				want := upsertWebError{
					Status:  http.StatusForbidden,
					Message: "creating config Topology/main-topo not allowed for test-user",
				}
				if exists {
					role = configsRole("create")
					want.Message = "updating config Topology/main-topo not allowed for test-user"
				}

				req := upsertTestRequest(t, query, upsertTestTopology)
				req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, role))

				err := WorkflowUpsertConfig(httptest.NewRecorder(), req)

				var werr *weberror.WebError
				if !errors.As(err, &werr) {
					t.Fatalf("WorkflowUpsertConfig(%s) error = %v, want a *weberror.WebError", query, err)
				}

				got := upsertWebError{Status: werr.Status, Message: werr.Message}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("WorkflowUpsertConfig(%s) error mismatch (-want +got):\n%s", query, diff)
				}
			})
		}
	}
}

// TestWorkflowUpsertConfigWritesWithoutDryRun pins the real upsert, which the
// dry run must not change: 201, a Location header and an empty body, for a
// topology without nodes and for one with a node that passes the node check.
func TestWorkflowUpsertConfigWritesWithoutDryRun(t *testing.T) {
	bodies := []struct {
		name string
		body string
	}{
		{name: "topology without nodes", body: upsertTestTopology},
		{name: "topology with a node", body: upsertTestNodeTopology},
	}

	for _, tt := range bodies {
		for _, query := range []string{"", "?dryRun=false"} {
			for _, exists := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s, query %q, exists=%t", tt.name, query, exists), func(t *testing.T) {
					t.Setenv("BRANCH_NAME", "")

					m := installValidationTestStore(t)
					key := workflowTestKey("topology/main-topo")

					if exists {
						// The handler's existence check, then config.Update's own read.
						m.EXPECT().Get(gomock.Eq(key)).Return(nil).Times(2)
						m.EXPECT().Update(gomock.Any()).Return(nil)
					} else {
						expectUpsertExists(m, key, false)
						m.EXPECT().Create(gomock.Any()).Return(nil)
					}

					rec := httptest.NewRecorder()

					if err := WorkflowUpsertConfig(rec, upsertTestRequest(t, query, tt.body)); err != nil {
						t.Fatalf("WorkflowUpsertConfig(%s) error = %v", query, err)
					}

					if rec.Code != http.StatusCreated {
						t.Errorf("WorkflowUpsertConfig(%s) status = %d, want %d", query, rec.Code, http.StatusCreated)
					}

					if got, want := rec.Header().Get("Location"), "/api/v1/configs/topology/main-topo"; got != want {
						t.Errorf("WorkflowUpsertConfig(%s) Location = %q, want %q", query, got, want)
					}

					if rec.Body.Len() != 0 {
						t.Errorf("WorkflowUpsertConfig(%s) body = %q, want empty", query, rec.Body.String())
					}
				})
			}
		}
	}
}

// configsRole holds the given configs verbs on every config: config names
// such as Topology/lab hold a "/", which "*" alone does not match.
func configsRole(verbs ...string) rbac.Role {
	return configsRoleOn([]string{"*", "*/*"}, verbs...)
}

// configsRoleOn holds the given configs verbs on the configs names match.
func configsRoleOn(names []string, verbs ...string) rbac.Role {
	return rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{
					Resources:     []string{"configs"},
					ResourceNames: names,
					Verbs:         verbs,
				},
			},
		},
	}
}

// Only a caller who may write the named config gets as far as parsing it.
// The parse error then quotes the placeholder, never the environment variable.
func TestWorkflowUpsertConfigChecksPermissionBeforeParsing(t *testing.T) {
	const secret = "s3cret-value-for-test"

	t.Setenv("PHENIX_TEST_SECRET", secret)

	// Parsing fails on the repeated key, with an error that quotes its value.
	body := `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: probe
  "${PHENIX_TEST_SECRET}": a
  "${PHENIX_TEST_SECRET}": b
spec:
  nodes: []
`

	tests := []struct {
		name      string
		role      rbac.Role
		lookup    bool // whether the handler looks the config up
		exists    bool
		forbidden bool
	}{
		// Role.Allowed needs a non-nil Spec.
		{name: "no policies", role: rbac.Role{Spec: &v1.RoleSpec{}}, forbidden: true},
		{name: "read only", role: configsRole("get", "list"), forbidden: true},
		{
			name:      "create other configs",
			role:      configsRoleOn([]string{"Topology/mine-*"}, "create", "update"),
			lookup:    true,
			forbidden: true,
		},
		{
			name:      "update other configs",
			role:      configsRoleOn([]string{"Topology/mine-*"}, "create", "update"),
			lookup:    true,
			exists:    true,
			forbidden: true,
		},
		{name: "create", role: configsRole("create"), lookup: true},
		{name: "update", role: configsRole("update"), lookup: true, exists: true},
		{name: "create this config", role: configsRoleOn([]string{"Topology/probe"}, "create"), lookup: true},
	}

	for _, query := range []string{"?dryRun=true", ""} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s, query %q", tt.name, query), func(t *testing.T) {
				t.Setenv("BRANCH_NAME", "")

				m := installValidationTestStore(t)
				if tt.lookup {
					expectUpsertExists(m, workflowTestKey("topology/probe"), tt.exists)
				}

				req := upsertTestRequest(t, query, body)
				req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, tt.role))

				rec := httptest.NewRecorder()
				weberror.ErrorHandler(WorkflowUpsertConfig).ServeHTTP(rec, req)

				if strings.Contains(rec.Body.String(), secret) {
					t.Fatalf("response quotes the environment variable: %s", rec.Body.String())
				}

				if !tt.forbidden {
					if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "${PHENIX_TEST_SECRET}") {
						t.Fatalf("status = %d, want a 400 that quotes the placeholder: %s", rec.Code, rec.Body.String())
					}

					return
				}

				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
				}
			})
		}
	}
}

// A placeholder other than ${BRANCH_NAME} in a config's kind or name would
// make the permission checks miss the name the config is stored under.
func TestWorkflowUpsertConfigRefusesEnvironmentInName(t *testing.T) {
	const secret = "s3cret-value-for-test"

	t.Setenv("PHENIX_TEST_SECRET", secret)
	t.Setenv("BRANCH_NAME", "")

	m := installValidationTestStore(t)
	expectUpsertExists(m, workflowTestKey("topology/1"), false)

	body := "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: ${PHENIX_TEST_SECRET}\nspec:\n  nodes: []\n"

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(WorkflowUpsertConfig).ServeHTTP(rec, upsertTestRequest(t, "?dryRun=true", body))

	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("status = %d, want a 400 that does not quote the variable: %s", rec.Code, rec.Body.String())
	}
}

func TestConfigName(t *testing.T) {
	t.Setenv("PHENIX_TEST_SECRET", "s3cret-value-for-test")

	tests := []struct {
		name string
		typ  string
		body string
		want string
	}{
		{
			name: "branch name",
			typ:  mimeYAML,
			body: "kind: Topology\nmetadata:\n  name: ${BRANCH_NAME}-topo\n",
			want: "Topology/main-topo",
		},
		{
			name: "branch name with a default",
			typ:  mimeYAML,
			body: "kind: Topology\nmetadata:\n  name: ${BRANCH_NAME:x}-topo\n",
			want: "Topology/main-topo",
		},
		{
			name: "other placeholders",
			typ:  mimeYAML,
			body: "kind: Topology\nmetadata:\n  name: ${PHENIX_TEST_SECRET}\n  ${A}: a\n  ${A}: b\n",
			want: "Topology/1",
		},
		{
			name: "unquoted JSON placeholder",
			typ:  mimeJSON,
			body: `{"kind": "Scenario", "metadata": {"name": "${BRANCH_NAME}"}, "spec": {"port": ${PORT}}}`,
			want: "Scenario/main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configName([]byte(tt.body), tt.typ, "main")
			if err != nil || got != tt.want {
				t.Fatalf("configName() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

// TestReadWorkflowConfigExpandsOnlyBranchName asserts that readWorkflowConfig
// fills in ${BRANCH_NAME} with the branch and keeps every other placeholder.
func TestReadWorkflowConfigExpandsOnlyBranchName(t *testing.T) {
	t.Setenv("BRANCH_NAME", "")
	t.Setenv("PHENIX_WEB_TEST_SECRET", "workflow-secret-value")

	body := workflowTestYAML("  topology: ${BRANCH_NAME}-topo\n  scenario: ${PHENIX_WEB_TEST_SECRET}\n" +
		"  notes: $PHENIX_WEB_TEST_SECRET\n")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow/apply/lab", strings.NewReader(body))
	req.Header.Set("Content-Type", mimeYAML)

	doc, _, err := readWorkflowConfig(req, "lab")
	if err != nil {
		t.Fatalf("readWorkflowConfig(lab) returned error: %v", err)
	}

	want := map[string]any{
		"topology": "lab-topo",
		"scenario": "${PHENIX_WEB_TEST_SECRET}",
		"notes":    "$PHENIX_WEB_TEST_SECRET",
	}

	if diff := cmp.Diff(want, doc["spec"]); diff != "" {
		t.Errorf("readWorkflowConfig(lab) spec mismatch (-want +got):\n%s", diff)
	}
}
