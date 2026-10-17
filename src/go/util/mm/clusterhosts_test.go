package mm

import (
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

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

func TestGetClusterHostsReusesCurrentNamespace(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head:    "head",
		peers:   "compute[1-2]",
		members: "compute2,compute1",
		compute: []string{"compute1", "compute2"},
	}.reply)

	hosts, err := Minimega{}.GetClusterHosts(false)
	if err != nil {
		t.Fatalf("GetClusterHosts(false) error = %v", err)
	}

	want := []string{
		"minimega: host",
		"mesh list peers",
		phenixHostsNS + ": ns hosts",
		phenixHostsNS + ": host",
	}

	if diff := cmp.Diff(want, hostCommands(received())); diff != "" {
		t.Errorf("GetClusterHosts(false) sent commands mismatch (-want +got):\n%s", diff)
	}

	wantRoles := []hostRole{
		{Name: "compute1", Headnode: false, Schedulable: true},
		{Name: "compute2", Headnode: false, Schedulable: true},
		{Name: "head", Headnode: true, Schedulable: false},
	}

	if diff := cmp.Diff(wantRoles, hostRoles(hosts)); diff != "" {
		t.Errorf("GetClusterHosts(false) mismatch (-want +got):\n%s", diff)
	}

	if got := (Minimega{}).Headnode(); got != "head" {
		t.Errorf("Headnode() = %q, want %q", got, "head")
	}

	if got := mmtest.Count(received(), "host"); got != 2 {
		t.Errorf("Headnode() after GetClusterHosts sent host commands, %d in all, want 2", got)
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
				t.Fatalf("GetClusterHosts(true) error = %v", err)
			}

			cmds := hostCommands(received())

			if got := mmtest.Count(received(), "clear namespace "+phenixHostsNS); got != 1 {
				t.Errorf("GetClusterHosts(true) sent %d clear namespace commands, want 1: %q", got, cmds)
			}

			if want := phenixHostsNS + ": host"; len(cmds) == 0 || cmds[len(cmds)-1] != want {
				t.Errorf("GetClusterHosts(true) sent %q, want %q last", cmds, want)
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
			t.Errorf("GetClusterHosts(%t) error = %v", schedOnly, err)

			continue
		}

		want := []hostRole{{Name: "node01", Headnode: true, Schedulable: true}}
		if diff := cmp.Diff(want, hostRoles(hosts)); diff != "" {
			t.Errorf("GetClusterHosts(%t) mismatch (-want +got):\n%s", schedOnly, diff)
		}
	}

	if got := mmtest.Count(received(), "clear namespace"); got != 0 {
		t.Errorf("GetClusterHosts on a single node sent %d clear namespace commands, want 0", got)
	}
}

func TestGetClusterHostsSchedOnlyLeavesOutHeadnode(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", members: "compute1", compute: []string{"compute1"},
	}.reply)

	hosts, err := Minimega{}.GetClusterHosts(true)
	if err != nil {
		t.Errorf("GetClusterHosts(true) error = %v", err)
	}

	if diff := cmp.Diff([]string{"compute1"}, hostNames(hosts)); diff != "" {
		t.Errorf("GetClusterHosts(true) host names mismatch (-want +got):\n%s", diff)
	}

	// With no compute node answering, nothing is schedulable.
	useFakeMinimega(t, fakeMesh{head: "head", peers: "compute1", members: "compute1"}.reply)

	hosts, err = Minimega{}.GetClusterHosts(true)
	if err != nil || hosts != nil {
		t.Errorf("GetClusterHosts(true) with no compute node = %#v, %v; want nil, nil", hosts, err)
	}
}

func TestGetClusterHostsReusesRecentListForDisplay(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, fakeMesh{
		head: "head", peers: "compute1", members: "compute1", compute: []string{"compute1"},
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

	if got := mmtest.Count(received(), "host"); got != 4 {
		t.Errorf("GetClusterHosts(true) brought the host commands to %d, want 4 (two runs)", got)
	}

	// Once the list is old, it is read again.
	recentHosts.mu.Lock()
	recentHosts.at = time.Now().Add(-clusterHostsTTL)
	recentHosts.mu.Unlock()

	if _, err := (Minimega{}).GetClusterHosts(false); err != nil {
		t.Errorf("GetClusterHosts(false) after %v error = %v", clusterHostsTTL, err)
	}

	if got := mmtest.Count(received(), "host"); got != 6 {
		t.Errorf("GetClusterHosts(false) after %v brought the host commands to %d, want 6 (three runs)", clusterHostsTTL, got)
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

func TestSameHostSet(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
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
		if got := sameHostSet(tc.a, tc.b); got != tc.want {
			t.Errorf("sameHostSet(%q, %q) = %t, want %t", tc.a, tc.b, got, tc.want)
		}
	}
}

// clusterhostsHost is a host name as minimega reports it: no leading zeros.
type clusterhostsHost struct {
	prefix string
	num    int
	rack   int // -1 for none
}

func genClusterhostsHosts() *rapid.Generator[[]clusterhostsHost] {
	host := rapid.Custom(func(t *rapid.T) clusterhostsHost {
		return clusterhostsHost{
			prefix: rapid.SampledFrom([]string{"node", "n", "gpu"}).Draw(t, "prefix"),
			num:    rapid.IntRange(0, 120).Draw(t, "num"),
			rack:   rapid.IntRange(-1, 3).Draw(t, "rack"),
		}
	})

	return rapid.SliceOfDistinct(host, func(h clusterhostsHost) clusterhostsHost { return h })
}

// clusterhostsSpell writes a host's numbers with any number of leading zeros.
func clusterhostsSpell(t *rapid.T, h clusterhostsHost) string {
	pad := func(n int) string {
		return strings.Repeat("0", rapid.IntRange(0, 2).Draw(t, "zeros")) + strconv.Itoa(n)
	}

	name := h.prefix + pad(h.num)
	if h.rack >= 0 {
		name += "-r" + pad(h.rack)
	}

	return name
}

// clusterhostsList spells hosts in a shuffled list with some names repeated.
func clusterhostsList(t *rapid.T, hosts []clusterhostsHost, label string) []string {
	names := make([]string, 0, 2*len(hosts))

	for _, h := range hosts {
		for range rapid.IntRange(1, 2).Draw(t, label+" copies") {
			names = append(names, clusterhostsSpell(t, h))
		}
	}

	return rapid.Permutation(names).Draw(t, label)
}

func TestSameHostSetProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		hosts := genClusterhostsHosts().Draw(t, "hosts")
		a, b := clusterhostsList(t, hosts, "a"), clusterhostsList(t, hosts, "b")

		if !sameHostSet(a, b) || !sameHostSet(b, a) {
			t.Fatalf("sameHostSet(%q, %q) = false, want true", a, b)
		}

		if len(hosts) == 0 {
			return
		}

		// leaving out any one host, every spelling of it, makes a different set
		drop := rapid.IntRange(0, len(hosts)-1).Draw(t, "drop")
		fewer := clusterhostsList(t, append(hosts[:drop:drop], hosts[drop+1:]...), "fewer")

		if sameHostSet(a, fewer) || sameHostSet(fewer, a) {
			t.Fatalf("sameHostSet(%q, %q) = true, want false", a, fewer)
		}
	})
}
