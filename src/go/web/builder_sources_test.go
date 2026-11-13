package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gopkg.in/yaml.v3"

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

// ignoreBuilderSourceStamps ignores what a source response takes from the
// stored config's metadata, which the tests do not set.
func ignoreBuilderSourceStamps() cmp.Options {
	return cmp.Options{cmpopts.IgnoreFields(builderSourceResponse{}, "APIVersion", "Created", "Updated", "Digest")}
}

// TestBuilderListSources asserts a source is listed only with configs list
// for its name and, for a kind that has one, the kind's own list permission.
func TestBuilderListSources(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, "Topology", "visible"),
		builderConfig(t, "Topology", "hidden"),
		builderConfig(t, "Experiment", "exp"),
		builderConfig(t, "Scenario", "allowed"),
		builderConfig(t, "Scenario", "denied"),
		builderConfig(t, "Image", "image"),
	)

	tests := []struct {
		name     string
		role     *rbac.Role
		want     [][]string
		unlisted []string
	}{
		{
			name: "kind permissions",
			role: builderRole(
				builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
				builderPolicy([]string{"topologies"}, []string{"visible"}, []string{"list"}),
				builderPolicy([]string{"scenarios"}, []string{"allowed"}, []string{"list"}),
			),
			// Image configs have no kind specific permission, so configs list alone admits them.
			want:     [][]string{{"Topology/visible"}, {}, {"Scenario/allowed"}, {"Image/image"}},
			unlisted: []string{"hidden", "denied"},
		},
		{
			name: "config permissions by name",
			role: builderRole(
				builderPolicy([]string{"configs"}, []string{"Topology/visible", "Scenario/*"}, []string{"list"}),
				builderPolicy([]string{"topologies", "experiments", "scenarios"}, []string{"*"}, []string{"list"}),
			),
			want:     [][]string{{"Topology/visible"}, {}, {"Scenario/allowed", "Scenario/denied"}, {}},
			unlisted: []string{"hidden", "Experiment/exp", "Image/image"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups, body := builderListSourcesBody(t, harness, tt.role)
			got := [][]string{
				builderSourceNames(groups.Topologies), builderSourceNames(groups.Experiments),
				builderSourceNames(groups.Scenarios), builderSourceNames(groups.Images),
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GET /builder/sources mismatch (-want +got):\n%s", diff)
			}

			for _, unlisted := range tt.unlisted {
				if strings.Contains(body, unlisted) {
					t.Errorf("GET /builder/sources names %q: %s", unlisted, body)
				}
			}
		})
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

	source := func(kind, name string, generatable bool) []builderSourceResponse {
		return []builderSourceResponse{{
			Kind: kind, Name: name, FullName: kind + "/" + name, Stored: true, Generatable: generatable,
		}}
	}

	want := builderSourceGroups{
		Topologies:  source("Topology", "topo", true),
		Experiments: source("Experiment", "exp", true),
		Scenarios:   source("Scenario", "scenario", false),
		Images:      source("Image", "image", false),
	}

	if diff := cmp.Diff(want, groups, ignoreBuilderSourceStamps()); diff != "" {
		t.Errorf("GET /builder/sources mismatch (-want +got):\n%s", diff)
	}

	for _, unwanted := range []string{"User/", "Role/", "vlans"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("GET /builder/sources reports %q: %s", unwanted, body)
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
		t.Fatalf("POST /builder/generate of Topology/topo: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response builderGenerateResponse

	harness.decode(recorder, &response)

	document, err := bdoc.Decode(response.Document)
	if err != nil {
		t.Fatalf("decoding the generated document: %v", err)
	}

	// The source keeps the config's own annotations, and says when it was imported.
	if diff := cmp.Diff(map[string]string{"team": "ops"}, document.Source.Annotations); diff != "" {
		t.Errorf("generated document source annotations mismatch (-want +got):\n%s", diff)
	}

	imported, err := time.Parse(time.RFC3339, document.Source.ImportedAt)
	if err != nil || imported.Before(before) || imported.After(time.Now().UTC()) {
		t.Errorf("generated document importedAt = %q, want the time of the request", document.Source.ImportedAt)
	}

	want := builderSourceResponse{
		Kind: "Topology", Name: "topo", FullName: "Topology/topo", Stored: true,
		Builder: bdoc.LegacyXMLAnnotation, Generatable: true,
	}
	if diff := cmp.Diff(want, response.Source, ignoreBuilderSourceStamps()); diff != "" {
		t.Errorf("POST /builder/generate of Topology/topo: source mismatch (-want +got):\n%s", diff)
	}

	if response.Warnings == nil {
		t.Error("POST /builder/generate of Topology/topo: warnings = null, want an array")
	}

	// The generated document round-trips through the draft service.
	harness.postDraft(builderAlice, map[string]any{"sourceToken": "Topology/topo", "document": response.Document})

	// Generating never writes a config.
	if len(harness.configs) != 1 || harness.configs[0].FullName() != stored.FullName() {
		t.Errorf("configs after generating = %+v, want the single stored config unchanged", harness.configs)
	}
}

func TestBuilderGenerateFromUpload(t *testing.T) {
	harness := newBuilderHarness(t)

	uploads := []struct {
		name    string
		content string
		// builder is the builder annotation the upload carries, and annotations
		// are those the document's source keeps.
		builder     string
		annotations map[string]string
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
			builder:     bapi.DocumentAnnotation,
			annotations: map[string]string{"team": "ops"},
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
				t.Fatalf("POST /builder/generate of an upload: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
			}

			var response builderGenerateResponse

			decodeBuilderAnswer(t, recorder, &response)

			want := builderSourceResponse{
				Kind: "Topology", Name: "uploaded", FullName: "Topology/uploaded", Builder: upload.builder, Generatable: true,
			}
			if diff := cmp.Diff(want, response.Source, ignoreBuilderSourceStamps()); diff != "" {
				t.Errorf("POST /builder/generate of an upload: source mismatch (-want +got):\n%s", diff)
			}

			document, err := bdoc.Decode(response.Document)
			if err != nil {
				t.Fatalf("decoding the generated document: %v", err)
			}

			if diff := cmp.Diff(upload.annotations, document.Source.Annotations, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("generated document source annotations mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// Nothing was stored.
	if len(harness.configs) != 0 {
		t.Errorf("configs after generating from uploads = %+v, want none", harness.configs)
	}
}

// TestBuilderUploadsKeepPlaceholders asserts that each Builder route that
// parses a config file keeps ${NAME} as written and never sends the value of
// a server environment variable back.
func TestBuilderUploadsKeepPlaceholders(t *testing.T) {
	const (
		secret = "builder-upload-secret-value"
		set    = "${PHENIX_BUILDER_TEST_SECRET}"
		unset  = "${PHENIX_BUILDER_TEST_UNSET:server-default}"
	)

	t.Setenv("PHENIX_BUILDER_TEST_SECRET", secret)

	topology := func(t *testing.T, legacy bool) store.Config {
		t.Helper()

		config := builderConfig(t, builderKindTopology, "uploaded")
		config.Metadata.Annotations = store.Annotations{}

		if legacy {
			config = legacySampleTopology(t)
		}

		config.Metadata.Annotations["set"] = set
		config.Metadata.Annotations["unset"] = unset

		return config
	}

	tests := []struct {
		name   string
		path   string
		legacy bool
		asYAML bool
	}{
		{name: "generate JSON", path: "/builder/generate"},
		{name: "generate YAML", path: "/builder/generate", asYAML: true},
		{name: "legacy JSON", path: "/builder/legacy", legacy: true},
		{name: "legacy YAML", path: "/builder/legacy", legacy: true, asYAML: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marshal := json.Marshal
			if tt.asYAML {
				marshal = yaml.Marshal
			}

			content, err := marshal(topology(t, tt.legacy))
			if err != nil {
				t.Fatalf("encoding the config: %v", err)
			}

			harness := newBuilderHarness(t)
			body := builderJSON(t, map[string]string{"content": string(content)})

			recorder := harness.do(builderRequest{method: http.MethodPost, path: tt.path, body: body, user: builderAlice})
			if err := builderStatusError(recorder, http.StatusOK); err != nil {
				t.Fatalf("POST %s (%s): %v", tt.path, tt.name, err)
			}

			if strings.Contains(recorder.Body.String(), secret) {
				t.Errorf("POST %s (%s) sent the value of PHENIX_BUILDER_TEST_SECRET, want no value from the server environment",
					tt.path, tt.name)
			}

			var response struct {
				Document json.RawMessage `json:"document"`
			}

			decodeBuilderAnswer(t, recorder, &response)

			document, err := bdoc.Parse(response.Document)
			if err != nil {
				t.Fatalf("bdoc.Parse(the document of POST %s) error: %v", tt.path, err)
			}

			got := map[string]string{"set": document.Source.Annotations["set"], "unset": document.Source.Annotations["unset"]}
			if diff := cmp.Diff(map[string]string{"set": set, "unset": unset}, got); diff != "" {
				t.Errorf("POST %s (%s): source annotations mismatch (-want +got):\n%s", tt.path, tt.name, diff)
			}
		})
	}
}

// TestBuilderFileImportKeepsDescriptionPlaceholder asserts that a device
// description in a config file stays as written in the generated document and
// in the draft made from that document, as Import from a config file does.
func TestBuilderFileImportKeepsDescriptionPlaceholder(t *testing.T) {
	const (
		placeholder = "${PHENIX_BUILDER_TEST_DESCRIPTION:expanded}"
		value       = "builder-description-value"
	)

	// The set variable and the default both show an expansion.
	t.Setenv("PHENIX_BUILDER_TEST_DESCRIPTION", value)

	content := builderJSON(t, map[string]any{
		"apiVersion": "phenix.sandia.gov/v1",
		"kind":       "Topology",
		"metadata":   map[string]any{"name": "envtopo"},
		"spec": map[string]any{"nodes": []any{map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": "envhost", "description": placeholder},
			"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "envhost.qc2"}}},
			"network": map[string]any{"interfaces": []any{map[string]any{
				"name": "eth0", "vlan": "EXP", "type": "ethernet", "proto": "dhcp",
			}}},
		}}},
	})

	description := func(t *testing.T, what string, data json.RawMessage) string {
		t.Helper()

		document, err := bdoc.Parse(data)
		if err != nil {
			t.Fatalf("bdoc.Parse(the document of %s) error: %v", what, err)
		}

		for _, node := range document.Nodes {
			if node.Kind != bdoc.NodeKindDevice || node.Device == nil {
				continue
			}

			general, _ := node.Device.Spec["general"].(map[string]any)
			text, _ := general["description"].(string)

			return text
		}

		t.Fatalf("the document of %s has no device node", what)

		return ""
	}

	harness := newBuilderHarness(t)

	var generated struct {
		Document json.RawMessage `json:"document"`
	}

	recorder := harness.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/generate", user: builderAlice,
		body: builderJSON(t, map[string]string{"content": content}),
	}, http.StatusOK, &generated)

	if strings.Contains(recorder.Body.String(), value) {
		t.Errorf("POST /builder/generate sent the value of PHENIX_BUILDER_TEST_DESCRIPTION, want no value from the server environment")
	}

	var created, stored builderDraftResponse

	harness.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderAlice,
		body: builderJSON(t, map[string]any{"document": generated.Document, "sourceFile": "topology.json"}),
	}, http.StatusCreated, &created)

	path := "/builder/drafts/" + created.ID
	harness.doDecode(builderRequest{method: http.MethodGet, path: path, user: builderAlice}, http.StatusOK, &stored)

	got := map[string]string{
		"generated": description(t, "POST /builder/generate", generated.Document),
		"stored":    description(t, "GET "+path, stored.Document),
	}
	if diff := cmp.Diff(map[string]string{"generated": placeholder, "stored": placeholder}, got); diff != "" {
		t.Errorf("device descriptions mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderGenerateUploadRequiresCreate asserts an upload needs configs
// create, the permission of POST /configs, which also takes a config file.
func TestBuilderGenerateUploadRequiresCreate(t *testing.T) {
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
			`metadata:\n  name: uploaded\nspec:\n  nodes: []\n"}`
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
				t.Fatalf("POST /builder/generate %s: status = %d, want %d: %s", tt.name, recorder.Code, tt.status, recorder.Body)
			}

			if tt.status != http.StatusForbidden {
				return
			}

			// The refusal names the source as the Import dialog does.
			want := "importing a Builder document from a config file: user " + builderAlice + " does not have permission"
			if got := builderRefusal(t, recorder).Message; !strings.Contains(got, want) {
				t.Errorf("POST /builder/generate %s: message = %q, want it to say %q", tt.name, got, want)
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
			name: "generating without the list permission of the kind", user: builderAlice, role: noKind,
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

	response, document := builderGenerate(t, harness, role, `{"source":"Topology/root"}`)

	if host := document.FindDevice("visible-host"); host == nil || host.Device.IncludedFrom != "visible" {
		t.Errorf("generated document = %s, want visible-host included from visible", response.Document)
	}

	if strings.Contains(string(response.Document), "secret-host") {
		t.Errorf("generated document = %s, want secret-host left out", response.Document)
	}

	for _, want := range []string{
		`the Builder could not read included topology "secret": you do not have permission to read it. ` +
			"The diagram does not show its nodes",
		`the Builder could not read included topology "../outside.yml": ` +
			`the Builder reads included topologies from the config store only, not from files. ` +
			"The diagram does not show its nodes",
		"The Builder added 1 node from included topology visible (1 node).",
	} {
		if !slices.ContainsFunc(response.Warnings, func(warning string) bool { return strings.HasPrefix(warning, want) }) {
			t.Errorf("generating Topology/root: warnings = %q, want one starting with %q", response.Warnings, want)
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
		strayName   = `use name only with copy or with includes "combine"`
		onlyTopo    = "an import can combine or copy only a topology"
		nameRule    = ". Use 1 to 512 letters, numbers, underscores, at signs, periods and hyphens"
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
			status: http.StatusBadRequest, message: "the request needs exactly one of source and content",
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
			name:   "an image file",
			body:   `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Image\nmetadata:\n  name: img\n"}`,
			status: http.StatusUnprocessableEntity, message: "the Builder cannot open Image configs",
		},
		{
			name:   "a topology file at another version",
			body:   `{"content":"apiVersion: phenix.sandia.gov/v0\nkind: Topology\nmetadata:\n  name: lab\nspec:\n  nodes: []\n"}`,
			status: http.StatusUnprocessableEntity, message: "the Builder cannot open Topology configs at version v0",
		},
		{
			name:   "an experiment file no document can be made from",
			body:   `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: lab\nspec:\n  topology: 5\n"}`,
			status: http.StatusUnprocessableEntity, message: "unable to import a Builder document from Experiment/lab",
		},
		{
			name: "a name with a space", body: `{"source":"Topology/root","copy":true,"name":"root copy"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "root copy" is not valid` + nameRule, code: bdoc.CodeImportNameInvalid,
		},
		{
			name: "a name with a slash", body: `{"source":"Topology/root","includes":"combine","name":"Topology/other"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "Topology/other" is not valid` + nameRule, code: bdoc.CodeImportNameInvalid,
		},
		{
			name:    "a name of 513 bytes, shown cut",
			body:    `{"source":"Topology/root","copy":true,"name":"` + strings.Repeat("n", 513) + `"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "` + strings.Repeat("n", 64) + `..." is not valid` + nameRule,
			code:    bdoc.CodeImportNameInvalid,
		},
		{
			name: "the name of the stored topology", body: `{"source":"Topology/root","copy":true,"name":"root"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "root" is the name of the imported topology. Enter another name`,
			code:    bdoc.CodeImportNameSource,
		},
		{
			name: "the name of the stored topology, combined", body: `{"source":"Topology/root","includes":"combine","name":"root"}`,
			status:  http.StatusUnprocessableEntity,
			message: `new topology name "root" is the name of the imported topology. Enter another name`,
			code:    bdoc.CodeImportNameSource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost, path: "/builder/generate", body: tt.body, user: builderAlice, role: tt.role,
			})
			if recorder.Code != tt.status {
				t.Errorf("POST /builder/generate: status = %d, want %d: %.300s", recorder.Code, tt.status, recorder.Body)
			}

			refusal := builderRefusal(t, recorder)

			if tt.message != "" && refusal.Message != tt.message {
				t.Errorf("POST /builder/generate: message = %q, want %q", refusal.Message, tt.message)
			}

			if tt.code != "" && refusal.Code != string(tt.code) {
				t.Errorf("POST /builder/generate: code = %q, want %q", refusal.Code, tt.code)
			}
		})
	}
}

