package web

import (
	"cmp"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
)

// exportBuilderTopology asks for the topology config document publishes as,
// named name (empty for the server's default), as role (nil for full access).
func exportBuilderTopology(
	t *testing.T,
	harness *builderBetaHarness,
	document *bdoc.Document,
	name string,
	role *rbac.Role,
) *httptest.ResponseRecorder {
	t.Helper()

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	body, err := json.Marshal(builderTopologyExportRequest{Document: data, Name: name})
	if err != nil {
		t.Fatalf("encoding export request: %v", err)
	}

	return harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/export/topology",
		body:   string(body),
		user:   builderBetaTestOwner,
		role:   role,
	})
}

// exportedBuilderTopology decodes a successful export and the config its
// YAML holds.
func exportedBuilderTopology(
	t *testing.T,
	harness *builderBetaHarness,
	recorder *httptest.ResponseRecorder,
) (builderTopologyExportResponse, store.Config) {
	t.Helper()

	if recorder.Code != http.StatusOK {
		t.Fatalf("export status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var response builderTopologyExportResponse
	harness.decode(recorder, &response)

	var config store.Config
	if err := yaml.Unmarshal([]byte(response.YAML), &config); err != nil {
		t.Fatalf("exported YAML does not parse: %v\n%s", err, response.YAML)
	}

	return response, config
}

// asJSONValue is value as JSON decodes it, so specs read from YAML and from
// the store compare equal when they hold the same numbers.
func asJSONValue(t *testing.T, value any) any {
	t.Helper()

	var decoded any
	if err := json.Unmarshal([]byte(asBuilderJSON(t, value)), &decoded); err != nil {
		t.Fatalf("decoding %T: %v", value, err)
	}

	return decoded
}

// TestBuilderBetaExportTopologyMatchesPublish exports the topology config a
// publish writes for the same document: its includes named, not merged, and
// named as the Publish dialog proposes. A view-only role may export, and
// nothing is written.
func TestBuilderBetaExportTopologyMatchesPublish(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t, includedTopologyFixture(t, "shared")...)
	document := generateBuilderDocument(t, harness, "Topology/root")
	document.Name = " Lab topology (2) "

	if included := document.FindDevice("inc-host"); included == nil || included.Device.IncludedFrom != "shared" {
		t.Fatalf("inc-host is not marked as included: %s", asBuilderJSON(t, document))
	}

	draft := createBuilderPublishDraft(t, harness, document)

	published := harness.do(builderBetaRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body:   `{"mode":"topology","topology":{"name":"Lab-topology-2","action":"create"}}`,
		user:   builderBetaTestOwner, ifMatch: draft.ETag,
	})
	if published.Code != http.StatusOK {
		t.Fatalf("publish status = %d: %s", published.Code, published.Body.String())
	}

	stored, err := harness.getConfig("Topology/Lab-topology-2")
	if err != nil {
		t.Fatalf("published topology missing: %v", err)
	}

	writes := harness.configWrites
	documents := harness.store.count(bapi.NamespacePublished)
	drafts := harness.store.count(bapi.NamespaceDrafts)
	viewer := builderBetaRole(builderBetaPolicy([]string{"configs"}, []string{"*"}, []string{"list", "get"}))

	response, exported := exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, document, "", &viewer))

	if response.Name != "Lab-topology-2" || len(response.Warnings) != 0 || len(response.PublishBlockers) != 0 {
		t.Fatalf("export = %+v, want topology Lab-topology-2 with no warnings", response)
	}

	if exported.Version != stored.Version || exported.Kind != stored.Kind ||
		exported.Metadata.Name != stored.Metadata.Name || len(exported.Metadata.Annotations) != 0 {
		t.Fatalf("exported config = %+v, want %s %s/%s without annotations",
			exported, stored.Version, stored.Kind, stored.Metadata.Name)
	}

	if got, want := asJSONValue(t, exported.Spec), asJSONValue(t, stored.Spec); !reflect.DeepEqual(got, want) {
		t.Fatalf("exported spec = %s, want the published %s", asBuilderJSON(t, got), asBuilderJSON(t, want))
	}

	exportedSpec, err := (&bdoc.Topology{Spec: exported.Spec, VLANAliases: nil, Warnings: nil}).SpecV1()
	if err != nil {
		t.Fatalf("exported spec is not a v1 topology: %v", err)
	}

	storedSpec, err := (&bdoc.Topology{Spec: stored.Spec, VLANAliases: nil, Warnings: nil}).SpecV1()
	if err != nil {
		t.Fatalf("published spec is not a v1 topology: %v", err)
	}

	if !reflect.DeepEqual(exportedSpec, storedSpec) {
		t.Fatalf("exported v1 topology = %s, want %s", asBuilderJSON(t, exportedSpec), asBuilderJSON(t, storedSpec))
	}

	if hostnames := topologySpecHostnames(exported.Spec); !slices.Equal(hostnames, []string{"web"}) ||
		!reflect.DeepEqual(exported.Spec["includeTopologies"], []any{"shared"}) {
		t.Fatalf("exported spec = %s, want web and the shared include", asBuilderJSON(t, exported.Spec))
	}

	if harness.configWrites != writes || harness.store.count(bapi.NamespacePublished) != documents ||
		harness.store.count(bapi.NamespaceDrafts) != drafts {
		t.Fatal("an export wrote to the store")
	}

	// Opening a draft takes configs get; listing them is not enough.
	lister := builderBetaRole(builderBetaPolicy([]string{"configs"}, []string{"*"}, []string{"list"}))
	if denied := exportBuilderTopology(t, harness, document, "", &lister); denied.Code != http.StatusForbidden {
		t.Fatalf("list-only export status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	// A name the client sends is used.
	response, exported = exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, document, "lab@2", nil))
	if response.Name != "lab@2" || exported.Metadata.Name != "lab@2" {
		t.Fatalf("export name = %q, config %q, want lab@2", response.Name, exported.Metadata.Name)
	}
}

