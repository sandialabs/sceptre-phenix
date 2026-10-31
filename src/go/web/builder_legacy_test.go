package web

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gopkg.in/yaml.v3"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
)

// The messages a conversion of the captured sample diagram warns with.
const (
	legacyAddedSwitch = "The Builder added a switch for 1 network that had none in the legacy diagram: b."
	legacyLeftOutLine = "The Builder did not import 1 line that was not a network link."
	legacyDecorations = "The Builder kept text and containers as notes and groups. " +
		"The Builder did not convert their colors, fonts and other formatting."
	legacyReplaced   = "This diagram replaced the legacy Builder diagram of topology sample."
	legacyNotDiagram = "this is not a legacy Builder diagram. A legacy diagram is mxGraph XML, " +
		"or a Topology config with the builder-xml annotation"
)

// legacySampleFile reads a file of the captured legacy diagram, which the
// document package keeps as test data.
func legacySampleFile(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "types", "builder", "testdata", "legacy", name))
	if err != nil {
		t.Fatalf("reading the legacy sample: %v", err)
	}

	return data
}

// legacySampleTopology is the topology the legacy Builder saved for the
// captured sample, with its builder-xml annotation and one of the user's.
func legacySampleTopology(t *testing.T) store.Config {
	t.Helper()

	var capture struct {
		Posts []struct {
			Body string `json:"body"`
		} `json:"posts"`
	}

	if err := json.Unmarshal(legacySampleFile(t, "sample-capture.json"), &capture); err != nil {
		t.Fatalf("decoding the capture: %v", err)
	}

	if len(capture.Posts) != 1 {
		t.Fatalf("the capture holds %d posts, want 1", len(capture.Posts))
	}

	var saved struct {
		Name       string         `json:"name"`
		Topology   map[string]any `json:"topology"`
		BuilderXML string         `json:"builderXML"`
	}

	if err := json.Unmarshal([]byte(capture.Posts[0].Body), &saved); err != nil {
		t.Fatalf("decoding what the legacy Builder posted: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"apiVersion": "phenix.sandia.gov/v1",
		"kind":       builderKindTopology,
		"metadata": map[string]any{
			"name":        saved.Name,
			"annotations": map[string]any{bdoc.LegacyXMLAnnotation: saved.BuilderXML, "team": "ops"},
		},
		"spec": saved.Topology,
	})
	if err != nil {
		t.Fatalf("encoding the topology: %v", err)
	}

	config, err := store.DecodeConfigJSON(body)
	if err != nil {
		t.Fatalf("DecodeConfigJSON returned error: %v", err)
	}

	return *config
}

// legacyResponse is the JSON view POST /builder/legacy returns.
type legacyResponse struct {
	Document json.RawMessage        `json:"document"`
	Warnings []string               `json:"warnings"`
	Source   *builderSourceResponse `json:"source"`
}

// postBuilderLegacy posts a conversion request with the given role (the full
// role when nil).
func postBuilderLegacy(
	t *testing.T,
	harness *builderHarness,
	role *rbac.Role,
	request map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/legacy", body: string(body), user: builderAlice, role: role,
	})

	if err := builderJSONError(recorder); err != nil {
		t.Errorf("POST /builder/legacy: %v", err)
	}

	return recorder
}

// convertBuilderLegacy is postBuilderLegacy for a conversion that must
// succeed, and returns the response with its document.
func convertBuilderLegacy(
	t *testing.T,
	harness *builderHarness,
	request map[string]any,
) (legacyResponse, *bdoc.Document) {
	t.Helper()

	recorder := postBuilderLegacy(t, harness, nil, request)
	if err := builderStatusError(recorder, http.StatusOK); err != nil {
		t.Fatalf("POST /builder/legacy: %v", err)
	}

	// A conversion stores nothing.
	if got := recorder.Header().Get("ETag"); got != "" {
		t.Errorf("POST /builder/legacy: ETag = %q, want none", got)
	}

	var response legacyResponse

	decodeBuilderAnswer(t, recorder, &response)

	document, err := bdoc.Parse(response.Document)
	if err != nil {
		t.Fatalf("the converted document is not valid: %v", err)
	}

	return response, document
}

// legacyPosition returns the position of the device hostname of document.
func legacyPosition(t *testing.T, document *bdoc.Document, hostname string) bdoc.Position {
	t.Helper()

	node := document.FindDevice(hostname)
	if node == nil {
		t.Fatalf("the document has no device %s", hostname)
	}

	return node.Position
}

