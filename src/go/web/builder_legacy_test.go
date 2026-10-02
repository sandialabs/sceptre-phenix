package web

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
)

// The messages a conversion of the captured sample diagram warns with.
const (
	legacyAddedSwitch = "Added a switch for 1 network that had none in the legacy diagram: b."
	legacyLeftOutLine = "Left out 1 line that was not a network link."
	legacyDecorations = "Text and containers were kept as notes and groups. " +
		"Their colors, fonts and other formatting were not converted."
	legacyReplaced   = "The legacy Builder diagram of topology sample was replaced by this diagram."
	legacyNotDiagram = "this is not a legacy Builder diagram: expected mxGraph XML, " +
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
// captured sample: the nodes it posted, with the diagram as the
// "builder-xml" annotation and an annotation of the user's own.
func legacySampleTopology(t *testing.T) store.Config {
	t.Helper()

	var capture struct {
		Posts []struct {
			Body string `json:"body"`
		} `json:"posts"`
	}

	if err := json.Unmarshal(legacySampleFile(t, "sample-capture.json"), &capture); err != nil || len(capture.Posts) != 1 {
		t.Fatalf("decoding the capture: %v", err)
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
			"annotations": map[string]any{bdoc.LegacyXMLAnnotation: saved.BuilderXML, "owner": "ops"},
		},
		"spec": saved.Topology,
	})
	if err != nil {
		t.Fatalf("encoding the topology: %v", err)
	}

	config, err := store.NewConfigFromJSON(body)
	if err != nil {
		t.Fatalf("NewConfigFromJSON returned error: %v", err)
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
		method: http.MethodPost, path: "/builder/legacy", body: string(body), user: builderTestOwner, role: role,
	})

	assertLegacyHeaders(t, recorder)

	return recorder
}

// assertLegacyHeaders checks the headers of an answer of POST
// /builder/legacy, which holds text taken from an uploaded file: the browser
// must not guess another type for it. The policy of the icon library is not
// on it.
func assertLegacyHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()

	header := recorder.Header()

	if got := header.Get("X-Content-Type-Options"); got != builderNoSniff {
		t.Errorf("status %d: X-Content-Type-Options = %q, want %q", recorder.Code, got, builderNoSniff)
	}

	if got := header.Get("Content-Security-Policy"); got != "" {
		t.Errorf("status %d: Content-Security-Policy = %q, want none outside the icon library", recorder.Code, got)
	}

	if got := header.Get("Content-Type"); recorder.Body.Len() != 0 && got != mimeJSON {
		t.Errorf("status %d: Content-Type = %q, want %q", recorder.Code, got, mimeJSON)
	}
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
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	if recorder.Header().Get("ETag") != "" {
		t.Error("a conversion stores nothing, but carries an entity tag")
	}

	var response legacyResponse

	harness.decode(recorder, &response)

	document, err := bdoc.Parse(response.Document)
	if err != nil {
		t.Fatalf("the converted document is not valid: %v", err)
	}

	return response, document
}

func legacyPosition(t *testing.T, document *bdoc.Document, hostname string) bdoc.Position {
	t.Helper()

	node := document.FindDevice(hostname)
	if node == nil {
		t.Fatalf("the document has no device %s", hostname)
	}

	return node.Position
}

