package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/encoding/protojson"
	gproto "google.golang.org/protobuf/proto"

	"phenix/store"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	"phenix/web/proto"
	"phenix/web/rbac"
)

// routeVMs are the VMs minimega runs in one namespace, all on host.
type routeVMs struct {
	count    int
	host     string
	snapshot bool
}

// routeCluster is a fake minimega cluster: a headnode, compute hosts, and the
// VMs running in each namespace. It answers what the VM listing, state of
// health and cluster host routes ask, holding a "hold" command until unhold
// is called.
type routeCluster struct {
	head    string
	compute []string
	vms     map[string]routeVMs // by namespace; not changed once in use
	release chan struct{}
	unhold  func() // set by useRouteCluster
}

var routeVMHeader = []string{ //nolint:gochecknoglobals // test fixture
	"uuid", "name", "state", "uptime", "vlan", "tap", "ip", "memory", "vcpus",
	"disks", "snapshot", "cdrom", "tags",
}

func routeVMName(i int) string { return fmt.Sprintf("vm-%02d", i) }

// routeVMDisk is the disk the VM boots from, its snapshot if it has one.
func routeVMDisk(name string, snapshot bool) string {
	if snapshot {
		return "/tmp/minimega/" + name + "_snapshot"
	}

	return "/images/" + name + ".qc2"
}

func (c *routeCluster) reply(cmd mmtest.Command) []*minicli.Response {
	base := cmd.Base

	switch {
	case base == "hold":
		<-c.release
	case base == "vm info":
		vms := c.vms[cmd.Namespace]
		rows := make([][]string, 0, vms.count)

		for i := range vms.count {
			name := routeVMName(i)
			rows = append(rows, []string{
				"uuid-" + cmd.Namespace + "-" + name, name, "RUNNING", "1h0m0s", "[EXP (101)]", "[mega_tap1]",
				"[10.0.0.1]", "512", "1", routeVMDisk(name, vms.snapshot) + ",virtio,writeback",
				strconv.FormatBool(vms.snapshot), "", "{}",
			})
		}

		return []*minicli.Response{mmtest.Tabular(vms.host, routeVMHeader, rows...)}
	case base == "vm info summary":
		vms := c.vms[cmd.Namespace]
		rows := make([][]string, 0, vms.count)

		for i := range vms.count {
			rows = append(rows, []string{routeVMName(i), "RUNNING"})
		}

		return []*minicli.Response{mmtest.Tabular(vms.host, []string{"name", "state"}, rows...)}
	case strings.HasPrefix(base, "vm screenshot "):
		png := base64.StdEncoding.EncodeToString([]byte("\x89PNG"))

		return []*minicli.Response{{Host: c.head, Data: png}}
	case strings.Contains(base, "disk info "):
		disk := base[strings.Index(base, "disk info ")+len("disk info "):]

		return []*minicli.Response{
			mmtest.Tabular(c.head, []string{"image", "backingfile"}, []string{disk, "/images/base.qc2"}),
		}
	case strings.Contains(base, "shell "):
		return []*minicli.Response{mmtest.Text(c.head, "0=1% 1=2%\n")}
	case base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular(c.head, []string{"name", "cpus"}, []string{c.head, "8"})}
	case base == "host" && cmd.Namespace == "__phenix__":
		resps := make([]*minicli.Response, 0, len(c.compute))

		for _, name := range c.compute {
			resps = append(resps, mmtest.Tabular(name, []string{"name", "cpus"}, []string{name, "16"}))
		}

		return resps
	case base == "mesh list peers" || base == "ns hosts":
		return []*minicli.Response{mmtest.Text(c.head, strings.Join(c.compute, ","))}
	}

	return nil
}

// useRouteCluster points the real mm package at the fake minimega cluster,
// with nothing remembered about the cluster from earlier tests, and returns
// the commands received since.
func useRouteCluster(t *testing.T, c *routeCluster) func() []mmtest.Command {
	t.Helper()

	c.release = make(chan struct{})
	c.unhold = sync.OnceFunc(func() { close(c.release) })
	t.Cleanup(c.unhold)

	return useMinimega(t, c.reply)
}

