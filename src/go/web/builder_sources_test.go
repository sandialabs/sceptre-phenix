package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// builderSourceGroups is the grouped JSON view GET /builder/sources
// returns.
type builderSourceGroups struct {
	Topologies  []builderSourceResponse `json:"topologies"`
	Experiments []builderSourceResponse `json:"experiments"`
	Scenarios   []builderSourceResponse `json:"scenarios"`
	Images      []builderSourceResponse `json:"images"`
}

// listSources requests the source listing with the given role.
func builderListSources(
	t *testing.T,
	harness *builderHarness,
	role *rbac.Role,
) builderSourceGroups {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodGet,
		path:   "/builder/sources",
		user:   builderTestOwner,
		role:   role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var groups builderSourceGroups

	harness.decode(recorder, &groups)

	return groups
}

// builderSourceNames returns the full names of a source group.
func builderSourceNames(sources []builderSourceResponse) []string {
	names := make([]string, 0, len(sources))

	for _, source := range sources {
		names = append(names, source.FullName)
	}

	return names
}

func TestBuilderListSources(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, "Topology", "visible"),
		builderConfig(t, "Topology", "hidden"),
		builderConfig(t, "Experiment", "exp"),
		builderConfig(t, "Scenario", "scenario"),
		builderConfig(t, "Image", "image"),
	)

	// The role may only list one of the topologies, and holds no experiment or
	// scenario permission at all.
	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderPolicy([]string{"topologies"}, []string{"visible"}, []string{"list"}),
	)

	groups := builderListSources(t, harness, &role)

	if names := builderSourceNames(groups.Topologies); len(names) != 1 ||
		names[0] != "Topology/visible" {
		t.Errorf("topologies = %v, want only Topology/visible", names)
	}

	if len(groups.Experiments) != 0 {
		t.Errorf("experiments = %v, want none without the experiments permission",
			builderSourceNames(groups.Experiments))
	}

	if len(groups.Scenarios) != 0 {
		t.Errorf("scenarios = %v, want none without the scenarios permission",
			builderSourceNames(groups.Scenarios))
	}

	// Image configs have no kind specific vocabulary, so the config permission
	// alone admits them.
	if names := builderSourceNames(groups.Images); len(names) != 1 || names[0] != "Image/image" {
		t.Errorf("images = %v, want only Image/image", names)
	}

	if strings.Contains(recorderlessBody(t, harness, &role), "hidden") {
		t.Error("listing leaks a config the caller may not list")
	}
}

// recorderlessBody returns the raw source listing body for leak assertions.
func recorderlessBody(t *testing.T, harness *builderHarness, role *rbac.Role) string {
	t.Helper()

	return harness.do(builderRequest{
		method: http.MethodGet,
		path:   "/builder/sources",
		user:   builderTestOwner,
		role:   role,
	}).Body.String()
}

// TestBuilderListSourcesFull asserts every offered kind is reported for a
// caller holding the permissions for all of them.
func TestBuilderListSourcesFull(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, "Topology", "topo"),
		builderConfig(t, "Experiment", "exp"),
		builderConfig(t, "Scenario", "scenario"),
		builderConfig(t, "Image", "image"),
	)

	groups := builderListSources(t, harness, nil)

	checks := []struct {
		name        string
		sources     []builderSourceResponse
		want        string
		generatable bool
	}{
		{name: "topologies", sources: groups.Topologies, want: "Topology/topo", generatable: true},
		{name: "experiments", sources: groups.Experiments, want: "Experiment/exp", generatable: true},
		{name: "scenarios", sources: groups.Scenarios, want: "Scenario/scenario", generatable: false},
		{name: "images", sources: groups.Images, want: "Image/image", generatable: false},
	}

	for _, check := range checks {
		if len(check.sources) != 1 || check.sources[0].FullName != check.want {
			t.Errorf("%s = %v, want only %q",
				check.name, builderSourceNames(check.sources), check.want)

			continue
		}

		if check.sources[0].Generatable != check.generatable {
			t.Errorf("%s generatable = %t, want %t",
				check.name, check.sources[0].Generatable, check.generatable)
		}
	}
}

// TestBuilderListSourcesKindPermission asserts the kind specific list
// permission is required in addition to the config permission.
func TestBuilderListSourcesKindPermission(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, "Topology", "topo"),
		builderConfig(t, "Scenario", "allowed"),
		builderConfig(t, "Scenario", "denied"),
	)

	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderPolicy([]string{"scenarios"}, []string{"allowed"}, []string{"list"}),
	)

	groups := builderListSources(t, harness, &role)

	if names := builderSourceNames(groups.Scenarios); len(names) != 1 ||
		names[0] != "Scenario/allowed" {
		t.Errorf("scenarios = %v, want only Scenario/allowed", names)
	}

	if len(groups.Topologies) != 0 {
		t.Errorf("topologies = %v, want none without the topologies permission",
			builderSourceNames(groups.Topologies))
	}
}

// TestBuilderListSourcesIgnoresOtherKinds asserts kinds the builder does
// not offer are never reported.
func TestBuilderListSourcesIgnoresOtherKinds(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, "Topology", "topo"),
		builderConfig(t, "User", "someone"),
		builderConfig(t, "Role", "somerole"),
	)

	body := recorderlessBody(t, harness, nil)

	for _, unwanted := range []string{"User/", "Role/", "vlans"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("listing reports %q", unwanted)
		}
	}
}

func TestBuilderGenerateFromStoredSource(t *testing.T) {
	stored := builderConfig(t, "Topology", "topo")
	stored.Metadata.Annotations = store.Annotations{
		"builder-xml": "<mxGraphModel/>",
		"owner":       "alice",
	}
	harness := newBuilderHarness(t, stored)

	before := time.Now().UTC().Truncate(time.Second)
	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/generate",
		body:   `{"source":"Topology/topo"}`,
		user:   builderTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderGenerateResponse

	harness.decode(recorder, &response)

	document, err := bdoc.Decode(response.Document)
	if err != nil {
		t.Fatalf("decoding the generated document: %v", err)
	}

	// The source keeps the config's own annotations, and says when it was
	// imported.
	if want := map[string]string{"owner": "alice"}; !reflect.DeepEqual(document.Source.Annotations, want) {
		t.Errorf("annotations = %v, want %v", document.Source.Annotations, want)
	}

	imported, err := time.Parse(time.RFC3339, document.Source.ImportedAt)
	if err != nil || imported.Before(before) || imported.After(time.Now().UTC()) {
		t.Errorf("importedAt = %q, want the time of the request", document.Source.ImportedAt)
	}

	if response.Source.FullName != "Topology/topo" {
		t.Errorf("source = %q, want %q", response.Source.FullName, "Topology/topo")
	}
	if !response.Source.Stored {
		t.Error("stored source was not marked stored")
	}

	if response.Warnings == nil {
		t.Error("warnings = null, want an array")
	}

	// The generated document round-trips through the draft service, which is
	// the only thing the client can do with it.
	created := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts",
		body:   `{"sourceToken":"Topology/topo","document":` + string(response.Document) + `}`,
		user:   builderTestOwner,
	})

	if created.Code != http.StatusCreated {
		t.Fatalf("draft status = %d, want %d: %s", created.Code, http.StatusCreated, created.Body)
	}

	// Generating never writes a config.
	if len(harness.configs) != 1 || harness.configs[0].FullName() != stored.FullName() {
		t.Errorf("configs = %+v, want the single stored config unchanged", harness.configs)
	}
}

