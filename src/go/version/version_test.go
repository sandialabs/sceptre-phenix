package version

import (
	"regexp"
	"testing"

	"pgregory.net/rapid"
)

func TestLabel(t *testing.T) {
	originalRepo := Repo
	originalTag := Tag
	defer func() {
		Repo = originalRepo
		Tag = originalTag
	}()

	tests := []struct {
		name string
		repo string
		tag  string
		want string
	}{
		{name: "release", repo: upstreamRepo, tag: "v2026.08.26", want: "Version v2026.08.26"},
		{name: "upstream branch", repo: upstreamRepo, tag: "main", want: "Branch main"},
		{name: "fork branch", repo: "GhostofGoes/sceptre-phenix", tag: "main", want: "Branch main [ghostofgoes/sceptre-phenix]"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			Repo = test.repo
			Tag = test.tag
			if got := Label(); got != test.want {
				t.Fatalf("Label() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAPI(t *testing.T) {
	originalCommit, originalTag := Commit, Tag

	t.Cleanup(func() { Commit, Tag = originalCommit, originalTag })

	tests := []struct {
		name, tag, commit, want string
	}{
		{name: "release", tag: "v2026.08.26", commit: "abc1234", want: "2026.08.26"},
		{name: "prerelease", tag: "v1.2.3-rc.1", commit: "abc1234", want: "1.2.3-rc.1"},
		{name: "branch", tag: "main", commit: "abc1234", want: "0.0.0-dev+abc1234"},
		{name: "dirty branch", tag: "main", commit: "abc1234 (dirty)", want: "0.0.0-dev+abc1234-dirty"},
		{name: "unstamped", tag: "tag not set", commit: unsetCommit, want: "0.0.0-dev"},
		{name: "empty commit", tag: "main", commit: "", want: "0.0.0-dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Commit, Tag = tt.commit, tt.tag

			if got := API(); got != tt.want {
				t.Errorf("API() with tag %q and commit %q = %q, want %q", tt.tag, tt.commit, got, tt.want)
			}
		})
	}
}

// API is always a semantic version whose characters are safe in a header.
func TestAPIIsSemanticVersion(t *testing.T) {
	originalCommit, originalTag := Commit, Tag

	t.Cleanup(func() { Commit, Tag = originalCommit, originalTag })

	semver := regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

	rapid.Check(t, func(t *rapid.T) {
		Commit = rapid.String().Draw(t, "commit")
		Tag = rapid.OneOf(rapid.String(), rapid.StringMatching(`v\d{1,4}\.\d{1,2}\.\d{1,2}`)).Draw(t, "tag")

		if got := API(); !semver.MatchString(got) {
			t.Fatalf("API() with tag %q and commit %q = %q, want a semantic version", Tag, Commit, got)
		}
	})
}
