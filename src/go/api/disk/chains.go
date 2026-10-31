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

// ChainScan is what BrokenImages found in a folder of disk images.
type ChainScan struct {
	// Images is how many images the folder holds, as the disk list finds them.
	Images int
	// Checked is how many of them had their backing chain walked in time.
	Checked int
	// Broken are the checked images qemu cannot open (see diskchain.Result.Broken), sorted by path.
	Broken []BrokenImage
}

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
	scan := ChainScan{Images: len(l.paths), Checked: len(results), Broken: nil}

	for path, res := range results {
		if !res.Broken() {
			continue
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = path
		}

		scan.Broken = append(scan.Broken, BrokenImage{Path: rel, Problem: res.MapPaths(mm.CleanBackingPath).Sentence()})
	}

	slices.SortFunc(scan.Broken, func(a, b BrokenImage) int { return strings.Compare(a.Path, b.Path) })

	return scan, nil
}
