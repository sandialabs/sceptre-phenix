package builder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/types/builder"
)

// fakePreflight is a [PreflightEnvironment] whose sources each test sets. A
// source left nil answers with nothing. An error set for it is returned.
type fakePreflight struct {
	hosts         []PreflightHost
	hostsErr      error
	images        []PreflightImage
	imagesErr     error
	apps          []string
	appsErr       error
	appsCalled    bool
	scenarios     map[string][]string
	scenarioErrs  map[string]error
	experiment    PreflightExperiment
	experimentErr error
	vlans         []PreflightVLAN
	vlansHidden   bool
	vlansErr      error
	excepted      string
	bridges       map[string][]string
	bridgesErr    error
	// block, when set, keeps ClusterHosts from answering until it is closed.
	block chan struct{}
}

func (f *fakePreflight) ClusterHosts(context.Context) ([]PreflightHost, error) {
	if f.block != nil {
		<-f.block
	}

	return f.hosts, f.hostsErr
}

func (f *fakePreflight) DiskImages(context.Context) ([]PreflightImage, error) {
	return f.images, f.imagesErr
}

func (f *fakePreflight) Apps(context.Context) ([]string, error) {
	f.appsCalled = true

	return f.apps, f.appsErr
}

func (f *fakePreflight) ScenarioApps(_ context.Context, scenario string) ([]string, error) {
	if err := f.scenarioErrs[scenario]; err != nil {
		return nil, err
	}

	apps, ok := f.scenarios[scenario]
	if !ok {
		return nil, fmt.Errorf("scenario %s: %w", scenario, ErrNotFound)
	}

	return apps, nil
}

func (f *fakePreflight) Experiment(context.Context, string) (PreflightExperiment, error) {
	return f.experiment, f.experimentErr
}

func (f *fakePreflight) VLANsInUse(_ context.Context, except string) (PreflightVLANs, error) {
	f.excepted = except

	return PreflightVLANs{InUse: f.vlans, Hidden: f.vlansHidden}, f.vlansErr
}

func (f *fakePreflight) Bridges(context.Context) (map[string][]string, error) {
	return f.bridges, f.bridgesErr
}

// preflightNode is a device node named hostname with the given spec.
func preflightNode(id, hostname string, spec map[string]any, handles ...builder.InterfaceHandle) builder.Node {
	return builder.Node{
		ID: id, Kind: builder.NodeKindDevice,
		Device: &builder.Device{Hostname: hostname, Spec: spec, Interfaces: handles},
	}
}

// runOne makes one check on doc against env.
func runOne(t *testing.T, doc *builder.Document, check PreflightCheck, experiment string, env PreflightEnvironment) PreflightResult {
	t.Helper()

	report := RunPreflight(t.Context(), doc, PreflightRequest{
		Checks: []PreflightCheck{check}, Experiment: experiment, Timeout: 0,
	}, env)

	if len(report.Checks) != 1 || report.Checks[0].Name != check {
		t.Fatalf("RunPreflight(%s) = %+v, want a report of the one check", check, report)
	}

	return report.Checks[0]
}

// issueCodes returns the code of each issue, in order.
func issueCodes(issues []builder.Issue) []builder.Code {
	codes := make([]builder.Code, len(issues))
	for i, issue := range issues {
		codes[i] = issue.Code
	}

	return codes
}

// resultDiff returns how two results differ, as cmp.Diff does, in all but
// their summaries and the text of their issues, which a test checks the parts
// of it needs on its own.
func resultDiff(want, got PreflightResult) string {
	return cmp.Diff(want, got, cmpopts.EquateEmpty(),
		cmpopts.IgnoreFields(PreflightResult{}, "Summary"), cmpopts.IgnoreFields(builder.Issue{}, "Message"))
}

