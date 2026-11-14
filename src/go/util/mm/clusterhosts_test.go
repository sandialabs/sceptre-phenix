package mm

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

// meshHostCmd is the command GetClusterHosts sends to read the compute nodes.
const meshHostCmd = "mesh send all host"

// meshDiskCmd is the prefix of the command that measures the disk usage of
// every mesh peer.
const meshDiskCmd = "mesh send all shell "

// headDiskCmd is the prefix of the command that measures the disk usage of
// the headnode.
const headDiskCmd = "shell "

// fakeMesh answers the commands GetClusterHosts sends for a headnode and its
// mesh peers.
type fakeMesh struct {
	head    string
	peers   string   // `mesh list peers`
	compute []string // the nodes that answer `mesh send all host` and `mesh send all shell`
	peerErr bool
	disk    map[string]string // disk usage output by host, "0=1% 1=2%" if not given
	diskErr map[string]bool   // hosts that answer the disk usage command with an error
}

// diskReply is host's answer to the disk usage command.
func (f fakeMesh) diskReply(host string) *minicli.Response {
	if f.diskErr[common.TrimHostnameSuffixes(host)] {
		return mmtest.Error(host, "df: timed out")
	}

	usage, ok := f.disk[common.TrimHostnameSuffixes(host)]
	if !ok {
		usage = "0=1% 1=2%\n"
	}

	return mmtest.Text(host, usage)
}

func (f fakeMesh) reply(cmd mmtest.Command) []*minicli.Response {
	header := []string{"name", "cpus"}

	switch {
	case cmd.Base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular(f.head, header, []string{f.head, "8"})}
	case cmd.Base == meshHostCmd && cmd.Namespace == "":
		resps := make([]*minicli.Response, 0, len(f.compute))

		for _, name := range f.compute {
			resps = append(resps, mmtest.Tabular(name, header, []string{name, "16"}))
		}

		return resps
	case cmd.Base == "mesh list peers":
		if f.peerErr {
			return []*minicli.Response{{Host: f.head, Error: "no such command"}}
		}

		return []*minicli.Response{mmtest.Text(f.head, f.peers)}
	case strings.HasPrefix(cmd.Base, meshDiskCmd):
		resps := make([]*minicli.Response, 0, len(f.compute))

		for _, name := range f.compute {
			resps = append(resps, f.diskReply(name))
		}

		return resps
	case strings.HasPrefix(cmd.Base, headDiskCmd):
		return []*minicli.Response{f.diskReply(f.head)}
	}

	return nil
}

// isDiskUsage reports whether cmd measures disk usage.
func isDiskUsage(cmd mmtest.Command) bool {
	return strings.HasPrefix(cmd.Base, headDiskCmd) || strings.HasPrefix(cmd.Base, meshDiskCmd)
}

// hostCommands lists the commands sent, leaving out disk usage measurements.
func hostCommands(cmds []mmtest.Command) []string {
	var out []string

	for _, cmd := range cmds {
		if isDiskUsage(cmd) {
			continue
		}

		if cmd.Namespace != "" {
			out = append(out, cmd.Namespace+": "+cmd.Base)
		} else {
			out = append(out, cmd.Base)
		}
	}

	return out
}

// noPhenixNamespace fails the test if a command used the `__phenix__`
// namespace that older phenix versions read the compute nodes from.
func noPhenixNamespace(t *testing.T, call string, cmds []mmtest.Command) {
	t.Helper()

	for _, cmd := range cmds {
		if cmd.Namespace == "__phenix__" || strings.Contains(cmd.Base, "__phenix__") ||
			strings.HasPrefix(cmd.Base, "clear namespace") {
			t.Errorf("%s sent %q in namespace %q, want no use of the __phenix__ namespace", call, cmd.Base, cmd.Namespace)
		}
	}
}

func hostNames(hosts Hosts) []string {
	names := make([]string, 0, len(hosts))

	for _, host := range hosts {
		names = append(names, host.Name)
	}

	return names
}

// hostRole is the part of a Host that says what the node is used for.
type hostRole struct {
	Name                  string
	Headnode, Schedulable bool
}

