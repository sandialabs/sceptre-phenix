package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	bdoc "phenix/types/builder"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// builderShareConfigVerbs is every config verb.
var builderShareConfigVerbs = []string{"list", "get", "create", "update", "delete"} //nolint:gochecknoglobals // test fixture

// builderRole returns a role holding exactly the given policies.
func builderRole(policies ...*v1.PolicySpec) rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Name: "test", Policies: policies}}
}

// builderPolicy returns one policy.
func builderPolicy(resources, names, verbs []string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: resources, ResourceNames: names, Verbs: verbs}
}

// builderFullRole may do everything, including operating on the drafts of
// other users. It mirrors the wildcards the global-admin role config uses.
func builderFullRole() rbac.Role {
	return builderRole(builderPolicy([]string{"*", "*/*"}, []string{"*", "*/*"}, builderShareConfigVerbs))
}

// builderShareRole returns a role holding the given config verbs, and the
// given "builder-drafts" verbs on every draft. The config resources are
// enumerated so no wildcard grants "builder-drafts" by accident.
func builderShareRole(configVerbs []string, draftVerbs ...string) *rbac.Role {
	policies := []*v1.PolicySpec{builderPolicy(
		[]string{"configs", "schemas", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		configVerbs,
	)}

	if len(draftVerbs) != 0 {
		policies = append(policies, builderPolicy(
			[]string{builderDraftsResource}, []string{"*", "*/*"}, draftVerbs,
		))
	}

	role := builderRole(policies...)

	return &role
}

// builderConfigsRole holds every config verb and no permission on a
// resource of the Builder's own: it may do everything with its own drafts,
// icons and templates, and with another user's only what a share grants.
// Given an account, it may share its drafts and templates, but it may not
// publish templates to every user.
func builderConfigsRole() *rbac.Role {
	return builderShareRole(builderShareConfigVerbs)
}

// builderAllButConfigsRole holds verbs on every resource a Builder role may
// name but configs: every user's drafts, resource, the schemas and the kinds
// of config.
func builderAllButConfigsRole(resource string, verbs []string) *rbac.Role {
	role := builderRole(builderPolicy(
		[]string{builderDraftsResource, resource, "schemas", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		verbs,
	))

	return &role
}

// builderIconAdmin returns builderConfigsRole with builder-icons update and
// delete, which rename and delete any user's icon.
func builderIconAdmin() *rbac.Role {
	role := builderRole(append(
		slices.Clone(builderConfigsRole().Spec.Policies),
		builderPolicy([]string{"builder-icons"}, nil, []string{"update", "delete"}),
	)...)

	return &role
}

// builderPublisherRole may do everything, also publish templates to every
// user: the wildcards of the global-admin role config.
func builderPublisherRole() *rbac.Role {
	role := builderRole(builderPolicy([]string{"*", "*/*"}, []string{"*", "*/*"}, []string{"*"}))

	return &role
}

// publisherOnly holds the permission to publish templates and nothing else.
func publisherOnly() *rbac.Role {
	role := builderRole(builderPolicy([]string{"builder-templates"}, nil, []string{"publish"}))

	return &role
}

// experimentRole may do everything with configs and may get only the named
// experiments, or every experiment when none is named.
func experimentRole(names ...string) rbac.Role {
	if len(names) == 0 {
		names = []string{"*"}
	}

	return builderRole(
		builderPolicy([]string{"configs", "topologies", "scenarios"}, []string{"*", "*/*"}, builderShareConfigVerbs),
		builderPolicy([]string{"experiments"}, names, []string{"get"}),
	)
}

// builtinBuilderRole returns the built-in Builder role, as a new store
// holds it. The store, which holds nothing else, stays the config store
// until the test ends.
func builtinBuilderRole(t *testing.T) *rbac.Role {
	t.Helper()

	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	previous := store.DefaultStore
	store.DefaultStore = db //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store

	if _, err := config.CreateDefault("Role", "builder"); err != nil {
		t.Fatalf("creating the built-in Builder role: %v", err)
	}

	role, err := rbac.RoleFromConfig("Builder")
	if err != nil {
		t.Fatalf("RoleFromConfig returned error: %v", err)
	}

	return role
}

// builderShareUser returns the User config of an account created at created.
func builderShareUser(name, created string) store.Config {
	return store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "User",
		Metadata: store.ConfigMetadata{
			Name: name, Created: created, Updated: created, Annotations: nil,
		},
		Spec:   nil,
		Status: nil,
	}
}

// setUser gives the named user an account created at created, replacing
// any it had, as deleting a user and creating one of the same name does.
func (h *builderHarness) setUser(name, created string) {
	h.removeUser(name)
	h.configs = append(h.configs, builderShareUser(name, created))
}

// removeUser deletes the account of the named user.
func (h *builderHarness) removeUser(name string) {
	h.configs = slices.DeleteFunc(h.configs, func(config store.Config) bool {
		return config.Kind == "User" && config.Metadata.Name == name
	})
}

// builderCaller makes requests as one user holding one role.
type builderCaller struct {
	harness *builderHarness
	user    string
	role    *rbac.Role
}

// as returns a caller making requests as user holding role: the full role
// when role is nil, and no identity at all when user is "".
func (h *builderHarness) as(user string, role *rbac.Role) builderCaller {
	return builderCaller{harness: h, user: user, role: role}
}

// do makes the request as the caller.
func (c builderCaller) do(request builderRequest) *httptest.ResponseRecorder {
	c.harness.t.Helper()

	request.user, request.role = c.user, c.role

	return c.harness.do(request)
}

// expect makes the request as the caller and fails the test unless it is
// answered with status. What names the request in the failure.
func (c builderCaller) expect(what string, request builderRequest, status int) *httptest.ResponseRecorder {
	c.harness.t.Helper()

	recorder := c.do(request)
	if recorder.Code != status {
		c.harness.t.Fatalf("%s: status = %d, want %d: %s", what, recorder.Code, status, recorder.Body)
	}

	return recorder
}

// builderAccessCase is one row of a permission table: who makes a request,
// the request, and the status and code it is answered with.
type builderAccessCase struct {
	name string
	// user makes the request holding role: with no identity when user is "",
	// and with the full role when role is nil.
	user    string
	role    *rbac.Role
	request builderRequest
	status  int
	// code, unless it is "", is the code the answer carries.
	code bdoc.Code
}

// runBuilderAccessCases runs each case as a subtest of its own against
// harness, which every case leaves as it found it, as a refused request or
// a read does.
func runBuilderAccessCases(t *testing.T, harness *builderHarness, cases []builderAccessCase) {
	t.Helper()

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.as(tt.user, tt.role).do(tt.request)
			if recorder.Code != tt.status {
				t.Fatalf("%s %s: status = %d, want %d: %s",
					tt.request.method, tt.request.path, recorder.Code, tt.status, recorder.Body)
			}

			if tt.code == "" {
				return
			}

			if got := builderRefusal(t, recorder).Code; got != string(tt.code) {
				t.Fatalf("%s %s: code = %q, want %q", tt.request.method, tt.request.path, got, tt.code)
			}
		})
	}
}