// unavailableDiff returns how result differs, as cmp.Diff does, from a check
// that is unavailable for the reason summary gives, in its summary and in its
// one issue.
func unavailableDiff(result PreflightResult, summary string) string {
	want := PreflightResult{
		Name: result.Name, Status: PreflightUnavailable, Summary: summary,
		Issues: []builder.Issue{{Code: builder.CodePreflightUnavailable, Severity: builder.SeverityWarning}},
	}

	return cmp.Diff(want, result, cmpopts.IgnoreFields(builder.Issue{}, "Message"))
}

func capacityDocument() *builder.Document {
	doc := builder.NewDocument("capacity")
	doc.Nodes = []builder.Node{
		preflightNode("n-web", "web", map[string]any{"hardware": map[string]any{"vcpus": float64(2), "memory": float64(2048)}}),
		preflightNode("n-db", "db", map[string]any{}),
		preflightNode("n-ext", "ext", map[string]any{"external": true, "hardware": map[string]any{"vcpus": float64(64)}}),
		{ID: "n-note", Kind: builder.NodeKindNote, Note: &builder.Note{Text: "x"}},
	}

	return doc
}

// TestPreflightDefaultsMatchSchema checks that the vCPUs and memory that
// preflight gives a device without them are the defaults of the phenix node
// schema. The editor header uses the same defaults (resources.js), and its
// test checks them against the schema bundle.
func TestPreflightDefaultsMatchSchema(t *testing.T) {
	defs, err := builder.PhenixDefs()
	if err != nil {
		t.Fatalf("PhenixDefs() error = %v", err)
	}

	node, _ := defs[builder.PhenixDefPrefix+"minimega_node"].(map[string]any)
	properties, _ := node["properties"].(map[string]any)
	hardware, _ := properties["hardware"].(map[string]any)
	fields, _ := hardware["properties"].(map[string]any)

	for name, want := range map[string]int{"vcpus": preflightDefaultVCPUs, "memory": preflightDefaultMemory} {
		field, _ := fields[name].(map[string]any)

		got, ok := specWhole(field["default"])
		if !ok {
			t.Errorf("default of hardware.%s in the phenix schema = %v, want a positive whole number", name, field["default"])

			continue
		}

		if got != want {
			t.Errorf("default of hardware.%s in the phenix schema = %d, want preflight's %d", name, got, want)
		}
	}
}

// The capacity check counts every device but an external one, with phenix's
// defaults for what a spec leaves out, against the schedulable hosts.
func TestPreflightCapacityPasses(t *testing.T) {
	env := &fakePreflight{hosts: []PreflightHost{
		{Name: "h1", CPUs: 8, CPUCommit: 4, MemTotal: 16384, MemCommit: 8192},
		{Name: "h2", CPUs: 4, CPUCommit: 2, MemTotal: 8192, MemCommit: 4096},
	}}

	result := runOne(t, capacityDocument(), PreflightCapacity, "", env)

	want := PreflightResult{
		Name: PreflightCapacity, Status: PreflightPassed,
		Summary: "Needed: 3 vCPUs and 2560 MB of memory for 2 devices. Free: 6 vCPUs and 12288 MB on 2 schedulable hosts.",
	}

	if diff := cmp.Diff(want, result, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("RunPreflight(capacity) mismatch (-want +got):\n%s", diff)
	}
}

