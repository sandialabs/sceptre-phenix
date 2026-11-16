package cmd

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	writableImagesName = "writable-images"
	imageLocksName     = "image-locks"

	// O_ACCMODE, O_WRONLY and O_RDWR, as the flags line of /proc/<pid>/fdinfo/<fd> gives them, in octal.
	accessModeMask = 0o3
	writeOnly      = 0o1
	readWrite      = 0o2

	// Phrases in the details of rows, which their hints look for.
	backsPhrase  = "backs other images"
	sharedPhrase = "VMs use it"
	lockPhrase   = "holds open"
)

// doctorImageUse is a disk image that VMs of stored experiments use, at its path in the images directory.
type doctorImageUse struct {
	path    string
	vms     []string // as <experiment>/<VM>
	writers []string // the VMs with snapshot: false, which write to it
	write   bool     // a VM with snapshot: false writes to it
}

// resolveImage is the path of image, as a topology names it, as minimega opens it: relative to the images directory.
func resolveImage(dir, image string) string {
	if filepath.IsAbs(image) {
		return filepath.Clean(image)
	}

	return filepath.Join(dir, image)
}

// imageUses lists the images that the VMs of the experiments that keep says use, by path. A VM with snapshot: false
// writes to each of its drive images; minimega runs every other VM on snapshots of its drives.
func imageUses(experiments []doctorExperiment, dir string, keep func(doctorExperiment) bool) []*doctorImageUse {
	uses := make(map[string]*doctorImageUse)

	for _, exp := range experiments {
		if !keep(exp) {
			continue
		}

		for _, vm := range exp.vms {
			for _, image := range vm.images {
				path := resolveImage(dir, image)

				use, ok := uses[path]
				if !ok {
					use = &doctorImageUse{path: path, vms: nil, writers: nil, write: false}
					uses[path] = use
				}

				use.vms = append(use.vms, exp.name+"/"+vm.name)

				if !vm.snapshot {
					use.writers = append(use.writers, exp.name+"/"+vm.name)
					use.write = true
				}
			}
		}
	}

	list := make([]*doctorImageUse, 0, len(uses))
	for _, path := range slices.Sorted(maps.Keys(uses)) {
		list = append(list, uses[path])
	}

	return list
}

// writableImagesCheck warns about each image that a VM with snapshot: false writes to, when another image in the
// images directory has it as a backing file, or when more than one VM uses it. It reads the stored experiments and the
// scan of disk-chains. It never fails.
func writableImagesCheck(ctx context.Context, in *doctorInput) doctorCheck {
	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	dir := in.imagesDir()
	uses := imageUses(experiments, dir, func(doctorExperiment) bool { return true })
	uses = slices.DeleteFunc(uses, func(u *doctorImageUse) bool { return !u.write })

	if len(uses) == 0 {
		return passCheck("no VM of a stored experiment has snapshot: false")
	}

	_, scan, stop := in.scanImages(ctx)

	var problems, notes []string

	for _, use := range uses {
		var overlays []string

		for _, rel := range slices.Sorted(maps.Keys(scan.Backing)) {
			if slices.Contains(scan.Backing[rel], use.path) {
				overlays = append(overlays, rel)
			}
		}

		writers := strings.Join(use.writers, ", ")

		if len(overlays) > 0 {
			problems = append(problems, fmt.Sprintf("%s %s, and %s writes to it (snapshot: false), which breaks them: %s",
				use.path, backsPhrase, writers, shortList(overlays)))
		}

		if len(use.vms) > 1 {
			problems = append(problems, fmt.Sprintf("%s: %s writes to it (snapshot: false), and %d %s: %s",
				use.path, writers, len(use.vms), sharedPhrase, strings.Join(use.vms, ", ")))
		}
	}

	if stop != nil {
		notes = append(notes, "the backing files are not checked: "+strings.TrimPrefix(stop.Detail, "not checked, because "))
	}

	if len(problems) > 0 {
		return warnCheck(strings.Join(append(problems, notes...), ". "))
	}

	detail := fmt.Sprintf("%d images with snapshot: false, each with one VM and no image on top of it", len(uses))
	if len(notes) > 0 {
		detail = fmt.Sprintf("%d images with snapshot: false, each with one VM. %s", len(uses), capitalize(notes[0]))
	}

	return passCheck(detail)
}