func TestBuilderGenerateFromUpload(t *testing.T) {
	harness := newBuilderHarness(t)

	uploads := []struct {
		name    string
		content string
	}{
		{
			name: "json",
			content: `{\"apiVersion\":\"phenix.sandia.gov/v1\",\"kind\":\"Topology\",` +
				`\"metadata\":{\"name\":\"uploaded\"},\"spec\":{\"nodes\":[]}}`,
		},
		{
			name: "yaml",
			content: `apiVersion: phenix.sandia.gov/v1\nkind: Topology\n` +
				`metadata:\n  name: uploaded\n  annotations:\n    builder-doc: x\n    owner: alice\n` +
				`spec:\n  nodes: []\n`,
		},
	}

	for _, upload := range uploads {
		t.Run(upload.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost,
				path:   "/builder/generate",
				body:   `{"content":"` + upload.content + `"}`,
				user:   builderTestOwner,
			})

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
			}

			var response builderGenerateResponse

			harness.decode(recorder, &response)

			if response.Source.Name != "uploaded" {
				t.Errorf("source = %q, want %q", response.Source.Name, "uploaded")
			}

			document, err := bdoc.Decode(response.Document)
			if err != nil {
				t.Fatalf("decoding the generated document: %v", err)
			}

			if upload.name == "yaml" && !reflect.DeepEqual(document.Source.Annotations, map[string]string{"owner": "alice"}) {
				t.Errorf("annotations = %v, want only owner", document.Source.Annotations)
			}
			if response.Source.Stored {
				t.Error("uploaded source was marked stored")
			}
		})
	}

	// Nothing was stored.
	if len(harness.configs) != 0 {
		t.Errorf("configs = %+v, want none", harness.configs)
	}
}

// TestBuilderGenerateUploadRequiresCreate asserts an uploaded config needs
// the permission POST /configs needs. Uploads are parsed with the same ${NAME}
// environment substitution, so a role that may only read configs, such as the
// default Global Viewer, must not be able to read the server's environment
// through a generated document. Generating from a stored config keeps needing
// only the read permission.
func TestBuilderGenerateUploadRequiresCreate(t *testing.T) { //nolint:paralleltest // sets an environment variable
	const secret = "builder-upload-secret-value"

	t.Setenv("PHENIX_BUILDER_TEST_SECRET", secret)

	harness := newBuilderHarness(t, builderConfig(t, "Topology", "topo"))

	var (
		// The list/get policy of api/config/default/global-viewer.yml.
		viewer = builderRole(builderPolicy(
			[]string{"*", "*/*"},
			[]string{"*", "*/*"},
			[]string{"list", "get"},
		))
		creator = builderRole(builderPolicy(
			[]string{"configs"},
			[]string{"*", "*/*"},
			[]string{"list", "get", "create"},
		))
		upload = `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Topology\n` +
			`metadata:\n  name: \"${PHENIX_BUILDER_TEST_SECRET}\"\nspec:\n  nodes: []\n"}`
	)

	tests := []struct {
		name   string
		role   rbac.Role
		body   string
		status int
	}{
		{name: "viewer upload", role: viewer, body: upload, status: http.StatusForbidden},
		{name: "viewer stored source", role: viewer, body: `{"source":"Topology/topo"}`, status: http.StatusOK},
		{name: "creator upload", role: creator, body: upload, status: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost,
				path:   "/builder/generate",
				body:   tt.body,
				user:   builderTestOwner,
				role:   &tt.role,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}

			if tt.status != http.StatusForbidden {
				return
			}

			if strings.Contains(recorder.Body.String(), secret) {
				t.Error("a refused upload disclosed the server environment")
			}

			// The refusal names the source as the Import dialog does.
			want := "importing a builder document from a config file not allowed for " + builderTestOwner
			if !strings.Contains(recorder.Body.String(), want) {
				t.Errorf("body = %s, want it to say %q", recorder.Body, want)
			}
		})
	}
}

func TestBuilderGenerateRequests(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, "Topology", "topo"),
		builderConfig(t, "Scenario", "scenario"),
		builderConfig(t, "Image", "image"),
	)

	// A message, where given, is what the Import dialog shows for a config
	// file: it names the file as the dialog's source does.
	tests := []struct {
		name    string
		body    string
		status  int
		message string
	}{
		{name: "neither", body: `{}`, status: http.StatusBadRequest},
		{
			name:   "both",
			body:   `{"source":"Topology/topo","content":"kind: Topology"}`,
			status: http.StatusBadRequest,
		},
		{name: "unnamed source", body: `{"source":"topo"}`, status: http.StatusBadRequest},
		{name: "unknown kind", body: `{"source":"Nope/topo"}`, status: http.StatusBadRequest},
		{name: "missing source", body: `{"source":"Topology/missing"}`, status: http.StatusNotFound},
		{
			name:   "scenario source",
			body:   `{"source":"Scenario/scenario"}`,
			status: http.StatusUnprocessableEntity,
		},
		{
			name:   "image source",
			body:   `{"source":"Image/image"}`,
			status: http.StatusUnprocessableEntity,
		},
		{
			name:    "unparsable config file",
			body:    `{"content":"\tnot: [valid"}`,
			status:  http.StatusUnprocessableEntity,
			message: "the config file is not valid JSON or YAML",
		},
		{
			name:    "config file of no known kind",
			body:    `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Nope\nmetadata:\n  name: lab\n"}`,
			status:  http.StatusUnprocessableEntity,
			message: "the config file is missing a known kind or a name",
		},
		{
			name:    "config file without a name",
			body:    `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nspec:\n  nodes: []\n"}`,
			status:  http.StatusUnprocessableEntity,
			message: "the config file is missing a known kind or a name",
		},
		{
			name:    "oversized config file",
			body:    `{"content":"` + strings.Repeat("x", bapi.MaxDocumentBytes+1) + `"}`,
			status:  http.StatusRequestEntityTooLarge,
			message: "the config file is larger than 5242880 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost,
				path:   "/builder/generate",
				body:   tt.body,
				user:   builderTestOwner,
			})

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}

			if tt.message == "" {
				return
			}

			var body struct {
				Message string `json:"message"`
			}

			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding the body: %v", err)
			}

			if body.Message != tt.message {
				t.Errorf("message = %q, want %q", body.Message, tt.message)
			}
		})
	}
}

