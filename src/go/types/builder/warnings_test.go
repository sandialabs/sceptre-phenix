package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

// warningCorpus is testdata/warning-corpus.json, which validate.contract.test.js runs
// through the front end's validateDocument. Codes are the codes of design
// warnings, and Blockers the codes of the errors only publishing refuses.
type warningCorpus struct {
	Document string         `json:"document"`
	Codes    []builder.Code `json:"codes"`
	Blockers []builder.Code `json:"blockers"`
	Cases    []struct {
		Name string `json:"name"`
		Set  []struct {
			Path   []any `json:"path"`
			Value  any   `json:"value"`
			Delete bool  `json:"delete"`
		} `json:"set"`
		Warnings []builder.Issue `json:"warnings"`
	} `json:"cases"`
}

// warningLines returns the issues as sorted lines of their code, path, field
// and message, to compare two lists of them whatever their order.
func warningLines(issues []builder.Issue) []string {
	lines := make([]string, 0, len(issues))

	for _, issue := range issues {
		lines = append(lines, string(issue.Code)+" "+issue.Path+" ["+issue.Field+"]: "+issue.Message)
	}

	slices.Sort(lines)

	return lines
}

func TestWarningCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "warning-corpus.json"))
	if err != nil {
		t.Fatalf("reading corpus: %v", err)
	}

	var corpus warningCorpus

	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decoding corpus: %v", err)
	}

	base, err := os.ReadFile(filepath.Join("testdata", corpus.Document))
	if err != nil {
		t.Fatalf("reading %s: %v", corpus.Document, err)
	}

	names := make(map[string]bool, len(corpus.Cases))
	covered := make(map[builder.Code]bool, len(corpus.Codes))

	for _, test := range corpus.Cases {
		if names[test.Name] {
			t.Errorf("warning corpus has case %q twice, want each name once", test.Name)
		}

		names[test.Name] = true

		for _, warning := range test.Warnings {
			covered[warning.Code] = true
		}

		t.Run(test.Name, func(t *testing.T) {
			var doc any

			if err := json.Unmarshal(base, &doc); err != nil {
				t.Fatalf("decoding %s: %v", corpus.Document, err)
			}

			for _, set := range test.Set {
				setIn(t, doc, set.Path, set.Value, set.Delete)
			}

			encoded, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("encoding: %v", err)
			}

			parsed, err := builder.Parse(encoded)
			if err != nil {
				t.Fatalf("Parse() = %v, want nil", err)
			}

			got := corpusIssues(t, parsed, corpus.Codes, corpus.Blockers)

			if diff := cmp.Diff(warningLines(test.Warnings), warningLines(got)); diff != "" {
				t.Errorf("DesignWarnings() and PublishBlockers(lab) mismatch (-want +got):\n%s", diff)
			}
		})
	}

	for _, code := range slices.Concat(corpus.Codes, corpus.Blockers) {
		want := builder.SeverityWarning
		if slices.Contains(corpus.Blockers, code) {
			want = builder.SeverityError
		}

		if info, ok := builder.LookupCode(code); !ok || info.Severity != want {
			t.Errorf("LookupCode(%s) = %+v, %t, want a registered %s", code, info, ok, want)
		}

		if !covered[code] {
			t.Errorf("warning corpus has no case with a %s issue, want one at least", code)
		}
	}
}

// corpusIssues returns the design warnings of doc of the codes, and the
// issues of the publish blockers of doc of the blocker codes. A blocker
// code among the design warnings is returned too, so a case fails on it.
func corpusIssues(t *testing.T, doc *builder.Document, codes, blockers []builder.Code) []builder.Issue {
	t.Helper()

	var issues []builder.Issue

	for _, warning := range doc.DesignWarnings() {
		if slices.Contains(codes, warning.Code) || slices.Contains(blockers, warning.Code) {
			issues = append(issues, warning)
		}
	}

	refusals, err := doc.PublishBlockers("lab")
	if err != nil {
		t.Fatalf("PublishBlockers(lab) = %v, want the blockers", err)
	}

	for _, refusal := range refusals {
		for _, issue := range builder.ErrorIssues(refusal) {
			if slices.Contains(blockers, issue.Code) {
				issues = append(issues, issue)
			}
		}
	}

	return issues
}

