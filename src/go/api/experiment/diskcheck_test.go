package experiment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/common"
	dct "phenix/util/diskchain/diskchaintest"
	"phenix/util/mm"
)

// diskVM is a VM of a disk check test: its hostname, drive images and CD-ROM image, and its snapshot, do_not_boot
// and external settings (nil leaves them unset).
type diskVM struct {
	name             string
	drives           []string
	cdrom            string
	snapshot, noBoot *bool
	external         bool
}

func boolPtr(b bool) *bool { return &b }

// diskSpec is an experiment spec of the VMs, deployed in mode with the schedules, with the defaults Init sets.
//
//nolint:ireturn // the spec as a stored experiment holds it
func diskSpec(mode string, schedules map[string]string, vms ...diskVM) ifaces.ExperimentSpec {
	exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})

	nodes := make([]*v1.Node, 0, len(vms))

	for _, vm := range vms {
		node := &v1.Node{
			GeneralF:  &v1.General{HostnameF: vm.name, SnapshotF: vm.snapshot, DoNotBootF: vm.noBoot},
			HardwareF: &v1.Hardware{},
			AdvancedF: map[string]string{},
		}

		for _, image := range vm.drives {
			node.HardwareF.DrivesF = append(node.HardwareF.DrivesF, &v1.Drive{ImageF: image})
		}

		if vm.cdrom != "" {
			node.AdvancedF["cdrom"] = vm.cdrom
		}

		if vm.external {
			node.ExternalF = boolPtr(true)
		}

		nodes = append(nodes, node)
	}

	exp.Spec.SetTopology(&v1.TopologySpec{NodesF: nodes})
	exp.Spec.SetDeployMode(mode)
	exp.Spec.SetSchedule(schedules)

	// the node defaults a stored experiment has, such as snapshot: true
	_ = exp.Spec.Init()

	return exp.Spec
}

// diskEnv is a check environment of the files directory dir, seen by phenix, on a cluster of hosts whose head node is
// "head", or one whose hosts phenix cannot list when hostsErr is set.
func diskEnv(dir string, hosts []string, hostsErr error) checkEnv {
	cluster := make(mm.Hosts, 0, len(hosts))
	for _, name := range hosts {
		cluster = append(cluster, mm.Host{Name: name, Headnode: name == "head"})
	}

	return checkEnv{
		filesDir:     dir,
		filesDirSeen: true,
		isHeadnode:   func(host string) (bool, error) { return host == "head", nil },
		hosts:        func(context.Context) (mm.Hosts, error) { return cluster, hostsErr },
		timeout:      10 * time.Second,
	}
}

// diskTexts gives the warnings and problems as text.
func diskTexts(warnings []error, problems []DiskProblem) ([]string, []string) {
	w := make([]string, 0, len(warnings))
	p := make([]string, 0, len(problems))

	for _, warning := range warnings {
		w = append(w, warning.Error())
	}

	for _, problem := range problems {
		p = append(p, problem.String())
	}

	return w, p
}