// builderVerbCaller is a row of a permission matrix: a caller, and the
// status each request of the matrix answers it, in the order of the
// requests.
type builderVerbCaller struct {
	name string
	// role is the caller's, which has no identity at all when anonymous.
	role      *rbac.Role
	anonymous bool
	want      []int
}

// runBuilderVerbMatrix makes the requests setup returns as each caller, in
// a harness setup makes for that caller alone, and checks the status of
// each answer, and with check the answer itself.
func runBuilderVerbMatrix(
	t *testing.T,
	callers []builderVerbCaller,
	setup func(t *testing.T) (*builderHarness, []builderRequest),
	check func(t *testing.T, what string, recorder *httptest.ResponseRecorder),
) {
	t.Helper()

	for _, caller := range callers {
		t.Run(caller.name, func(t *testing.T) {
			harness, requests := setup(t)
			if len(requests) != len(caller.want) {
				t.Fatalf("%d requests, and %d statuses for them", len(requests), len(caller.want))
			}

			user := builderTestOwner
			if caller.anonymous {
				user = ""
			}

			for i, request := range requests {
				recorder := harness.as(user, caller.role).do(request)

				check(t, request.method, recorder)

				if recorder.Code != caller.want[i] {
					t.Errorf("%s %s: status = %d, want %d: %s",
						request.method, request.path, recorder.Code, caller.want[i], recorder.Body)
				}
			}
		})
	}
}

