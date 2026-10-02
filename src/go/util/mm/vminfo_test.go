package mm

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
)

var vmInfoHeader = []string{ //nolint:gochecknoglobals // test fixture
	"uuid", "name", "state", "uptime", "vlan", "tap", "ip", "memory", "vcpus",
	"disks", "snapshot", "cdrom", "tags",
}

// fakeVM is one row of the fake `vm info` table.
type fakeVM struct {
	host, name, uptime, disk string
	snapshot                 bool
}

func (v fakeVM) row() []string {
	snapshot := "false"
	if v.snapshot {
		snapshot = "true"
	}

	return []string{
		"uuid-" + v.name, v.name, "RUNNING", v.uptime, "[EXP (101)]", "[mega_tap1]",
		"[10.0.0.1]", "2048", "2", v.disk + ",virtio,writeback", snapshot, "", `{"a":"b"}`,
	}
}

// vmInfoCluster answers the commands GetVMInfo sends for the given VMs.
type vmInfoCluster struct {
	mu  sync.Mutex
	vms []fakeVM
}

func (c *vmInfoCluster) set(vms ...fakeVM) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.vms = vms
}

func (c *vmInfoCluster) reply(cmd mmtest.Command) []*minicli.Response {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case cmd.Base == "host":
		return []*minicli.Response{mmtest.Tabular("head", []string{"name", "cpus"}, []string{"head", "8"})}
	case cmd.Base == ccClientCmd:
		return []*minicli.Response{mmtest.Tabular("head", []string{"uuid"}, []string{"uuid-a"})}
	case cmd.Base == captureCmd:
		return []*minicli.Response{mmtest.Tabular("head", []string{"interface", "path"},
			[]string{"a:0", "/a0.pcap"},
			[]string{"b:1", "/b1.pcap"},
			[]string{"a:1", "/a1.pcap"},
		)}
	case cmd.Base == vmInfoCmd:
		byHost := make(map[string][][]string)

		for _, vm := range c.vms {
			byHost[vm.host] = append(byHost[vm.host], vm.row())
		}

		resps := make([]*minicli.Response, 0, len(byHost))

		for host, rows := range byHost {
			resps = append(resps, mmtest.Tabular(host, vmInfoHeader, rows...))
		}

		return resps
	case strings.Contains(cmd.Base, "disk info "):
		disk := cmd.Base[strings.Index(cmd.Base, "disk info ")+len("disk info "):]

		return []*minicli.Response{
			mmtest.Tabular("any", []string{"image", "backingfile"}, []string{disk, "/base/" + disk}),
		}
	}

	return nil
}

