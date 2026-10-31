package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"phenix/api/disk"
	"phenix/util/partition"
)

const (
	injectPartitionsName = "inject-partitions"

	// bootPartitionMax is the size under which doctor calls a partition a boot partition, when the disk has a larger
	// Linux or NTFS partition.
	bootPartitionMax = 1 << 30
	// maxListedPartitions is the most partitions a row lists for one image.
	maxListedPartitions = 8

	// Phrases in the details of rows, which their hints look for.
	noPartitionPhrase = "names no partition"
	noFSPhrase        = "not a file system that minimega can write files to"
	bootPhrase        = "a small boot partition"
	noTablePhrase     = "has no partition table"
	noFirstPhrase     = "has no partition 1"
	noQemuImgPhrase   = "qemu-img is not installed"
	suggestPhrase     = "try inject_partition:"
)

// injectFSNames are the file systems that minimega cannot inject files into, with their names: no files, encrypted,
// or read-only. They are the ones the disk details card of the UI names.
var injectFSNames = map[string]string{ //nolint:gochecknoglobals // constant
	partition.FSSwap:     "Linux swap",
	partition.FSLVM:      "an LVM physical volume",
	partition.FSLUKS:     "a LUKS volume",
	partition.FSISO9660:  "ISO 9660",
	partition.FSSquashFS: "squashfs",
	partition.FSUnknown:  "no file system that doctor knows",
}

// dataFS are the file systems of the Linux and NTFS data partitions that doctor suggests for injects.
var dataFS = []string{ //nolint:gochecknoglobals // constant
	partition.FSExt2, partition.FSExt3, partition.FSExt4, partition.FSXFS, partition.FSBtrfs, partition.FSNTFS,
}

// injectTarget is a partition of a disk image that minimega injects files into at a start, and the VMs that inject.
type injectTarget struct {
	path      string
	partition int
	vms       []string // as <experiment>/<VM>
}

// injectImage is what doctor read of a disk image that receives injects.
type injectImage struct {
	found disk.Inspection
	why   string // why doctor did not read the partitions, or ""
}

// injectPartitionsCheck reads the partition table of each disk image that receives injects at the start of a stored
// experiment, once per image, and warns when the inject partition of a VM is not one that minimega can write files
// to. It never fails.
func injectPartitionsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	experiments, why := in.storedExperiments(ctx)
	if why != "" {
		return hostSkip(why)
	}

	targets := injectTargets(experiments, in.imagesDir())
	if len(targets) == 0 {
		return passCheck("no VM of a stored experiment gets injects")
	}

	qemuImg, err := in.env.lookPath("qemu-img")
	if err != nil {
		qemuImg = ""
	}

	ctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()

	var (
		images               = make(map[string]*injectImage)
		problems, notChecked []string
		checked              int
	)

	for _, t := range targets {
		image, ok := images[t.path]
		if !ok {
			image = in.readInjectImage(ctx, t.path, qemuImg)
			images[t.path] = image

			if image.why != "" {
				notChecked = append(notChecked, fmt.Sprintf("%s (%s)", t.path, image.why))
			}
		}

		if image.why != "" {
			continue
		}

		problem, note := injectProblem(image.found, t.partition)

		switch {
		case problem != "":
			problems = append(problems, fmt.Sprintf("%s (image %s, inject_partition %d): %s; %s",
				strings.Join(t.vms, ", "), t.path, t.partition, problem, partitionSummary(image.found)))
		case note != "":
			notChecked = append(notChecked, fmt.Sprintf("%s partition %d (%s)", t.path, t.partition, note))

			continue
		}

		checked++
	}

	return injectPartitionsRow(problems, notChecked, checked, len(targets))
}