// TestBuilderDocumentRoutePermissions asks the import and published
// document routes as roles that may or may not read what a request names.
// The kind specific list permission gates generation as it gates listing,
// so a config the caller cannot see is not reachable by naming it; a config
// the caller cannot read through /configs cannot be read through the
// Builder; and a published document of a config the caller may not read is
// answered as one that does not exist.
func TestBuilderDocumentRoutePermissions(t *testing.T) {
	harness := newBuilderHarness(t, builderConfig(t, "Topology", "topo"), builderConfig(t, "Topology", "secret"))
	document := builderPublish(t, harness, "hidden")

	var (
		// configs list and get on every config, and no topologies
		// permission.
		noKind = builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}))
		// configs list and get on a config that is not the one asked for.
		public = builderRole(builderPolicy([]string{"configs"}, []string{"Topology/public"}, []string{"list", "get"}))
	)

	generate := func(source string) builderRequest {
		return builderRequest{method: http.MethodPost, path: "/builder/generate", body: `{"source":"` + source + `"}`}
	}

	get := func(id string) builderRequest {
		return builderRequest{method: http.MethodGet, path: "/builder/documents/" + id}
	}

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{
			name: "generating without the kind's list permission", user: builderTestOwner, role: &noKind,
			request: generate("Topology/topo"), status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{
			name: "generating from a config the role may not read", user: builderTestOwner, role: &public,
			request: generate("Topology/secret"), status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{name: "generating with every permission", user: builderTestOwner, request: generate("Topology/topo"), status: http.StatusOK},
		{
			name: "a document of a config the role may not read", user: builderTestOwner, role: &public,
			request: get(document.ID), status: http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
		{
			name: "a document that does not exist", user: builderTestOwner, role: &public,
			request: get("missing"), status: http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
		{name: "a document with every permission", user: builderTestOwner, request: get(document.ID), status: http.StatusOK},
	})
}

// TestBuilderGenerateResolvesIncludes asserts included topologies are
// resolved from the store under the caller's own permissions: a topology the
// caller may not read, or a file path, is reported instead of shown.
func TestBuilderGenerateResolvesIncludes(t *testing.T) {
	node := func(hostname string) map[string]any {
		return map[string]any{
			"type":    "VirtualMachine",
			"general": map[string]any{"hostname": hostname},
			"network": map[string]any{
				"interfaces": []any{map[string]any{"name": "eth0", "vlan": "EXP"}},
			},
		}
	}

	root := builderConfig(t, "Topology", "root")
	root.Spec = map[string]any{
		"nodes":             []any{node("root-host")},
		"includeTopologies": []any{"visible", "secret", "../outside.yml"},
	}

	visible := builderConfig(t, "Topology", "visible")
	visible.Spec = map[string]any{"nodes": []any{node("visible-host")}}

	secret := builderConfig(t, "Topology", "secret")
	secret.Spec = map[string]any{"nodes": []any{node("secret-host")}}

	harness := newBuilderHarness(t, root, visible, secret)

	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"Topology/root", "Topology/visible"}, []string{"list", "get"}),
		builderPolicy([]string{"topologies"}, []string{"root", "visible", "secret"}, []string{"list"}),
	)

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/generate",
		body:   `{"source":"Topology/root"}`,
		user:   builderTestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderGenerateResponse

	harness.decode(recorder, &response)

	body := string(response.Document)
	if !strings.Contains(body, `"includedFrom":"visible"`) || strings.Contains(body, "secret-host") {
		t.Fatalf("document = %s, want visible-host included and secret-host left out", body)
	}

	for _, want := range []string{
		`included topology "secret" could not be read and its nodes are not shown: you are not allowed to read it`,
		`included topology "../outside.yml" could not be read and its nodes are not shown: ` +
			`the Builder reads included topologies from the config store only, not from files`,
		"Added 1 node from included topology visible (1 node).",
	} {
		if !strings.Contains(strings.Join(response.Warnings, "\n"), want) {
			t.Errorf("warnings = %q, want one containing %q", response.Warnings, want)
		}
	}
}

// builderGenerate posts body to /builder/generate as role (the full role
// when nil), which must answer 200, and returns the response and its
// document.
func builderGenerate(
	t *testing.T,
	harness *builderHarness,
	role *rbac.Role,
	body string,
) (builderGenerateResponse, *bdoc.Document) {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/generate", body: body, user: builderTestOwner, role: role,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("generate %s: status = %d, want %d: %s", body, recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderGenerateResponse

	harness.decode(recorder, &response)

	document, err := bdoc.Parse(response.Document)
	if err != nil {
		t.Fatalf("generate %s: the document is not valid: %v", body, err)
	}

	return response, document
}

// includeSourceFixture returns topology "root" with node root-host, which
// includes "visible" (visible-host), "secret" (secret-host) and a file, and
// a role that may read root and visible but not secret.
func includeSourceFixture(t *testing.T) ([]store.Config, rbac.Role) {
	t.Helper()

	root := builderConfig(t, builderKindTopology, "root")
	root.Spec = map[string]any{
		"nodes":             []any{includeNode("root-host")},
		"includeTopologies": []any{"visible", "secret", "../outside.yml"},
	}
	root.Metadata.Annotations = store.Annotations{"owner": "ops"}

	visible := builderConfig(t, builderKindTopology, "visible")
	visible.Spec = map[string]any{"nodes": []any{includeNode("visible-host")}}

	secret := builderConfig(t, builderKindTopology, "secret")
	secret.Spec = map[string]any{"nodes": []any{includeNode("secret-host")}}

	role := builderRole(
		builderPolicy(
			[]string{"configs"},
			[]string{"Topology/root", "Topology/visible"},
			[]string{"list", "get", "create"},
		),
		builderPolicy([]string{"topologies"}, []string{"root", "visible", "secret"}, []string{"list"}),
	)

	return []store.Config{root, visible, secret}, role
}

