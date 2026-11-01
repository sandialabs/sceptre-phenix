package disk

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"phenix/util/diskchain"
	"phenix/util/mm"
)

// ChainScan is what BrokenImages found in a folder of disk images, from one walk of each image.
type ChainScan struct {
	// Images is how many images the folder holds, as the disk list finds them.
	Images int
	// Checked is how many of them had their backing chain walked in time.
	Checked int
	// Broken are the checked images qemu cannot open (see diskchain.Result.Broken), sorted by path.
	Broken []BrokenImage
	// Formats are the checked images whose own header shows an old format, a format phenix does not check, a reason
	// qemu refuses the image, or a feature that needs a recent qemu, sorted by path.
	Formats []FormatImage
	// Backing are the backing files that the walk of each checked image read, in chain order, by the image's path
	// relative to the folder scanned. Each backing file is a cleaned path. An image with no backing file is not here.
	Backing map[string][]string
}

// FormatImage is an image whose header shows an old or unsupported format, or a feature that needs a recent qemu.
type FormatImage struct {
	// Path is the image's path, relative to the folder scanned.
	Path string
	// Kind is the image's old or unsupported format: diskchain.QcowV1, Qcow2V2, QED or VMDK, or 0 for none.
	Kind diskchain.NoteKind
	// Problem is why qemu refuses the image, in one sentence with cleaned paths, or "": an unknown version or feature
	// bit, encryption, or a corrupt mark, which qemu refuses for a read/write open.
	Problem string
	// Needs are the features of the image that need a recent qemu.
	Needs []QemuNeed
}

// QemuNeed is a feature of an image that needs qemu Version or later.
type QemuNeed struct {
	Feature, Version string
}

// minDataFileQemu is the oldest qemu version that opens a qcow2 image with an external data file.
const minDataFileQemu = "4.0"

// BrokenImage is an image whose backing chain is broken.
type BrokenImage struct {
	// Path is the image's path, relative to the folder scanned.
	Path string
	// Problem is the problem in one sentence (see diskchain.Result.Sentence), with cleaned paths.
	Problem string
}

// BrokenImages walks the backing chain of each image in dir, as the disk list finds them (see imageFiles), reading
// only headers, until ctx is done. It never asks minimega anything. It fails when it cannot list dir, or does not
// finish the listing before ctx is done.
func BrokenImages(ctx context.Context, dir string) (ChainScan, error) {
	type listing struct {
		paths []string
		err   error
	}

	// buffered, so the listing exits on its own when ctx ends first
	listed := make(chan listing, 1)

	go func() {
		paths, err := imageFiles(dir)
		listed <- listing{paths: paths, err: err}
	}()

	var l listing

	select {
	case l = <-listed:
	case <-ctx.Done():
		return ChainScan{}, fmt.Errorf("listing %s: %w", dir, ctx.Err())
	}

	if l.err != nil {
		return ChainScan{}, l.err
	}

	results := diskchain.WalkAll(ctx, l.paths, nil)
	scan := ChainScan{Images: len(l.paths), Checked: len(results), Broken: nil, Formats: nil, Backing: nil}

	for path, res := range results {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = path
		}

		if res.Broken() {
			scan.Broken = append(scan.Broken, BrokenImage{Path: rel, Problem: res.MapPaths(mm.CleanBackingPath).Sentence()})
		}

		if image, ok := formatOf(path, res); ok {
			image.Path = rel
			scan.Formats = append(scan.Formats, image)
		}

		if len(res.Members) > 1 {
			if scan.Backing == nil {
				scan.Backing = make(map[string][]string)
			}

			for _, member := range res.Members[1:] {
				scan.Backing[rel] = append(scan.Backing[rel], filepath.Clean(member))
			}
		}
	}

	slices.SortFunc(scan.Broken, func(a, b BrokenImage) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(scan.Formats, func(a, b FormatImage) int { return strings.Compare(a.Path, b.Path) })

	return scan, nil
}

// formatOf returns what res, the walk of the image at path, says about the format of that image itself, and whether
// it is anything but a current format with no recent features.
func formatOf(path string, res diskchain.Result) (FormatImage, bool) {
	image := FormatImage{Path: path, Kind: 0, Problem: "", Needs: nil}
	clean := res.MapPaths(mm.CleanBackingPath)

	switch {
	case res.Refused() && res.At == path:
		image.Problem = clean.Sentence()
	case res.Problem == diskchain.DataFile && res.At == path:
		image.Needs = append(image.Needs, QemuNeed{Feature: "an external data file", Version: minDataFileQemu})
	}

	for i, note := range res.Notes {
		if note.Path != path {
			continue
		}

		switch note.Kind {
		case diskchain.QcowV1, diskchain.Qcow2V2, diskchain.QED, diskchain.VMDK:
			image.Kind = note.Kind
		case diskchain.Corrupt:
			image.Problem = clean.Notes[i].Sentence()
		case diskchain.Zstd, diskchain.ExtendedL2:
			version, feature := note.Kind.MinQemu()
			image.Needs = append(image.Needs, QemuNeed{Feature: feature, Version: version})
		case diskchain.Dirty:
		}
	}

	return image, image.Kind != 0 || image.Problem != "" || len(image.Needs) > 0
}
