package cmd

import (
	"context"
	"fmt"
	"io/fs"
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
		wantHint    string
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
			wantHint: "set snapshot: true for the VM, or give it its own copy of the image (qemu-img convert -O qcow2 " +
				"<image> <copy>). Set snapshot: true for each VM, or give each VM its own copy of the image (cp or " +
				"qemu-img convert)",
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

			if hint := doctorHintOf(got, writableImagesHint); hint != tc.wantHint {
				t.Errorf("writableImagesHint() = %q, want %q", hint, tc.wantHint)
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

			wantHint := ""
			if got.Status == doctorWarn {
				wantHint = doctorImagesLockHint
			}

			if hint := doctorHintOf(got, imageLocksHint); hint != wantHint {
				t.Errorf("imageLocksHint() = %q, want %q", hint, wantHint)
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

// doctorImagesLockHint is the hint of an image-locks row that warns.
const doctorImagesLockHint = "stop each process that the row names when no VM, inject or image command uses it: " +
	"kill <pid>, or qemu-nbd -d /dev/nbdN for a qemu-nbd (see the nbd row)"

// The image-locks hint names how to stop the processes, and a skipped row gets none.
func TestImageLocksHint(t *testing.T) {
	t.Parallel()

	if got := imageLocksHint(row("", doctorWarn, "pid 100 (qemu-nbd) holds open x")); got != doctorImagesLockHint {
		t.Errorf("imageLocksHint(warn) = %q, want %q", got, doctorImagesLockHint)
	}

	if got := imageLocksHint(hostSkip("x")); got != "" {
		t.Errorf("imageLocksHint(skip) = %q, want none", got)
	}
}

// When the context ends before image-locks reads every process, it passes and says how many it checked, with the
// reason it does not know the VMs of running experiments.
func TestDoctorImageLocksPartialScan(t *testing.T) {
	t.Parallel()

	host := doctorGoodHost()
	host["/proc/1/comm"] = "qemu-system-x86\n"

	in := hostInput(t, host, doctorHostTools)
	files := filepath.Join(filepath.Dir(in.text("base-dir.phenix")), "files")
	writeSized(t, filepath.Join(files, "own.qc2"), 1)

	in.found = &doctorFound{
		minimegaUp: true, minimegaArgs: map[string]string{"filepath": files},
		experiments: &doctorExperiments{list: []doctorExperiment{
			{name: "stopped", vms: []doctorVM{{name: "a", images: []string{"own.qc2"}}}},
		}},
		inventory: &doctorInventory{why: "minimega gave no version"},
	}
	in.images = func(context.Context, string) (disk.ChainScan, error) { return disk.ChainScan{}, nil }

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	want := row("", doctorPass, "no other process holds open the 1 images and backing files of stopped experiments. "+
		"It checked 0 processes within 1s. The VMs of running experiments are not known, because minimega gave no version")
	if diff := cmp.Diff(want, imageLocksCheck(ctx, in)); diff != "" {
		t.Errorf("imageLocksCheck() mismatch (-want +got):\n%s", diff)
	}
}

// An image path from a topology is relative to the images directory unless it is absolute, and is cleaned.
func TestResolveImage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ dir, image, want string }{
		"relative":       {dir: "/phenix/images", image: "a.qc2", want: "/phenix/images/a.qc2"},
		"subdirectory":   {dir: "/phenix/images", image: "sub/../b.qc2", want: "/phenix/images/b.qc2"},
		"absolute":       {dir: "/phenix/images", image: "/data//c.qc2", want: "/data/c.qc2"},
		"leaves the dir": {dir: "/phenix/images", image: "../d.qc2", want: "/phenix/d.qc2"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := resolveImage(tc.dir, tc.image); got != tc.want {
				t.Errorf("resolveImage(%q, %q) = %q, want %q", tc.dir, tc.image, got, tc.want)
			}
		})
	}
}