// TestBuilderGenerateScenarioLookupFailure asserts an import answers 500 when
// the store cannot read the scenario an experiment names.
func TestBuilderGenerateScenarioLookupFailure(t *testing.T) {
	harness := newBuilderHarness(t)
	offline := errors.New("store offline")

	harness.reroute(withBuilderConfigs(harness.listConfigs, func(name string) (*store.Config, error) {
		if strings.HasPrefix(name, "Scenario/") {
			return nil, offline
		}

		return harness.getConfig(name)
	}))

	body := `{"content":"apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: lab\n` +
		`  annotations:\n    scenario: scn\nspec:\n  topology:\n    nodes: []\n"}`

	recorder := harness.do(builderRequest{method: http.MethodPost, path: "/builder/generate", body: body, user: builderAlice})
	if err := builderStatusError(recorder, http.StatusInternalServerError); err != nil {
		t.Fatalf("POST /builder/generate: %v", err)
	}

	if got, want := builderRefusal(t, recorder).Message, "unable to read the scenario of Experiment/lab"; got != want {
		t.Errorf("POST /builder/generate: message = %q, want %q", got, want)
	}
}

// TestBuilderGenerateChoices asserts the name and note an allowed import's
// document gets. A file has no stored topology, so it keeps its own name.
func TestBuilderGenerateChoices(t *testing.T) {
	configs, _ := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	long := strings.Repeat("n", 512)
	file := `apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: plant\nspec:\n  nodes: []\n`

	for _, tt := range []struct {
		name, body string
		want       bdoc.Metadata
	}{
		{name: "a source", body: `{"source":"Topology/root"}`, want: bdoc.Metadata{Name: "root"}},
		{name: "a source with no includes choice", body: `{"source":"Topology/root","includes":""}`, want: bdoc.Metadata{Name: "root"}},
		{
			name: "a source with includes kept and no copy", body: `{"source":"Topology/root","includes":"keep","copy":false}`,
			want: bdoc.Metadata{Name: "root"},
		},
		{
			name: "a copy with a name of 512 bytes", body: `{"source":"Topology/root","copy":true,"name":"` + long + `"}`,
			want: bdoc.Metadata{Name: long, Notes: []string{"Copied from root"}},
		},
		{
			name: "a copied file named as it is", body: `{"content":"` + file + `","copy":true,"name":"plant"}`,
			want: bdoc.Metadata{Name: "plant", Notes: []string{"Copied from plant"}},
		},
		{
			name: "a combined file", body: `{"content":"` + file + `","includes":"combine"}`,
			want: bdoc.Metadata{Name: "plant-combined", Notes: []string{"Copied from plant"}},
		},
		{
			name: "a copied file with includes kept", body: `{"content":"` + file + `","includes":"keep","copy":true}`,
			want: bdoc.Metadata{Name: "plant-copy", Notes: []string{"Copied from plant"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, document := builderGenerate(t, harness, nil, tt.body)
			if diff := cmp.Diff(tt.want, document.Metadata, builderNameAndNotes()); diff != "" {
				t.Errorf("generating %.80s: metadata mismatch (-want +got):\n%s", tt.body, diff)
			}
		})
	}
}

// builderNameAndNotes compares only the name and the notes of document
// metadata.
func builderNameAndNotes() cmp.Options {
	return cmp.Options{
		cmpopts.IgnoreFields(bdoc.Metadata{}, "ID", "Description", "CreatedBy", "CreatedAt", "UpdatedBy", "UpdatedAt"),
	}
}

// ignoreCopiedSourceDetails ignores what a copy keeps of how it was read,
// leaving the fields that would link it to a config.
func ignoreCopiedSourceDetails() cmp.Options {
	return cmp.Options{
		cmpopts.IgnoreFields(bdoc.Source{}, "ImportedAt", "IncludeTopologies", "UnresolvedIncludes", "Warnings"),
	}
}

// TestBuilderGenerateCopy asserts a copy is a plain import's document linked
// to no config, while the response still names the topology that was read.
func TestBuilderGenerateCopy(t *testing.T) {
	configs, _ := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	_, plain := builderGenerate(t, harness, nil, `{"source":"Topology/root"}`)
	response, copied := builderGenerate(t, harness, nil, `{"source":"Topology/root","copy":true}`)

	if plain.Source.Kind != bdoc.SourceKindTopology {
		t.Errorf("generating Topology/root: source kind = %q, want %q", plain.Source.Kind, bdoc.SourceKindTopology)
	}

	if diff := cmp.Diff(&bdoc.Source{Kind: bdoc.SourceKindManual}, copied.Source, ignoreCopiedSourceDetails()); diff != "" {
		t.Errorf("copying Topology/root: source mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(plain.Nodes, copied.Nodes); diff != "" {
		t.Errorf("copying Topology/root: nodes mismatch with a plain import (-plain +copy):\n%s", diff)
	}

	want := builderSourceResponse{
		Kind: "Topology", Name: "root", FullName: "Topology/root", Stored: true, Generatable: true,
	}
	if diff := cmp.Diff(want, response.Source, ignoreBuilderSourceStamps()); diff != "" {
		t.Errorf("copying Topology/root: response source mismatch (-want +got):\n%s", diff)
	}

	if harness.configWrites != 0 {
		t.Errorf("copying Topology/root wrote %d configs, want none", harness.configWrites)
	}
}

// TestBuilderGenerateCombine asserts a combined import copies the nodes of the
// includes the caller may read. The others stay included by reference.
func TestBuilderGenerateCombine(t *testing.T) {
	configs, role := includeSourceFixture(t)
	harness := newBuilderHarness(t, configs...)

	response, document := builderGenerate(t, harness, role, `{"source":"Topology/root","includes":"combine"}`)

	want := bdoc.Metadata{ID: bdoc.DocumentID("root-combined"), Name: "root-combined", Notes: []string{"Copied from root"}}
	if diff := cmp.Diff(want, document.Metadata,
		cmpopts.IgnoreFields(bdoc.Metadata{}, "Description", "CreatedBy", "CreatedAt", "UpdatedBy", "UpdatedAt")); diff != "" {
		t.Errorf("combining Topology/root: metadata mismatch (-want +got):\n%s", diff)
	}

	for _, left := range []string{"secret-host", "includedFrom"} {
		if strings.Contains(string(response.Document), left) {
			t.Errorf("combining Topology/root: document = %s, want no %s", response.Document, left)
		}
	}

	for _, hostname := range []string{"root-host", "visible-host"} {
		if document.FindDevice(hostname) == nil {
			t.Errorf("combining Topology/root: document = %s, want %s", response.Document, hostname)
		}
	}

	wantSource := &bdoc.Source{
		Kind:              bdoc.SourceKindManual,
		IncludeTopologies: []string{"secret", "../outside.yml"},
		Warnings:          response.Warnings,
	}
	if diff := cmp.Diff(wantSource, document.Source, cmpopts.IgnoreFields(bdoc.Source{}, "ImportedAt")); diff != "" {
		t.Errorf("combining Topology/root: document source mismatch (-want +got):\n%s", diff)
	}

	if document.Source != nil && document.Source.ImportedAt == "" {
		t.Error("combining Topology/root: document source importedAt = \"\", want the time of the request")
	}

	wantWarnings := []string{
		`the Builder could not read included topology "secret": you do not have permission to read it. ` +
			"The diagram does not show its nodes",
		`the Builder could not read included topology "../outside.yml": ` +
			`the Builder reads included topologies from the config store only, not from files. ` +
			"The diagram does not show its nodes",
		"The Builder copied 1 node from included topology visible (1 node). They are ordinary nodes of this diagram now: " +
			"changes here do not reach that topology, and later changes there do not reach this diagram.",
		"The Builder did not combine included topologies secret and ../outside.yml. They stay in includeTopologies, " +
			"and publishing keeps the references.",
	}
	if diff := cmp.Diff(wantWarnings, response.Warnings); diff != "" {
		t.Errorf("combining Topology/root: warnings mismatch (-want +got):\n%s", diff)
	}

	wantResponse := builderSourceResponse{
		Kind: "Topology", Name: "root", FullName: "Topology/root", Stored: true, Generatable: true,
	}
	if diff := cmp.Diff(wantResponse, response.Source, ignoreBuilderSourceStamps()); diff != "" {
		t.Errorf("combining Topology/root: response source mismatch (-want +got):\n%s", diff)
	}

	// The topology the document projects holds both nodes and the includes
	// that were kept.
	topology, err := document.ToTopology()
	if err != nil {
		t.Fatalf("ToTopology returned error: %v", err)
	}

	if nodes, _ := topology.Spec["nodes"].([]any); len(nodes) != 2 {
		t.Errorf("combining Topology/root: projected topology nodes = %v, want two", topology.Spec["nodes"])
	}

	if diff := cmp.Diff([]string{"secret", "../outside.yml"}, topology.Spec["includeTopologies"]); diff != "" {
		t.Errorf("combining Topology/root: projected topology includeTopologies mismatch (-want +got):\n%s", diff)
	}

	// A caller who may read every include, and names the new topology.
	_, document = builderGenerate(t, harness, nil,
		`{"source":"Topology/root","includes":"combine","copy":true,"name":"site_all"}`)

	if diff := cmp.Diff(bdoc.Metadata{Name: "site_all", Notes: []string{"Copied from root"}}, document.Metadata,
		builderNameAndNotes()); diff != "" {
		t.Errorf("combining Topology/root as site_all: metadata mismatch (-want +got):\n%s", diff)
	}

	if document.FindDevice("secret-host") == nil {
		t.Errorf("combining Topology/root as site_all: document source = %s, want secret-host", builderJSON(t, document.Source))
	}

	if diff := cmp.Diff([]string{"../outside.yml"}, document.Source.IncludeTopologies); diff != "" {
		t.Errorf("combining Topology/root as site_all: includeTopologies mismatch (-want +got):\n%s", diff)
	}

	// A config file is combined the same way, from the store.
	content, err := json.Marshal(configs[0])
	if err != nil {
		t.Fatalf("encoding the topology: %v", err)
	}

	response, document = builderGenerate(t, harness, nil,
		builderJSON(t, map[string]string{"content": string(content), "includes": "combine"}))

	if diff := cmp.Diff(bdoc.Metadata{Name: "root-combined", Notes: []string{"Copied from root"}}, document.Metadata,
		builderNameAndNotes()); diff != "" {
		t.Errorf("combining a file of root: metadata mismatch (-want +got):\n%s", diff)
	}

	if document.FindDevice("visible-host") == nil {
		t.Errorf("combining a file of root: document = %s, want visible-host", response.Document)
	}

	if response.Source.Stored {
		t.Errorf("combining a file of root: response source = %+v, want it not stored", response.Source)
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

	groups := builderListSources(t, harness, nil)
	if len(groups.Topologies) == 0 {
		t.Fatal("GET /builder/sources: topologies = none, want sample and shared")
	}

	want := builderSourceResponse{
		Kind: "Topology", Name: "sample", FullName: "Topology/sample", Stored: true,
		Builder: bdoc.LegacyXMLAnnotation, Generatable: true, IncludeCount: 2,
	}
	if diff := cmp.Diff(want, groups.Topologies[0], ignoreBuilderSourceStamps()); diff != "" {
		t.Errorf("GET /builder/sources: first topology mismatch (-want +got):\n%s", diff)
	}

	response, copied := builderGenerate(t, harness, nil, `{"source":"Topology/sample","copy":true}`)

	if response.Source.Builder != bdoc.LegacyXMLAnnotation {
		t.Errorf("copying Topology/sample: response source builder = %q, want %q", response.Source.Builder, bdoc.LegacyXMLAnnotation)
	}

	if diff := cmp.Diff(bdoc.Metadata{Name: "sample-copy", Notes: []string{"Copied from sample"}}, copied.Metadata,
		builderNameAndNotes()); diff != "" {
		t.Errorf("copying Topology/sample: metadata mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(&bdoc.Source{Kind: bdoc.SourceKindManual}, copied.Source, ignoreCopiedSourceDetails()); diff != "" {
		t.Errorf("copying Topology/sample: source mismatch (-want +got):\n%s", diff)
	}

	if got, want := legacyPosition(t, copied, "server-device-5"), (bdoc.Position{X: 1280, Y: 512}); got != want {
		t.Errorf("copying Topology/sample: position of server-device-5 = %+v, want %+v", got, want)
	}

	if host := copied.FindDevice("inc-host"); host == nil || host.Device.IncludedFrom != "shared" {
		t.Errorf("copying Topology/sample: inc-host = %+v, want it included from shared", host)
	}

	if diff := cmp.Diff([]string{"gone"}, copied.Source.UnresolvedIncludes); diff != "" {
		t.Errorf("copying Topology/sample: unresolved includes mismatch (-want +got):\n%s", diff)
	}

	_, combined := builderGenerate(t, harness, nil, `{"source":"Topology/sample","includes":"combine"}`)

	if got, want := legacyPosition(t, combined, "server-device-5"), (bdoc.Position{X: 1280, Y: 512}); got != want {
		t.Errorf("combining Topology/sample: position of server-device-5 = %+v, want %+v", got, want)
	}

	if diff := cmp.Diff(bdoc.Metadata{Name: "sample-combined", Notes: []string{"Copied from sample"}}, combined.Metadata,
		builderNameAndNotes()); diff != "" {
		t.Errorf("combining Topology/sample: metadata mismatch (-want +got):\n%s", diff)
	}

	if host := combined.FindDevice("inc-host"); host == nil || host.Device.IncludedFrom != "" {
		t.Errorf("combining Topology/sample: inc-host = %+v, want it as a node of its own", host)
	}

	if combined.Source.Kind != bdoc.SourceKindManual {
		t.Errorf("combining Topology/sample: source kind = %q, want %q", combined.Source.Kind, bdoc.SourceKindManual)
	}

	if diff := cmp.Diff([]string{"gone"}, combined.Source.IncludeTopologies); diff != "" {
		t.Errorf("combining Topology/sample: includeTopologies mismatch (-want +got):\n%s", diff)
	}

	for _, want := range []string{
		"The Builder copied 1 node from included topology shared (1 node).",
		"The Builder did not combine included topology gone. It stays in includeTopologies, and publishing keeps the reference.",
		legacyDecorations,
	} {
		if !slices.ContainsFunc(combined.Source.Warnings, func(warning string) bool { return strings.Contains(warning, want) }) {
			t.Errorf("combining Topology/sample: warnings = %q, want one containing %q", combined.Source.Warnings, want)
		}
	}

	// Neither draft replaces the legacy topology: publishing refuses it as
	// any draft that was not imported from the topology, and the diagram stays.
	for name, document := range map[string]*bdoc.Document{"copy": copied, "combined": combined} {
		t.Run(name, func(t *testing.T) {
			draft := createBuilderPublishDraft(t, harness, document, "")

			const want = "topology sample is not the source of this draft"

			_, refusal := publishBuilderDraft(t, harness, draft,
				`{"mode":"topology","topology":{"name":"sample","action":"update"}}`, http.StatusConflict)
			if refusal != want {
				t.Errorf("publishing the %s to sample: refusal = %q, want %q", name, refusal, want)
			}
		})
	}

	if !harness.configs[0].HasAnnotation(bdoc.LegacyXMLAnnotation) || harness.configs[0].HasAnnotation(bapi.DocumentAnnotation) {
		t.Errorf("Topology/sample annotations = %v, want the legacy diagram untouched", harness.configs[0].Metadata.Annotations)
	}

	if harness.configWrites != 0 {
		t.Errorf("generating and publishing wrote %d configs, want none", harness.configWrites)
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
	if diff := cmp.Diff(want, counts(groups.Topologies)); diff != "" {
		t.Errorf("GET /builder/sources: topology include counts mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]int{"exp": 0}, counts(groups.Experiments)); diff != "" {
		t.Errorf("GET /builder/sources: experiment include counts mismatch (-want +got):\n%s", diff)
	}

	// A topology that includes nothing has no count at all.
	_, body := builderListSourcesBody(t, harness, nil)

	var decoded map[string][]map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding GET /builder/sources: %v", err)
	}

	given := map[string]any{}

	for _, sources := range decoded {
		for _, source := range sources {
			if count, ok := source["includeCount"]; ok {
				given[fmt.Sprint(source["fullName"])] = count
			}
		}
	}

	if diff := cmp.Diff(map[string]any{"Topology/root": 3.0, "Topology/listed": 1.0}, given); diff != "" {
		t.Errorf("GET /builder/sources: includeCount fields mismatch (-want +got):\n%s", diff)
	}

	// The caller may list every topology, but get only root.
	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list"}),
		builderPolicy([]string{"configs"}, []string{"Topology/root"}, []string{"get"}),
		builderPolicy([]string{"topologies"}, []string{"*"}, []string{"list"}),
	)

	want = map[string]int{"root": 3, "visible": 0, "secret": 0, "listed": 0}
	if diff := cmp.Diff(want, counts(builderListSources(t, harness, role).Topologies)); diff != "" {
		t.Errorf("GET /builder/sources for a caller who may get only root: include counts mismatch (-want +got):\n%s", diff)
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
	if want := []string{kindExperiment, builderKindTopology}; !slices.Equal(lists, want) {
		t.Errorf("GET /builder/documents listed the kinds %q, want %q once each", harness.configLists, want)
	}

	if harness.configGets != 0 {
		t.Errorf("GET /builder/documents got %d configs, want none", harness.configGets)
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

		if listed[document.ID] != current[document.ID] {
			t.Errorf("GET /builder/documents: document %s of %s listed without its bytes = %t, want %t",
				document.ID, document.Target, listed[document.ID], current[document.ID])
		}

		if (got == http.StatusOK) != current[document.ID] {
			t.Errorf("GET /builder/documents/%s of %s: status = %d, want %d only when current and allowed",
				document.ID, document.Target, got, http.StatusOK)
		}
	}
}

func TestBuilderGetDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	document := builderPublish(t, harness, "topo")

	var response builderDocumentResponse

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents/" + document.ID, user: builderAlice},
		http.StatusOK, &response)

	if response.Config != "Topology/topo" {
		t.Errorf("GET /builder/documents/%s: config = %q, want Topology/topo", document.ID, response.Config)
	}

	var want, got any

	if err := json.Unmarshal(builderDocument(t, "topo"), &want); err != nil {
		t.Fatalf("decoding the published document: %v", err)
	}

	if err := json.Unmarshal(response.Document, &got); err != nil {
		t.Errorf("GET /builder/documents/%s: document %s is not JSON: %v", document.ID, response.Document, err)
	} else if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET /builder/documents/%s: document mismatch (-want +got):\n%s", document.ID, diff)
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
// phenix store whose config hooks run. With records, records are kept there.
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

// documentsKept returns an error naming each of the published documents that
// is still stored, or nil.
func (h *builderHarness) documentsKept(documents []*bapi.PublishedDocument) error {
	var kept []error

	for _, document := range documents {
		if _, err := h.service.GetPublishedDocument(h.t.Context(), document.ID); !errors.Is(err, bapi.ErrNotFound) {
			kept = append(
				kept,
				fmt.Errorf("GetPublishedDocument(%s) of %s error = %w, want %w", document.ID, document.Target, err, bapi.ErrNotFound),
			)
		}
	}

	return errors.Join(kept...)
}

// documentTag returns the entity tag of the published document id, as GET
// /builder/documents/{id} answers alice with it.
func (h *builderHarness) documentTag(id string) string {
	h.t.Helper()

	var response builderDocumentResponse

	h.doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents/" + id, user: builderAlice},
		http.StatusOK, &response)

	if response.ETag == "" {
		h.t.Fatalf("GET /builder/documents/%s: etag is empty", id)
	}

	return response.ETag
}

// renameBody returns the body of the PUT /configs that renames the stored
// topology name as renamed, keeping everything else it holds.
func (h *builderHarness) renameBody(name, renamed string) string {
	h.t.Helper()

	stored, err := config.Get(builderKindTopology + "/" + name)
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
	remove := builderRequest{
		method: http.MethodDelete, path: "/builder/documents/" + documents[1].ID, user: builderAlice,
		ifMatch: harness.documentTag(documents[1].ID),
	}

	if recorder := harness.doDecode(remove, http.StatusNoContent, nil); recorder.Header().Get("Warning") != "" {
		t.Errorf("DELETE %s: Warning = %q, want none", remove.path, recorder.Header().Get("Warning"))
	}

	if err := harness.documentsKept(documents); err != nil {
		t.Errorf("after DELETE %s: %v", remove.path, err)
	}

	if _, err := config.Get("Topology/topo"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("config.Get(Topology/topo) error = %v, want %v", err, store.ErrNotExist)
	}

	if _, _, err := harness.service.GetPublishedDocumentData(t.Context(), kept[1].ID); err != nil {
		t.Errorf("GetPublishedDocumentData(%s) of another topology returned error: %v, want it kept", kept[1].ID, err)
	}

	harness.doDecode(remove, http.StatusNotFound, nil)
}

// TestBuilderDeleteDocumentWarnsWithConfigHook asserts a delete whose
// document removal fails deletes the topology, keeps the documents whole, and
// answers with the warning.
func TestBuilderDeleteDocumentWarnsWithConfigHook(t *testing.T) {
	records := memrecord.New()
	harness := newBuilderStoreHarness(t, records)
	documents := harness.publishTopology("topo")

	records.FailDelete = func(namespace, _ string) error {
		if namespace == bapi.NamespaceContent {
			return errors.New("injected content delete failure")
		}

		return nil
	}

	recorder := harness.doDecode(builderRequest{
		method: http.MethodDelete, path: "/builder/documents/" + documents[1].ID, user: builderAlice,
		ifMatch: harness.documentTag(documents[1].ID),
	}, http.StatusNoContent, nil)

	const warning = `199 phenix "delete published topology succeeded, but phenix could not remove the superseded content"`

	if got := recorder.Header().Get("Warning"); got != warning {
		t.Errorf("DELETE /builder/documents/%s: Warning = %q, want %q", documents[1].ID, got, warning)
	}

	if _, err := config.Get("Topology/topo"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("config.Get(Topology/topo) error = %v, want %v", err, store.ErrNotExist)
	}

	// A document whose content could not be removed stays whole, for the
	// next cleanup to remove.
	for _, document := range documents {
		if _, _, err := harness.service.GetPublishedDocumentData(t.Context(), document.ID); err != nil {
			t.Errorf("GetPublishedDocumentData(%s) after the failed removal returned error: %v, want it whole", document.ID, err)
		}
	}
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

	ids := make([]string, 0, len(listed.Documents))
	for _, document := range listed.Documents {
		ids = append(ids, document.ID)
	}

	if want := []string{kept[1].ID}; !slices.Equal(ids, want) {
		t.Errorf("GET /builder/documents lists %q, want only %q of the topology kept", ids, want)
	}

	builders := map[string]string{}
	for _, source := range builderListSources(t, harness, nil).Topologies {
		builders[source.Name] = source.Builder
	}

	if diff := cmp.Diff(map[string]string{"kept": bapi.DocumentAnnotation, "renamed": ""}, builders); diff != "" {
		t.Errorf("GET /builder/sources: topology builders mismatch (-want +got):\n%s", diff)
	}
}

// TestConfigRoutesWaitForBuilderPublishing asserts deleting, renaming or
// changing the kind of a topology through /configs waits for the Builder
// publish lock. Other changes do not.
func TestConfigRoutesWaitForBuilderPublishing(t *testing.T) {
	harness := newBuilderStoreHarness(t, nil)

	harness.publishTopology("deleted")
	harness.publishTopology("topo")

	scenario := builderConfig(t, "Scenario", "scenario")
	if err := store.Create(&scenario); err != nil {
		t.Fatalf("storing the scenario returned error: %v", err)
	}

	// A body of another kind, under the name of the path, replaces the topology.
	other := builderConfig(t, "Scenario", "renamed")
	other.Version = "phenix.sandia.gov/v2"
	other.Spec = map[string]any{"apps": []any{}}

	rekinded, err := json.Marshal(other)
	if err != nil {
		t.Fatalf("encoding the scenario returned error: %v", err)
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
		{
			name: "changing the kind of a topology",
			request: builderRequest{
				method: http.MethodPut, path: "/configs/topology/renamed",
				body: string(rekinded), contentType: "application/json",
			},
			waits: true,
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
				patience = builderLockPatience
			}

			var recorder *httptest.ResponseRecorder

			select {
			case recorder = <-answered:
				builderPublishLock.Unlock()

				if test.waits {
					t.Errorf("%s %s answered %d while a publication held the lock", test.request.method, test.request.path, recorder.Code)
				}
			case <-time.After(patience):
				builderPublishLock.Unlock()

				if !test.waits {
					t.Errorf("%s %s waited for the publish lock", test.request.method, test.request.path)
				}

				recorder = <-answered
			}

			if recorder.Code != http.StatusNoContent {
				t.Errorf(
					"%s %s: status = %d, want %d: %s",
					test.request.method,
					test.request.path,
					recorder.Code,
					http.StatusNoContent,
					recorder.Body,
				)
			}
		})
	}
}

// TestBuilderDeleteDocumentRefusals asserts a published topology is deleted
// only with configs delete for it, while current and at the tag the caller
// read, and an experiment never.
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
		name    string
		role    *rbac.Role
		stale   bool
		changed bool
		bare    bool
		kind    string
		status  int
		reason  string
	}{
		{name: "no configs delete", role: readers, status: http.StatusForbidden},
		{name: "configs delete for another topology", role: otherDeleter, status: http.StatusForbidden},
		{name: "hidden", role: hidden, status: http.StatusNotFound},
		{name: "published again since", stale: true, status: http.StatusNotFound},
		{name: "changed since it was read", changed: true, status: http.StatusPreconditionFailed},
		{name: "no If-Match", bare: true, status: http.StatusBadRequest},
		{
			name: "experiment", kind: kindExperiment, status: http.StatusUnprocessableEntity,
			reason: "You can delete only published topologies here. Delete experiments from the Experiments page.",
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

			// The tag of the document as listed. Then the topology may change
			// as another user would change it.
			ifMatch, err := builderDocumentETag(document, &config)
			if err != nil {
				t.Fatalf("builderDocumentETag returned error: %v", err)
			}

			if test.changed {
				harness.configs[len(harness.configs)-1].Metadata.Annotations["team"] = "ops"
			}

			if test.bare {
				ifMatch = ""
			}

			var refusal builderErrorBody

			recorder := harness.doDecode(builderRequest{
				method: http.MethodDelete, path: "/builder/documents/" + document.ID, user: builderAlice, role: test.role,
				ifMatch: ifMatch,
			}, test.status, &refusal)

			if test.reason != "" && refusal.Message != test.reason {
				t.Errorf("DELETE /builder/documents/%s: message = %q, want %q", document.ID, refusal.Message, test.reason)
			}

			if test.status == http.StatusPreconditionFailed {
				want := builderErrorBody{Code: string(bdoc.CodeRequestStale), Message: "topology topo changed after you last read it"}
				if diff := cmp.Diff(want, refusal, cmpopts.IgnoreFields(builderErrorBody{}, "Cause", "Metadata", "Issues")); diff != "" {
					t.Errorf("DELETE /builder/documents/%s: refusal mismatch (-want +got):\n%s", document.ID, diff)
				}

				if got, want := recorder.Header().Get("ETag"), harness.documentTag(document.ID); got != want {
					t.Errorf("DELETE /builder/documents/%s: ETag = %q, want %q", document.ID, got, want)
				}
			}

			if _, err := harness.getConfig(config.FullName()); err != nil {
				t.Errorf("after a refused delete: getConfig(%s) returned error: %v", config.FullName(), err)
			}

			if _, err := harness.service.GetPublishedDocument(t.Context(), document.ID); err != nil {
				t.Errorf("after a refused delete: GetPublishedDocument(%s) returned error: %v", document.ID, err)
			}
		})
	}
}