// TestBuilderConvertLegacyDiagram converts a bare diagram to a document that
// names no source config, and writes nothing.
func TestBuilderConvertLegacyDiagram(t *testing.T) {
	harness := newBuilderHarness(t, builderConfig(t, builderKindTopology, "sample"))
	sample := string(legacySampleFile(t, "sample.xml"))
	gets, lists := harness.configGets, len(harness.configLists)

	before := time.Now().UTC().Truncate(time.Second)
	response, document := convertBuilderLegacy(t, harness, map[string]any{"content": sample, "name": " branch-office "})

	if response.Source != nil {
		t.Errorf("POST /builder/legacy (a bare diagram): source = %+v, want none", response.Source)
	}

	want := []string{legacyAddedSwitch, legacyLeftOutLine, legacyDecorations}
	if diff := cmp.Diff(want, response.Warnings); diff != "" {
		t.Errorf("POST /builder/legacy (a bare diagram): warnings mismatch (-want +got):\n%s", diff)
	}

	if got := document.Metadata.Name; got != "branch-office" {
		t.Errorf("POST /builder/legacy (a bare diagram): name = %q, want %q", got, "branch-office")
	}

	// A source that names no config, with the warnings.
	wantSource := &bdoc.Source{Kind: bdoc.SourceKindManual, Warnings: want}
	if diff := cmp.Diff(wantSource, document.Source, cmpopts.IgnoreFields(bdoc.Source{}, "ImportedAt")); diff != "" {
		t.Errorf("POST /builder/legacy (a bare diagram): document source mismatch (-want +got):\n%s", diff)
	}

	imported, err := time.Parse(time.RFC3339, document.Source.ImportedAt)
	if err != nil || imported.Before(before) || imported.After(time.Now().UTC()) {
		t.Errorf("POST /builder/legacy (a bare diagram): importedAt = %q (%v), want the time of the request",
			document.Source.ImportedAt, err)
	}

	if got := legacyPosition(t, document, "router-device-0"); got != (bdoc.Position{X: 80, Y: 112}) {
		t.Errorf("legacyPosition(router-device-0) = %+v, want %+v of the diagram", got, bdoc.Position{X: 80, Y: 112})
	}

	// Without a name, and with a blank one, the document gets the default.
	for _, tt := range []struct {
		name    string
		request map[string]any
	}{
		{name: "no name", request: map[string]any{"content": sample}},
		{name: "a blank name", request: map[string]any{"content": "\xef\xbb\xbf \n" + sample, "name": "  "}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, unnamed := convertBuilderLegacy(t, harness, tt.request); unnamed.Metadata.Name != "legacy-diagram" {
				t.Errorf("POST /builder/legacy (%s): name = %q, want %q", tt.name, unnamed.Metadata.Name, "legacy-diagram")
			}
		})
	}

	// An empty diagram is converted too.
	empty, blank := convertBuilderLegacy(t, harness, map[string]any{"content": "<mxGraphModel/>"})
	if len(blank.Nodes) != 0 {
		t.Errorf("POST /builder/legacy (an empty diagram) made %d nodes, want 0", len(blank.Nodes))
	}

	if diff := cmp.Diff([]string{"The diagram has no nodes."}, empty.Warnings); diff != "" {
		t.Errorf("POST /builder/legacy (an empty diagram): warnings mismatch (-want +got):\n%s", diff)
	}

	// Converting reads no config and writes none.
	type configUse struct {
		Writes, Configs, Gets int
		Lists                 []string
	}

	wantUse := configUse{Configs: 1, Gets: gets, Lists: harness.configLists[:lists]}
	gotUse := configUse{Writes: harness.configWrites, Configs: len(harness.configs), Gets: harness.configGets, Lists: harness.configLists}

	if diff := cmp.Diff(wantUse, gotUse); diff != "" {
		t.Errorf("POST /builder/legacy: config use mismatch, want no config read or written (-want +got):\n%s", diff)
	}

	// The document is what a draft is made from, with the token of a bare
	// diagram, which never lets the draft update a stored config.
	var draft builderDraftResponse

	harness.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderAlice,
		body: `{"sourceToken":"uploaded/legacy-xml","document":` + string(response.Document) + `}`,
	}, http.StatusCreated, &draft)

	_, refusal := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"sample","action":"update"}}`, http.StatusConflict)
	if want := "topology sample is not the source of this draft"; refusal != want {
		t.Errorf("publishing the draft of a bare diagram over topology sample: refusal = %q, want %q", refusal, want)
	}

	if harness.configWrites != 0 {
		t.Errorf("the refused publication wrote %d configs, want 0", harness.configWrites)
	}

	if harness.configs[0].HasAnnotation(bapi.DocumentAnnotation) {
		t.Errorf("the refused publication left %s on the topology", bapi.DocumentAnnotation)
	}
}

// TestBuilderConvertLegacyTopology converts a Topology config with a legacy
// diagram, as YAML and as JSON, placing its nodes as the diagram does.
func TestBuilderConvertLegacyTopology(t *testing.T) {
	topology := legacySampleTopology(t)

	asYAML, err := yaml.Marshal(topology)
	if err != nil {
		t.Fatalf("encoding the topology as YAML: %v", err)
	}

	asJSON, err := json.Marshal(topology)
	if err != nil {
		t.Fatalf("encoding the topology as JSON: %v", err)
	}

	// A Builder document reference beside the diagram does not matter: the
	// caller asked for the legacy diagram.
	published := legacySampleTopology(t)
	published.Metadata.Annotations[bapi.DocumentAnnotation] = `{"digest":"sha256:` + strings.Repeat("0", 64) + `"}`

	both, err := json.Marshal(published)
	if err != nil {
		t.Fatalf("encoding the published topology: %v", err)
	}

	// The digest of an import covers the diagram it read.
	digest, err := bdoc.ImportDigest(topology)
	if err != nil {
		t.Fatalf("ImportDigest returned error: %v", err)
	}

	for name, content := range map[string]string{
		"YAML": string(asYAML), "JSON": string(asJSON), "JSON with a document reference": string(both),
	} {
		t.Run(name, func(t *testing.T) {
			harness := newBuilderHarness(t)

			// The name is that of the topology, whatever the request says.
			response, document := convertBuilderLegacy(t, harness, map[string]any{"content": content, "name": "ignored"})

			// The uploaded topology, with its legacy diagram and digest.
			wantSource := &builderSourceResponse{
				Kind: builderKindTopology, Name: "sample", FullName: "Topology/sample", APIVersion: "phenix.sandia.gov/v1",
				Builder: bdoc.LegacyXMLAnnotation, Digest: digest, Generatable: true,
			}
			if diff := cmp.Diff(
				wantSource,
				response.Source,
				cmpopts.IgnoreFields(builderSourceResponse{}, "Created", "Updated"),
			); diff != "" {
				t.Errorf("POST /builder/legacy (%s): source mismatch (-want +got):\n%s", name, diff)
			}

			if got := document.Metadata.Name; got != "sample" {
				t.Errorf("POST /builder/legacy (%s): name = %q, want %q", name, got, "sample")
			}

			// The annotations are the topology's without the diagram.
			wantDocumentSource := &bdoc.Source{
				Kind: bdoc.SourceKindTopology, Name: "sample", APIVersion: "phenix.sandia.gov/v1", Digest: digest,
				Annotations: map[string]string{"team": "ops"},
				Warnings:    []string{legacyAddedSwitch, legacyLeftOutLine, legacyDecorations},
			}
			if diff := cmp.Diff(wantDocumentSource, document.Source, cmpopts.IgnoreFields(bdoc.Source{}, "ImportedAt")); diff != "" {
				t.Errorf("POST /builder/legacy (%s): document source mismatch (-want +got):\n%s", name, diff)
			}

			if got := legacyPosition(t, document, "client-1"); got != (bdoc.Position{X: 80, Y: 832}) {
				t.Errorf("legacyPosition(client-1) = %+v, want %+v of the diagram", got, bdoc.Position{X: 80, Y: 832})
			}

			want := []string{legacyAddedSwitch, legacyLeftOutLine, legacyDecorations}
			if diff := cmp.Diff(want, response.Warnings); diff != "" {
				t.Errorf("POST /builder/legacy (%s): warnings mismatch (-want +got):\n%s", name, diff)
			}

			if harness.configWrites != 0 || harness.configGets != 0 {
				t.Errorf("POST /builder/legacy (%s): config writes = %d, gets = %d, want none",
					name, harness.configWrites, harness.configGets)
			}
		})
	}
}

// TestBuilderConvertLegacyTopologyResolvesIncludes asserts a converted topology's
// includes are read under the caller's permissions. legacy_test.go covers the rest.
func TestBuilderConvertLegacyTopologyResolvesIncludes(t *testing.T) {
	shared := builderConfig(t, builderKindTopology, "shared")
	shared.Spec["nodes"] = []any{includeNode("dns")}

	topology := legacySampleTopology(t)
	topology.Spec["includeTopologies"] = []any{"shared"}

	content, err := json.Marshal(topology)
	if err != nil {
		t.Fatalf("encoding the topology: %v", err)
	}

	harness := newBuilderHarness(t, shared)

	// A caller who may not read the included topology gets the rest, with a warning.
	role := builderRole(builderPolicy([]string{"configs"}, []string{"*"}, []string{"get", "create"}))

	recorder := postBuilderLegacy(t, harness, role, map[string]any{"content": string(content)})
	if err := builderStatusError(recorder, http.StatusOK); err != nil {
		t.Fatalf("POST /builder/legacy (a topology including shared): %v", err)
	}

	var response legacyResponse

	harness.decode(recorder, &response)

	const refused = `the Builder could not read included topology "shared"`

	if !strings.Contains(strings.Join(response.Warnings, "\n"), refused) {
		t.Errorf("POST /builder/legacy (a topology including shared): warnings = %q, want %q among them", response.Warnings, refused)
	}

	if strings.Contains(string(response.Document), `"dns"`) {
		t.Errorf("POST /builder/legacy (a topology including shared): the document holds the included node dns, want it left out")
	}
}

// TestBuilderConvertLegacyRequests asserts the HTTP refusals of a conversion,
// each with its exact message and no cause. legacy_test.go holds the others.
func TestBuilderConvertLegacyRequests(t *testing.T) {
	config := func(kind, name, rest string) string {
		return "apiVersion: phenix.sandia.gov/v1\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n" + rest
	}

	// A device with this many interfaces, each on a VLAN of its own.
	wide := func(count int) string {
		interfaces := make([]any, count)
		for i := range interfaces {
			interfaces[i] = map[string]any{"name": "eth" + strconv.Itoa(i), "vlan": "vlan" + strconv.Itoa(i)}
		}

		settings, err := json.Marshal(map[string]any{
			"device":  "server",
			"general": map[string]any{"hostname": "wide"},
			"network": map[string]any{"interfaces": interfaces},
		})
		if err != nil {
			t.Fatalf("encoding the settings: %v", err)
		}

		return `<root><mxCell id="0"/><mxCell id="1" parent="0"/><object id="2" label="wide" schemaVars="` +
			html.EscapeString(string(settings)) + `"><mxCell vertex="1" parent="1"/></object></root>`
	}

	for _, test := range []struct {
		name    string
		body    string
		content string
		status  int
		message string
	}{
		{name: "a body that is not JSON", body: `{"content":`, status: http.StatusBadRequest},
		{name: "an unknown field", body: `{"content":"<root/>","text":"x"}`, status: http.StatusBadRequest},
		{
			name: "no content", body: `{"name":"lab"}`,
			status: http.StatusBadRequest, message: "the request needs content",
		},
		{
			name: "a name that is too long", body: `{"content":"<root/>","name":"` + strings.Repeat("n", 513) + `"}`,
			status:  http.StatusBadRequest,
			message: "name must be at most 512 bytes and must not contain control characters",
		},
		{
			name: "a name with a control character", body: `{"content":"<root/>","name":"a\u0007b"}`,
			status:  http.StatusBadRequest,
			message: "name must be at most 512 bytes and must not contain control characters",
		},
		{
			name: "content beyond the limit", content: "<root>" + strings.Repeat(" ", bdoc.MaxLegacyBytes) + "</root>",
			status: http.StatusRequestEntityTooLarge, message: "the legacy diagram is larger than 5242880 bytes",
		},
		{
			// A switch, a network and an edge for each interface make the
			// document many times the size of the diagram.
			name: "a diagram whose document is beyond the limit", content: wide(8000),
			status: http.StatusRequestEntityTooLarge, message: "the converted diagram is larger than 5242880 bytes",
		},
		{name: "plain words", content: "hello", status: http.StatusUnprocessableEntity, message: legacyNotDiagram},
		{
			name: "a config without a name", content: "apiVersion: phenix.sandia.gov/v1\nkind: Topology\n",
			status: http.StatusUnprocessableEntity, message: legacyNotDiagram,
		},
		{
			name: "an Experiment config", content: config("Experiment", "exp", "spec: {}\n"),
			status:  http.StatusUnprocessableEntity,
			message: "only a Topology config can hold a legacy Builder diagram, not a Experiment config",
		},
		{
			name: "a Topology without a legacy diagram", content: config("topology", "plain", "spec:\n  nodes: []\n"),
			status: http.StatusUnprocessableEntity,
			message: "topology plain has no legacy Builder diagram (no builder-xml annotation). " +
				"Select Import to make a diagram from it",
		},
		{
			name: "a Topology the Builder cannot import",
			content: config("Topology", "broken",
				"  annotations:\n    builder-xml: <mxGraphModel/>\nspec:\n  nodes:\n    - general:\n        hostname: two words\n"),
			status:  http.StatusUnprocessableEntity,
			message: "unable to import a Builder document from Topology/broken",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newBuilderHarness(t)

			body := test.body
			if body == "" {
				encoded, err := json.Marshal(map[string]any{"content": test.content})
				if err != nil {
					t.Fatalf("encoding the request: %v", err)
				}

				body = string(encoded)
			}

			recorder := harness.do(builderRequest{
				method: http.MethodPost, path: "/builder/legacy", body: body, user: builderAlice,
			})
			if err := builderStatusError(recorder, test.status); err != nil {
				t.Errorf("POST /builder/legacy: %v", err)
			}

			refusal := builderRefusal(t, recorder)

			if test.message == "" {
				return
			}

			if refusal.Message != test.message {
				t.Errorf("POST /builder/legacy: message = %q, want %q", refusal.Message, test.message)
			}

			if refusal.Cause != "" {
				t.Errorf("POST /builder/legacy: cause = %q, want none", refusal.Cause)
			}
		})
	}
}

