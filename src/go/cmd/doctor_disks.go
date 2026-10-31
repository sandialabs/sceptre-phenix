package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"phenix/api/disk"
)

const (
	diskChainsName = "disk chains"

	// maxListedImages is how many broken images the disk-chains row names before it counts the rest.
	maxListedImages = 5
)

// imagesDir is the folder phenix keeps disk images in: minimega's -filepath, as minimega-env read it, or
// <base-dir.phenix>/images when minimega gives no absolute one, as phenix does.
func (in *doctorInput) imagesDir() string {
	if dir := in.found.minimegaArgs["filepath"]; filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}

	return filepath.Join(in.text("base-dir.phenix"), "images")
}

// diskChainsCheck warns about the disk images in the images folder whose backing chain is broken, reading only image
// headers for at most the doctor timeout.
func diskChainsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	dir := in.imagesDir()

	if _, err := in.env.stat(dir); errors.Is(err, os.ErrNotExist) {
		return doctorCheck{Name: "", Status: doctorSkip, Detail: "not checked, because " + dir + " does not exist"}
	}

	ctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()

	scan, err := in.images(ctx, dir)

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return warnCheck(fmt.Sprintf("did not list the images in %s within %s", dir, in.timeout))
	case err != nil:
		var perr *fs.PathError
		if errors.As(err, &perr) {
			err = perr.Err
		}

		return warnCheck(fmt.Sprintf("cannot list %s: %v", dir, err))
	}

	broken := brokenImagesList(scan.Broken)

	switch {
	case scan.Checked < scan.Images && len(scan.Broken) == 0:
		return warnCheck(fmt.Sprintf("checked %d of %d images in %s within %s, and found none broken",
			scan.Checked, scan.Images, dir, in.timeout))
	case scan.Checked < scan.Images:
		return warnCheck(fmt.Sprintf("checked %d of %d images in %s within %s, and found %d broken: %s",
			scan.Checked, scan.Images, dir, in.timeout, len(scan.Broken), broken))
	case len(scan.Broken) == 1:
		return warnCheck(fmt.Sprintf("1 of %s in %s has a broken backing chain: %s",
			countOf(scan.Images, "image"), dir, broken))
	case len(scan.Broken) > 1:
		return warnCheck(fmt.Sprintf("%d of %s in %s have a broken backing chain: %s",
			len(scan.Broken), countOf(scan.Images, "image"), dir, broken))
	case scan.Images == 0:
		return passCheck("no images in " + dir)
	}

	return passCheck(fmt.Sprintf("%s in %s, and every backing chain is whole", countOf(scan.Images, "image"), dir))
}

// brokenImagesList names each image with its problem, at most maxListedImages of them, and counts the rest.
func brokenImagesList(images []disk.BrokenImage) string {
	items := make([]string, 0, min(len(images), maxListedImages)+1)

	for i, image := range images {
		if i == maxListedImages {
			items = append(items, fmt.Sprintf("and %d more", len(images)-maxListedImages))

			break
		}

		items = append(items, fmt.Sprintf("%s (%s)", image.Path, image.Problem))
	}

	return strings.Join(items, ", ")
}

// countOf is n with word, plural unless n is 1.
func countOf(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}

	return fmt.Sprintf("%d %ss", n, word)
}

// diskChainsHint is what to do about a disk-chains row that warns.
func diskChainsHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case strings.Contains(c.Detail, "within"):
		return "increase --timeout to check every image"
	case strings.HasPrefix(c.Detail, "cannot list"):
		return "run phenix doctor as a user that can read the images folder"
	}

	return "restore missing backing files, rebase the image onto one that exists " +
		"(Rebase on the Disks page), or delete the image"
}