// TestBuilderGenerateChoices checks each rule a request's includes, copy
// and name are held to, in the order the handler applies them, with the
// status and the message of its refusal.
func TestBuilderGenerateChoices(t *testing.T) {
	const (
		badIncludes = `includes must be "keep" or "combine"`
		strayName   = `name is only used with copy or with includes "combine"`
		onlyTopo    = "only a topology can be combined or copied on import"
		nameRule    = ": use 1 to 512 letters, numbers, underscores, at signs, periods and hyphens"
		experiment  = `apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: lab\n` +
			`spec:\n  topology:\n    nodes: []\n`
	)

	configs, limited := includeSourceFixture(t)
	harness := newBuilderHarness(t, append(configs, builderConfig(t, kindExperiment, "exp"))...)

	tests := []struct {
		name    string
		body    string
		role    *rbac.Role
		status  int
		message string
		// code is the code of the refusal, when the case pins it.
		code bdoc.Code
	}{
		{
			name:   "an unknown field",
			body:   `{"source":"Topology/root","combine":true}`,
			status: http.StatusBadRequest, message: "request body is not a valid Builder request",
		},
		{
			name:   "source and content come first",
			body:   `{"includes":"flatten","name":"x"}`,
			status: http.StatusBadRequest, message: "exactly one of source and content is required",
		},
		{
			name:   "includes of another value",
			body:   `{"source":"Topology/root","includes":"flatten"}`,
			status: http.StatusBadRequest, message: badIncludes,
		},
		{
			name:   "includes is checked before the name",
			body:   `{"source":"Topology/root","includes":"Combine","name":"x"}`,
			status: http.StatusBadRequest, message: badIncludes,
		},
		{
			name:   "includes is checked before the source is read",
			body:   `{"source":"Topology/missing","includes":"all"}`,
			status: http.StatusBadRequest, message: badIncludes,
		},
		{
			name:   "a name without copy or combine",
			body:   `{"source":"Topology/root","name":"root-2"}`,
			status: http.StatusBadRequest, message: strayName,
		},
		{
			name:   "a name with includes kept",
			body:   `{"source":"Topology/root","includes":"keep","name":"root-2"}`,
			status: http.StatusBadRequest, message: strayName,
		},
		{
			name:   "a name with copy false",
			body:   `{"source":"Topology/missing","copy":false,"name":"root-2"}`,
			status: http.StatusBadRequest, message: strayName,
		},
		{
			name:   "the source is read before the choices are applied to it",
			body:   `{"source":"Topology/missing","copy":true,"name":"bad name"}`,
			status: http.StatusNotFound,
		},
		{
			name:   "a source the caller may not read",
			body:   `{"source":"Topology/secret","includes":"combine"}`,
			role:   &limited,
			status: http.StatusForbidden,
		},
		{
			name:   "a copy of an experiment",
			body:   `{"source":"Experiment/exp","copy":true}`,
			status: http.StatusUnprocessableEntity, message: onlyTopo,
		},
		{
			name:   "a combined experiment",
			body:   `{"source":"Experiment/exp","includes":"combine","name":"exp-2"}`,
			status: http.StatusUnprocessableEntity, message: onlyTopo,
		},
		{
			name:   "a combined experiment file, before its name is checked",
			body:   `{"content":"` + experiment + `","includes":"combine","name":"bad name"}`,
			status: http.StatusUnprocessableEntity, message: onlyTopo,
		},
		{
			name:   "a name with a space",
			body:   `{"source":"Topology/root","copy":true,"name":"root copy"}`,
			status: http.StatusUnprocessableEntity, message: `new topology name "root copy" is not allowed` + nameRule,
			code: bdoc.CodeImportNameInvalid,
		},
		{
			name:   "a name with a slash",
			body:   `{"source":"Topology/root","includes":"combine","name":"Topology/other"}`,
			status: http.StatusUnprocessableEntity, message: `new topology name "Topology/other" is not allowed` + nameRule,
			code: bdoc.CodeImportNameInvalid,
		},
		{
			name:    "a name of 513 bytes, shown cut",
			body:    `{"source":"Topology/root","copy":true,"name":"` + strings.Repeat("n", 513) + `"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "` + strings.Repeat("n", 64) + `..." is not allowed` + nameRule,
			code:    bdoc.CodeImportNameInvalid,
		},
		{
			name:    "the name of the stored topology",
			body:    `{"source":"Topology/root","copy":true,"name":"root"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "root" is the name of the imported topology: enter another name`,
			code:    bdoc.CodeImportNameSource,
		},
		{
			name:    "the name of the stored topology, combined",
			body:    `{"source":"Topology/root","includes":"combine","name":"root"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "root" is the name of the imported topology: enter another name`,
			code:    bdoc.CodeImportNameSource,
		},
		{
			name:   "a scenario file is refused for its kind, not for a name",
			body:   `{"content":"apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: a b\n","copy":true}`,
			status: http.StatusUnprocessableEntity, message: "Scenario configs cannot be opened in the builder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost, path: "/builder/generate", body: tt.body, user: builderTestOwner, role: tt.role,
			})
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}

			var refusal builderErrorBody

			harness.decode(recorder, &refusal)

			if tt.message != "" && refusal.Message != tt.message {
				t.Errorf("message = %q, want %q", refusal.Message, tt.message)
			}

			if tt.code != "" && refusal.Code != string(tt.code) {
				t.Errorf("code = %q, want %q", refusal.Code, tt.code)
			}
		})
	}

	// What is allowed: the default choice by either spelling, a name of 512
	// bytes, and for a config file the name the file has, since no stored
	// topology is linked to it.
	long := strings.Repeat("n", 512)
	file := `apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: plant\nspec:\n  nodes: []\n`

	for body, want := range map[string]string{
		`{"source":"Topology/root"}`:                                   "root",
		`{"source":"Topology/root","includes":""}`:                     "root",
		`{"source":"Topology/root","includes":"keep","copy":false}`:    "root",
		`{"source":"Topology/root","copy":true,"name":"` + long + `"}`: long,
		`{"content":"` + file + `","copy":true,"name":"plant"}`:        "plant",
		`{"content":"` + file + `","includes":"combine"}`:              "plant-combined",
		`{"content":"` + file + `","includes":"keep","copy":true}`:     "plant-copy",
	} {
		if _, document := builderGenerate(t, harness, nil, body); document.Metadata.Name != want {
			t.Errorf("%.80s: document name = %q, want %q", body, document.Metadata.Name, want)
		}
	}
}

// TestBuilderGenerateCopy asserts a copy is the document a plain import
// generates, under a new name and linked to no config: its included nodes
// stay read only, and the response still describes the topology that was
// read.
func TestBuilderGenerateCopy(t *testing.T) {
	configs, _ := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	_, plain := builderGenerate(t, harness, nil, `{"source":"Topology/root"}`)

	if plain.Source.Kind != bdoc.SourceKindTopology || plain.Source.Name != "root" || plain.Source.Digest == "" ||
		!reflect.DeepEqual(plain.Source.UnresolvedIncludes, []string{"../outside.yml"}) {
		t.Fatalf("a plain import's source = %+v, want the topology and the include it could not read", plain.Source)
	}

	for body, name := range map[string]string{
		`{"source":"Topology/root","copy":true}`:                                    "root-copy",
		`{"source":"Topology/root","copy":true,"includes":"keep","name":"site.v2"}`: "site.v2",
	} {
		response, document := builderGenerate(t, harness, nil, body)

		if document.Metadata.Name != name || document.Metadata.ID != bdoc.DocumentID(name) {
			t.Errorf("%s: name = %q, id = %q, want %q and its document ID", body, document.Metadata.Name, document.Metadata.ID, name)
		}

		source := document.Source
		if _, err := time.Parse(time.RFC3339, source.ImportedAt); err != nil {
			t.Errorf("%s: importedAt = %q, want the time of the import", body, source.ImportedAt)
		}

		want := &bdoc.Source{
			Kind:               bdoc.SourceKindManual,
			ImportedAt:         source.ImportedAt,
			IncludeTopologies:  []string{"visible", "secret", "../outside.yml"},
			UnresolvedIncludes: []string{"../outside.yml"},
			Warnings:           response.Warnings,
		}
		if !reflect.DeepEqual(source, want) {
			t.Errorf("%s: source = %s, want %s", body, builderJSON(t, source), builderJSON(t, want))
		}

		if !reflect.DeepEqual(response.Warnings, plain.Source.Warnings) {
			t.Errorf("%s: warnings = %q, want those of a plain import %q", body, response.Warnings, plain.Source.Warnings)
		}

		if !reflect.DeepEqual(document.Nodes, plain.Nodes) || !reflect.DeepEqual(document.Edges, plain.Edges) ||
			document.FindDevice("visible-host").Device.IncludedFrom != "visible" {
			t.Errorf("%s: nodes = %s, want those of a plain import", body, builderJSON(t, document.Nodes))
		}

		// The response names what was read, not the copy.
		if response.Source.FullName != "Topology/root" || !response.Source.Stored {
			t.Errorf("%s: source = %+v, want the stored topology root", body, response.Source)
		}
	}

	if harness.configWrites != 0 || len(harness.configs) != len(configs) {
		t.Errorf("a copy wrote %d configs and left %d, want none written", harness.configWrites, len(harness.configs))
	}
}