// The check refuses a start for each image qemu cannot open where the VM runs, and only warns where phenix cannot
// tell: it reads the head node's files, which minimega snapshots from, and copies from inside its files directory.
//
//nolint:maintidx // the table
func TestCheckDisksClassifies(t *testing.T) {
	t.Parallel()

	off := boolPtr(false)
	unknown := errors.New("minimega is busy")

	tests := []struct {
		name         string
		mode         string
		schedules    map[string]string
		hosts        []string
		hostsErr     error
		unseen       bool // phenix cannot see the files directory
		setup        func(t *testing.T, images, outside string)
		vms          func(images, outside string) []diskVM
		wantWarnings []string
		wantProblems []string
	}{
		{
			name:         "a snapshot of a missing image is refused, wherever the VM runs",
			vms:          func(_, outside string) []diskVM { return []diskVM{{name: "a", drives: []string{outside + "/a.qc2"}}} },
			wantProblems: []string{"VM a: disk {outside}/a.qc2: disk image {outside}/a.qc2 is missing"},
		},
		{
			name: "a missing backing file inside the files directory is refused",
			setup: func(t *testing.T, images, _ string) {
				t.Helper()
				dct.WriteQcow2(t, images+"/kali.qc2", dct.Qcow2{Backing: "ro_kali.qc2"})
			},
			vms: func(string, string) []diskVM {
				return []diskVM{{name: "host-01", drives: []string{"kali.qc2"}, snapshot: off}}
			},
			wantProblems: []string{
				"VM host-01: disk kali.qc2: backing file {images}/ro_kali.qc2 is missing",
			},
		},
		{
			name: "outside the files directory, a VM deployed only to the head node is refused",
			mode: string(common.DeployModeOnlyHeadnode), hosts: []string{"head", "c1"},
			vms: func(_, outside string) []diskVM {
				return []diskVM{{name: "a", drives: []string{outside + "/a.qc2"}, snapshot: off}}
			},
			wantProblems: []string{"VM a: disk {outside}/a.qc2: disk image {outside}/a.qc2 is missing"},
		},
		{
			name: "outside the files directory, a VM scheduled to the head node is refused",
			mode: string(common.DeployModeNoHeadnode), schedules: map[string]string{"a": "head"}, hosts: []string{"head", "c1"},
			vms: func(_, outside string) []diskVM {
				return []diskVM{{name: "a", drives: []string{outside + "/a.qc2"}, snapshot: off}}
			},
			wantProblems: []string{"VM a: disk {outside}/a.qc2: disk image {outside}/a.qc2 is missing"},
		},
		{
			name: "outside the files directory, a cluster of the head node alone is refused",
			mode: string(common.DeployModeNoHeadnode), hosts: []string{"head"},
			vms: func(_, outside string) []diskVM {
				return []diskVM{{name: "a", drives: []string{outside + "/a.qc2"}, snapshot: off}}
			},
			wantProblems: []string{"VM a: disk {outside}/a.qc2: disk image {outside}/a.qc2 is missing"},
		},
		{
			name: "outside the files directory, a VM scheduled to another node only warns",
			mode: string(common.DeployModeAll), schedules: map[string]string{"a": "c1"}, hosts: []string{"head", "c1"},
			vms: func(_, outside string) []diskVM {
				return []diskVM{{name: "a", drives: []string{outside + "/a.qc2"}, snapshot: off}}
			},
			wantWarnings: []string{
				"VM a: disk {outside}/a.qc2: disk image {outside}/a.qc2 is missing. " +
					"phenix checks only the head node, and the VM may run on another node",
			},
		},
		{
			name: "outside the files directory, a VM the cluster may place on another node only warns",
			mode: string(common.DeployModeNoHeadnode), hosts: []string{"head", "c1"},
			vms: func(_, outside string) []diskVM {
				return []diskVM{{name: "a", drives: []string{outside + "/a.qc2"}, snapshot: off}}
			},
			wantWarnings: []string{
				"VM a: disk {outside}/a.qc2: disk image {outside}/a.qc2 is missing. " +
					"phenix checks only the head node, and the VM may run on another node",
			},
		},
		{
			name: "outside the files directory, cluster hosts phenix cannot list only warn",
			mode: string(common.DeployModeAll), hostsErr: unknown,
			vms: func(_, outside string) []diskVM {
				return []diskVM{{name: "a", drives: []string{outside + "/a.qc2"}, snapshot: off}}
			},
			wantWarnings: []string{
				"VM a: disk {outside}/a.qc2: disk image {outside}/a.qc2 is missing. " +
					"phenix could not tell which node runs the VM: minimega is busy",
			},
		},
		{
			name:   "a files directory phenix cannot see makes a missing file a warning",
			unseen: true,
			vms:    func(string, string) []diskVM { return []diskVM{{name: "a", drives: []string{"a.qc2"}}} },
			wantWarnings: []string{
				"VM a: disk a.qc2: disk image {images}/a.qc2 is missing. " +
					"phenix cannot see minimega's files directory {images}, so it did not check the image",
			},
		},
		{
			name:   "a loop phenix can read is refused even when it cannot see the files directory",
			unseen: true,
			setup: func(t *testing.T, images, _ string) {
				t.Helper()
				dct.WriteQcow2(t, images+"/a.qc2", dct.Qcow2{Backing: "a.qc2"})
			},
			vms:          func(string, string) []diskVM { return []diskVM{{name: "a", drives: []string{"a.qc2"}}} },
			wantProblems: []string{"VM a: disk a.qc2: backing chain loops back to {images}/a.qc2"},
		},
		{
			name: "external and do_not_boot VMs are left out",
			vms: func(string, string) []diskVM {
				return []diskVM{
					{name: "ext", drives: []string{"gone.qc2"}, external: true},
					{name: "off", drives: []string{"gone.qc2"}, noBoot: boolPtr(true)},
				}
			},
		},
		{
			name: "a missing CD-ROM image is refused, and an ISO drive is only opened",
			setup: func(t *testing.T, images, _ string) {
				t.Helper()
				dct.WriteQcow2(t, images+"/tools.iso", dct.Qcow2{Backing: "gone.qc2"})
			},
			vms: func(string, string) []diskVM {
				return []diskVM{{name: "a", drives: []string{"tools.iso"}, cdrom: "install.iso", snapshot: off}}
			},
			wantProblems: []string{"VM a: CD-ROM install.iso: CD-ROM image {images}/install.iso is missing"},
		},
		{
			name: "an image phenix does not follow is one warning naming the VMs sharing it",
			setup: func(t *testing.T, images, _ string) {
				t.Helper()
				dct.WriteFile(t, images+"/win.vmdk", []byte("KDMV"))
			},
			vms: func(string, string) []diskVM {
				return []diskVM{{name: "a", drives: []string{"win.vmdk"}}, {name: "b", drives: []string{"win.vmdk"}}}
			},
			wantWarnings: []string{
				"disk win.vmdk of VMs a, b: {images}/win.vmdk is a vmdk image, so phenix does not check past it",
			},
		},
		{
			name: "images qemu refuses for their version, feature bits or encryption are refused",
			setup: func(t *testing.T, images, _ string) {
				t.Helper()
				v4 := dct.Qcow2{}.Bytes()
				v4[7] = 4
				dct.WriteFile(t, images+"/v4.qc2", v4)
				dct.WriteQcow2(t, images+"/bits.qc2", dct.Qcow2{Features: 1 << 5})
				dct.WriteQcow2(t, images+"/luks.qc2", dct.Qcow2{Encryption: 2})
				dct.WriteQcow2(t, images+"/kali.qc2", dct.Qcow2{Backing: "old.qcow", BackingFormat: "qcow"})
				dct.WriteQcow(t, images+"/old.qcow", dct.Qcow{Encryption: 1})
			},
			vms: func(string, string) []diskVM {
				return []diskVM{{name: "a", drives: []string{"v4.qc2", "bits.qc2", "luks.qc2", "kali.qc2"}, snapshot: off}}
			},
			wantProblems: []string{
				"VM a: disk v4.qc2: {images}/v4.qc2 is a qcow2 version 4 image, which qemu cannot open",
				"VM a: disk bits.qc2: {images}/bits.qc2 uses qcow2 features that qemu does not know " +
					"(incompatible feature bits 0x20)",
				"VM a: disk luks.qc2: {images}/luks.qc2 uses LUKS encryption, and phenix cannot give qemu its key",
				"VM a: disk kali.qc2: {images}/old.qcow uses the old qcow AES encryption, which qemu does not open for VMs",
			},
		},
		{
			name: "a qcow version 1 image and a dirty image only warn, and version 2 and zstd images pass",
			setup: func(t *testing.T, images, _ string) {
				t.Helper()
				dct.WriteQcow2(t, images+"/kali.qc2", dct.Qcow2{Backing: "ro_kali.qcow", BackingFormat: "qcow"})
				dct.WriteQcow(t, images+"/ro_kali.qcow", dct.Qcow{})
				dct.WriteQcow2(t, images+"/dirty.qc2", dct.Qcow2{Features: 1})
				dct.WriteQcow2(t, images+"/v2.qc2", dct.Qcow2{Version: 2, Backing: "zstd.qc2"})
				dct.WriteQcow2(t, images+"/zstd.qc2", dct.Qcow2{Zstd: true})
			},
			vms: func(string, string) []diskVM {
				return []diskVM{
					{name: "a", drives: []string{"kali.qc2", "v2.qc2"}},
					{name: "b", drives: []string{"dirty.qc2"}, snapshot: off},
				}
			},
			wantWarnings: []string{
				"disk dirty.qc2 of VM b: qemu did not close {images}/dirty.qc2 cleanly. Run qemu-img check -r all on it",
				"disk kali.qc2 of VM a: {images}/ro_kali.qcow is a qcow version 1 image, an old format that some qemu " +
					"builds cannot open. Convert it to qcow2",
			},
		},
		{
			name: "a corrupt image is refused only where qemu opens it read/write",
			setup: func(t *testing.T, images, _ string) {
				t.Helper()
				dct.WriteQcow2(t, images+"/kali.qc2", dct.Qcow2{Features: 1 << 1})
				dct.WriteQcow2(t, images+"/top.qc2", dct.Qcow2{Backing: "kali.qc2"})
			},
			vms: func(string, string) []diskVM {
				return []diskVM{
					{name: "a", drives: []string{"kali.qc2"}, snapshot: off},
					{name: "b", drives: []string{"kali.qc2"}},
					{name: "c", drives: []string{"top.qc2"}, snapshot: off},
				}
			},
			wantWarnings: []string{
				"disk kali.qc2 of VM b: qemu marked {images}/kali.qc2 as corrupt. Copy it, then run qemu-img check -r all on it",
				"disk top.qc2 of VM c: qemu marked {images}/kali.qc2 as corrupt. Copy it, then run qemu-img check -r all on it",
			},
			wantProblems: []string{"VM a: disk kali.qc2: qemu marked {images}/kali.qc2 as corrupt"},
		},
		{
			name: "outside the files directory, an image qemu refuses on another node only warns",
			mode: string(common.DeployModeAll), schedules: map[string]string{"a": "c1"}, hosts: []string{"head", "c1"},
			setup: func(t *testing.T, _, outside string) {
				t.Helper()
				dct.WriteQcow2(t, outside+"/a.qc2", dct.Qcow2{Encryption: 1})
			},
			vms: func(_, outside string) []diskVM {
				return []diskVM{{name: "a", drives: []string{outside + "/a.qc2"}, snapshot: off}}
			},
			wantWarnings: []string{
				"VM a: disk {outside}/a.qc2: {outside}/a.qc2 uses the old qcow AES encryption, which qemu does not open " +
					"for VMs. phenix checks only the head node, and the VM may run on another node",
			},
		},
		{
			name: "problems are sorted by VM, then drive",
			setup: func(t *testing.T, images, _ string) {
				t.Helper()
				dct.WriteFile(t, images+"/ok.img", []byte("raw"))
			},
			vms: func(string, string) []diskVM {
				return []diskVM{
					{name: "b", drives: []string{"ok.img", "b1.qc2"}},
					{name: "a", drives: []string{"a0.qc2", "a1.qc2"}, cdrom: "a.iso"},
				}
			},
			wantProblems: []string{
				"VM a: disk a0.qc2: disk image {images}/a0.qc2 is missing",
				"VM a: disk a1.qc2: disk image {images}/a1.qc2 is missing",
				"VM a: CD-ROM a.iso: CD-ROM image {images}/a.iso is missing",
				"VM b: disk b1.qc2: disk image {images}/b1.qc2 is missing",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				root    = t.TempDir()
				images  = root + "/images"
				outside = root + "/outside"
				fill    = strings.NewReplacer("{images}", images, "{outside}", outside)
			)

			if tc.setup != nil {
				tc.setup(t, images, outside)
			}

			env := diskEnv(images, tc.hosts, tc.hostsErr)
			env.filesDirSeen = !tc.unseen

			spec := diskSpec(tc.mode, tc.schedules, tc.vms(images, outside)...)
			gotWarnings, gotProblems := diskTexts(checkDisks(t.Context(), spec, diskScope{}, env))

			var wantWarnings, wantProblems []string
			for _, w := range tc.wantWarnings {
				wantWarnings = append(wantWarnings, fill.Replace(w))
			}

			for _, p := range tc.wantProblems {
				wantProblems = append(wantProblems, fill.Replace(p))
			}

			if diff := cmp.Diff(wantWarnings, gotWarnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("checkDisks warnings mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(wantProblems, gotProblems, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("checkDisks problems mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A problem names the image as the topology does, its full path and the missing file, for the REST error body.
func TestCheckDisksProblemFields(t *testing.T) {
	t.Parallel()

	images := t.TempDir()
	dct.WriteQcow2(t, images+"/sub/top.qc2", dct.Qcow2{Backing: "../ro_top.qc2"})

	spec := diskSpec("", nil, diskVM{name: "a", drives: []string{"sub/top.qc2"}})
	_, problems := checkDisks(t.Context(), spec, diskScope{}, diskEnv(images, nil, nil))

	want := []DiskProblem{{
		VM: "a", Drive: "disk", Image: "sub/top.qc2", Path: images + "/sub/top.qc2", Missing: images + "/ro_top.qc2",
		Problem: "backing file " + images + "/ro_top.qc2 is missing",
	}}

	if diff := cmp.Diff(want, problems, cmpopts.IgnoreUnexported(DiskProblem{})); diff != "" {
		t.Errorf("checkDisks problems mismatch (-want +got):\n%s", diff)
	}
}

// A problem of an image qemu refuses names no missing file.
func TestCheckDisksRefusedFields(t *testing.T) {
	t.Parallel()

	images := t.TempDir()
	dct.WriteQcow2(t, images+"/kali.qc2", dct.Qcow2{Encryption: 2})

	spec := diskSpec("", nil, diskVM{name: "a", drives: []string{"kali.qc2"}})
	_, problems := checkDisks(t.Context(), spec, diskScope{}, diskEnv(images, nil, nil))

	want := []DiskProblem{{
		VM: "a", Drive: "disk", Image: "kali.qc2", Path: images + "/kali.qc2", Missing: "",
		Problem: images + "/kali.qc2 uses LUKS encryption, and phenix cannot give qemu its key",
	}}

	if diff := cmp.Diff(want, problems, cmpopts.IgnoreUnexported(DiskProblem{})); diff != "" {
		t.Errorf("checkDisks problems mismatch (-want +got):\n%s", diff)
	}
}

// Only the VMs a bulk action names are checked.
func TestCheckDisksOnlyNamedVMs(t *testing.T) {
	t.Parallel()

	images := t.TempDir()
	spec := diskSpec("", nil, diskVM{name: "a", drives: []string{"a.qc2"}}, diskVM{name: "b", drives: []string{"b.qc2"}})

	_, problems := diskTexts(checkDisks(t.Context(), spec, diskScope{vms: map[string]bool{"b": true}}, diskEnv(images, nil, nil)))

	if diff := cmp.Diff([]string{"VM b: disk b.qc2: disk image " + images + "/b.qc2 is missing"}, problems); diff != "" {
		t.Errorf("checkDisks problems mismatch (-want +got):\n%s", diff)
	}
}

// A VM marked do not boot is checked when it booted before the pre-start apps ran, as the startup app marks a VM
// whose first disk image is missing; a VM marked so before them is not.
func TestCheckDisksBootingBeforeApps(t *testing.T) {
	t.Parallel()

	images := t.TempDir()
	on := boolPtr(true)
	spec := diskSpec("", nil,
		diskVM{name: "a", drives: []string{"a.qc2"}, noBoot: on}, diskVM{name: "b", drives: []string{"b.qc2"}, noBoot: on})

	scope := diskScope{booting: bootingVMs(diskSpec("", nil,
		diskVM{name: "a", drives: []string{"a.qc2"}}, diskVM{name: "b", drives: []string{"b.qc2"}, noBoot: on}))}
	_, problems := diskTexts(checkDisks(t.Context(), spec, scope, diskEnv(images, nil, nil)))

	if diff := cmp.Diff([]string{"VM a: disk a.qc2: disk image " + images + "/a.qc2 is missing"}, problems); diff != "" {
		t.Errorf("checkDisks problems mismatch (-want +got):\n%s", diff)
	}
}

// A check out of time starts anyway, with a warning naming every image it did not read.
func TestCheckDisksOutOfTime(t *testing.T) {
	t.Parallel()

	images := t.TempDir()
	env := diskEnv(images, nil, nil)
	env.timeout = 0 // the deadline has passed before any image is read

	spec := diskSpec("", nil, diskVM{name: "a", drives: []string{"a.qc2", "/b.qc2"}})
	warnings, problems := diskTexts(checkDisks(t.Context(), spec, diskScope{}, env))

	want := []string{
		"did not finish checking 2 disk images within 0s, so the experiment starts without checking /b.qc2, " + images + "/a.qc2",
	}

	if diff := cmp.Diff(want, warnings); diff != "" {
		t.Errorf("checkDisks warnings mismatch (-want +got):\n%s", diff)
	}

	if len(problems) != 0 {
		t.Errorf("checkDisks problems = %q, want none", problems)
	}
}

// The error counts disks and VMs, lists each disk of each VM, and stops listing after 20.
func TestBrokenDisksError(t *testing.T) {
	t.Parallel()

	problem := func(vm, image string) DiskProblem {
		return DiskProblem{VM: vm, Drive: "disk", Image: image, Problem: "disk image /i/" + image + " is missing"}
	}

	many := make([]DiskProblem, 0, 25)
	for i := range 25 {
		many = append(many, problem(fmt.Sprintf("vm%02d", i), "x.qc2"))
	}

	var manyItems strings.Builder
	for i := range 20 {
		fmt.Fprintf(&manyItems, "\n\t* VM vm%02d: disk x.qc2: disk image /i/x.qc2 is missing", i)
	}

	tests := []struct {
		name     string
		problems []DiskProblem
		want     string
	}{
		{
			name:     "one disk",
			problems: []DiskProblem{problem("a", "a.qc2")},
			want: "qemu cannot open 1 disk on 1 VM: 1 error occurred:" +
				"\n\t* VM a: disk a.qc2: disk image /i/a.qc2 is missing",
		},
		{
			name:     "two disks of one VM",
			problems: []DiskProblem{problem("a", "a.qc2"), problem("a", "b.qc2")},
			want: "qemu cannot open 2 disks on 1 VM: 2 errors occurred:" +
				"\n\t* VM a: disk a.qc2: disk image /i/a.qc2 is missing" +
				"\n\t* VM a: disk b.qc2: disk image /i/b.qc2 is missing",
		},
		{
			name:     "more than 20",
			problems: many,
			want: "qemu cannot open 25 disks on 25 VMs: 25 errors occurred:" +
				manyItems.String() + "\n\t* and 5 more",
		},
	}

	for _, tc := range tests {
		err := error(&BrokenDisksError{Problems: tc.problems})

		if got := err.Error(); got != tc.want {
			t.Errorf("%s: Error() =\n%s\nwant\n%s", tc.name, got, tc.want)
		}

		if !errors.Is(fmt.Errorf("starting: %w", err), ErrBrokenDisks) {
			t.Errorf("%s: the error does not match ErrBrokenDisks", tc.name)
		}
	}
}

// The warn mode and a dry run turn the problems into warnings; the error mode refuses.
func TestCheckOutcome(t *testing.T) {
	t.Parallel()

	var (
		warning  = errors.New("disk win.vmdk of VM a: not checked")
		problems = []DiskProblem{{VM: "a", Drive: "disk", Image: "a.qc2", Problem: "disk image /i/a.qc2 is missing"}}
		item     = "VM a: disk a.qc2: disk image /i/a.qc2 is missing"
	)

	tests := []struct {
		name         string
		opts         checkOptions
		wantWarnings []string
		wantBroken   bool
	}{
		{name: "error refuses", opts: checkOptions{mode: common.DiskCheckError}, wantWarnings: []string{warning.Error()}, wantBroken: true},
		{name: "warn warns", opts: checkOptions{mode: common.DiskCheckWarn}, wantWarnings: []string{warning.Error(), item}},
		{
			name: "a dry run warns", opts: checkOptions{mode: common.DiskCheckError, asWarnings: true},
			wantWarnings: []string{warning.Error(), item},
		},
	}

	for _, tc := range tests {
		warnings, err := tc.opts.outcome([]error{warning}, problems)

		got, _ := diskTexts(warnings, nil)
		if diff := cmp.Diff(tc.wantWarnings, got); diff != "" {
			t.Errorf("%s: warnings mismatch (-want +got):\n%s", tc.name, diff)
		}

		if broken := errors.Is(err, ErrBrokenDisks); broken != tc.wantBroken {
			t.Errorf("%s: error = %v, want broken disks %t", tc.name, err, tc.wantBroken)
		}
	}
}

// With the mode off nothing is read, not even minimega's files directory.
func TestCheckDisksOff(t *testing.T) {
	t.Parallel()

	spec := diskSpec("", nil, diskVM{name: "a", drives: []string{"/gone.qc2"}})

	warnings, err := CheckDisks(t.Context(), spec, CheckWithMode(common.DiskCheckOff))
	if warnings != nil || err != nil {
		t.Errorf("CheckDisks = %v, %v, want nothing", warnings, err)
	}
}

// A check whose caller gave up returns the caller's error, not a warning that the start goes on, in every mode that
// checks.
func TestCheckDisksCancelled(t *testing.T) {
	t.Parallel()

	spec := diskSpec("", nil, diskVM{name: "a", drives: []string{"/gone.qc2"}})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, mode := range []common.DiskCheck{common.DiskCheckError, common.DiskCheckWarn} {
		warnings, err := CheckDisks(ctx, spec, CheckWithMode(mode), CheckAsWarnings(true))
		if warnings != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("CheckDisks(%s, cancelled) = %v, %v, want no warnings and context.Canceled", mode, warnings, err)
		}
	}
}

// A restart's check before the stop refuses only when no user app can change the disks first.
func TestHasUserApps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		apps []*v2.ScenarioApp
		want bool
	}{
		{name: "no scenario apps"},
		{name: "only default apps", apps: []*v2.ScenarioApp{{NameF: "ntp"}, {NameF: "startup"}}},
		{name: "a disabled user app", apps: []*v2.ScenarioApp{{NameF: "soh", DisabledF: true}}},
		{name: "a user app", apps: []*v2.ScenarioApp{{NameF: "ntp"}, {NameF: "soh"}}, want: true},
	}

	for _, tc := range tests {
		spec := diskSpec("", nil)
		spec.SetScenario(&v2.ScenarioSpec{AppsF: tc.apps})

		if got := hasUserApps(spec); got != tc.want {
			t.Errorf("%s: hasUserApps = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// The VMs in the problems are exactly those booting from a missing image, in sorted order, for any topology.
func TestCheckDisksProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		images := t.TempDir()
		defer func() { _ = os.RemoveAll(images) }()

		present := rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"p", "q", "r", "s"}), 0, 4, rapid.ID[string]).Draw(rt, "present")
		for _, name := range present {
			dct.WriteFile(t, filepath.Join(images, name+".img"), []byte("raw"))
		}

		count := rapid.IntRange(0, 6).Draw(rt, "vms")

		var (
			vms  = make([]diskVM, 0, count)
			want []string
		)

		for i := range count {
			vm := diskVM{name: fmt.Sprintf("vm%d", i)}
			vm.drives = rapid.SliceOfN(rapid.SampledFrom([]string{"p", "q", "r", "s"}), 0, 3).Draw(rt, "drives")

			broken := false

			for j, drive := range vm.drives {
				broken = broken || !slices.Contains(present, drive)
				vm.drives[j] = drive + ".img"
			}

			vm.noBoot = boolPtr(rapid.Bool().Draw(rt, "do not boot"))
			if broken && !*vm.noBoot {
				want = append(want, vm.name)
			}

			vms = append(vms, vm)
		}

		_, problems := checkDisks(t.Context(), diskSpec("", nil, vms...), diskScope{}, diskEnv(images, nil, nil))

		var got []string
		for _, p := range problems {
			if !slices.Contains(got, p.VM) {
				got = append(got, p.VM)
			}
		}

		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("VMs with problems mismatch (-want +got):\n%s", diff)
		}

		if !slices.IsSortedFunc(problems, func(a, b DiskProblem) int {
			return strings.Compare(a.VM, b.VM)*10 + a.index - b.index
		}) {
			rt.Fatalf("problems are not sorted: %+v", problems)
		}
	})
}