func TestDesignWarningsOverlappingSubnets(t *testing.T) {
	tests := []struct {
		name    string
		subnets []string
		// want is, for each network that overlaps another, by name, the name
		// of the network its warning names.
		want map[string]string
	}{
		{
			name: "nested three deep", subnets: []string{"10.0.0.0/8", "10.1.0.0/16", "10.1.2.0/24"},
			want: map[string]string{"n0": "n1", "n1": "n0", "n2": "n1"},
		},
		{
			name: "one subnet holds two", subnets: []string{"10.1.1.0/24", "10.1.0.0/16", "10.1.2.0/24"},
			want: map[string]string{"n0": "n1", "n1": "n0", "n2": "n1"},
		},
		{
			name: "a large subnet after a small one ends", subnets: []string{"10.0.0.0/24", "10.0.1.0/24", "10.0.0.0/23"},
			want: map[string]string{"n0": "n2", "n1": "n2", "n2": "n0"},
		},
		{
			name: "apart", subnets: []string{"10.0.0.0/24", "10.0.1.0/24", "192.168.0.0/16"},
			want: map[string]string{},
		},
		{
			name: "invalid and blank subnets", subnets: []string{"10.0.0.0/24", "", "10.0.0.1/24"},
			want: map[string]string{},
		},
		{
			name: "everything and one host", subnets: []string{"0.0.0.0/0", "255.255.255.255/32"},
			want: map[string]string{"n0": "n1", "n1": "n0"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			doc.Networks = nil

			for i, subnet := range test.subnets {
				name := fmt.Sprintf("n%d", i)
				doc.Networks = append(doc.Networks, builder.Network{
					ID: builder.NetworkID(name), Name: name, Alias: nil, Description: "", Color: "",
					LineStyle: "", Subnet: subnet, Gateway: "",
				})
			}

			got := map[string]string{}

			for _, warning := range doc.DesignWarnings() {
				if warning.Code != builder.CodeNetworkSubnetOverlap {
					continue
				}

				for _, network := range doc.Networks {
					if network.ID == warning.NetworkID {
						got[network.Name] = namedOther(doc, warning.Message)
					}
				}
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("DesignWarnings() overlaps of %q mismatch (-want +got):\n%s", test.subnets, diff)
			}
		})
	}
}

// namedOther is the name of the network whose subnet an overlap warning
// says the subnet overlaps, or "" for none.
func namedOther(doc *builder.Document, message string) string {
	for _, network := range doc.Networks {
		if strings.Contains(message, fmt.Sprintf("overlaps subnet %s of network %q", network.Subnet, network.Name)) {
			return network.Name
		}
	}

	return ""
}

// A device from an included topology is its own topology's to fix: it gets
// no warning about its rulesets or addresses, but it holds a gateway.
func TestDesignWarningsLeaveIncludedDevices(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Source.IncludeTopologies = []string{"shared"}
	doc.Networks[0].Subnet = "10.9.0.0/24"
	doc.Networks[0].Gateway = "10.9.0.1"

	router := nodeByHostname(t, doc, "router")
	router.Device.IncludedFrom = "shared"
	router.Device.Spec["network"].(map[string]any)["rulesets"] = []any{map[string]any{"name": "in"}}
	router.Device.Spec["network"].(map[string]any)["interfaces"].([]any)[0].(map[string]any)["address"] = "10.9.0.1"

	host := nodeByHostname(t, doc, "host-a")

	want := []string{string(builder.CodeInterfaceIPOutside) + " " + host.ID}

	warnings := doc.DesignWarnings()
	got := make([]string, 0, len(warnings))

	for _, warning := range warnings {
		got = append(got, string(warning.Code)+" "+warning.NodeID)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("DesignWarnings() of a document with an included router mismatch (-want +got):\n%s", diff)
	}
}

// Publishing lists the design warnings with its own.
func TestPublishTopologyListsDesignWarnings(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Networks[0].Subnet = "10.0.0.0/24"
	doc.Networks[0].Gateway = "10.0.0.254"

	// The fixture's dhcp interface has no VLAN, which blocks publishing. The
	// warnings come with the refusal.
	_, _, warnings, err := doc.PublishTopology("lab")
	if err == nil {
		t.Errorf("PublishTopology(lab) = nil error, want the interface without a VLAN refused")
	}

	found := slices.ContainsFunc(warnings, func(warning builder.Issue) bool {
		return warning.Code == builder.CodeNetworkGatewayUnowned && warning.NetworkID == doc.Networks[0].ID &&
			warning.Path == "networks[0].gateway"
	})
	if !found {
		t.Errorf("PublishTopology(lab) warnings = %+v, want a %s warning", warnings, builder.CodeNetworkGatewayUnowned)
	}
}

