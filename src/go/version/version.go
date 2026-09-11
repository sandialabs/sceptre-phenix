package version

import (
	"regexp"
	"strings"
)

const upstreamRepo = "sandialabs/sceptre-phenix"

var (
	Commit = "commit hash not set" //nolint:gochecknoglobals // build info
	Tag    = "tag not set"         //nolint:gochecknoglobals // build info
	Date   = "build date not set"  //nolint:gochecknoglobals // build info
	Repo   = "repo not set"        //nolint:gochecknoglobals // build info
)

var releaseVersionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

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