func hostRoles(hosts Hosts) []hostRole {
	roles := make([]hostRole, 0, len(hosts))

	for _, host := range hosts {
		roles = append(roles, hostRole{Name: host.Name, Headnode: host.Headnode, Schedulable: host.Schedulable})
	}

	return roles
}

func TestGetClusterHosts(t *testing.T) { //nolint:paralleltest // shares the fake minimega and common.HostnameSuffixes
	saved := common.HostnameSuffixes
	common.HostnameSuffixes = "-minimega,.example.com" //nolint:reassign // names minimega reports with a suffix

	t.Cleanup(func() { common.HostnameSuffixes = saved }) //nolint:reassign // restore

	for _, tc := range []struct {
		name      string
		mesh      fakeMesh
		wantCmds  []string
		wantRoles []hostRole
		wantDisk  map[string]*DiskUsage
	}{
		{
			name: "single node",
			// a lone node would answer a mesh send if it were sent, so the test can see one
			mesh:      fakeMesh{head: "node01", peers: "", compute: []string{"node01"}},
			wantCmds:  []string{"minimega: host", "mesh list peers"},
			wantRoles: []hostRole{{Name: "node01", Headnode: true, Schedulable: true}},
			wantDisk:  map[string]*DiskUsage{"node01": {Phenix: 1, Minimega: 2}},
		},
		{
			name: "several nodes",
			mesh: fakeMesh{
				head:    "head-minimega",
				peers:   "compute1-minimega,compute2.example.com",
				compute: []string{"compute1-minimega", "compute2.example.com"},
				disk: map[string]string{
					"compute1": "0=11% 1=12%\n",
					"compute2": "0=21% 1=22%\n",
					"head":     "0=1% 1=2%\n",
				},
			},
			wantCmds: []string{"minimega: host", "mesh list peers", meshHostCmd},
			wantRoles: []hostRole{
				{Name: "compute1", Headnode: false, Schedulable: true},
				{Name: "compute2", Headnode: false, Schedulable: true},
				{Name: "head", Headnode: true, Schedulable: false},
			},
			wantDisk: map[string]*DiskUsage{
				"compute1": {Phenix: 11, Minimega: 12},
				"compute2": {Phenix: 21, Minimega: 22},
				"head":     {Phenix: 1, Minimega: 2},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := useFakeMinimega(t, tc.mesh.reply)

			hosts, err := Minimega{}.GetClusterHosts(false, true)
			if err != nil {
				t.Fatalf("GetClusterHosts(false, true) error = %v", err)
			}

			cmds := received()

			// the headnode and the mesh peers are read at once
			if diff := cmp.Diff(tc.wantCmds, hostCommands(cmds), sortStrings); diff != "" {
				t.Errorf("GetClusterHosts(false, true) sent commands mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantRoles, hostRoles(hosts)); diff != "" {
				t.Errorf("GetClusterHosts(false, true) mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantDisk, diskByHost(hosts)); diff != "" {
				t.Errorf("GetClusterHosts(false, true) disk usage by host mismatch (-want +got):\n%s", diff)
			}

			if got, want := mmtest.Count(cmds, "host"), 1; got != want {
				t.Errorf("GetClusterHosts(false, true) sent %d host commands, want %d", got, want)
			}

			noPhenixNamespace(t, "GetClusterHosts(false, true)", cmds)

			if got, want := (Minimega{}).Headnode(), tc.wantRoles[len(tc.wantRoles)-1].Name; got != want {
				t.Errorf("Headnode() = %q, want %q", got, want)
			}

			if got := len(received()); got != len(cmds) {
				t.Errorf("Headnode() after GetClusterHosts sent %d commands, want 0", got-len(cmds))
			}
		})
	}
}

// Without the mesh peers, the compute nodes are unknown: the call fails and
// the failure is not kept for later callers.
func TestGetClusterHostsPeersUnreadable(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, mesh := range map[string]fakeMesh{
		"mesh list peers fails": {head: "head", peerErr: true, compute: []string{"compute1"}},
		"peers not a host list": {head: "head", peers: "compute[1-", compute: []string{"compute1"}},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, mesh.reply)

			for range 2 {
				hosts, err := Minimega{}.GetClusterHosts(false, false)
				if err == nil || len(hosts) != 0 {
					t.Errorf("GetClusterHosts(false, false) = %#v, %v; want no hosts and an error", hosts, err)
				}
			}

			cmds := received()

			if got := mmtest.Count(cmds, "mesh list peers"); got != 2 {
				t.Errorf("GetClusterHosts(false, false) twice sent %d mesh list peers, want 2", got)
			}

			if got := mmtest.Count(cmds, meshHostCmd); got != 0 {
				t.Errorf("GetClusterHosts(false, false) sent %d %q, want 0", got, meshHostCmd)
			}

			noPhenixNamespace(t, "GetClusterHosts(false, false)", cmds)
		})
	}
}

