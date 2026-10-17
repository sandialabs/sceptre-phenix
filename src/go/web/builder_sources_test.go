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
	"phenix/store/storetest"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// builderSourceGroups is the grouped JSON view GET /builder/sources returns.
type builderSourceGroups struct {
	Topologies  []builderSourceResponse `json:"topologies"`
	Experiments []builderSourceResponse `json:"experiments"`
	Scenarios   []builderSourceResponse `json:"scenarios"`
	Images      []builderSourceResponse `json:"images"`
}

// builderListSources lists the sources as alice holding role, and returns the
// listing and its raw answer.
func builderListSources(t *testing.T, harness *builderHarness, role *rbac.Role) builderSourceGroups {
	t.Helper()

	groups, _ := builderListSourcesBody(t, harness, role)

	return groups
}

// builderListSourcesBody is [builderListSources], also returning the raw answer.
func builderListSourcesBody(t *testing.T, harness *builderHarness, role *rbac.Role) (builderSourceGroups, string) {
	t.Helper()

	var groups builderSourceGroups

	recorder := harness.doDecode(builderRequest{
		method: http.MethodGet, path: "/builder/sources", user: builderAlice, role: role,
	}, http.StatusOK, &groups)

	return groups, recorder.Body.String()
}

// builderSourceNames returns the full names of a source group.
func builderSourceNames(sources []builderSourceResponse) []string {
	names := make([]string, 0, len(sources))

	for _, source := range sources {
		names = append(names, source.FullName)
	}

	return names
}

// TestBuilderListSources asserts a source is listed only with configs list
// and, for a kind that has one, the kind's own list permission.
func TestBuilderListSources(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, "Topology", "visible"),
		builderConfig(t, "Topology", "hidden"),
		builderConfig(t, "Experiment", "exp"),
		builderConfig(t, "Scenario", "allowed"),
		builderConfig(t, "Scenario", "denied"),
		builderConfig(t, "Image", "image"),
	)

	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderPolicy([]string{"topologies"}, []string{"visible"}, []string{"list"}),
		builderPolicy([]string{"scenarios"}, []string{"allowed"}, []string{"list"}),
	)

	groups, body := builderListSourcesBody(t, harness, role)
	got := [][]string{
		builderSourceNames(groups.Topologies), builderSourceNames(groups.Experiments),
		builderSourceNames(groups.Scenarios), builderSourceNames(groups.Images),
	}

	// Image configs have no kind specific permission, so configs list alone admits them.
	want := [][]string{{"Topology/visible"}, {}, {"Scenario/allowed"}, {"Image/image"}}
	if !reflect.DeepEqual(got, want) || strings.Contains(body, "hidden") || strings.Contains(body, "denied") {
		t.Errorf("sources = %v, want %v and no other: %s", got, want, body)
	}
}

// TestBuilderListSourcesFull asserts every offered kind is listed for a
// caller holding every permission, and no other kind is.
func TestBuilderListSourcesFull(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, "Topology", "topo"),
		builderConfig(t, "Experiment", "exp"),
		builderConfig(t, "Scenario", "scenario"),
		builderConfig(t, "Image", "image"),
		builderConfig(t, "User", "someone"),
		builderConfig(t, "Role", "somerole"),
	)

	groups, body := builderListSourcesBody(t, harness, nil)

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
		if len(check.sources) != 1 || check.sources[0].FullName != check.want ||
			check.sources[0].Generatable != check.generatable {
			t.Errorf("%s = %+v, want only %q, generatable %t", check.name, check.sources, check.want, check.generatable)
		}
	}

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
		"team":        "ops",
	}
	harness := newBuilderHarness(t, stored)

	before := time.Now().UTC().Truncate(time.Second)
	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/generate",
		body:   `{"source":"Topology/topo"}`,
		user:   builderAlice,
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

	// The source keeps the config's own annotations, and says when it was imported.
	if want := map[string]string{"team": "ops"}; !reflect.DeepEqual(document.Source.Annotations, want) {
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

	// The generated document round-trips through the draft service.
	harness.postDraft(builderAlice, map[string]any{"sourceToken": "Topology/topo", "document": response.Document})

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
				`metadata:\n  name: uploaded\n  annotations:\n    builder-doc: x\n    team: ops\n` +
				`spec:\n  nodes: []\n`,
		},
	}

	for _, upload := range uploads {
		t.Run(upload.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost,
				path:   "/builder/generate",
				body:   `{"content":"` + upload.content + `"}`,
				user:   builderAlice,
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

			if upload.name == "yaml" && !reflect.DeepEqual(document.Source.Annotations, map[string]string{"team": "ops"}) {
				t.Errorf("annotations = %v, want only team", document.Source.Annotations)
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

// TestBuilderGenerateUploadRequiresCreate asserts an upload, parsed with
// ${NAME} substitution, needs configs create so a viewer cannot read the environment.
func TestBuilderGenerateUploadRequiresCreate(t *testing.T) {
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
		role   *rbac.Role
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
				user:   builderAlice,
				role:   tt.role,
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
			want := "importing a builder document from a config file not allowed for " + builderAlice
			if !strings.Contains(recorder.Body.String(), want) {
				t.Errorf("body = %s, want it to say %q", recorder.Body, want)
			}
		})
	}
}