// TestBuilderConvertLegacyDiagram converts a diagram that comes without a
// topology: the document is named as asked, names no source config, and is
// what a draft is made from. Nothing is written.
func TestBuilderConvertLegacyDiagram(t *testing.T) {
	harness := newBuilderHarness(t, builderConfig(t, builderKindTopology, "sample"))
	sample := string(legacySampleFile(t, "sample.xml"))
	gets, lists := harness.configGets, len(harness.configLists)

	before := time.Now().UTC().Truncate(time.Second)
	response, document := convertBuilderLegacy(t, harness, map[string]any{"content": sample, "name": " branch-office "})

	if response.Source != nil {
		t.Errorf("source = %+v, want none for a diagram that came without a topology", response.Source)
	}

	want := []string{legacyAddedSwitch, legacyLeftOutLine, legacyDecorations}
	if !slices.Equal(response.Warnings, want) || !slices.Equal(document.Source.Warnings, want) {
		t.Errorf("warnings = %q and %q in the document, want %q", response.Warnings, document.Source.Warnings, want)
	}

	if document.Name != "branch-office" || document.Source.Kind != bdoc.SourceKindManual ||
		document.Source.Name != "" || document.Source.Digest != "" {
		t.Errorf("name = %q, source = %+v, want the name asked for and a source that names no config",
			document.Name, document.Source)
	}

	imported, err := time.Parse(time.RFC3339, document.Source.ImportedAt)
	if err != nil || imported.Before(before) || imported.After(time.Now().UTC()) {
		t.Errorf("importedAt = %q, want the time of the request", document.Source.ImportedAt)
	}

	if got := legacyPosition(t, document, "router-device-0"); got != (bdoc.Position{X: 80, Y: 112}) {
		t.Errorf("router position = %+v, want the diagram's", got)
	}

	// Without a name, and with a blank one, the document gets the default.
	for _, request := range []map[string]any{{"content": sample}, {"content": "\xef\xbb\xbf \n" + sample, "name": "  "}} {
		if _, unnamed := convertBuilderLegacy(t, harness, request); unnamed.Name != "legacy-diagram" {
			t.Errorf("name = %q, want the default", unnamed.Name)
		}
	}

	// An empty diagram is converted too.
	empty, blank := convertBuilderLegacy(t, harness, map[string]any{"content": "<mxGraphModel/>"})
	if len(blank.Nodes) != 0 || !slices.Equal(empty.Warnings, []string{"The diagram has no nodes."}) {
		t.Errorf("an empty diagram gave %d nodes and the warnings %q", len(blank.Nodes), empty.Warnings)
	}

	// Converting reads no config and writes none.
	if harness.configWrites != 0 || len(harness.configs) != 1 || harness.configGets != gets ||
		len(harness.configLists) != lists {
		t.Errorf("writes = %d, configs = %d, gets = %d, lists = %v, want no config read or written",
			harness.configWrites, len(harness.configs), harness.configGets, harness.configLists)
	}

	// The document is what a draft is made from, with the token of a bare
	// diagram, which never lets the draft update a stored config.
	created := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderTestOwner,
		body: `{"sourceToken":"uploaded/legacy-xml","document":` + string(response.Document) + `}`,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("draft status = %d, want %d: %s", created.Code, http.StatusCreated, created.Body)
	}

	var draft builderDraftResponse

	harness.decode(created, &draft)

	_, refusal := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"sample","action":"update"}}`, http.StatusConflict)
	if refusal != "topology sample is not the source this draft was loaded from" {
		t.Errorf("refusal = %q, want the draft refused as not loaded from the topology", refusal)
	}

	if harness.configWrites != 0 || harness.configs[0].HasAnnotation(bapi.DocumentAnnotation) {
		t.Error("the refused publication changed the topology")
	}
}

// TestBuilderConvertLegacyTopology converts a Topology config that carries
// a legacy diagram, as YAML and as JSON: the topology's own nodes are used,
// the diagram places them, and the response describes the topology.
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

			source := response.Source
			if source == nil || source.FullName != "Topology/sample" || source.Stored ||
				source.Builder != bdoc.LegacyXMLAnnotation || source.Digest != digest {
				t.Fatalf("source = %+v, want the uploaded topology with its legacy diagram and digest", source)
			}

			if document.Name != "sample" || document.Source.Kind != bdoc.SourceKindTopology ||
				document.Source.Name != "sample" || document.Source.Digest != digest {
				t.Errorf("name = %q, source = %+v, want those of the topology", document.Name, document.Source)
			}

			if want := map[string]string{"owner": "ops"}; !reflect.DeepEqual(document.Source.Annotations, want) {
				t.Errorf("annotations = %v, want %v without the diagram", document.Source.Annotations, want)
			}

			if got := legacyPosition(t, document, "client-1"); got != (bdoc.Position{X: 80, Y: 832}) {
				t.Errorf("client position = %+v, want the diagram's", got)
			}

			want := []string{legacyAddedSwitch, legacyLeftOutLine, legacyDecorations}
			if !slices.Equal(response.Warnings, want) {
				t.Errorf("warnings = %q, want %q", response.Warnings, want)
			}

			if harness.configWrites != 0 || harness.configGets != 0 {
				t.Errorf("writes = %d, gets = %d, want no config read or written", harness.configWrites, harness.configGets)
			}
		})
	}
}

// TestBuilderConvertLegacyTopologyResolvesIncludes reads the topologies a
// converted topology includes from the store, under the caller's
// permissions, and nothing else.
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

	_, document := convertBuilderLegacy(t, harness, map[string]any{"content": string(content)})
	if dns := document.FindDevice("dns"); dns == nil || dns.Device.IncludedFrom != "shared" {
		t.Errorf("dns = %+v, want the included node", dns)
	}

	// A caller who may not read the included topology gets the rest, with a
	// warning.
	role := builderRole(builderPolicy([]string{"configs"}, []string{"*"}, []string{"get", "create"}))

	recorder := postBuilderLegacy(t, harness, &role, map[string]any{"content": string(content)})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var response legacyResponse

	harness.decode(recorder, &response)

	if !strings.Contains(strings.Join(response.Warnings, "\n"), `included topology "shared" could not be read`) ||
		strings.Contains(string(response.Document), `"dns"`) {
		t.Errorf("warnings = %q, want the include refused and its node left out", response.Warnings)
	}
}

// TestBuilderConvertLegacyRequests asserts each refusal of a conversion: its
// status and its exact message, without a cause.
func TestBuilderConvertLegacyRequests(t *testing.T) {
	config := func(kind, name, rest string) string {
		return "apiVersion: phenix.sandia.gov/v1\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n" + rest
	}

	cells := func(count int) string {
		return "<root>" + strings.Repeat("<mxCell/>", count) + "</root>"
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
			status: http.StatusBadRequest, message: "content is required",
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
			name: "a body beyond the limit", body: `{"content":"` + strings.Repeat("x", builderMaxRequestBytes) + `"}`,
			status: http.StatusRequestEntityTooLarge, message: "request body is larger than 6291456 bytes",
		},
		{
			// A switch, a network and an edge for each interface make the
			// document many times the size of the diagram.
			name: "a diagram whose document is beyond the limit", content: wide(8000),
			status: http.StatusRequestEntityTooLarge, message: "the converted diagram is larger than 5242880 bytes",
		},
		{name: "plain words", content: "hello", status: http.StatusUnprocessableEntity, message: legacyNotDiagram},
		{name: "only white space", content: " \n\t", status: http.StatusUnprocessableEntity, message: legacyNotDiagram},
		{
			// Encoded forms are not read: this is the base64 of a diagram.
			name: "base64 text", content: "PG14R3JhcGhNb2RlbC8+",
			status: http.StatusUnprocessableEntity, message: legacyNotDiagram,
		},
		{
			name: "a config of no known kind", content: config("Nope", "lab", ""),
			status: http.StatusUnprocessableEntity, message: legacyNotDiagram,
		},
		{
			name: "a config without a name", content: "apiVersion: phenix.sandia.gov/v1\nkind: Topology\n",
			status: http.StatusUnprocessableEntity, message: legacyNotDiagram,
		},
		{
			name: "YAML that cannot be parsed", content: "\tnot: [valid",
			status: http.StatusUnprocessableEntity, message: legacyNotDiagram,
		},
		{
			name: "another root element", content: `<svg xmlns="http://www.w3.org/2000/svg"/>`,
			status:  http.StatusUnprocessableEntity,
			message: "the XML is not a legacy Builder diagram: its root element is <svg>, not <mxGraphModel>",
		},
		{
			// The wrapper draw.io saves is not read either.
			name: "a draw.io file", content: `<mxfile><diagram><mxGraphModel/></diagram></mxfile>`,
			status:  http.StatusUnprocessableEntity,
			message: "the XML is not a legacy Builder diagram: its root element is <mxfile>, not <mxGraphModel>",
		},
		{
			name: "a DOCTYPE", content: `<!DOCTYPE mxGraphModel [<!ENTITY a "b">]><mxGraphModel/>`,
			status:  http.StatusUnprocessableEntity,
			message: "the legacy diagram has a DOCTYPE or entity declaration, which is not accepted",
		},
		{
			name: "XML cut short", content: "<mxGraphModel>\n<root>\n<mxCell",
			status:  http.StatusUnprocessableEntity,
			message: "the legacy diagram is not well-formed XML (line 3)",
		},
		{
			name: "elements nested too deep", content: strings.Repeat("<root>", 33) + strings.Repeat("</root>", 33),
			status:  http.StatusUnprocessableEntity,
			message: "the legacy diagram nests elements more than 32 levels deep",
		},
		{
			name: "too many cells", content: cells(10001),
			status: http.StatusUnprocessableEntity, message: "the legacy diagram has more than 10000 cells",
		},
		{
			name: "an Experiment config", content: config("Experiment", "exp", "spec: {}\n"),
			status:  http.StatusUnprocessableEntity,
			message: "only a Topology config can hold a legacy Builder diagram, not a Experiment config",
		},
		{
			name: "a Topology without a legacy diagram", content: config("topology", "plain", "spec:\n  nodes: []\n"),
			status: http.StatusUnprocessableEntity,
			message: "topology plain has no legacy Builder diagram (no builder-xml annotation); " +
				"use Import to make a diagram from it",
		},
		{
			name: "a Topology the Builder cannot import",
			content: config("Topology", "broken",
				"  annotations:\n    builder-xml: <mxGraphModel/>\nspec:\n  nodes:\n    - general:\n        hostname: two words\n"),
			status:  http.StatusUnprocessableEntity,
			message: "unable to import a builder document from Topology/broken",
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
				method: http.MethodPost, path: "/builder/legacy", body: body, user: builderTestOwner,
			})
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body)
			}

			assertLegacyHeaders(t, recorder)

			var refusal builderPublishRefusal

			harness.decode(recorder, &refusal)

			if test.message != "" && (refusal.Message != test.message || refusal.Cause != "") {
				t.Errorf("refusal = %+v, want the message %q and no cause", refusal, test.message)
			}
		})
	}

	// The most cells a diagram may hold are converted.
	harness := newBuilderHarness(t)
	if recorder := postBuilderLegacy(t, harness, nil, map[string]any{"content": cells(10000)}); recorder.Code != http.StatusOK {
		t.Errorf("status of 10000 cells = %d, want %d", recorder.Code, http.StatusOK)
	}
}

// TestBuilderConvertLegacyRequiresGetAndCreate asserts a conversion needs
// the permissions to read and to create configs, as importing a config file
// does, and no other.
func TestBuilderConvertLegacyRequiresGetAndCreate(t *testing.T) {
	harness := newBuilderHarness(t)
	request := map[string]any{"content": string(legacySampleFile(t, "sample.xml"))}

	for name, verbs := range map[string][]string{
		"get only":    {"list", "get", "update", "delete"},
		"create only": {"list", "create", "update", "delete"},
	} {
		role := builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, verbs))

		recorder := postBuilderLegacy(t, harness, &role, request)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want %d: %s", name, recorder.Code, http.StatusForbidden, recorder.Body)
		}

		if strings.Contains(recorder.Body.String(), "mxGraphModel") {
			t.Errorf("%s: the refusal repeats the content", name)
		}
	}

	role := builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"get", "create"}))
	if recorder := postBuilderLegacy(t, harness, &role, request); recorder.Code != http.StatusOK {
		t.Errorf("get and create: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	// Without an identity at all.
	recorder := harness.do(builderRequest{method: http.MethodPost, path: "/builder/legacy", body: `{"content":"<root/>"}`})
	if recorder.Code != http.StatusForbidden {
		t.Errorf("anonymous: status = %d, want %d", recorder.Code, http.StatusForbidden)
	}

	assertLegacyHeaders(t, recorder)
}

// TestBuilderConvertLegacyNeverRepeatsContent asserts that no refusal and no
// log line holds anything of the content: not the text around a syntax
// error, not what a parser quotes, not a node's settings.
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
		recorder := postBuilderLegacy(t, harness, nil, map[string]any{"content": content})

		converted := recorder.Code == http.StatusOK
		if !converted && recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d: %s", name, recorder.Code, recorder.Body)
		}

		if !converted && strings.Contains(recorder.Body.String(), secret) {
			t.Errorf("%s: the refusal repeats the content: %s", name, recorder.Body)
		}

		if logged := logs.String(); strings.Contains(logged, secret) {
			t.Errorf("%s: the log repeats the content: %s", name, logged)
		}

		logs.Take(t, nil)
	}

	// What a conversion logs: who, which kind of input, and how many
	// warnings.
	convertBuilderLegacy(t, harness, map[string]any{"content": string(legacySampleFile(t, "sample.xml"))})

	records := logs.Take(t, plogtest.Message("converted legacy builder diagram"))
	if len(records) != 1 || records[0]["user"] != builderTestOwner || records[0]["kind"] != "diagram" ||
		records[0]["warnings"] != float64(3) {
		t.Errorf("logged %v, want one record with the user, the kind and the number of warnings", records)
	}
}

// TestBuilderConvertLegacyLeavesTheEnvironmentAlone asserts a diagram that
// comes without a topology takes nothing from the server's environment: its
// $NAME placeholders are resolved from the diagram and the legacy defaults
// only, and ${NAME} is left as it is.
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

	spec := document.FindDevice("host").Device.Spec
	if commands := spec["commands"]; !reflect.DeepEqual(commands, []any{"cd $HOME", "echo ${HOME}"}) {
		t.Errorf("commands = %v, want them as the diagram has them", commands)
	}

	if vcpus := spec["hardware"].(map[string]any)["vcpus"]; vcpus != "1" {
		t.Errorf("vcpus = %v, want the legacy default", vcpus)
	}

	want := []string{"Used the legacy defaults for variables the diagram does not define: $DEFAULT_VCPU = 1."}
	if !slices.Equal(response.Warnings, want) {
		t.Errorf("warnings = %q, want %q", response.Warnings, want)
	}
}

// TestBuilderGenerateConvertsLegacyTopology asserts Import converts a
// topology the legacy Builder drew, stored or in a config file, and reads
// no diagram of a topology the Builder has published.
func TestBuilderGenerateConvertsLegacyTopology(t *testing.T) {
	topology := legacySampleTopology(t)
	harness := newBuilderHarness(t, topology)

	groups := builderListSources(t, harness, nil)
	if len(groups.Topologies) != 1 || groups.Topologies[0].Builder != bdoc.LegacyXMLAnnotation {
		t.Fatalf("sources = %+v, want the topology marked as a legacy one", groups.Topologies)
	}

	content, err := json.Marshal(topology)
	if err != nil {
		t.Fatalf("encoding the topology: %v", err)
	}

	stored := `{"source":"Topology/sample"}`
	uploaded := asBuilderJSON(t, map[string]string{"content": string(content)})

	for name, body := range map[string]string{"stored": stored, "a config file": uploaded} {
		recorder := harness.do(builderRequest{
			method: http.MethodPost, path: "/builder/generate", body: body, user: builderTestOwner,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", name, recorder.Code, recorder.Body)
		}

		var response builderGenerateResponse

		harness.decode(recorder, &response)

		document, err := bdoc.Parse(response.Document)
		if err != nil {
			t.Fatalf("%s: the generated document is not valid: %v", name, err)
		}

		if response.Source.Builder != bdoc.LegacyXMLAnnotation || response.Source.Stored != (body == stored) {
			t.Errorf("%s: source = %+v, want it marked as a legacy topology", name, response.Source)
		}

		if got := legacyPosition(t, document, "server-device-5"); got != (bdoc.Position{X: 1280, Y: 512}) {
			t.Errorf("%s: position = %+v, want the diagram's", name, got)
		}

		if want := []string{legacyAddedSwitch, legacyLeftOutLine, legacyDecorations}; !slices.Equal(response.Warnings, want) {
			t.Errorf("%s: warnings = %q, want %q", name, response.Warnings, want)
		}

		if document.Source.Kind != bdoc.SourceKindTopology || document.Source.ImportedAt == "" {
			t.Errorf("%s: source = %+v, want the topology's with the time of the import", name, document.Source)
		}
	}

	// A topology the Builder has published is generated from its spec: the
	// diagram left beside the document reference is not read.
	harness.configs[0].Metadata.Annotations[bapi.DocumentAnnotation] = `{"digest":"sha256:` + strings.Repeat("0", 64) + `"}`

	document, warnings := postBuilderGenerate(t, harness, nil, stored)
	if got := legacyPosition(t, document, "server-device-5"); got == (bdoc.Position{X: 1280, Y: 512}) || len(warnings) != 0 {
		t.Errorf("position = %+v, warnings = %q, want the layout of a plain import", got, warnings)
	}

	// Only a topology holds a legacy diagram.
	experiment := builderConfig(t, kindExperiment, "exp")
	experiment.Metadata.Annotations = store.Annotations{bdoc.LegacyXMLAnnotation: "<mxGraphModel/>"}
	harness.configs = append(harness.configs, experiment)

	if groups := builderListSources(t, harness, nil); len(groups.Experiments) != 1 || groups.Experiments[0].Builder != "" {
		t.Errorf("experiments = %+v, want none marked as a legacy topology", groups.Experiments)
	}
}

// importLegacyDraft imports the stored topology name and makes a draft of
// the document, as the editor does.
func importLegacyDraft(t *testing.T, harness *builderHarness, name, token string) builderDraftResponse {
	t.Helper()

	return createBuilderPublishDraft(t, harness, generateBuilderDocument(t, harness, "Topology/"+name), token)
}

// TestBuilderPublishReplacesLegacyDiagram publishes the draft imported from
// a topology the legacy Builder drew back to that topology: the legacy
// diagram goes, the document reference comes, every other annotation stays,
// and a warning says so. The topology is a Builder topology from then on.
func TestBuilderPublishReplacesLegacyDiagram(t *testing.T) {
	harness := newBuilderHarness(t, legacySampleTopology(t))
	document := generateBuilderDocument(t, harness, "Topology/sample")
	draft := createBuilderPublishDraft(t, harness, document, "Topology/sample")

	const update = `{"mode":"topology","topology":{"name":"sample","action":"update"}}`

	// A new topology of that name cannot be created over it.
	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"sample","action":"create"}}`, http.StatusConflict)

	if harness.configWrites != 0 || !harness.configs[0].HasAnnotation(bdoc.LegacyXMLAnnotation) {
		t.Fatal("a refused publication changed the legacy topology")
	}

	response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)

	count := 0

	for _, warning := range response.Warnings {
		if warning == legacyReplaced {
			count++
		}
	}

	if count != 1 {
		t.Errorf("warnings = %q, want %q once", response.Warnings, legacyReplaced)
	}

	published, err := harness.getConfig("Topology/sample")
	if err != nil {
		t.Fatalf("the published topology is missing: %v", err)
	}

	annotations := published.Metadata.Annotations
	if _, legacy := annotations[bdoc.LegacyXMLAnnotation]; legacy || annotations["owner"] != "ops" || len(annotations) != 2 {
		t.Errorf("annotations = %v, want the legacy diagram gone, and the owner and the document reference", annotations)
	}

	if _, err := bapi.DecodeReference(annotations[bapi.DocumentAnnotation]); err != nil {
		t.Errorf("the document reference is not valid: %v", err)
	}

	if got := topologyHostnames(t, harness, "sample"); len(got) != 5 {
		t.Errorf("nodes = %v, want the five of the topology", got)
	}

	// The sources list it as a Builder topology, and Import reads its spec.
	groups := builderListSources(t, harness, nil)
	if len(groups.Topologies) != 1 || groups.Topologies[0].Builder != bapi.DocumentAnnotation {
		t.Errorf("sources = %+v, want the topology marked as a Builder one", groups.Topologies)
	}

	// Publishing again, after an edit, updates it as any Builder topology,
	// with no word of a legacy diagram.
	edited := editBuilderDraft(t, harness, response.Draft, document, "added")

	again, _ := publishBuilderDraft(t, harness, edited, update, http.StatusOK)
	if slices.Contains(again.Warnings, legacyReplaced) {
		t.Errorf("warnings of the second publication = %q, want none of a legacy diagram", again.Warnings)
	}

	if got := topologyHostnames(t, harness, "sample"); !slices.Contains(got, "added") {
		t.Errorf("nodes = %v, want the edit published", got)
	}
}

