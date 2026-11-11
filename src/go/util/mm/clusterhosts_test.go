package mm

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
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
		wantDisk  map[string]DiskUsage
	}{
		{
			name: "single node",
			// a lone node would answer a mesh send if it were sent, so the test can see one
			mesh:      fakeMesh{head: "node01", peers: "", compute: []string{"node01"}},
			wantCmds:  []string{"minimega: host", "mesh list peers"},
			wantRoles: []hostRole{{Name: "node01", Headnode: true, Schedulable: true}},
			wantDisk:  map[string]DiskUsage{"node01": {Phenix: 1, Minimega: 2}},
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
			wantDisk: map[string]DiskUsage{
				"compute1": {Phenix: 11, Minimega: 12},
				"compute2": {Phenix: 21, Minimega: 22},
				"head":     {Phenix: 1, Minimega: 2},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := useFakeMinimega(t, tc.mesh.reply)

			hosts, err := Minimega{}.GetClusterHosts(false)
			if err != nil {
				t.Fatalf("GetClusterHosts(false) error = %v", err)
			}

			cmds := received()

			if diff := cmp.Diff(tc.wantCmds, hostCommands(cmds)); diff != "" {
				t.Errorf("GetClusterHosts(false) sent commands mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantRoles, hostRoles(hosts)); diff != "" {
				t.Errorf("GetClusterHosts(false) mismatch (-want +got):\n%s", diff)
			}

			disk := make(map[string]DiskUsage, len(hosts))
			for _, host := range hosts {
				disk[host.Name] = host.DiskUsage
			}

			if diff := cmp.Diff(tc.wantDisk, disk); diff != "" {
				t.Errorf("GetClusterHosts(false) disk usage by host mismatch (-want +got):\n%s", diff)
			}

			if got, want := mmtest.Count(cmds, "host"), 1; got != want {
				t.Errorf("GetClusterHosts(false) sent %d host commands, want %d", got, want)
			}

			noPhenixNamespace(t, "GetClusterHosts(false)", cmds)

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
				hosts, err := Minimega{}.GetClusterHosts(false)
				if err == nil || len(hosts) != 0 {
					t.Errorf("GetClusterHosts(false) = %#v, %v; want no hosts and an error", hosts, err)
				}
			}

			cmds := received()

			if got := mmtest.Count(cmds, "mesh list peers"); got != 2 {
				t.Errorf("GetClusterHosts(false) twice sent %d mesh list peers, want 2", got)
			}

			if got := mmtest.Count(cmds, meshHostCmd); got != 0 {
				t.Errorf("GetClusterHosts(false) sent %d %q, want 0", got, meshHostCmd)
			}

			noPhenixNamespace(t, "GetClusterHosts(false)", cmds)
		})
	}
}

func TestGetClusterHostsSchedOnlyLeavesOutHeadnode(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", compute: []string{"compute1"},
	}.reply)

	hosts, err := Minimega{}.GetClusterHosts(true)
	if err != nil {
		t.Errorf("GetClusterHosts(true) error = %v", err)
	}

	if diff := cmp.Diff([]string{"compute1"}, hostNames(hosts)); diff != "" {
		t.Errorf("GetClusterHosts(true) host names mismatch (-want +got):\n%s", diff)
	}

	// With no compute node answering, nothing is schedulable.
	useFakeMinimega(t, fakeMesh{head: "head", peers: "compute1"}.reply)

	hosts, err = Minimega{}.GetClusterHosts(true)
	if err != nil || hosts != nil {
		t.Errorf("GetClusterHosts(true) with no compute node = %#v, %v; want nil, nil", hosts, err)
	}
}