// builderErrorBody is what a Builder route answers a refused request with:
// the code of the refusal, its words and its cause, what it names, and the
// issues it is made of.
type builderErrorBody struct {
	Code     string            `json:"code"`
	Message  string            `json:"message"`
	Cause    string            `json:"cause"`
	Metadata map[string]string `json:"metadata"`
	Issues   []bdoc.Issue      `json:"issues"`
}

// builderRefusal returns what the answer to a refused request says.
func builderRefusal(t *testing.T, recorder *httptest.ResponseRecorder) builderErrorBody {
	t.Helper()

	var refusal builderErrorBody

	if err := json.Unmarshal(recorder.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decoding the refusal: %v: %s", err, recorder.Body)
	}

	return refusal
}

// builderMessage returns the message of an error response.
func builderMessage(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()

	var body weberror.WebError

	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the error: %v: %s", err, recorder.Body)
	}

	return body.Message
}

// builderJSON returns value encoded as JSON, as a request body is.
func builderJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}

	return string(data)
}

// builderConfig returns a minimal stored config of the given kind.
func builderConfig(t *testing.T, kind, name string) store.Config {
	t.Helper()

	body := `{
		"apiVersion": "phenix.sandia.gov/v1",
		"kind": "` + kind + `",
		"metadata": {"name": "` + name + `"},
		"spec": {"nodes": [], "vlans": {"aliases": {}}}
	}`

	config, err := store.NewConfigFromJSON([]byte(body))
	if err != nil {
		t.Fatalf("NewConfigFromJSON returned error: %v", err)
	}

	return *config
}

// builderDocument returns a small, valid builder document.
func builderDocument(t *testing.T, name string) []byte {
	t.Helper()

	document := bdoc.NewDocument(name)

	document.Nodes = append(document.Nodes, bdoc.Node{
		ID:       bdoc.NoteNodeID(name),
		Kind:     bdoc.NodeKindNote,
		Position: bdoc.Position{X: 0, Y: 0},
		Note:     &bdoc.Note{Text: name, Color: ""},
	})

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	return data
}

// builderPutDocument stores a published document of a target that no
// config references yet.
func builderPutDocument(t *testing.T, harness *builderHarness, kind, target, content string) *bapi.PublishedDocument {
	t.Helper()

	document, err := harness.service.PutPublishedDocument(t.Context(), bapi.PutPublishedDocumentRequest{
		Target:     target,
		Kind:       kind,
		Actor:      builderTestOwner,
		Document:   builderDocument(t, content),
		DraftID:    "",
		SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	return document
}

// builderPublish stores a published document and its target config,
// standing in for the publish endpoint exercised separately.
func builderPublish(t *testing.T, harness *builderHarness, target string) *bapi.PublishedDocument {
	t.Helper()

	document := builderPutDocument(t, harness, builderKindTopology, target, target)

	reference, err := document.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	config := builderConfig(t, builderKindTopology, target)
	config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
	harness.configs = append(harness.configs, config)

	return document
}

// createDraft creates a draft owned by the given user and returns it.
func (h *builderHarness) createDraft(user, name string) builderDraftResponse {
	h.t.Helper()

	recorder := h.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   `{"title":"` + name + `","document":` + string(builderDocument(h.t, name)) + `}`,
		user:   user,
	})

	if recorder.Code != http.StatusCreated {
		h.t.Fatalf("creating draft: status = %d, want %d", recorder.Code, http.StatusCreated)
	}

	var draft builderDraftResponse

	h.decode(recorder, &draft)

	return draft
}