// topologySpecHostnames lists the hostnames of a topology spec's nodes.
func topologySpecHostnames(spec map[string]any) []string {
	nodes, _ := spec["nodes"].([]any)
	names := make([]string, 0, len(nodes))

	for _, entry := range nodes {
		node, _ := entry.(map[string]any)
		general, _ := node["general"].(map[string]any)
		name, _ := general["hostname"].(string)
		names = append(names, name)
	}

	return names
}

// exportVLANDocument is a document whose host has the given interfaces, each
// a DHCP Ethernet interface unless it says otherwise.
func exportVLANDocument(fields ...map[string]any) *bdoc.Document {
	interfaces := make([]any, 0, len(fields))

	for _, iface := range fields {
		entry := map[string]any{"type": "ethernet", "proto": "dhcp"}
		maps.Copy(entry, iface)
		interfaces = append(interfaces, entry)
	}

	document := bdoc.NewDocument("no-vlan")
	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID("host"), Kind: bdoc.NodeKindDevice, Label: "host",
		Device: &bdoc.Device{
			Hostname: "host",
			Spec: map[string]any{
				"type":     "VirtualMachine",
				"general":  map[string]any{"hostname": "host"},
				"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "miniccc.qc2"}}},
				"network":  map[string]any{"interfaces": interfaces},
			},
			Interfaces: []bdoc.InterfaceHandle{},
		},
	})

	return document
}

