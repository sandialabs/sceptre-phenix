package builder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"phenix/types/builder"
)

// fakePreflight is a [PreflightEnvironment] whose sources each test sets. A
// source left nil answers with nothing; an error set for it is returned.
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

func (f *fakePreflight) VLANsInUse(_ context.Context, except string) ([]PreflightVLAN, error) {
	f.excepted = except

	return f.vlans, f.vlansErr
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
		t.Fatalf("report = %+v, want the one check %s", report, check)
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

// expectUnavailable fails the test unless result is unavailable for the
// reason given, in its summary and in its one issue.
func expectUnavailable(t *testing.T, result PreflightResult, summary string) {
	t.Helper()

	if result.Status != PreflightUnavailable || result.Summary != summary ||
		len(result.Issues) != 1 || result.Issues[0].Code != builder.CodePreflightUnavailable ||
		result.Issues[0].Severity != builder.SeverityWarning {
		t.Fatalf("result = %+v, want unavailable saying %q", result, summary)
	}
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

// The capacity check counts every device but an external one, with phenix's
// defaults for what a spec leaves out, against the schedulable hosts.
func TestPreflightCapacityPasses(t *testing.T) {
	env := &fakePreflight{hosts: []PreflightHost{
		{Name: "h1", CPUs: 8, CPUCommit: 4, MemTotal: 16384, MemCommit: 8192},
		{Name: "h2", CPUs: 4, CPUCommit: 2, MemTotal: 8192, MemCommit: 4096},
	}}

	result := runOne(t, capacityDocument(), PreflightCapacity, "", env)

	want := "Needed: 3 vCPUs and 2560 MB of memory for 2 devices. Free: 6 vCPUs and 12288 MB on 2 schedulable hosts."
	if result.Status != PreflightPassed || len(result.Issues) != 0 || result.Summary != want {
		t.Fatalf("result = %+v, want passed with summary %q", result, want)
	}
}

// A device larger than every host and too little memory fail the check; too
// few free CPUs is a warning, since VMs share CPUs.
func TestPreflightCapacityFails(t *testing.T) {
	doc := capacityDocument()
	doc.Nodes = append(doc.Nodes, preflightNode("n-big", "big", map[string]any{
		"hardware": map[string]any{"vcpus": "16", "memory": "32768"},
	}))

	env := &fakePreflight{hosts: []PreflightHost{{Name: "h1", CPUs: 8, CPUCommit: 0, MemTotal: 16384, MemCommit: 0}}}

	result := runOne(t, doc, PreflightCapacity, "", env)

	want := []builder.Code{
		builder.CodePreflightCapacityVMTooBig, builder.CodePreflightCapacityVMTooBig,
		builder.CodePreflightCapacityMemory, builder.CodePreflightCapacityCPU,
	}

	if got := issueCodes(result.Issues); result.Status != PreflightFailed || !slices.Equal(got, want) {
		t.Fatalf("result = %+v, want failed with %v", result, want)
	}

	if issue := result.Issues[0]; issue.NodeID != "n-big" || issue.Field != "spec.hardware.vcpus" ||
		issue.Path != "nodes[4].device.spec.hardware.vcpus" || issue.Severity != builder.SeverityError {
		t.Fatalf("vCPU issue = %+v, want it located at big's vcpus", issue)
	}

	if issue := result.Issues[1]; issue.Field != "spec.hardware.memory" ||
		!strings.Contains(issue.Message, "32768 MB") {
		t.Fatalf("memory issue = %+v, want it about big's 32768 MB", issue)
	}

	if issue := result.Issues[3]; issue.Severity != builder.SeverityWarning {
		t.Fatalf("CPU issue = %+v, want a warning", issue)
	}
}

// A device must fit on one host: 16 vCPUs and 8 GB fit neither a host with
// 16 CPUs and 4 GB nor one with 4 CPUs and 16 GB, though each of its numbers
// fits one of them. A device that fits one host passes.
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

	if got := issueCodes(result.Issues); result.Status != PreflightFailed ||
		!slices.Equal(got, []builder.Code{builder.CodePreflightCapacityVMTooBig}) {
		t.Fatalf("result = %+v, want failed with one %s", result, builder.CodePreflightCapacityVMTooBig)
	}

	if issue := result.Issues[0]; issue.NodeID != "n-big" || issue.Field != "spec.hardware.vcpus" ||
		issue.Severity != builder.SeverityError || !strings.Contains(issue.Message, "no single schedulable host") {
		t.Fatalf("issue = %+v, want big fitting no single host", issue)
	}

	env.hosts = append(env.hosts, PreflightHost{Name: "both", CPUs: 16, CPUCommit: 0, MemTotal: 8192, MemCommit: 0})

	if result := runOne(t, doc, PreflightCapacity, "", env); result.Status != PreflightPassed || len(result.Issues) != 0 {
		t.Fatalf("result = %+v, want passed once a host holds big", result)
	}
}

// The capacity check is unavailable when the role may not list the hosts or
// minimega cannot be reached, and says why.
func TestPreflightCapacityUnavailable(t *testing.T) {
	for _, test := range []struct {
		err     error
		summary string
	}{
		{
			err:     NewPreflightUnavailable("your role may not list the cluster hosts"),
			summary: "Your role may not list the cluster hosts.",
		},
		{
			err:     errors.New("no cluster hosts found"),
			summary: "The cluster hosts cannot be read: no cluster hosts found.",
		},
		{err: nil, summary: "No schedulable cluster host is listed."},
	} {
		result := runOne(t, capacityDocument(), PreflightCapacity, "", &fakePreflight{hostsErr: test.err})
		expectUnavailable(t, result, test.summary)
	}
}

// networkDocument has networks EXP (alias 101) and MGMT (alias 300); device
// a has eth0 connected to EXP and eth1 on VLAN MGMT of bridge lab; the
// external device b is left out.
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

// The network check compares the VLANs the devices use (a connected
// interface on its network) and the aliases with the experiment's range,
// the aliases with running experiments' VLANs, and the bridges with the
// hosts'. A missing bridge is a warning, located at the interface naming it.
func TestPreflightNetworkFindsEveryProblem(t *testing.T) {
	env := &fakePreflight{
		experiment: PreflightExperiment{VLANMin: 100, VLANMax: 200, DefaultBridge: ""},
		vlans:      []PreflightVLAN{{ID: 101, Alias: "X", Experiment: "other"}, {ID: 102, Alias: "Y", Experiment: ""}},
		bridges:    map[string][]string{"h1": {"phenix"}, "h2": {"lab", "phenix"}},
	}

	result := runOne(t, networkDocument(), PreflightNetwork, "exp", env)

	want := []builder.Code{
		builder.CodePreflightNetworkVLANRange, builder.CodePreflightNetworkAliasInUse, builder.CodePreflightNetworkBridge,
	}

	if got := issueCodes(result.Issues); result.Status != PreflightFailed || !slices.Equal(got, want) {
		t.Fatalf("result = %+v, want failed with %v", result, want)
	}

	if issue := result.Issues[0]; issue.NetworkID != "net-mgmt" || issue.Path != "networks[1].alias" {
		t.Fatalf("range issue = %+v, want it on MGMT's alias", issue)
	}

	if issue := result.Issues[1]; issue.NetworkID != "net-exp" ||
		!strings.Contains(issue.Message, `running experiment other uses for VLAN "X"`) {
		t.Fatalf("alias issue = %+v, want EXP's alias in use by other", issue)
	}

	if issue := result.Issues[2]; issue.Severity != builder.SeverityWarning || issue.NodeID != "n-a" ||
		issue.Field != "spec.network.interfaces.1.bridge" || !strings.Contains(issue.Message, "host h1") {
		t.Fatalf("bridge issue = %+v, want a warning about lab on h1 at a's eth1", issue)
	}

	if env.excepted != "exp" {
		t.Fatalf("VLANsInUse was asked to leave out %q, want exp", env.excepted)
	}

	if want := "2 VLANs; experiment exp's VLAN range is 100 to 200"; !strings.HasPrefix(result.Summary, want) {
		t.Fatalf("summary = %q, want it to start with %q", result.Summary, want)
	}
}

// A range smaller than the VLANs the devices use fails the check; without
// an experiment no range applies, and the default bridge is phenix.
func TestPreflightNetworkRange(t *testing.T) {
	doc := networkDocument()

	env := &fakePreflight{
		experiment: PreflightExperiment{VLANMin: 101, VLANMax: 101, DefaultBridge: "mine"},
		bridges:    map[string][]string{"h1": {"lab", "phenix"}},
	}

	result := runOne(t, doc, PreflightNetwork, "exp", env)

	if result.Status != PreflightFailed || len(result.Issues) < 2 ||
		result.Issues[0].Code != builder.CodePreflightNetworkVLANRange || result.Issues[0].Path != "" ||
		!strings.Contains(result.Issues[0].Message, "holds 1 VLAN") {
		t.Fatalf("result = %+v, want the range too small for 2 VLANs", result)
	}

	last := result.Issues[len(result.Issues)-1]
	if last.Code != builder.CodePreflightNetworkBridge || last.NodeID != "" || !strings.Contains(last.Message, `"mine"`) {
		t.Fatalf("last issue = %+v, want the experiment's default bridge mine missing", last)
	}

	result = runOne(t, doc, PreflightNetwork, "", env)

	if result.Status != PreflightPassed || len(result.Issues) != 0 ||
		!strings.Contains(result.Summary, "no experiment is named, so no VLAN range applies") {
		t.Fatalf("result = %+v, want passed without a range", result)
	}
}

// A part of the network check that cannot be read leaves the check
// unavailable, says so in the summary and an issue, and the other parts are
// still checked.
func TestPreflightNetworkPartlyUnavailable(t *testing.T) {
	env := &fakePreflight{
		experimentErr: NewPreflightUnavailable("experiment exp does not exist, or your role may not read it"),
		vlansErr:      NewPreflightUnavailable("your role may not list experiments"),
		bridges:       map[string][]string{"h1": {"phenix"}},
	}

	result := runOne(t, networkDocument(), PreflightNetwork, "exp", env)

	if result.Status != PreflightUnavailable || !strings.Contains(result.Summary, "Not checked: experiment exp does not exist") {
		t.Fatalf("result = %+v, want unavailable naming the experiment", result)
	}

	// The bridge lab is still checked; the default bridge, unknown, is not.
	want := []builder.Code{builder.CodePreflightNetworkBridge, builder.CodePreflightUnavailable, builder.CodePreflightUnavailable}
	if got := issueCodes(result.Issues); !slices.Equal(got, want) {
		t.Fatalf("issues = %v, want %v", got, want)
	}

	env = &fakePreflight{bridgesErr: errors.New("unable to dial"), vlans: nil}

	result = runOne(t, networkDocument(), PreflightNetwork, "", env)

	if result.Status != PreflightUnavailable ||
		!strings.Contains(result.Summary, "the bridges of the cluster hosts cannot be read: unable to dial") {
		t.Fatalf("result = %+v, want unavailable naming minimega's failure", result)
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
// against the device's; external devices are left out.
func TestPreflightDisks(t *testing.T) {
	env := &fakePreflight{images: []PreflightImage{
		{Name: "base.qc2", Kind: PreflightImageVM},
		{Name: "alpine_rootfs.tgz", Kind: PreflightImageContainer},
		{Name: "tools.iso", Kind: PreflightImageISO},
	}}

	result := runOne(t, disksDocument(), PreflightDisks, "", env)

	want := []builder.Code{builder.CodePreflightDiskMissing, builder.CodePreflightDiskKind, builder.CodePreflightDiskKind}
	if got := issueCodes(result.Issues); result.Status != PreflightFailed || !slices.Equal(got, want) {
		t.Fatalf("result = %+v, want failed with %v", result, want)
	}

	if issue := result.Issues[0]; issue.NodeID != "n-vm2" || issue.Field != "spec.hardware.drives.0.image" ||
		issue.Path != "nodes[1].device.spec.hardware.drives[0].image" {
		t.Fatalf("missing issue = %+v, want it at vm2's first drive", issue)
	}

	if issue := result.Issues[1]; issue.NodeID != "n-ct1" || !strings.Contains(issue.Message, "is a container") {
		t.Fatalf("kind issue = %+v, want the container ct1 with a VM image", issue)
	}

	if issue := result.Issues[2]; issue.NodeID != "n-vm3" || issue.Field != "spec.hardware.drives.1.image" ||
		!strings.Contains(issue.Message, "a container image") {
		t.Fatalf("kind issue = %+v, want the kvm VM vm3 with a container image", issue)
	}

	if want := "Found: 3 of 6 drive images, among the server's 3 disk images."; result.Summary != want {
		t.Fatalf("summary = %q, want %q", result.Summary, want)
	}
}

// The disks check is unavailable without the permission, when the images
// cannot be read, and when the server lists none, as it does without
// minimega.
func TestPreflightDisksUnavailable(t *testing.T) {
	for _, test := range []struct {
		env     *fakePreflight
		summary string
	}{
		{
			env:     &fakePreflight{imagesErr: NewPreflightUnavailable("your role may not list the disk images")},
			summary: "Your role may not list the disk images.",
		},
		{
			env:     &fakePreflight{images: []PreflightImage{}},
			summary: "The server listed no disk images, as it does when minimega cannot be reached.",
		},
	} {
		expectUnavailable(t, runOne(t, disksDocument(), PreflightDisks, "", test.env), test.summary)
	}
}

// The apps check reads each listed scenario: an app the server lacks or a
// scenario that does not exist fails it; a scenario the role may not read is
// a warning that leaves it unavailable.
func TestPreflightApps(t *testing.T) {
	doc := builder.NewDocument("apps")
	doc.Scenarios = []string{"s1", "s2", "s3", "hidden"}

	env := &fakePreflight{
		apps:         []string{"ntp", "serial", "startup", "vrouter", "scorch"},
		scenarios:    map[string][]string{"s1": {"scorch", "vrouter"}, "s2": {"nope"}},
		scenarioErrs: map[string]error{"hidden": NewPreflightUnavailable("your role may not read scenario hidden")},
	}

	result := runOne(t, doc, PreflightApps, "", env)

	want := []builder.Code{
		builder.CodePreflightAppMissing, builder.CodePreflightScenarioMissing, builder.CodePreflightScenarioUnread,
	}

	if got := issueCodes(result.Issues); result.Status != PreflightFailed || !slices.Equal(got, want) {
		t.Fatalf("result = %+v, want failed with %v", result, want)
	}

	if issue := result.Issues[0]; issue.Path != "scenarios[1]" || !strings.Contains(issue.Message, `app "nope"`) {
		t.Fatalf("app issue = %+v, want s2's app nope", issue)
	}

	if issue := result.Issues[1]; issue.Severity != builder.SeverityError || !strings.Contains(issue.Message, "does not exist") {
		t.Fatalf("scenario issue = %+v, want s3 missing as an error", issue)
	}

	if issue := result.Issues[2]; issue.Severity != builder.SeverityWarning ||
		issue.Message != "your role may not read scenario hidden" {
		t.Fatalf("scenario issue = %+v, want hidden unreadable as a warning", issue)
	}

	if want := "Found: 2 of 3 apps, named by 2 scenarios. Not read: 2 scenarios."; result.Summary != want {
		t.Fatalf("summary = %q, want %q", result.Summary, want)
	}

	doc.Scenarios = []string{"s1", "hidden"}

	if result := runOne(t, doc, PreflightApps, "", env); result.Status != PreflightUnavailable {
		t.Fatalf("result = %+v, want unavailable with only a scenario it may not read", result)
	}
}

// Without scenarios no app is needed, and nothing is read; without the
// permission to list apps the check is unavailable.
func TestPreflightAppsWithoutScenariosOrPermission(t *testing.T) {
	doc := builder.NewDocument("apps")
	env := &fakePreflight{appsErr: NewPreflightUnavailable("your role may not list the apps")}

	if result := runOne(t, doc, PreflightApps, "", env); result.Status != PreflightPassed || env.appsCalled {
		t.Fatalf("result = %+v (apps read: %t), want passed without reading the apps", result, env.appsCalled)
	}

	doc.Scenarios = []string{"s1"}

	expectUnavailable(t, runOne(t, doc, PreflightApps, "", env), "Your role may not list the apps.")
}

// The report lists the checks in the order asked, and the names of those
// that passed, failed and were unavailable; a check that takes too long is
// unavailable, and the others are reported.
func TestRunPreflightOrderAndTimeout(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	doc := disksDocument()
	env := &fakePreflight{block: block, images: []PreflightImage{{Name: "base.qc2", Kind: PreflightImageVM}}}

	report := RunPreflight(t.Context(), doc, PreflightRequest{
		Checks:     []PreflightCheck{PreflightApps, PreflightCapacity, PreflightDisks},
		Experiment: "",
		Timeout:    20 * time.Millisecond,
	}, env)

	names := make([]PreflightCheck, len(report.Checks))
	for i, result := range report.Checks {
		names[i] = result.Name
	}

	if want := []PreflightCheck{PreflightApps, PreflightCapacity, PreflightDisks}; !slices.Equal(names, want) {
		t.Fatalf("checks = %v, want %v", names, want)
	}

	if !slices.Equal(report.Passed, []PreflightCheck{PreflightApps}) ||
		!slices.Equal(report.Failed, []PreflightCheck{PreflightDisks}) ||
		!slices.Equal(report.Unavailable, []PreflightCheck{PreflightCapacity}) {
		t.Fatalf("report = %+v, want apps passed, disks failed, capacity unavailable", report)
	}

	expectUnavailable(t, report.Checks[1], "The check did not finish within 20ms.")
}

func TestParsePreflightCheck(t *testing.T) {
	for _, check := range PreflightChecks() {
		if got, ok := ParsePreflightCheck(string(check)); !ok || got != check {
			t.Errorf("ParsePreflightCheck(%q) = %q, %t", check, got, ok)
		}
	}

	for _, name := range []string{"", "Capacity", "network ", "everything"} {
		if _, ok := ParsePreflightCheck(name); ok {
			t.Errorf("ParsePreflightCheck(%q) is a check", name)
		}
	}
}