// A device setting that makes the experiment fail to start blocks
// publishing, after the other publish checks, and is not a design warning.
func TestPublishTopologyRefusesDeviceSetup(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	router := nodeByHostname(t, doc, "router")
	router.Device.Spec["type"] = "Router"
	router.Device.Spec["hardware"].(map[string]any)["os_type"] = "vyos"

	ifaces := router.Device.Spec["network"].(map[string]any)["interfaces"].([]any)
	ifaces[0].(map[string]any)["proto"] = "ospf"
	ifaces[1].(map[string]any)["vlan"] = "EXP"

	_, _, warnings, err := doc.PublishTopology("lab")

	var setup *builder.DeviceSetupError
	if !errors.As(err, &setup) {
		t.Fatalf("PublishTopology(lab) = %v, want a *DeviceSetupError", err)
	}

	issues := builder.ErrorIssues(err)
	if len(issues) != 1 || issues[0].Code != builder.CodeDeviceOSPFMissing || issues[0].NodeID != router.ID ||
		issues[0].Field != "spec.network.interfaces.0.proto" || issues[0].Severity != builder.SeverityError {
		t.Errorf("ErrorIssues(PublishTopology(lab)) = %+v, want one %s error at the router's eth0 proto",
			issues, builder.CodeDeviceOSPFMissing)
	}

	if slices.ContainsFunc(warnings, func(warning builder.Issue) bool {
		return warning.Code == builder.CodeDeviceOSPFMissing
	}) {
		t.Errorf("PublishTopology(lab) warnings = %+v, want no %s among them", warnings, builder.CodeDeviceOSPFMissing)
	}

	// An interface without a VLAN is refused first.
	delete(ifaces[1].(map[string]any), "vlan")

	if _, _, _, err := doc.PublishTopology("lab"); errors.As(err, &setup) {
		t.Errorf("PublishTopology(lab) = %v, want the interface without a VLAN refused first", err)
	}

	blockers, err := doc.PublishBlockers("lab")
	if err != nil || len(blockers) != 2 || !errors.As(blockers[1], &setup) {
		t.Errorf("PublishBlockers(lab) = %v, %v, want the VLAN blocker, then the device setup", blockers, err)
	}
}

// A device with no drive blocks publishing with a device setup error, and an
// export gives that error in place of the phenix schema text. An external
// device needs no drive.
func TestPublishTopologyRefusesDeviceWithNoDrive(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	router := nodeByHostname(t, doc, "router")
	ifaces := router.Device.Spec["network"].(map[string]any)["interfaces"].([]any)
	ifaces[1].(map[string]any)["vlan"] = "EXP"

	host := nodeByHostname(t, doc, "host-a")
	hardware := host.Device.Spec["hardware"].(map[string]any)
	hardware["drives"] = []any{}

	if err := doc.Validate(); err != nil {
		t.Fatalf("the draft must stay valid: %v", err)
	}

	var setup *builder.DeviceSetupError
	if _, _, _, err := doc.PublishTopology("lab"); !errors.As(err, &setup) {
		t.Fatalf("PublishTopology(lab) = %v, want a *DeviceSetupError", err)
	}

	issues := setup.Issues()
	if len(issues) != 1 || issues[0].Code != builder.CodeDeviceDrivesNone || issues[0].NodeID != host.ID ||
		issues[0].Field != "spec.hardware.drives" || issues[0].Severity != builder.SeverityError ||
		!strings.Contains(issues[0].Message, `"host-a" has no drive`) {
		t.Errorf("DeviceSetupError.Issues() = %+v, want one %s error at the drives of host-a",
			issues, builder.CodeDeviceDrivesNone)
	}

	if _, err := doc.ExportTopologyConfig("lab"); !errors.As(err, &setup) {
		t.Errorf("ExportTopologyConfig(lab) = %v, want the *DeviceSetupError", err)
	}

	host.Device.Spec["external"] = true

	if _, _, _, err := doc.PublishTopology("lab"); err != nil {
		t.Errorf("PublishTopology(lab) = %v, want an external device with no drive published", err)
	}
}