func TestGetClusterHostsSchedOnlyLeavesOutHeadnode(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", compute: []string{"compute1"},
	}.reply)

	hosts, err := Minimega{}.GetClusterHosts(true, false)
	if err != nil {
		t.Errorf("GetClusterHosts(true, false) error = %v", err)
	}

	if diff := cmp.Diff([]string{"compute1"}, hostNames(hosts)); diff != "" {
		t.Errorf("GetClusterHosts(true, false) host names mismatch (-want +got):\n%s", diff)
	}

	// With no compute node answering, the schedulable hosts are unknown.
	useFakeMinimega(t, fakeMesh{head: "head", peers: "compute1"}.reply)

	hosts, err = Minimega{}.GetClusterHosts(true, false)
	if !errors.Is(err, ErrMeshUnavailable) || hosts != nil {
		t.Errorf("GetClusterHosts(true, false) with no compute node = %#v, %v; want nil, %v",
			hosts, err, ErrMeshUnavailable)
	}
}

// When the cluster has mesh peers and none of them answers `mesh send all
// host`, or the command fails, a read for VM placement fails with
// ErrMeshUnavailable, and the full list for display still has the headnode
// and the peers that answered. A node without peers is schedulable itself.
func TestClusterHostsMeshUnavailable(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		mesh        fakeMesh
		meshErr     bool
		wantErr     error
		wantSched   []string
		wantDisplay []string
	}{
		"no peer answers": {
			mesh: fakeMesh{head: "head", peers: "compute1,compute2"}, wantErr: ErrMeshUnavailable,
			wantDisplay: []string{"head"},
		},
		"a peer answers with an error": {
			mesh: fakeMesh{head: "head", peers: "compute1,compute2", compute: []string{"compute1"}}, meshErr: true,
			wantErr: ErrMeshUnavailable, wantDisplay: []string{"compute1", "head"},
		},
		"only the headnode": {
			mesh: fakeMesh{head: "head"}, wantSched: []string{"head"}, wantDisplay: []string{"head"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				resps := tc.mesh.reply(cmd)
				if tc.meshErr && cmd.Base == meshHostCmd {
					resps = append(resps, mmtest.Error("compute2", "timed out"))
				}

				return resps
			})

			hosts, err := Minimega{}.GetClusterHosts(true, false)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("GetClusterHosts(true, false) error = %v, want %v", err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.wantSched, hostNames(hosts), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GetClusterHosts(true, false) host names mismatch (-want +got):\n%s", diff)
			}

			display, err := Minimega{}.GetClusterHosts(false, false)
			if err != nil {
				t.Fatalf("GetClusterHosts(false, false) error = %v", err)
			}

			if diff := cmp.Diff(tc.wantDisplay, hostNames(display)); diff != "" {
				t.Errorf("GetClusterHosts(false, false) host names mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetClusterHostsReusesRecentListForDisplay(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", compute: []string{"compute1"},
	}.reply)

	first, read, err := Minimega{}.GetClusterHostsObserved(t.Context(), false, true)
	if err != nil || !read.Known || read.At.IsZero() {
		t.Fatalf("GetClusterHostsObserved(false, true) = %+v, %v; want a known read", read, err)
	}

	sent := len(received())

	second, reused, err := Minimega{}.GetClusterHostsObserved(t.Context(), false, true)
	if err != nil {
		t.Fatalf("GetClusterHostsObserved(false, true) again error = %v", err)
	}

	if got := len(received()); got != sent {
		t.Errorf("GetClusterHostsObserved(false, true) again within %v sent %d more commands, want 0",
			clusterHostsTTL, got-sent)
	}

	if !reused.At.Equal(read.At) {
		t.Errorf("GetClusterHostsObserved(false, true) again observed at %v, want the first read's %v", reused.At, read.At)
	}

	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("GetClusterHostsObserved(false, true) again mismatch (-first +second):\n%s", diff)
	}

	if len(second) == 0 || second[0].DiskUsage == nil {
		t.Fatalf("GetClusterHostsObserved(false, true) again = %+v, want hosts with their disk usage", second)
	}

	// Callers get their own copies.
	second[0].Load = append(second[0].Load, "changed")
	second[0].Name = "changed"
	second[0].DiskUsage.Phenix = 99

	third, _ := Minimega{}.GetClusterHosts(false, true)
	if diff := cmp.Diff(first, third); diff != "" {
		t.Errorf("GetClusterHosts(false, true) after changing a returned list mismatch (-want +got):\n%s", diff)
	}

	// Scheduling always asks minimega.
	if _, err := (Minimega{}).GetClusterHosts(true, false); err != nil {
		t.Errorf("GetClusterHosts(true, false) error = %v", err)
	}

	if got := mmtest.Count(received(), meshHostCmd); got != 2 {
		t.Errorf("GetClusterHosts(true, false) brought the %q commands to %d, want 2 (two runs)", meshHostCmd, got)
	}

	// Once the list is old, it is read again.
	recent := recentHostsFor(true)
	recent.mu.Lock()
	recent.at = time.Now().Add(-clusterHostsTTL)
	recent.mu.Unlock()

	if _, err := (Minimega{}).GetClusterHosts(false, true); err != nil {
		t.Errorf("GetClusterHosts(false, true) after %v error = %v", clusterHostsTTL, err)
	}

	if got := mmtest.Count(received(), meshHostCmd); got != 3 {
		t.Errorf("GetClusterHosts(false, true) after %v brought the %q commands to %d, want 3 (three runs)",
			clusterHostsTTL, meshHostCmd, got)
	}
}