// TestBuilderSourceKindFor finds each kind a document can name as a source,
// and no other kind.
func TestBuilderSourceKindFor(t *testing.T) {
	type found struct {
		Key         string
		Generatable bool
		OK          bool
	}

	tests := []struct {
		kind string
		want found
	}{
		{kind: builderKindTopology, want: found{Key: builderTopologies, Generatable: true, OK: true}},
		{kind: kindExperiment, want: found{Key: builderExperiments, Generatable: true, OK: true}},
		{kind: builderKindScenario, want: found{Key: builderScenarios, OK: true}},
		{kind: "Image", want: found{Key: "images", OK: true}},
		{kind: "User"},
		{kind: "topology"},
		{kind: ""},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			entry, ok := builderSourceKindFor(tt.kind)

			if diff := cmp.Diff(tt.want, found{Key: entry.key, Generatable: entry.generatable, OK: ok}); diff != "" {
				t.Errorf("builderSourceKindFor(%q) mismatch (-want +got):\n%s", tt.kind, diff)
			}
		})
	}
}

// TestBuilderKindAllowed checks the kind specific list permission of each
// source resource, and refuses a resource it does not know.
func TestBuilderKindAllowed(t *testing.T) {
	role := *nonBuilderRole(
		builderPolicy([]string{"topologies"}, []string{"topo"}, []string{"list"}),
		builderPolicy([]string{"experiments", "scenarios"}, []string{"*"}, []string{"list"}),
	)

	tests := []struct {
		name, resource string
		names          []string
		want           bool
	}{
		{name: "a listed topology", resource: builderTopologies, names: []string{"topo"}, want: true},
		{name: "another topology", resource: builderTopologies, names: []string{"other"}},
		{name: "an experiment", resource: builderExperiments, names: []string{"exp"}, want: true},
		{name: "a scenario", resource: builderScenarios, names: []string{"sc"}, want: true},
		{name: "a kind with no permission of its own", resource: "", names: []string{"img"}, want: true},
		{name: "an unknown resource", resource: "images", names: []string{"img"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := builderKindAllowed(role, tt.resource, tt.names...); got != tt.want {
				t.Errorf("builderKindAllowed(%q, %q) = %t, want %t", tt.resource, tt.names, got, tt.want)
			}
		})
	}
}