func TestGetClusterHostsReusesRecentListForDisplay(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", compute: []string{"compute1"},
	}.reply)

	first, read, err := Minimega{}.GetClusterHostsObserved(t.Context(), false)
	if err != nil || !read.Known || read.At.IsZero() {
		t.Fatalf("GetClusterHostsObserved(false) = %+v, %v; want a known read", read, err)
	}

	sent := len(received())

	second, reused, err := Minimega{}.GetClusterHostsObserved(t.Context(), false)
	if err != nil {
		t.Fatalf("GetClusterHostsObserved(false) again error = %v", err)
	}

	if got := len(received()); got != sent {
		t.Errorf("GetClusterHostsObserved(false) again within %v sent %d more commands, want 0", clusterHostsTTL, got-sent)
	}

	if !reused.At.Equal(read.At) {
		t.Errorf("GetClusterHostsObserved(false) again observed at %v, want the first read's %v", reused.At, read.At)
	}

	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("GetClusterHostsObserved(false) again mismatch (-first +second):\n%s", diff)
	}

	if len(second) == 0 {
		t.Fatal("GetClusterHostsObserved(false) again returned no hosts")
	}

	// Callers get their own copies.
	second[0].Load = append(second[0].Load, "changed")
	second[0].Name = "changed"

	third, _ := Minimega{}.GetClusterHosts(false)
	if diff := cmp.Diff(first, third); diff != "" {
		t.Errorf("GetClusterHosts(false) after changing a returned list mismatch (-want +got):\n%s", diff)
	}

	// Scheduling always asks minimega.
	if _, err := (Minimega{}).GetClusterHosts(true); err != nil {
		t.Errorf("GetClusterHosts(true) error = %v", err)
	}

	if got := mmtest.Count(received(), meshHostCmd); got != 2 {
		t.Errorf("GetClusterHosts(true) brought the %q commands to %d, want 2 (two runs)", meshHostCmd, got)
	}

	// Once the list is old, it is read again.
	recentHosts.mu.Lock()
	recentHosts.at = time.Now().Add(-clusterHostsTTL)
	recentHosts.mu.Unlock()

	if _, err := (Minimega{}).GetClusterHosts(false); err != nil {
		t.Errorf("GetClusterHosts(false) after %v error = %v", clusterHostsTTL, err)
	}

	if got := mmtest.Count(received(), meshHostCmd); got != 3 {
		t.Errorf("GetClusterHosts(false) after %v brought the %q commands to %d, want 3 (three runs)",
			clusterHostsTTL, meshHostCmd, got)
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

	want := map[string]DiskUsage{
		"compute1": {Phenix: 11, Minimega: 12},
		"compute2": {Phenix: 0, Minimega: 22},
		"compute3": {Phenix: 0, Minimega: 0},
		"head":     {Phenix: 1, Minimega: 2},
	}

	// A schedOnly call always asks minimega for the hosts, but reuses the disk usage measured.
	for _, schedOnly := range []bool{false, true} {
		hosts, err := Minimega{}.GetClusterHosts(schedOnly)
		if err != nil {
			t.Errorf("GetClusterHosts(%t) error = %v", schedOnly, err)

			continue
		}

		wantUsage := maps.Clone(want)
		if schedOnly {
			delete(wantUsage, "head")
		}

		got := make(map[string]DiskUsage, len(hosts))
		for _, host := range hosts {
			got[host.Name] = host.DiskUsage
		}

		if diff := cmp.Diff(wantUsage, got); diff != "" {
			t.Errorf("GetClusterHosts(%t) disk usage by host mismatch (-want +got):\n%s", schedOnly, diff)
		}
	}

	cmds := received()

	for prefix, want := range map[string]int{meshDiskCmd: 1, headDiskCmd: 1, "mesh send compute": 0} {
		if got := mmtest.Count(cmds, prefix); got != want {
			t.Errorf("GetClusterHosts(false) then (true) sent %d %q commands, want %d", got, prefix, want)
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

	if _, err := (Minimega{}).GetClusterHosts(true); err != nil {
		t.Errorf("GetClusterHosts(true) after %v error = %v", diskUsageFailureTTL, err)
	}

	cmds = received()

	for prefix, want := range map[string]int{meshDiskCmd: 2, headDiskCmd: 1} {
		if got := mmtest.Count(cmds, prefix); got != want {
			t.Errorf("GetClusterHosts(true) after %v brought the %q commands to %d, want %d",
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

	if hosts, err := (Minimega{}).GetSchedulableHosts(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetSchedulableHosts(true) with a peer that does not answer = %v, %v; want %v",
			hosts, err, context.DeadlineExceeded)
	}

	diskUsageMu.Lock()
	_, kept := diskUsageCache["compute1\x00"+common.PhenixBase]
	diskUsageMu.Unlock()

	if kept {
		t.Errorf("GetSchedulableHosts(true) kept the disk usage of compute1 after its context ended, want none")
	}

	if got := mmtest.Count(received(), meshDiskCmd); got != 1 {
		t.Errorf("GetSchedulableHosts(true) sent %d %q commands, want 1", got, meshDiskCmd)
	}
}

// GetSchedulableHosts reads the schedulable hosts with no disk usage command
// and shares no run with GetClusterHosts; diskUsage adds the disk usage
// with one command for the mesh peers and one for the headnode.
func TestGetSchedulableHosts(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	mesh := fakeMesh{
		head: "head", peers: "compute[1-2]", compute: []string{"compute1", "compute2"},
		disk: map[string]string{"compute1": "0=11% 1=12%\n", "compute2": "0=21% 1=22%\n"},
	}

	for name, tc := range map[string]struct {
		diskUsage bool
		wantDisk  map[string]DiskUsage
		wantCmds  []string
	}{
		"without disk usage": {
			wantDisk: map[string]DiskUsage{"compute1": {}, "compute2": {}},
			wantCmds: []string{"minimega: host", "mesh list peers", meshHostCmd},
		},
		"with disk usage": {
			diskUsage: true,
			wantDisk: map[string]DiskUsage{
				"compute1": {Phenix: 11, Minimega: 12}, "compute2": {Phenix: 21, Minimega: 22},
			},
			wantCmds: []string{"minimega: host", "mesh list peers", meshHostCmd, headDiskCmd, meshDiskCmd},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, mesh.reply)

			hosts, err := Minimega{}.GetSchedulableHosts(t.Context(), tc.diskUsage)
			if err != nil {
				t.Fatalf("GetSchedulableHosts() error = %v", err)
			}

			got := make(map[string]DiskUsage, len(hosts))
			for _, host := range hosts {
				got[host.Name] = host.DiskUsage
			}

			if diff := cmp.Diff(tc.wantDisk, got); diff != "" {
				t.Errorf("GetSchedulableHosts() disk usage by host mismatch (-want +got):\n%s", diff)
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

			if diff := cmp.Diff(tc.wantCmds, sent); diff != "" {
				t.Errorf("GetSchedulableHosts() sent commands mismatch (-want +got):\n%s", diff)
			}

			// nothing is kept for GetClusterHosts(false) to reuse
			if _, err := (Minimega{}).GetClusterHosts(false); err != nil {
				t.Errorf("GetClusterHosts(false) error = %v", err)
			}

			if got := mmtest.Count(received(), meshHostCmd); got != 2 {
				t.Errorf("GetSchedulableHosts() then GetClusterHosts(false) sent %d %q, want 2", got, meshHostCmd)
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

// Once the deadline of its context passes, GetSchedulableHosts gives up at
// once with an error wrapping [context.DeadlineExceeded], also while a command
// waits for minimega's answer.
func TestGetSchedulableHostsDeadline(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	release := make(chan struct{})
	mesh := fakeMesh{head: "head", peers: "compute1", compute: []string{"compute1"}}

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == meshHostCmd {
			<-release
		}

		return mesh.reply(cmd)
	})

	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	hosts, err := Minimega{}.GetSchedulableHosts(ctx, false)
	if !errors.Is(err, context.DeadlineExceeded) || hosts != nil {
		t.Errorf("GetSchedulableHosts() with a mesh that does not answer = %v, %v; want nil, %v",
			hosts, err, context.DeadlineExceeded)
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("GetSchedulableHosts() took %v after a 200ms deadline, want it to give up at once", took)
	}
}

func TestGetClusterHostsErrorIsNotReused(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(mmtest.Command) []*minicli.Response { return nil })

	for range 2 {
		hosts, err := Minimega{}.GetClusterHosts(false)
		if err == nil || hosts == nil || len(hosts) != 0 {
			t.Errorf("GetClusterHosts(false) with no host answering = %#v, %v; want an empty list and an error", hosts, err)
		}
	}

	if got := mmtest.Count(received(), "host"); got != 2 {
		t.Errorf("GetClusterHosts(false) twice with no host answering sent %d host commands, want 2", got)
	}
}