// A full list without disk usage measures nothing and is never handed to a
// caller that asks for disk usage, nor a list with it to one that does not.
func TestGetClusterHostsKeepsDiskUsageListsApart(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", compute: []string{"compute1"},
	}.reply)

	plain, err := Minimega{}.GetClusterHosts(false, false)
	if err != nil {
		t.Fatalf("GetClusterHosts(false, false) error = %v", err)
	}

	if diff := cmp.Diff(map[string]*DiskUsage{"compute1": nil, "head": nil}, diskByHost(plain)); diff != "" {
		t.Errorf("GetClusterHosts(false, false) disk usage by host mismatch (-want +got):\n%s", diff)
	}

	if got := countDiskUsage(received()); got != 0 {
		t.Errorf("GetClusterHosts(false, false) sent %d disk usage commands, want 0", got)
	}

	withDisk, err := Minimega{}.GetClusterHosts(false, true)
	if err != nil {
		t.Fatalf("GetClusterHosts(false, true) after (false, false) error = %v", err)
	}

	want := map[string]*DiskUsage{"compute1": {Phenix: 1, Minimega: 2}, "head": {Phenix: 1, Minimega: 2}}
	if diff := cmp.Diff(want, diskByHost(withDisk)); diff != "" {
		t.Errorf("GetClusterHosts(false, true) after (false, false) disk usage by host mismatch (-want +got):\n%s", diff)
	}

	cmds := received()

	if got := mmtest.Count(cmds, meshHostCmd); got != 2 {
		t.Errorf("GetClusterHosts(false, false) then (false, true) sent %d %q, want 2 (two runs)", got, meshHostCmd)
	}

	if got := countDiskUsage(cmds); got != 2 {
		t.Errorf("GetClusterHosts(false, true) sent %d disk usage commands, want 2", got)
	}

	// the list without disk usage is still the one kept for its callers
	again, err := Minimega{}.GetClusterHosts(false, false)
	if err != nil {
		t.Fatalf("GetClusterHosts(false, false) again error = %v", err)
	}

	if diff := cmp.Diff(plain, again); diff != "" {
		t.Errorf("GetClusterHosts(false, false) again mismatch (-want +got):\n%s", diff)
	}

	if got := len(received()); got != len(cmds) {
		t.Errorf("GetClusterHosts(false, false) again sent %d commands, want 0", got-len(cmds))
	}
}

