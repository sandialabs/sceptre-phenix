package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/api/disk"
	"phenix/store"
	"phenix/store/storetest"
	bdoc "phenix/types/builder"
	v1 "phenix/types/version/v1"
	"phenix/web/rbac"
)

// The users the tests make requests as.
const (
	builderAlice = "alice"
	builderBob   = "bob"
	builderCarol = "carol"
)

// builderConfigVerbs is every config verb.
var builderConfigVerbs = []string{"list", "get", "create", "update", "delete"} //nolint:gochecknoglobals // test fixture

// builderRole returns the role of a Builder user: the permission to use the
// Builder, and the given policies.
func builderRole(policies ...*v1.PolicySpec) *rbac.Role {
	return nonBuilderRole(append([]*v1.PolicySpec{builderPolicy([]string{"builder"}, nil, []string{"use"})}, policies...)...)
}

// nonBuilderRole returns a role holding exactly the given policies.
func nonBuilderRole(policies ...*v1.PolicySpec) *rbac.Role {
	return &rbac.Role{Spec: &v1.RoleSpec{Name: "test", Policies: policies}}
}

// builderPolicy returns one policy.
func builderPolicy(resources, names, verbs []string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: resources, ResourceNames: names, Verbs: verbs}
}

// builderFullRole may do everything, as the global-admin role config may.
func builderFullRole() *rbac.Role {
	return nonBuilderRole(builderPolicy([]string{"*", "*/*"}, []string{"*", "*/*"}, []string{"*"}))
}