// TestBuilderDocumentRoutePermissions asserts a config the role may not list
// or read cannot be imported, and its published document is answered as missing.
func TestBuilderDocumentRoutePermissions(t *testing.T) {
	harness := newBuilderHarness(t, builderConfig(t, "Topology", "topo"), builderConfig(t, "Topology", "secret"))
	document := builderPublish(t, harness, "hidden")

	var (
		// configs list and get on every config, and no topologies permission.
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
			name: "generating without the kind's list permission", user: builderAlice, role: noKind,
			request: generate("Topology/topo"), status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{
			name: "generating from a config the role may not read", user: builderAlice, role: public,
			request: generate("Topology/secret"), status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{
			name: "a document of a config the role may not read", user: builderAlice, role: public,
			request: get(document.ID), status: http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
		{
			name: "a document that does not exist", user: builderAlice, role: public,
			request: get("missing"), status: http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
	})
}

// TestBuilderGenerateResolvesIncludes asserts included topologies are read
// under the caller's permissions: one it may not read, or a file, is reported.
func TestBuilderGenerateResolvesIncludes(t *testing.T) {
	configs, role := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	response, _ := builderGenerate(t, harness, role, `{"source":"Topology/root"}`)

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
		if !slices.ContainsFunc(response.Warnings, func(warning string) bool { return strings.HasPrefix(warning, want) }) {
			t.Errorf("warnings = %q, want %q", response.Warnings, want)
		}
	}
}

// builderGenerate posts body to /builder/generate as role (nil for the full
// role), expects 200, and returns the response and its document.
func builderGenerate(
	t *testing.T,
	harness *builderHarness,
	role *rbac.Role,
	body string,
) (builderGenerateResponse, *bdoc.Document) {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/generate", body: body, user: builderAlice, role: role,
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

// includeSourceFixture returns topology root, including visible, secret and a
// file, and a role that may read root and visible but not secret.
func includeSourceFixture(t *testing.T) ([]store.Config, *rbac.Role) {
	t.Helper()

	root := builderConfig(t, builderKindTopology, "root")
	root.Spec = map[string]any{
		"nodes":             []any{includeNode("root-host")},
		"includeTopologies": []any{"visible", "secret", "../outside.yml"},
	}
	root.Metadata.Annotations = store.Annotations{"team": "ops"}

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

// TestBuilderGenerateRequests checks the status, message and code of each
// refused import request.
func TestBuilderGenerateRequests(t *testing.T) {
	const (
		badIncludes = `includes must be "keep" or "combine"`
		strayName   = `name is only used with copy or with includes "combine"`
		onlyTopo    = "only a topology can be combined or copied on import"
		nameRule    = ": use 1 to 512 letters, numbers, underscores, at signs, periods and hyphens"
		kindOrName  = "the config file is missing a known kind or a name"
	)

	configs, limited := includeSourceFixture(t)
	harness := newBuilderHarness(t, append(configs,
		builderConfig(t, kindExperiment, "exp"),
		builderConfig(t, "Scenario", "scenario"),
		builderConfig(t, "Image", "image"),
	)...)

	tests := []struct {
		name    string
		body    string
		role    *rbac.Role
		status  int
		message string
		code    bdoc.Code
	}{
		{
			name: "an unknown field", body: `{"source":"Topology/root","combine":true}`,
			status: http.StatusBadRequest, message: "request body is not a valid Builder request",
		},
		{
			name: "neither source nor content", body: `{"includes":"flatten","name":"x"}`,
			status: http.StatusBadRequest, message: "exactly one of source and content is required",
		},
		{name: "both", body: `{"source":"Topology/root","content":"kind: Topology"}`, status: http.StatusBadRequest},
		{name: "unnamed source", body: `{"source":"topo"}`, status: http.StatusBadRequest},
		{name: "unknown kind", body: `{"source":"Nope/topo"}`, status: http.StatusBadRequest},
		{name: "missing source", body: `{"source":"Topology/missing"}`, status: http.StatusNotFound},
		{name: "scenario source", body: `{"source":"Scenario/scenario"}`, status: http.StatusUnprocessableEntity},
		{name: "image source", body: `{"source":"Image/image"}`, status: http.StatusUnprocessableEntity},
		{
			name: "unparsable config file", body: `{"content":"\tnot: [valid"}`,
			status: http.StatusUnprocessableEntity, message: "the config file is not valid JSON or YAML",
		},
		{
			name:    "config file of no known kind",
			body:    `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Nope\nmetadata:\n  name: lab\n"}`,
			status:  http.StatusUnprocessableEntity,
			message: kindOrName,
		},
		{
			name:    "config file without a name",
			body:    `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nspec:\n  nodes: []\n"}`,
			status:  http.StatusUnprocessableEntity,
			message: kindOrName,
		},
		{
			name: "oversized config file", body: `{"content":"` + strings.Repeat("x", bapi.MaxDocumentBytes+1) + `"}`,
			status: http.StatusRequestEntityTooLarge, message: "the config file is larger than 5242880 bytes",
		},
		{
			name: "includes of another value", body: `{"source":"Topology/root","includes":"flatten"}`,
			status: http.StatusBadRequest, message: badIncludes,
		},
		{
			name: "includes is checked before the source is read", body: `{"source":"Topology/missing","includes":"all"}`,
			status: http.StatusBadRequest, message: badIncludes,
		},
		{
			name: "a name without copy or combine", body: `{"source":"Topology/root","name":"root-2"}`,
			status: http.StatusBadRequest, message: strayName,
		},
		{
			name: "a name with includes kept", body: `{"source":"Topology/root","includes":"keep","name":"root-2"}`,
			status: http.StatusBadRequest, message: strayName,
		},
		{
			name: "a name with copy false", body: `{"source":"Topology/root","copy":false,"name":"root-2"}`,
			status: http.StatusBadRequest, message: strayName,
		},
		{
			name: "the source is read before the name is checked", body: `{"source":"Topology/missing","copy":true,"name":"bad name"}`,
			status: http.StatusNotFound,
		},
		{
			name: "a source the caller may not read", body: `{"source":"Topology/secret","includes":"combine"}`,
			role: limited, status: http.StatusForbidden,
		},
		{
			name: "a copy of an experiment", body: `{"source":"Experiment/exp","copy":true}`,
			status: http.StatusUnprocessableEntity, message: onlyTopo,
		},
		{
			name: "a combined experiment", body: `{"source":"Experiment/exp","includes":"combine","name":"exp-2"}`,
			status: http.StatusUnprocessableEntity, message: onlyTopo,
		},
		{
			name: "a combined experiment file, before its name is checked",
			body: `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: lab\n` +
				`spec:\n  topology:\n    nodes: []\n","includes":"combine","name":"bad name"}`,
			status: http.StatusUnprocessableEntity, message: onlyTopo,
		},
		{
			name:   "a scenario file is refused for its kind, not for a name",
			body:   `{"content":"apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: a b\n","copy":true}`,
			status: http.StatusUnprocessableEntity, message: "the Builder cannot open Scenario configs",
		},
		{
			name: "a name with a space", body: `{"source":"Topology/root","copy":true,"name":"root copy"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "root copy" is not allowed` + nameRule, code: bdoc.CodeImportNameInvalid,
		},
		{
			name: "a name with a slash", body: `{"source":"Topology/root","includes":"combine","name":"Topology/other"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "Topology/other" is not allowed` + nameRule, code: bdoc.CodeImportNameInvalid,
		},
		{
			name:    "a name of 513 bytes, shown cut",
			body:    `{"source":"Topology/root","copy":true,"name":"` + strings.Repeat("n", 513) + `"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "` + strings.Repeat("n", 64) + `..." is not allowed` + nameRule,
			code:    bdoc.CodeImportNameInvalid,
		},
		{
			name: "the name of the stored topology", body: `{"source":"Topology/root","copy":true,"name":"root"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "root" is the name of the imported topology: enter another name`,
			code:    bdoc.CodeImportNameSource,
		},
		{
			name: "the name of the stored topology, combined", body: `{"source":"Topology/root","includes":"combine","name":"root"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "root" is the name of the imported topology: enter another name`,
			code:    bdoc.CodeImportNameSource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost, path: "/builder/generate", body: tt.body, user: builderAlice, role: tt.role,
			})
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}

			refusal := builderRefusal(t, recorder)

			if tt.message != "" && refusal.Message != tt.message {
				t.Errorf("message = %q, want %q", refusal.Message, tt.message)
			}

			if tt.code != "" && refusal.Code != string(tt.code) {
				t.Errorf("code = %q, want %q", refusal.Code, tt.code)
			}
		})
	}
}