// useMinimega points the real mm package at a fake minimega answering with handle, with no
// cluster state left from earlier tests, and returns the commands received since.
func useMinimega(t *testing.T, handle mmtest.Handler) func() []mmtest.Command {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = new(mm.Minimega) //nolint:reassign // the real one, against the fake minimega

	received := mmtest.Use(t, handle)

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	return received
}

// routeRuns numbers the experiments of the route tests, so a run of a test
// does not reuse what an earlier run left cached by experiment name.
var routeRuns atomic.Int64 //nolint:gochecknoglobals // unique across tests

// routeExperiment is an experiment, named after prefix and running if running
// is set, whose topology has count VMs named as routeCluster names them.
func routeExperiment(t *testing.T, prefix string, count int, running bool) store.Config {
	t.Helper()

	nodes := make([]map[string]any, 0, count)
	for i := range count {
		nodes = append(nodes, diskVM(routeVMName(i), "/images/"+routeVMName(i)+".qc2"))
	}

	started := ""
	if running {
		started = "2024-01-01T00:00:00Z"
	}

	return namedExperiment(t, fmt.Sprintf("%s-%d", prefix, routeRuns.Add(1)), started, nodes...)
}

// routeViewerRole may list and get every experiment, VM and host.
func routeViewerRole() rbac.Role {
	return combinedRole(
		testRole("experiments", "list", "*"),
		testRole("experiments", "get", "*"),
		testRole("vms", "list", "*", "*/*"),
		testRole("hosts", "list", "*"),
	)
}

// commandKinds counts the commands by kind: the base without the VM, disk or
// shell command it names, after the namespace sent in, if any. Looking up
// minimega's files directory ("args") is left out, as it is asked once per
// process and these tests leave it unanswered.
func commandKinds(cmds []mmtest.Command) map[string]int {
	kinds := make(map[string]int)

	for _, cmd := range cmds {
		kind := cmd.Base

		switch {
		case kind == "args":
			continue
		case strings.HasPrefix(kind, "vm screenshot "):
			kind = "vm screenshot"
		case strings.Contains(kind, "disk info "):
			kind = "disk info"
		case strings.Contains(kind, "shell "):
			kind = "disk usage"
		}

		if cmd.Namespace != "" {
			kind = cmd.Namespace + ": " + kind
		}

		kinds[kind]++
	}

	return kinds
}

// commandCounter counts the minimega commands each request sends.
type commandCounter struct {
	received func() []mmtest.Command
	seen     int
}

// since returns the commands received since it was last called.
func (c *commandCounter) since() []mmtest.Command {
	all := c.received()
	cmds := all[c.seen:]
	c.seen = len(all)

	return cmds
}

// wantCommands fails the test unless the request sent the minimega commands
// of want, by kind (see commandKinds), saying how many more or fewer it sent.
func (c *commandCounter) wantCommands(t *testing.T, request string, want map[string]int) {
	t.Helper()

	cmds := c.since()
	got := commandKinds(cmds)

	var wantTotal, gotTotal int
	for _, n := range want {
		wantTotal += n
	}

	for _, n := range got {
		gotTotal += n
	}

	if gotTotal != wantTotal {
		t.Errorf("%s sent %d minimega commands, want %d (%+d): %q",
			request, gotTotal, wantTotal, gotTotal-wantTotal, mmtest.Bases(cmds))
	}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("%s minimega commands by kind mismatch (-want +got):\n%s", request, diff)
	}
}

// vmLister is a response listing VMs.
type vmLister interface {
	gproto.Message
	GetVms() []*proto.VM
}

// vmInfoRun is the commands of one GetVMInfo run in a namespace with VMs and
// no snapshot disks: whatever the paging, sorting or VM count.
func vmInfoRun(ns string) map[string]int {
	return map[string]int{ns + ": cc client": 1, ns + ": vm info": 1, ns + ": capture": 1}
}