// TestNewBuilderSourceResponse describes a config as a source: its digest
// when a document can be generated from it, and the diagram it carries.
func TestNewBuilderSourceResponse(t *testing.T) {
	plain := builderConfig(t, builderKindTopology, "plain")
	published := builderConfig(t, builderKindTopology, "published")
	published.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: "ref"}
	legacy := builderConfig(t, builderKindTopology, "legacy")
	legacy.Metadata.Annotations = store.Annotations{bdoc.LegacyXMLAnnotation: "<mxfile/>"}
	scenario := builderConfig(t, builderKindScenario, "sc")

	digest := func(config store.Config) string {
		t.Helper()

		value, err := bdoc.ImportDigest(config)
		if err != nil {
			t.Fatalf("ImportDigest(%s) returned error: %v", config.FullName(), err)
		}

		return value
	}

	source := func(config store.Config, stored bool, builder string, digest string) builderSourceResponse {
		return builderSourceResponse{
			Kind: config.Kind, Name: config.Metadata.Name, FullName: config.FullName(), APIVersion: config.Version,
			Digest: digest, Stored: stored, Builder: builder, Generatable: digest != "",
		}
	}

	tests := []struct {
		name   string
		config store.Config
		stored bool
		want   builderSourceResponse
	}{
		{name: "a topology", config: plain, stored: true, want: source(plain, true, "", digest(plain))},
		{
			name: "a published topology", config: published, stored: true,
			want: source(published, true, bapi.DocumentAnnotation, digest(published)),
		},
		{
			name: "an uploaded legacy topology", config: legacy,
			want: source(legacy, false, bdoc.LegacyXMLAnnotation, digest(legacy)),
		},
		{name: "a scenario", config: scenario, stored: true, want: source(scenario, true, "", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newBuilderSourceResponse(&tt.config, tt.stored)
			if err != nil {
				t.Fatalf("newBuilderSourceResponse(%s) returned error: %v", tt.config.FullName(), err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("newBuilderSourceResponse(%s) mismatch (-want +got):\n%s", tt.config.FullName(), diff)
			}
		})
	}

	undigestable := builderConfig(t, builderKindTopology, "nan")
	undigestable.Spec["weight"] = math.NaN()

	if _, err := newBuilderSourceResponse(&undigestable, true); err == nil {
		t.Errorf("newBuilderSourceResponse(Topology/nan) = nil error, want one for a spec with no digest")
	}
}