// TestBuilderBetaExportTopologyRefusesAsPublishDoes refuses what Publish
// refuses for failing phenix's config validation, with its status and
// message, and exports what only publishing refuses, saying why. Publish
// names interfaces without a VLAN first; an export refused beside a blank
// VLAN, which it would export, names the schema's reason instead.
func TestBuilderBetaExportTopologyRefusesAsPublishDoes(t *testing.T) { //nolint:paralleltest // mutates feature options
	tests := []struct {
		name     string
		document *bdoc.Document
		exports  bool
		// refusal is how the publish message starts.
		refusal string
		// exportRefusal is the export's message when it is not the publish
		// message, and exportCause what its cause then holds.
		exportRefusal string
		exportCause   string
	}{
		{
			// phenix stores it; minimega refuses it when the experiment starts.
			name: "empty VLANs",
			document: exportVLANDocument(
				map[string]any{"name": "eth0", "vlan": ""},
				map[string]any{"name": "eth1", "vlan": "  "},
				map[string]any{"name": "eth2", "vlan": ""},
				map[string]any{"name": "eth3", "vlan": ""},
				map[string]any{"name": "eth4", "vlan": "EXP"},
			),
			exports: true,
			refusal: "topology no-vlan cannot be published: ",
		},
		{
			// phenix's schema requires the key.
			name:     "missing VLAN",
			document: exportVLANDocument(map[string]any{"name": "eth0"}),
			exports:  false,
			refusal:  "topology no-vlan cannot be published: ",
		},
		{
			name:     "invalid MAC address",
			document: exportVLANDocument(map[string]any{"name": "eth0", "vlan": "EXP", "mac": "not-a-mac"}),
			exports:  false,
			refusal:  "builder document cannot be published as topology no-vlan",
		},
		{
			name: "empty VLAN and invalid MAC address",
			document: exportVLANDocument(
				map[string]any{"name": "eth0", "vlan": ""},
				map[string]any{"name": "eth1", "vlan": "EXP", "mac": "not-a-mac"},
			),
			exports:       false,
			refusal:       "topology no-vlan cannot be published: ",
			exportRefusal: "builder document cannot be published as topology no-vlan",
			exportCause:   `Error at "/mac"`,
		},
		{
			// The schema refuses the missing VLAN too, so its error is why.
			name: "missing VLAN and invalid MAC address",
			document: exportVLANDocument(
				map[string]any{"name": "eth0"},
				map[string]any{"name": "eth1", "vlan": "EXP", "mac": "not-a-mac"},
			),
			exports: false,
			refusal: "topology no-vlan cannot be published: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderBetaHarness(t)
			draft := createBuilderPublishDraft(t, harness, tt.document)

			refused := harness.do(builderBetaRequest{
				method: http.MethodPost,
				path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
				body:   `{"mode":"topology","topology":{"name":"no-vlan","action":"create"}}`,
				user:   builderBetaTestOwner, ifMatch: draft.ETag,
			})
			if refused.Code != http.StatusUnprocessableEntity {
				t.Fatalf("publish status = %d, want %d: %s", refused.Code, http.StatusUnprocessableEntity, refused.Body)
			}

			var publishRefusal struct {
				Message string `json:"message"`
			}
			harness.decode(refused, &publishRefusal)

			if !strings.HasPrefix(publishRefusal.Message, tt.refusal) {
				t.Fatalf("publish message = %q, want it to start %q", publishRefusal.Message, tt.refusal)
			}

			recorder := exportBuilderTopology(t, harness, tt.document, "", nil)

			if !tt.exports {
				var exportErr struct {
					Message string `json:"message"`
					Cause   string `json:"cause"`
				}

				if recorder.Code != refused.Code {
					t.Fatalf("export status = %d, want the publish status %d: %s", recorder.Code, refused.Code, recorder.Body)
				}

				harness.decode(recorder, &exportErr)

				if want := cmp.Or(tt.exportRefusal, publishRefusal.Message); exportErr.Message != want {
					t.Fatalf("export message = %q, want %q", exportErr.Message, want)
				}

				if tt.exportCause != "" &&
					(!strings.Contains(exportErr.Cause, tt.exportCause) || strings.Contains(exportErr.Cause, "no VLAN")) {
					t.Fatalf("export cause = %q, want it to hold %q and name no VLAN", exportErr.Cause, tt.exportCause)
				}

				return
			}

			response, exported := exportedBuilderTopology(t, harness, recorder)

			if want := []string{
				strings.TrimPrefix(publishRefusal.Message, tt.refusal),
			}; !slices.Equal(response.PublishBlockers, want) || !strings.HasSuffix(want[0], "; 1 more interface has no VLAN") {
				t.Fatalf("publish blockers = %q, want %q", response.PublishBlockers, want)
			}

			if hostnames := topologySpecHostnames(exported.Spec); !slices.Equal(hostnames, []string{"host"}) {
				t.Fatalf("exported spec = %s, want the host", asBuilderJSON(t, exported.Spec))
			}
		})
	}
}

