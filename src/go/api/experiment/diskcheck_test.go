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
	"phenix/util/notes"
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

// A problem names the image as the topology does and its full path, for the REST error body, and the missing file
// only when a file is missing.
func TestCheckDisksProblemFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		image string
		setup func(t *testing.T, images string)
		want  func(images string) DiskProblem
	}{
		{
			name:  "a missing backing file",
			image: "sub/top.qc2",
			setup: func(t *testing.T, images string) {
				t.Helper()
				dct.WriteQcow2(t, images+"/sub/top.qc2", dct.Qcow2{Backing: "../ro_top.qc2"})
			},
			want: func(images string) DiskProblem {
				return DiskProblem{
					VM: "a", Drive: "disk", Image: "sub/top.qc2", Path: images + "/sub/top.qc2", Missing: images + "/ro_top.qc2",
					Problem: "backing file " + images + "/ro_top.qc2 is missing",
				}
			},
		},
		{
			name:  "an image qemu refuses",
			image: "kali.qc2",
			setup: func(t *testing.T, images string) {
				t.Helper()
				dct.WriteQcow2(t, images+"/kali.qc2", dct.Qcow2{Encryption: 2})
			},
			want: func(images string) DiskProblem {
				return DiskProblem{
					VM: "a", Drive: "disk", Image: "kali.qc2", Path: images + "/kali.qc2", Missing: "",
					Problem: images + "/kali.qc2 uses LUKS encryption, and phenix cannot give qemu its key",
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			images := t.TempDir()
			tc.setup(t, images)

			spec := diskSpec("", nil, diskVM{name: "a", drives: []string{tc.image}})
			_, problems := checkDisks(t.Context(), spec, diskScope{}, diskEnv(images, nil, nil))

			if diff := cmp.Diff([]DiskProblem{tc.want(images)}, problems, cmpopts.IgnoreUnexported(DiskProblem{})); diff != "" {
				t.Errorf("checkDisks problems mismatch (-want +got):\n%s", diff)
			}
		})
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

	problems := func(n int) []DiskProblem {
		many := make([]DiskProblem, 0, n)
		for i := range n {
			many = append(many, problem(fmt.Sprintf("vm%02d", i), "x.qc2"))
		}

		return many
	}

	items := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "\n\t* VM vm%02d: disk x.qc2: disk image /i/x.qc2 is missing", i)
		}

		return b.String()
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
			name:     "a CD-ROM",
			problems: []DiskProblem{{VM: "a", Drive: "cdrom", Image: "a.iso", Problem: "CD-ROM image /i/a.iso is missing"}},
			want: "qemu cannot open 1 disk on 1 VM: 1 error occurred:" +
				"\n\t* VM a: CD-ROM a.iso: CD-ROM image /i/a.iso is missing",
		},
		{
			name:     "exactly 20",
			problems: problems(20),
			want:     "qemu cannot open 20 disks on 20 VMs: 20 errors occurred:" + items(20),
		},
		{
			name:     "21",
			problems: problems(21),
			want:     "qemu cannot open 21 disks on 21 VMs: 21 errors occurred:" + items(20) + "\n\t* and 1 more",
		},
		{
			name:     "more than 20",
			problems: problems(25),
			want:     "qemu cannot open 25 disks on 25 VMs: 25 errors occurred:" + items(20) + "\n\t* and 5 more",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := error(&BrokenDisksError{Problems: tc.problems})

			if diff := cmp.Diff(tc.want, err.Error()); diff != "" {
				t.Errorf("Error() mismatch (-want +got):\n%s", diff)
			}

			if !errors.Is(fmt.Errorf("starting: %w", err), ErrBrokenDisks) {
				t.Error("the error does not match ErrBrokenDisks")
			}

			if errors.Is(err, context.Canceled) || errors.Is(err, ErrFileNotFound) {
				t.Error("the error matches an error other than ErrBrokenDisks")
			}
		})
	}
}