func TestGetVMInfoCommandsAndDiskCache(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	cluster := new(vmInfoCluster)
	cluster.set(
		fakeVM{host: "compute1", name: "a", uptime: "1h0m0s", disk: "snap-a", snapshot: true},
		fakeVM{host: "head", name: "b", uptime: "1h0m0s", disk: "snap-b", snapshot: true},
		fakeVM{host: "compute1", name: "c", uptime: "1h0m0s", disk: "plain-c", snapshot: false},
	)

	received := useFakeMinimega(t, cluster.reply)

	vms := Minimega{}.GetVMInfo(NS("exp"))
	vms.SortByName(true)

	if len(vms) != 3 {
		t.Fatalf("got %d VMs, want 3", len(vms))
	}

	wantCaptures := map[string][]Capture{
		"a": {{VM: "a", Interface: 0, Filepath: "/a0.pcap"}, {VM: "a", Interface: 1, Filepath: "/a1.pcap"}},
		"b": {{VM: "b", Interface: 1, Filepath: "/b1.pcap"}},
		"c": nil,
	}

	wantDisks := map[string]string{"a": "/base/snap-a", "b": "/base/snap-b", "c": "plain-c"}

	for _, vm := range vms {
		if !reflect.DeepEqual(vm.Captures, wantCaptures[vm.Name]) {
			t.Errorf("VM %s captures = %#v, want %#v", vm.Name, vm.Captures, wantCaptures[vm.Name])
		}

		if vm.Disk != wantDisks[vm.Name] {
			t.Errorf("VM %s disk = %q, want %q", vm.Name, vm.Disk, wantDisks[vm.Name])
		}

		if vm.CCActive != (vm.Name == "a") {
			t.Errorf("VM %s ccActive = %v", vm.Name, vm.CCActive)
		}

		if !reflect.DeepEqual(vm.IPv4, []string{"10.0.0.1"}) || vm.Uptime != 3600 || vm.RAM != 2048 {
			t.Errorf("VM %s parsed wrong: %#v", vm.Name, vm)
		}
	}

	cmds := received()

	for prefix, want := range map[string]int{
		"host":                         1, // headnode lookup, then cached
		ccClientCmd:                    1,
		vmInfoCmd:                      1,
		captureCmd:                     1, // once for the namespace, not per VM
		"disk info":                    1, // headnode VM
		"mesh send compute1 disk info": 1,
	} {
		if got := mmtest.Count(cmds, prefix); got != want {
			t.Errorf("sent %d %q commands, want %d (all: %v)", got, prefix, want, cmds)
		}
	}

	// Listing again reuses the snapshot disk details and headnode.
	_ = Minimega{}.GetVMInfo(NS("exp"))

	cmds = received()
	if got := len(cmds); got != 9 {
		t.Errorf("second listing sent %d more commands, want 3 (all: %v)", got-6, cmds)
	}

	// A relaunched VM (a much younger uptime) is asked about again.
	cluster.set(
		fakeVM{host: "compute1", name: "a", uptime: "5s", disk: "snap-a", snapshot: true},
		fakeVM{host: "head", name: "b", uptime: "1h0m0s", disk: "snap-b", snapshot: true},
	)

	_ = Minimega{}.GetVMInfo(NS("exp"))

	if got := mmtest.Count(received(), "mesh send compute1 disk info"); got != 2 {
		t.Errorf("relaunched VM's disk was asked about %d times, want 2", got)
	}

	// Killing VMs forgets everything known about the namespace.
	_ = Minimega{}.KillVM(NS("exp"), VMName("b"))
	_ = Minimega{}.GetVMInfo(NS("exp"))

	if got := mmtest.Count(received(), "disk info"); got != 2 {
		t.Errorf("headnode VM's disk was asked about %d times after a kill, want 2", got)
	}
}

// A running VM's disk is the image its snapshot is backed by, named by its
// cleaned absolute path, however minimega stored the backing file.
func TestGetVMInfoNamesTheImageBehindASnapshot(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	cluster := new(vmInfoCluster)
	cluster.set(
		fakeVM{host: "head", name: "nested", uptime: "1m0s", disk: "/f/h_e_nested_snapshot", snapshot: true},
		fakeVM{host: "head", name: "outside", uptime: "1m0s", disk: "/f/h_e_outside_snapshot", snapshot: true},
		fakeVM{host: "head", name: "absolute", uptime: "1m0s", disk: "/f/h_e_absolute_snapshot", snapshot: true},
		fakeVM{host: "head", name: "plain", uptime: "1m0s", disk: "/f/./win/../plain.qc2", snapshot: false},
	)

	backing := map[string]string{
		"/f/h_e_nested_snapshot":   "win/win10.qcow2",
		"/f/h_e_outside_snapshot":  "../data/o.qc2",
		"/f/h_e_absolute_snapshot": "/data/./vms/a.qc2",
	}

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if disk, ok := strings.CutPrefix(cmd.Base, "disk info "); ok {
			return []*minicli.Response{
				mmtest.Tabular("head", []string{"image", "backingfile"}, []string{disk, backing[disk]}),
			}
		}

		return cluster.reply(cmd)
	})

	want := map[string]string{
		"nested":   "/f/win/win10.qcow2",
		"outside":  "/data/o.qc2",
		"absolute": "/data/vms/a.qc2",
		"plain":    "/f/plain.qc2",
	}

	vms := Minimega{}.GetVMInfo(NS("exp"))
	if len(vms) != len(want) {
		t.Fatalf("got %d VMs, want %d", len(vms), len(want))
	}

	for _, vm := range vms {
		if vm.Disk != want[vm.Name] {
			t.Errorf("VM %s disk = %q, want %q", vm.Name, vm.Disk, want[vm.Name])
		}
	}
}

