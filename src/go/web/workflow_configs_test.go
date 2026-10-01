package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

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

func TestWorkflowUpsertConfigDryRun(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		key    string
		exists bool
		want   string
	}{
		{
			name:   "new topology",
			body:   upsertTestTopology,
			key:    "topology/main-topo",
			exists: false,
			want:   `{"action":"create","kind":"Topology","name":"main-topo","dryRun":true,"pending":"Topology/main-topo"}`,
		},
		{
			name:   "stored topology",
			body:   upsertTestTopology,
			key:    "topology/main-topo",
			exists: true,
			want:   `{"action":"update","kind":"Topology","name":"main-topo","dryRun":true,"pending":"Topology/main-topo"}`,
		},
		{
			name:   "topology with a node",
			body:   upsertTestNodeTopology,
			key:    "topology/main-topo",
			exists: false,
			want:   `{"action":"create","kind":"Topology","name":"main-topo","dryRun":true,"pending":"Topology/main-topo"}`,
		},
		{
			// The include is not loaded: the node check covers the config's
			// own nodes only.
			name:   "topology that includes a topology that is not stored",
			body:   upsertTestIncludeTopology,
			key:    "topology/main-topo",
			exists: false,
			want:   `{"action":"create","kind":"Topology","name":"main-topo","dryRun":true,"pending":"Topology/main-topo?include=not-stored"}`,
		},
		{
			// A scenario is not decoded as a topology.
			name:   "scenario whose spec carries nodes",
			body:   upsertTestScenarioWithNodes,
			key:    "scenario/main-scn",
			exists: false,
			want:   `{"action":"create","kind":"Scenario","name":"main-scn","dryRun":true,"pending":"Scenario/main-scn?"}`,
		},
		{
			name:   "new experiment runs no config hook",
			body:   upsertTestExperiment,
			key:    "experiment/main-exp",
			exists: false,
			want:   `{"action":"create","kind":"Experiment","name":"main-exp","dryRun":true,"pending":"Experiment/main-exp"}`,
		},
		{
			name:   "stored experiment runs no config hook",
			body:   upsertTestExperiment,
			key:    "experiment/main-exp",
			exists: true,
			want:   `{"action":"update","kind":"Experiment","name":"main-exp","dryRun":true,"pending":"Experiment/main-exp"}`,
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
				t.Fatalf("WorkflowUpsertConfig() error = %v", err)
			}

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}

			if got := rec.Header().Get("Content-Type"); got != mimeJSON {
				t.Errorf("Content-Type = %q, want %q", got, mimeJSON)
			}

			if got := rec.Header().Get("Location"); got != "" {
				t.Errorf("Location = %q, want none", got)
			}

			if got := rec.Body.String(); got != tt.want {
				t.Errorf("body:\n got %s\nwant %s", got, tt.want)
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

				assertValidationWebError(t, dryErr, tt.raw, tt.lines)

				if !reflect.DeepEqual(dryErr, realErr) {
					t.Errorf("dry-run error:\n got %#v\nwant the real upsert's %#v", dryErr, realErr)
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
					t.Fatalf("expected a *weberror.WebError, got %v", dryErr)
				}

				if werr.Status != http.StatusBadRequest || werr.Message != wantMessage || !strings.Contains(werr.Cause, tt.wantCause) {
					t.Errorf(
						"error = {status: %d, message: %q, cause: %q}, want {status: 400, message: %q, cause containing %q}",
						werr.Status, werr.Message, werr.Cause, wantMessage, tt.wantCause,
					)
				}

				if !reflect.DeepEqual(dryErr, realErr) {
					t.Errorf("dry-run error:\n got %#v\nwant the real upsert's %#v", dryErr, realErr)
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

	assertValidationWebError(t, err, `validating config: config validation failed: *"/kind"*`, []string{`kind (line 2): *`})
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
				t.Fatalf("expected a *weberror.WebError, got %v", err)
			}

			if werr.Status != http.StatusBadRequest || werr.Message != tt.wantMsg || werr.Cause != tt.wantCause {
				t.Errorf(
					"error = {status: %d, message: %q, cause: %q}, want {status: 400, message: %q, cause: %q}",
					werr.Status, werr.Message, werr.Cause, tt.wantMsg, tt.wantCause,
				)
			}
		})
	}
}

func TestWorkflowUpsertConfigForbidden(t *testing.T) {
	for _, query := range []string{"?dryRun=true", ""} {
		for _, exists := range []bool{false, true} {
			t.Run(fmt.Sprintf("query %q, exists=%t", query, exists), func(t *testing.T) {
				t.Setenv("BRANCH_NAME", "")

				m := installValidationTestStore(t)
				expectUpsertExists(m, workflowTestKey("topology/main-topo"), exists)

				// A role with no policies. Role.Allowed needs a non-nil Spec.
				req := upsertTestRequest(t, query, upsertTestTopology)
				req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, rbac.Role{Spec: &v1.RoleSpec{}}))

				want := "creating configs not allowed for test-user"
				if exists {
					want = "updating config Topology/main-topo not allowed for test-user"
				}

				err := WorkflowUpsertConfig(httptest.NewRecorder(), req)

				var werr *weberror.WebError
				if !errors.As(err, &werr) || werr.Status != http.StatusForbidden || werr.Message != want {
					t.Fatalf("error = %v, want a 403 with message %q", err, want)
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
						t.Fatalf("WorkflowUpsertConfig() error = %v", err)
					}

					if rec.Code != http.StatusCreated {
						t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
					}

					if got := rec.Header().Get("Location"); got != "/api/v1/configs/topology/main-topo" {
						t.Errorf("Location = %q, want %q", got, "/api/v1/configs/topology/main-topo")
					}

					if rec.Body.Len() != 0 {
						t.Errorf("body = %q, want empty", rec.Body.String())
					}
				})
			}
		}
	}
}
