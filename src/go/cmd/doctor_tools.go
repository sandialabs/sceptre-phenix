package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"phenix/web"
)

const (
	// The executables phenix runs for user apps and SCORCH components.
	userAppPrefix         = "phenix-app-"
	scorchComponentPrefix = "phenix-scorch-component-"
)

// autoFeatures are the ui.features phenix ui turns on by itself.
//
//nolint:gochecknoglobals // constant list
var autoFeatures = []string{web.FeatureTunnelerDownload, web.FeatureFileServer}

// relativeToCwd names path, relative to the working directory cwd when it is relative.
func relativeToCwd(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}

	return fmt.Sprintf("%s (under %s)", path, cwd)
}

// unknownFeatureDetail explains a ui.features name phenix ui does not know,
// and suggests the one it may misspell.
func unknownFeatureDetail(feature string) string {
	detail := fmt.Sprintf("unknown feature %q", feature)

	best, bestDistance := "", maxEnvSuggestDistance+1

	for _, known := range append([]string{web.FeatureVMMount}, autoFeatures...) {
		if d := editDistance(feature, known); d < bestDistance {
			best, bestDistance = known, d
		}
	}

	if best != "" {
		detail += " (did you mean " + best + "?)"
	}

	if strings.Contains(feature, ",") {
		detail += ". Separate features with spaces, or list them in YAML"
	}

	return detail
}