// injectPartitionsRow is the row of injectPartitionsCheck for the problems it found, and the images and partitions
// it did not check, after it checked checked of the targets.
func injectPartitionsRow(problems, notChecked []string, checked, targets int) doctorCheck {
	var notes string
	if len(notChecked) > 0 {
		notes = ". Not checked: " + strings.Join(capList(notChecked, maxListedImages), "; ")
	}

	switch {
	case len(problems) > 0:
		return warnCheck(fmt.Sprintf("minimega cannot inject files into %s of %d at a start: %s%s",
			plural(len(problems), "inject partition", "inject partitions"), targets,
			strings.Join(capList(problems, maxListedImages), ". "), notes))
	case checked == 0:
		return hostSkip("doctor checked no inject partition: " +
			strings.Join(capList(notChecked, maxListedImages), "; "))
	}

	return passCheck(fmt.Sprintf("%s holds a file system that minimega can write files to%s",
		eachOf(checked, "inject partition", "inject partitions"), notes))
}

// injectTargets are the partitions that the VMs of experiments inject files into at a start, by image path and
// partition number. A relative image is in dir, the images directory, as minimega finds it.
func injectTargets(experiments []doctorExperiment, dir string) []*injectTarget {
	type key struct {
		path      string
		partition int
	}

	targets := make(map[key]*injectTarget)

	for _, exp := range experiments {
		for _, vm := range exp.vms {
			if vm.injectPartition == 0 || vm.injectImage == "" {
				continue
			}

			k := key{path: resolveImage(dir, vm.injectImage), partition: vm.injectPartition}

			t, ok := targets[k]
			if !ok {
				t = &injectTarget{path: k.path, partition: k.partition, vms: nil}
				targets[k] = t
			}

			t.vms = append(t.vms, exp.name+"/"+vm.name)
		}
	}

	return slices.SortedFunc(maps.Values(targets), func(a, b *injectTarget) int {
		if c := strings.Compare(a.path, b.path); c != 0 {
			return c
		}

		return a.partition - b.partition
	})
}

// readInjectImage reads the partitions of the disk image at path with qemuImg, or "" when qemu-img is not installed,
// and says why it did not.
func (in *doctorInput) readInjectImage(ctx context.Context, path, qemuImg string) *injectImage {
	image := new(injectImage)

	if _, err := in.env.stat(path); errors.Is(err, fs.ErrNotExist) {
		image.why = "it does not exist"

		return image
	} else if err != nil {
		image.why = "doctor cannot read it: " + unwrapPathError(err).Error()

		return image
	}

	stopped := fmt.Sprintf("doctor stopped reading images after %s", in.timeout)
	if ctx.Err() != nil {
		image.why = stopped

		return image
	}

	image.found = in.inspectDisk(ctx, path, qemuImg)

	switch {
	case ctx.Err() != nil:
		image.why = stopped
	case image.found.Qcow != nil && qemuImg == "":
		image.why = noQemuImgPhrase
	case image.found.PartitionsUnavailable != "":
		image.why = strings.TrimPrefix(image.found.PartitionsUnavailable, "phenix cannot read the partitions: ")
	}

	return image
}

// injectProblem says why minimega cannot inject files into partition number of the disk found holds, or why doctor
// cannot tell, as a note.
func injectProblem(found disk.Inspection, number int) (string, string) {
	switch partition.Scheme(found.PartitionTable) {
	case partition.SchemeNone:
		return fmt.Sprintf("the disk %s, so minimega finds no partition (no partitions found on image)", noTablePhrase), ""
	case partition.SchemeMBR, partition.SchemeGPT:
	case partition.SchemeUnknown:
		return "", "doctor cannot read its partition table: " + strings.Join(found.PartitionNotes, ", ")
	}

	if !hasPartition(found.Partitions, 1) {
		return fmt.Sprintf("the disk %s, and minimega waits for partition 1 before it opens any partition, so it "+
			"finds no partition (no partitions found on image)", noFirstPhrase), ""
	}

	i := slices.IndexFunc(found.Partitions, func(p disk.Partition) bool { return p.Number == number })
	if i < 0 {
		return fmt.Sprintf("inject_partition %d %s (desired partition %d not found)", number, noPartitionPhrase, number), ""
	}

	p := found.Partitions[i]
	suggested := suggestedPartition(found.Partitions)

	switch {
	case p.Extended:
		return fmt.Sprintf("partition %d is an extended partition, which holds no file system, so it is %s",
			number, noFSPhrase), ""
	case p.Filesystem == nil:
		return "", "doctor read only part of the disk, and not its file system"
	case injectFSNames[p.Filesystem.Type] != "":
		return fmt.Sprintf("partition %d holds %s, %s", number, injectFSNames[p.Filesystem.Type], noFSPhrase), ""
	case suggested != nil && suggested.Number != number && p.Size < bootPartitionMax && suggested.Size >= bootPartitionMax:
		return fmt.Sprintf("partition %d is %s (%s), so the files go to the wrong file system or the copy fails",
			number, bootPhrase, doctorBytes(p.Size)), ""
	}

	return "", ""
}