// A device whose phenix/default-apps annotation is false gets no warning
// about what the default apps configure, but its file injections still
// apply or not.
func TestDesignWarningsLeaveDevicesWithoutDefaultApps(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	router := nodeByHostname(t, doc, "router")
	router.Device.Spec["type"] = "Router"
	router.Device.Spec["hardware"].(map[string]any)["os_type"] = "vyatta"
	router.Device.Spec["annotations"] = map[string]any{"phenix/default-apps": false}
	router.Device.Spec["injections"] = []any{map[string]any{"src": "a", "dst": "/b"}}
	router.Device.Spec["general"].(map[string]any)["snapshot"] = false
	router.Device.Spec["network"].(map[string]any)["interfaces"].([]any)[0].(map[string]any)["proto"] = "ospf"

	warnings := doc.DesignWarnings()
	got := make([]builder.Code, 0, len(warnings))

	for _, warning := range warnings {
		got = append(got, warning.Code)
	}

	if diff := cmp.Diff([]builder.Code{builder.CodeDeviceInjectionsUnapplied}, got); diff != "" {
		t.Errorf("DesignWarnings() codes mismatch (-want +got):\n%s", diff)
	}

	if blockers, err := doc.PublishBlockers("lab"); err != nil || slices.ContainsFunc(blockers, func(blocker error) bool {
		var setup *builder.DeviceSetupError

		return errors.As(blocker, &setup)
	}) {
		t.Errorf("PublishBlockers(lab) = %v, %v, want no device setup blocker", blockers, err)
	}
}