// TestBuilderGenerateCombine asserts a combined import copies the nodes of
// the included topologies the caller may read, and of no other: a topology
// the caller may not read, and a file, stay included by reference.
func TestBuilderGenerateCombine(t *testing.T) {
	configs, role := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	response, document := builderGenerate(t, harness, &role, `{"source":"Topology/root","includes":"combine"}`)

	if document.Metadata.Name != "root-combined" || document.Metadata.ID != bdoc.DocumentID("root-combined") {
		t.Errorf("name = %q, id = %q, want root-combined and its document ID", document.Metadata.Name, document.Metadata.ID)
	}

	if strings.Contains(string(response.Document), "secret-host") || strings.Contains(string(response.Document), "includedFrom") {
		t.Fatalf("document = %s, want no node of the secret topology and no node marked as included", response.Document)
	}

	if host := document.FindDevice("visible-host"); host == nil || document.FindDevice("root-host") == nil {
		t.Fatalf("document = %s, want root-host and visible-host", response.Document)
	}

	want := &bdoc.Source{
		Kind:              bdoc.SourceKindManual,
		ImportedAt:        document.Source.ImportedAt,
		IncludeTopologies: []string{"secret", "../outside.yml"},
		Warnings:          response.Warnings,
	}
	if !reflect.DeepEqual(document.Source, want) || document.Source.ImportedAt == "" {
		t.Errorf("source = %s, want %s", builderJSON(t, document.Source), builderJSON(t, want))
	}

	wantWarnings := []string{
		`included topology "secret" could not be read and its nodes are not shown: you are not allowed to read it`,
		`included topology "../outside.yml" could not be read and its nodes are not shown: ` +
			`the Builder reads included topologies from the config store only, not from files`,
		"Copied 1 node from included topology visible (1 node). They are ordinary nodes of this diagram now: " +
			"changes here do not reach that topology, and later changes there do not reach this diagram.",
		"Included topologies secret and ../outside.yml were not combined and stay in includeTopologies: " +
			"publishing keeps the references.",
	}
	if !reflect.DeepEqual(response.Warnings, wantWarnings) {
		t.Errorf("warnings = %q, want %q", response.Warnings, wantWarnings)
	}

	if response.Source.FullName != "Topology/root" || !response.Source.Stored {
		t.Errorf("source = %+v, want the stored topology root", response.Source)
	}

	// The topology the document projects holds both nodes and the includes
	// that were kept.
	topology, err := document.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology returned error: %v", err)
	}

	if nodes, _ := topology.Spec["nodes"].([]any); len(nodes) != 2 ||
		!reflect.DeepEqual(topology.Spec["includeTopologies"], []string{"secret", "../outside.yml"}) {
		t.Errorf("projected topology = %s, want two nodes and the two kept includes", builderJSON(t, topology.Spec))
	}

	// A caller who may read every include, and names the new topology.
	_, document = builderGenerate(t, harness, nil,
		`{"source":"Topology/root","includes":"combine","copy":true,"name":"site_all"}`)

	if document.Metadata.Name != "site_all" || document.FindDevice("secret-host") == nil ||
		!reflect.DeepEqual(document.Source.IncludeTopologies, []string{"../outside.yml"}) {
		t.Errorf("name = %q, source = %s, want site_all with secret-host and only the file still included",
			document.Metadata.Name, builderJSON(t, document.Source))
	}

	// A config file is combined the same way, from the store.
	content, err := json.Marshal(configs[0])
	if err != nil {
		t.Fatalf("encoding the topology: %v", err)
	}

	response, document = builderGenerate(t, harness, nil,
		builderJSON(t, map[string]string{"content": string(content), "includes": "combine"}))

	if document.Metadata.Name != "root-combined" || document.FindDevice("visible-host") == nil || response.Source.Stored {
		t.Errorf("name = %q, source = %+v, want root-combined made from the file", document.Metadata.Name, response.Source)
	}

	if harness.configWrites != 0 {
		t.Errorf("combining wrote %d configs, want none", harness.configWrites)
	}
}

// TestBuilderGenerateLegacyTopologyChoices asserts a topology the legacy
// Builder drew takes the same choices as any other: a copy and a combined
// import are converted from its diagram, and neither is linked to it.
func TestBuilderGenerateLegacyTopologyChoices(t *testing.T) {
	topology := legacySampleTopology(t)
	topology.Spec["includeTopologies"] = []any{"shared", "gone"}

	shared := builderConfig(t, builderKindTopology, "shared")
	shared.Spec = map[string]any{"nodes": []any{includeNode("inc-host")}}

	harness := newBuilderHarness(t, topology, shared)

	if groups := builderListSources(t, harness, nil); groups.Topologies[0].Name != "sample" ||
		groups.Topologies[0].Builder != bdoc.LegacyXMLAnnotation || groups.Topologies[0].IncludeCount != 2 {
		t.Fatalf("sources = %+v, want the legacy topology with its two includes", groups.Topologies)
	}

	response, copied := builderGenerate(t, harness, nil, `{"source":"Topology/sample","copy":true}`)

	if response.Source.Builder != bdoc.LegacyXMLAnnotation || copied.Metadata.Name != "sample-copy" ||
		copied.Source.Kind != bdoc.SourceKindManual || copied.Source.Name != "" || copied.Source.Digest != "" {
		t.Errorf("copy: response source = %+v, document source = %+v, want a legacy conversion linked to nothing",
			response.Source, copied.Source)
	}

	if got := legacyPosition(t, copied, "server-device-5"); got != (bdoc.Position{X: 1280, Y: 512}) {
		t.Errorf("copy: position = %+v, want the diagram's", got)
	}

	if host := copied.FindDevice("inc-host"); host == nil || host.Device.IncludedFrom != "shared" ||
		!reflect.DeepEqual(copied.Source.UnresolvedIncludes, []string{"gone"}) {
		t.Errorf("copy: source = %+v, want inc-host read only and the missing include listed", copied.Source)
	}

	_, combined := builderGenerate(t, harness, nil, `{"source":"Topology/sample","includes":"combine"}`)

	if got := legacyPosition(t, combined, "server-device-5"); got != (bdoc.Position{X: 1280, Y: 512}) {
		t.Errorf("combined: position = %+v, want the diagram's", got)
	}

	if host := combined.FindDevice("inc-host"); combined.Metadata.Name != "sample-combined" || host == nil ||
		host.Device.IncludedFrom != "" || !reflect.DeepEqual(combined.Source.IncludeTopologies, []string{"gone"}) ||
		combined.Source.Kind != bdoc.SourceKindManual {
		t.Errorf("combined: name = %q, source = %+v, want inc-host as its own node and only the missing include kept",
			combined.Metadata.Name, combined.Source)
	}

	for _, want := range []string{
		"Copied 1 node from included topology shared (1 node).",
		"Included topology gone was not combined and stays in includeTopologies: publishing keeps the reference.",
		legacyDecorations,
	} {
		if !slices.ContainsFunc(combined.Source.Warnings, func(warning string) bool { return strings.Contains(warning, want) }) {
			t.Errorf("combined: warnings = %q, want one containing %q", combined.Source.Warnings, want)
		}
	}

	// Neither draft replaces the legacy topology: publishing refuses it as
	// any draft that was not imported from the topology, and the diagram stays.
	for name, document := range map[string]*bdoc.Document{"copy": copied, "combined": combined} {
		draft := createBuilderPublishDraft(t, harness, document, "")

		_, refusal := publishBuilderDraft(t, harness, draft,
			`{"mode":"topology","topology":{"name":"sample","action":"update"}}`, http.StatusConflict)
		if refusal != "topology sample is not the source this draft was loaded from" {
			t.Errorf("%s: refusal = %q, want the draft refused as not loaded from the topology", name, refusal)
		}
	}

	if !harness.configs[0].HasAnnotation(bdoc.LegacyXMLAnnotation) || harness.configs[0].HasAnnotation(bapi.DocumentAnnotation) ||
		harness.configWrites != 0 {
		t.Errorf("annotations = %v after %d writes, want the legacy diagram untouched",
			harness.configs[0].Metadata.Annotations, harness.configWrites)
	}
}

