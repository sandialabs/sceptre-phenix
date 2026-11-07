package v1

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	ifaces "phenix/types/interfaces"
)

// testOSType is the os_type of the test nodes.
const testOSType = "linux"

func TestNodeDelayed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		delay *Delay
		want  string
	}{
		{name: "no delay", want: ""},
		{name: "empty delay", delay: &Delay{}, want: ""},
		{name: "timer", delay: &Delay{TimerF: "5m", UserF: true}, want: "timer:5m"},
		{name: "user", delay: &Delay{UserF: true, C2F: []C2Delay{{HostnameF: "a"}}}, want: "user"},
		{name: "c2", delay: &Delay{C2F: []C2Delay{{HostnameF: "a"}, {HostnameF: "b", UseUUIDF: true}}}, want: "cc:a,b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			n := Node{DelayF: tt.delay}
			if got := n.Delayed(); got != tt.want {
				t.Errorf("Delayed() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDelayAccessors(t *testing.T) {
	t.Parallel()

	if got := (Node{}).Delay().Timer(); got != 0 {
		t.Errorf("Delay().Timer() of a node without a delay = %v, want 0", got)
	}

	d := Delay{TimerF: "90s", UserF: true, C2F: []C2Delay{{HostnameF: "c2", UseUUIDF: true}}}

	if got := d.Timer(); got != 90*time.Second {
		t.Errorf("Timer() = %v, want 90s", got)
	}

	if !d.User() {
		t.Error("User() = false, want true")
	}

	c2 := d.C2()
	if len(c2) != 1 || c2[0].Hostname() != "c2" || !c2[0].UseUUID() {
		t.Errorf("C2() = %+v, want one delay on c2 with UUID", c2)
	}

	if got := (Node{DelayF: &d}).Delay().Timer(); got != 90*time.Second {
		t.Errorf("Delay().Timer() = %v, want 90s", got)
	}

	n := Node{DelayF: &Delay{}}
	n.AddTimerDelay("1m")
	n.AddUserDelay(true)

	if n.DelayF.TimerF != "1m" || !n.DelayF.UserF {
		t.Errorf("delay after AddTimerDelay and AddUserDelay = %+v", n.DelayF)
	}

	n.AddC2Delay("host", true)

	if diff := cmp.Diff([]C2Delay{{HostnameF: "host", UseUUIDF: true}}, n.DelayF.C2F); diff != "" {
		t.Errorf("delay after AddC2Delay (-want +got):\n%s", diff)
	}
}

// The Add delay methods make the delay of a node that has none, and each keeps the delays set before it.
func TestNodeAddDelays(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		add  func(n *Node)
		want *Delay
	}{
		"timer on a node with no delay": {
			add:  func(n *Node) { n.AddTimerDelay("1m") },
			want: &Delay{TimerF: "1m"},
		},
		"user on a node with no delay": {
			add:  func(n *Node) { n.AddUserDelay(true) },
			want: &Delay{UserF: true},
		},
		"c2 on a node with no delay": {
			add:  func(n *Node) { n.AddC2Delay("host", false) },
			want: &Delay{C2F: []C2Delay{{HostnameF: "host"}}},
		},
		"c2 after a timer and a user delay": {
			add: func(n *Node) {
				n.AddTimerDelay("5m")
				n.AddUserDelay(true)
				n.AddC2Delay("host-a", true)
				n.AddC2Delay("host-b", false)
			},
			want: &Delay{TimerF: "5m", UserF: true, C2F: []C2Delay{{HostnameF: "host-a", UseUUIDF: true}, {HostnameF: "host-b"}}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var n Node
			tc.add(&n)

			if diff := cmp.Diff(tc.want, n.DelayF); diff != "" {
				t.Errorf("delay mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNodeAddInject(t *testing.T) {
	t.Parallel()

	n := &Node{}
	n.AddInject("a.txt", "/a", "0644", "first")
	n.AddInject("b.txt", "/b", "", "")
	n.AddInject("c.txt", "/a", "0600", "replaced")

	want := []*Injection{
		{SrcF: "c.txt", DstF: "/a", PermissionsF: "0600", DescriptionF: "replaced"},
		{SrcF: "b.txt", DstF: "/b"},
	}

	if diff := cmp.Diff(want, n.InjectionsF); diff != "" {
		t.Errorf("injections (-want +got):\n%s", diff)
	}

	inj := n.Injections()[0]
	if inj.Src() != "c.txt" || inj.Dst() != "/a" || inj.Permissions() != "0600" || inj.Description() != "replaced" {
		t.Errorf("Injections()[0] = %+v", inj)
	}

	disabled := &Node{LabelsF: map[string]string{"disable-injects": ""}}
	disabled.AddInject("a.txt", "/a", "", "")
	disabled.AddDeletion("/a", "")

	if len(disabled.InjectionsF) != 0 || len(disabled.DeletionsF) != 0 {
		t.Errorf(
			"node with disable-injects has %d injections and %d deletions, want 0",
			len(disabled.InjectionsF),
			len(disabled.DeletionsF),
		)
	}

	n.SetInjections([]ifaces.NodeInjection{&Injection{DstF: "/only"}})

	if diff := cmp.Diff([]*Injection{{DstF: "/only"}}, n.InjectionsF); diff != "" {
		t.Errorf("injections after SetInjections (-want +got):\n%s", diff)
	}
}

func TestNodeAddDeletion(t *testing.T) {
	t.Parallel()

	n := &Node{}
	n.AddDeletion("/tmp/a", "first")
	n.AddDeletion("/tmp/b", "")
	n.AddDeletion("/tmp/a", "replaced")

	want := []*Deletion{{PathF: "/tmp/a", DescriptionF: "replaced"}, {PathF: "/tmp/b"}}
	if diff := cmp.Diff(want, n.DeletionsF); diff != "" {
		t.Errorf("deletions (-want +got):\n%s", diff)
	}

	if got := n.FileDeletions(); got != `"/tmp/a","/tmp/b"` {
		t.Errorf("FileDeletions() = %q", got)
	}

	if d := n.Deletions()[0]; d.Path() != "/tmp/a" || d.Description() != "replaced" {
		t.Errorf("Deletions()[0] = %+v", d)
	}

	n.SetDeletions([]ifaces.NodeDeletion{&Deletion{PathF: "/only"}})

	if diff := cmp.Diff([]*Deletion{{PathF: "/only"}}, n.DeletionsF); diff != "" {
		t.Errorf("deletions after SetDeletions (-want +got):\n%s", diff)
	}
}

func TestNodeMutators(t *testing.T) {
	t.Parallel()

	n := &Node{GeneralF: &General{HostnameF: "host"}}

	n.SetType("VirtualMachine")
	n.AddLabel("role", "web")
	n.AddAnnotation("note", 1)
	n.AddAdvanced("cpu", "host")
	n.AddOverride("from", "to")
	n.AddCommand("echo hi")
	n.AddCommand("echo hi")
	n.AddCommand("echo bye")

	hw := n.AddHardware(testOSType, 2, 1024)
	hw.AddDrive("disk.qc2", 2)

	n.AddNetworkRoute("0.0.0.0/0", "10.0.0.254", 1)
	n.AddNetworkInterface("ethernet", "eth0", "EXP")
	n.AddNetworkNAT([]map[string][]string{{"eth1": {"eth0"}}})
	n.AddNetworkOSPF("1.1.1.1", 40, 10, 5, map[int][]string{0: {"10.0.0.0/24"}})

	if n.Type() != "VirtualMachine" || n.Labels()["role"] != "web" || n.Annotations()["note"] != 1 {
		t.Errorf("Type(), Labels(), Annotations() = %q, %v, %v", n.Type(), n.Labels(), n.Annotations())
	}

	if diff := cmp.Diff([]string{"echo hi", "echo bye"}, n.Commands()); diff != "" {
		t.Errorf("Commands() (-want +got):\n%s", diff)
	}

	if n.Advanced()["cpu"] != "host" || n.Overrides()["from"] != "to" {
		t.Errorf("Advanced(), Overrides() = %v, %v", n.Advanced(), n.Overrides())
	}

	part := 2
	cost := 1
	dead, hello, retrans, area := 40, 10, 5, 0

	wantHW := &Hardware{OSTypeF: testOSType, VCPUF: 2, MemoryF: 1024, DrivesF: []*Drive{{ImageF: "disk.qc2", InjectPartitionF: &part}}}
	if diff := cmp.Diff(wantHW, n.HardwareF); diff != "" {
		t.Errorf("hardware (-want +got):\n%s", diff)
	}

	wantNet := &Network{
		InterfacesF: []*Interface{{TypeF: "ethernet", NameF: "eth0", VLANF: "EXP"}},
		RoutesF:     []Route{{DestinationF: "0.0.0.0/0", NextF: "10.0.0.254", CostF: &cost}},
		NATF:        []NAT{{InF: []string{"eth0"}, OutF: "eth1"}},
		OSPFF: &OSPF{
			RouterIDF: "1.1.1.1", DeadIntervalF: &dead, HelloIntervalF: &hello, RetransmissionIntervalF: &retrans,
			AreasF: []Area{{AreaIDF: &area, AreaNetworksF: []AreaNetwork{{NetworkF: "10.0.0.0/24"}}}},
		},
	}
	if diff := cmp.Diff(wantNet, n.NetworkF); diff != "" {
		t.Errorf("network (-want +got):\n%s", diff)
	}

	n.SetLabels(map[string]string{"x": "y"})
	n.SetAnnotations(map[string]any{"a": "b"})
	n.SetAdvanced(map[string]string{"k": "v"})
	n.SetCommands([]string{"only"})

	if n.LabelsF["x"] != "y" || n.AnnotationsF["a"] != "b" || n.AdvancedF["k"] != "v" || len(n.CommandsF) != 1 {
		t.Errorf("node after Set* = %+v", n)
	}

	if got, ok := n.GetAnnotation("a"); !ok || got != "b" {
		t.Errorf("GetAnnotation(a) = %v, %t; want b, true", got, ok)
	}

	if _, ok := n.GetAnnotation("missing"); ok {
		t.Error("GetAnnotation(missing) found a value")
	}

	if _, ok := (Node{}).GetAnnotation("a"); ok {
		t.Error("GetAnnotation(a) on a node without annotations found a value")
	}

	if n.General().Hostname() != "host" || n.Hardware().OSType() != testOSType || len(n.Network().Interfaces()) != 1 {
		t.Error("General(), Hardware() or Network() do not return the node's parts")
	}
}

func TestCheckHostname(t *testing.T) {
	t.Parallel()

	if warning, err := CheckHostname("web-01", testOSType); warning != "" || err != nil {
		t.Errorf("CheckHostname(web-01, linux) = %q, %v; want no warning and no error", warning, err)
	}

	if _, err := CheckHostname("all", testOSType); err == nil {
		t.Error("CheckHostname(all, linux) error = nil, want an error")
	}
}

func TestGeneralAccessors(t *testing.T) {
	t.Parallel()

	var nilGeneral *General

	if nilGeneral.Hostname() != "" || nilGeneral.Description() != "" || nilGeneral.VMType() != "" ||
		nilGeneral.Snapshot() != nil || nilGeneral.DoNotBoot() != nil {
		t.Error("accessors of a nil *General return values, want zero values")
	}

	g := &General{HostnameF: "h", DescriptionF: "d", VMTypeF: "kvm"}

	if *g.Snapshot() || *g.DoNotBoot() {
		t.Error("Snapshot() or DoNotBoot() of an unset General is true, want false")
	}

	g.SetSnapshot(true)
	g.SetDoNotBoot(true)

	if g.Hostname() != "h" || g.Description() != "d" || g.VMType() != "kvm" || !*g.Snapshot() || !*g.DoNotBoot() {
		t.Errorf("General getters after setters = %+v", g)
	}
}

func TestHardwareAccessors(t *testing.T) {
	t.Parallel()

	var nilHW *Hardware

	if nilHW.CPU() != "" || nilHW.VCPU() != 0 || nilHW.Memory() != 0 || nilHW.OSType() != "" || nilHW.Drives() != nil {
		t.Error("accessors of a nil *Hardware return values, want zero values")
	}

	h := &Hardware{CPUF: "Broadwell", OSTypeF: testOSType, DrivesF: []*Drive{{ImageF: "a.qc2", IfaceF: "virtio", CacheModeF: "none"}}}
	h.SetVCPU(4)
	h.SetMemory(2048)

	if h.CPU() != "Broadwell" || h.VCPU() != 4 || h.Memory() != 2048 || h.OSType() != testOSType {
		t.Errorf("Hardware getters = %+v", h)
	}

	d := h.Drives()[0]
	if d.Image() != "a.qc2" || d.Interface() != "virtio" || d.CacheMode() != "none" || *d.InjectPartition() != 1 {
		t.Errorf("Drives()[0] = %+v", d)
	}
}

func TestDriveInjectPartition(t *testing.T) {
	t.Parallel()

	var d Drive

	if got := d.GetInjectPartition(); got != 1 {
		t.Errorf("GetInjectPartition() of an unset drive = %d, want 1", got)
	}

	p := 3
	d.SetInjectPartition(&p)
	d.SetImage("b.qc2")

	if d.GetInjectPartition() != 3 || *d.InjectPartition() != 3 || d.ImageF != "b.qc2" {
		t.Errorf("drive after setters = %+v", d)
	}
}

func TestHardwareDiskConfig(t *testing.T) {
	t.Parallel()

	h := Hardware{DrivesF: []*Drive{
		{ImageF: "base.qc2", IfaceF: "virtio"},
		{ImageF: "data.qc2", CacheModeF: "none"},
		{ImageF: "extra.qc2"},
	}}

	tests := []struct {
		snapshot string
		want     string
	}{
		{snapshot: "", want: "base.qc2,virtio data.qc2,none extra.qc2"},
		{snapshot: "snap.qc2", want: "snap.qc2,virtio,writeback data.qc2,none extra.qc2,writeback"},
	}

	for _, tt := range tests {
		if got := h.DiskConfig(tt.snapshot); got != tt.want {
			t.Errorf("DiskConfig(%q) = %q, want %q", tt.snapshot, got, tt.want)
		}
	}

	if got := (Hardware{}).DiskConfig("snap"); got != "" {
		t.Errorf("DiskConfig() with no drives = %q, want empty", got)
	}
}