// TestBuilderSourcesStoreFailures answers 500 when the store cannot list or
// read a source, or a stored source has no digest.
func TestBuilderSourcesStoreFailures(t *testing.T) {
	undigestable := builderConfig(t, builderKindTopology, "nan")
	undigestable.Spec["weight"] = math.NaN()

	offline := errors.New("store offline")

	tests := []struct {
		name    string
		configs []store.Config
		list    string
		get     string
		request builderRequest
		want    string
	}{
		{
			name: "a listing", list: "experiment",
			request: builderRequest{method: http.MethodGet, path: "/builder/sources"},
			want:    "unable to list Experiment configs",
		},
		{
			name: "a source with no digest", configs: []store.Config{undigestable},
			request: builderRequest{method: http.MethodGet, path: "/builder/sources"},
			want:    "unable to describe Topology/nan config",
		},
		{
			name: "a read of the source", get: "Topology/broken",
			request: builderRequest{method: http.MethodPost, path: "/builder/generate", body: `{"source":"Topology/broken"}`},
			want:    "unable to get config Topology/broken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderHarness(t, tt.configs...)
			harness.reroute(withBuilderConfigs(
				func(kind string) (store.Configs, error) {
					if kind == tt.list {
						return nil, offline
					}

					return harness.listConfigs(kind)
				},
				func(name string) (*store.Config, error) {
					if name == tt.get {
						return nil, offline
					}

					return harness.getConfig(name)
				},
			))

			request := tt.request
			request.user = builderAlice

			recorder := harness.do(request)
			if err := builderStatusError(recorder, http.StatusInternalServerError); err != nil {
				t.Fatalf("%s %s: %v", request.method, request.path, err)
			}

			if got := builderRefusal(t, recorder).Message; got != tt.want {
				t.Errorf("%s %s: message = %q, want %q", request.method, request.path, got, tt.want)
			}
		})
	}
}