// Listing an experiment's VMs sends one GetVMInfo run's commands, however many
// VMs it has and whatever the page, sort or search.
func TestVMListingCommandCount(t *testing.T) {
	for _, count := range []int{1, 25} {
		t.Run(fmt.Sprintf("%d VMs", count), func(t *testing.T) {
			exp := routeExperiment(t, "listing", count, true)
			name := exp.Metadata.Name

			useTestStore(t, exp)

			var (
				cluster  = &routeCluster{head: "head", vms: map[string]routeVMs{name: {count: count, host: "head"}}}
				counter  = commandCounter{received: useRouteCluster(t, cluster)}
				server   = serveAs(t, "alice", routeViewerRole()).URL
				vmsPath  = "/api/v1/experiments/" + name + "/vms"
				expPath  = "/api/v1/experiments/" + name
				onePage  = min(count, 10)
				listing  = vmInfoRun(name)
				noneSent = map[string]int{}
			)

			for _, tt := range []struct {
				path string
				msg  vmLister
				vms  int
				want map[string]int
			}{
				{path: vmsPath, msg: new(proto.VMList), vms: count, want: listing},
				{path: vmsPath + "?pageNum=1&perPage=10", msg: new(proto.VMList), vms: onePage, want: listing},
				{
					path: vmsPath + "?pageNum=1&perPage=10&sortCol=name&sortDir=desc",
					msg:  new(proto.VMList), vms: onePage, want: listing,
				},
				{path: vmsPath + "?sortCol=uptime&sortDir=asc", msg: new(proto.VMList), vms: count, want: listing},
				{path: expPath, msg: new(proto.Experiment), vms: count, want: listing},
				{
					path: expPath + "?filter=vm-00&pageNum=1&perPage=10&sortCol=host&sortDir=asc",
					msg:  new(proto.Experiment), vms: 1, want: listing,
				},
				{path: expPath + "?vms=false", msg: new(proto.Experiment), vms: 0, want: noneSent},
			} {
				request := "GET " + tt.path

				if code := getAPI(t, server+tt.path, tt.msg); code != http.StatusOK {
					t.Fatalf("%s = %d, want %d", request, code, http.StatusOK)
				}

				if got := len(tt.msg.GetVms()); got != tt.vms {
					t.Errorf("%s listed %d VMs, want %d", request, got, tt.vms)
				}

				counter.wantCommands(t, request, tt.want)
			}
		})
	}
}

// Screenshots are taken for the page's VMs alone, and reused while fresh.
func TestVMListingScreenshotCommandCount(t *testing.T) {
	const count, perPage = 25, 5

	exp := routeExperiment(t, "screenshots", count, true)
	name := exp.Metadata.Name

	useTestStore(t, exp)

	var (
		cluster = &routeCluster{head: "head", vms: map[string]routeVMs{name: {count: count, host: "head"}}}
		counter = commandCounter{received: useRouteCluster(t, cluster)}
		server  = serveAs(t, "alice", routeViewerRole()).URL
		path    = fmt.Sprintf("/api/v1/experiments/%s/vms?pageNum=2&perPage=%d&screenshot=200", name, perPage)
	)

	first := vmInfoRun(name)
	first[name+": vm screenshot"] = perPage

	for i, want := range []map[string]int{first, vmInfoRun(name)} {
		request := fmt.Sprintf("GET %s (request %d)", path, i+1)

		var list proto.VMList
		if code := getAPI(t, server+path, &list); code != http.StatusOK {
			t.Fatalf("%s = %d, want %d", request, code, http.StatusOK)
		}

		for _, vm := range list.GetVms() {
			if vm.GetScreenshot() == "" {
				t.Errorf("%s VM %s has no screenshot", request, vm.GetName())
			}
		}

		counter.wantCommands(t, request, want)
	}
}

