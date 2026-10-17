package mm

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
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

func vmNames(vms VMs) []string {
	names := make([]string, 0, len(vms))

	for _, vm := range vms {
		names = append(names, vm.Name)
	}

	return names
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

	// The fields GetVMInfo fills from the fake's answers, by VM.
	type listed struct {
		Captures []Capture
		Disk     string
		CCActive bool
		IPv4     []string
		Uptime   float64
		RAM      int
	}

	ip := []string{"10.0.0.1"}
	want := map[string]listed{
		"a": {
			Captures: []Capture{{VM: "a", Interface: 0, Filepath: "/a0.pcap"}, {VM: "a", Interface: 1, Filepath: "/a1.pcap"}},
			Disk:     "/base/snap-a", CCActive: true, IPv4: ip, Uptime: 3600, RAM: 2048,
		},
		"b": {
			Captures: []Capture{{VM: "b", Interface: 1, Filepath: "/b1.pcap"}},
			Disk:     "/base/snap-b", CCActive: false, IPv4: ip, Uptime: 3600, RAM: 2048,
		},
		"c": {Captures: nil, Disk: "plain-c", CCActive: false, IPv4: ip, Uptime: 3600, RAM: 2048},
	}

	got := make(map[string]listed, len(vms))
	for _, vm := range vms {
		got[vm.Name] = listed{
			Captures: vm.Captures, Disk: vm.Disk, CCActive: vm.CCActive, IPv4: vm.IPv4, Uptime: vm.Uptime, RAM: vm.RAM,
		}
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetVMInfo(exp) mismatch (-want +got):\n%s", diff)
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
			t.Errorf("GetVMInfo(exp) sent %d %q commands, want %d (all: %v)", got, prefix, want, cmds)
		}
	}

	if len(cmds) != 6 {
		t.Errorf("GetVMInfo(exp) sent %d commands, want 6 (all: %v)", len(cmds), cmds)
	}

	// Listing again reuses the snapshot disk details and headnode.
	_ = Minimega{}.GetVMInfo(NS("exp"))

	if more := len(received()) - len(cmds); more != 3 {
		t.Errorf("GetVMInfo(exp) again sent %d more commands, want 3 (all: %v)", more, received())
	}

	// A relaunched VM (a much younger uptime) is asked about again.
	cluster.set(
		fakeVM{host: "compute1", name: "a", uptime: "5s", disk: "snap-a", snapshot: true},
		fakeVM{host: "head", name: "b", uptime: "1h0m0s", disk: "snap-b", snapshot: true},
	)

	_ = Minimega{}.GetVMInfo(NS("exp"))

	if got := mmtest.Count(received(), "mesh send compute1 disk info"); got != 2 {
		t.Errorf("GetVMInfo(exp) after a relaunch asked about compute1's disk %d times in all, want 2", got)
	}

	// Killing VMs forgets everything known about the namespace.
	_ = Minimega{}.KillVM(NS("exp"), VMName("b"))
	_ = Minimega{}.GetVMInfo(NS("exp"))

	if got := mmtest.Count(received(), "disk info"); got != 2 {
		t.Errorf("GetVMInfo(exp) after KillVM(b) asked about the headnode VM's disk %d times in all, want 2", got)
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

	got := make(map[string]string)
	for _, vm := range (Minimega{}).GetVMInfo(NS("exp")) {
		got[vm.Name] = vm.Disk
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetVMInfo(exp) disks by VM mismatch (-want +got):\n%s", diff)
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

	polltest.Until(t, "every caller waits for the run", func() bool { return vmInfoFlights.Waiting("exp\x00") == callers })

	unhold()
	wg.Wait()

	if err := <-held; err != nil {
		t.Errorf("holding command error = %v", err)
	}

	if got := mmtest.Count(received(), vmInfoCmd); got != 1 {
		t.Errorf("%d concurrent GetVMInfo(exp) calls sent %d vm info commands, want 1", callers, got)
	}

	for _, vms := range results {
		if diff := cmp.Diff([]string{"a"}, vmNames(vms)); diff != "" {
			t.Fatalf("concurrent GetVMInfo(exp) VM names mismatch (-want +got):\n%s", diff)
		}
	}

	// Each caller has its own copy.
	results[0][0].IPv4[0] = "changed"

	if got := results[1][0].IPv4[0]; got != "10.0.0.1" {
		t.Errorf("another caller's IPv4[0] after one changed its result = %q, want %q", got, "10.0.0.1")
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
		t.Errorf("Headnode() = %q, want %q", got, "head")
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
			t.Errorf("Headnode() within the TTL = %q, want %q", got, "head")
		}
	}

	if got := mmtest.Count(received(), "host"); got != 1 {
		t.Errorf("Headnode() within the TTL sent %d host commands in all, want 1", got)
	}

	// Expired: looked up again, and a failed lookup keeps the last known name.
	age(headnodeTTL)

	failing.Store(true)

	if got := (Minimega{}).Headnode(); got != "head" {
		t.Errorf("Headnode() after a failed lookup = %q, want the last known %q", got, "head")
	}

	if got := mmtest.Count(received(), "host"); got != 2 {
		t.Errorf("Headnode() after the TTL sent %d host commands in all, want 2", got)
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

	want := map[string]string{"a": "compute1", "b": "compute2"}
	if diff := cmp.Diff(want, Minimega{}.GetVMHosts(NS("exp"))); diff != "" {
		t.Errorf("GetVMHosts(exp) mismatch (-want +got):\n%s", diff)
	}

	ips, err := Minimega{}.GetVMIPv4(NS("exp"), VMName("a"))
	if err != nil {
		t.Errorf("GetVMIPv4(a) error = %v", err)
	}

	if diff := cmp.Diff([]string{"10.0.0.1", ""}, ips); diff != "" {
		t.Errorf("GetVMIPv4(a) mismatch (-want +got):\n%s", diff)
	}

	if ips, err := (Minimega{}).GetVMIPv4(NS("exp"), VMName("missing")); err == nil {
		t.Errorf("GetVMIPv4(missing) = %q, nil; want an error", ips)
	}

	for _, cmd := range received() {
		if !strings.Contains(cmd.Raw, ".columns") {
			t.Errorf("GetVMHosts or GetVMIPv4 sent %q, want a vm info with columns", cmd.Raw)
		}
	}
}

func TestCloneVMsKeepsNilAndCopies(t *testing.T) {
	t.Parallel()

	if got := cloneVMs(nil); got != nil {
		t.Errorf("cloneVMs(nil) = %#v, want nil", got)
	}

	vms := VMs{{Name: "a", IPv4: []string{"1.1.1.1"}, Tags: map[string]string{"k": "v"}}, {Name: "b"}}
	clone := cloneVMs(vms)

	if diff := cmp.Diff(vms, clone); diff != "" {
		t.Fatalf("cloneVMs(vms) mismatch (-want +got):\n%s", diff)
	}

	clone[0].IPv4[0] = "changed"
	clone[0].Tags["k"] = "changed"

	if vms[0].IPv4[0] != "1.1.1.1" || vms[0].Tags["k"] != "v" {
		t.Errorf("changing cloneVMs(vms) changed vms[0] to IPv4 %q, tags %v", vms[0].IPv4, vms[0].Tags)
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
		if diff := cmp.Diff(want, parseList(in)); diff != "" {
			t.Errorf("parseList(%q) mismatch (-want +got):\n%s", in, diff)
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
			t.Errorf("base.sameAs(%s launch %+v) = %v, want %v", name, tc.other, got, tc.want)
		}
	}
}

func TestDiskUsageCommandAndParse(t *testing.T) {
	t.Parallel()

	cmd := diskUsageCommand("/phenix", "/tmp/minimega")
	want := `bash -c "echo 0=$(df /phenix | awk '{print $(NF-1)}' | tail -1) ` +
		`1=$(df /tmp/minimega | awk '{print $(NF-1)}' | tail -1)"`

	if cmd != want {
		t.Errorf("diskUsageCommand(/phenix, /tmp/minimega) =\n%s\nwant\n%s", cmd, want)
	}

	for resp, want := range map[string]map[int]float64{
		"0=42% 1=17%": {0: 42, 1: 17},
		"0= 1=17%":    {1: 17},
		"0=Use% 1=5%": {1: 5},
		"":            {},
		"0=1% 7=2%":   {0: 1},
	} {
		if diff := cmp.Diff(want, parseDiskUsage(resp, 2)); diff != "" {
			t.Errorf("parseDiskUsage(%q, 2) mismatch (-want +got):\n%s", resp, diff)
		}
	}
}

// A `vm info` error is reported as an unknown observation, never as a
// successful empty list, with the rows of the nodes that did answer.
func TestGetVMInfoObservedReportsErrors(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	cluster := new(vmInfoCluster)
	cluster.set(fakeVM{host: "head", name: "b", uptime: "1h0m0s", disk: "plain-b", snapshot: false})

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		resps := cluster.reply(cmd)

		if cmd.Base == vmInfoCmd {
			resps = append(resps, &minicli.Response{Host: "compute1", Error: "compute1 is not answering"})
		}

		return resps
	})

	before := time.Now()

	vms, obs := Minimega{}.GetVMInfoObserved(NS("exp"))
	if obs.Known || obs.Err == nil || !strings.Contains(obs.Err.Error(), "not answering") {
		t.Errorf("GetVMInfoObserved(exp) observation = %+v, want unknown with minimega's error", obs)
	}

	if obs.At.Before(before) || obs.At.After(time.Now()) {
		t.Errorf("GetVMInfoObserved(exp) observed at %v, want during the call from %v", obs.At, before)
	}

	if diff := cmp.Diff([]string{"b"}, vmNames(vms)); diff != "" {
		t.Errorf("GetVMInfoObserved(exp) VM names mismatch (-want +got):\n%s", diff)
	}

	// GetVMInfo has no error to return; it logs it and answers what it read
	if diff := cmp.Diff([]string{"b"}, vmNames(Minimega{}.GetVMInfo(NS("exp")))); diff != "" {
		t.Errorf("GetVMInfo(exp) VM names mismatch (-want +got):\n%s", diff)
	}
}

// A successful read is known, and observed when its run started.
func TestGetVMInfoObservedWhenRead(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	cluster := new(vmInfoCluster)
	cluster.set(fakeVM{host: "head", name: "b", uptime: "1h0m0s", disk: "plain-b", snapshot: false})

	var (
		mu           sync.Mutex
		firstCommand time.Time
	)

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		mu.Lock()
		if firstCommand.IsZero() {
			firstCommand = time.Now()
		}
		mu.Unlock()

		return cluster.reply(cmd)
	})

	_, obs := Minimega{}.GetVMInfoObserved(NS("exp"))

	mu.Lock()
	defer mu.Unlock()

	if !obs.Known || obs.Err != nil || obs.At.IsZero() || obs.At.After(firstCommand) {
		t.Errorf("GetVMInfoObserved(exp) observation = %+v with the first command at %v, want known, at or before it", obs, firstCommand)
	}
}
