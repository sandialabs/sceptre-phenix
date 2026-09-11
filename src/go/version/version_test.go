package version

import "testing"

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
