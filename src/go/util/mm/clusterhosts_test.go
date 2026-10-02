package mm

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
)

// fakeMesh answers the commands GetClusterHosts sends for a headnode and the
// compute nodes the __phenix__ namespace spans.
type fakeMesh struct {
	head    string
	peers   string // `mesh list peers`
	members string // `ns hosts` in __phenix__
	compute []string
	peerErr bool
	disk    map[string]string // disk usage output by host, "0=1% 1=2%" if not given
}

func (f fakeMesh) reply(cmd mmtest.Command) []*minicli.Response {
	header := []string{"name", "cpus"}

	switch {
	case cmd.Base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular(f.head, header, []string{f.head, "8"})}
	case cmd.Base == "host" && cmd.Namespace == phenixHostsNS:
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
	case cmd.Base == "ns hosts" && cmd.Namespace == phenixHostsNS:
		return []*minicli.Response{mmtest.Text(f.head, f.members)}
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

// hostCommands lists the commands sent, leaving out disk usage measurements.
func hostCommands(cmds []mmtest.Command) []string {
	var out []string

	for _, cmd := range cmds {
		if strings.HasPrefix(cmd.Base, "mesh send ") || strings.HasPrefix(cmd.Base, "shell ") {
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

func hostNames(hosts Hosts) []string {
	names := make([]string, 0, len(hosts))

	for _, host := range hosts {
		names = append(names, host.Name)
	}

	return names
}

func TestGetClusterHostsReusesCurrentNamespace(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head:    "head",
		peers:   "compute[1-2]",
		members: "compute2,compute1",
		compute: []string{"compute1", "compute2"},
	}.reply)

	hosts, err := Minimega{}.GetClusterHosts(false)
	if err != nil {
		t.Fatalf("GetClusterHosts: %v", err)
	}

	want := []string{
		"minimega: host",
		"mesh list peers",
		phenixHostsNS + ": ns hosts",
		phenixHostsNS + ": host",
	}

	if got := hostCommands(received()); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent %q, want %q", got, want)
	}

	if got := hostNames(hosts); !reflect.DeepEqual(got, []string{"compute1", "compute2", "head"}) {
		t.Fatalf("hosts = %q", got)
	}

	head := hosts[2]
	if !head.Headnode || head.Schedulable {
		t.Fatalf("head = %#v, want an unschedulable headnode", head)
	}

	if !hosts[0].Schedulable || hosts[0].Headnode {
		t.Fatalf("compute1 = %#v, want a schedulable compute node", hosts[0])
	}

	if got := (Minimega{}).Headnode(); got != "head" {
		t.Fatalf("Headnode() = %q, want %q", got, "head")
	}

	if got := mmtest.Count(received(), "host"); got != 2 {
		t.Fatalf("Headnode() sent its own host command (%d in all)", got)
	}
}

func TestGetClusterHostsRecreatesChangedNamespace(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, mesh := range map[string]fakeMesh{
		"node joined": {
			head: "head", peers: "compute[1-3]", members: "compute[1-2]",
			compute: []string{"compute1", "compute2"},
		},
		"node left": {
			head: "head", peers: "compute1", members: "compute[1-2]",
			compute: []string{"compute1"},
		},
		"peers unreadable": {
			head: "head", peerErr: true, members: "compute1",
			compute: []string{"compute1"},
		},
		"only node left": {
			head: "head", peers: "", members: "compute1",
			compute: []string{"head"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, mesh.reply)

			if _, err := (Minimega{}).GetClusterHosts(true); err != nil {
				t.Fatalf("GetClusterHosts: %v", err)
			}

			cmds := hostCommands(received())

			if got := mmtest.Count(received(), "clear namespace "+phenixHostsNS); got != 1 {
				t.Fatalf("sent %q, want one clear namespace", cmds)
			}

			if last := cmds[len(cmds)-1]; last != phenixHostsNS+": host" {
				t.Fatalf("sent %q, want host in the recreated namespace last", cmds)
			}
		})
	}
}

func TestGetClusterHostsSingleNode(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	// minimega reports host lists compressed, zero padding dropped
	received := useFakeMinimega(t, fakeMesh{
		head:    "node01",
		peers:   "",
		members: "node1",
		compute: []string{"node01"},
	}.reply)

	for _, schedOnly := range []bool{true, false} {
		hosts, err := Minimega{}.GetClusterHosts(schedOnly)
		if err != nil {
			t.Fatalf("GetClusterHosts(%t): %v", schedOnly, err)
		}

		if len(hosts) != 1 || hosts[0].Name != "node01" || !hosts[0].Headnode || !hosts[0].Schedulable {
			t.Fatalf("GetClusterHosts(%t) = %#v, want the schedulable headnode", schedOnly, hosts)
		}
	}

	if got := mmtest.Count(received(), "clear namespace"); got != 0 {
		t.Fatalf("cleared the namespace %d times, want 0", got)
	}
}

