package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"phenix/api/disk"
	"phenix/util/diskchain"
)

const (
	// formatsDocs is the docs section about image format versions and their fixes.
	formatsDocs = "https://phenix.sceptre.dev/latest/disks/#image-format-versions"

	qcow2V2Label = "qcow2 version 2"
	qcowV1Label  = "qcow version 1"

	// qemuNeedsPrefix starts the disk-formats row about images that need a later qemu.
	qemuNeedsPrefix = "qemu-img is version "
)

// qemuVersionPattern finds the version in the first line of qemu-img --version.
var qemuVersionPattern = regexp.MustCompile(`version (\d+)\.(\d+)`)

// diskFormatsChecks warns about the images in the images folder whose own format is old, one phenix does not
// check, or one qemu refuses, and about images with a feature that the local qemu-img is too old for. It shares the
// scan of disk-chains, and never fails.
func diskFormatsChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	dir, scan, stop := in.scanImages(ctx)
	if stop != nil {
		return []doctorCheck{*stop}
	}

	var items []string

	for _, image := range scan.Formats {
		switch {
		case image.Problem != "":
			items = append(items, fmt.Sprintf("%s (%s)", image.Path, image.Problem))
		case image.Kind != 0:
			items = append(items, fmt.Sprintf("%s (%s)", image.Path, formatLabel(image.Kind)))
		}
	}

	list := shortList(items)

	var check doctorCheck

	switch {
	case scan.Checked < scan.Images && len(items) == 0:
		check = warnCheck(fmt.Sprintf("checked %d of %d images in %s within %s, and found none in an old or "+
			"unsupported format", scan.Checked, scan.Images, dir, in.timeout))
	case scan.Checked < scan.Images:
		check = warnCheck(fmt.Sprintf("checked %d of %d images in %s within %s, and found %d in an old or "+
			"unsupported format: %s", scan.Checked, scan.Images, dir, in.timeout, len(items), list))
	case len(items) == 1:
		check = warnCheck(fmt.Sprintf("1 of %s in %s uses an old or unsupported format: %s",
			imageCount(scan.Images), dir, list))
	case len(items) > 1:
		check = warnCheck(fmt.Sprintf("%d of %s in %s use an old or unsupported format: %s",
			len(items), imageCount(scan.Images), dir, list))
	case scan.Images == 0:
		check = passCheck("no images in " + dir)
	default:
		check = passCheck(fmt.Sprintf("%s in %s, all qcow2 version 3 or raw", imageCount(scan.Images), dir))
	}

	checks := []doctorCheck{check}

	if needs, ok := qemuNeedsCheck(ctx, in, dir, scan.Formats); ok {
		checks = append(checks, needs)
	}

	return checks
}

// formatLabel names an old or unsupported format as the disk-formats row lists it.
func formatLabel(kind diskchain.NoteKind) string {
	switch kind {
	case diskchain.QcowV1:
		return qcowV1Label
	case diskchain.Qcow2V2:
		return qcow2V2Label
	case diskchain.QED:
		return "qed"
	case diskchain.VMDK:
		return "vmdk"
	case diskchain.Dirty, diskchain.Corrupt, diskchain.Zstd, diskchain.ExtendedL2:
	}

	return "unknown format"
}

// qemuNeedsCheck warns about the images with a feature that needs a later qemu than the local qemu-img. It reports
// false when no image needs one, or when it cannot read the qemu-img version.
func qemuNeedsCheck(ctx context.Context, in *doctorInput, dir string, images []disk.FormatImage) (doctorCheck, bool) {
	var none doctorCheck

	if !hasNeeds(images) {
		return none, false
	}

	path, err := in.env.lookPath("qemu-img")
	if err != nil {
		return none, false
	}

	ctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()

	line, err := in.qemuImg(ctx, path)
	if err != nil {
		return none, false
	}

	local, ok := parseQemuVersion(line)
	if !ok {
		return none, false
	}

	var (
		items []string
		count int
	)

	for _, image := range images {
		before := len(items)

		for _, need := range image.Needs {
			if want, ok := parseQemuVersion("version " + need.Version); ok && olderQemu(local, want) {
				items = append(items, fmt.Sprintf("%s (qemu %s or later for %s)", image.Path, need.Version, need.Feature))
			}
		}

		if len(items) > before {
			count++
		}
	}

	if count == 0 {
		return none, false
	}

	verb := "need"
	if count == 1 {
		verb = "needs"
	}

	return warnCheck(fmt.Sprintf("%s%d.%d, and %d of the images in %s %s a later qemu: %s",
		qemuNeedsPrefix, local[0], local[1], count, dir, verb, shortList(items))), true
}

func hasNeeds(images []disk.FormatImage) bool {
	for _, image := range images {
		if len(image.Needs) > 0 {
			return true
		}
	}

	return false
}

// parseQemuVersion reads the major and minor version from text such as "qemu-img version 8.2.2 (Debian ...)".
func parseQemuVersion(text string) ([2]int, bool) {
	m := qemuVersionPattern.FindStringSubmatch(text)
	if m == nil {
		return [2]int{}, false
	}

	major, errMajor := strconv.Atoi(m[1])
	minor, errMinor := strconv.Atoi(m[2])

	return [2]int{major, minor}, errMajor == nil && errMinor == nil
}

// olderQemu reports whether version a is older than version b.
func olderQemu(a, b [2]int) bool {
	return a[0] < b[0] || a[0] == b[0] && a[1] < b[1]
}

// diskFormatsHint is what to do about a disk-formats row that warns.
func diskFormatsHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case strings.HasPrefix(c.Detail, qemuNeedsPrefix):
		return "update qemu on each node, or convert each image with a qemu-img that knows its features: " +
			"qemu-img convert -O qcow2 -o compat=1.1 <image> <new image>"
	case strings.Contains(c.Detail, "within"):
		return "increase --timeout to check every image"
	case strings.HasPrefix(c.Detail, "cannot list"):
		return "run phenix doctor as a user that can read the images folder"
	}

	var hints []string

	if strings.Contains(c.Detail, "("+qcow2V2Label+")") {
		hints = append(hints, "upgrade each version 2 image with qemu-img amend -f qcow2 -o compat=1.1 <image>")
	}

	if strings.Contains(c.Detail, "("+qcowV1Label+")") {
		hints = append(hints, "convert each qcow version 1 image with qemu-img convert -f qcow -O qcow2 <image> <new image>")
	}

	if len(hints) == 0 {
		return "see " + formatsDocs + " for the fix of each format"
	}

	hints = append(hints, "see "+formatsDocs+" for the other fixes")

	for i := 1; i < len(hints); i++ {
		hints[i] = strings.ToUpper(hints[i][:1]) + hints[i][1:]
	}

	return strings.Join(hints, ". ")
}
