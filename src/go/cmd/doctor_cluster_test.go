package cmd

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// clusterInventory is a minimega on the node mm1 with the namespaces of vms, and the VMs vms.
func clusterInventory(vms ...doctorMMVM) *doctorInventory {
	inv := doctorTestInventory(vms, nil, nil)
	inv.local = "mm1"

	for _, vm := range vms {
		if !strings.Contains(strings.Join(inv.namespaces, " "), vm.namespace) {
			inv.namespaces = append(inv.namespaces, vm.namespace)
		}
	}

	return inv
}

// limitsHost is a good host with the minimega process pid 77, its limits and open files.
func limitsHost(limits string, open int) doctorHostFS {
	host := doctorGoodHost()
	host["/proc/77/cmdline"] = "/usr/local/bin/minimega\x00-base=/tmp/minimega\x00"
	host["/proc/77/limits"] = limits

	for i := range open {
		host["/proc/77/fd/"+string(rune('a'+i%26))+strings.Repeat("x", i/26)] = ""
	}

	return host
}

const (
	limits1024 = "Limit                     Soft Limit           Hard Limit           Units     \n" +
		"Max cpu time              unlimited            unlimited            seconds   \n" +
		"Max open files            1024                 4096                 files     \n"
	limitsNone = "Max open files            unlimited            unlimited            files     \n"
)