// TestBuilderBetaExportTopologyKeepsStrings exports strings that yaml.v3
// would write as block scalars it does not read back as they are, such as
// one starting with a line break, so that the file loads, as phenix loads a
// config, as the config Publish writes. Other multi-line strings stay block
// scalars.
func TestBuilderBetaExportTopologyKeepsStrings(t *testing.T) { //nolint:paralleltest // mutates feature options
	document := exportVLANDocument(map[string]any{"name": "eth0", "vlan": "EXP"})
	spec := document.Nodes[0].Device.Spec

	general, _ := spec["general"].(map[string]any)
	general["description"] = "\n  indented first line\nsecond"
	spec["commands"] = []any{"\n", "\n\n", "\tfirst\nsecond", "\n#x", "one\ntwo\n"}
	spec["advanced"] = map[string]any{"\nkey": "\n\tvalue", "plain": "a\n b"}

	harness := newBuilderBetaHarness(t)
	response, _ := exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, document, "", nil))

	loaded, err := store.NewConfigFromYAML([]byte(response.YAML))
	if err != nil {
		t.Fatalf("exported YAML does not load: %v\n%s", err, response.YAML)
	}

	published, _, err := document.PublishTopologyConfig(response.Name)
	if err != nil {
		t.Fatalf("PublishTopologyConfig returned error: %v", err)
	}

	if got, want := asJSONValue(t, loaded.Spec), asJSONValue(t, published.Spec); !reflect.DeepEqual(got, want) {
		t.Fatalf("exported spec = %s, want the published %s\n%s",
			asBuilderJSON(t, got), asBuilderJSON(t, want), response.YAML)
	}

	for _, want := range []string{"- |\n", "plain: |-\n"} {
		if !strings.Contains(response.YAML, want) {
			t.Fatalf("exported YAML does not hold %q:\n%s", want, response.YAML)
		}
	}
}

// A spec whose strings yaml.v3 reads back as they are is written as
// yaml.Marshal writes it, and each string it would not read back is double
// quoted, map keys included.
func TestBuilderYAMLExactMarshalsLikeYAML(t *testing.T) {
	t.Parallel()

	plain := map[string]any{
		"nodes": []any{map[string]any{
			"a10": 1.5, "a9": true, "b": nil, "multi": "one\ntwo\n", "x": []string{"y"}, "n": 3,
		}},
		"quoted":   []any{"true", "", " lead", "#hash", "a\u2028b", "a \nb", "a\n\tb"},
		"empty":    map[string]any{},
		"none":     []any{},
		"nilMap":   map[string]any(nil),
		"nilSlice": []string(nil),
		"strings":  map[string]string{"k": "v"},
	}

	want, err := yaml.Marshal(plain)
	if err != nil {
		t.Fatalf("yaml.Marshal returned error: %v", err)
	}

	got, err := yaml.Marshal(builderYAMLExact(plain))
	if err != nil || string(got) != string(want) {
		t.Fatalf("YAML = %v\n%s\nwant\n%s", err, got, want)
	}

	changed := map[string]any{
		"\nkey": []any{"\n", "\n a", "\ta\nb"}, "k": map[string]string{"v": "\n\n a\n"}, "a\nb": "c\n",
	}

	got, err = yaml.Marshal(builderYAMLExact(changed))
	if err != nil {
		t.Fatalf("yaml.Marshal returned error: %v", err)
	}

	var loaded any
	if err := yaml.Unmarshal(got, &loaded); err != nil {
		t.Fatalf("YAML does not load: %v\n%s", err, got)
	}

	if !reflect.DeepEqual(asJSONValue(t, loaded), asJSONValue(t, changed)) {
		t.Fatalf("YAML loads as %s, want %s\n%s", asBuilderJSON(t, loaded), asBuilderJSON(t, changed), got)
	}
}