// A device larger than every host and too little memory fail the check. Too
// few free CPUs is a warning, since VMs share CPUs.
func TestPreflightCapacityFails(t *testing.T) {
	doc := capacityDocument()
	doc.Nodes = append(doc.Nodes, preflightNode("n-big", "big", map[string]any{
		"hardware": map[string]any{"vcpus": "16", "memory": "32768"},
	}))

	env := &fakePreflight{hosts: []PreflightHost{{Name: "h1", CPUs: 8, CPUCommit: 0, MemTotal: 16384, MemCommit: 0}}}

	result := runOne(t, doc, PreflightCapacity, "", env)

	want := PreflightResult{Name: PreflightCapacity, Status: PreflightFailed, Issues: []builder.Issue{
		{
			Code: builder.CodePreflightCapacityVMTooBig, Severity: builder.SeverityError,
			Path: "nodes[4].device.spec.hardware.vcpus", NodeID: "n-big", Field: "spec.hardware.vcpus",
		},
		{
			Code: builder.CodePreflightCapacityVMTooBig, Severity: builder.SeverityError,
			Path: "nodes[4].device.spec.hardware.memory", NodeID: "n-big", Field: "spec.hardware.memory",
		},
		{Code: builder.CodePreflightCapacityMemory, Severity: builder.SeverityError},
		{Code: builder.CodePreflightCapacityCPU, Severity: builder.SeverityWarning},
	}}

	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(capacity) mismatch (-want +got):\n%s", diff)
	}

	if len(result.Issues) > 1 && !strings.Contains(result.Issues[1].Message, "32768 MB") {
		t.Errorf("RunPreflight(capacity) memory issue message = %q, want it about big's 32768 MB", result.Issues[1].Message)
	}
}

// A device must fit on one host: 16 vCPUs and 8 GB fit neither 16 CPUs with
// 4 GB nor 4 CPUs with 16 GB.
func TestPreflightCapacityNeedsOneHostForBoth(t *testing.T) {
	doc := capacityDocument()
	doc.Nodes = append(doc.Nodes, preflightNode("n-big", "big", map[string]any{
		"hardware": map[string]any{"vcpus": float64(16), "memory": float64(8192)},
	}))

	env := &fakePreflight{hosts: []PreflightHost{
		{Name: "wide", CPUs: 16, CPUCommit: 0, MemTotal: 4096, MemCommit: 0},
		{Name: "deep", CPUs: 4, CPUCommit: 0, MemTotal: 16384, MemCommit: 0},
	}}

	result := runOne(t, doc, PreflightCapacity, "", env)

	want := PreflightResult{Name: PreflightCapacity, Status: PreflightFailed, Issues: []builder.Issue{{
		Code: builder.CodePreflightCapacityVMTooBig, Severity: builder.SeverityError,
		Path: "nodes[4].device.spec.hardware.vcpus", NodeID: "n-big", Field: "spec.hardware.vcpus",
	}}}

	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(capacity) mismatch (-want +got):\n%s", diff)
	}

	if len(result.Issues) > 0 && !strings.Contains(result.Issues[0].Message, "no single schedulable host") {
		t.Errorf("RunPreflight(capacity) issue message = %q, want big fitting no single host", result.Issues[0].Message)
	}

	env.hosts = append(env.hosts, PreflightHost{Name: "both", CPUs: 16, CPUCommit: 0, MemTotal: 8192, MemCommit: 0})

	result = runOne(t, doc, PreflightCapacity, "", env)

	want = PreflightResult{Name: PreflightCapacity, Status: PreflightPassed}
	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(capacity) with a host that holds big mismatch (-want +got):\n%s", diff)
	}
}

