package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
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

// shortList joins the first maxListedImages items with commas, and counts the rest.
func shortList(items []string) string {
	if len(items) > maxListedImages {
		items = append(items[:maxListedImages:maxListedImages], fmt.Sprintf("and %d more", len(items)-maxListedImages))
	}

	return strings.Join(items, ", ")
}