// TestBuilderBetaExportTopologyRequests checks the request as saving a draft
// checks it, and the topology name as publishing checks it.
func TestBuilderBetaExportTopologyRequests(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderBetaHarness(t)
	document := string(builderBetaDocument(t, "topo"))

	tests := []struct {
		name   string
		body   string
		status int
		// error is a part of the error's message or cause.
		error string
	}{
		{name: "missing document", body: `{"name":"topo"}`, status: http.StatusBadRequest, error: "document is required"},
		{
			name:   "unknown field",
			body:   `{"document":` + document + `,"mode":"topology"}`,
			status: http.StatusBadRequest,
			error:  `unknown field "mode"`,
		},
		{
			name:   "invalid document",
			body:   `{"document":{"apiVersion":"builder/v1","kind":"nope"}}`,
			status: http.StatusUnprocessableEntity,
			error:  "not a valid builder document",
		},
		{
			name:   "invalid name",
			body:   `{"document":` + document + `,"name":"lab topology"}`,
			status: http.StatusBadRequest,
			error:  `topology target "lab topology" is not a valid config name`,
		},
		{
			name:   "document larger than a draft holds",
			body:   `{"document":{"name":"` + strings.Repeat("x", bapi.MaxDocumentBytes) + `"}}`,
			status: http.StatusRequestEntityTooLarge,
			error:  "document is 5242891 bytes, limit is 5242880 bytes",
		},
		{
			name:   "oversized body",
			body:   `{"name":"` + strings.Repeat("x", builderBetaMaxRequestBytes) + `"}`,
			status: http.StatusRequestEntityTooLarge,
			error:  "request body is larger than",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderBetaRequest{
				method: http.MethodPost,
				path:   "/builder/export/topology",
				body:   tt.body,
				user:   builderBetaTestOwner,
			})

			var answer struct {
				Message string `json:"message"`
				Cause   string `json:"cause"`
			}
			harness.decode(recorder, &answer)

			if recorder.Code != tt.status || !strings.Contains(answer.Message+": "+answer.Cause, tt.error) {
				t.Fatalf("answer = %d %+v, want %d and %q", recorder.Code, answer, tt.status, tt.error)
			}
		})
	}

	// A document whose name leaves no topology name is named as its export
	// files are.
	unnamed := bdoc.NewDocument("!!!")

	response, exported := exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, unnamed, "", nil))
	if response.Name != "topology" || exported.Metadata.Name != "topology" {
		t.Fatalf("export name = %q, config %q, want topology", response.Name, exported.Metadata.Name)
	}
}

func TestBuilderTopologyNameMatchesPublishDialog(t *testing.T) {
	t.Parallel()

	// The cases of configName in src/js/test/builder/publish.test.js, and a
	// name that leaves none.
	for name, want := range map[string]string{
		"Untitled topology":  "Untitled-topology",
		"  lab #2 (copy) ":   "lab-2-copy",
		"core_net@site.v2":   "core_net@site.v2",
		" Lab topology (2) ": "Lab-topology-2",
		"":                   "topology",
		"!!":                 "topology",
	} {
		if got := builderTopologyName(name); got != want {
			t.Errorf("builderTopologyName(%q) = %q, want %q", name, got, want)
		}
	}
}