// The capacity check is unavailable when the role may not list the hosts or
// minimega cannot be reached, and says why.
func TestPreflightCapacityUnavailable(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		summary string
	}{
		{
			name:    "no permission",
			err:     NewPreflightUnavailable("your role may not list the cluster hosts"),
			summary: "Your role may not list the cluster hosts.",
		},
		{
			name:    "an error",
			err:     errors.New("no cluster hosts found"),
			summary: "The server cannot read the cluster hosts: no cluster hosts found.",
		},
		{name: "no hosts", err: nil, summary: "The server lists no schedulable cluster host."},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runOne(t, capacityDocument(), PreflightCapacity, "", &fakePreflight{hostsErr: test.err})
			if diff := unavailableDiff(result, test.summary); diff != "" {
				t.Errorf("RunPreflight(capacity) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// networkDocument has networks EXP (101) and MGMT (300). Device a uses both, on
// bridge lab, and the external device b is left out.
func networkDocument() *builder.Document {
	alias := func(n int) *int { return &n }

	doc := builder.NewDocument("network")
	doc.Networks = []builder.Network{
		{ID: "net-exp", Name: "EXP", Alias: alias(101)},
		{ID: "net-mgmt", Name: "MGMT", Alias: alias(300)},
	}
	doc.Nodes = []builder.Node{
		preflightNode("n-a", "a", map[string]any{"network": map[string]any{"interfaces": []any{
			map[string]any{"name": "eth0", "vlan": ""},
			map[string]any{"name": "eth1", "vlan": "MGMT", "bridge": "lab"},
		}}}, builder.InterfaceHandle{ID: "h-eth0", Name: "ETH0", Index: 0}),
		{ID: "n-sw", Kind: builder.NodeKindSwitch, Switch: &builder.Switch{NetworkID: "net-exp"}},
		preflightNode("n-b", "b", map[string]any{"external": true, "network": map[string]any{"interfaces": []any{
			map[string]any{"name": "eth0", "vlan": "OTHER", "bridge": "elsewhere"},
		}}}),
	}
	doc.Edges = []builder.Edge{{
		ID: "e1", SourceNodeID: "n-a", SourceHandleID: "h-eth0", TargetNodeID: "n-sw", NetworkID: "net-exp",
	}}

	return doc
}

// labBridgeIssue is the warning that bridge lab, on a's eth1, is missing.
func labBridgeIssue() builder.Issue {
	return builder.Issue{
		Code: builder.CodePreflightNetworkBridge, Severity: builder.SeverityWarning,
		Path: "nodes[0].device.spec.network.interfaces[1].bridge", NodeID: "n-a", Field: "spec.network.interfaces.1.bridge",
	}
}

// The network check compares VLANs, aliases and bridges with the range, the
// running experiments and the hosts. A missing bridge is a warning.
func TestPreflightNetworkFindsEveryProblem(t *testing.T) {
	env := &fakePreflight{
		experiment: PreflightExperiment{VLANMin: 100, VLANMax: 200, DefaultBridge: ""},
		vlans:      []PreflightVLAN{{ID: 101, Alias: "X", Experiment: "other"}, {ID: 102, Alias: "Y", Experiment: "third"}},
		bridges:    map[string][]string{"h1": {"phenix"}, "h2": {"lab", "phenix"}},
	}

	result := runOne(t, networkDocument(), PreflightNetwork, "exp", env)

	want := PreflightResult{Name: PreflightNetwork, Status: PreflightFailed, Issues: []builder.Issue{
		{
			Code: builder.CodePreflightNetworkVLANRange, Severity: builder.SeverityError,
			Path: "networks[1].alias", NetworkID: "net-mgmt",
		},
		{
			Code: builder.CodePreflightNetworkAliasInUse, Severity: builder.SeverityError,
			Path: "networks[0].alias", NetworkID: "net-exp",
		},
		labBridgeIssue(),
	}}

	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(network) mismatch (-want +got):\n%s", diff)
	}

	if len(result.Issues) == len(want.Issues) {
		if message := result.Issues[1].Message; !strings.Contains(message, `running experiment other uses for VLAN "X"`) {
			t.Errorf("RunPreflight(network) alias issue message = %q, want EXP's alias in use by other", message)
		}

		if message := result.Issues[2].Message; !strings.Contains(message, "host h1") {
			t.Errorf("RunPreflight(network) bridge issue message = %q, want lab missing on h1", message)
		}
	}

	if env.excepted != "exp" {
		t.Errorf("VLANsInUse left out %q, want exp", env.excepted)
	}

	if want := "The diagram's devices use 2 VLANs. " +
		"The VLAN range of experiment exp is 100 to 200"; !strings.HasPrefix(result.Summary, want) {
		t.Errorf("RunPreflight(network) summary = %q, want it to start with %q", result.Summary, want)
	}

	if strings.Contains(result.Summary, "may not list") {
		t.Errorf("RunPreflight(network) summary = %q, want no note of running experiments left out, since none was",
			result.Summary)
	}
}

// A running experiment the caller may not see is left out, and the summary
// says so without naming it.
func TestPreflightNetworkNotesHiddenExperiments(t *testing.T) {
	env := &fakePreflight{
		vlans:       []PreflightVLAN{{ID: 300, Alias: "M", Experiment: "seen"}},
		vlansHidden: true,
		bridges:     map[string][]string{"h1": {"lab", "phenix"}},
	}

	result := runOne(t, networkDocument(), PreflightNetwork, "", env)

	// Only MGMT's alias, which seen uses, fails the check.
	want := PreflightResult{Name: PreflightNetwork, Status: PreflightFailed, Issues: []builder.Issue{{
		Code: builder.CodePreflightNetworkAliasInUse, Severity: builder.SeverityError,
		Path: "networks[1].alias", NetworkID: "net-mgmt",
	}}}

	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(network) mismatch (-want +got):\n%s", diff)
	}

	note := "The check compared 2 VLAN aliases with running experiments. " +
		"The check did not compare running experiments that your role may not list"
	if !strings.Contains(result.Summary, note) {
		t.Errorf("RunPreflight(network) summary = %q, want it to contain %q", result.Summary, note)
	}

	env.vlans = nil

	result = runOne(t, networkDocument(), PreflightNetwork, "", env)

	want = PreflightResult{Name: PreflightNetwork, Status: PreflightPassed}
	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(network) with no VLAN in use mismatch (-want +got):\n%s", diff)
	}

	if !strings.Contains(result.Summary, note) {
		t.Errorf("RunPreflight(network) with no VLAN in use summary = %q, want it to contain %q", result.Summary, note)
	}
}