func TestGetVMInfoMergesConcurrentCalls(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	const callers = 4

	var (
		cluster = new(vmInfoCluster)
		release = make(chan struct{})
	)

	cluster.set(fakeVM{host: "head", name: "a", uptime: "1m0s", disk: "plain", snapshot: false})

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "hold" {
			<-release
		}

		return cluster.reply(cmd)
	})

	var (
		wg      sync.WaitGroup
		results = make([]VMs, callers)
		held    = make(chan error, 1)
	)

	// Free the shared connection even if the test fails before it would, so
	// every caller finishes.
	unhold := sync.OnceFunc(func() { close(release) })

	t.Cleanup(func() {
		unhold()
		wg.Wait()
	})

	// Hold the shared connection so the callers below queue up behind it.
	go func() {
		cmd := mmcli.NewCommand()
		cmd.Command = "hold"

		held <- mmcli.ErrorResponse(mmcli.Run(cmd))
	}()

	mmtest.Await(t, "hold")

	for i := range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			results[i] = Minimega{}.GetVMInfo(NS("exp"))
		}()
	}

	waitForJoined(t, callers-1)

	unhold()
	wg.Wait()

	if err := <-held; err != nil {
		t.Fatalf("holding command failed: %v", err)
	}

	if got := mmtest.Count(received(), vmInfoCmd); got != 1 {
		t.Fatalf("sent %d vm info commands for %d concurrent callers, want 1", got, callers)
	}

	for i, vms := range results {
		if len(vms) != 1 || vms[0].Name != "a" {
			t.Fatalf("caller %d got %#v", i, vms)
		}
	}

	// Each caller has its own copy.
	results[0][0].IPv4[0] = "changed"

	if results[1][0].IPv4[0] != "10.0.0.1" {
		t.Fatal("callers share a result's slices")
	}
}

func TestHeadnodeIsLookedUpOncePerTTL(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	var failing atomic.Bool

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "host" && failing.Load() {
			return []*minicli.Response{{Host: "head", Error: "boom"}}
		}

		if cmd.Base == "host" {
			return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
		}

		return nil
	})

	if got := (Minimega{}).Headnode(); got != "head" {
		t.Fatalf("Headnode() = %q, want %q", got, "head")
	}

	age := func(d time.Duration) {
		headnode.mu.Lock()
		headnode.at = headnode.at.Add(-d)
		headnode.mu.Unlock()
	}

	// Still fresh: reused.
	age(headnodeTTL / 2)

	for range 2 {
		if got := (Minimega{}).Headnode(); got != "head" {
			t.Fatalf("Headnode() = %q, want %q", got, "head")
		}
	}

	if got := mmtest.Count(received(), "host"); got != 1 {
		t.Fatalf("sent %d host commands within the TTL, want 1", got)
	}

	// Expired: looked up again, and a failed lookup keeps the last known name.
	age(headnodeTTL)

	failing.Store(true)

	if got := (Minimega{}).Headnode(); got != "head" {
		t.Fatalf("Headnode() after a failed lookup = %q, want the last known %q", got, "head")
	}

	if got := mmtest.Count(received(), "host"); got != 2 {
		t.Fatalf("sent %d host commands after the TTL, want 2", got)
	}
}

