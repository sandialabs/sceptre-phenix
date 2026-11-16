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
	diskChainsName  = "disk chains"
	diskFormatsName = "disk formats"

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

// doctorImageScan is one scan of the images folder, which disk-chains and disk-formats share.
type doctorImageScan struct {
	dir  string
	scan disk.ChainScan
	err  error
}

// scanImages scans the images folder (see disk.BrokenImages) for at most the doctor timeout, once in a run: a second
// call returns the first scan. It returns a row that ends the check when the folder does not exist or the scan
// failed.
func (in *doctorInput) scanImages(ctx context.Context) (string, disk.ChainScan, *doctorCheck) {
	dir := in.imagesDir()

	if _, err := in.env.stat(dir); errors.Is(err, os.ErrNotExist) {
		return dir, disk.ChainScan{}, &doctorCheck{Name: "", Status: doctorSkip, Detail: "not checked, because " + dir + " does not exist"}
	}

	var s *doctorImageScan
	if in.found != nil && in.found.images != nil && in.found.images.dir == dir {
		s = in.found.images
	} else {
		ctx, cancel := context.WithTimeout(ctx, in.timeout)
		defer cancel()

		scan, err := in.images(ctx, dir)
		s = &doctorImageScan{dir: dir, scan: scan, err: err}

		if in.found != nil {
			in.found.images = s
		}
	}

	switch {
	case errors.Is(s.err, context.DeadlineExceeded):
		check := warnCheck(fmt.Sprintf("did not list the images in %s within %s", dir, in.timeout))

		return dir, s.scan, &check
	case s.err != nil:
		err := s.err

		var perr *fs.PathError
		if errors.As(err, &perr) {
			err = perr.Err
		}

		check := warnCheck(fmt.Sprintf("cannot list %s: %v", dir, err))

		return dir, s.scan, &check
	}

	return dir, s.scan, nil
}

// diskChainsCheck warns about the disk images in the images folder whose backing chain is broken, reading only image
// headers for at most the doctor timeout.
func diskChainsCheck(ctx context.Context, in *doctorInput) doctorCheck {
	dir, scan, stop := in.scanImages(ctx)
	if stop != nil {
		return *stop
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
			imageCount(scan.Images), dir, broken))
	case len(scan.Broken) > 1:
		return warnCheck(fmt.Sprintf("%d of %s in %s have a broken backing chain: %s",
			len(scan.Broken), imageCount(scan.Images), dir, broken))
	case scan.Images == 0:
		return passCheck("no images in " + dir)
	}

	return passCheck(fmt.Sprintf("%s in %s, and every backing chain is whole", imageCount(scan.Images), dir))
}

// brokenImagesList names each image with its problem, at most maxListedImages of them, and counts the rest.
func brokenImagesList(images []disk.BrokenImage) string {
	items := make([]string, 0, len(images))
	for _, image := range images {
		items = append(items, fmt.Sprintf("%s (%s)", image.Path, image.Problem))
	}

	return shortList(items)
}

// shortList joins the first maxListedImages items with commas, and counts the rest.
func shortList(items []string) string {
	if len(items) > maxListedImages {
		items = append(items[:maxListedImages:maxListedImages], fmt.Sprintf("and %d more", len(items)-maxListedImages))
	}

	return strings.Join(items, ", ")
}

// imageCount is n with the word image, plural unless n is 1.
func imageCount(n int) string {
	if n == 1 {
		return "1 image"
	}

	return fmt.Sprintf("%d images", n)
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