// A range smaller than the VLANs the devices use fails the check. Without
// an experiment no range applies, and the default bridge is phenix.
func TestPreflightNetworkRange(t *testing.T) {
	doc := networkDocument()

	env := &fakePreflight{
		experiment: PreflightExperiment{VLANMin: 101, VLANMax: 101, DefaultBridge: "mine"},
		bridges:    map[string][]string{"h1": {"lab", "phenix"}},
	}

	result := runOne(t, doc, PreflightNetwork, "exp", env)

	// The range is too small for 2 VLANs, and the experiment's default bridge
	// mine is missing.
	want := PreflightResult{Name: PreflightNetwork, Status: PreflightFailed, Issues: []builder.Issue{
		{Code: builder.CodePreflightNetworkVLANRange, Severity: builder.SeverityError},
		{
			Code: builder.CodePreflightNetworkVLANRange, Severity: builder.SeverityError,
			Path: "networks[1].alias", NetworkID: "net-mgmt",
		},
		{Code: builder.CodePreflightNetworkBridge, Severity: builder.SeverityWarning},
	}}

	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(network) mismatch (-want +got):\n%s", diff)
	}

	if len(result.Issues) == len(want.Issues) {
		if message := result.Issues[0].Message; !strings.Contains(message, "holds 1 VLAN") {
			t.Errorf("RunPreflight(network) range issue message = %q, want the range too small for 2 VLANs", message)
		}

		if message := result.Issues[2].Message; !strings.Contains(message, `"mine"`) {
			t.Errorf("RunPreflight(network) bridge issue message = %q, want the default bridge mine missing", message)
		}
	}

	result = runOne(t, doc, PreflightNetwork, "", env)

	want = PreflightResult{Name: PreflightNetwork, Status: PreflightPassed}
	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(network) without an experiment mismatch (-want +got):\n%s", diff)
	}

	if note := "No VLAN range applies, because the check has no experiment"; !strings.Contains(result.Summary, note) {
		t.Errorf("RunPreflight(network) without an experiment summary = %q, want it to contain %q", result.Summary, note)
	}
}