func TestNarrowVMQueries(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base != vmInfoCmd {
			return nil
		}

		if strings.Contains(cmd.Raw, `.columns "ip"`) {
			if strings.Contains(cmd.Raw, "name=missing") {
				return nil
			}

			return []*minicli.Response{mmtest.Tabular("compute1", []string{"ip"}, []string{"[10.0.0.1, ]"})}
		}

		return []*minicli.Response{
			mmtest.Tabular("compute1", []string{"name"}, []string{"a"}),
			mmtest.Tabular("compute2", []string{"name"}, []string{"b"}),
		}
	})

	hosts := Minimega{}.GetVMHosts(NS("exp"))
	if want := map[string]string{"a": "compute1", "b": "compute2"}; !reflect.DeepEqual(hosts, want) {
		t.Fatalf("GetVMHosts = %v, want %v", hosts, want)
	}

	ips, err := Minimega{}.GetVMIPv4(NS("exp"), VMName("a"))
	if err != nil || !reflect.DeepEqual(ips, []string{"10.0.0.1", ""}) {
		t.Fatalf("GetVMIPv4 = %#v, %v", ips, err)
	}

	if _, err := (Minimega{}).GetVMIPv4(NS("exp"), VMName("missing")); err == nil {
		t.Fatal("GetVMIPv4 found a missing VM")
	}

	for _, cmd := range received() {
		if !strings.Contains(cmd.Raw, ".columns") {
			t.Fatalf("sent a vm info without columns: %q", cmd.Raw)
		}
	}
}

func TestCloneVMsKeepsNilAndCopies(t *testing.T) {
	t.Parallel()

	if cloneVMs(nil) != nil {
		t.Fatal("cloneVMs(nil) is not nil")
	}

	vms := VMs{{Name: "a", IPv4: []string{"1.1.1.1"}, Tags: map[string]string{"k": "v"}}, {Name: "b"}}
	clone := cloneVMs(vms)

	before, _ := json.Marshal(vms)
	after, _ := json.Marshal(clone)

	if string(before) != string(after) {
		t.Fatalf("clone marshals differently:\n%s\n%s", before, after)
	}

	clone[0].IPv4[0] = "changed"
	clone[0].Tags["k"] = "changed"

	if vms[0].IPv4[0] != "1.1.1.1" || vms[0].Tags["k"] != "v" {
		t.Fatal("clone shares slices or maps with the original")
	}
}

func TestParseList(t *testing.T) {
	t.Parallel()

	for in, want := range map[string][]string{
		"":          nil,
		"[]":        nil,
		"[a]":       {"a"},
		"[a, b]":    {"a", "b"},
		"[a, , c]":  {"a", "", "c"},
		"[EXP (1)]": {"EXP (1)"},
	} {
		if got := parseList(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseList(%q) = %#v, want %#v", in, got, want)
		}
	}
}

func TestVMLaunchSameAs(t *testing.T) {
	t.Parallel()

	now := time.Now()
	base := vmLaunch{ns: "exp", name: "a", uuid: "u", host: "h", disk: "d", at: now}

	later := base
	later.at = now.Add(launchTolerance / 2)

	relaunched := base
	relaunched.at = now.Add(2 * launchTolerance)

	moved := base
	moved.host = "other"

	unknown := base
	unknown.at = time.Time{}

	for name, tc := range map[string]struct {
		other vmLaunch
		want  bool
	}{
		"same":       {base, true},
		"jitter":     {later, true},
		"relaunched": {relaunched, false},
		"moved":      {moved, false},
		"unknown":    {unknown, false},
	} {
		if got := base.sameAs(tc.other); got != tc.want {
			t.Errorf("%s: sameAs = %v, want %v", name, got, tc.want)
		}
	}
}

func TestDiskUsageCommandAndParse(t *testing.T) {
	t.Parallel()

	cmd := diskUsageCommand("/phenix", "/tmp/minimega")
	want := `bash -c "echo 0=$(df /phenix | awk '{print $(NF-1)}' | tail -1) ` +
		`1=$(df /tmp/minimega | awk '{print $(NF-1)}' | tail -1)"`

	if cmd != want {
		t.Fatalf("diskUsageCommand =\n%s\nwant\n%s", cmd, want)
	}

	for resp, want := range map[string]map[int]float64{
		"0=42% 1=17%": {0: 42, 1: 17},
		"0= 1=17%":    {1: 17},
		"0=Use% 1=5%": {1: 5},
		"":            {},
		"0=1% 7=2%":   {0: 1},
	} {
		if got := parseDiskUsage(resp, 2); !reflect.DeepEqual(got, want) {
			t.Errorf("parseDiskUsage(%q) = %v, want %v", resp, got, want)
		}
	}
}