// TestBuilderConvertLegacyRequiresGetAndCreate asserts a conversion needs
// configs get and create, as importing a config file does.
func TestBuilderConvertLegacyRequiresGetAndCreate(t *testing.T) {
	harness := newBuilderHarness(t)
	request := map[string]any{"content": string(legacySampleFile(t, "sample.xml"))}

	for name, verbs := range map[string][]string{
		"get only":    {"list", "get", "update", "delete"},
		"create only": {"list", "create", "update", "delete"},
	} {
		t.Run(name, func(t *testing.T) {
			role := builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, verbs))

			recorder := postBuilderLegacy(t, harness, role, request)
			if err := builderStatusError(recorder, http.StatusForbidden); err != nil {
				t.Errorf("POST /builder/legacy with configs %q: %v", verbs, err)
			}

			if strings.Contains(recorder.Body.String(), "mxGraphModel") {
				t.Errorf("POST /builder/legacy with configs %q: the refusal repeats the content: %s", verbs, recorder.Body)
			}
		})
	}

	role := builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"get", "create"}))
	if err := builderStatusError(postBuilderLegacy(t, harness, role, request), http.StatusOK); err != nil {
		t.Errorf("POST /builder/legacy with configs get and create: %v", err)
	}

	// Without an identity at all.
	recorder := harness.do(builderRequest{method: http.MethodPost, path: "/builder/legacy", body: `{"content":"<root/>"}`})
	if recorder.Code != http.StatusForbidden {
		t.Errorf("POST /builder/legacy without an identity: status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

// TestBuilderConvertLegacyNeverRepeatsContent asserts no refusal and no log
// line holds anything of the content.
func TestBuilderConvertLegacyNeverRepeatsContent(t *testing.T) {
	// Short enough for a parser to quote it whole.
	const secret = "s3cr3t-xq"

	logs := plogtest.Capture(t)
	harness := newBuilderHarness(t)

	for name, content := range map[string]string{
		"malformed XML":           `<mxGraphModel ` + secret + `="1"><root><` + secret + `></root>`,
		"an undefined entity":     `<mxGraphModel><root><mxCell value="&` + secret + `;"/></root></mxGraphModel>`,
		"another encoding":        `<?xml version="1.0" encoding="` + secret + `"?><mxGraphModel/>`,
		"a DOCTYPE":               `<!DOCTYPE x [<!ENTITY a "` + secret + `">]><mxGraphModel/>`,
		"plain words":             secret,
		"YAML that fails":         secret + ": [",
		"YAML of the wrong shape": "kind: Topology\nmetadata: " + secret + "\n",
		"JSON that fails":         `{"kind": "Topology", "metadata": {"name": ` + secret + `}}`,
		"a config of another kind": "apiVersion: phenix.sandia.gov/v1\nkind: Experiment\nmetadata:\n  name: exp\n" +
			"  annotations:\n    note: " + secret + "\n",
		"a topology that is refused": "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: broken\n" +
			"  annotations:\n    builder-xml: <mxGraphModel/>\nspec:\n  nodes:\n    - general:\n        hostname: " +
			secret + " two\n",
		"a diagram that converts": `<mxGraphModel><root><mxCell id="0"/><object label="` + secret + `" id="2" ` +
			`schemaVars="{&quot;general&quot;:{&quot;hostname&quot;:&quot;` + secret + `&quot;}}">` +
			`<mxCell vertex="1" parent="0"/></object></root></mxGraphModel>`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := postBuilderLegacy(t, harness, nil, map[string]any{"content": content})

			converted := recorder.Code == http.StatusOK
			if !converted && recorder.Code != http.StatusUnprocessableEntity {
				t.Errorf("POST /builder/legacy: status = %d, want %d or %d: %s",
					recorder.Code, http.StatusOK, http.StatusUnprocessableEntity, recorder.Body)
			}

			if !converted && strings.Contains(recorder.Body.String(), secret) {
				t.Errorf("POST /builder/legacy: the refusal repeats the content: %s", recorder.Body)
			}

			if logged := logs.String(); strings.Contains(logged, secret) {
				t.Errorf("POST /builder/legacy: the log repeats the content: %s", logged)
			}

			logs.Take(t, nil)
		})
	}

	// What a conversion logs: who, which kind of input, and how many
	// warnings.
	convertBuilderLegacy(t, harness, map[string]any{"content": string(legacySampleFile(t, "sample.xml"))})

	records := logs.Take(t, plogtest.Message("converted legacy builder diagram"))

	logged := make([]map[string]any, 0, len(records))
	for _, record := range records {
		logged = append(logged, map[string]any{"user": record["user"], "kind": record["kind"], "warnings": record["warnings"]})
	}

	// One record with the user, the kind and the number of warnings.
	want := []map[string]any{{"user": builderAlice, "kind": "diagram", "warnings": float64(3)}}
	if diff := cmp.Diff(want, logged); diff != "" {
		t.Errorf("POST /builder/legacy (the sample diagram): log mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderConvertLegacyLeavesTheEnvironmentAlone resolves the $NAME of a
// bare diagram from the legacy defaults, never the server's environment.
func TestBuilderConvertLegacyLeavesTheEnvironmentAlone(t *testing.T) {
	t.Setenv("HOME", "/root/of-the-server")
	t.Setenv("DEFAULT_VCPU", "64")

	settings := `{"general":{"hostname":"host"},"hardware":{"vcpus":"$DEFAULT_VCPU"},` +
		`"commands":["cd $HOME","echo ${HOME}"]}`
	content := `<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/>` +
		`<object label="host" id="2" schemaVars="` + strings.ReplaceAll(settings, `"`, "&quot;") + `">` +
		`<mxCell vertex="1" parent="1"/></object></root></mxGraphModel>`

	harness := newBuilderHarness(t)
	response, document := convertBuilderLegacy(t, harness, map[string]any{"content": content})

	node := document.FindDevice("host")
	if node == nil {
		t.Fatalf("the document has no device host")
	}

	spec := node.Device.Spec

	// The commands as the diagram has them.
	if diff := cmp.Diff([]any{"cd $HOME", "echo ${HOME}"}, spec["commands"]); diff != "" {
		t.Errorf("POST /builder/legacy: commands mismatch (-want +got):\n%s", diff)
	}

	hardware, _ := spec["hardware"].(map[string]any)
	if vcpus := hardware["vcpus"]; vcpus != "1" {
		t.Errorf("POST /builder/legacy: vcpus = %v, want the legacy default 1", vcpus)
	}

	want := []string{"Used the legacy defaults for variables the diagram does not define: $DEFAULT_VCPU = 1."}
	if diff := cmp.Diff(want, response.Warnings); diff != "" {
		t.Errorf("POST /builder/legacy: warnings mismatch (-want +got):\n%s", diff)
	}
}

// legacyBuilders returns the builder annotation each source carries, in order.
func legacyBuilders(sources []builderSourceResponse) []string {
	builders := make([]string, 0, len(sources))
	for _, source := range sources {
		builders = append(builders, source.Builder)
	}

	return builders
}

// isDocumentReference reports whether key is the document reference, which a
// test decodes on its own, to leave it out of a comparison of annotations.
func isDocumentReference(key, _ string) bool {
	return key == bapi.DocumentAnnotation
}

// TestBuilderGenerateConvertsLegacyTopology imports a legacy topology, stored
// or uploaded, and reads no diagram of one the Builder published.
func TestBuilderGenerateConvertsLegacyTopology(t *testing.T) {
	topology := legacySampleTopology(t)
	harness := newBuilderHarness(t, topology)

	// The topology is marked as a legacy one.
	groups := builderListSources(t, harness, nil)
	if diff := cmp.Diff([]string{bdoc.LegacyXMLAnnotation}, legacyBuilders(groups.Topologies)); diff != "" {
		t.Errorf("GET /builder/sources: builders of the topologies mismatch (-want +got):\n%s", diff)
	}

	content, err := json.Marshal(topology)
	if err != nil {
		t.Fatalf("encoding the topology: %v", err)
	}

	stored := `{"source":"Topology/sample"}`
	uploaded := builderJSON(t, map[string]string{"content": string(content)})

	for name, body := range map[string]string{"stored": stored, "a config file": uploaded} {
		t.Run(name, func(t *testing.T) {
			var response builderGenerateResponse

			recorder := harness.do(builderRequest{method: http.MethodPost, path: "/builder/generate", body: body, user: builderAlice})
			if err := builderStatusError(recorder, http.StatusOK); err != nil {
				t.Fatalf("POST /builder/generate %s: %v", body, err)
			}

			decodeBuilderAnswer(t, recorder, &response)

			document, err := bdoc.Parse(response.Document)
			if err != nil {
				t.Fatalf("bdoc.Parse(the generated document) error: %v", err)
			}

			// Marked as a legacy topology.
			wantSource := builderSourceResponse{
				Kind: builderKindTopology, Name: "sample", FullName: "Topology/sample", APIVersion: "phenix.sandia.gov/v1",
				Digest: document.Source.Digest, Stored: body == stored, Builder: bdoc.LegacyXMLAnnotation, Generatable: true,
			}
			if diff := cmp.Diff(
				wantSource,
				response.Source,
				cmpopts.IgnoreFields(builderSourceResponse{}, "Created", "Updated"),
			); diff != "" {
				t.Errorf("POST /builder/generate %s: source mismatch (-want +got):\n%s", name, diff)
			}

			if got := legacyPosition(t, document, "server-device-5"); got != (bdoc.Position{X: 1280, Y: 512}) {
				t.Errorf("legacyPosition(server-device-5) = %+v, want %+v of the diagram", got, bdoc.Position{X: 1280, Y: 512})
			}

			want := []string{legacyAddedSwitch, legacyLeftOutLine, legacyDecorations}
			if diff := cmp.Diff(want, response.Warnings); diff != "" {
				t.Errorf("POST /builder/generate %s: warnings mismatch (-want +got):\n%s", name, diff)
			}

			if got := document.Source.Kind; got != bdoc.SourceKindTopology {
				t.Errorf("POST /builder/generate %s: document source kind = %q, want %q", name, got, bdoc.SourceKindTopology)
			}

			if document.Source.ImportedAt == "" {
				t.Errorf("POST /builder/generate %s: importedAt is empty, want the time of the import", name)
			}
		})
	}

	// A topology the Builder has published is generated from its spec: the
	// diagram left beside the document reference is not read.
	harness.configs[0].Metadata.Annotations[bapi.DocumentAnnotation] = `{"digest":"sha256:` + strings.Repeat("0", 64) + `"}`

	document, warnings := postBuilderGenerate(t, harness, nil, stored)
	if got := legacyPosition(t, document, "server-device-5"); got == (bdoc.Position{X: 1280, Y: 512}) {
		t.Errorf("legacyPosition(server-device-5) of a published topology = %+v, want the layout of a plain import", got)
	}

	if len(warnings) != 0 {
		t.Errorf("POST /builder/generate of a published topology: warnings = %q, want none", warnings)
	}

	// Only a topology holds a legacy diagram.
	experiment := builderConfig(t, kindExperiment, "exp")
	experiment.Metadata.Annotations = store.Annotations{bdoc.LegacyXMLAnnotation: "<mxGraphModel/>"}
	harness.configs = append(harness.configs, experiment)

	// No experiment is marked as a legacy topology.
	if diff := cmp.Diff([]string{""}, legacyBuilders(builderListSources(t, harness, nil).Experiments)); diff != "" {
		t.Errorf("GET /builder/sources: builders of the experiments mismatch (-want +got):\n%s", diff)
	}
}

// importLegacyDraft imports the stored topology name and makes a draft of
// the document, as the editor does.
func importLegacyDraft(t *testing.T, harness *builderHarness, name, token string) builderDraftResponse {
	t.Helper()

	return createBuilderPublishDraft(t, harness, generateBuilderDocument(t, harness, "Topology/"+name), token)
}

// TestBuilderPublishReplacesLegacyDiagram publishes the draft imported from a
// legacy topology back to it: the diagram goes, with a warning, and the rest stays.
func TestBuilderPublishReplacesLegacyDiagram(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"sample","action":"update"}}`

	for mode, intent := range map[string]string{
		"topology": update,
		"topology-experiment": `{"mode":"topology-experiment","topology":{"name":"sample","action":"update"},` +
			`"experiment":{"name":"sample-exp","action":"create"}}`,
	} {
		t.Run(mode, func(t *testing.T) {
			harness := newBuilderHarness(t, legacySampleTopology(t))
			document := generateBuilderDocument(t, harness, "Topology/sample")
			draft := createBuilderPublishDraft(t, harness, document, "Topology/sample")

			// A new topology of that name cannot be created over it.
			publishBuilderDraft(t, harness, draft,
				`{"mode":"topology","topology":{"name":"sample","action":"create"}}`, http.StatusConflict)

			if harness.configWrites != 0 {
				t.Errorf("a refused publication wrote %d configs, want 0", harness.configWrites)
			}

			if !harness.configs[0].HasAnnotation(bdoc.LegacyXMLAnnotation) {
				t.Errorf("a refused publication removed %s from the topology", bdoc.LegacyXMLAnnotation)
			}

			response, _ := publishBuilderDraft(t, harness, draft, intent, http.StatusOK)

			count := 0

			for _, warning := range response.Warnings {
				if warning == bdoc.NewIssue(bdoc.CodePublishLegacyReplaced, "", legacyReplaced) {
					count++
				}
			}

			if count != 1 {
				t.Errorf("publishing %s: warnings = %+v, want %q once, of code %s",
					intent, response.Warnings, legacyReplaced, bdoc.CodePublishLegacyReplaced)
			}

			published, err := harness.getConfig("Topology/sample")
			if err != nil {
				t.Fatalf("the published topology is missing: %v", err)
			}

			// The legacy diagram is gone. The team and the document reference stay.
			annotations := published.Metadata.Annotations
			if diff := cmp.Diff(store.Annotations{"team": "ops"}, annotations, cmpopts.IgnoreMapEntries(isDocumentReference)); diff != "" {
				t.Errorf("publishing %s: annotations mismatch (-want +got):\n%s", intent, diff)
			}

			if _, err := bapi.DecodeReference(annotations[bapi.DocumentAnnotation]); err != nil {
				t.Errorf("bapi.DecodeReference(the document reference) error: %v", err)
			}

			if got := topologyHostnames(t, harness, "sample"); len(got) != 5 {
				t.Errorf("topologyHostnames(sample) = %v, want the five of the topology", got)
			}

			// The sources list it as a Builder topology, and Import reads its spec.
			groups := builderListSources(t, harness, nil)
			if diff := cmp.Diff([]string{bapi.DocumentAnnotation}, legacyBuilders(groups.Topologies)); diff != "" {
				t.Errorf("GET /builder/sources: builders of the topologies mismatch (-want +got):\n%s", diff)
			}

			// Publishing again, after an edit, updates it as any Builder topology,
			// with no word of a legacy diagram.
			edited := editBuilderDraft(t, harness, response.Draft, document, "added")

			again, _ := publishBuilderDraft(t, harness, edited, update, http.StatusOK)
			if slices.Contains(bdoc.IssueMessages(again.Warnings), legacyReplaced) {
				t.Errorf("publishing again: warnings = %q, want none of a legacy diagram", again.Warnings)
			}

			if got := topologyHostnames(t, harness, "sample"); !slices.Contains(got, "added") {
				t.Errorf("topologyHostnames(sample) = %v, want added among them", got)
			}
		})
	}
}

// TestBuilderPublishRemovesUnreadableLegacyDiagram publishes the draft
// imported from a topology whose legacy diagram cannot be read, and removes it.
func TestBuilderPublishRemovesUnreadableLegacyDiagram(t *testing.T) {
	const (
		unread  = "The Builder could not read the legacy diagram of topology sample ("
		removed = "The server could not read the legacy Builder diagram of topology sample. The server removed it."
	)

	topology := legacySampleTopology(t)
	topology.Metadata.Annotations[bdoc.LegacyXMLAnnotation] = "PG14R3JhcGhNb2RlbC8+"

	harness := newBuilderHarness(t, topology)

	document, warnings := postBuilderGenerate(t, harness, nil, `{"source":"Topology/sample"}`)
	if len(warnings) == 0 || !strings.HasPrefix(warnings[0], unread) {
		t.Errorf("POST /builder/generate: warnings = %q, want the first to start %q", warnings, unread)
	}

	topologies := builderListSources(t, harness, nil).Topologies

	digests := make([]string, 0, len(topologies))
	for _, source := range topologies {
		digests = append(digests, source.Digest)
	}

	if diff := cmp.Diff([]string{document.Source.Digest}, digests); diff != "" {
		t.Errorf("GET /builder/sources: digests of the topologies mismatch, want that of the import (-want +got):\n%s", diff)
	}

	draft := createBuilderPublishDraft(t, harness, document, "Topology/sample")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"sample","action":"update"}}`, http.StatusOK)

	if !slices.Contains(response.Warnings, bdoc.NewIssue(bdoc.CodePublishLegacyRemoved, "", removed)) {
		t.Errorf("publishing: warnings = %q, want %q among them", response.Warnings, removed)
	}

	if slices.Contains(bdoc.IssueMessages(response.Warnings), legacyReplaced) {
		t.Errorf("publishing: warnings = %q, want nothing of a replaced diagram", response.Warnings)
	}

	published, err := harness.getConfig("Topology/sample")
	if err != nil {
		t.Fatalf("the published topology is missing: %v", err)
	}

	// The diagram is gone and the team kept.
	if diff := cmp.Diff(
		store.Annotations{"team": "ops"},
		published.Metadata.Annotations,
		cmpopts.IgnoreMapEntries(isDocumentReference),
	); diff != "" {
		t.Errorf("publishing: annotations mismatch (-want +got):\n%s", diff)
	}

	if !published.HasAnnotation(bapi.DocumentAnnotation) {
		t.Errorf("publishing: annotations = %v, want %s among them", published.Metadata.Annotations, bapi.DocumentAnnotation)
	}
}