// Only an IPv4 address can be outside an IPv4 subnet, and a spec interface that is not an object is
// on no network.
func TestDesignWarningsSkipInterfacesWithoutIPv4(t *testing.T) {
	for name, test := range map[string]struct {
		address string
		want    []builder.Code
	}{
		"IPv4 outside the subnet": {address: "10.1.0.5", want: []builder.Code{builder.CodeInterfaceIPOutside}},
		"IPv6":                    {address: "2001:db8::5", want: []builder.Code{}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			doc.Networks[0].Subnet = "10.0.0.0/24"
			doc.Networks[0].Gateway = "10.0.0.1"

			router := nodeByHostname(t, doc, "router")
			network, _ := router.Device.Spec["network"].(map[string]any)
			ifaces, _ := network["interfaces"].([]any)
			network["interfaces"] = append(ifaces, "eth2")
			ifaces[0].(map[string]any)["ruleset_in"] = "in"
			network["rulesets"] = []any{map[string]any{"name": "in"}}
			router.Device.Spec["type"] = "Router"
			router.Device.Spec["hardware"].(map[string]any)["os_type"] = "vyatta"

			host := nodeByHostname(t, doc, "host-a")
			host.Device.Spec["network"].(map[string]any)["interfaces"].([]any)[0].(map[string]any)["address"] = test.address

			warnings := doc.DesignWarnings()
			got := make([]builder.Code, 0, len(warnings))

			for _, warning := range warnings {
				got = append(got, warning.Code)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("DesignWarnings() codes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// warningFields returns the field of each design warning of doc with the code.
func warningFields(doc *builder.Document, code builder.Code) []string {
	fields := []string{}

	for _, warning := range doc.DesignWarnings() {
		if warning.Code == code {
			fields = append(fields, warning.Field)
		}
	}

	return fields
}

// A device with file injections gets a warning about its drives only when its first drive has
// inject_partition 0, as an int or as a float.
func TestDesignWarningsInjectionsReadTheFirstDrive(t *testing.T) {
	const partition = "spec.hardware.drives.0.inject_partition"

	for name, test := range map[string]struct {
		drives any
		want   []string
	}{
		"no drives":                {drives: []any{}, want: []string{}},
		"partition 0":              {drives: []any{map[string]any{"image": "a.qc2", "inject_partition": 0}}, want: []string{partition}},
		"partition 0 as a float":   {drives: []any{map[string]any{"image": "a.qc2", "inject_partition": 0.0}}, want: []string{partition}},
		"partition 1 as a float":   {drives: []any{map[string]any{"image": "a.qc2", "inject_partition": 1.0}}, want: []string{}},
		"partition that is unset":  {drives: []any{map[string]any{"image": "a.qc2"}}, want: []string{}},
		"partition that is text 0": {drives: []any{map[string]any{"image": "a.qc2", "inject_partition": "0"}}, want: []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			host := nodeByHostname(t, doc, "host-a")
			host.Device.Spec["injections"] = []any{map[string]any{"src": "a", "dst": "/b"}}
			host.Device.Spec["hardware"].(map[string]any)["drives"] = test.drives

			got := warningFields(doc, builder.CodeDeviceInjectionsUnapplied)
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("DesignWarnings() %s fields mismatch (-want +got):\n%s", builder.CodeDeviceInjectionsUnapplied, diff)
			}
		})
	}
}

// An ospf interface of a minirouter without an IPv4 address is in no area network, and gets no
// warning that it is outside one.
func TestDesignWarningsOSPFSkipInterfacesWithoutAddress(t *testing.T) {
	for name, test := range map[string]struct {
		address any
		want    []string
	}{
		"no address":           {address: nil, want: []string{}},
		"an IPv6 address":      {address: "2001:db8::1", want: []string{}},
		"an address elsewhere": {address: "192.168.1.1", want: []string{"spec.network.interfaces.1.address"}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			router := nodeByHostname(t, doc, "router")
			router.Device.Spec["type"] = "Router"
			router.Device.Spec["hardware"].(map[string]any)["os_type"] = "minirouter"

			network, _ := router.Device.Spec["network"].(map[string]any)
			network["ospf"] = map[string]any{"areas": []any{map[string]any{
				"area_id": 0, "area_networks": []any{map[string]any{"network": "10.0.0.0/24"}},
			}}}

			ifaces, _ := network["interfaces"].([]any)
			ifaces[0].(map[string]any)["proto"] = "ospf"

			eth1, _ := ifaces[1].(map[string]any)
			eth1["proto"] = "ospf"

			if test.address != nil {
				eth1["address"] = test.address
			}

			got := warningFields(doc, builder.CodeInterfaceOSPFOutside)
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("DesignWarnings() %s fields mismatch (-want +got):\n%s", builder.CodeInterfaceOSPFOutside, diff)
			}

			if unused := warningFields(doc, builder.CodeDeviceOSPFNetworkUnused); len(unused) != 0 {
				t.Errorf("DesignWarnings() %s fields = %q, want none: eth0 is in the area network",
					builder.CodeDeviceOSPFNetworkUnused, unused)
			}
		})
	}
}

// The ntp-server device publishes an interface on the VLAN of its connection to a switch. A
// connection that does not join it to a switch is ignored, and the interface keeps its spec VLAN.
func TestDesignWarningsNTPServerReadsPublishedVLANs(t *testing.T) {
	connection := func(target, handle string) builder.Edge {
		return builder.Edge{
			ID: "c5e3f1a2-6b4d-5c8e-9f0a-1b2c3d4e5f60", SourceNodeID: idDevRouter, SourceHandleID: idHRouterEth1,
			TargetNodeID: target, TargetHandleID: handle, NetworkID: idNetExp,
		}
	}

	for name, test := range map[string]struct {
		edit func(doc *builder.Document, ifaces []any) []any
		want []string
	}{
		"an unconnected interface": {
			edit: func(_ *builder.Document, ifaces []any) []any { return ifaces },
			want: []string{},
		},
		"a connection to a node that is not there": {
			edit: func(doc *builder.Document, ifaces []any) []any {
				doc.Edges = append(doc.Edges, connection("missing", ""))

				return ifaces
			},
			want: []string{},
		},
		"a connection to another device": {
			edit: func(doc *builder.Document, ifaces []any) []any {
				doc.Edges = append(doc.Edges, connection(idDevHost, idHHostEth0))

				return ifaces
			},
			want: []string{},
		},
		"a connection to a switch": {
			edit: func(doc *builder.Document, ifaces []any) []any {
				doc.Edges = append(doc.Edges, connection(idSwExp, ""))

				return ifaces
			},
			want: []string{"spec.labels.ntp-server"},
		},
		"an interface that is not an object": {
			edit: func(_ *builder.Document, ifaces []any) []any { return []any{ifaces[0], "mgmt"} },
			want: []string{"spec.labels.ntp-server"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			router := nodeByHostname(t, doc, "router")
			router.Device.Spec["type"] = "Router"
			router.Device.Spec["hardware"].(map[string]any)["os_type"] = "vyatta"
			router.Device.Spec["labels"] = map[string]any{"ntp-server": "eth0"}

			network, _ := router.Device.Spec["network"].(map[string]any)
			ifaces, _ := network["interfaces"].([]any)
			ifaces[1].(map[string]any)["vlan"] = "mgmt"
			network["interfaces"] = test.edit(doc, ifaces)

			got := warningFields(doc, builder.CodeDeviceNTPServerNoMgmt)
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("DesignWarnings() %s fields mismatch (-want +got):\n%s", builder.CodeDeviceNTPServerNoMgmt, diff)
			}
		})
	}
}