// TestBuilderListSourcesIncludeCount asserts a topology's listing says how
// many topologies it includes, to a caller who may get the config and to no
// other, and says it of no other kind.
func TestBuilderListSourcesIncludeCount(t *testing.T) {
	configs, _ := includeSourceFixture(t)

	listed := builderConfig(t, builderKindTopology, "listed")
	listed.Spec = map[string]any{"nodes": []any{}, "includeTopologies": []any{"visible"}}

	experiment := builderConfig(t, kindExperiment, "exp")
	experiment.Spec = map[string]any{
		"topology": map[string]any{"nodes": []any{}, "includeTopologies": []any{"visible"}},
	}

	harness := newBuilderHarness(t, append(configs, listed, experiment)...)

	counts := func(sources []builderSourceResponse) map[string]int {
		byName := make(map[string]int, len(sources))
		for _, source := range sources {
			byName[source.Name] = source.IncludeCount
		}

		return byName
	}

	groups := builderListSources(t, harness, nil)

	want := map[string]int{"root": 3, "visible": 0, "secret": 0, "listed": 1}
	if got := counts(groups.Topologies); !reflect.DeepEqual(got, want) {
		t.Errorf("topology include counts = %v, want %v", got, want)
	}

	if got := counts(groups.Experiments); !reflect.DeepEqual(got, map[string]int{"exp": 0}) {
		t.Errorf("experiment include counts = %v, want none", got)
	}

	// A topology that includes nothing has no count at all.
	body := recorderlessBody(t, harness, nil)
	if got := strings.Count(body, `"includeCount"`); got != 2 || strings.Contains(body, `"includeCount":0`) {
		t.Errorf("listing has %d include counts, want one for root and one for listed: %s", got, body)
	}

	// The caller may list every topology, but get only root.
	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list"}),
		builderPolicy([]string{"configs"}, []string{"Topology/root"}, []string{"get"}),
		builderPolicy([]string{"topologies"}, []string{"*"}, []string{"list"}),
	)

	want = map[string]int{"root": 3, "visible": 0, "secret": 0, "listed": 0}
	if got := counts(builderListSources(t, harness, &role).Topologies); !reflect.DeepEqual(got, want) {
		t.Errorf("topology include counts for a caller who may get only root = %v, want %v", got, want)
	}
}

func TestBuilderListDocuments(t *testing.T) {
	harness := newBuilderHarness(t)
	visible := builderPublish(t, harness, "visible")
	hidden := builderPublish(t, harness, "hidden")

	role := builderRole(builderPolicy(
		[]string{"configs"},
		[]string{"Topology/visible"},
		[]string{"list", "get"},
	))

	recorder := harness.do(builderRequest{
		method: http.MethodGet,
		path:   "/builder/documents",
		user:   builderTestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.decode(recorder, &response)

	if len(response.Documents) != 1 || response.Documents[0].ID != visible.ID {
		t.Fatalf("documents = %+v, want only %q", response.Documents, visible.ID)
	}

	if response.Documents[0].Document != nil {
		t.Error("listing carries document bytes")
	}

	if strings.Contains(recorder.Body.String(), hidden.ID) {
		t.Error("listing leaks a document the caller may not read")
	}
}

// TestBuilderListDocumentsReadsEachKindOnce lists current documents among
// many stale and orphaned ones: the listing lists each kind of config once
// and gets none, and lists exactly the documents a get answers.
func TestBuilderListDocumentsReadsEachKindOnce(t *testing.T) {
	harness := newBuilderHarness(t)

	const leftovers = 20

	targets := []string{"a", "b", "c", "hidden"}
	documents := make([]*bapi.PublishedDocument, 0, len(targets)+1+2*leftovers)
	current := map[string]bool{}

	for _, target := range targets {
		document := builderPublish(t, harness, target)
		documents = append(documents, document)
		current[document.ID] = target != "hidden"
	}

	experiment := builderPutDocument(t, harness, kindExperiment, "exp", "exp")
	documents = append(documents, experiment)
	current[experiment.ID] = true

	reference, err := experiment.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	config := builderConfig(t, kindExperiment, "exp")
	config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
	harness.configs = append(harness.configs, config)

	for i := range leftovers {
		stale := builderPutDocument(t, harness, builderKindTopology, "a", fmt.Sprintf("a-%d", i))
		orphaned := builderPutDocument(t, harness, builderKindTopology, fmt.Sprintf("gone-%d", i), "gone")
		documents = append(documents, stale, orphaned)
	}

	role := builderRole(builderPolicy(
		[]string{"configs"},
		[]string{"Topology/a", "Topology/b", "Topology/c", "Topology/gone-*", "Experiment/exp"},
		[]string{"list", "get"},
	))

	harness.configLists, harness.configGets = nil, 0

	recorder := harness.do(builderRequest{
		method: http.MethodGet,
		path:   "/builder/documents",
		user:   builderTestOwner,
		role:   &role,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	lists := slices.Sorted(slices.Values(harness.configLists))
	if !slices.Equal(lists, []string{kindExperiment, builderKindTopology}) || harness.configGets != 0 {
		t.Fatalf("the listing listed %q and got %d configs, want each kind listed once and none got",
			harness.configLists, harness.configGets)
	}

	var response struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.decode(recorder, &response)

	listed := map[string]bool{}
	for _, document := range response.Documents {
		listed[document.ID] = true
	}

	for _, document := range documents {
		got := harness.do(builderRequest{
			method: http.MethodGet,
			path:   "/builder/documents/" + document.ID,
			user:   builderTestOwner,
			role:   &role,
		}).Code

		if listed[document.ID] != current[document.ID] || (got == http.StatusOK) != current[document.ID] {
			t.Errorf("document %s of %s: listed %t, get %d; want listed and got only when current and allowed",
				document.ID, document.Target, listed[document.ID], got)
		}
	}
}

func TestBuilderGetDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	document := builderPublish(t, harness, "topo")

	recorder := harness.do(builderRequest{
		method: http.MethodGet,
		path:   "/builder/documents/" + document.ID,
		user:   builderTestOwner,
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderDocumentResponse

	harness.decode(recorder, &response)

	if response.Config != "Topology/topo" {
		t.Errorf("config = %q, want %q", response.Config, "Topology/topo")
	}

	if !strings.Contains(string(response.Document), "topo") {
		t.Error("response does not carry the published document")
	}
}

// TestBuilderDeleteDocument deletes a published topology: the config
// the document is current for and every document of that topology are gone,
// and nothing of another topology is touched.
func TestBuilderDeleteDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	earlier := builderPutDocument(t, harness, builderKindTopology, "topo", "earlier")
	document := builderPublish(t, harness, "topo")
	other := builderPublish(t, harness, "other")

	remove := func() int {
		return harness.do(builderRequest{
			method: http.MethodDelete,
			path:   "/builder/documents/" + document.ID,
			user:   builderTestOwner,
		}).Code
	}

	if status := remove(); status != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", status, http.StatusNoContent)
	}

	if _, err := harness.getConfig("Topology/topo"); !errors.Is(err, store.ErrNotExist) {
		t.Fatalf("deleted topology: error = %v, want it gone", err)
	}

	for _, id := range []string{document.ID, earlier.ID} {
		if _, err := harness.service.GetPublishedDocument(t.Context(), id); !errors.Is(err, bapi.ErrNotFound) {
			t.Fatalf("document %s: error = %v, want it gone", id, err)
		}
	}

	if _, err := harness.getConfig("Topology/other"); err != nil {
		t.Fatalf("another topology was deleted: %v", err)
	}

	if _, err := harness.service.GetPublishedDocument(t.Context(), other.ID); err != nil {
		t.Fatalf("another topology's document was deleted: %v", err)
	}

	if status := remove(); status != http.StatusNotFound {
		t.Fatalf("deleting again: status = %d, want %d", status, http.StatusNotFound)
	}
}

// builderTestStore is a phenix store that keeps configs in a BoltDB and
// records in memory, so a test can fail the removal of a document's content.
type builderTestStore struct {
	*memrecord.Store
	builderTestConfigs
}

// builderTestConfigs gives builderTestStore its config methods. Embedded
// a level deeper than the record store, it never has its record methods
// promoted.
type builderTestConfigs struct{ store.Store }

// newBuilderStoreHarness returns a harness serving the Builder routes,
// and the /configs routes that list, get, create, update and delete a
// config, from a BoltDB phenix store of its own, where the config hooks run
// as they do in production. With records, the store keeps its records there
// instead.
func newBuilderStoreHarness(t *testing.T, records *memrecord.Store) *builderHarness {
	t.Helper()

	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	phenix := db
	if records != nil {
		phenix = &builderTestStore{Store: records, builderTestConfigs: builderTestConfigs{Store: db}}
	}

	previous := store.DefaultStore
	store.DefaultStore = phenix //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store

	service, err := bapi.New()
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	router, api := newBuilderRouter()
	api.Handle("/configs", weberror.ErrorHandler(GetConfigs)).Methods(http.MethodGet)
	api.Handle("/configs", weberror.ErrorHandler(CreateConfig)).Methods(http.MethodPost)
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(GetConfig)).Methods(http.MethodGet)
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(UpdateConfig)).Methods(http.MethodPut)
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(DeleteConfig)).Methods(http.MethodDelete)

	files := filepath.Join(t.TempDir(), "phenix")

	if err := registerBuilderRoutes(api, withBuilderDocumentFiles(files, filepath.Join(files, "mounts"))); err != nil {
		t.Fatalf("registerBuilderRoutes returned error: %v", err)
	}

	return &builderHarness{t: t, router: router, service: service, store: records, files: files}
}