// TestIncludedTopologyLoader reads an included topology from the config
// store only, and only one the caller may read.
func TestIncludedTopologyLoader(t *testing.T) {
	shared := builderConfig(t, builderKindTopology, "shared")
	offline := errors.New("store offline")

	api := &builderAPI{getConfig: func(name string) (*store.Config, error) {
		switch name {
		case shared.FullName():
			return &shared, nil
		case "Topology/broken":
			return nil, offline
		}

		return nil, store.ErrNotExist
	}}

	limited := nonBuilderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"get"}),
		builderPolicy([]string{"topologies"}, []string{"other"}, []string{"list"}),
	)

	tests := []struct {
		name, include string
		role          *rbac.Role
		want          string
		is            error
	}{
		{name: "a stored topology", include: "shared", want: "Topology/shared"},
		{name: "a file path", include: "dir/shared.yml"},
		{name: "a Windows file path", include: `dir\shared.yml`},
		{name: "a topology the caller may not list", include: "shared", role: limited, is: errBuilderIncludeForbidden},
		{name: "a missing topology", include: "absent"},
		{name: "a store failure", include: "broken", is: offline},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role := builderFullRole()
			if tt.role != nil {
				role = tt.role
			}

			config, err := api.includedTopologyLoader(builderActor{user: builderAlice, role: *role})(tt.include)

			switch {
			case tt.want != "":
				if err != nil || config == nil || config.FullName() != tt.want {
					t.Errorf("loading %q = %v, %v, want %s", tt.include, config, err, tt.want)
				}
			case err == nil:
				t.Errorf("loading %q = %v, want an error", tt.include, config)
			case tt.is != nil && !errors.Is(err, tt.is):
				t.Errorf("loading %q = %v, want %v", tt.include, err, tt.is)
			case tt.is == nil && errors.Is(err, errBuilderIncludeForbidden):
				t.Errorf("loading %q = %v, want an error other than a refusal of permission", tt.include, err)
			}
		})
	}
}

// FuzzBuilderUploadedSource asserts an uploaded config file is either a
// config of a known kind with a name, or refused with 413 or 422.
func FuzzBuilderUploadedSource(f *testing.F) {
	f.Add(`{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"lab"},"spec":{"nodes":[]}}`)
	f.Add("apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: lab\n")
	f.Add("kind: Nope\nmetadata:\n  name: lab\n")
	f.Add("\tnot: [valid")
	f.Add("  {")

	f.Fuzz(func(t *testing.T, content string) {
		config, err := builderUploadedSource(content)
		if err == nil {
			if config.Metadata.Name == "" || store.ConfigFullName(config.Kind, config.Metadata.Name) == "" {
				t.Errorf("builderUploadedSource(%q) = %s, want a known kind and a name", content, config.FullName())
			}

			return
		}

		var werr *weberror.WebError
		if !errors.As(err, &werr) {
			t.Fatalf("builderUploadedSource(%q) = %v, want a *weberror.WebError", content, err)
		}

		if werr.Status != http.StatusUnprocessableEntity && werr.Status != http.StatusRequestEntityTooLarge {
			t.Errorf("builderUploadedSource(%q): status = %d, want 422 or 413", content, werr.Status)
		}
	})
}