// writableImagesHint is what to do about a writable-images row that warns.
func writableImagesHint(c doctorCheck) string {
	return hostHints(c, [][2]string{
		{backsPhrase, "set snapshot: true for the VM, or give it its own copy of the image (qemu-img convert -O qcow2 " +
			"<image> <copy>)"},
		{sharedPhrase, "set snapshot: true for each VM, or give each VM its own copy of the image (cp or qemu-img convert)"},
	})
}

// imageTarget is a file that the start of a stopped experiment opens, which another process can lock.
type imageTarget struct {
	path  string
	info  os.FileInfo
	vms   []string
	write bool // the start writes to it, so a process that only reads it locks it too
}

// imageLocksCheck looks in /proc/<pid>/fd for qemu processes (qemu-system, qemu-nbd, qemu-img) that hold open a disk
// image or a backing file that the VMs of a stopped experiment use, and that are not VMs that minimega runs for a
// running experiment. qemu refuses to start a VM on such an image: Failed to get "write" lock. It reads only, never
// fails, and skips in a container that shows no minimega process.
func imageLocksCheck(ctx context.Context, in *doctorInput) doctorCheck {
	if in.system.os != linuxOS {
		return hostSkip("/proc/<pid>/fd is a Linux feature")
	}

	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	targets := in.imageTargets(ctx, experiments)
	if len(targets) == 0 {
		return passCheck("no image of a stopped experiment to check")
	}

	running := make(map[int]bool)

	inv := in.minimegaInventory(ctx)
	for _, vm := range inv.vms {
		if exp, ok := experimentNamed(experiments, vm.namespace); ok && exp.running && vm.pid > 0 {
			running[vm.pid] = true
		}
	}

	ctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()

	scan := in.imageHolders(ctx, targets, running)
	holders := scan.holders

	if len(holders) == 0 && !scan.minimega && containerHint(in.env) != "" {
		return hostSkip("this container shows no minimega process in /proc, so it can have its own PID namespace")
	}

	var notes []string

	if scan.denied > 0 {
		notes = append(notes, fmt.Sprintf("this user cannot read the open files of %d processes", scan.denied))
	}

	if !scan.whole {
		notes = append(notes, fmt.Sprintf("it checked %d processes within %s", scan.scanned, in.timeout))
	}

	if inv.why != "" {
		notes = append(notes, "the VMs of running experiments are not known, because "+inv.why)
	}

	for i := range notes {
		notes[i] = capitalize(notes[i])
	}

	if len(holders) > 0 {
		return warnCheck(fmt.Sprintf("processes that are not VMs of running experiments hold open images of "+
			"stopped experiments, so qemu cannot lock the images at a start: %s",
			strings.Join(append([]string{shortList(holders)}, notes...), ". ")))
	}

	detail := fmt.Sprintf("no other process %s the %d images and backing files of stopped experiments",
		lockPhrase, len(targets))
	if len(notes) > 0 {
		detail += ". " + strings.Join(notes, ". ")
	}

	return passCheck(detail)
}

