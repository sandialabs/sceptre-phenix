package mm

import (
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

// fakeMesh answers the commands GetClusterHosts sends for a headnode and its
// mesh peers.
type fakeMesh struct {
	head    string
	peers   string   // `mesh list peers`
	compute []string // the nodes that answer `mesh send all host`
	peerErr bool
	disk    map[string]string // disk usage output by host, "0=1% 1=2%" if not given
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
	case strings.HasPrefix(cmd.Base, "mesh send ") || strings.HasPrefix(cmd.Base, "shell "):
		host := f.head
		if fields := strings.Fields(cmd.Base); fields[0] == "mesh" {
			host = fields[2]
		}

		usage, ok := f.disk[host]
		if !ok {
			usage = "0=1% 1=2%\n"
		}

		return []*minicli.Response{mmtest.Text(host, usage)}
	}

	return nil
}

// isDiskUsage reports whether cmd measures a host's disk usage.
func isDiskUsage(cmd mmtest.Command) bool {
	return strings.HasPrefix(cmd.Base, "shell ") ||
		(strings.HasPrefix(cmd.Base, "mesh send ") && strings.Contains(cmd.Base, " shell "))
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

func TestGetClusterHostsMeasuresDiskOncePerHost(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute[1-2]",
		compute: []string{"compute1", "compute2"},
		disk: map[string]string{
			"compute1": "0=11% 1=12%\n",
			"compute2": "0= 1=22%\n", // df failed for the first path
			"head":     "0=1% 1=2%\n",
		},
	}.reply)

	want := map[string]DiskUsage{
		"compute1": {Phenix: 11, Minimega: 12},
		"compute2": {Phenix: 0, Minimega: 22},
		"head":     {Phenix: 1, Minimega: 2},
	}

	// A schedOnly call always asks minimega for the hosts, but reuses the
	// disk usage measured, except for compute2's failed path.
	for _, schedOnly := range []bool{false, true} {
		hosts, err := Minimega{}.GetClusterHosts(schedOnly)
		if err != nil {
			t.Errorf("GetClusterHosts(%t) error = %v", schedOnly, err)

			continue
		}

		names := []string{"compute1", "compute2", "head"}
		wantUsage := maps.Clone(want)

		if schedOnly {
			names = names[:2]
			delete(wantUsage, "head")
		}

		if diff := cmp.Diff(names, hostNames(hosts)); diff != "" {
			t.Errorf("GetClusterHosts(%t) host names mismatch (-want +got):\n%s", schedOnly, diff)
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

	for prefix, want := range map[string]int{
		"mesh send compute1 shell ": 1,
		"mesh send compute2 shell ": 2,
		"shell ":                    1,
	} {
		if got := mmtest.Count(cmds, prefix); got != want {
			t.Errorf("GetClusterHosts(false) then (true) sent %d %q commands, want %d", got, prefix, want)
		}
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