// TestBuilderPublishRemovesUnreadableLegacyDiagram publishes the draft
// imported from a topology whose legacy diagram cannot be read. The import
// says the diagram was not used, and the publication that it was removed,
// not replaced: nothing of it is in the draft. The sources list the digest
// the import recorded, which covers the diagram.
func TestBuilderPublishRemovesUnreadableLegacyDiagram(t *testing.T) {
	const (
		unread  = "The legacy diagram of topology sample could not be read ("
		removed = "The legacy Builder diagram of topology sample could not be read and was removed."
	)

	for name, diagram := range map[string]string{
		"base64 text":     "PG14R3JhcGhNb2RlbC8+",
		"a draw.io file":  "<mxfile><diagram><mxGraphModel/></diagram></mxfile>",
		"a DOCTYPE":       "<!DOCTYPE mxGraphModel><mxGraphModel/>",
		"a cut-off model": "<mxGraphModel><root>",
	} {
		t.Run(name, func(t *testing.T) {
			topology := legacySampleTopology(t)
			topology.Metadata.Annotations[bdoc.LegacyXMLAnnotation] = diagram

			harness := newBuilderHarness(t, topology)

			document, warnings := postBuilderGenerate(t, harness, nil, `{"source":"Topology/sample"}`)
			if len(warnings) == 0 || !strings.HasPrefix(warnings[0], unread) {
				t.Fatalf("import warnings = %q, want the diagram reported as not read", warnings)
			}

			groups := builderListSources(t, harness, nil)
			if len(groups.Topologies) != 1 || groups.Topologies[0].Digest != document.Source.Digest {
				t.Errorf("sources = %+v, want the digest %s of the import", groups.Topologies, document.Source.Digest)
			}

			draft := createBuilderPublishDraft(t, harness, document, "Topology/sample")

			response, _ := publishBuilderDraft(t, harness, draft,
				`{"mode":"topology","topology":{"name":"sample","action":"update"}}`, http.StatusOK)

			if !slices.Contains(response.Warnings, removed) || slices.Contains(response.Warnings, legacyReplaced) {
				t.Errorf("warnings = %q, want %q and nothing of a replaced diagram", response.Warnings, removed)
			}

			published, err := harness.getConfig("Topology/sample")
			if err != nil {
				t.Fatalf("the published topology is missing: %v", err)
			}

			annotations := published.Metadata.Annotations
			if _, legacy := annotations[bdoc.LegacyXMLAnnotation]; legacy || annotations["owner"] != "ops" ||
				!published.HasAnnotation(bapi.DocumentAnnotation) {
				t.Errorf("annotations = %v, want the diagram gone, the owner kept and the document reference", annotations)
			}
		})
	}
}

