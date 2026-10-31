package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

// leftoverInput is an input on host with minimega up, the images directory <dir>/files, the stored experiments
// experiments, and inv as minimega's inventory.
func leftoverInput(t *testing.T, host doctorHostFS, experiments []doctorExperiment, inv *doctorInventory) (*doctorInput, string) {
	t.Helper()

	in := hostInput(t, host, doctorHostTools)
	dir := filepath.Dir(in.text("base-dir.phenix"))

	in.found = &doctorFound{
		minimegaUp:   true,
		minimegaArgs: map[string]string{"filepath": filepath.Join(dir, "files"), "base": filepath.Join(dir, "minimega")},
		experiments:  &doctorExperiments{list: experiments, why: ""},
		inventory:    inv,
	}

	return in, dir
}

// doctorTestInventory is a minimega on the host mm1 that answers with vms, taps and captures of whole bridges.
func doctorTestInventory(vms []doctorMMVM, taps, captures []string) *doctorInventory {
	inv := &doctorInventory{host: "mm1", vms: vms, taps: map[string]bool{}, bridgeCaptures: map[string]bool{}, why: ""}

	for _, tap := range taps {
		inv.taps[tap] = true
	}

	for _, bridge := range captures {
		inv.bridgeCaptures[bridge] = true
	}

	return inv
}