// A VM's snapshot disk is looked up once per launch: listing the VMs again
// asks only for what changes.
func TestSnapshotVMListingCommandCount(t *testing.T) {
	const count = 5

	exp := routeExperiment(t, "snapshots", count, true)
	name := exp.Metadata.Name

	useTestStore(t, exp)

	var (
		cluster = &routeCluster{
			head: "head", compute: []string{"compute1"},
			vms: map[string]routeVMs{name: {count: count, host: "compute1", snapshot: true}},
		}
		counter = commandCounter{received: useRouteCluster(t, cluster)}
		server  = serveAs(t, "alice", routeViewerRole()).URL
		path    = "/api/v1/experiments/" + name + "/vms"
	)

	first := vmInfoRun(name)
	first["minimega: host"] = 1 // the headnode, to tell where each disk is
	first["disk info"] = count

	for i, want := range []map[string]int{first, vmInfoRun(name)} {
		request := fmt.Sprintf("GET %s (request %d)", path, i+1)

		var list proto.VMList
		if code := getAPI(t, server+path, &list); code != http.StatusOK {
			t.Fatalf("%s = %d, want %d", request, code, http.StatusOK)
		}

		for _, vm := range list.GetVms() {
			if vm.GetDisk() != "/images/base.qc2" {
				t.Errorf("%s VM %s disk = %q, want %q", request, vm.GetName(), vm.GetDisk(), "/images/base.qc2")
			}
		}

		counter.wantCommands(t, request, want)
	}
}

// Listing every VM sends one GetVMInfo run per running experiment, and none
// for a stopped one.
func TestAllVMsCommandCount(t *testing.T) {
	var (
		small   = routeExperiment(t, "all-small", 3, true)
		large   = routeExperiment(t, "all-large", 20, true)
		stopped = routeExperiment(t, "all-stopped", 4, false)
	)

	useTestStore(t, small, large, stopped)

	var (
		cluster = &routeCluster{head: "head", vms: map[string]routeVMs{
			small.Metadata.Name: {count: 3, host: "head"},
			large.Metadata.Name: {count: 20, host: "head"},
		}}
		counter = commandCounter{received: useRouteCluster(t, cluster)}
		server  = serveAs(t, "alice", routeViewerRole()).URL
	)

	var list proto.VMList
	if code := getAPI(t, server+"/api/v1/vms", &list); code != http.StatusOK {
		t.Fatalf("GET /api/v1/vms = %d, want %d", code, http.StatusOK)
	}

	if got := len(list.GetVms()); got != 23 {
		t.Errorf("GET /api/v1/vms listed %d VMs, want 23", got)
	}

	want := vmInfoRun(small.Metadata.Name)
	maps.Copy(want, vmInfoRun(large.Metadata.Name))

	counter.wantCommands(t, "GET /api/v1/vms", want)
}