// A part of the network check that cannot be read leaves the check unavailable.
// The other parts are still checked.
func TestPreflightNetworkPartlyUnavailable(t *testing.T) {
	env := &fakePreflight{
		experimentErr: NewPreflightUnavailable("experiment exp does not exist, or your role may not read it"),
		vlansErr:      NewPreflightUnavailable("your role may not list experiments"),
		bridges:       map[string][]string{"h1": {"phenix"}},
	}

	result := runOne(t, networkDocument(), PreflightNetwork, "exp", env)

	// The bridge lab is still checked. The default bridge, unknown, is not.
	unavailable := builder.Issue{Code: builder.CodePreflightUnavailable, Severity: builder.SeverityWarning}
	want := PreflightResult{
		Name: PreflightNetwork, Status: PreflightUnavailable,
		Issues: []builder.Issue{labBridgeIssue(), unavailable, unavailable},
	}

	if diff := resultDiff(want, result); diff != "" {
		t.Errorf("RunPreflight(network) mismatch (-want +got):\n%s", diff)
	}

	if note := "Experiment exp does not exist"; !strings.Contains(result.Summary, note) {
		t.Errorf("RunPreflight(network) summary = %q, want it to contain %q", result.Summary, note)
	}

	env = &fakePreflight{bridgesErr: errors.New("unable to dial"), vlans: nil}

	result = runOne(t, networkDocument(), PreflightNetwork, "", env)

	if result.Status != PreflightUnavailable {
		t.Errorf("RunPreflight(network) when minimega fails status = %s, want %s", result.Status, PreflightUnavailable)
	}

	if note := "The server cannot read the bridges of the cluster hosts: unable to dial"; !strings.Contains(result.Summary, note) {
		t.Errorf("RunPreflight(network) when minimega fails summary = %q, want it to contain %q", result.Summary, note)
	}
}

func disksDocument() *builder.Document {
	drives := func(images ...string) []any {
		list := make([]any, len(images))
		for i, image := range images {
			list[i] = map[string]any{"image": image}
		}

		return list
	}

	hardware := func(images ...string) map[string]any { return map[string]any{"drives": drives(images...)} }
	container := map[string]any{"vm_type": "container"}

	doc := builder.NewDocument("disks")
	doc.Nodes = []builder.Node{
		preflightNode("n-vm1", "vm1", map[string]any{"hardware": hardware("/phenix/images/base.qc2", "tools.iso")}),
		preflightNode("n-vm2", "vm2", map[string]any{"hardware": hardware("missing.qc2")}),
		preflightNode("n-ct1", "ct1", map[string]any{"general": container, "hardware": hardware("base.qc2")}),
		preflightNode("n-ct2", "ct2", map[string]any{"general": container, "hardware": hardware("alpine_rootfs.tgz")}),
		preflightNode("n-vm3", "vm3", map[string]any{
			"general": map[string]any{"vm_type": "kvm"}, "hardware": hardware("", "alpine_rootfs.tgz"),
		}),
		preflightNode("n-ext", "ext", map[string]any{"external": true, "hardware": hardware("gone.qc2")}),
	}

	return doc
}

