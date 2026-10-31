package web

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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
	harness *builderHarness,
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

	return harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/export/topology",
		body:   string(body),
		user:   builderAlice,
		role:   role,
	})
}

// exportedBuilderTopology decodes a successful export and the config its
// YAML holds.
func exportedBuilderTopology(
	t *testing.T,
	harness *builderHarness,
	recorder *httptest.ResponseRecorder,
) (builderTopologyExportResponse, store.Config) {
	t.Helper()

	if recorder.Code != http.StatusOK {
		t.Fatalf("POST /builder/export/topology: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
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
	if err := json.Unmarshal([]byte(builderJSON(t, value)), &decoded); err != nil {
		t.Fatalf("decoding %T: %v", value, err)
	}

	return decoded
}

// issueSummary is the code and message of an issue.
type issueSummary struct {
	Code    bdoc.Code
	Message string
}

// issueSummaries returns the code and message of each issue.
func issueSummaries(issues []bdoc.Issue) []issueSummary {
	summaries := make([]issueSummary, 0, len(issues))
	for _, issue := range issues {
		summaries = append(summaries, issueSummary{Code: issue.Code, Message: issue.Message})
	}

	return summaries
}

// TestBuilderExportTopologyMatchesPublish exports the config a publish writes
// for the same document, for a view-only role, and writes nothing.
func TestBuilderExportTopologyMatchesPublish(t *testing.T) {
	harness := newBuilderHarness(t, includedTopologyFixture(t, "shared")...)
	document := generateBuilderDocument(t, harness, "Topology/root")
	document.Metadata.Name = " Lab topology (2) "

	if included := document.FindDevice("inc-host"); included == nil || included.Device.IncludedFrom != "shared" {
		t.Fatalf("inc-host is not marked as included: %s", builderJSON(t, document))
	}

	draft := createBuilderPublishDraft(t, harness, document)

	publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"Lab-topology-2","action":"create"}}`, http.StatusOK)

	stored, err := harness.getConfig("Topology/Lab-topology-2")
	if err != nil {
		t.Fatalf("getConfig(Topology/Lab-topology-2) after publishing error: %v", err)
	}

	writes := harness.configWrites
	documents := harness.store.Count(bapi.NamespacePublished)
	drafts := harness.store.Count(bapi.NamespaceDrafts)
	viewer := builderRole(builderPolicy([]string{"configs"}, []string{"*"}, []string{"list", "get"}))

	response, exported := exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, document, "", viewer))

	wantResponse := builderTopologyExportResponse{Name: "Lab-topology-2"}
	if diff := cmp.Diff(wantResponse, response,
		cmpopts.IgnoreFields(builderTopologyExportResponse{}, "YAML"), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("POST /builder/export/topology mismatch (-want +got):\n%s", diff)
	}

	// The spec is compared below, as JSON decodes it.
	wantConfig := store.Config{
		Version: stored.Version, Kind: stored.Kind, Metadata: store.ConfigMetadata{Name: stored.Metadata.Name},
	}
	if diff := cmp.Diff(wantConfig, exported, cmpopts.IgnoreFields(store.Config{}, "Spec"), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("exported config mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(asJSONValue(t, stored.Spec), asJSONValue(t, exported.Spec)); diff != "" {
		t.Errorf("exported spec mismatch with the published one (-want +got):\n%s", diff)
	}

	exportedSpec, err := (&bdoc.Topology{Spec: exported.Spec, VLANAliases: nil, Warnings: nil}).SpecV1()
	if err != nil {
		t.Fatalf("exported spec is not a v1 topology: %v", err)
	}

	storedSpec, err := (&bdoc.Topology{Spec: stored.Spec, VLANAliases: nil, Warnings: nil}).SpecV1()
	if err != nil {
		t.Fatalf("published spec is not a v1 topology: %v", err)
	}

	if diff := cmp.Diff(storedSpec, exportedSpec); diff != "" {
		t.Errorf("exported v1 topology mismatch with the published one (-want +got):\n%s", diff)
	}

	if hostnames := topologySpecHostnames(exported.Spec); !slices.Equal(hostnames, []string{"web"}) {
		t.Errorf("exported spec hostnames = %q, want [web]", hostnames)
	}

	if diff := cmp.Diff([]any{"shared"}, exported.Spec["includeTopologies"]); diff != "" {
		t.Errorf("exported spec includeTopologies mismatch (-want +got):\n%s", diff)
	}

	gotCounts := builderStoreCounts{
		ConfigWrites: harness.configWrites, Published: harness.store.Count(bapi.NamespacePublished),
		Drafts: harness.store.Count(bapi.NamespaceDrafts),
	}
	wantCounts := builderStoreCounts{ConfigWrites: writes, Published: documents, Drafts: drafts}

	if diff := cmp.Diff(wantCounts, gotCounts); diff != "" {
		t.Errorf("after an export, the store mismatch (-want +got):\n%s", diff)
	}

	// Opening a draft takes configs get. Listing them is not enough.
	lister := builderRole(builderPolicy([]string{"configs"}, []string{"*"}, []string{"list"}))
	if denied := exportBuilderTopology(t, harness, document, "", lister); denied.Code != http.StatusForbidden {
		t.Errorf("POST /builder/export/topology as a lister: status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	// A name the client sends is used.
	response, exported = exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, document, "lab@2", nil))
	if response.Name != "lab@2" {
		t.Errorf("POST /builder/export/topology named lab@2: name = %q, want lab@2", response.Name)
	}

	if exported.Metadata.Name != "lab@2" {
		t.Errorf("POST /builder/export/topology named lab@2: config name = %q, want lab@2", exported.Metadata.Name)
	}
}

// builderStoreCounts is how many configs were written and documents and
// drafts are stored.
type builderStoreCounts struct {
	ConfigWrites int
	Published    int
	Drafts       int
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
	document := bdoc.NewDocument("no-vlan")
	addExportHost(document, "host", fields...)

	return document
}

// addExportHost adds a host named hostname to document, with the given
// interfaces, each a DHCP Ethernet interface unless it says otherwise.
func addExportHost(document *bdoc.Document, hostname string, fields ...map[string]any) {
	interfaces := make([]any, 0, len(fields))

	for _, iface := range fields {
		entry := map[string]any{"type": "ethernet", "proto": "dhcp"}
		maps.Copy(entry, iface)
		interfaces = append(interfaces, entry)
	}

	document.Nodes = append(document.Nodes, bdoc.Node{
		ID: bdoc.DeviceNodeID(hostname), Kind: bdoc.NodeKindDevice, Label: hostname,
		Device: &bdoc.Device{
			Hostname: hostname,
			Spec: map[string]any{
				"type":     "VirtualMachine",
				"general":  map[string]any{"hostname": hostname},
				"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "miniccc.qc2"}}},
				"network":  map[string]any{"interfaces": interfaces},
			},
			Interfaces: []bdoc.InterfaceHandle{},
		},
	})
}

// TestBuilderExportTopologyRefusesAsPublishDoes refuses what fails phenix's
// config validation as Publish does, and exports what only Publish refuses.
func TestBuilderExportTopologyRefusesAsPublishDoes(t *testing.T) {
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
			// phenix stores it. minimega refuses it when the experiment starts.
			name: "empty VLANs",
			document: exportVLANDocument(
				map[string]any{"name": "eth0", "vlan": ""},
				map[string]any{"name": "eth1", "vlan": "  "},
				map[string]any{"name": "eth2", "vlan": ""},
				map[string]any{"name": "eth3", "vlan": ""},
				map[string]any{"name": "eth4", "vlan": "EXP"},
			),
			exports: true,
			refusal: "phenix cannot publish topology no-vlan: ",
		},
		{
			// phenix's schema requires the key.
			name:     "missing VLAN",
			document: exportVLANDocument(map[string]any{"name": "eth0"}),
			exports:  false,
			refusal:  "phenix cannot publish topology no-vlan: ",
		},
		{
			name:     "invalid MAC address",
			document: exportVLANDocument(map[string]any{"name": "eth0", "vlan": "EXP", "mac": "not-a-mac"}),
			exports:  false,
			refusal:  "Builder document cannot be published as topology no-vlan",
		},
		{
			name: "empty VLAN and invalid MAC address",
			document: exportVLANDocument(
				map[string]any{"name": "eth0", "vlan": ""},
				map[string]any{"name": "eth1", "vlan": "EXP", "mac": "not-a-mac"},
			),
			exports:       false,
			refusal:       "phenix cannot publish topology no-vlan: ",
			exportRefusal: "Builder document cannot be published as topology no-vlan",
			exportCause:   `Error at "/mac"`,
		},
		{
			// Publish names the shared address. The schema accepts it.
			name: "shared address and invalid MAC address",
			document: exportVLANDocument(
				map[string]any{"name": "eth0", "vlan": "EXP", "proto": "static", "address": "10.0.0.5", "mask": 24},
				map[string]any{"name": "eth1", "vlan": "EXP", "proto": "static", "address": "10.0.0.5", "mask": 24},
				map[string]any{"name": "eth2", "vlan": "EXP", "mac": "not-a-mac"},
			),
			exports:       false,
			refusal:       `phenix cannot publish topology no-vlan: IP address 10.0.0.5 on VLAN "EXP" is used by `,
			exportRefusal: "Builder document cannot be published as topology no-vlan",
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
			refusal: "phenix cannot publish topology no-vlan: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderHarness(t)
			draft := createBuilderPublishDraft(t, harness, tt.document)

			_, publishRefusal := publishBuilderDraftAs(t, harness, draft, nil,
				`{"mode":"topology","topology":{"name":"no-vlan","action":"create"}}`, http.StatusUnprocessableEntity)

			if !strings.HasPrefix(publishRefusal.Message, tt.refusal) {
				t.Errorf("publish message = %q, want it to start %q", publishRefusal.Message, tt.refusal)
			}

			recorder := exportBuilderTopology(t, harness, tt.document, "", nil)

			if !tt.exports {
				if recorder.Code != http.StatusUnprocessableEntity {
					t.Fatalf("POST /builder/export/topology: status = %d, want the publish status %d: %s",
						recorder.Code, http.StatusUnprocessableEntity, recorder.Body)
				}

				exportErr := builderRefusal(t, recorder)

				want := tt.exportRefusal
				if want == "" {
					want = publishRefusal.Message
				}

				if exportErr.Message != want {
					t.Errorf("POST /builder/export/topology message = %q, want %q", exportErr.Message, want)
				}

				if tt.exportCause != "" && (!strings.Contains(exportErr.Cause, tt.exportCause) ||
					strings.Contains(exportErr.Cause, "no VLAN") || strings.Contains(exportErr.Cause, " is used by ")) {
					t.Errorf("POST /builder/export/topology cause = %q, want it to hold %q and name no VLAN or shared address",
						exportErr.Cause, tt.exportCause)
				}

				return
			}

			response, exported := exportedBuilderTopology(t, harness, recorder)

			message := strings.TrimPrefix(publishRefusal.Message, tt.refusal)
			if !strings.HasSuffix(message, ". 1 more interface has no VLAN") {
				t.Errorf("publish message = %q, want it to end with the one more interface that has no VLAN", message)
			}

			want := []issueSummary{{Code: bdoc.CodeInterfaceVLANMissing, Message: message}}
			if diff := cmp.Diff(want, issueSummaries(response.PublishBlockers)); diff != "" {
				t.Errorf("POST /builder/export/topology publish blockers mismatch (-want +got):\n%s", diff)
			}

			if hostnames := topologySpecHostnames(exported.Spec); !slices.Equal(hostnames, []string{"host"}) {
				t.Errorf("exported spec hostnames = %q, want [host]", hostnames)
			}
		})
	}
}

// TestBuilderExportTopologyNamesSharedAddresses exports interfaces that share
// an address, and names it in publishBlockers as Publish's refusal does.
func TestBuilderExportTopologyNamesSharedAddresses(t *testing.T) {
	const (
		refusal = "phenix cannot publish topology shared: "
		shared  = `IP address 10.0.0.5 on VLAN "EXP" is used by interface "eth0" of device "aa" and interface "eth0" of device "bb"`
		fix     = " has no VLAN: connect it to a network, or type a VLAN for it"
		noVLAN  = `Interface "eth1" of device "aa"` + fix + `. Interface "eth1" of device "bb"` + fix
	)

	tests := []struct {
		name string
		eth1 bool
		// publish is Publish's message after refusal.
		publish  string
		blockers []string
	}{
		{name: "shared address", eth1: false, publish: shared, blockers: []string{shared}},
		{name: "shared address and no VLAN", eth1: true, publish: noVLAN, blockers: []string{noVLAN, shared}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := bdoc.NewDocument("shared")

			for _, hostname := range []string{"aa", "bb"} {
				fields := []map[string]any{
					{"name": "eth0", "vlan": "EXP", "proto": "static", "address": "10.0.0.5", "mask": 24},
				}

				if tt.eth1 {
					fields = append(fields, map[string]any{"name": "eth1", "vlan": ""})
				}

				addExportHost(document, hostname, fields...)
			}

			harness := newBuilderHarness(t)
			draft := createBuilderPublishDraft(t, harness, document)

			_, publishRefusal := publishBuilderDraftAs(t, harness, draft, nil,
				`{"mode":"topology","topology":{"name":"shared","action":"create"}}`, http.StatusUnprocessableEntity)

			if want := refusal + tt.publish; publishRefusal.Message != want {
				t.Errorf("publish message = %q, want %q", publishRefusal.Message, want)
			}

			// The refusal names each problem as an issue at its device.
			code := bdoc.CodeInterfaceIPShared
			if tt.eth1 {
				code = bdoc.CodeInterfaceVLANMissing
			}

			if publishRefusal.Code != string(bdoc.CodePublishBlocked) {
				t.Errorf("publish refusal code = %q, want %q", publishRefusal.Code, bdoc.CodePublishBlocked)
			}

			if len(publishRefusal.Issues) == 0 || slices.ContainsFunc(publishRefusal.Issues, func(issue bdoc.Issue) bool {
				return issue.Code != code || issue.NodeID == "" || issue.Severity != bdoc.SeverityError
			}) {
				t.Errorf("publish refusal issues = %+v, want errors of code %s at their devices", publishRefusal.Issues, code)
			}

			response, exported := exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, document, "shared", nil))

			if diff := cmp.Diff(tt.blockers, bdoc.IssueMessages(response.PublishBlockers)); diff != "" {
				t.Errorf("POST /builder/export/topology publish blocker messages mismatch (-want +got):\n%s", diff)
			}

			if hostnames := topologySpecHostnames(exported.Spec); !slices.Equal(hostnames, []string{"aa", "bb"}) {
				t.Errorf("exported spec hostnames = %q, want [aa bb]", hostnames)
			}

			if harness.configWrites != 0 {
				t.Errorf("the export and the refused publication wrote %d configs, want none", harness.configWrites)
			}

			if len(response.PublishBlockers) == 0 {
				return
			}

			// The first is what Publish's refusal says.
			if got, want := response.PublishBlockers[0].Message, strings.TrimPrefix(publishRefusal.Message, refusal); got != want {
				t.Errorf("first publish blocker message = %q, want the publish message %q", got, want)
			}

			// The shared address blocker is at the first device that uses it.
			last := response.PublishBlockers[len(response.PublishBlockers)-1]
			want := bdoc.Issue{
				Code: bdoc.CodeInterfaceIPShared, Severity: bdoc.SeverityError, Message: shared,
				Path:   "nodes[0].device.spec.network.interfaces[0].address",
				NodeID: bdoc.DeviceNodeID("aa"), Field: "spec.network.interfaces.0.address",
			}
			if diff := cmp.Diff(want, last); diff != "" {
				t.Errorf("last publish blocker mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestBuilderExportTopologyNamesHostnames exports the reserved hostname "all"
// with a blocker, and refuses a one-character hostname as Publish does.
func TestBuilderExportTopologyNamesHostnames(t *testing.T) {
	const refusal = "phenix cannot publish topology hostnames: "

	tests := []struct {
		name, hostname string
		// reason starts what Publish's refusal says after refusal.
		reason  string
		exports bool
	}{
		{name: "reserved", hostname: "all", reason: "Hostname 'all' is reserved: ", exports: true},
		{name: "one character", hostname: "a", reason: "Hostname 'a' is 1 character long: ", exports: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := bdoc.NewDocument("hostnames")
			addExportHost(document, tt.hostname, map[string]any{"name": "eth0", "vlan": "EXP"})

			harness := newBuilderHarness(t)
			draft := createBuilderPublishDraft(t, harness, document)

			_, publishRefusal := publishBuilderDraftAs(t, harness, draft, nil,
				`{"mode":"topology","topology":{"name":"hostnames","action":"create"}}`, http.StatusUnprocessableEntity)

			if !strings.HasPrefix(publishRefusal.Message, refusal+tt.reason) {
				t.Errorf("publish message = %q, want it to start %q", publishRefusal.Message, refusal+tt.reason)
			}

			recorder := exportBuilderTopology(t, harness, document, "hostnames", nil)

			if !tt.exports {
				exportRefusal := builderRefusal(t, recorder)

				if recorder.Code != http.StatusUnprocessableEntity {
					t.Errorf("POST /builder/export/topology: status = %d, want %d", recorder.Code, http.StatusUnprocessableEntity)
				}

				if exportRefusal.Message != publishRefusal.Message {
					t.Errorf("POST /builder/export/topology message = %q, want the publish message %q",
						exportRefusal.Message, publishRefusal.Message)
				}
			} else {
				response, _ := exportedBuilderTopology(t, harness, recorder)

				want := []issueSummary{
					{Code: bdoc.CodeNodeHostnameReserved, Message: strings.TrimPrefix(publishRefusal.Message, refusal)},
				}
				if diff := cmp.Diff(want, issueSummaries(response.PublishBlockers)); diff != "" {
					t.Errorf("POST /builder/export/topology publish blockers mismatch (-want +got):\n%s", diff)
				}

				for _, blocker := range response.PublishBlockers {
					if !strings.HasSuffix(blocker.Path, ".device.hostname") || blocker.NodeID == "" {
						t.Errorf("publish blocker at node %q path %q, want the device hostname", blocker.NodeID, blocker.Path)
					}
				}
			}

			gotCounts := builderStoreCounts{
				ConfigWrites: harness.configWrites, Published: harness.store.Count(bapi.NamespacePublished),
			}
			if diff := cmp.Diff(builderStoreCounts{}, gotCounts); diff != "" {
				t.Errorf("the export and the refused publication wrote (-want +got):\n%s", diff)
			}
		})
	}
}

// TestBuilderExportTopologyKeepsStrings exports strings yaml.v3 would not read
// back from a block scalar so the file loads as the config Publish writes.
func TestBuilderExportTopologyKeepsStrings(t *testing.T) {
	document := exportVLANDocument(map[string]any{"name": "eth0", "vlan": "EXP"})
	spec := document.Nodes[0].Device.Spec

	general, _ := spec["general"].(map[string]any)
	general["description"] = "\n  indented first line\nsecond"
	spec["commands"] = []any{"\n", "\n\n", "\tfirst\nsecond", "\n#x", "one\ntwo\n"}
	spec["advanced"] = map[string]any{"\nkey": "\n\tvalue", "plain": "a\n b"}

	harness := newBuilderHarness(t)
	response, _ := exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, document, "", nil))

	loaded, err := store.NewConfigFromYAML([]byte(response.YAML))
	if err != nil {
		t.Fatalf("exported YAML does not load: %v\n%s", err, response.YAML)
	}

	published, _, _, err := document.PublishTopology(response.Name)
	if err != nil {
		t.Fatalf("PublishTopology returned error: %v", err)
	}

	if diff := cmp.Diff(asJSONValue(t, published.Spec), asJSONValue(t, loaded.Spec)); diff != "" {
		t.Errorf("exported spec mismatch with the published one (-want +got):\n%s\nYAML:\n%s", diff, response.YAML)
	}

	for _, want := range []string{"- |\n", "plain: |-\n"} {
		if !strings.Contains(response.YAML, want) {
			t.Errorf("exported YAML does not hold %q:\n%s", want, response.YAML)
		}
	}
}

// TestBuilderExportTopologyRequests checks the request as saving a draft
// checks it, and the topology name as publishing checks it.
func TestBuilderExportTopologyRequests(t *testing.T) {
	harness := newBuilderHarness(t)
	document := string(builderDocument(t, "topo"))

	tests := []struct {
		name   string
		body   string
		status int
		// error is a part of the error's message or cause.
		error string
	}{
		{name: "missing document", body: `{"name":"topo"}`, status: http.StatusBadRequest, error: "the request needs a Builder document"},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(builderRequest{
				method: http.MethodPost,
				path:   "/builder/export/topology",
				body:   tt.body,
				user:   builderAlice,
			})

			answer := builderRefusal(t, recorder)

			if recorder.Code != tt.status {
				t.Errorf("POST /builder/export/topology: status = %d, want %d", recorder.Code, tt.status)
			}

			if said := answer.Message + ": " + answer.Cause; !strings.Contains(said, tt.error) {
				t.Errorf("POST /builder/export/topology said %q, want it to hold %q", said, tt.error)
			}
		})
	}

	// A document whose name leaves no topology name is named "topology".
	unnamed := bdoc.NewDocument("!!!")

	response, exported := exportedBuilderTopology(t, harness, exportBuilderTopology(t, harness, unnamed, "", nil))
	if response.Name != "topology" {
		t.Errorf("POST /builder/export/topology of document !!!: name = %q, want topology", response.Name)
	}

	if exported.Metadata.Name != "topology" {
		t.Errorf("POST /builder/export/topology of document !!!: config name = %q, want topology", exported.Metadata.Name)
	}
}