// TestBuilderPublishExperimentReplacesLegacyDiagram does the same in the
// experiment mode, which writes the topology first.
func TestBuilderPublishExperimentReplacesLegacyDiagram(t *testing.T) {
	harness := newBuilderHarness(t, legacySampleTopology(t))
	draft := importLegacyDraft(t, harness, "sample", "Topology/sample")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology-experiment","topology":{"name":"sample","action":"update"},`+
			`"experiment":{"name":"sample-exp","action":"create"}}`, http.StatusOK)

	published, err := harness.getConfig("Topology/sample")
	if err != nil {
		t.Fatalf("the published topology is missing: %v", err)
	}

	if published.HasAnnotation(bdoc.LegacyXMLAnnotation) || !published.HasAnnotation(bapi.DocumentAnnotation) ||
		!slices.Contains(response.Warnings, legacyReplaced) {
		t.Errorf("annotations = %v, warnings = %q, want the legacy diagram replaced and a warning",
			published.Metadata.Annotations, response.Warnings)
	}
}

// TestBuilderPublishKeepsLegacyTopology asserts nothing but the draft
// imported from a topology the legacy Builder drew, as the topology is now,
// replaces it: every other draft is refused, and the topology and its
// diagram stay as they were.
func TestBuilderPublishKeepsLegacyTopology(t *testing.T) {
	const (
		update    = `{"mode":"topology","topology":{"name":"sample","action":"update"}}`
		notSource = "topology sample is not the source this draft was loaded from"
		changed   = "builder source Topology/sample changed after this draft was imported"
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
			name: "a diagram drawn by hand",
			draft: func(t *testing.T, harness *builderHarness) builderDraftResponse {
				t.Helper()

				return createBuilderPublishDraft(t, harness, bdoc.NewDocument("sample"))
			},
			refusal: notSource,
		},
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
				t.Errorf("refusal = %q, want %q", refusal, test.refusal)
			}

			if !reflect.DeepEqual(harness.configs[0], before) || harness.configWrites != 0 ||
				harness.store.Count(bapi.NamespacePublished) != 0 {
				t.Errorf("the refused publication wrote something: %+v", harness.configs[0].Metadata.Annotations)
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