// The warn mode and a dry run turn the problems into warnings; the error mode refuses. Without problems no mode
// refuses.
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
		warnings     []error
		problems     []DiskProblem
		wantWarnings []string
		wantBroken   bool
	}{
		{
			name: "error refuses", opts: checkOptions{mode: common.DiskCheckError}, warnings: []error{warning},
			problems: problems, wantWarnings: []string{warning.Error()}, wantBroken: true,
		},
		{
			name: "warn warns", opts: checkOptions{mode: common.DiskCheckWarn}, warnings: []error{warning},
			problems: problems, wantWarnings: []string{warning.Error(), item},
		},
		{
			name: "a dry run warns", opts: checkOptions{mode: common.DiskCheckError, asWarnings: true},
			warnings: []error{warning}, problems: problems, wantWarnings: []string{warning.Error(), item},
		},
		{
			name: "error without problems passes the warnings", opts: checkOptions{mode: common.DiskCheckError},
			warnings: []error{warning}, wantWarnings: []string{warning.Error()},
		},
		{name: "nothing found", opts: checkOptions{mode: common.DiskCheckError}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warnings, err := tc.opts.outcome(tc.warnings, tc.problems)

			got, _ := diskTexts(warnings, nil)
			if diff := cmp.Diff(tc.wantWarnings, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("warnings mismatch (-want +got):\n%s", diff)
			}

			if broken := errors.Is(err, ErrBrokenDisks); broken != tc.wantBroken || (err != nil) != tc.wantBroken {
				t.Errorf("error = %v, want broken disks %t", err, tc.wantBroken)
			}

			var bde *BrokenDisksError
			if errors.As(err, &bde) {
				if diff := cmp.Diff(tc.problems, bde.Problems, cmpopts.IgnoreUnexported(DiskProblem{})); diff != "" {
					t.Errorf("BrokenDisksError problems mismatch (-want +got):\n%s", diff)
				}
			}
		})
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
		name   string
		noSpec bool
		apps   []*v2.ScenarioApp
		want   bool
	}{
		{name: "no spec", noSpec: true},
		{name: "no scenario apps"},
		{name: "only default apps", apps: []*v2.ScenarioApp{{NameF: "ntp"}, {NameF: "startup"}}},
		{name: "a disabled user app", apps: []*v2.ScenarioApp{{NameF: "soh", DisabledF: true}}},
		{name: "a user app", apps: []*v2.ScenarioApp{{NameF: "ntp"}, {NameF: "soh"}}, want: true},
		{
			name: "a disabled and an enabled user app", apps: []*v2.ScenarioApp{{NameF: "a", DisabledF: true}, {NameF: "b"}},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var spec ifaces.ExperimentSpec
			if !tc.noSpec {
				spec = diskSpec("", nil)
				spec.SetScenario(&v2.ScenarioSpec{AppsF: tc.apps})
			}

			if got := hasUserApps(spec); got != tc.want {
				t.Errorf("hasUserApps = %t, want %t", got, tc.want)
			}
		})
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

