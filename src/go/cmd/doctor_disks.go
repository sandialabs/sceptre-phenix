package cmd

import (
	"fmt"
	"strings"
)

const (
	// maxListedImages is how many broken images the disk-chains row names before it counts the rest.
	maxListedImages = 5
)

// shortList joins the first maxListedImages items with commas, and counts the rest.
func shortList(items []string) string {
	if len(items) > maxListedImages {
		items = append(items[:maxListedImages:maxListedImages], fmt.Sprintf("and %d more", len(items)-maxListedImages))
	}

	return strings.Join(items, ", ")
}