// The disk usage of all mesh peers is measured with one `mesh send all`
// command and that of the headnode with one `shell` command, and reused: a
// path df fails for, or a host that answers with an error, is not asked again
// until diskUsageFailureTTL has passed.
func TestGetClusterHostsMeasuresDiskOncePerHost(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head: "head-minimega", peers: "compute1-minimega,compute2-minimega,compute3-minimega",
		compute: []string{"compute1-minimega", "compute2-minimega", "compute3-minimega"},
		disk: map[string]string{
			"compute1": "0=11% 1=12%\n",
			"compute2": "0= 1=22%\n", // df failed for the first path
			"head":     "0=1% 1=2%\n",
		},
		diskErr: map[string]bool{"compute3": true},
	}.reply)

	saved := common.HostnameSuffixes
	common.HostnameSuffixes = "-minimega" //nolint:reassign // names minimega reports with a suffix

	t.Cleanup(func() { common.HostnameSuffixes = saved }) //nolint:reassign // restore

	want := map[string]*DiskUsage{
		"compute1": {Phenix: 11, Minimega: 12},
		"compute2": {Phenix: 0, Minimega: 22},
		"compute3": {Phenix: 0, Minimega: 0},
		"head":     {Phenix: 1, Minimega: 2},
	}

	// A schedOnly call always asks minimega for the hosts, but reuses the disk usage measured.
	for _, schedOnly := range []bool{false, true} {
		hosts, err := Minimega{}.GetClusterHosts(schedOnly, true)
		if err != nil {
			t.Errorf("GetClusterHosts(%t, true) error = %v", schedOnly, err)

			continue
		}

		wantUsage := maps.Clone(want)
		if schedOnly {
			delete(wantUsage, "head")
		}

		if diff := cmp.Diff(wantUsage, diskByHost(hosts)); diff != "" {
			t.Errorf("GetClusterHosts(%t, true) disk usage by host mismatch (-want +got):\n%s", schedOnly, diff)
		}
	}

	cmds := received()

	for prefix, want := range map[string]int{meshDiskCmd: 1, headDiskCmd: 1, "mesh send compute": 0} {
		if got := mmtest.Count(cmds, prefix); got != want {
			t.Errorf("GetClusterHosts(false, true) then (true, true) sent %d %q commands, want %d", got, prefix, want)
		}
	}

	// Once the failures are old, the peers are asked again; the head is still fresh.
	diskUsageMu.Lock()

	for key, entry := range diskUsageCache {
		if entry.failed {
			entry.at = entry.at.Add(-diskUsageFailureTTL)
			diskUsageCache[key] = entry
		}
	}

	diskUsageMu.Unlock()

	if _, err := (Minimega{}).GetClusterHosts(true, true); err != nil {
		t.Errorf("GetClusterHosts(true, true) after %v error = %v", diskUsageFailureTTL, err)
	}

	cmds = received()

	for prefix, want := range map[string]int{meshDiskCmd: 2, headDiskCmd: 1} {
		if got := mmtest.Count(cmds, prefix); got != want {
			t.Errorf("GetClusterHosts(true, true) after %v brought the %q commands to %d, want %d",
				diskUsageFailureTTL, prefix, got, want)
		}
	}
}