// The state of health summary reads each running experiment's VM states with
// one narrow command, however many VMs it has, and asks nothing about a
// stopped one.
func TestSoHSummaryCommandCount(t *testing.T) {
	var (
		small   = routeExperiment(t, "soh-small", 3, true)
		large   = routeExperiment(t, "soh-large", 40, true)
		stopped = routeExperiment(t, "soh-stopped", 4, false)
	)

	useTestStore(t, small, large, stopped)

	var (
		cluster = &routeCluster{head: "head", vms: map[string]routeVMs{
			small.Metadata.Name: {count: 3, host: "head"},
			large.Metadata.Name: {count: 40, host: "head"},
		}}
		counter = commandCounter{received: useRouteCluster(t, cluster)}
		server  = serveAs(t, "alice", routeViewerRole()).URL
	)

	resp := do(t, http.MethodGet, server+"/api/v1/soh", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/soh = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	var summary struct {
		Experiments []struct {
			Name string `json:"name"`
		} `json:"experiments"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decoding GET /api/v1/soh: %v", err)
	}

	if got := len(summary.Experiments); got != 3 {
		t.Errorf("GET /api/v1/soh summarized %d experiments, want 3", got)
	}

	counter.wantCommands(t, "GET /api/v1/soh", map[string]int{
		small.Metadata.Name + ": vm info summary": 1,
		large.Metadata.Name + ": vm info summary": 1,
	})
}

// Listing the cluster's hosts reads the mesh once and each host's disk usage
// once, then reuses the list for a while.
func TestClusterHostsCommandCount(t *testing.T) {
	for _, compute := range [][]string{{"compute1"}, {"compute1", "compute2", "compute3"}} {
		t.Run(fmt.Sprintf("%d compute hosts", len(compute)), func(t *testing.T) {
			var (
				cluster = &routeCluster{head: "head", compute: compute}
				counter = commandCounter{received: useRouteCluster(t, cluster)}
				server  = serveAs(t, "alice", routeViewerRole()).URL
			)

			first := map[string]int{
				"minimega: host":       1,
				"mesh list peers":      1,
				"__phenix__: ns hosts": 1,
				"__phenix__: host":     1,
				"disk usage":           len(compute) + 1,
			}

			for i, want := range []map[string]int{first, {}} {
				request := fmt.Sprintf("GET /api/v1/hosts (request %d)", i+1)

				resp := do(t, http.MethodGet, server+"/api/v1/hosts", "")
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s = %d, want %d: %s", request, resp.StatusCode, http.StatusOK, readBody(t, resp))
				}

				var listed mm.Cluster
				if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
					t.Fatalf("decoding %s: %v", request, err)
				}

				if got := len(listed.Hosts); got != len(compute)+1 {
					t.Errorf("%s listed %d hosts, want %d", request, got, len(compute)+1)
				}

				counter.wantCommands(t, request, want)
			}
		})
	}
}

// Identical VM listings that arrive together share one GetVMInfo run.
func TestConcurrentVMListingsShareOneRun(t *testing.T) {
	const listings, count = 2, 10

	exp := routeExperiment(t, "concurrent", count, true)
	name := exp.Metadata.Name

	useTestStore(t, exp)

	var (
		cluster  = &routeCluster{head: "head", vms: map[string]routeVMs{name: {count: count, host: "head"}}}
		received = useRouteCluster(t, cluster)
		counter  = commandCounter{received: received}
		server   = serveAs(t, "alice", routeViewerRole()).URL
		url      = server + "/api/v1/experiments/" + name + "/vms"
		held     = make(chan error, 1)
		results  = make(chan error, listings)
	)

	// Hold the shared connection so the listings queue up behind it.
	go func() {
		cmd := mmcli.NewCommand()
		cmd.Command = "hold"

		held <- mmcli.ErrorResponse(mmcli.Run(cmd))
	}()

	mmtest.Await(t, "hold")

	for range listings {
		go func() { results <- getVMCount(t.Context(), url, count) }()
	}

	polltest.Until(t, "every listing waits for one run", func() bool { return mm.VMInfoWaiting(name) == listings })

	cluster.unhold()

	if err := <-held; err != nil {
		t.Errorf("holding command error = %v", err)
	}

	for range listings {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}

	want := vmInfoRun(name)
	want["hold"] = 1

	counter.wantCommands(t, fmt.Sprintf("%d concurrent GET %s", listings, url), want)
}

// getVMCount gets the VM list at url, reporting an error unless it lists want
// VMs. Unlike getAPI it may run outside the test's goroutine.
func getVMCount(ctx context.Context, url string, want int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("making GET %s: %w", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading GET %s: %w", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s = %d, want %d: %s", url, resp.StatusCode, http.StatusOK, body)
	}

	var list proto.VMList
	if err := protojson.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("GET %s = %s, not a VM list: %w", url, body, err)
	}

	if got := len(list.GetVms()); got != want {
		return fmt.Errorf("GET %s listed %d VMs, want %d", url, got, want)
	}

	return nil
}