func TestGetClusterHostsSchedOnlyLeavesOutHeadnode(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", members: "compute1", compute: []string{"compute1"},
	}.reply)

	hosts, err := Minimega{}.GetClusterHosts(true)
	if err != nil {
		t.Fatalf("GetClusterHosts: %v", err)
	}

	if got := hostNames(hosts); !reflect.DeepEqual(got, []string{"compute1"}) {
		t.Fatalf("hosts = %q, want only the compute node", got)
	}

	// With no compute node answering, nothing is schedulable.
	useFakeMinimega(t, fakeMesh{head: "head", peers: "compute1", members: "compute1"}.reply)

	hosts, err = Minimega{}.GetClusterHosts(true)
	if err != nil || hosts != nil {
		t.Fatalf("GetClusterHosts = %#v, %v; want nil hosts", hosts, err)
	}
}

func TestGetClusterHostsReusesRecentListForDisplay(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", members: "compute1", compute: []string{"compute1"},
	}.reply)

	first, err := Minimega{}.GetClusterHosts(false)
	if err != nil {
		t.Fatalf("GetClusterHosts: %v", err)
	}

	sent := len(received())

	second, err := Minimega{}.GetClusterHosts(false)
	if err != nil {
		t.Fatalf("GetClusterHosts: %v", err)
	}

	if got := len(received()); got != sent {
		t.Fatalf("a second call within %v sent %d more commands", clusterHostsTTL, got-sent)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reused list %#v differs from %#v", second, first)
	}

	// Callers get their own copies.
	second[0].Load = append(second[0].Load, "changed")
	second[0].Name = "changed"

	third, _ := Minimega{}.GetClusterHosts(false)
	if !reflect.DeepEqual(first, third) {
		t.Fatalf("changing a returned list changed the shared one: %#v", third)
	}

	// Scheduling always asks minimega.
	if _, err := (Minimega{}).GetClusterHosts(true); err != nil {
		t.Fatalf("GetClusterHosts: %v", err)
	}

	if got := mmtest.Count(received(), "host"); got != 4 {
		t.Fatalf("sent %d host commands, want 4 (two runs)", got)
	}

	// Once the list is old, it is read again.
	recentHosts.mu.Lock()
	recentHosts.at = time.Now().Add(-clusterHostsTTL)
	recentHosts.mu.Unlock()

	if _, err := (Minimega{}).GetClusterHosts(false); err != nil {
		t.Fatalf("GetClusterHosts: %v", err)
	}

	if got := mmtest.Count(received(), "host"); got != 6 {
		t.Fatalf("sent %d host commands, want 6 (three runs)", got)
	}
}

func TestGetClusterHostsMeasuresDiskOncePerHost(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute[1-2]", members: "compute[1-2]",
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
			t.Fatalf("GetClusterHosts(%t): %v", schedOnly, err)
		}

		names := []string{"compute1", "compute2", "head"}
		if schedOnly {
			names = names[:2]
		}

		if got := hostNames(hosts); !reflect.DeepEqual(got, names) {
			t.Fatalf("GetClusterHosts(%t) = %q, want %q", schedOnly, got, names)
		}

		for _, host := range hosts {
			if host.DiskUsage != want[host.Name] {
				t.Errorf("host %s disk usage = %#v, want %#v", host.Name, host.DiskUsage, want[host.Name])
			}
		}
	}

	cmds := received()

	for prefix, want := range map[string]int{
		"mesh send compute1 shell ": 1,
		"mesh send compute2 shell ": 2,
		"shell ":                    1,
	} {
		if got := mmtest.Count(cmds, prefix); got != want {
			t.Errorf("sent %d %q commands, want %d", got, prefix, want)
		}
	}
}

func TestGetClusterHostsErrorIsNotReused(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(mmtest.Command) []*minicli.Response { return nil })

	for range 2 {
		hosts, err := Minimega{}.GetClusterHosts(false)
		if err == nil || hosts == nil || len(hosts) != 0 {
			t.Fatalf("GetClusterHosts = %#v, %v; want an empty list and an error", hosts, err)
		}
	}

	if got := mmtest.Count(received(), "host"); got != 2 {
		t.Fatalf("sent %d host commands, want 2", got)
	}
}

func TestSameHostSet(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		a, b []string
		want bool
	}{
		{nil, nil, true},
		{[]string{"a", "b"}, []string{"b", "a"}, true},
		{[]string{"a", "a"}, []string{"a"}, true},
		{[]string{"node01", "node010"}, []string{"node1", "node10"}, true},
		{[]string{"node0"}, []string{"node00"}, true},
		{[]string{"a"}, nil, false},
		{[]string{"a", "b"}, []string{"a", "c"}, false},
		{[]string{"node1"}, []string{"node11"}, false},
	} {
		if got := sameHostSet(test.a, test.b); got != test.want {
			t.Errorf("sameHostSet(%q, %q) = %t, want %t", test.a, test.b, got, test.want)
		}
	}
}