// TestBuilderGenerateChoices asserts the name and note an allowed import's
// document gets; a file has no stored topology, so it keeps its own name.
func TestBuilderGenerateChoices(t *testing.T) {
	configs, _ := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	long := strings.Repeat("n", 512)
	file := `apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: plant\nspec:\n  nodes: []\n`

	for body, want := range map[string]struct{ name, note string }{
		`{"source":"Topology/root"}`:                                   {"root", ""},
		`{"source":"Topology/root","includes":""}`:                     {"root", ""},
		`{"source":"Topology/root","includes":"keep","copy":false}`:    {"root", ""},
		`{"source":"Topology/root","copy":true,"name":"` + long + `"}`: {long, "Copied from root"},
		`{"content":"` + file + `","copy":true,"name":"plant"}`:        {"plant", "Copied from plant"},
		`{"content":"` + file + `","includes":"combine"}`:              {"plant-combined", "Copied from plant"},
		`{"content":"` + file + `","includes":"keep","copy":true}`:     {"plant-copy", "Copied from plant"},
	} {
		_, document := builderGenerate(t, harness, nil, body)
		if document.Metadata.Name != want.name {
			t.Errorf("%.80s: document name = %q, want %q", body, document.Metadata.Name, want.name)
		}

		var notes []string
		if want.note != "" {
			notes = []string{want.note}
		}

		if !reflect.DeepEqual(document.Metadata.Notes, notes) {
			t.Errorf("%.80s: notes = %q, want %q", body, document.Metadata.Notes, notes)
		}
	}
}