// CheckVMs, CheckWithMode, CheckAsWarnings and checkBooting set their options; CheckAsWarnings never turns a dry run
// back off.
func TestCheckOptions(t *testing.T) {
	t.Parallel()

	booting := map[string]bool{"a": true}

	tests := []struct {
		name string
		opts []CheckOption
		want checkOptions
	}{
		{name: "none"},
		{name: "a mode", opts: []CheckOption{CheckWithMode(common.DiskCheckWarn)}, want: checkOptions{mode: common.DiskCheckWarn}},
		{name: "a dry run", opts: []CheckOption{CheckAsWarnings(true)}, want: checkOptions{asWarnings: true}},
		{
			name: "a dry run stays one", opts: []CheckOption{CheckAsWarnings(true), CheckAsWarnings(false)},
			want: checkOptions{asWarnings: true},
		},
		{name: "not a dry run", opts: []CheckOption{CheckAsWarnings(false)}},
		{
			name: "named VMs, repeated", opts: []CheckOption{CheckVMs("a", "b", "a")},
			want: checkOptions{vms: map[string]bool{"a": true, "b": true}},
		},
		{name: "no VMs named checks none", opts: []CheckOption{CheckVMs()}, want: checkOptions{vms: map[string]bool{}}},
		{
			name: "the last VMs named win", opts: []CheckOption{CheckVMs("a"), CheckVMs("b")},
			want: checkOptions{vms: map[string]bool{"b": true}},
		},
		{name: "VMs booting before the apps", opts: []CheckOption{checkBooting(booting)}, want: checkOptions{booting: booting}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got checkOptions
			for _, opt := range tc.opts {
				opt(&got)
			}

			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(checkOptions{})); diff != "" {
				t.Errorf("options mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// diskcheckLoop writes a qcow2 image at path whose backing chain loops back to it, which qemu refuses wherever phenix
// cannot see minimega's files directory.
func diskcheckLoop(t *testing.T, path string) {
	t.Helper()

	dct.WriteQcow2(t, path, dct.Qcow2{Backing: filepath.Base(path)})
}

// CheckDisks checks only the VMs CheckVMs names, and CheckDisksBeforeStop refuses only a restart that no user app
// can change the disks of first.
func TestCheckDisksWithOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		beforeStop   bool
		userApp      bool
		opts         []CheckOption
		wantWarnings []string
		wantProblems []string
	}{
		{name: "every VM", wantProblems: []string{"a", "b"}},
		{name: "only the named VM", opts: []CheckOption{CheckVMs("b")}, wantProblems: []string{"b"}},
		{name: "no VM named", opts: []CheckOption{CheckVMs()}},
		{name: "warn", opts: []CheckOption{CheckWithMode(common.DiskCheckWarn)}, wantWarnings: []string{"a", "b"}},
		{name: "before a stop with no user app", beforeStop: true, wantProblems: []string{"a", "b"}},
		{name: "before a stop with a user app", beforeStop: true, userApp: true, wantWarnings: []string{"a", "b"}},
		{
			name: "before a stop, only the named VM", beforeStop: true, opts: []CheckOption{CheckVMs("a")},
			wantProblems: []string{"a"},
		},
		{name: "a user app does not change a start's check", userApp: true, wantProblems: []string{"a", "b"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			diskcheckLoop(t, dir+"/a.qc2")
			diskcheckLoop(t, dir+"/b.qc2")

			spec := diskSpec("", nil,
				diskVM{name: "a", drives: []string{dir + "/a.qc2"}}, diskVM{name: "b", drives: []string{dir + "/b.qc2"}})
			if tc.userApp {
				spec.SetScenario(&v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{NameF: "soh"}}})
			}

			check := CheckDisks
			if tc.beforeStop {
				check = CheckDisksBeforeStop
			}

			warnings, err := check(t.Context(), spec, append([]CheckOption{CheckWithMode(common.DiskCheckError)}, tc.opts...)...)

			var gotWarnings, gotProblems []string

			for _, w := range warnings {
				gotWarnings = append(gotWarnings, strings.TrimPrefix(strings.SplitN(w.Error(), ":", 2)[0], "VM "))
			}

			var bde *BrokenDisksError
			if errors.As(err, &bde) {
				for _, p := range bde.Problems {
					gotProblems = append(gotProblems, p.VM)
				}
			} else if err != nil {
				t.Fatalf("check error = %v, want nil or a *BrokenDisksError", err)
			}

			if diff := cmp.Diff(tc.wantWarnings, gotWarnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("VMs warned of mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantProblems, gotProblems, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("VMs refused mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBootingVMs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec ifaces.ExperimentSpec
		want map[string]bool
	}{
		{name: "no spec", want: map[string]bool{}},
		{name: "no nodes", spec: diskSpec("", nil), want: map[string]bool{}},
		{
			name: "VMs not marked do not boot",
			spec: diskSpec("", nil,
				diskVM{name: "unset"}, diskVM{name: "boots", noBoot: boolPtr(false)}, diskVM{name: "off", noBoot: boolPtr(true)}),
			want: map[string]bool{"unset": true, "boots": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, bootingVMs(tc.spec)); diff != "" {
				t.Errorf("bootingVMs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// collectDisks maps each image to its uses in VM order, skipping drives without an image, and marks a path open only
// when every use of it is a CD-ROM or an ISO drive.
func TestCollectDisks(t *testing.T) {
	t.Parallel()

	off := boolPtr(false)
	spec := diskSpec("", nil,
		diskVM{name: "a", drives: []string{"base.qc2", "", "tools.ISO"}, cdrom: "install.iso"},
		diskVM{name: "b", drives: []string{"/abs/../shared.qc2"}, cdrom: "tools.ISO", snapshot: off},
		diskVM{name: "c", drives: []string{"install.iso"}, snapshot: off},
		diskVM{name: "d", drives: []string{"skipped.qc2"}, external: true},
	)

	uses, openOnly := collectDisks(spec, diskScope{}, "/files")

	wantUses := map[string][]diskUse{
		"/files/base.qc2": {{vm: "a", drive: driveDisk, image: "base.qc2", index: 0, snapshot: true, readOnly: true}},
		"/files/tools.ISO": {
			{vm: "a", drive: driveDisk, image: "tools.ISO", index: 2, snapshot: false, readOnly: true},
			{vm: "b", drive: driveCDROM, image: "tools.ISO", index: 1, snapshot: false, readOnly: true},
		},
		"/files/install.iso": {
			{vm: "a", drive: driveCDROM, image: "install.iso", index: 3, snapshot: false, readOnly: true},
			{vm: "c", drive: driveDisk, image: "install.iso", index: 0, snapshot: false, readOnly: false},
		},
		"/shared.qc2": {{vm: "b", drive: driveDisk, image: "/abs/../shared.qc2", index: 0, snapshot: false, readOnly: false}},
	}
	if diff := cmp.Diff(wantUses, uses, cmp.AllowUnexported(diskUse{})); diff != "" {
		t.Errorf("collectDisks uses mismatch (-want +got):\n%s", diff)
	}

	wantOpen := map[string]bool{"/files/base.qc2": false, "/files/tools.ISO": true, "/files/install.iso": true, "/shared.qc2": false}
	if diff := cmp.Diff(wantOpen, openOnly); diff != "" {
		t.Errorf("collectDisks open-only mismatch (-want +got):\n%s", diff)
	}
}

func TestDiskcheckWords(t *testing.T) {
	t.Parallel()

	t.Run("countOf", func(t *testing.T) {
		t.Parallel()

		for n, want := range map[int]string{0: "0 disks", 1: "1 disk", 2: "2 disks", 21: "21 disks"} {
			if got := countOf(n, "disk"); got != want {
				t.Errorf("countOf(%d, disk) = %q, want %q", n, got, want)
			}
		}
	})

	t.Run("listOf", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			items []string
			limit int
			want  string
		}{
			{items: nil, limit: 2, want: ""},
			{items: []string{"a"}, limit: 2, want: "a"},
			{items: []string{"a", "b"}, limit: 2, want: "a, b"},
			{items: []string{"a", "b", "c"}, limit: 2, want: "a, b and 1 more"},
			{items: []string{"a", "b", "c", "d"}, limit: 0, want: " and 4 more"},
		}

		for _, tc := range tests {
			if got := listOf(tc.items, tc.limit); got != tc.want {
				t.Errorf("listOf(%q, %d) = %q, want %q", tc.items, tc.limit, got, tc.want)
			}
		}
	})

	t.Run("driveLabel", func(t *testing.T) {
		t.Parallel()

		for drive, want := range map[string]string{driveDisk: "disk", driveCDROM: "CD-ROM", "": "disk", "floppy": "disk"} {
			if got := driveLabel(drive); got != want {
				t.Errorf("driveLabel(%q) = %q, want %q", drive, got, want)
			}
		}
	})

	t.Run("derefBool", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			b           *bool
			unset, want bool
		}{
			{b: nil, unset: true, want: true},
			{b: nil, unset: false, want: false},
			{b: boolPtr(true), unset: false, want: true},
			{b: boolPtr(false), unset: true, want: false},
		} {
			if got := derefBool(tc.b, tc.unset); got != tc.want {
				t.Errorf("derefBool(%v, %t) = %t, want %t", tc.b, tc.unset, got, tc.want)
			}
		}
	})

	t.Run("fullPath", func(t *testing.T) {
		t.Parallel()

		for image, want := range map[string]string{
			"a.qc2": "/files/a.qc2", "sub/../a.qc2": "/files/a.qc2", "/x//y/../a.qc2": "/x/a.qc2", "": "/files",
		} {
			if got := fullPath("/files", image); got != want {
				t.Errorf("fullPath(/files, %q) = %q, want %q", image, got, want)
			}
		}
	})

	t.Run("labels", func(t *testing.T) {
		t.Parallel()

		use := diskUse{vm: "a", drive: driveCDROM, image: "x.iso"}
		if got, want := use.label(), "VM a: CD-ROM x.iso"; got != want {
			t.Errorf("diskUse.label() = %q, want %q", got, want)
		}

		p := DiskProblem{VM: "a", Drive: driveDisk, Image: "a.qc2", Problem: "it is gone"}
		if got, want := p.String(), "VM a: disk a.qc2: it is gone"; got != want {
			t.Errorf("DiskProblem.String() = %q, want %q", got, want)
		}
	})
}

// A warning about a shared image names each VM once, at most five of them.
func TestSharedWarning(t *testing.T) {
	t.Parallel()

	uses := func(vms ...string) []diskUse {
		out := make([]diskUse, 0, len(vms))
		for _, vm := range vms {
			out = append(out, diskUse{vm: vm, drive: driveDisk, image: "w.vmdk"})
		}

		return out
	}

	tests := map[string]struct {
		uses []diskUse
		want string
	}{
		"one VM":               {uses: uses("a"), want: "disk w.vmdk of VM a: why"},
		"one VM, two drives":   {uses: uses("a", "a"), want: "disk w.vmdk of VM a: why"},
		"two VMs":              {uses: uses("a", "b", "a"), want: "disk w.vmdk of VMs a, b: why"},
		"more than five VMs":   {uses: uses("a", "b", "c", "d", "e", "f", "g"), want: "disk w.vmdk of VMs a, b, c, d, e and 2 more: why"},
		"a CD-ROM names it so": {uses: []diskUse{{vm: "a", drive: driveCDROM, image: "x.iso"}}, want: "CD-ROM x.iso of VM a: why"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := sharedWarning(tc.uses, "why").Error(); got != tc.want {
				t.Errorf("sharedWarning() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A placement asks minimega for the cluster's hosts once, however many VMs it places, and places nothing on the head
// node of an empty cluster.
func TestPlacementListsHostsOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		hosts mm.Hosts
		err   error
		want  bool
	}{
		{name: "the head node alone", hosts: mm.Hosts{{Name: "head", Headnode: true}}, want: true},
		{name: "two nodes", hosts: mm.Hosts{{Name: "head", Headnode: true}, {Name: "c1"}}},
		{name: "no hosts"},
		{name: "hosts phenix cannot list", err: errors.New("minimega is busy")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			env := checkEnv{hosts: func(context.Context) (mm.Hosts, error) {
				calls++

				return tc.hosts, tc.err
			}}

			where := newPlacement(diskSpec(string(common.DeployModeAll), nil), env)

			for _, vm := range []string{"a", "b", "c"} {
				got, err := where.onHeadnode(t.Context(), vm)
				if got != tc.want || !errors.Is(err, tc.err) {
					t.Errorf("onHeadnode(%s) = %t, %v; want %t, %v", vm, got, err, tc.want, tc.err)
				}
			}

			if calls != 1 {
				t.Errorf("hosts listed %d times, want 1", calls)
			}
		})
	}
}

// A web start keeps the disk warnings for its caller, unseen; a CLI start logs them at once.
func TestReportDiskWarnings(t *testing.T) {
	t.Parallel()

	warnings := []error{errors.New("disk a.qc2 of VM a: one"), errors.New("disk b.qc2 of VM b: two")}

	tests := []struct {
		name       string
		flush      bool
		warnings   []error
		wantUnseen int
	}{
		{name: "web", warnings: warnings, wantUnseen: 2},
		{name: "CLI", flush: true, warnings: warnings},
		{name: "no warnings"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := notes.Context(t.Context(), tc.flush)
			reportDiskWarnings(ctx, "exp", tc.warnings)

			if got := len(notes.Warnings(ctx, false)); got != tc.wantUnseen {
				t.Errorf("unseen warnings = %d, want %d", got, tc.wantUnseen)
			}

			if diff := cmp.Diff(tc.warnings, notes.Warnings(ctx, true), cmpopts.EquateErrors(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("recorded warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// diskcheckMM names the head node and lists the cluster's hosts; other mm.MM methods panic.
type diskcheckMM struct {
	mm.MM

	headnode string
	hosts    mm.Hosts
	err      error
}

func (m *diskcheckMM) Headnode() string { return m.headnode }

func (m *diskcheckMM) IsHeadnode(host string) bool { return host == m.headnode }

func (m *diskcheckMM) GetClusterHosts(bool) (mm.Hosts, error) { return m.hosts, m.err }

// The check's environment asks minimega which host is the head node and which hosts the cluster has.
func TestMMCheckEnv(t *testing.T) { //nolint:paralleltest // replaces mm.DefaultMM
	busy := errors.New("minimega is busy")

	tests := []struct {
		name      string
		fake      diskcheckMM
		host      string
		wantHead  bool
		wantErr   bool
		wantHosts mm.Hosts
	}{
		{
			name: "the head node", fake: diskcheckMM{headnode: "head", hosts: mm.Hosts{{Name: "head", Headnode: true}}},
			host: "head", wantHead: true, wantHosts: mm.Hosts{{Name: "head", Headnode: true}},
		},
		{name: "another node", fake: diskcheckMM{headnode: "head"}, host: "c1"},
		{name: "minimega names no host", fake: diskcheckMM{err: busy}, host: "head", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := mm.DefaultMM
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore default

			fake := tc.fake
			mm.DefaultMM = &fake //nolint:reassign // install test double

			env := mmCheckEnv()

			if env.filesDir != mm.GetMMFullPath("") || env.timeout != diskCheckTimeout {
				t.Errorf("mmCheckEnv() files directory %q, timeout %v; want %q, %v",
					env.filesDir, env.timeout, mm.GetMMFullPath(""), diskCheckTimeout)
			}

			head, err := env.isHeadnode(tc.host)
			if head != tc.wantHead || (err != nil) != tc.wantErr {
				t.Errorf("isHeadnode(%s) = %t, %v; want %t, error %t", tc.host, head, err, tc.wantHead, tc.wantErr)
			}

			hosts, err := env.hosts(t.Context())
			if !errors.Is(err, tc.fake.err) {
				t.Errorf("hosts() error = %v, want %v", err, tc.fake.err)
			}

			if diff := cmp.Diff(tc.wantHosts, hosts, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("hosts() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