// imageTargets are the drive images of the VMs of the stopped experiments, and the backing files that disk-chains
// read for them, each a file that exists.
func (in *doctorInput) imageTargets(ctx context.Context, experiments []doctorExperiment) []imageTarget {
	dir := in.imagesDir()
	uses := imageUses(experiments, dir, func(e doctorExperiment) bool { return !e.running })

	if len(uses) == 0 {
		return nil
	}

	_, scan, _ := in.scanImages(ctx)

	var targets []imageTarget

	add := func(path string, vms []string, write bool) {
		for i, t := range targets {
			if t.path == path {
				targets[i].vms = append(targets[i].vms, vms...)
				targets[i].write = targets[i].write || write

				return
			}
		}

		if info, err := in.env.stat(path); err == nil && info.Mode().IsRegular() {
			targets = append(targets, imageTarget{path: path, info: info, vms: slices.Clone(vms), write: write})
		}
	}

	for _, use := range uses {
		add(use.path, use.vms, use.write)

		if rel, err := filepath.Rel(dir, use.path); err == nil {
			for _, backing := range scan.Backing[rel] {
				add(backing, use.vms, false)
			}
		}
	}

	return targets
}

// imageHolderScan is what imageHolders found in /proc.
type imageHolderScan struct {
	holders         []string
	scanned, denied int  // the qemu processes it checked, and those it could not read
	whole           bool // it checked every process before ctx ended
	minimega        bool // it saw the minimega process or a VM of a running experiment
}

// imageHolders names each qemu process in /proc, except the pids in skip, that holds a target open in a way that
// blocks a start: any open of an image that the start writes to, and an open for writing of the others.
func (in *doctorInput) imageHolders(ctx context.Context, targets []imageTarget, skip map[int]bool) imageHolderScan {
	scan := imageHolderScan{holders: nil, scanned: 0, denied: 0, whole: true, minimega: false}

	entries, err := in.env.readDir("/proc")
	if err != nil {
		return scan
	}

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		if skip[pid] {
			scan.minimega = true

			continue
		}

		if ctx.Err() != nil {
			scan.whole = false

			return scan
		}

		comm, _ := in.env.readFile(fmt.Sprintf("/proc/%d/comm", pid))
		name := strings.TrimSpace(string(comm))
		scan.minimega = scan.minimega || name == "minimega"

		if !strings.HasPrefix(name, "qemu") {
			continue
		}

		scan.scanned++

		fds, err := in.env.readDir(fmt.Sprintf("/proc/%d/fd", pid))
		if err != nil {
			scan.denied++

			continue
		}

		for _, fd := range fds {
			if item := in.heldTarget(pid, name, fd.Name(), targets); item != "" {
				scan.holders = append(scan.holders, item)
			}
		}
	}

	return scan
}

// heldTarget describes the target that the open file fd of the process pid, whose command is name, holds in a way that
// blocks a start, or is "".
func (in *doctorInput) heldTarget(pid int, name, fd string, targets []imageTarget) string {
	info, err := in.env.stat(fmt.Sprintf("/proc/%d/fd/%s", pid, fd))
	if err != nil {
		return ""
	}

	for _, t := range targets {
		if !os.SameFile(info, t.info) {
			continue
		}

		if !t.write && !in.fdWrites(pid, fd) {
			return ""
		}

		return fmt.Sprintf("pid %d (%s) %s %s, which %s uses", pid, name, lockPhrase, t.path, strings.Join(t.vms, ", "))
	}

	return ""
}

// fdWrites reports whether the process pid opened its file fd for writing, from /proc/<pid>/fdinfo/<fd>, and true when
// it cannot tell.
func (in *doctorInput) fdWrites(pid int, fd string) bool {
	data, err := in.env.readFile(fmt.Sprintf("/proc/%d/fdinfo/%s", pid, fd))
	if err != nil {
		return true
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if flags, ok := strings.CutPrefix(line, "flags:"); ok {
			mode, err := strconv.ParseUint(strings.TrimSpace(flags), 8, 64)
			if err != nil {
				return true
			}

			return mode&accessModeMask == writeOnly || mode&accessModeMask == readWrite
		}
	}

	return true
}

// imageLocksHint is what to do about an image-locks row that warns.
func imageLocksHint(c doctorCheck) string {
	if c.Status == doctorSkip {
		return ""
	}

	return "stop each process that the row names when no VM, inject or image command uses it: kill <pid>, or " +
		"qemu-nbd -d /dev/nbdN for a qemu-nbd (see the nbd row)"
}