// A disk usage command that ends because the caller's context is done keeps
// no failure, so the next caller measures again.
func TestDiskUsageKeepsNoFailureOfAnEndedCall(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	release := make(chan struct{})
	mesh := fakeMesh{head: "head", peers: "compute1", compute: []string{"compute1"}}

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.HasPrefix(cmd.Base, meshDiskCmd) {
			<-release
		}

		return mesh.reply(cmd)
	})

	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	if hosts, _, err := (Minimega{}).GetClusterHostsObserved(ctx, true, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetClusterHostsObserved(true, true) with a peer that does not answer = %v, %v; want %v",
			hosts, err, context.DeadlineExceeded)
	}

	diskUsageMu.Lock()
	_, kept := diskUsageCache["compute1\x00"+common.PhenixBase]
	diskUsageMu.Unlock()

	if kept {
		t.Errorf("GetClusterHostsObserved(true, true) kept the disk usage of compute1 after its context ended, want none")
	}

	if got := mmtest.Count(received(), meshDiskCmd); got != 1 {
		t.Errorf("GetClusterHostsObserved(true, true) sent %d %q commands, want 1", got, meshDiskCmd)
	}
}

// A base path that is not safe in a command gives each host a zero disk
// usage, not none, as the other failures do.
func TestFillDiskUsageUnsafeBasePath(t *testing.T) { //nolint:paralleltest // changes common.PhenixBase
	saved := common.PhenixBase
	common.PhenixBase = "/phenix base" //nolint:reassign // a path that fails mmcli.SafeArg

	t.Cleanup(func() { common.PhenixBase = saved }) //nolint:reassign // restore

	hosts := Hosts{{Name: "unsafe-head"}, {Name: "unsafe-compute1"}}
	fillDiskUsage(t.Context(), hosts, func(name string) bool { return name == "unsafe-head" })

	want := map[string]*DiskUsage{"unsafe-compute1": {}, "unsafe-head": {}}
	if diff := cmp.Diff(want, diskByHost(hosts)); diff != "" {
		t.Errorf("fillDiskUsage with base path %q disk usage mismatch (-want +got):\n%s", common.PhenixBase, diff)
	}
}

// A schedOnly read gives the schedulable hosts with no disk usage command
// and shares no run with the full list; diskUsage adds the disk usage of the
// schedulable hosts only: one command for the mesh peers, and one for the
// headnode only when VMs are scheduled on it.
func TestGetClusterHostsSchedOnly(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	mesh := fakeMesh{
		head: "head", peers: "compute[1-2]", compute: []string{"compute1", "compute2"},
		disk: map[string]string{"compute1": "0=11% 1=12%\n", "compute2": "0=21% 1=22%\n"},
	}

	for name, tc := range map[string]struct {
		mesh      fakeMesh
		diskUsage bool
		wantDisk  map[string]*DiskUsage
		wantCmds  []string
	}{
		"without disk usage": {
			mesh:     mesh,
			wantDisk: map[string]*DiskUsage{"compute1": nil, "compute2": nil},
			wantCmds: []string{"minimega: host", "mesh list peers", meshHostCmd},
		},
		"with disk usage": {
			mesh:      mesh,
			diskUsage: true,
			wantDisk: map[string]*DiskUsage{
				"compute1": {Phenix: 11, Minimega: 12}, "compute2": {Phenix: 21, Minimega: 22},
			},
			wantCmds: []string{"minimega: host", "mesh list peers", meshHostCmd, meshDiskCmd},
		},
		"with disk usage of a schedulable headnode": {
			mesh:      fakeMesh{head: "head", disk: map[string]string{"head": "0=5% 1=6%\n"}},
			diskUsage: true,
			wantDisk:  map[string]*DiskUsage{"head": {Phenix: 5, Minimega: 6}},
			wantCmds:  []string{"minimega: host", "mesh list peers", headDiskCmd},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, tc.mesh.reply)

			hosts, err := Minimega{}.GetClusterHosts(true, tc.diskUsage)
			if err != nil {
				t.Fatalf("GetClusterHosts(true, %t) error = %v", tc.diskUsage, err)
			}

			if diff := cmp.Diff(tc.wantDisk, diskByHost(hosts)); diff != "" {
				t.Errorf("GetClusterHosts(true, %t) disk usage by host mismatch (-want +got):\n%s", tc.diskUsage, diff)
			}

			sent := make([]string, 0, len(tc.wantCmds))

			for _, cmd := range received() {
				base := cmd.Base
				if prefix, ok := diskPrefix(cmd); ok {
					base = prefix
				}

				if cmd.Namespace != "" {
					base = cmd.Namespace + ": " + base
				}

				sent = append(sent, base)
			}

			if diff := cmp.Diff(tc.wantCmds, sent, sortStrings); diff != "" {
				t.Errorf("GetClusterHosts(true, %t) sent commands mismatch (-want +got):\n%s", tc.diskUsage, diff)
			}

			// nothing is kept for the full list to reuse
			before := len(received())

			if _, err := (Minimega{}).GetClusterHosts(false, tc.diskUsage); err != nil {
				t.Errorf("GetClusterHosts(false, %t) error = %v", tc.diskUsage, err)
			}

			if got := mmtest.Count(received()[before:], "host"); got != 1 {
				t.Errorf("GetClusterHosts(true, %t) then (false, %t) sent %d more host commands, want 1",
					tc.diskUsage, tc.diskUsage, got)
			}
		})
	}
}