// TestBuilderPublishKeepsLegacyTopology refuses every draft but the one
// imported from the legacy topology as it is now, and writes nothing.
func TestBuilderPublishKeepsLegacyTopology(t *testing.T) {
	const (
		update    = `{"mode":"topology","topology":{"name":"sample","action":"update"}}`
		notSource = "topology sample is not the source of this draft"
		changed   = "this diagram comes from topology sample. That topology changed on the server " +
			"after Builder made the diagram. Publish refuses this diagram under any name. To continue, " +
			"make a new draft of topology sample: import it again or open its published diagram. " +
			"Then copy your changes to the new draft"
	)

	experiment := func(t *testing.T) store.Config {
		t.Helper()

		config := builderConfig(t, kindExperiment, "exp")
		config.Metadata.Annotations = store.Annotations{"topology": "sample"}
		config.Spec = map[string]any{"topology": legacySampleTopology(t).Spec}

		return config
	}

	for _, test := range []struct {
		name    string
		draft   func(t *testing.T, harness *builderHarness) builderDraftResponse
		refusal string
	}{
		{
			name: "the topology imported from a config file",
			draft: func(t *testing.T, harness *builderHarness) builderDraftResponse {
				t.Helper()

				return importLegacyDraft(t, harness, "sample", "uploaded/Topology/sample")
			},
			refusal: notSource,
		},
		{
			name: "the diagram uploaded without its topology",
			draft: func(t *testing.T, harness *builderHarness) builderDraftResponse {
				t.Helper()

				content := harness.configs[0].Metadata.Annotations[bdoc.LegacyXMLAnnotation]
				response, _ := convertBuilderLegacy(t, harness, map[string]any{"content": content, "name": "sample"})

				document, err := bdoc.Parse(response.Document)
				if err != nil {
					t.Fatalf("the converted document is not valid: %v", err)
				}

				return createBuilderPublishDraft(t, harness, document, "uploaded/legacy-xml")
			},
			refusal: notSource,
		},
		{
			name: "another topology",
			draft: func(t *testing.T, harness *builderHarness) builderDraftResponse {
				t.Helper()

				return importLegacyDraft(t, harness, "other", "Topology/other")
			},
			refusal: notSource,
		},
		{
			// An experiment built from the topology never held the diagram.
			name: "an experiment built from the topology",
			draft: func(t *testing.T, harness *builderHarness) builderDraftResponse {
				t.Helper()

				return createBuilderPublishDraft(t, harness, generateBuilderDocument(t, harness, "Experiment/exp"), "Experiment/exp")
			},
			refusal: notSource,
		},
		{
			name: "the topology as it was before someone changed it",
			draft: func(t *testing.T, harness *builderHarness) builderDraftResponse {
				t.Helper()

				draft := importLegacyDraft(t, harness, "sample", "Topology/sample")
				harness.configs[0].Spec["changed"] = true

				return draft
			},
			refusal: changed,
		},
		{
			// The update would remove a diagram the draft never converted.
			name: "the topology as it was before someone changed its legacy diagram",
			draft: func(t *testing.T, harness *builderHarness) builderDraftResponse {
				t.Helper()

				draft := importLegacyDraft(t, harness, "sample", "Topology/sample")
				annotations := harness.configs[0].Metadata.Annotations
				diagram := annotations[bdoc.LegacyXMLAnnotation]

				annotations[bdoc.LegacyXMLAnnotation] = strings.Replace(diagram, "a note", "another note", 1)
				if annotations[bdoc.LegacyXMLAnnotation] == diagram {
					t.Fatal("the sample diagram has no note to change")
				}

				return draft
			},
			refusal: changed,
		},
		{
			name: "the topology as it was before it had a legacy diagram",
			draft: func(t *testing.T, harness *builderHarness) builderDraftResponse {
				t.Helper()

				diagram := harness.configs[0].Metadata.Annotations[bdoc.LegacyXMLAnnotation]
				delete(harness.configs[0].Metadata.Annotations, bdoc.LegacyXMLAnnotation)

				draft := importLegacyDraft(t, harness, "sample", "Topology/sample")
				harness.configs[0].Metadata.Annotations[bdoc.LegacyXMLAnnotation] = diagram

				return draft
			},
			refusal: changed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			other := legacySampleTopology(t)
			other.Metadata.Name = "other"

			harness := newBuilderHarness(t, legacySampleTopology(t), other, experiment(t))
			draft := test.draft(t, harness)
			before := *cloneBuilderConfig(&harness.configs[0])

			_, refusal := publishBuilderDraft(t, harness, draft, update, http.StatusConflict)
			if refusal != test.refusal {
				t.Errorf("publishing %s: refusal = %q, want %q", update, refusal, test.refusal)
			}

			if diff := cmp.Diff(before, harness.configs[0]); diff != "" {
				t.Errorf("the refused publication changed the topology (-want +got):\n%s", diff)
			}

			if harness.configWrites != 0 {
				t.Errorf("the refused publication wrote %d configs, want 0", harness.configWrites)
			}

			if got := harness.store.Count(bapi.NamespacePublished); got != 0 {
				t.Errorf("the refused publication stored %d published records, want 0", got)
			}
		})
	}

	// The same experiment updates a topology that carries no legacy diagram.
	plain := legacySampleTopology(t)
	delete(plain.Metadata.Annotations, bdoc.LegacyXMLAnnotation)

	harness := newBuilderHarness(t, plain, experiment(t))
	draft := createBuilderPublishDraft(t, harness, generateBuilderDocument(t, harness, "Experiment/exp"), "Experiment/exp")

	publishBuilderDraft(t, harness, draft, update, http.StatusOK)
}