// doctorImagesUse is a doctorImageUse with exported fields, to compare.
type doctorImagesUse struct {
	Path         string
	VMs, Writers []string
	Write        bool
}

// imageUses lists each image of the experiments that keep says once, by path, with every VM that uses it and those
// with snapshot: false.
func TestImageUses(t *testing.T) {
	t.Parallel()

	experiments := []doctorExperiment{
		{name: "e1", vms: []doctorVM{
			{name: "a", images: []string{"base.qc2", "/data/extra.qc2"}},
			{name: "b", snapshot: true, images: []string{"base.qc2"}},
		}},
		{name: "e2", running: true, vms: []doctorVM{{name: "c", snapshot: true, images: []string{"/phenix/images/base.qc2"}}}},
		{name: "e3", vms: []doctorVM{{name: "d"}}},
	}

	tests := map[string]struct {
		keep func(doctorExperiment) bool
		want []doctorImagesUse
	}{
		"every experiment": {
			keep: func(doctorExperiment) bool { return true },
			want: []doctorImagesUse{
				{Path: "/data/extra.qc2", VMs: []string{"e1/a"}, Writers: []string{"e1/a"}, Write: true},
				{Path: "/phenix/images/base.qc2", VMs: []string{"e1/a", "e1/b", "e2/c"}, Writers: []string{"e1/a"}, Write: true},
			},
		},
		"running only": {
			keep: func(e doctorExperiment) bool { return e.running },
			want: []doctorImagesUse{{Path: "/phenix/images/base.qc2", VMs: []string{"e2/c"}}},
		},
		"none": {keep: func(doctorExperiment) bool { return false }, want: []doctorImagesUse{}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := []doctorImagesUse{}
			for _, u := range imageUses(experiments, "/phenix/images", tc.keep) {
				got = append(got, doctorImagesUse{Path: u.path, VMs: u.vms, Writers: u.writers, Write: u.write})
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("imageUses() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// imageTargets are the images of stopped experiments that exist as regular files, and their backing files, each
// once: a file that is both gets the VMs of both, and is written to when any use writes to it.
func TestImageTargets(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	files := filepath.Join(dir, "files")

	for _, image := range []string{"base.qc2", "over.qc2", "root.qc2"} {
		writeSized(t, filepath.Join(files, image), 1)
	}

	if err := os.Mkdir(filepath.Join(files, "dir.qc2"), 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}

	in := fakeDoctorInput(dir, nil)
	in.found = &doctorFound{minimegaUp: true, minimegaArgs: map[string]string{"filepath": files}}
	in.images = func(context.Context, string) (disk.ChainScan, error) {
		return disk.ChainScan{Images: 2, Checked: 2, Backing: map[string][]string{
			"over.qc2": {filepath.Join(files, "base.qc2"), filepath.Join(files, "root.qc2")},
			"base.qc2": {filepath.Join(files, "root.qc2")},
		}}, nil
	}

	experiments := []doctorExperiment{
		{name: "e", vms: []doctorVM{
			{name: "a", images: []string{"base.qc2", "missing.qc2", "dir.qc2"}},
			{name: "b", snapshot: true, images: []string{"over.qc2"}},
		}},
		{name: "run", running: true, vms: []doctorVM{{name: "c", images: []string{"root.qc2"}}}},
	}

	targets := in.imageTargets(t.Context(), experiments)
	got := make([]doctorImagesUse, 0, len(targets))

	for _, target := range targets {
		if target.info == nil || target.info.Name() != filepath.Base(target.path) {
			t.Errorf("imageTargets() target %s has file information %v", target.path, target.info)
		}

		got = append(got, doctorImagesUse{Path: target.path, VMs: target.vms, Write: target.write})
	}

	want := []doctorImagesUse{
		{Path: filepath.Join(files, "base.qc2"), VMs: []string{"e/a", "e/b"}, Write: true},
		{Path: filepath.Join(files, "root.qc2"), VMs: []string{"e/a", "e/b"}},
		{Path: filepath.Join(files, "over.qc2"), VMs: []string{"e/b"}},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("imageTargets() mismatch (-want +got):\n%s", diff)
	}

	if targets := in.imageTargets(t.Context(), experiments[1:]); targets != nil {
		t.Errorf("imageTargets(running experiments only) = %v, want none", targets)
	}
}

// imageHolders finds nothing, and counts no process, when it cannot list /proc.
func TestImageHoldersNoProc(t *testing.T) {
	t.Parallel()

	in := hostInput(t, doctorGoodHost(), doctorHostTools)
	in.env.readDir = func(string) ([]os.DirEntry, error) { return nil, fs.ErrPermission }

	scan := in.imageHolders(t.Context(), nil, nil)
	if scan.holders != nil || scan.scanned != 0 || scan.denied != 0 || !scan.whole || scan.minimega {
		t.Errorf("imageHolders() without /proc = %+v, want a whole scan that found nothing", scan)
	}
}

// heldTarget names a target only when the open file is that target and the open blocks a start: any open of a
// target the start writes to, and an open for writing of the others. A file that is gone holds nothing.
func TestHeldTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	image, other := filepath.Join(dir, "image.qc2"), filepath.Join(dir, "other.qc2")
	writeSized(t, image, 1)
	writeSized(t, other, 1)

	info, err := os.Stat(image)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	tests := map[string]struct {
		link  string // the file the open file is, or none
		write bool   // the start writes to the target
		flags string // the flags line of fdinfo, or no fdinfo when empty
		want  string
	}{
		"gone":         {write: true},
		"another file": {link: other, write: true, flags: "0100002"},
		"read of a written target": {
			link: image, write: true, flags: "0100000", want: "pid 7 (qemu-nbd) holds open " + image + ", which e/a uses",
		},
		"read of a read target": {link: image, flags: "0100000"},
		"write of a read target": {
			link: image, flags: "0100001", want: "pid 7 (qemu-nbd) holds open " + image + ", which e/a uses",
		},
		"no fdinfo, a read target": {link: image, want: "pid 7 (qemu-nbd) holds open " + image + ", which e/a uses"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			if tc.flags != "" {
				host["/proc/7/fdinfo/3"] = "pos:\t0\nflags:\t" + tc.flags + "\n"
			}

			in := hostInput(t, host, doctorHostTools)
			stat := in.env.stat
			in.env.stat = func(path string) (os.FileInfo, error) {
				if path == "/proc/7/fd/3" && tc.link != "" {
					return os.Stat(tc.link)
				}

				return stat(path)
			}

			targets := []imageTarget{{path: image, info: info, vms: []string{"e/a"}, write: tc.write}}
			if got := in.heldTarget(7, "qemu-nbd", "3", targets); got != tc.want {
				t.Errorf("heldTarget() = %q, want %q", got, tc.want)
			}
		})
	}
}

// fdWrites reads the access mode of the flags line of fdinfo, and is true when it cannot tell.
func TestFdWrites(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		fdinfo string // none when "-"
		want   bool
	}{
		"read only":     {fdinfo: "pos:\t0\nflags:\t0100000\nmnt_id:\t25\n", want: false},
		"write only":    {fdinfo: "flags:\t0100001\n", want: true},
		"read write":    {fdinfo: "flags:\t02\n", want: true},
		"both bits":     {fdinfo: "flags:\t03\n", want: false},
		"not octal":     {fdinfo: "flags:\t0109\n", want: true},
		"no flags line": {fdinfo: "pos:\t0\n", want: true},
		"no fdinfo":     {fdinfo: "-", want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			if tc.fdinfo != "-" {
				host["/proc/9/fdinfo/4"] = tc.fdinfo
			}

			if got := hostInput(t, host, doctorHostTools).fdWrites(9, "4"); got != tc.want {
				t.Errorf("fdWrites() of %q = %t, want %t", tc.fdinfo, got, tc.want)
			}
		})
	}
}