// The disks check looks each drive image up by file name and checks its kind
// against the device's. External devices are left out.
func TestPreflightDisks(t *testing.T) {
	env := &fakePreflight{images: []PreflightImage{
		{Name: "base.qc2", Kind: PreflightImageVM},
		{Name: "alpine_rootfs.tgz", Kind: PreflightImageContainer},
		{Name: "tools.iso", Kind: PreflightImageISO},
	}}

	result := runOne(t, disksDocument(), PreflightDisks, "", env)

	want := PreflightResult{
		Name: PreflightDisks, Status: PreflightFailed,
		Summary: "Found: 3 of 6 drive images, among the server's 3 disk images.",
		Issues: []builder.Issue{
			{
				Code: builder.CodePreflightDiskMissing, Severity: builder.SeverityError,
				Path: "nodes[1].device.spec.hardware.drives[0].image", NodeID: "n-vm2", Field: "spec.hardware.drives.0.image",
			},
			{
				Code: builder.CodePreflightDiskKind, Severity: builder.SeverityError,
				Path: "nodes[2].device.spec.hardware.drives[0].image", NodeID: "n-ct1", Field: "spec.hardware.drives.0.image",
			},
			{
				Code: builder.CodePreflightDiskKind, Severity: builder.SeverityError,
				Path: "nodes[4].device.spec.hardware.drives[1].image", NodeID: "n-vm3", Field: "spec.hardware.drives.1.image",
			},
		},
	}

	if diff := cmp.Diff(want, result, cmpopts.IgnoreFields(builder.Issue{}, "Message")); diff != "" {
		t.Errorf("RunPreflight(disks) mismatch (-want +got):\n%s", diff)
	}

	if len(result.Issues) == len(want.Issues) {
		if message := result.Issues[1].Message; !strings.Contains(message, "is a container") {
			t.Errorf("RunPreflight(disks) ct1 issue message = %q, want the container ct1 with a VM image", message)
		}

		if message := result.Issues[2].Message; !strings.Contains(message, "a container image") {
			t.Errorf("RunPreflight(disks) vm3 issue message = %q, want the kvm VM vm3 with a container image", message)
		}
	}
}