// hasPartition says if partitions holds a partition of number.
func hasPartition(partitions []disk.Partition, number int) bool {
	return slices.ContainsFunc(partitions, func(p disk.Partition) bool { return p.Number == number })
}

// suggestedPartition is the largest Linux or NTFS data partition of partitions, the first of equal ones, or nil.
func suggestedPartition(partitions []disk.Partition) *disk.Partition {
	var best *disk.Partition

	for i, p := range partitions {
		if p.Extended || p.Filesystem == nil || !slices.Contains(dataFS, p.Filesystem.Type) {
			continue
		}

		if best == nil || p.Size > best.Size {
			best = &partitions[i]
		}
	}

	return best
}

// partitionSummary lists the partitions of the disk found holds, with their types, file systems and sizes, and the
// inject_partition that doctor suggests.
func partitionSummary(found disk.Inspection) string {
	if len(found.Partitions) == 0 {
		return "the disk has no partitions; set inject_partition: 0 to turn off injects"
	}

	items := make([]string, 0, len(found.Partitions))

	for _, p := range found.Partitions {
		fields := []string{cmp.Or(p.Type, "unknown type")}

		switch {
		case p.Extended:
		case p.Filesystem == nil:
			fields = append(fields, "file system not read")
		default:
			fields = append(fields, p.Filesystem.Type)
		}

		fields = append(fields, doctorBytes(p.Size))
		items = append(items, fmt.Sprintf("%d (%s)", p.Number, strings.Join(fields, ", ")))
	}

	list := "partitions " + strings.Join(capList(items, maxListedPartitions), ", ")

	if !hasPartition(found.Partitions, 1) {
		return list + "; minimega injects into no partition of this disk, so set inject_partition: 0 to turn off injects"
	}

	if s := suggestedPartition(found.Partitions); s != nil {
		return list + "; " + suggestPhrase + " " + strconv.Itoa(s.Number)
	}

	return list + "; no Linux or NTFS partition to suggest, so set inject_partition: 0 to turn off injects"
}

// capList is the first n items, and a count of the rest.
func capList(items []string, n int) []string {
	if len(items) <= n {
		return items
	}

	return append(items[:n:n], fmt.Sprintf("and %d more", len(items)-n))
}

// injectPartitionsHint is what to do about an inject-partitions row that warns.
func injectPartitionsHint(c doctorCheck) string {
	set := "set inject_partition of the first drive of the VM in the topology (hardware.drives[0].inject_partition) " +
		"to the partition the row suggests, or to 0 to turn off injects (see Troubleshooting)"

	return hostHints(c, [][2]string{
		{noPartitionPhrase, set},
		{noFSPhrase, set},
		{bootPhrase, set},
		{noTablePhrase, "set inject_partition: 0 for the VM, because minimega injects files only into a partition"},
		{noFirstPhrase, "set inject_partition: 0 for the VM, or use an image that has a partition 1"},
		{noQemuImgPhrase, "install qemu-img (the qemu-utils package on Debian and Ubuntu) to check qcow2 images"},
	})
}
