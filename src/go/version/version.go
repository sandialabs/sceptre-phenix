package version

import (
	"regexp"
	"strings"
)

const upstreamRepo = "sandialabs/sceptre-phenix"

var (
	Commit = unsetCommit          //nolint:gochecknoglobals // build info
	Tag    = "tag not set"        //nolint:gochecknoglobals // build info
	Date   = "build date not set" //nolint:gochecknoglobals // build info
	Repo   = "repo not set"       //nolint:gochecknoglobals // build info
)

var (
	releaseVersionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	notBuildMetadata      = regexp.MustCompile(`[^0-9A-Za-z-]+`)
)

const (
	unsetCommit = "commit hash not set"
	// devVersion is API's version of a build that is not a release.
	devVersion = "0.0.0-dev"
)

// Label identifies whether the build came from a release or a branch.
func Label() string {
	if releaseVersionPattern.MatchString(Tag) {
		return "Version " + Tag
	}

	if Repo != "" && Repo != "repo not set" && Repo != upstreamRepo {
		return "Branch " + Tag + " [" + strings.ToLower(Repo) + "]"
	}

	return "Branch " + Tag
}

// API names the build in openapi.yml's info.version and the X-Phenix-Version
// header: a release's version without its "v", else 0.0.0-dev+<commit>.
func API() string {
	if releaseVersionPattern.MatchString(Tag) {
		return strings.TrimPrefix(Tag, "v")
	}

	commit := strings.Trim(notBuildMetadata.ReplaceAllString(Commit, "-"), "-")
	if Commit == unsetCommit || commit == "" {
		return devVersion
	}

	return devVersion + "+" + commit
}