// diskPrefix is the prefix that names a disk usage command, without the
// command itself.
func diskPrefix(cmd mmtest.Command) (string, bool) {
	for _, prefix := range []string{meshDiskCmd, headDiskCmd} {
		if strings.HasPrefix(cmd.Base, prefix) {
			return prefix, true
		}
	}

	return "", false
}

// countDiskUsage counts the disk usage commands among cmds.
func countDiskUsage(cmds []mmtest.Command) int {
	var count int

	for _, cmd := range cmds {
		if isDiskUsage(cmd) {
			count++
		}
	}

	return count
}

// diskByHost gives the disk usage of each host by name, nil where it was not
// measured.
func diskByHost(hosts Hosts) map[string]*DiskUsage {
	disk := make(map[string]*DiskUsage, len(hosts))

	for _, host := range hosts {
		disk[host.Name] = host.DiskUsage
	}

	return disk
}

// sortStrings compares string lists whose order does not matter.
var sortStrings = cmpopts.SortSlices(func(a, b string) bool { return a < b }) //nolint:gochecknoglobals // test option

// Once the deadline of its context passes, a schedOnly read gives up at once
// with an error wrapping [context.DeadlineExceeded], also while a command
// waits for minimega's answer.
func TestGetClusterHostsSchedOnlyDeadline(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	mesh := fakeMesh{head: "head", peers: "compute1", compute: []string{"compute1"}}

	for name, held := range map[string]func(mmtest.Command) bool{
		"the headnode":   func(cmd mmtest.Command) bool { return cmd.Base == "host" && cmd.Namespace == "minimega" },
		"the mesh peers": func(cmd mmtest.Command) bool { return cmd.Base == meshHostCmd },
	} {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})

			useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if held(cmd) {
					<-release
				}

				return mesh.reply(cmd)
			})

			t.Cleanup(func() { close(release) })

			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()

			start := time.Now()

			hosts, _, err := Minimega{}.GetClusterHostsObserved(ctx, true, false)
			if !errors.Is(err, context.DeadlineExceeded) || hosts != nil {
				t.Errorf("GetClusterHostsObserved(true, false) with %s not answering = %v, %v; want nil, %v",
					name, hosts, err, context.DeadlineExceeded)
			}

			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("GetClusterHostsObserved(true, false) took %v after a 200ms deadline, want it to give up at once",
					took)
			}
		})
	}
}

// pastDeadline is a context whose deadline has passed and whose Err is still
// nil, as a context is in the moment before its own timer fires.
type pastDeadline struct {
	context.Context
}

func (pastDeadline) Deadline() (time.Time, bool) { return time.Now().Add(-time.Millisecond), true }

// ended reports a passed deadline also before ctx's own timer sets its error,
// so a command whose timer fired first does not read as a full answer.
func TestEndedAfterTheDeadline(t *testing.T) {
	t.Parallel()

	if err := ended(pastDeadline{Context: context.Background()}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ended(a context past its deadline) = %v, want %v", err, context.DeadlineExceeded)
	}

	if err := ended(t.Context()); err != nil {
		t.Errorf("ended(a live context) = %v, want nil", err)
	}
}