func writeSized(t *testing.T, path string, size int) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("os.MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

// The snapshots row warns about the snapshot files of this head node whose experiment the store does not have. It
// leaves out the files of other head nodes, and a file that can belong to a stored experiment or to a namespace in
// which minimega runs a VM.
func TestDoctorLeftoverSnapshots(t *testing.T) {
	t.Parallel()

	stored := []doctorExperiment{{name: "kept"}}

	tests := map[string]struct {
		files map[string]int
		why   string
		host  string // the head node minimega answers from; empty for none
		vms   []doctorMMVM
		want  func(dir string) doctorCheck
	}{
		"experiment that minimega runs": {
			files: map[string]int{"mm1_live_vm1_snapshot": 1, "mm1_dead_x_snapshot": 2},
			host:  "mm1",
			vms:   []doctorMMVM{{namespace: "live", name: "vm1", state: "RUNNING"}, {namespace: "dead", name: "x", state: "QUIT"}},
			want: func(dir string) doctorCheck {
				return row("", doctorWarn, "1 snapshot files in "+dir+" belong to experiments that the store does not "+
					"have, 2 B in all: mm1_dead_x_snapshot (2 B)")
			},
		},
		"deleted experiment": {
			files: map[string]int{
				"mm1_gone_vm1_snapshot": 2048, "mm1_gone_vm2_snapshot": 10, "mm1_kept_vm1_snapshot": 1,
				"mm1_kept_old_vm_snapshot": 1, "other_gone_vm1_snapshot": 1, "base.qc2": 1,
			},
			host: "mm1",
			want: func(dir string) doctorCheck {
				return row("", doctorWarn, "2 snapshot files in "+dir+" belong to experiments that the store does not "+
					"have, 2.0 KiB in all: mm1_gone_vm1_snapshot (2.0 KiB), mm1_gone_vm2_snapshot (10 B)")
			},
		},
		"none": {
			files: map[string]int{"mm1_kept_vm1_snapshot": 1}, host: "mm1",
			want: func(dir string) doctorCheck {
				return row("", doctorPass, "no snapshot files of deleted experiments in "+dir)
			},
		},
		"head node from this host": {
			files: map[string]int{"doctor-test-host_gone_vm_snapshot": 1},
			want: func(dir string) doctorCheck {
				return row("", doctorWarn, "1 snapshot files in "+dir+" belong to experiments that the store does not "+
					"have, 1 B in all: doctor-test-host_gone_vm_snapshot (1 B)")
			},
		},
		"store not read": {
			why: "another process held the lock of /phenix/store.bdb for 1s", host: "mm1",
			want: func(string) doctorCheck {
				return row("", doctorSkip, "not checked, because another process held the lock of /phenix/store.bdb for 1s")
			},
		},
		"no images directory": {
			host: "mm1",
			want: func(dir string) doctorCheck {
				return row("", doctorSkip, "not checked, because "+dir+" does not exist")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inv := doctorTestInventory(tc.vms, nil, nil)
			inv.host = tc.host

			in, dir := leftoverInput(t, doctorGoodHost(), stored, inv)
			images := filepath.Join(dir, "files")

			if name == "no images directory" {
				images = filepath.Join(dir, "missing")
				in.found.minimegaArgs["filepath"] = images
			}

			for file, size := range tc.files {
				writeSized(t, filepath.Join(images, file), size)
			}

			found := &doctorLeftovers{}

			got := leftoverSnapshotsCheck(t.Context(), in, stored, tc.why, found)
			if diff := cmp.Diff(tc.want(images), got); diff != "" {
				t.Errorf("leftoverSnapshotsCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The files row warns about the base directories and file directories of experiments that the store does not have
// and minimega does not run, with their sizes, and says when it did not measure them in full.
func TestDoctorLeftoverFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		canceled bool
		want     func(base, images string) doctorCheck
	}{
		"deleted experiments": {
			want: func(base, images string) doctorCheck {
				return row("", doctorWarn, "2 directories belong to experiments that the store does not have, 15 B in all: "+
					filepath.Join(base, "gone")+" (10 B), "+filepath.Join(images, "gone2")+" (5 B)")
			},
		},
		"not measured in time": {
			canceled: true,
			want: func(base, images string) doctorCheck {
				return row("", doctorWarn, "2 directories belong to experiments that the store does not have, at least 0 B "+
					"in all (not measured in full within 1s): "+filepath.Join(base, "gone")+" (0 B), "+
					filepath.Join(images, "gone2")+" (0 B)")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			live := []doctorMMVM{{namespace: "live", name: "vm1", state: "RUNNING"}}
			in, dir := leftoverInput(t, doctorGoodHost(), nil, doctorTestInventory(live, nil, nil))
			base := filepath.Join(dir, "phenix", "experiments")
			images := filepath.Join(dir, "files")
			stored := []doctorExperiment{{name: "kept"}, {name: "moved", baseDir: filepath.Join(base, "custom")}}

			writeSized(t, filepath.Join(base, "kept", "mm_files", "kept.mm"), 3)
			writeSized(t, filepath.Join(base, "custom", "x"), 3)
			writeSized(t, filepath.Join(base, "gone", "mm_files", "gone.mm"), 6)
			writeSized(t, filepath.Join(base, "gone", "startup", "a"), 4)
			writeSized(t, filepath.Join(images, "gone2", "files", "a.pcap"), 5)
			writeSized(t, filepath.Join(images, "kept", "files", "b.pcap"), 5)
			writeSized(t, filepath.Join(base, "live", "mm_files", "live.mm"), 5)
			writeSized(t, filepath.Join(images, "live", "files", "c.pcap"), 5)
			writeSized(t, filepath.Join(images, "ubuntu", "noble.qc2"), 5)

			ctx := t.Context()
			if tc.canceled {
				canceled, cancel := context.WithCancel(ctx)
				cancel()

				ctx = canceled
			}

			got := leftoverFilesCheck(ctx, in, stored, "", &doctorLeftovers{})
			if diff := cmp.Diff(tc.want(base, images), got); diff != "" {
				t.Errorf("leftoverFilesCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The mounts row warns about the VM file system mounts under mount-dir whose experiment the store does not have and
// minimega does not run, or whose VM minimega does not run, and says when it cannot ask minimega.
func TestDoctorLeftoverMounts(t *testing.T) {
	t.Parallel()

	stored := []doctorExperiment{{name: "kept"}}
	vms := []doctorMMVM{
		{namespace: "kept", name: "vm1", state: "RUNNING"}, {namespace: "kept", name: "vm2", state: "QUIT"},
		{namespace: "live", name: "vm1", state: "RUNNING"},
	}

	tests := map[string]struct {
		mounts []string // relative to mount-dir
		inv    *doctorInventory
		want   func(dir string) doctorCheck
	}{
		"left mounts": {
			mounts: []string{"gone/vm1", "kept/vm1", "kept/vm2", "live/vm1"},
			inv:    doctorTestInventory(vms, nil, nil),
			want: func(dir string) doctorCheck {
				return row("", doctorWarn, "VM file system mounts under "+dir+" with no running VM: "+
					filepath.Join(dir, "gone/vm1")+" (the store has no experiment gone), "+
					filepath.Join(dir, "kept/vm2")+" (minimega does not run the VM vm2)")
			},
		},
		"minimega down": {
			mounts: []string{"gone/vm1", "kept/vm2"},
			inv:    &doctorInventory{why: "minimega gave no version"},
			want: func(dir string) doctorCheck {
				return row("", doctorWarn, "VM file system mounts under "+dir+" with no running VM: "+
					filepath.Join(dir, "gone/vm1")+" (the store has no experiment gone). It did not check whether "+
					"the VMs of 1 mounts run, because minimega gave no version")
			},
		},
		"running VMs": {
			mounts: []string{"kept/vm1"},
			inv:    doctorTestInventory(vms, nil, nil),
			want: func(dir string) doctorCheck {
				return row("", doctorPass, "1 VM file system mounts under "+dir+", each of an experiment that the store "+
					"has or minimega runs")
			},
		},
		"none": {
			inv:  doctorTestInventory(vms, nil, nil),
			want: func(dir string) doctorCheck { return row("", doctorPass, "no VM file system mounts under "+dir) },
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			in, dir := leftoverInput(t, host, stored, tc.inv)
			mounts := filepath.Join(dir, "phenix", "mounts")

			lines := []string{"proc /proc proc rw 0 0"}
			for _, m := range tc.mounts {
				lines = append(lines, "127.0.0.1 "+filepath.Join(mounts, m)+" 9p rw 0 0")
			}

			host["/proc/mounts"] = strings.Join(lines, "\n") + "\n"
			host.apply(&in.env)

			got := leftoverMountsCheck(t.Context(), in, stored, "")
			if diff := cmp.Diff(tc.want(mounts), got); diff != "" {
				t.Errorf("leftoverMountsCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// doctorLeftoverShow is ovs-vsctl show with minimega taps on the bridge phenix: one a VM owns, one whose device is
// gone, one no VM owns, and the tap of a capture of the whole bridge.
const doctorLeftoverShow = `0b9d
    Bridge phenix
        Port phenix
            Interface phenix
                type: internal
        Port mega_tap1
            tag: 101
            Interface mega_tap1
        Port mega_tap4
            Interface mega_tap4
                error: "could not open network device mega_tap4 (No such device)"
        Port mega_tap5
            Interface mega_tap5
        Port mega_tap3
            Interface mega_tap3
    ovs_version: "3.3.0"
`

// The taps and mirrors rows warn about the minimega taps on this host and the bridges that no VM, host tap or capture
// owns, the ports whose device is gone, and the mirrors no capture owns. Each part says why it is not checked.
func TestDoctorLeftoverNetwork(t *testing.T) {
	t.Parallel()

	const (
		show    = "ovs-vsctl --timeout=1 show"
		mirrors = "ovs-vsctl --timeout=1 --columns=_uuid,name list Mirror"
		bridges = "ovs-vsctl --timeout=1 --columns=name,mirrors list Bridge"
	)

	commands := map[string]doctorCommand{
		show: {out: doctorLeftoverShow},
		mirrors: {out: "_uuid               : 1a\nname                : \"mirror-mega_tap3\"\n\n" +
			"_uuid               : 2b\nname                : \"mirror-mega_tap9\"\n\n" +
			"_uuid               : 3c\nname                : \"mirror-mega_tap1\"\n"},
		bridges: {out: "name                : phenix\nmirrors             : [1a]\n\nname                : other\n" +
			"mirrors             : [2b, 3c]\n"},
	}
	vmTaps := doctorTestInventory([]doctorMMVM{{namespace: "e", name: "a", state: "RUNNING"}}, []string{"mega_tap1"},
		[]string{"phenix"})

	ports := "Open vSwitch ports (bridge and port) whose network device is gone: phenix mega_tap4. Open vSwitch ports " +
		"(bridge and port) that no VM or host tap owns: phenix mega_tap5"

	tests := map[string]struct {
		os        string
		container bool
		devices   []string // in /sys/class/net; nil for mega_tap1, mega_tap2, mega_tap3 and eth0
		inv       *doctorInventory
		tools     []string
		commands  map[string]doctorCommand
		want      []doctorCheck
	}{
		"container with its own network namespace": {
			container: true, devices: []string{"eth0", "lo"}, inv: vmTaps, commands: commands,
			want: []doctorCheck{
				row("", doctorWarn, ports+". The taps on this host are not checked, because this container shows no "+
					"minimega tap, Open vSwitch bridge or ovs-system device, so it can have its own network namespace"),
				row("", doctorWarn, "Open vSwitch mirrors that no capture owns: mirror-mega_tap9 on bridge other"),
			},
		},
		"container on the host network": {
			container: true, devices: []string{"eth0", "phenix", "mega_tap7"}, inv: vmTaps, commands: commands,
			want: []doctorCheck{
				row("", doctorWarn, "taps that no VM or host tap owns: mega_tap7. "+ports),
				row("", doctorWarn, "Open vSwitch mirrors that no capture owns: mirror-mega_tap9 on bridge other"),
			},
		},
		"leftovers": {
			inv: vmTaps, commands: commands,
			want: []doctorCheck{
				row("", doctorWarn, "taps that no VM or host tap owns: mega_tap2. Open vSwitch ports (bridge and port) "+
					"whose network device is gone: phenix mega_tap4. Open vSwitch ports (bridge and port) that no VM or "+
					"host tap owns: phenix mega_tap5"),
				row("", doctorWarn, "Open vSwitch mirrors that no capture owns: mirror-mega_tap9 on bridge other"),
			},
		},
		"no ovs-vsctl": {
			inv: vmTaps, tools: []string{"qemu-nbd"},
			want: []doctorCheck{
				row("", doctorWarn, "taps that no VM or host tap owns: mega_tap2, mega_tap3. The Open vSwitch ports are "+
					"not checked, because ovs-vsctl is not on PATH"),
				row("", doctorSkip, "not checked, because ovs-vsctl is not on PATH"),
			},
		},
		"ovs-vsctl list fails": {
			inv: doctorTestInventory(nil, []string{"mega_tap1", "mega_tap2", "mega_tap5"}, []string{"phenix"}),
			commands: map[string]doctorCommand{
				show: {out: doctorLeftoverShow}, mirrors: {err: errors.New("exit status 1")},
			},
			want: []doctorCheck{
				row("", doctorWarn, "Open vSwitch ports (bridge and port) whose network device is gone: phenix mega_tap4. "+
					"The taps that no VM owns are not checked, because a capture of a whole bridge runs, and its tap is "+
					"not known: ovs-vsctl list Mirror: exit status 1"),
				row("", doctorSkip, "not checked, because ovs-vsctl list Mirror: exit status 1"),
			},
		},
		"every tap owned": {
			inv: doctorTestInventory(nil, []string{"mega_tap1", "mega_tap2", "mega_tap3", "mega_tap4", "mega_tap5"},
				[]string{"other"}),
			commands: map[string]doctorCommand{
				show: {out: strings.ReplaceAll(doctorLeftoverShow, "error: ", "status: ")}, mirrors: commands[mirrors],
				bridges: commands[bridges],
			},
			want: []doctorCheck{
				row("", doctorPass, "every minimega tap on this host and on the Open vSwitch bridges has an owner"),
				row("", doctorPass, "no minimega mirror without a capture on the Open vSwitch bridges"),
			},
		},
		"minimega down": {
			inv: &doctorInventory{why: "minimega gave no version"},
			want: []doctorCheck{
				row("", doctorSkip, "not checked, because minimega gave no version"),
				row("", doctorSkip, "not checked, because minimega gave no version"),
			},
		},
		"not linux": {
			os: "darwin", inv: vmTaps,
			want: []doctorCheck{
				row("", doctorSkip, "not checked, because minimega runs only on Linux"),
				row("", doctorSkip, "not checked, because minimega runs only on Linux"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()

			devices := tc.devices
			if devices == nil {
				devices = []string{"mega_tap1", "mega_tap2", "mega_tap3", "eth0"}
			}

			for _, device := range devices {
				host[sysClassNet+"/"+device+"/"] = ""
			}

			if tc.container {
				host["/.dockerenv"] = ""
			}

			in, _ := leftoverInput(t, host, nil, tc.inv)
			if tc.tools != nil {
				in.env.lookPath = lookPathIn(map[string]string{"qemu-nbd": "/usr/bin/qemu-nbd"})
			}

			if tc.os != "" {
				in.system.os = tc.os
			}

			in.command = doctorCommands(tc.commands)

			got := leftoverNetworkChecks(t.Context(), in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("leftoverNetworkChecks() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The inventory lists minimega's namespaces, then asks each for its VMs, host taps and captures. A namespace name
// that minimega does not accept is not asked, and a failure says which command failed.
func TestDoctorMinimegaInventory(t *testing.T) {
	t.Parallel()

	answers := map[string][]*minicli.Response{
		"namespace": {{Host: "ws42-minimega", Header: []string{"namespace", "vlans", "active"},
			Tabular: [][]string{{"exp1", "101-200", "false"}, {"bad name", "", "false"}}}},
		"namespace exp1 vm info": {
			{Host: "ws42", Header: []string{"name", "state", "pid", "tap", "uuid", "memory", "uptime"},
				Tabular: [][]string{
					{"a", "RUNNING", "300", "[mega_tap1, mega_tap2]", "u-a", "2048", "1h2m3.5s"},
					{"b", "QUIT", "0", "[]", "u-b", "N/A", ""},
				}},
			{Host: "node2", Header: []string{"name", "state", "pid", "tap"}, Tabular: [][]string{{"c", "PAUSED", "7", "[mega_tap0]"}}},
		},
		"namespace exp1 tap": {{Host: "ws42", Header: []string{"bridge", "tap", "vlan"},
			Tabular: [][]string{{"phenix", "mega_tap9", "EXP"}}}},
		"namespace exp1 capture": {{Host: "ws42", Header: []string{"bridge", "type", "interface", "mode", "compress", "path"},
			Tabular: [][]string{{"phenix", "pcap", "a:0", "", "", "/x.pcap"}, {"other", "pcap", "", "", "", "/y.pcap"},
				{"third", "netflow", "", "raw", "false", "/z"}}}},
	}

	tests := map[string]struct {
		up      bool
		answers map[string][]*minicli.Response
		want    *doctorInventory
	}{
		"inventory": {
			up: true, answers: answers,
			want: &doctorInventory{
				host: "ws42", local: "ws42-minimega", namespaces: []string{"exp1"},
				vms: []doctorMMVM{
					{
						namespace: "exp1", name: "a", state: "RUNNING", pid: 300, host: "ws42", uuid: "u-a", memory: 2048,
						uptime: time.Hour + 2*time.Minute + 3500*time.Millisecond,
					},
					{namespace: "exp1", name: "b", state: "QUIT", host: "ws42", uuid: "u-b"},
					{namespace: "exp1", name: "c", state: "PAUSED", pid: 7, host: "node2"},
				},
				taps:           map[string]bool{"mega_tap0": true, "mega_tap1": true, "mega_tap2": true, "mega_tap9": true},
				bridgeCaptures: map[string]bool{"other": true},
			},
		},
		"minimega down": {
			want: &doctorInventory{taps: map[string]bool{}, bridgeCaptures: map[string]bool{}, why: "minimega gave no version"},
		},
		"a command fails": {
			up: true, answers: map[string][]*minicli.Response{"namespace": answers["namespace"]},
			want: &doctorInventory{
				host: "ws42", local: "ws42-minimega", namespaces: []string{"exp1"}, taps: map[string]bool{},
				bridgeCaptures: map[string]bool{}, why: "minimega did not answer namespace exp1 vm info: invalid command",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(newDoctorTestDir(t), map[string]string{"hostname-suffixes": "-minimega,-phenix"})
			mm := &doctorMinimega{answers: tc.answers}
			in.minimega = mm.run
			in.found = &doctorFound{minimegaUp: tc.up}

			got := in.minimegaInventory(t.Context())
			opts := cmp.AllowUnexported(doctorInventory{}, doctorMMVM{})

			if diff := cmp.Diff(tc.want, got, opts); diff != "" {
				t.Errorf("minimegaInventory() mismatch (-want +got):\n%s", diff)
			}

			if again := in.minimegaInventory(t.Context()); again != got {
				t.Errorf("minimegaInventory() asked minimega again in one run")
			}
		})
	}
}

// Each leftovers row gives the fix for what it found, and none for a skip.
func TestDoctorLeftoversHint(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		check doctorCheck
		want  string
	}{
		"snapshots": {
			check: row(leftoverSnapshotRow, doctorWarn, "1 snapshot files"),
			want:  "delete each file that the row names, with rm <file>. No stored experiment uses it",
		},
		"taps and ports": {
			check: row(leftoverTapsRow, doctorWarn, orphanTapPhrase+": mega_tap2. "+orphanPortPhrase+" (bridge and port)"),
			want:  "delete each port with ovs-vsctl del-port <bridge> <port>. Delete each tap with ip link delete <tap>",
		},
		"mirrors": {
			check: row(leftoverMirrorsRow, doctorWarn, "x"),
			want:  "remove each mirror with ovs-vsctl -- --id=@m get mirror <mirror> -- remove bridge <bridge> mirrors @m",
		},
		"skip": {check: row(leftoverMountsRow, doctorSkip, "not checked"), want: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := leftoversHint(tc.check); got != tc.want {
				t.Errorf("leftoversHint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The stored experiments keep the VMs phenix launches, with their drive images, snapshot setting and why phenix needs
// miniccc in them, and leave out external and do-not-boot nodes. They keep the VLAN range, the pinned VLANs and the
// VLAN count, and the schedules. An experiment that does not decode keeps its name.
func TestDoctorDecodeExperiment(t *testing.T) {
	t.Parallel()

	yes, no := true, false

	exp := types.NewExperiment(store.ConfigMetadata{Name: "e1"})
	exp.Spec.SetExperimentName("e1")
	exp.Spec.SetBaseDir("/phenix/experiments/e1")
	exp.Spec.SetTopology(&v1.TopologySpec{NodesF: []*v1.Node{
		{
			GeneralF:  &v1.General{HostnameF: "a", SnapshotF: &no},
			HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: "a.qc2"}, {ImageF: "/data/b.qc2"}}},
			NetworkF:  &v1.Network{InterfacesF: []*v1.Interface{{VLANF: "EXP"}, {VLANF: "MGMT"}}},
		},
		{
			GeneralF:     &v1.General{HostnameF: "b", SnapshotF: &yes},
			HardwareF:    &v1.Hardware{OSTypeF: "linux", DrivesF: []*v1.Drive{{ImageF: "c.qc2"}}},
			NetworkF:     &v1.Network{InterfacesF: []*v1.Interface{{VLANF: "OTHER"}}},
			AnnotationsF: map[string]any{"phenix/startup-via-cc": true},
			DelayF:       &v1.Delay{C2F: []v1.C2Delay{{HostnameF: "e"}}},
		},
		{GeneralF: &v1.General{HostnameF: "c", DoNotBootF: &yes}, HardwareF: &v1.Hardware{}},
		{ExternalF: &yes, GeneralF: &v1.General{HostnameF: "d"}},
		{GeneralF: &v1.General{HostnameF: "e"}, HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: "e.qc2"}}}},
		{GeneralF: &v1.General{HostnameF: "f"}, HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: "skip.qc2"}}}},
	}})
	exp.Spec.SetScenario(&v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{
		{NameF: "soh", MetadataF: map[string]any{"skipHosts": []any{"a", "skip.qc2"}}},
	}})
	exp.Spec.SetSchedule(map[string]string{"a": "node1"})

	if err := exp.Spec.SetVLANRange(200, 300, false); err != nil {
		t.Fatalf("SetVLANRange() error = %v", err)
	}

	exp.Spec.VLANs().SetAliases(map[string]int{"EXP": 0, "MGMT": 250})
	exp.Status.SetStartTime(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC).Format(time.RFC3339))

	config := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "e1"},
		Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	}

	want := doctorExperiment{
		name: "e1", baseDir: "/phenix/experiments/e1", running: true,
		vms: []doctorVM{
			{name: "a", snapshot: false, images: []string{"a.qc2", "/data/b.qc2"}},
			{name: "b", snapshot: true, images: []string{"c.qc2"}, cc: "the startup app sends its scripts through miniccc"},
			{name: "e", images: []string{"e.qc2"}, cc: "the start of b waits for its miniccc client"},
			{name: "f", images: []string{"skip.qc2"}},
		},
		vlans:     doctorVLANs{min: 200, max: 300, pinned: map[string]int{"MGMT": 250}, count: 3},
		schedules: map[string]string{"a": "node1"},
	}

	opts := cmp.AllowUnexported(doctorExperiment{}, doctorVM{}, doctorVLANs{})

	if diff := cmp.Diff(want, decodeDoctorExperiment(config), opts); diff != "" {
		t.Errorf("decodeDoctorExperiment() mismatch (-want +got):\n%s", diff)
	}

	bad := store.Config{Version: "phenix.sandia.gov/v9", Kind: "Experiment", Metadata: store.ConfigMetadata{Name: "e2"}}
	if diff := cmp.Diff(doctorExperiment{name: "e2"}, decodeDoctorExperiment(bad), opts); diff != "" {
		t.Errorf("decodeDoctorExperiment() of a bad config mismatch (-want +got):\n%s", diff)
	}
}

// minimegaList splits every list minimega's vm info prints back into its items.
func TestDoctorMinimegaListProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		items := rapid.SliceOf(rapid.StringMatching(`[a-z0-9_]{1,12}`)).Draw(t, "items")
		printed := "[" + strings.Join(items, ", ") + "]"

		if diff := cmp.Diff(items, minimegaList(printed), cmpEmptyAsNil); diff != "" {
			t.Fatalf("minimegaList(%q) mismatch (-want +got):\n%s", printed, diff)
		}
	})
}

// parseOVSRecords reads back each record of ovs-vsctl list, with quoted and plain values, and never panics.
func TestDoctorOVSRecordsProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 4).Draw(t, "records")
		want := make([]map[string]string, 0, n)

		var out strings.Builder

		for i := range n {
			record := map[string]string{
				"_uuid": rapid.StringMatching(`[0-9a-f]{8}`).Draw(t, "uuid"),
				"name":  rapid.StringMatching(`[a-z][a-z0-9_-]{0,12}`).Draw(t, "name"),
			}
			want = append(want, record)

			if i > 0 {
				out.WriteString("\n")
			}

			out.WriteString("_uuid               : " + record["_uuid"] + "\n")
			out.WriteString("name                : \"" + record["name"] + "\"\n")
		}

		if diff := cmp.Diff(want, parseOVSRecords(out.String()), cmpEmptyAsNil); diff != "" {
			t.Fatalf("parseOVSRecords() mismatch (-want +got):\n%s", diff)
		}

		_ = parseOVSRecords(rapid.String().Draw(t, "any"))
	})
}

// cmpEmptyAsNil makes an empty slice equal to nil.
//
//nolint:gochecknoglobals // a cmp option
var cmpEmptyAsNil = cmp.FilterValues(func(a, b any) bool {
	return isEmptySlice(a) && isEmptySlice(b)
}, cmp.Comparer(func(any, any) bool { return true }))

func isEmptySlice(v any) bool {
	switch s := v.(type) {
	case []string:
		return len(s) == 0
	case []map[string]string:
		return len(s) == 0
	}

	return false
}

// doctorBytes gives the largest unit that keeps the number at 1 or more.
func TestDoctorBytes(t *testing.T) {
	t.Parallel()

	tests := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 30: "5.0 GiB", 1 << 62: "4.0 EiB"}

	for n, want := range tests {
		if got := doctorBytes(n); got != want {
			t.Errorf("doctorBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

// A store file that does not exist or is empty is a reason to read no experiments, so the leftovers rows skip and
// do not name the files of every experiment.
func TestDoctorStoredExperimentsNoStore(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		write bool // an empty store file
	}{
		"no store file":    {write: false},
		"empty store file": {write: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)
			path := filepath.Join(dir, "store.bdb")

			if tc.write {
				writeSized(t, path, 0)
			}

			in := fakeDoctorInput(dir, nil)
			in.found = &doctorFound{}

			list, why := in.storedExperiments(t.Context())
			if list != nil || why != "the store file "+path+" does not exist or is empty" {
				t.Errorf("storedExperiments() = %v, %q, want no experiments and a reason", list, why)
			}

			rows := leftoversChecks(t.Context(), in)
			for _, r := range rows[:3] {
				if r.Status != doctorSkip {
					t.Errorf("leftoversChecks() row %s = %s, want skip", r.Name, r.Status)
				}
			}
		})
	}
}