// createBuilderPublishDraft creates a draft of document as alice, imported
// from the source token when one is given, and returns it.
func createBuilderPublishDraft(
	t *testing.T,
	harness *builderHarness,
	document *bdoc.Document,
	sourceToken ...string,
) builderDraftResponse {
	t.Helper()

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	request := map[string]any{"document": json.RawMessage(data)}
	if len(sourceToken) != 0 {
		request["sourceToken"] = sourceToken[0]
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   builderJSON(t, request),
		user:   builderTestOwner,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("creating publish draft: status %d: %s", recorder.Code, recorder.Body.String())
	}

	var draft builderDraftResponse
	harness.decode(recorder, &draft)

	return draft
}

// forkBuilderDraft creates a draft for the user that forks the draft named
// forkOf ("<owner>/<draft id>"), as saving the editor's history as a new
// draft does, and returns the answer.
func forkBuilderDraft(
	t *testing.T,
	harness *builderHarness,
	user string,
	role *rbac.Role,
	forkOf string,
	document *bdoc.Document,
) *httptest.ResponseRecorder {
	t.Helper()

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	return harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: user, role: role,
		body: builderJSON(t, map[string]any{"forkOf": forkOf, "document": json.RawMessage(data)}),
	})
}

// editBuilderDraft adds a device to the document and saves it as the draft's
// next snapshot, as an edit in the editor does, and returns the draft after
// it.
func editBuilderDraft(
	t *testing.T,
	harness *builderHarness,
	draft builderDraftResponse,
	document *bdoc.Document,
	hostname string,
) builderDraftResponse {
	t.Helper()

	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID(hostname), Kind: bdoc.NodeKindDevice, Label: hostname,
		Device: &bdoc.Device{Hostname: hostname, Spec: includeNode(hostname), Interfaces: []bdoc.InterfaceHandle{}},
	})

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/snapshots",
		body:   `{"summary":"added ` + hostname + `","document":` + string(data) + `}`,
		user:   builderTestOwner, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("saving the edit: status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var edited builderDraftResponse
	harness.decode(recorder, &edited)

	return edited
}

// mustDraftMeta returns a draft as stored.
func mustDraftMeta(t *testing.T, harness *builderHarness, id string) *bapi.DraftMetadata {
	t.Helper()

	meta, err := harness.service.GetDraft(t.Context(), id)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	return meta
}

// readBuilderDraftAs reads one draft as the user with the role (the full role
// when nil), as the JSON object the answer is and as its typed view.
func readBuilderDraftAs(
	t *testing.T,
	harness *builderHarness,
	user string,
	role *rbac.Role,
	owner, id string,
) (builderDraftResponse, map[string]any) {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/drafts/" + owner + "/" + id, user: user, role: role,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("getting draft %s/%s as %s: status = %d: %s", owner, id, user, recorder.Code, recorder.Body)
	}

	var (
		draft builderDraftResponse
		raw   map[string]any
	)

	harness.decode(recorder, &draft)
	harness.decode(recorder, &raw)

	return draft, raw
}

// publishBuilderDraft posts a publish intent for the draft, which must be
// answered with the given status, and returns the publish response, or for a
// refusal, which has none, the reason the server gave.
func publishBuilderDraft(
	t *testing.T,
	harness *builderHarness,
	draft builderDraftResponse,
	body string,
	status int,
) (builderPublishResponse, string) {
	t.Helper()

	response, refusal := publishBuilderDraftAs(t, harness, draft, nil, body, status)

	return response, refusal.Message
}

// publishBuilderDraftAs is [publishBuilderDraft] with the caller holding role
// (the full role when nil), returning all of a refusal.
func publishBuilderDraftAs(
	t *testing.T,
	harness *builderHarness,
	draft builderDraftResponse,
	role *rbac.Role,
	body string,
	status int,
) (builderPublishResponse, builderErrorBody) {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   body, user: builderTestOwner, role: role, ifMatch: draft.ETag,
	})
	if recorder.Code != status {
		t.Fatalf("publish %s: status = %d, want %d: %s", body, recorder.Code, status, recorder.Body.String())
	}

	var (
		response builderPublishResponse
		refusal  builderErrorBody
	)

	harness.decode(recorder, &response)
	harness.decode(recorder, &refusal)

	return response, refusal
}