// The disks check is unavailable without the permission, when the images cannot
// be read, and when the server lists none.
func TestPreflightDisksUnavailable(t *testing.T) {
	for _, test := range []struct {
		name    string
		env     *fakePreflight
		summary string
	}{
		{
			name:    "no permission",
			env:     &fakePreflight{imagesErr: NewPreflightUnavailable("your role may not list the disk images")},
			summary: "Your role may not list the disk images.",
		},
		{
			name:    "no images",
			env:     &fakePreflight{images: []PreflightImage{}},
			summary: "The server listed no disk images, as it does when it cannot reach minimega.",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if diff := unavailableDiff(runOne(t, disksDocument(), PreflightDisks, "", test.env), test.summary); diff != "" {
				t.Errorf("RunPreflight(disks) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The apps check fails on a missing app or scenario. A scenario the role may not
// read leaves it unavailable.
func TestPreflightApps(t *testing.T) {
	doc := builder.NewDocument("apps")
	doc.Scenarios = []string{"s1", "s2", "s3", "hidden"}

	env := &fakePreflight{
		apps:         []string{"ntp", "serial", "startup", "vrouter", "scorch"},
		scenarios:    map[string][]string{"s1": {"scorch", "vrouter"}, "s2": {"nope"}},
		scenarioErrs: map[string]error{"hidden": NewPreflightUnavailable("your role may not read scenario hidden")},
	}

	result := runOne(t, doc, PreflightApps, "", env)

	want := PreflightResult{
		Name: PreflightApps, Status: PreflightFailed,
		Summary: "Found: 2 of 3 apps in 2 scenarios. The server could not read 2 scenarios.",
		Issues: []builder.Issue{
			{Code: builder.CodePreflightAppMissing, Severity: builder.SeverityError, Path: "scenarios[1]"},
			{Code: builder.CodePreflightScenarioMissing, Severity: builder.SeverityError, Path: "scenarios[2]"},
			{Code: builder.CodePreflightScenarioUnread, Severity: builder.SeverityWarning, Path: "scenarios[3]"},
		},
	}

	if diff := cmp.Diff(want, result, cmpopts.IgnoreFields(builder.Issue{}, "Message")); diff != "" {
		t.Errorf("RunPreflight(apps) mismatch (-want +got):\n%s", diff)
	}

	if len(result.Issues) == len(want.Issues) {
		if message := result.Issues[0].Message; !strings.Contains(message, `app "nope"`) {
			t.Errorf("RunPreflight(apps) app issue message = %q, want s2's app nope", message)
		}

		if message := result.Issues[1].Message; !strings.Contains(message, "does not exist") {
			t.Errorf("RunPreflight(apps) scenario issue message = %q, want s3 missing", message)
		}

		if message, want := result.Issues[2].Message, "your role may not read scenario hidden"; message != want {
			t.Errorf("RunPreflight(apps) unread scenario issue message = %q, want %q", message, want)
		}
	}

	doc.Scenarios = []string{"s1", "hidden"}

	if result := runOne(t, doc, PreflightApps, "", env); result.Status != PreflightUnavailable {
		t.Errorf("RunPreflight(apps) with only a scenario it may not read status = %s, want %s",
			result.Status, PreflightUnavailable)
	}
}

// Without scenarios no app is needed, and nothing is read. Without the
// permission to list apps the check is unavailable.
func TestPreflightAppsWithoutScenariosOrPermission(t *testing.T) {
	doc := builder.NewDocument("apps")
	env := &fakePreflight{appsErr: NewPreflightUnavailable("your role may not list the apps")}

	if result := runOne(t, doc, PreflightApps, "", env); result.Status != PreflightPassed {
		t.Errorf("RunPreflight(apps) without scenarios status = %s, want %s", result.Status, PreflightPassed)
	}

	if env.appsCalled {
		t.Error("RunPreflight(apps) without scenarios read the apps, want them not read")
	}

	doc.Scenarios = []string{"s1"}

	if diff := unavailableDiff(runOne(t, doc, PreflightApps, "", env), "Your role may not list the apps."); diff != "" {
		t.Errorf("RunPreflight(apps) without the permission mismatch (-want +got):\n%s", diff)
	}
}

// The report keeps the order asked. With no hosts the capacity check is
// unavailable.
func TestRunPreflightOrder(t *testing.T) {
	doc := disksDocument()
	env := &fakePreflight{images: []PreflightImage{{Name: "base.qc2", Kind: PreflightImageVM}}}

	report := RunPreflight(t.Context(), doc, PreflightRequest{
		Checks:     []PreflightCheck{PreflightApps, PreflightCapacity, PreflightDisks},
		Experiment: "",
		Timeout:    time.Minute,
	}, env)

	names := make([]PreflightCheck, len(report.Checks))
	for i, result := range report.Checks {
		names[i] = result.Name
	}

	if want := []PreflightCheck{PreflightApps, PreflightCapacity, PreflightDisks}; !slices.Equal(names, want) {
		t.Fatalf("RunPreflight checks = %v, want %v", names, want)
	}

	want := PreflightReport{
		Passed:      []PreflightCheck{PreflightApps},
		Failed:      []PreflightCheck{PreflightDisks},
		Unavailable: []PreflightCheck{PreflightCapacity},
	}

	if diff := cmp.Diff(want, report, cmpopts.IgnoreFields(PreflightReport{}, "Checks")); diff != "" {
		t.Errorf("RunPreflight mismatch (-want +got):\n%s", diff)
	}
}

// A check that times out is unavailable. Only the blocked check runs, so
// machine load does not matter.
func TestRunPreflightTimeout(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	report := RunPreflight(t.Context(), disksDocument(), PreflightRequest{
		Checks:     []PreflightCheck{PreflightCapacity},
		Experiment: "",
		Timeout:    20 * time.Millisecond,
	}, &fakePreflight{block: block})

	if len(report.Checks) != 1 {
		t.Fatalf("RunPreflight checks = %v, want the capacity check", report.Checks)
	}

	if diff := unavailableDiff(report.Checks[0], "The check did not finish within 20ms."); diff != "" {
		t.Errorf("RunPreflight capacity check that timed out mismatch (-want +got):\n%s", diff)
	}
}

func TestParsePreflightCheck(t *testing.T) {
	for _, check := range PreflightChecks() {
		if got, ok := ParsePreflightCheck(string(check)); !ok || got != check {
			t.Errorf("ParsePreflightCheck(%q) = %q, %t; want %q, true", check, got, ok, check)
		}
	}

	for _, name := range []string{"", "Capacity", "network ", "everything"} {
		if got, ok := ParsePreflightCheck(name); ok {
			t.Errorf("ParsePreflightCheck(%q) = %q, true; want false", name, got)
		}
	}
}