// publishTopology stores two documents of target in the phenix store and the
// topology referencing the second, as publishing twice does.
func (h *builderHarness) publishTopology(target string) []*bapi.PublishedDocument {
	h.t.Helper()

	contents := []string{target + "-v1", target + "-v2"}
	documents := make([]*bapi.PublishedDocument, 0, len(contents))

	for _, content := range contents {
		document, err := h.service.PutPublishedDocument(h.t.Context(), bapi.PutPublishedDocumentRequest{
			Target: target, Kind: builderKindTopology, Actor: builderTestOwner,
			Document: builderDocument(h.t, content),
		})
		if err != nil {
			h.t.Fatalf("PutPublishedDocument returned error: %v", err)
		}

		documents = append(documents, document)
	}

	reference, err := documents[1].Reference().EncodeReference()
	if err != nil {
		h.t.Fatalf("EncodeReference returned error: %v", err)
	}

	config := builderConfig(h.t, builderKindTopology, target)
	config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}

	if err := store.Create(&config); err != nil {
		h.t.Fatalf("storing topology %s returned error: %v", target, err)
	}

	return documents
}

// assertDocumentsGone fails unless none of the published documents is stored.
func (h *builderHarness) assertDocumentsGone(documents []*bapi.PublishedDocument) {
	h.t.Helper()

	for _, document := range documents {
		if _, err := h.service.GetPublishedDocument(h.t.Context(), document.ID); !errors.Is(err, bapi.ErrNotFound) {
			h.t.Errorf("document %s of %s: error = %v, want it gone", document.ID, document.Target, err)
		}
	}
}

// renameBody returns the body of the PUT /configs that renames the stored
// topology name as renamed, keeping everything else it holds.
func (h *builderHarness) renameBody(name, renamed string) string {
	h.t.Helper()

	stored, err := config.Get(builderKindTopology+"/"+name, false)
	if err != nil {
		h.t.Fatalf("getting topology %s returned error: %v", name, err)
	}

	stored.Metadata.Name = renamed

	body, err := json.Marshal(stored)
	if err != nil {
		h.t.Fatalf("encoding topology %s returned error: %v", name, err)
	}

	return string(body)
}