func TestGetClusterHostsErrorIsNotReused(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(mmtest.Command) []*minicli.Response { return nil })

	for range 2 {
		hosts, err := Minimega{}.GetClusterHosts(false, true)
		if err == nil || hosts == nil || len(hosts) != 0 {
			t.Errorf("GetClusterHosts(false, true) with no host answering = %#v, %v; want an empty list and an error",
				hosts, err)
		}
	}

	if got := mmtest.Count(received(), "host"); got != 2 {
		t.Errorf("GetClusterHosts(false, true) twice with no host answering sent %d host commands, want 2", got)
	}
}

// clusterOnlyMM has only GetClusterHosts of the cluster host reads; other MM
// methods panic.
type clusterOnlyMM struct {
	MM

	asked *[2]bool // schedOnly and diskUsage
}

func (m clusterOnlyMM) GetClusterHosts(schedOnly, diskUsage bool) (Hosts, error) {
	*m.asked = [2]bool{schedOnly, diskUsage}

	return Hosts{{Name: "compute1", Schedulable: true}}, nil
}

// The package GetClusterHostsObserved asks DefaultMM for the hosts, and an MM
// without GetClusterHostsObserved for GetClusterHosts with the same flags.
func TestPackageGetClusterHostsObserved(t *testing.T) { //nolint:paralleltest // replaces DefaultMM and shares the fake minimega
	saved := DefaultMM
	t.Cleanup(func() { DefaultMM = saved })

	t.Run("Minimega", func(t *testing.T) {
		DefaultMM = Minimega{}

		useFakeMinimega(t, fakeMesh{head: "head", peers: "compute1", compute: []string{"compute1"}}.reply)

		hosts, _, err := GetClusterHostsObserved(t.Context(), true, false)
		if err != nil {
			t.Fatalf("GetClusterHostsObserved(true, false) error = %v", err)
		}

		if diff := cmp.Diff([]string{"compute1"}, hostNames(hosts)); diff != "" {
			t.Errorf("GetClusterHostsObserved(true, false) host names mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("an MM without it", func(t *testing.T) {
		var asked [2]bool

		DefaultMM = clusterOnlyMM{asked: &asked}

		hosts, observed, err := GetClusterHostsObserved(t.Context(), true, true)
		if err != nil || !observed.Known || asked != [2]bool{true, true} {
			t.Fatalf("GetClusterHostsObserved(true, true) = %v, %+v, %v asking %v; want GetClusterHosts(true, true)",
				hosts, observed, err, asked)
		}

		if diff := cmp.Diff([]string{"compute1"}, hostNames(hosts)); diff != "" {
			t.Errorf("GetClusterHostsObserved(true, true) host names mismatch (-want +got):\n%s", diff)
		}
	})
}

// The host scan, its disk usage and a VM listing send only read-only commands, so they never wait
// for the shared connection.
func TestReadsDoNotWaitForTheSharedConnection(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	mesh := fakeMesh{head: "head", peers: "compute1", compute: []string{"compute1"}}
	cluster := new(vmInfoCluster)
	cluster.set(fakeVM{host: "compute1", name: "a", uptime: "1h0m0s", disk: "snap-a", snapshot: true})

	received := holdShared(t, func(cmd mmtest.Command) []*minicli.Response {
		if resps := mesh.reply(cmd); resps != nil {
			return resps
		}

		return cluster.reply(cmd)
	})

	done := make(chan error, 1)

	go func() {
		if _, err := (Minimega{}).GetClusterHosts(false, true); err != nil {
			done <- err

			return
		}

		_, obs := Minimega{}.GetVMInfoObserved(NS("exp"))
		done <- obs.Err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("GetClusterHosts(false, true) and GetVMInfoObserved(exp) error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("GetClusterHosts(false, true) and GetVMInfoObserved(exp) waited for the shared connection (sent %v)",
			mmtest.Bases(received()))
	}

	for prefix, want := range map[string]int{meshDiskCmd: 1, headDiskCmd: 1, vmInfoCmd: 1, "mesh send compute1 disk info": 1} {
		if got := mmtest.Count(received(), prefix); got != want {
			t.Errorf("GetClusterHosts(false, true) and GetVMInfoObserved(exp) sent %d %q, want %d", got, prefix, want)
		}
	}
}