// TestBuilderGenerateCopy asserts a copy is a plain import's document linked
// to no config, while the response still names the topology that was read.
func TestBuilderGenerateCopy(t *testing.T) {
	configs, _ := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	_, plain := builderGenerate(t, harness, nil, `{"source":"Topology/root"}`)
	response, copied := builderGenerate(t, harness, nil, `{"source":"Topology/root","copy":true}`)

	if plain.Source.Kind != bdoc.SourceKindTopology || copied.Source.Kind != bdoc.SourceKindManual ||
		copied.Source.Name != "" || !reflect.DeepEqual(copied.Nodes, plain.Nodes) {
		t.Errorf("plain source %+v, copy source %+v, want the copy's nodes linked to nothing", plain.Source, copied.Source)
	}

	if response.Source.FullName != "Topology/root" || !response.Source.Stored || harness.configWrites != 0 {
		t.Errorf("source = %+v after %d writes, want the stored topology root and none", response.Source, harness.configWrites)
	}
}

// TestBuilderGenerateCombine asserts a combined import copies the nodes of the
// includes the caller may read; the others stay included by reference.
func TestBuilderGenerateCombine(t *testing.T) {
	configs, role := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	response, document := builderGenerate(t, harness, role, `{"source":"Topology/root","includes":"combine"}`)

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

	if notes := []string{"Copied from root"}; !reflect.DeepEqual(document.Metadata.Notes, notes) {
		t.Errorf("notes = %q, want %q", document.Metadata.Notes, notes)
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
		!reflect.DeepEqual(document.Source.IncludeTopologies, []string{"../outside.yml"}) ||
		!reflect.DeepEqual(document.Metadata.Notes, []string{"Copied from root"}) {
		t.Errorf("name = %q, source = %s, notes = %q, want site_all with secret-host, only the file still included "+
			"and the note", document.Metadata.Name, builderJSON(t, document.Source), document.Metadata.Notes)
	}

	// A config file is combined the same way, from the store.
	content, err := json.Marshal(configs[0])
	if err != nil {
		t.Fatalf("encoding the topology: %v", err)
	}

	response, document = builderGenerate(t, harness, nil,
		builderJSON(t, map[string]string{"content": string(content), "includes": "combine"}))

	if document.Metadata.Name != "root-combined" || document.FindDevice("visible-host") == nil || response.Source.Stored ||
		!reflect.DeepEqual(document.Metadata.Notes, []string{"Copied from root"}) {
		t.Errorf("name = %q, source = %+v, notes = %q, want root-combined made from the file", document.Metadata.Name,
			response.Source, document.Metadata.Notes)
	}

	if harness.configWrites != 0 {
		t.Errorf("combining wrote %d configs, want none", harness.configWrites)
	}
}

// TestBuilderGenerateLegacyTopologyChoices asserts a legacy Builder topology
// may be copied or combined, converted from its diagram and linked to nothing.
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
		copied.Source.Kind != bdoc.SourceKindManual || copied.Source.Name != "" || copied.Source.Digest != "" ||
		!reflect.DeepEqual(copied.Metadata.Notes, []string{"Copied from sample"}) {
		t.Errorf("copy: response source = %+v, document source = %+v, notes = %q, want a legacy conversion "+
			"linked to nothing that names its source in a note", response.Source, copied.Source, copied.Metadata.Notes)
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
		combined.Source.Kind != bdoc.SourceKindManual || !reflect.DeepEqual(combined.Metadata.Notes, []string{"Copied from sample"}) {
		t.Errorf("combined: name = %q, source = %+v, notes = %q, want inc-host as its own node, only the missing "+
			"include kept and the note", combined.Metadata.Name, combined.Source, combined.Metadata.Notes)
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

// TestBuilderListSourcesIncludeCount asserts a topology lists its include
// count only to a caller who may get it, and no other kind has one.
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
	_, body := builderListSourcesBody(t, harness, nil)
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
	if got := counts(builderListSources(t, harness, role).Topologies); !reflect.DeepEqual(got, want) {
		t.Errorf("topology include counts for a caller who may get only root = %v, want %v", got, want)
	}
}

// TestBuilderListDocumentsReadsEachKindOnce asserts the listing lists each
// config kind once, gets none, and lists exactly the documents a get answers.
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

	var response struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents", user: builderAlice, role: role},
		http.StatusOK, &response)

	lists := slices.Sorted(slices.Values(harness.configLists))
	if !slices.Equal(lists, []string{kindExperiment, builderKindTopology}) || harness.configGets != 0 {
		t.Fatalf("the listing listed %q and got %d configs, want each kind listed once and none got",
			harness.configLists, harness.configGets)
	}

	listed := map[string]bool{}
	for _, document := range response.Documents {
		listed[document.ID] = document.Document == nil
	}

	for _, document := range documents {
		got := harness.do(builderRequest{
			method: http.MethodGet,
			path:   "/builder/documents/" + document.ID,
			user:   builderAlice,
			role:   role,
		}).Code

		if listed[document.ID] != current[document.ID] || (got == http.StatusOK) != current[document.ID] {
			t.Errorf("document %s of %s: listed %t, get %d; want listed, without its bytes, and got only when current and allowed",
				document.ID, document.Target, listed[document.ID], got)
		}
	}
}

func TestBuilderGetDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	document := builderPublish(t, harness, "topo")

	var response builderDocumentResponse

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents/" + document.ID, user: builderAlice},
		http.StatusOK, &response)

	if response.Config != "Topology/topo" || !strings.Contains(string(response.Document), "topo") {
		t.Errorf("config = %q, document %s; want Topology/topo and its document", response.Config, response.Document)
	}
}

// builderTestStore is a phenix store that keeps configs in a BoltDB and
// records in memory, so a test can fail the removal of a document's content.
type builderTestStore struct {
	*memrecord.Store
	builderTestConfigs
}

// builderTestConfigs gives builderTestStore its config methods, a level deeper
// than the record store so its record methods are never promoted.
type builderTestConfigs struct{ store.Store }

// newBuilderStoreHarness serves the Builder and /configs routes from a BoltDB
// phenix store whose config hooks run; with records, records are kept there.
func newBuilderStoreHarness(t *testing.T, records *memrecord.Store) *builderHarness {
	t.Helper()

	db := storetest.NewBolt(t)

	var phenix store.Store = db
	if records != nil {
		phenix = &builderTestStore{Store: records, builderTestConfigs: builderTestConfigs{Store: db}}
	}

	storetest.Use(t, phenix)

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
		documents = append(documents, builderPutDocument(h.t, h, builderKindTopology, target, content))
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

// TestBuilderDocumentsDeletedWithTopology asserts deleting a published
// topology through the Builder deletes its documents, without a warning.
func TestBuilderDocumentsDeletedWithTopology(t *testing.T) {
	harness := newBuilderStoreHarness(t, nil)
	documents := harness.publishTopology("topo")
	kept := harness.publishTopology("kept")
	remove := builderRequest{method: http.MethodDelete, path: "/builder/documents/" + documents[1].ID, user: builderAlice}

	if recorder := harness.doDecode(remove, http.StatusNoContent, nil); recorder.Header().Get("Warning") != "" {
		t.Fatalf("warning %q, want none", recorder.Header().Get("Warning"))
	}

	harness.assertDocumentsGone(documents)

	if _, err := config.Get("Topology/topo", false); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("topology: error = %v, want it deleted", err)
	}

	if _, _, err := harness.service.GetPublishedDocumentData(t.Context(), kept[1].ID); err != nil {
		t.Errorf("another topology's document: %v, want it kept", err)
	}

	harness.doDecode(remove, http.StatusNotFound, nil)
}

