package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/disk"
)

// writable-images warns about an image that a VM with snapshot: false writes to when another image is on top of it,
// or when more than one VM uses it, and skips when the store cannot be read.
func TestDoctorWritableImages(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		experiments []doctorExperiment
		why         string
		scanErr     error
		want        func(files string) doctorCheck
	}{
		"backing file and shared image": {
			experiments: []doctorExperiment{
				{name: "e1", vms: []doctorVM{{name: "a", images: []string{"base.qc2"}}, {name: "c", images: []string{"solo.qc2"}}}},
				{
					name: "e2",
					vms:  []doctorVM{{name: "b", images: []string{"base.qc2"}}, {name: "d", snapshot: true, images: []string{"solo.qc2"}}},
				},
			},
			want: func(files string) doctorCheck {
				return row("", doctorWarn, filepath.Join(files, "base.qc2")+": e1/a, e2/b writes to it (snapshot: false), "+
					"and 2 VMs use it: e1/a, e2/b. "+filepath.Join(files, "solo.qc2")+" backs other images, and e1/c writes "+
					"to it (snapshot: false), which breaks them: over.qc2, sub/over2.qc2. "+filepath.Join(files, "solo.qc2")+
					": e1/c writes to it (snapshot: false), and 2 VMs use it: e1/c, e2/d")
			},
		},
		"every VM on a snapshot": {
			experiments: []doctorExperiment{{name: "e1", vms: []doctorVM{{name: "a", snapshot: true, images: []string{"base.qc2"}}}}},
			want: func(string) doctorCheck {
				return row("", doctorPass, "no VM of a stored experiment has snapshot: false")
			},
		},
		"one VM on its own image": {
			experiments: []doctorExperiment{{name: "e1", vms: []doctorVM{{name: "a", images: []string{"/data/own.qc2"}}}}},
			want: func(string) doctorCheck {
				return row("", doctorPass, "1 images with snapshot: false, each with one VM and no image on top of it")
			},
		},
		"scan fails": {
			experiments: []doctorExperiment{{name: "e1", vms: []doctorVM{{name: "a", images: []string{"/data/own.qc2"}}}}},
			scanErr:     fmt.Errorf("listing: %w", context.DeadlineExceeded),
			want: func(files string) doctorCheck {
				return row("", doctorPass, "1 images with snapshot: false, each with one VM. The backing files are not "+
					"checked: did not list the images in "+files+" within 1s")
			},
		},
		"store not read": {
			why: "this user cannot read /phenix/store.bdb",
			want: func(string) doctorCheck {
				return row("", doctorSkip, "not checked, because this user cannot read /phenix/store.bdb")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)
			files := filepath.Join(dir, "files")
			in := fakeDoctorInput(dir, nil)
			in.found = &doctorFound{
				minimegaUp: true, minimegaArgs: map[string]string{"filepath": files},
				experiments: &doctorExperiments{list: tc.experiments, why: tc.why},
			}
			in.images = func(context.Context, string) (disk.ChainScan, error) {
				return disk.ChainScan{Images: 4, Checked: 4, Backing: map[string][]string{
					"over.qc2": {
						filepath.Join(files, "solo.qc2"),
					},
					"sub/over2.qc2": {filepath.Join(files, "x.qc2"), filepath.Join(files, "solo.qc2")},
					"base.qc2":      {filepath.Join(files, "root.qc2")},
				}}, tc.scanErr
			}

			got := writableImagesCheck(t.Context(), in)
			if diff := cmp.Diff(tc.want(files), got); diff != "" {
				t.Errorf("writableImagesCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// image-locks names the qemu processes that hold open an image or backing file of a stopped experiment in a way that
// blocks its start: any open of an image a VM writes to, and an open for writing of the others. It leaves out the VMs
// of running experiments, and counts the processes it cannot read.
func TestDoctorImageLocks(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	files := filepath.Join(dir, "files")

	for _, image := range []string{"own.qc2", "base.qc2", "root.qc2"} {
		writeSized(t, filepath.Join(files, image), 1)
	}

	experiments := []doctorExperiment{
		{name: "stopped1", vms: []doctorVM{{name: "a", images: []string{"own.qc2"}}}},
		{name: "stopped2", vms: []doctorVM{{name: "b", snapshot: true, images: []string{"base.qc2"}}}},
		{name: "running", running: true, vms: []doctorVM{{name: "c", images: []string{"own.qc2"}}}},
	}

	// pid: comm, then each open file by fd: its image and the flags of its fdinfo
	type open struct{ image, flags string }

	procs := map[int]struct {
		comm  string
		files map[string]open
	}{
		100: {comm: "qemu-system-x86", files: map[string]open{"3": {"own.qc2", "0100000"}}},
		101: {comm: "qemu-img", files: map[string]open{"4": {"base.qc2", "0100000"}}},
		102: {comm: "qemu-nbd", files: map[string]open{"5": {"root.qc2", "0100002"}}},
		103: {comm: "bash", files: map[string]open{"6": {"own.qc2", "0100002"}}},
		300: {comm: "qemu-system-x86", files: map[string]open{"7": {"own.qc2", "0100002"}}},
	}

	tests := map[string]struct {
		os   string
		inv  *doctorInventory
		why  string
		want doctorCheck
	}{
		"stray processes": {
			inv: doctorTestInventory([]doctorMMVM{{namespace: "running", name: "c", state: "RUNNING", pid: 300}}, nil, nil),
			want: row("", doctorWarn, "processes that are not VMs of running experiments hold open images of stopped "+
				"experiments, so qemu cannot lock the images at a start: pid 100 (qemu-system-x86) holds "+
				"open "+filepath.Join(files, "own.qc2")+", which stopped1/a uses, pid 102 (qemu-nbd) holds open "+
				filepath.Join(files, "root.qc2")+", which stopped2/b uses. This user cannot read the open files of 1 processes"),
		},
		"minimega down": {
			inv: &doctorInventory{why: "minimega gave no version"},
			want: row("", doctorWarn, "processes that are not VMs of running experiments hold open images of stopped "+
				"experiments, so qemu cannot lock the images at a start: pid 100 (qemu-system-x86) holds "+
				"open "+filepath.Join(files, "own.qc2")+", which stopped1/a uses, pid 102 (qemu-nbd) holds open "+
				filepath.Join(files, "root.qc2")+", which stopped2/b uses, pid 300 (qemu-system-x86) holds open "+
				filepath.Join(files, "own.qc2")+", which stopped1/a uses. This user cannot read the open files of 1 "+
				"processes. The VMs of running experiments are not known, because minimega gave no version"),
		},
		"not linux": {os: "darwin", want: row("", doctorSkip, "not checked, because /proc/<pid>/fd is a Linux feature")},
		"store not read": {
			why:  "this user cannot read /phenix/store.bdb",
			want: row("", doctorSkip, "not checked, because this user cannot read /phenix/store.bdb"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			links := make(map[string]string)

			for pid, p := range procs {
				host[fmt.Sprintf("/proc/%d/comm", pid)] = p.comm + "\n"

				for fd, o := range p.files {
					link := fmt.Sprintf("/proc/%d/fd/%s", pid, fd)
					host[link] = ""
					host[fmt.Sprintf("/proc/%d/fdinfo/%s", pid, fd)] = "pos:\t0\nflags:\t" + o.flags + "\nmnt_id:\t25\n"
					links[link] = filepath.Join(files, o.image)
				}
			}

			// a qemu whose open files this user cannot read
			host["/proc/104/comm"] = "qemu-system-x86\n"

			in := hostInput(t, host, doctorHostTools)
			in.found = &doctorFound{
				minimegaUp: true, minimegaArgs: map[string]string{"filepath": files},
				experiments: &doctorExperiments{list: experiments, why: tc.why}, inventory: tc.inv,
			}
			in.images = func(context.Context, string) (disk.ChainScan, error) {
				return disk.ChainScan{Images: 3, Checked: 3, Backing: map[string][]string{
					"base.qc2": {filepath.Join(files, "root.qc2")},
				}}, nil
			}

			if tc.os != "" {
				in.system.os = tc.os
			}

			stat := in.env.stat
			in.env.stat = func(path string) (os.FileInfo, error) {
				if target, ok := links[path]; ok {
					return os.Stat(target)
				}

				return stat(path)
			}

			got := imageLocksCheck(t.Context(), in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("imageLocksCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// image-locks passes when no stopped experiment uses an image, and says how many files it checked otherwise.
func TestDoctorImageLocksNone(t *testing.T) {
	t.Parallel()

	in := hostInput(t, doctorGoodHost(), doctorHostTools)
	in.found = &doctorFound{minimegaUp: true, experiments: &doctorExperiments{}, inventory: doctorTestInventory(nil, nil, nil)}

	if diff := cmp.Diff(row("", doctorPass, "no image of a stopped experiment to check"), imageLocksCheck(t.Context(), in)); diff != "" {
		t.Errorf("imageLocksCheck() mismatch (-want +got):\n%s", diff)
	}
}

// In a container, image-locks passes only when it sees the minimega process or a VM of a running experiment, because
// a container with its own PID namespace does not show the processes of minimega.
func TestDoctorImageLocksContainer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		procs map[string]string // the comm of each pid
		vms   []doctorMMVM
		want  doctorCheck
	}{
		"no minimega process": {
			procs: map[string]string{"1": "sh"},
			want: row("", doctorSkip, "not checked, because this container shows no minimega process in /proc, so it "+
				"can have its own PID namespace"),
		},
		"minimega process": {
			procs: map[string]string{"1": "sh", "7": "minimega"},
			want:  row("", doctorPass, "no other process "+lockPhrase+" the 1 images and backing files of stopped experiments"),
		},
		"VM of a running experiment": {
			procs: map[string]string{"1": "sh", "300": "qemu-system-x86"},
			vms:   []doctorMMVM{{namespace: "running", name: "c", state: "RUNNING", pid: 300}},
			want:  row("", doctorPass, "no other process "+lockPhrase+" the 1 images and backing files of stopped experiments"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			host["/.dockerenv"] = ""

			for pid, comm := range tc.procs {
				host["/proc/"+pid+"/comm"] = comm + "\n"
			}

			in := hostInput(t, host, doctorHostTools)
			files := filepath.Join(filepath.Dir(in.text("base-dir.phenix")), "files")
			writeSized(t, filepath.Join(files, "own.qc2"), 1)

			experiments := []doctorExperiment{
				{name: "stopped", vms: []doctorVM{{name: "a", snapshot: true, images: []string{"own.qc2"}}}},
				{name: "running", running: true, vms: []doctorVM{{name: "c", snapshot: true, images: []string{"run.qc2"}}}},
			}
			in.found = &doctorFound{
				minimegaUp: true, minimegaArgs: map[string]string{"filepath": files},
				experiments: &doctorExperiments{list: experiments}, inventory: doctorTestInventory(tc.vms, nil, nil),
			}
			in.images = func(context.Context, string) (disk.ChainScan, error) { return disk.ChainScan{}, nil }

			if diff := cmp.Diff(tc.want, imageLocksCheck(t.Context(), in)); diff != "" {
				t.Errorf("imageLocksCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