// builderConfigPolicy holds the given verbs on every config and on the
// schemas and kinds of config the Builder reads.
func builderConfigPolicy(verbs []string) *v1.PolicySpec {
	return builderPolicy(
		[]string{"configs", "schemas", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		verbs,
	)
}

// builderUserRole returns a Builder user that holds every config verb, as
// the built-in Builder role does.
func builderUserRole() *rbac.Role {
	return builderRole(builderConfigPolicy(builderConfigVerbs))
}

// builderOnlyRole returns a role that may use the Builder and holds no
// config permission.
func builderOnlyRole() *rbac.Role {
	return builderRole()
}

// builderConfigsOnlyRole returns a role that holds every config verb but
// may not use the Builder.
func builderConfigsOnlyRole() *rbac.Role {
	return nonBuilderRole(builderConfigPolicy(builderConfigVerbs))
}

// experimentRole may use the Builder and do everything with configs, and may
// get only the named experiments, or every experiment when none is named.
func experimentRole(names ...string) *rbac.Role {
	if len(names) == 0 {
		names = []string{"*"}
	}

	return builderRole(
		builderPolicy([]string{"configs", "topologies", "scenarios"}, []string{"*", "*/*"}, builderConfigVerbs),
		builderPolicy([]string{"experiments"}, names, []string{"get"}),
	)
}

// builtinRole returns the built-in role of the given config name, from a new
// config store that stays in use until the test ends.
func builtinRole(t *testing.T, name string) *rbac.Role {
	t.Helper()

	storetest.UseBolt(t)

	created, err := config.CreateDefault("Role", name)
	if err != nil {
		t.Fatalf("creating the built-in %s role: %v", name, err)
	}

	role, err := rbac.RoleFromConfig(created.Metadata.Name)
	if err != nil {
		t.Fatalf("RoleFromConfig returned error: %v", err)
	}

	return role
}

// builtinBuilderRole returns the built-in Builder role (see [builtinRole]).
func builtinBuilderRole(t *testing.T) *rbac.Role {
	t.Helper()

	return builtinRole(t, "builder")
}

// reroute serves requests from newly registered routes over the same fakes,
// with the harness's options and then extra.
func (h *builderHarness) reroute(extra ...builderOption) {
	h.t.Helper()

	router, api := newBuilderRouter()

	options := slices.Concat([]builderOption{
		withBuilderService(h.service),
		withBuilderConfigs(h.listConfigs, h.getConfig),
		withBuilderPublishOps(h.publishOps()),
		func(api *builderAPI) {
			api.documentFiles = func() (string, []string) {
				h.fileReads++

				return h.files, []string{filepath.Join(h.files, "mounts")}
			}
		},
		withBuilderTemplateFiles(""),
	}, extra)

	if err := registerBuilderRoutes(api, options...); err != nil {
		h.t.Fatalf("registerBuilderRoutes returned error: %v", err)
	}

	h.router, h.api = router, api
}

// withBuilderService sets the draft service the handlers persist to.
func withBuilderService(service *bapi.Service) builderOption {
	return func(api *builderAPI) { api.drafts = service }
}

// withBuilderConfigs sets how the handlers list and get configs.
func withBuilderConfigs(
	list func(kind string) (store.Configs, error),
	get func(name string) (*store.Config, error),
) builderOption {
	return func(api *builderAPI) {
		api.listConfigs = list
		api.getConfig = get
	}
}

// withBuilderPublishOps sets the config and experiment writes of a publication.
func withBuilderPublishOps(ops builderPublishOps) builderOption {
	return func(api *builderAPI) { api.publish = ops }
}

// withBuilderTemplateFiles sets the template file directory. An empty directory reads none.
func withBuilderTemplateFiles(directory string) builderOption {
	return func(api *builderAPI) { api.templateFiles = directory }
}

// withBuilderDocumentFiles sets the Builder file directory and its excluded subdirectories.
func withBuilderDocumentFiles(root string, excluded ...string) builderOption {
	return func(api *builderAPI) {
		api.documentFiles = func() (string, []string) { return root, excluded }
	}
}

// withBuilderDisks sets how the server's disk images are listed.
func withBuilderDisks(list func() ([]disk.Details, error)) builderOption {
	return func(api *builderAPI) { api.disks = newBuilderDiskLister(list, time.Now) }
}

// withBuilderApps sets how the package routes list the apps of the server.
func withBuilderApps(list func() []string) builderOption {
	return func(api *builderAPI) { api.appNames = list }
}

// withBuilderPreflightSources sets where the preflight checks read the cluster from.
func withBuilderPreflightSources(sources builderPreflightSources) builderOption {
	return func(api *builderAPI) { api.preflight = sources }
}

// damageDraft adds an unknown field to a draft's record, so its metadata no
// longer decodes, and returns the record's new ETag.
func (h *builderHarness) damageDraft(id string) string {
	h.t.Helper()

	record, err := h.store.GetRecord(bapi.NamespaceDrafts, id)
	if err != nil {
		h.t.Fatalf("reading draft %s: %v", id, err)
	}

	value := append(bytes.TrimSuffix(record.Value, []byte("}")), `,"future":true}`...)

	damaged, err := h.store.UpdateRecord(bapi.NamespaceDrafts, id, value, record.Revision)
	if err != nil {
		h.t.Fatalf("damaging draft %s: %v", id, err)
	}

	return bapi.RevisionETag(damaged.Revision)
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

// mustDo makes the request as the caller and fails the test unless it is
// answered with status, for a step later lines depend on. What names it.
func (c builderCaller) mustDo(what string, request builderRequest, status int) *httptest.ResponseRecorder {
	c.harness.t.Helper()

	recorder := c.do(request)
	if recorder.Code != status {
		c.harness.t.Fatalf("%s: status = %d, want %d: %s", what, recorder.Code, status, recorder.Body)
	}

	return recorder
}

// doDecode is [builderHarness.doDecode] as the caller.
func (c builderCaller) doDecode(request builderRequest, status int, out any) {
	c.harness.t.Helper()

	request.user, request.role = c.user, c.role

	c.harness.doDecode(request, status, out)
}

// doDecode makes the request, fails the test unless it is answered with
// status, and decodes the answer into out unless it is nil.
func (h *builderHarness) doDecode(request builderRequest, status int, out any) *httptest.ResponseRecorder {
	h.t.Helper()

	recorder := h.do(request)
	if recorder.Code != status {
		h.t.Fatalf("%s %s: status = %d, want %d: %s", request.method, request.path, recorder.Code, status, recorder.Body)
	}

	if out != nil {
		h.decode(recorder, out)
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

// runBuilderAccessCases runs each case as a subtest against harness. Each
// case must leave harness unchanged.
func runBuilderAccessCases(t *testing.T, harness *builderHarness, cases []builderAccessCase) {
	t.Helper()

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.as(tt.user, tt.role).do(tt.request)
			if recorder.Code != tt.status {
				t.Errorf("%s %s: status = %d, want %d: %s",
					tt.request.method, tt.request.path, recorder.Code, tt.status, recorder.Body)
			}

			if tt.code == "" {
				return
			}

			if got := builderRefusal(t, recorder).Code; got != string(tt.code) {
				t.Errorf("%s %s: code = %q, want %q", tt.request.method, tt.request.path, got, tt.code)
			}
		})
	}
}

// builderJSONError reports an answer with a body that is not JSON. The security
// headers of every route are TestBuilderResponseHeaders's.
func builderJSONError(recorder *httptest.ResponseRecorder) error {
	if got := recorder.Header().Get("Content-Type"); recorder.Body.Len() != 0 && got != mimeJSON {
		return fmt.Errorf("Content-Type = %q, want %q", got, mimeJSON)
	}

	return nil
}

// builderStatusError reports an answer whose status is not status.
func builderStatusError(recorder *httptest.ResponseRecorder, status int) error {
	if recorder.Code != status {
		return fmt.Errorf("status = %d, want %d: %s", recorder.Code, status, recorder.Body)
	}

	return nil
}

// builderErrorBody is the answer of a Builder route to a refused request.
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

// decodeBuilderAnswer unmarshals a recorded answer body into target.
func decodeBuilderAnswer(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()

	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decoding response: %v (body %d bytes)", err, recorder.Body.Len())
	}
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

// builderJSONValue returns value as JSON decodes it: a deep copy of value to
// compare with cmp.Diff.
func builderJSONValue(t *testing.T, value any) any {
	t.Helper()

	var decoded any

	if err := json.Unmarshal([]byte(builderJSON(t, value)), &decoded); err != nil {
		t.Fatalf("decoding %T: %v", value, err)
	}

	return decoded
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

	config, err := store.DecodeConfigJSON([]byte(body))
	if err != nil {
		t.Fatalf("DecodeConfigJSON returned error: %v", err)
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
		Actor:      builderAlice,
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

// createDraft creates a draft of a small document named name as user.
func (h *builderHarness) createDraft(user, name string) builderDraftResponse {
	h.t.Helper()

	return h.postDraft(user, map[string]any{"title": name, "document": json.RawMessage(builderDocument(h.t, name))})
}

// createBuilderPublishDraft creates a draft of document as alice, imported
// from the source token when one is given.
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

	fields := map[string]any{"document": json.RawMessage(data)}
	if len(sourceToken) != 0 {
		fields["sourceToken"] = sourceToken[0]
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", body: builderJSON(t, fields), user: builderAlice,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST /builder/drafts: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	var draft builderDraftResponse

	decodeBuilderAnswer(t, recorder, &draft)

	return draft
}

// postDraft creates a draft from the fields of a create request as user.
func (h *builderHarness) postDraft(user string, fields map[string]any) builderDraftResponse {
	h.t.Helper()

	var draft builderDraftResponse

	h.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", body: builderJSON(h.t, fields), user: user,
	}, http.StatusCreated, &draft)

	return draft
}

// forkBuilderDraft creates, as user, a draft that forks the draft forkOf, and
// returns the answer.
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

// editBuilderDraft adds a device to the document, saves it as the draft's
// next snapshot, and returns the draft.
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
		path:   "/builder/drafts/" + draft.ID + "/snapshots",
		body:   `{"summary":"added ` + hostname + `","document":` + string(data) + `}`,
		user:   builderAlice, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("saving the edit: status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var edited builderDraftResponse
	decodeBuilderAnswer(t, recorder, &edited)

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
	id string,
) (builderDraftResponse, map[string]any) {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/drafts/" + id, user: user, role: role,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("getting draft %s as %s: status = %d: %s", id, user, recorder.Code, recorder.Body)
	}

	var (
		draft builderDraftResponse
		raw   map[string]any
	)

	decodeBuilderAnswer(t, recorder, &draft)
	decodeBuilderAnswer(t, recorder, &raw)

	return draft, raw
}

// publishBuilderDraft publishes the draft, expects status, and returns the
// answer, or the message of a refusal.
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
		path:   "/builder/drafts/" + draft.ID + "/publish",
		body:   body, user: builderAlice, role: role, ifMatch: draft.ETag,
	})
	if recorder.Code != status {
		t.Fatalf("publish %s: status = %d, want %d: %s", body, recorder.Code, status, recorder.Body.String())
	}

	var (
		response builderPublishResponse
		refusal  builderErrorBody
	)

	decodeBuilderAnswer(t, recorder, &response)
	decodeBuilderAnswer(t, recorder, &refusal)

	return response, refusal
}