// TestBuilderDeleteDocumentWarnsWithConfigHook asserts a delete whose content
// removal fails deletes the topology and answers with the warning.
func TestBuilderDeleteDocumentWarnsWithConfigHook(t *testing.T) {
	records := memrecord.New()
	harness := newBuilderStoreHarness(t, records)
	documents := harness.publishTopology("topo")

	records.FailPrefixDelete = func(string, string) error { return errors.New("injected content delete failure") }

	recorder := harness.doDecode(builderRequest{
		method: http.MethodDelete, path: "/builder/documents/" + documents[1].ID, user: builderAlice,
	}, http.StatusNoContent, nil)

	const warning = `199 phenix "delete published topology succeeded but removing superseded content failed"`

	if recorder.Header().Get("Warning") != warning {
		t.Fatalf("warning %q, want %q", recorder.Header().Get("Warning"), warning)
	}

	if _, err := config.Get("Topology/topo", false); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("topology: error = %v, want it deleted", err)
	}

	harness.assertDocumentsGone(documents)
}

// TestBuilderDocumentsDeletedWithRenamedTopology asserts a topology renamed
// through PUT /configs names no document, and one PUT under its name stays published.
func TestBuilderDocumentsDeletedWithRenamedTopology(t *testing.T) {
	harness := newBuilderStoreHarness(t, nil)

	harness.publishTopology("topo")
	kept := harness.publishTopology("kept")

	for name, renamed := range map[string]string{"topo": "renamed", "kept": "kept"} {
		harness.doDecode(builderRequest{
			method: http.MethodPut, path: "/configs/topology/" + name, user: builderAlice,
			body: harness.renameBody(name, renamed), contentType: "application/json",
		}, http.StatusNoContent, nil)
	}

	var listed struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents", user: builderAlice},
		http.StatusOK, &listed)

	if len(listed.Documents) != 1 || listed.Documents[0].ID != kept[1].ID {
		t.Errorf("listed documents = %+v, want only %s of the topology kept", listed.Documents, kept[1].ID)
	}

	for _, source := range builderListSources(t, harness, nil).Topologies {
		if want := map[string]string{"kept": bapi.DocumentAnnotation, "renamed": ""}[source.Name]; source.Builder != want {
			t.Errorf("source %s builder = %q, want %q", source.Name, source.Builder, want)
		}
	}
}

// TestConfigRoutesWaitForBuilderPublishing asserts deleting or renaming a
// topology through /configs waits for the Builder publish lock; other changes do not.
func TestConfigRoutesWaitForBuilderPublishing(t *testing.T) {
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
			test.request.user = builderAlice
			answered := make(chan *httptest.ResponseRecorder, 1)

			builderPublishLock.Lock()

			go func() { answered <- harness.do(test.request) }()

			// A request that does not wait gets as long as a slow machine may need.
			patience := time.Minute
			if test.waits {
				patience = 75 * time.Millisecond
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

// TestBuilderDeleteDocumentRefusals asserts a published topology is deleted
// only with configs delete for it and while current, and an experiment never.
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
		{name: "no configs delete", role: readers, status: http.StatusForbidden},
		{name: "configs delete for another topology", role: otherDeleter, status: http.StatusForbidden},
		{name: "hidden", role: hidden, status: http.StatusNotFound},
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

			var refusal builderErrorBody

			harness.doDecode(builderRequest{
				method: http.MethodDelete, path: "/builder/documents/" + document.ID, user: builderAlice, role: test.role,
			}, test.status, &refusal)

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