// The open files row compares the soft limit of minimega's open files with what its VMs on this node need and with
// the files it has open. It skips when it cannot find or read the minimega process.
func TestMinimegaOpenFilesCheck(t *testing.T) {
	t.Parallel()

	vms := func(n int, host string) []doctorMMVM {
		list := make([]doctorMMVM, n)
		for i := range list {
			list[i] = doctorMMVM{namespace: "e", name: "vm", state: "RUNNING", host: host}
		}

		return list
	}

	tests := map[string]struct {
		host    doctorHostFS
		pid     string // the content of minimega.pid; "" for none
		vms     []doctorMMVM
		invWhy  string
		want    doctorCheck
		wantPid bool
	}{
		"enough": {
			host: limitsHost(limits1024, 3), pid: "77\n", vms: append(vms(2, "mm1"), vms(500, "node2")...),
			want: row("", doctorPass, "minimega (pid 77) can open 1024 files (soft limit, hard limit 4096). Its 2 VMs on "+
				"this node need about 74 (64, and 5 for each VM: QMP, VNC, cc and two qemu output pipes). 3 files are open now"),
		},
		"too many VMs": {
			host: limitsHost(limits1024, 3), pid: "77", vms: vms(200, "mm1"),
			want: row("", doctorWarn, "minimega (pid 77) can open 1024 files (soft limit, hard limit 4096). That is too few "+
				"for its VMs: its 200 VMs on this node need about 1064 (64, and 5 for each VM: QMP, VNC, cc and two qemu "+
				"output pipes). 3 files are open now"),
		},
		"most files open": {
			host: limitsHost(limits1024, 900), pid: "77",
			want: row("", doctorWarn, "minimega (pid 77) can open 1024 files (soft limit, hard limit 4096). Its 0 VMs on "+
				"this node need about 64 (64, and 5 for each VM: QMP, VNC, cc and two qemu output pipes). 900 files are "+
				"open now, so more than 80 percent of the limit are open"),
		},
		"one VM": {
			host: limitsHost(limits1024, 3), pid: "77", vms: vms(1, "mm1"),
			want: row("", doctorPass, "minimega (pid 77) can open 1024 files (soft limit, hard limit 4096). Its 1 VM on "+
				"this node needs about 69 (64, and 5 for each VM: QMP, VNC, cc and two qemu output pipes). 3 files are open now"),
		},
		"no minimega answer": {
			host: limitsHost(limits1024, 1), pid: "77", invWhy: "minimega gave no version",
			want: row("", doctorPass, "minimega (pid 77) can open 1024 files (soft limit, hard limit 4096). The VMs are not "+
				"counted, because minimega gave no version. 1 file is open now"),
		},
		"unlimited": {
			host: limitsHost(limitsNone, 1), pid: "77",
			want: row("", doctorPass, "minimega (pid 77) has no limit of open files"),
		},
		"no fd access": {
			host: limitsHost(limits1024, 0), pid: "77",
			want: row("", doctorPass, "minimega (pid 77) can open 1024 files (soft limit, hard limit 4096). Its 0 VMs on "+
				"this node need about 64 (64, and 5 for each VM: QMP, VNC, cc and two qemu output pipes). This user "+
				"cannot count the open files in /proc/77/fd"),
		},
		"no limits line": {
			host: limitsHost("Max cpu time unlimited unlimited seconds\n", 0), pid: "77",
			want: row("", doctorSkip, "not checked, because /proc/77/limits gives no limit of open files"),
		},
		"another container": {
			host: doctorGoodHost(), pid: "77",
			want: row("", doctorSkip, "not checked, because this host shows no process 77 (from <base>/minimega.pid), "+
				"such as when minimega runs in another container"),
		},
		"not minimega": {
			host: func() doctorHostFS { h := doctorGoodHost(); h["/proc/77/cmdline"] = "/sbin/init\x00"; return h }(),
			pid:  "77",
			want: row("", doctorSkip, "not checked, because the process 77 (from <base>/minimega.pid) is not minimega "+
				"here, such as when minimega runs in another container"),
		},
		"no pid file": {
			host: limitsHost(limits1024, 0),
			want: row("", doctorSkip, "not checked, because <base>/minimega.pid cannot be read, so the minimega process "+
				"is not known"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inv := clusterInventory(tc.vms...)
			inv.why = tc.invWhy
			in, dir := leftoverInput(t, tc.host, nil, inv)
			base := filepath.Join(dir, "minimega")

			readFile := in.env.readFile
			in.env.readFile = func(path string) ([]byte, error) {
				if path == filepath.Join(base, minimegaPidFile) && tc.pid != "" {
					return []byte(tc.pid), nil
				}

				return readFile(path)
			}

			if name == "no fd access" {
				readDir := in.env.readDir
				in.env.readDir = func(path string) ([]os.DirEntry, error) {
					if path == "/proc/77/fd" {
						return nil, os.ErrPermission
					}

					return readDir(path)
				}
			}

			got := minimegaOpenFilesCheck(t.Context(), in)
			got.Detail = strings.ReplaceAll(got.Detail, base, "<base>")

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("minimegaOpenFilesCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The memory row warns when little memory is available, when most of the swap is in use, or when the VMs on this
// node ask for more than the memory and swap.
func TestMinimegaMemoryCheck(t *testing.T) {
	t.Parallel()

	meminfo := func(total, avail, swapTotal, swapFree int) string {
		return "MemTotal: " + strconv.Itoa(total) + " kB\nMemAvailable: " + strconv.Itoa(avail) + " kB\n" +
			"SwapTotal: " + strconv.Itoa(swapTotal) + " kB\nSwapFree: " + strconv.Itoa(swapFree) + " kB\n"
	}
	gib := 1 << 20 // in kB

	tests := map[string]struct {
		meminfo string // "" for no /proc/meminfo
		vms     []doctorMMVM
		invWhy  string
		want    doctorCheck
	}{
		"good": {
			meminfo: meminfo(16*gib, 8*gib, 4*gib, 4*gib),
			vms: []doctorMMVM{
				{namespace: "e", name: "a", state: "RUNNING", host: "mm1", memory: 2048},
				{namespace: "e", name: "b", state: "QUIT", host: "mm1", memory: 1 << 20},
				{namespace: "e", name: "c", state: "RUNNING", host: "node2", memory: 1 << 20},
			},
			want: row("", doctorPass, "the 1 VM on this node asks for 2.0 GiB of memory. MemTotal 16.0 GiB, MemAvailable "+
				"8.0 GiB, SwapTotal 4.0 GiB, SwapFree 4.0 GiB"),
		},
		"short of memory and swap": {
			meminfo: meminfo(16*gib, gib, 4*gib, gib),
			want: row("", doctorWarn, "only 1.0 GiB of 16.0 GiB memory is available, less than 10 percent. 3.0 GiB of "+
				"4.0 GiB swap is in use, more than 50 percent. The 0 VMs on this node ask for 0 B of memory. MemTotal "+
				"16.0 GiB, MemAvailable 1.0 GiB, SwapTotal 4.0 GiB, SwapFree 1.0 GiB"),
		},
		"VMs ask too much": {
			meminfo: meminfo(4*gib, 3*gib, 0, 0),
			vms: []doctorMMVM{
				{namespace: "e", name: "a", state: "RUNNING", host: "mm1", memory: 4096},
				{namespace: "f", name: "b", state: "PAUSED", host: "mm1", memory: 1024},
			},
			want: row("", doctorWarn, "the 2 VMs on this node ask for 5.0 GiB of memory, more than the 4.0 GiB of memory "+
				"and swap. MemTotal 4.0 GiB, MemAvailable 3.0 GiB, SwapTotal 0 B, SwapFree 0 B"),
		},
		"no minimega answer": {
			meminfo: meminfo(4*gib, 3*gib, 0, 0), invWhy: "minimega gave no version",
			want: row("", doctorPass, "the memory of the VMs is not checked, because minimega gave no version. MemTotal "+
				"4.0 GiB, MemAvailable 3.0 GiB, SwapTotal 0 B, SwapFree 0 B"),
		},
		"no meminfo": {want: row("", doctorSkip, "not checked, because this host has no /proc/meminfo to read")},
		"no MemTotal": {
			meminfo: "MemFree: 10 kB\n",
			want:    row("", doctorSkip, "not checked, because /proc/meminfo gives no MemTotal"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			delete(host, "/proc/meminfo")

			if tc.meminfo != "" {
				host["/proc/meminfo"] = tc.meminfo
			}

			inv := clusterInventory(tc.vms...)
			inv.why = tc.invWhy
			in, _ := leftoverInput(t, host, nil, inv)

			if diff := cmp.Diff(tc.want, minimegaMemoryCheck(t.Context(), in)); diff != "" {
				t.Errorf("minimegaMemoryCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// minimega-limits gives its two rows, named, and skips both off Linux.
func TestMinimegaLimitsChecks(t *testing.T) {
	t.Parallel()

	in, _ := leftoverInput(t, doctorGoodHost(), nil, clusterInventory())

	got := minimegaLimitsChecks(t.Context(), in)
	if names := []string{got[0].Name, got[1].Name}; !cmp.Equal(names, []string{limitsFilesRow, limitsMemoryRow}) {
		t.Errorf("minimegaLimitsChecks() names = %v", names)
	}

	in.system.os = "darwin"

	want := []doctorCheck{
		row(limitsFilesRow, doctorSkip, "not checked, because minimega runs only on Linux"),
		row(limitsMemoryRow, doctorSkip, "not checked, because minimega runs only on Linux"),
	}
	if diff := cmp.Diff(want, minimegaLimitsChecks(t.Context(), in)); diff != "" {
		t.Errorf("minimegaLimitsChecks() off Linux mismatch (-want +got):\n%s", diff)
	}
}

// state-drift warns about stored running experiments with no VMs in minimega, and about namespaces with VMs that no
// stored experiment owns. Empty namespaces are not named.
func TestStateDriftChecks(t *testing.T) {
	t.Parallel()

	stored := []doctorExperiment{
		{name: "up", running: true}, {name: "gone", running: true}, {name: "empty", running: true}, {name: "off"},
	}
	inv := clusterInventory(
		doctorMMVM{namespace: "up", name: "a", state: "RUNNING"}, doctorMMVM{namespace: "stray", name: "x", state: "QUIT"},
		doctorMMVM{namespace: "stray", name: "y", state: "RUNNING"}, doctorMMVM{namespace: "minimega", name: "z"},
	)
	inv.namespaces = append(inv.namespaces, "empty", "__phenix__")

	tests := map[string]struct {
		stored []doctorExperiment
		why    string
		inv    *doctorInventory
		want   []doctorCheck
	}{
		"drift": {
			stored: stored, inv: inv,
			want: []doctorCheck{
				row(driftExperimentsRow, doctorWarn, "the store marks experiments as running, but minimega has no VMs for "+
					"them, such as after minimega restarted: gone (no namespace), empty (no VMs in its namespace)"),
				row(driftNamespacesRow, doctorWarn,
					"minimega namespaces with VMs that no stored experiment owns: stray (2 VMs), minimega (1 VM)"),
			},
		},
		"in step": {
			stored: []doctorExperiment{{name: "up", running: true}, {name: "stray"}, {name: "minimega"}}, inv: inv,
			want: []doctorCheck{
				row(driftExperimentsRow, doctorPass, "minimega has VMs for each of the 1 experiments that the store "+
					"marks as running"),
				row(driftNamespacesRow, doctorPass, "each minimega namespace with VMs belongs to a stored experiment"),
			},
		},
		"nothing running": {
			inv: clusterInventory(),
			want: []doctorCheck{
				row(driftExperimentsRow, doctorPass, "the store marks no experiment as running"),
				row(driftNamespacesRow, doctorPass, "each minimega namespace with VMs belongs to a stored experiment"),
			},
		},
		"no store": {
			why: "the store file /x does not exist or is empty", inv: inv,
			want: []doctorCheck{
				row(driftExperimentsRow, doctorSkip, "not checked, because the store file /x does not exist or is empty"),
				row(driftNamespacesRow, doctorSkip, "not checked, because the store file /x does not exist or is empty"),
			},
		},
		"no minimega": {
			inv: &doctorInventory{why: "minimega gave no version"},
			want: []doctorCheck{
				row(driftExperimentsRow, doctorSkip, "not checked, because minimega gave no version"),
				row(driftNamespacesRow, doctorSkip, "not checked, because minimega gave no version"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, _ := leftoverInput(t, doctorGoodHost(), tc.stored, tc.inv)
			in.found.experiments.why = tc.why

			if diff := cmp.Diff(tc.want, stateDriftChecks(t.Context(), in)); diff != "" {
				t.Errorf("stateDriftChecks() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// vlansAnswers is a minimega with the namespaces minimega (range 101-110, VLAN 105 on the blacklist) and live (the
// VLANs 101 and 102, and its own range 200-210 that uses 200).
func vlansAnswers() map[string][]*minicli.Response {
	aliases := func(rows ...[]string) []*minicli.Response {
		return []*minicli.Response{{Host: "mm1", Header: []string{"alias", "vlan"}, Tabular: rows}}
	}
	rangeOf := func(rows ...[]string) []*minicli.Response {
		return []*minicli.Response{{Host: "mm1", Header: []string{"min", "max", "next"}, Tabular: rows}}
	}

	return map[string][]*minicli.Response{
		"namespace minimega vlans":           aliases(),
		"namespace minimega vlans range":     rangeOf([]string{"101", "110", "101"}),
		"namespace minimega vlans blacklist": {{Host: "mm1", Header: []string{"vlan"}, Tabular: [][]string{{"105"}}}},
		"namespace live vlans":               aliases([]string{"EXP", "101"}, []string{"MGMT", "102"}, []string{"X", "200"}),
		"namespace live vlans range":         rangeOf([]string{"200", "210", "201"}),
	}
}

// The pinned row warns about VLAN IDs that more than one experiment pins. The free row warns about a stopped
// experiment that cannot get its VLANs from minimega: too few free, a range that overlaps another namespace's, or a
// pinned VLAN in use.
func TestVLANsChecks(t *testing.T) {
	t.Parallel()

	vlans := func(minVLAN, maxVLAN, count int, pinned map[string]int) doctorVLANs {
		return doctorVLANs{min: minVLAN, max: maxVLAN, pinned: pinned, count: count}
	}
	inv := clusterInventory(doctorMMVM{namespace: "minimega"}, doctorMMVM{namespace: "live", name: "a", state: "RUNNING"})

	tests := map[string]struct {
		stored  []doctorExperiment
		why     string
		answers map[string][]*minicli.Response
		want    []doctorCheck
	}{
		"fits": {
			stored: []doctorExperiment{
				{name: "live", running: true, vlans: vlans(200, 210, 3, map[string]int{"X": 200})},
				{name: "small", vlans: vlans(0, 0, 6, nil)},
				{name: "own", vlans: vlans(300, 302, 2, nil)},
			},
			answers: vlansAnswers(),
			want: []doctorCheck{
				row(vlansPinnedRow, doctorPass, "no two stored experiments pin the same VLAN ID"),
				row(vlansFreeRow, doctorPass, "each of the 2 stopped experiments can get its VLANs. 6 of minimega's VLAN "+
					"range 101-110 are free"),
			},
		},
		"problems": {
			stored: []doctorExperiment{
				{name: "a", vlans: vlans(0, 0, 9, map[string]int{"EXP": 101, "B": 150})},
				{name: "b", vlans: vlans(205, 220, 1, map[string]int{"OTHER": 150})},
			},
			answers: vlansAnswers(),
			want: []doctorCheck{
				row(vlansPinnedRow, doctorWarn, "stored experiments pin the same VLAN ID, so they cannot run at the same "+
					"time: VLAN 150 in a (alias B) and b (alias OTHER)"),
				row(vlansFreeRow, doctorWarn, "stopped experiments cannot get their VLANs now: VLAN 101 of a (alias EXP) is "+
					"in use in the namespace live, a needs 7 VLANs from the range 101-110, but only 6 VLANs are free, the "+
					"VLAN range 205-220 of b overlaps the VLAN range 200-210 of the namespace live. 6 of minimega's VLAN "+
					"range 101-110 are free"),
			},
		},
		"no store": {
			why: "the store file /x does not exist or is empty", answers: vlansAnswers(),
			want: []doctorCheck{
				row(vlansPinnedRow, doctorSkip, "not checked, because the store file /x does not exist or is empty"),
				row(vlansFreeRow, doctorSkip, "not checked, because the store file /x does not exist or is empty"),
			},
		},
		"minimega fails": {
			stored:  []doctorExperiment{{name: "a"}},
			answers: map[string][]*minicli.Response{},
			want: []doctorCheck{
				row(vlansPinnedRow, doctorPass, "no two stored experiments pin the same VLAN ID"),
				row(vlansFreeRow, doctorSkip, "not checked, because minimega did not answer namespace minimega vlans: "+
					"invalid command"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, _ := leftoverInput(t, doctorGoodHost(), tc.stored, inv)
			in.found.experiments.why = tc.why
			in.minimega = (&doctorMinimega{answers: tc.answers}).run

			if diff := cmp.Diff(tc.want, vlansChecks(t.Context(), in)); diff != "" {
				t.Errorf("vlansChecks() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// doctorTrunkShow is what ovs-vsctl show prints with the bridge phenix, a VM tap, a GRE tunnel and an internal port.
const doctorTrunkShow = `0b9d3ad4
    Bridge phenix
        Port mega_tap0
            tag: 101
            Interface mega_tap0
        Port phenix
            Interface phenix
                type: internal
        Port mega_tap9
            Interface mega_tap9
                type: gre
                options: {remote_ip="10.0.0.2"}
    Bridge other
        Port eth1
            Interface eth1
    ovs_version: "3.3.0"
`

// bridge-trunk warns in a mesh of more than one node about an experiment bridge with no trunk or tunnel port, and
// names the kind of each trunk it finds. It skips for one node, with the user's words.
func TestBridgeTrunkCheck(t *testing.T) {
	t.Parallel()

	bare := strings.Replace(doctorOVSShow, "    ovs_version", "        Port mega_tap1\n            tag: 5\n"+
		"            Interface mega_tap1\n    Bridge exp2\n        Port mega_tap2\n            tag: 7\n"+
		"            Interface mega_tap2\n    ovs_version", 1)
	trunked := `    Bridge phenix
        Port bond0
            Interface eth2
            Interface eth3
        Port eth1
            Interface eth1
        Port vlan10
            Interface vlan10
        Port enp1
            Interface enp1
                type: system
        Port mega_tap3
            Interface mega_tap3
        Port patch-x
            Interface patch-x
                type: patch
`

	tests := map[string]struct {
		nodes    []string
		meshWhy  string
		show     string
		showErr  error
		settings map[string]string
		want     doctorCheck
	}{
		"tunnel": {
			nodes: []string{"mm1", "node2"}, show: doctorTrunkShow,
			want: row("", doctorPass, "The mesh has 2 nodes. The bridge phenix connects to mega_tap9 (gre tunnel)"),
		},
		"no trunk": {
			nodes: []string{"mm1", "node2", "node3"}, show: bare,
			want: row("", doctorWarn, "the mesh has 3 nodes, but these bridges on this node have no trunk or tunnel "+
				"port, so their VMs cannot reach VMs on other nodes: phenix, exp2"),
		},
		"kinds": {
			nodes: []string{"mm1", "node2"}, show: trunked,
			want: row("", doctorPass, "The mesh has 2 nodes. The bridge phenix connects to bond0 (Open vSwitch bond of "+
				"eth2, eth3), eth1 (physical NIC), vlan10 (VLAN device), enp1 (interface)"),
		},
		"no experiment bridge": {
			nodes: []string{"mm1", "node2"}, show: "    Bridge other\n        Port eth1\n            Interface eth1\n",
			want: row("", doctorSkip, "not checked, because this node has no bridge phenix and no bridge with VM taps yet"),
		},
		"one node": {nodes: []string{"mm1"}, want: row("", doctorSkip, "one node, no trunk needed")},
		"GRE mesh": {
			nodes: []string{"mm1", "node2"}, settings: map[string]string{"use-gre-mesh": "true"},
			want: row("", doctorPass, "the mesh has 2 nodes, and use-gre-mesh is true, so phenix connects the nodes "+
				"with GRE tunnels at each start"),
		},
		"no mesh": {
			meshWhy: "minimega gave no version",
			want:    row("", doctorSkip, "not checked, because minimega gave no version"),
		},
		"show fails": {
			nodes: []string{"mm1", "node2"}, showErr: errors.New("exit status 1"),
			want: row("", doctorSkip, "not checked, because ovs-vsctl show failed (see the openvswitch row)"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			maps.Copy(host, doctorHostFS{
				"/sys/class/net/eth1/device/": "", "/sys/class/net/bond0/bonding/": "", "/proc/net/vlan/vlan10": "",
			})

			in, _ := leftoverInput(t, host, nil, clusterInventory())
			in.found.mesh = &doctorMesh{nodes: tc.nodes, why: tc.meshWhy}
			in.command = doctorCommands(map[string]doctorCommand{"ovs-vsctl --timeout=1 show": {out: tc.show, err: tc.showErr}})

			for key, value := range tc.settings {
				in.settings[key] = resolvedSetting{value: value, text: value, source: sourceFlag}
			}

			if diff := cmp.Diff(tc.want, bridgeTrunkCheck(t.Context(), in)); diff != "" {
				t.Errorf("bridgeTrunkCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// schedule-hosts warns about each schedule that names a node not in the mesh, and compares the names as minimega
// does: without the hostname-suffixes and the leading zeros of numbers.
func TestScheduleHostsCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		stored  []doctorExperiment
		meshWhy string
		want    doctorCheck
	}{
		"unknown node": {
			stored: []doctorExperiment{
				{name: "e1", schedules: map[string]string{"a": "node01", "b": "node9", "c": "ws42-phenix", "d": ""}},
				{name: "e2", schedules: map[string]string{"x": "localhost", "y": "gone"}},
			},
			want: row("", doctorWarn, "VM schedules name cluster nodes that are not in minimega's mesh (2 nodes): "+
				"e1 VM b on node9, e2 VM y on gone"),
		},
		"all known": {
			stored: []doctorExperiment{{name: "e1", schedules: map[string]string{"a": "node1", "b": "mm1"}}},
			want:   row("", doctorPass, "each of the 2 VM schedules names a node in minimega's mesh (2 nodes)"),
		},
		"no schedules": {
			stored: []doctorExperiment{{name: "e1"}},
			want:   row("", doctorPass, "no stored experiment schedules a VM on a cluster node"),
		},
		"no mesh": {
			meshWhy: "minimega did not answer mesh list all within 1s",
			want:    row("", doctorSkip, "not checked, because minimega did not answer mesh list all within 1s"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inv := clusterInventory()
			inv.host = "ws42"
			in, _ := leftoverInput(t, doctorGoodHost(), tc.stored, inv)
			in.settings["hostname-suffixes"] = resolvedSetting{value: "-phenix", text: "-phenix", source: sourceFlag}
			in.found.mesh = &doctorMesh{nodes: []string{"mm1", "node1"}, why: tc.meshWhy}

			if diff := cmp.Diff(tc.want, scheduleHostsCheck(t.Context(), in)); diff != "" {
				t.Errorf("scheduleHostsCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// cc-clients warns about the running VMs that need miniccc and have no client, but not about VMs that started a short
// time ago or are not running. It also warns about clients that minimega logged with another miniccc version after
// their VMs launched.
func TestCCClientsCheck(t *testing.T) {
	t.Parallel()

	up := 10 * time.Minute
	stored := []doctorExperiment{{name: "e1", running: true, vms: []doctorVM{
		{name: "a", cc: "the startup app sends its scripts through miniccc"},
		{name: "b", cc: "the soh app checks it through miniccc"},
		{name: "c", cc: "the soh app checks it through miniccc"},
		{name: "d", cc: "the soh app checks it through miniccc"},
		{name: "e"},
	}}, {name: "off", vms: []doctorVM{{name: "z", cc: "x"}}}}
	inv := clusterInventory(
		doctorMMVM{namespace: "e1", name: "a", state: "RUNNING", uuid: "U-A", uptime: up},
		doctorMMVM{namespace: "e1", name: "b", state: "RUNNING", uuid: "u-b", uptime: up},
		doctorMMVM{namespace: "e1", name: "c", state: "RUNNING", uuid: "u-c", uptime: time.Minute},
		doctorMMVM{namespace: "e1", name: "d", state: "PAUSED", uuid: "u-d", uptime: up},
		doctorMMVM{namespace: "e1", name: "e", state: "RUNNING", uuid: "u-e", uptime: up},
	)
	clients := map[string][]*minicli.Response{"namespace e1 cc clients": {{
		Host: "mm1", Header: []string{"uuid", "hostname", "arch", "os", "ip", "mac"},
		Tabular: [][]string{{"u-a", "a", "amd64", "linux", "[]", "[]"}},
	}}}

	tests := map[string]struct {
		answers map[string][]*minicli.Response
		log     string // the content of minimega's log; "" for no -logfile
		want    doctorCheck
	}{
		"missing client and version": {
			answers: clients,
			log: "2026/10/30 11:52:00 WARN server.go:746: mismatched miniccc version on a: v2.8\n" +
				"2026/10/30 11:55:00 WARN server.go:746: mismatched miniccc version on gone: v1\n",
			want: row("", doctorWarn, "VMs that have no miniccc client in minimega, but phenix needs one: e1 VM b (the "+
				"soh app checks it through miniccc). minimega logged a miniccc version that differs from its own for "+
				"these clients: a (miniccc v2.8, logged 2026/10/30 11:52:00)"),
		},
		"version line from before the launch": {
			answers: clients,
			log:     "2026/10/30 11:40:00 WARN server.go:746: mismatched miniccc version on a: v2.8\n",
			want: row("", doctorWarn, "VMs that have no miniccc client in minimega, but phenix needs one: e1 VM b (the "+
				"soh app checks it through miniccc)"),
		},
		"version line with no time": {
			answers: clients,
			log:     "WARN server.go:746: mismatched miniccc version on a: v2.8\n",
			want: row("", doctorWarn, "VMs that have no miniccc client in minimega, but phenix needs one: e1 VM b (the "+
				"soh app checks it through miniccc). minimega logged a miniccc version that differs from its own for "+
				"these clients: a (miniccc v2.8)"),
		},
		"no log": {
			answers: map[string][]*minicli.Response{"namespace e1 cc clients": {{
				Host: "mm1", Header: []string{"uuid", "hostname"}, Tabular: [][]string{{"u-a", "a"}, {"u-b", "b"}},
			}}},
			want: row("", doctorPass, "each of the 2 running VMs that phenix needs miniccc in has a client. The miniccc "+
				"versions are not checked, because minimega gives no -logfile"),
		},
		"cc clients fails": {
			answers: map[string][]*minicli.Response{},
			want: row("", doctorPass, "no running VM that phenix needs miniccc in has run for 5m0s or more. The clients "+
				"of e1 are not checked, because minimega did not answer namespace e1 cc clients: invalid command"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, dir := leftoverInput(t, doctorGoodHost(), stored, inv)
			in.minimega = (&doctorMinimega{answers: tc.answers}).run

			if tc.log != "" {
				in.found.minimegaArgs["logfile"] = writeTestFile(t, dir, "minimega.log", strings.Repeat("x\n", 10)+tc.log)
			}

			if diff := cmp.Diff(tc.want, ccClientsCheck(t.Context(), in)); diff != "" {
				t.Errorf("ccClientsCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("relative log", func(t *testing.T) {
		t.Parallel()

		in, _ := leftoverInput(t, doctorGoodHost(), stored, inv)
		in.minimega = (&doctorMinimega{answers: clients}).run
		in.found.minimegaArgs["logfile"] = "minimega.log"

		want := row("", doctorWarn, "VMs that have no miniccc client in minimega, but phenix needs one: e1 VM b (the "+
			"soh app checks it through miniccc). The miniccc versions are not checked, because minimega's -logfile "+
			"minimega.log is relative to the directory of minimega, which doctor does not know")
		if diff := cmp.Diff(want, ccClientsCheck(t.Context(), in)); diff != "" {
			t.Errorf("ccClientsCheck() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no answer stops", func(t *testing.T) {
		t.Parallel()

		many := make([]doctorExperiment, 0, 3)
		for _, name := range []string{"e1", "e2", "e3"} {
			many = append(many, doctorExperiment{name: name, running: true})
		}

		in, _ := leftoverInput(t, doctorGoodHost(), many, clusterInventory(
			doctorMMVM{namespace: "e1"}, doctorMMVM{namespace: "e2"}, doctorMMVM{namespace: "e3"}))
		in.timeout = time.Second

		var asked []string
		in.minimega = func(_, command string, _ time.Duration) ([]*minicli.Response, error) {
			asked = append(asked, command)

			return nil, errNoAnswer
		}

		want := row("", doctorPass, "no running VM that phenix needs miniccc in has run for 5m0s or more. The clients "+
			"of e1 are not checked, because minimega did not answer namespace e1 cc clients within 1s. The clients of "+
			"2 more running experiments are not checked")
		if diff := cmp.Diff(want, ccClientsCheck(t.Context(), in)); diff != "" {
			t.Errorf("ccClientsCheck() mismatch (-want +got):\n%s", diff)
		}

		if diff := cmp.Diff([]string{"namespace e1 cc clients"}, asked); diff != "" {
			t.Errorf("ccClientsCheck() commands mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no store", func(t *testing.T) {
		t.Parallel()

		in, _ := leftoverInput(t, doctorGoodHost(), nil, inv)
		in.found.experiments.why = "the store is locked"

		want := row("", doctorSkip, "not checked, because the store is locked")
		if diff := cmp.Diff(want, ccClientsCheck(t.Context(), in)); diff != "" {
			t.Errorf("ccClientsCheck() mismatch (-want +got):\n%s", diff)
		}
	})
}

// The mesh is read once in a run from mesh list all, which minimega answers with a compressed host list.
func TestMinimegaMesh(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		up     bool
		answer []*minicli.Response
		want   *doctorMesh
	}{
		"mesh": {
			up: true, answer: []*minicli.Response{{Host: "mm1", Response: "head,node[1-3]\n"}},
			want: &doctorMesh{nodes: []string{"head", "node1", "node2", "node3"}},
		},
		"bad list": {
			up: true, answer: []*minicli.Response{{Host: "mm1", Response: "node[1-"}},
			want: &doctorMesh{why: "minimega's answer to mesh list all does not parse: unterminated '['"},
		},
		"empty": {
			up: true, answer: []*minicli.Response{{Host: "mm1", Response: ""}},
			want: &doctorMesh{why: "minimega's answer to mesh list all names no node"},
		},
		"no answer": {up: true, want: &doctorMesh{why: "minimega did not answer mesh list all: invalid command"}},
		"down":      {want: &doctorMesh{why: "minimega gave no version"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(newDoctorTestDir(t), nil)
			answers := map[string][]*minicli.Response{}

			if tc.answer != nil {
				answers["mesh list all"] = tc.answer
			}

			in.minimega = (&doctorMinimega{answers: answers}).run
			in.found = &doctorFound{minimegaUp: tc.up}

			got := in.minimegaMesh(t.Context())
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(doctorMesh{})); diff != "" {
				t.Errorf("minimegaMesh() mismatch (-want +got):\n%s", diff)
			}

			if again := in.minimegaMesh(t.Context()); again != got {
				t.Errorf("minimegaMesh() asked minimega again in one run")
			}
		})
	}
}

// Each new row gives the fix for what it found, and none for a skip.
func TestClusterHints(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		hint  func(doctorCheck) string
		check doctorCheck
		want  string
	}{
		"skip": {hint: vlansHint, check: hostSkip("x"), want: ""},
		"open files": {
			hint: minimegaLimitsHint, check: row(limitsFilesRow, doctorWarn, "too few"),
			want: "increase the open files limit of minimega, with LimitNOFILE= in its systemd unit or " +
				"docker run --ulimit nofile=<n>:<n>, then restart minimega",
		},
		"memory": {
			hint: minimegaLimitsHint, check: row(limitsMemoryRow, doctorWarn, "x"),
			want: "stop VMs, or give the VMs less memory in their topologies",
		},
		"drift experiments": {
			hint: stateDriftHint, check: row(driftExperimentsRow, doctorWarn, "x"),
			want: "reset the stored state of each experiment with phenix experiment stop <experiment>",
		},
		"drift namespaces": {
			hint: stateDriftHint, check: row(driftNamespacesRow, doctorWarn, "x"),
			want: "make sure that doctor reads the store of phenix ui. Then kill the VMs of each namespace with " +
				"minimega -e 'clear namespace <namespace>'",
		},
		"vlans": {
			hint: vlansHint, check: row(vlansFreeRow, doctorWarn, "is in use in the namespace x, only 1 VLANs are free"),
			want: "stop the experiment that uses the VLAN, or pin another VLAN ID with phenix vlan alias <experiment> " +
				"<alias> <id> --force. Stop experiments to free VLANs, or set a larger range with phenix vlan range " +
				"<experiment> <min> <max> --force (vlans.min and vlans.max in the spec)",
		},
		"trunk": {
			hint: bridgeTrunkHint, check: row("", doctorWarn, "x"),
			want: "add a trunk port to the bridge on each node with minimega -e 'bridge trunk <bridge> <interface>', " +
				"or set use-gre-mesh to true",
		},
		"schedule": {
			hint: scheduleHostsHint, check: row("", doctorWarn, "x"),
			want: "change the schedules with phenix experiment edit <experiment>, or start minimega on the node and " +
				"connect it to the mesh",
		},
		"cc": {
			hint: ccClientsHint, check: row("", doctorWarn, "differs from its own"),
			want: "copy the miniccc of the same minimega release into the images with phenix image inject-miniexe " +
				"<miniccc> <image>",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.hint(tc.check); got != tc.want {
				t.Errorf("hint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The parsers of /proc and ovs-vsctl output never panic, and meshNodeKey drops only leading zeros, once.
func TestClusterParsersProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		text := rapid.String().Draw(t, "text")

		parseMeminfo(text)
		parseOVSBridges(text)

		if soft, hard, ok := parseOpenFilesLimit(text); ok && (soft < unlimitedOpenFiles || hard < unlimitedOpenFiles) {
			t.Fatalf("parseOpenFilesLimit(%q) = %d, %d", text, soft, hard)
		}

		name := rapid.StringMatching(`[a-z0-9-]{0,12}`).Draw(t, "name")
		key := meshNodeKey(name)

		if meshNodeKey(key) != key {
			t.Fatalf("meshNodeKey(%q) = %q, which changes again", name, key)
		}

		if strings.ReplaceAll(key, "0", "") != strings.ReplaceAll(name, "0", "") {
			t.Fatalf("meshNodeKey(%q) = %q, which changes more than zeros", name, key)
		}
	})
}

// --report redacts the mesh nodes, the host minimega answers from and the nodes that VM schedules name, which the
// checks of the run found.
func TestDoctorReportRedactsWhatChecksFound(t *testing.T) {
	t.Parallel()

	in := fakeDoctorInput(newDoctorTestDir(t), map[string]string{"hostname-suffixes": "-minimega"})
	in.minimega = (&doctorMinimega{answers: map[string][]*minicli.Response{
		"mesh list all": {{Host: "mm7-minimega", Response: "mm7-minimega,node[1-2]"}},
		"namespace":     {{Host: "mm7-minimega", Header: []string{"namespace", "vlans", "active"}}},
	}}).run
	defs := []doctorCheckDef{{
		id: "nodes", group: doctorGroupMinimega, name: "nodes", hint: nil,
		run: func(ctx context.Context, in *doctorInput) []doctorCheck {
			in.found.minimegaUp = true
			in.found.experiments = &doctorExperiments{why: "", list: []doctorExperiment{{
				name: "e1", schedules: map[string]string{"b": "lab-n9.corp.example"},
			}}}

			return []doctorCheck{passCheck(strings.Join(in.minimegaMesh(ctx).nodes, " ") + " " +
				in.minimegaInventory(ctx).host + " e1 VM b on lab-n9.corp.example")}
		},
	}}

	report := runDoctor(t.Context(), in, defs, nil, doctorOptions{output: "text", report: true})

	want := "<host-4>-minimega <host-2> <host-3> <host-4> e1 VM b on <host-1>"
	if got := report.Checks[0].Detail; got != want {
		t.Errorf("runDoctor() detail = %q, want %q", got, want)
	}
}