// TestBuilderDocumentsDeletedWithTopology deletes published topologies
// from a BoltDB phenix store, through DELETE /configs and through the Builder
// route, which has the Topology config hook leave the documents to it:
// either way the documents are gone, and the route answers without a warning.
func TestBuilderDocumentsDeletedWithTopology(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	harness := newBuilderStoreHarness(t, nil)

	viaConfigs := harness.publishTopology("via-configs")
	viaBuilder := harness.publishTopology("via-builder")
	kept := harness.publishTopology("kept")

	recorder := harness.do(builderRequest{
		method: http.MethodDelete, path: "/configs/topology/via-configs", user: builderTestOwner,
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE /configs: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	harness.assertDocumentsGone(viaConfigs)

	recorder = harness.do(builderRequest{
		method: http.MethodDelete, path: "/builder/documents/" + viaBuilder[1].ID, user: builderTestOwner,
	})
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Warning") != "" {
		t.Fatalf("DELETE /builder/documents: status = %d, warning %q; want %d and none: %s",
			recorder.Code, recorder.Header().Get("Warning"), http.StatusNoContent, recorder.Body)
	}

	harness.assertDocumentsGone(viaBuilder)

	for _, name := range []string{"Topology/via-configs", "Topology/via-builder"} {
		if _, err := config.Get(name, false); !errors.Is(err, store.ErrNotExist) {
			t.Errorf("%s: error = %v, want it deleted", name, err)
		}
	}

	if _, _, err := harness.service.GetPublishedDocumentData(t.Context(), kept[1].ID); err != nil {
		t.Errorf("another topology's document: %v, want it kept", err)
	}
}

// TestBuilderDeleteDocumentWarnsWithConfigHook deletes a published topology
// through the Builder route, with the real config delete and its Topology
// config hook, when the documents' content cannot be removed: the topology is
// gone, and the answer carries the warning, which the hook alone would only
// log.
func TestBuilderDeleteDocumentWarnsWithConfigHook(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	records := memrecord.New()
	harness := newBuilderStoreHarness(t, records)
	documents := harness.publishTopology("topo")

	records.FailPrefixDelete = func(string, string) error { return errors.New("injected content delete failure") }

	recorder := harness.do(builderRequest{
		method: http.MethodDelete, path: "/builder/documents/" + documents[1].ID, user: builderTestOwner,
	})

	const warning = `199 phenix "delete published topology succeeded but removing superseded content failed"`

	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Warning") != warning {
		t.Fatalf("status = %d, warning %q; want %d and %q: %s",
			recorder.Code, recorder.Header().Get("Warning"), http.StatusNoContent, warning, recorder.Body)
	}

	if _, err := config.Get("Topology/topo", false); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("topology: error = %v, want it deleted", err)
	}

	harness.assertDocumentsGone(documents)

	if records.Count(bapi.NamespaceChunks) == 0 {
		t.Error("no content is left: the injected failure did not fail its removal")
	}

	// The hook removes the documents of a topology deleted any other way.
	records.FailPrefixDelete = nil
	other := harness.publishTopology("other")

	recorder = harness.do(builderRequest{
		method: http.MethodDelete, path: "/configs/topology/other", user: builderTestOwner,
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE /configs: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	harness.assertDocumentsGone(other)
}

// TestBuilderDocumentsDeletedWithRenamedTopology renames a published
// topology with PUT /configs, which stores it under the new name and deletes
// it under the old one: the documents of the old name go, and the renamed
// topology names none, so it is a topology like any other until it is
// published again.
func TestBuilderDocumentsDeletedWithRenamedTopology(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	harness := newBuilderStoreHarness(t, nil)

	documents := harness.publishTopology("topo")
	kept := harness.publishTopology("kept")

	recorder := harness.do(builderRequest{
		method: http.MethodPut, path: "/configs/topology/topo", user: builderTestOwner,
		body: harness.renameBody("topo", "renamed"), contentType: "application/json",
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("PUT /configs: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if _, err := config.Get("Topology/topo", false); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("topology under its old name: error = %v, want it gone", err)
	}

	renamed, err := config.Get("Topology/renamed", false)
	if err != nil {
		t.Fatalf("getting the renamed topology returned error: %v", err)
	}

	if renamed.HasAnnotation(bapi.DocumentAnnotation) {
		t.Errorf("the renamed topology names a document: %q", renamed.Metadata.Annotations[bapi.DocumentAnnotation])
	}

	harness.assertDocumentsGone(documents)

	// A PUT that keeps the name keeps the topology published.
	recorder = harness.do(builderRequest{
		method: http.MethodPut, path: "/configs/topology/kept", user: builderTestOwner,
		body: harness.renameBody("kept", "kept"), contentType: "application/json",
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("PUT /configs: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	recorder = harness.do(builderRequest{method: http.MethodGet, path: "/builder/documents", user: builderTestOwner})

	var listed struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.decode(recorder, &listed)

	if len(listed.Documents) != 1 || listed.Documents[0].ID != kept[1].ID {
		t.Errorf("listed documents = %+v, want only %s of the topology kept", listed.Documents, kept[1].ID)
	}

	sources := harness.do(builderRequest{method: http.MethodGet, path: "/builder/sources", user: builderTestOwner})

	var groups builderSourceGroups

	harness.decode(sources, &groups)

	for _, source := range groups.Topologies {
		if want := map[string]string{"kept": bapi.DocumentAnnotation, "renamed": ""}[source.Name]; source.Builder != want {
			t.Errorf("source %s builder = %q, want %q", source.Name, source.Builder, want)
		}
	}
}

// TestConfigRoutesWaitForBuilderPublishing asserts that deleting a topology
// and renaming one through /configs wait for the Builder publish lock, so
// no publication of this process is in flight while the topology's documents
// are removed. A config of another kind does not wait.
func TestConfigRoutesWaitForBuilderPublishing(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	harness := newBuilderStoreHarness(t, nil)

	harness.publishTopology("deleted")
	harness.publishTopology("topo")

	scenario := builderConfig(t, "Scenario", "scenario")
	if err := store.Create(&scenario); err != nil {
		t.Fatalf("storing the scenario returned error: %v", err)
	}

	for _, test := range []struct {
		name    string
		request builderRequest
		waits   bool
	}{
		{
			name:    "deleting a topology",
			request: builderRequest{method: http.MethodDelete, path: "/configs/topology/deleted"},
			waits:   true,
		},
		{
			name: "renaming a topology",
			request: builderRequest{
				method: http.MethodPut, path: "/configs/topology/topo",
				body: harness.renameBody("topo", "renamed"), contentType: "application/json",
			},
			waits: true,
		},
		{
			name: "updating a topology",
			request: builderRequest{
				method: http.MethodPut, path: "/configs/topology/renamed",
				body: harness.renameBody("topo", "renamed"), contentType: "application/json",
			},
		},
		{
			name:    "deleting a scenario",
			request: builderRequest{method: http.MethodDelete, path: "/configs/scenario/scenario"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.request.user = builderTestOwner
			answered := make(chan *httptest.ResponseRecorder, 1)

			builderPublishLock.Lock()

			go func() { answered <- harness.do(test.request) }()

			// A request that waits gets a moment to answer all the same; one
			// that does not wait gets as long as a slow machine may need.
			patience := time.Minute
			if test.waits {
				patience = 250 * time.Millisecond
			}

			var recorder *httptest.ResponseRecorder

			select {
			case recorder = <-answered:
				builderPublishLock.Unlock()

				if test.waits {
					t.Fatalf("answered %d while a publication held the lock", recorder.Code)
				}
			case <-time.After(patience):
				builderPublishLock.Unlock()

				if !test.waits {
					t.Fatal("waited for the publish lock")
				}

				recorder = <-answered
			}

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
			}
		})
	}
}

// TestBuilderDeleteDocumentRefusals asserts a published topology is
// deleted only with configs delete for it, only while its document is
// current, and a published experiment never.
func TestBuilderDeleteDocumentRefusals(t *testing.T) {
	readers := builderRole(builderPolicy(
		[]string{"configs"}, []string{"*/*"}, []string{"list", "get"},
	))
	otherDeleter := builderRole(builderPolicy(
		[]string{"configs"}, []string{"*/*"}, []string{"list", "get"},
	), builderPolicy(
		[]string{"configs"}, []string{"Topology/other"}, []string{"delete"},
	))
	hidden := builderRole(builderPolicy(
		[]string{"configs"}, []string{"Topology/public"}, []string{"list", "get", "delete"},
	))

	for _, test := range []struct {
		name   string
		role   *rbac.Role
		stale  bool
		kind   string
		status int
		reason string
	}{
		{name: "no configs delete", role: &readers, status: http.StatusForbidden},
		{name: "configs delete for another topology", role: &otherDeleter, status: http.StatusForbidden},
		{name: "hidden", role: &hidden, status: http.StatusNotFound},
		{name: "published again since", stale: true, status: http.StatusNotFound},
		{
			name: "experiment", kind: kindExperiment, status: http.StatusUnprocessableEntity,
			reason: "Only published topologies can be deleted here. Delete experiments from the Experiments page.",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderHarness(t)
			kind := builderKindTopology

			if test.kind != "" {
				kind = test.kind
			}

			document := builderPutDocument(t, harness, kind, "topo", "topo")
			current := document

			if test.stale {
				current = builderPutDocument(t, harness, kind, "topo", "republished")
			}

			reference, err := current.Reference().EncodeReference()
			if err != nil {
				t.Fatalf("EncodeReference returned error: %v", err)
			}

			config := builderConfig(t, kind, "topo")
			config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: reference}
			harness.configs = append(harness.configs, config)

			recorder := harness.do(builderRequest{
				method: http.MethodDelete,
				path:   "/builder/documents/" + document.ID,
				user:   builderTestOwner,
				role:   test.role,
			})

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body)
			}

			var refusal struct {
				Message string `json:"message"`
			}

			harness.decode(recorder, &refusal)

			if test.reason != "" && refusal.Message != test.reason {
				t.Errorf("message = %q, want %q", refusal.Message, test.reason)
			}

			if _, err := harness.getConfig(config.FullName()); err != nil {
				t.Errorf("a refused delete deleted %s: %v", config.FullName(), err)
			}

			if _, err := harness.service.GetPublishedDocument(t.Context(), document.ID); err != nil {
				t.Errorf("a refused delete deleted document %s: %v", document.ID, err)
			}
		})
	}
}
