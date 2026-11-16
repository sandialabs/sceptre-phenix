package cmd

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
)

func TestMatchDeletePattern(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		pattern, name string
		want          bool
	}{
		{pattern: "", name: "", want: true},
		{pattern: "", name: "a", want: false},
		{pattern: "*", name: "", want: true},
		{pattern: "*", name: "anything at all", want: true},
		{pattern: "**", name: "", want: true},
		{pattern: "cm-*", name: "cm-", want: true},
		{pattern: "cm-*", name: "cm-site", want: true},
		{pattern: "cm-*", name: "xcm-site", want: false},
		{pattern: "cm-*", name: "CM-site", want: false},
		{pattern: "*-site", name: "cm-site", want: true},
		{pattern: "*-site", name: "cm-sites", want: false},
		{pattern: "cm*site", name: "cmsite", want: true},
		{pattern: "cm*site", name: "cm-big-site", want: true},
		{pattern: "cm*site", name: "cm-big-sit", want: false},
		{pattern: "ab*ba", name: "aba", want: false},
		{pattern: "a*b*c", name: "abc", want: true},
		{pattern: "a*b*c", name: "a-c-b-c", want: true},
		{pattern: "a*b*c", name: "acb", want: false},
		{pattern: "*a*a*", name: "banana", want: true},
		{pattern: "*a*a*", name: "bna", want: false},
		{pattern: "site?", name: "site?", want: true},
		{pattern: "site?", name: "site1", want: false},
		{pattern: "[ab]*", name: "[ab]-1", want: true},
		{pattern: "[ab]*", name: "a-1", want: false},
		{pattern: `a\*`, name: `a\b`, want: true},
		{pattern: `a\*`, name: "a*", want: false},
		{pattern: "día-*", name: "día-1", want: true},
	} {
		if got := matchDeletePattern(tt.pattern, tt.name); got != tt.want {
			t.Errorf("matchDeletePattern(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

// deletePatternRegexp is an oracle for matchDeletePattern: each * is .* and
// every other character matches only itself.
func deletePatternRegexp(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}

	return regexp.MustCompile(`^(?s:` + strings.Join(parts, ".*") + `)$`)
}

// matchDeletePattern agrees with a regular expression in which each * is .*.
func TestMatchDeletePatternOracle(t *testing.T) {
	t.Parallel()

	text := rapid.StringMatching(`[ab*?\[\\]{0,8}`)

	rapid.Check(t, func(t *rapid.T) {
		pattern := text.Draw(t, "pattern")
		name := strings.ReplaceAll(text.Draw(t, "name"), "*", "")

		if got, want := matchDeletePattern(pattern, name), deletePatternRegexp(pattern).MatchString(name); got != want {
			t.Fatalf("matchDeletePattern(%q, %q) = %v, want %v", pattern, name, got, want)
		}
	})
}

// A name matches each pattern made from it by putting * in place of some of
// its parts.
func TestMatchDeletePatternFromName(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[a-z?\[\\.-]{0,12}`).Draw(t, "name")

		var pattern strings.Builder

		for i := 0; i < len(name); {
			n := rapid.IntRange(1, len(name)-i).Draw(t, "length")
			if rapid.Bool().Draw(t, "wildcard") {
				pattern.WriteString("*")
			} else {
				pattern.WriteString(name[i : i+n])
			}

			i += n
		}

		if !matchDeletePattern(pattern.String(), name) {
			t.Fatalf("matchDeletePattern(%q, %q) = false, want true", pattern.String(), name)
		}
	})
}

// deletePatternStore stores the configs names, each as kind/name, and the
// experiment running, which runs.
func deletePatternStore(t *testing.T, names ...string) {
	t.Helper()

	storetest.UseBolt(t)
	storeConfigTestConfigs(t, names...)

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "running"},
		Spec:     map[string]any{"experimentName": "running", "topology": map[string]any{"nodes": []any{}}},
		Status:   map[string]any{"startTime": "2026-01-01T00:00:00Z"},
	}

	if err := store.Create(&c); err != nil {
		t.Fatalf("store.Create(experiment running) error = %v", err)
	}
}

// deletePatternLeft returns the full names of the stored configs, sorted.
func deletePatternLeft(t *testing.T) []string {
	t.Helper()

	configs, err := config.List(allExperiments)
	if err != nil {
		t.Fatalf("config.List(all) error = %v", err)
	}

	names := make([]string, 0, len(configs))
	for _, c := range configs {
		names = append(names, deleteKey(c.Kind, c.Metadata.Name))
	}

	slices.Sort(names)

	return names
}

// config delete deletes the configs that each pattern selects, once, after
// it names them, and skips the config of a running experiment.
func TestConfigDeletePatterns(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	stored := []string{"topology/cm-b", "topology/cm-a", "topology/site", "image/cm-disk", "user/alice"}
	all := slices.Sorted(slices.Values(append([]string{"experiment/running"}, stored...)))

	tests := []struct {
		name    string
		args    []string
		want    string // the output
		wantErr string // the prefix of the error
		left    []string
		dryAll  bool // the output ends with the dry run lines of */*
	}{
		{
			name: "pattern", args: []string{"delete", "topology/cm-*"},
			want: "2 configurations match topology/cm-*:\n  topology/cm-a\n  topology/cm-b\n",
			left: []string{"experiment/running", "image/cm-disk", "topology/site", "user/alice"},
		},
		{
			name: "kind in capitals", args: []string{"delete", "TOPOLOGY/*site"},
			want: "1 configuration matches TOPOLOGY/*site:\n  topology/site\n",
			left: []string{"experiment/running", "image/cm-disk", "topology/cm-a", "topology/cm-b", "user/alice"},
		},
		{
			name: "no match", args: []string{"delete", "topology/zz-*", "topology/site"},
			wantErr: "Unable to delete topology/zz-*: the pattern matches no configuration",
			left:    []string{"experiment/running", "image/cm-disk", "topology/cm-a", "topology/cm-b", "user/alice"},
		},
		{
			name: "names are case-sensitive", args: []string{"delete", "topology/CM-*"},
			wantErr: "Unable to delete topology/CM-*: the pattern matches no configuration", left: all,
		},
		{
			name: "exact name and pattern, with a duplicate",
			args: []string{"delete", "topology/cm-a", "topology/cm-*", "topology/cm-b", "image/cm-disk"},
			want: "1 configuration matches topology/cm-*:\n  topology/cm-b\n",
			left: []string{"experiment/running", "topology/site", "user/alice"},
		},
		{
			name: "running experiment", args: []string{"delete", "experiment/*"},
			want:    "1 configuration matches experiment/*:\n  experiment/running\n",
			wantErr: "Unable to delete the experiment/running configuration: the experiment is running", left: all,
		},
		{
			name: "every config of a kind", args: []string{"delete", "topology/*"},
			want: "3 configurations match topology/*:\n  topology/cm-a\n  topology/cm-b\n  topology/site\n",
			left: []string{"experiment/running", "image/cm-disk", "user/alice"},
		},
		{
			name: "every config of a kind that has none", args: []string{"delete", "scenario/*"}, left: all,
		},
		{
			name: "every config", args: []string{"delete", "*/*"},
			want: "6 configurations match */*:\n  experiment/running\n  image/cm-disk\n" +
				"  topology/cm-a\n  topology/cm-b\n  topology/site\n  user/alice\n",
			wantErr: "Unable to delete the experiment/running configuration: the experiment is running",
			left:    []string{"experiment/running"},
		},
		{
			name: "dry run", args: []string{"delete", "topology/cm-*", "image/cm-disk", "--dry-run"},
			want: "2 configurations match topology/cm-*:\n  topology/cm-a\n  topology/cm-b\n" +
				"would delete configuration topology/cm-a\nwould delete configuration topology/cm-b\n" +
				"would delete configuration image/cm-disk\n",
			left: all,
		},
		{
			name: "dry run, every config", args: []string{"delete", "*/*", "--dry-run"},
			want: "6 configurations match */*:\n  experiment/running\n  image/cm-disk\n" +
				"  topology/cm-a\n  topology/cm-b\n  topology/site\n  user/alice\n",
			wantErr: "Unable to delete the experiment/running configuration: the experiment is running",
			left:    all, dryAll: true,
		},
		{
			name: "wildcard in the kind", args: []string{"delete", "topology/site", "top*/cm-a"},
			wantErr: "the configuration kind cannot contain * (use */* for all configurations), received top*/cm-a",
			left:    all,
		},
		{
			name: "wildcard kind with an exact name", args: []string{"delete", "*/site"},
			wantErr: "the configuration kind cannot contain *", left: all,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deletePatternStore(t, stored...)

			out, err := runConfigTestCommand(tt.args...)
			if got := errorText(err); (tt.wantErr == "") != (got == "") || !strings.HasPrefix(got, tt.wantErr) {
				t.Errorf("config %q error = %v, want %q", tt.args, err, tt.wantErr)
			}

			want := tt.want
			if tt.dryAll {
				want += deletePatternDryRunLines(t)
			}

			if diff := cmp.Diff(want, out); diff != "" {
				t.Errorf("config %q output mismatch (-want +got):\n%s", tt.args, diff)
			}

			if diff := cmp.Diff(tt.left, deletePatternLeft(t)); diff != "" {
				t.Errorf("configs after config %q mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// deletePatternDryRunLines are the lines of a dry run of config delete */*:
// one for each stored config but the running experiment, in the order of
// config.List.
func deletePatternDryRunLines(t *testing.T) string {
	t.Helper()

	configs, err := config.List(allExperiments)
	if err != nil {
		t.Fatalf("config.List(all) error = %v", err)
	}

	var lines strings.Builder

	for _, c := range configs {
		if c.Kind != "Experiment" {
			lines.WriteString("would delete configuration " + deleteKey(c.Kind, c.Metadata.Name) + "\n")
		}
	}

	return lines.String()
}

// phenix image delete deletes the image configurations that each pattern
// selects, once, after it names them.
func TestImageCmdDeletePatterns(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	stored := []string{"image/cm-b", "image/cm-a", "image/kali?", "topology/cm-a"}
	all := slices.Sorted(slices.Values(append([]string{"experiment/running"}, stored...)))

	tests := []struct {
		name    string
		args    []string
		want    string // the output
		wantErr string // the prefix of the error
		left    []string
	}{
		{
			name: "pattern", args: []string{"delete", "cm-*"},
			want: "2 image configurations match cm-*:\n  cm-a\n  cm-b\n",
			left: []string{"experiment/running", "image/kali?", "topology/cm-a"},
		},
		{
			name: "question mark is not a wildcard", args: []string{"delete", "*?"},
			want: "1 image configuration matches *?:\n  kali?\n",
			left: []string{"experiment/running", "image/cm-a", "image/cm-b", "topology/cm-a"},
		},
		{
			name: "no match", args: []string{"delete", "zz-*", "cm-a"},
			wantErr: "Unable to delete zz-*: the pattern matches no configuration",
			left:    []string{"experiment/running", "image/cm-b", "image/kali?", "topology/cm-a"},
		},
		{
			name: "two failures", args: []string{"delete", "zz-*", "missing"},
			wantErr: "unable to delete 2 images:\n  Unable to delete zz-*: the pattern matches no configuration\n" +
				"  Unable to delete the missing image",
			left: all,
		},
		{
			name: "exact name and pattern, with a duplicate", args: []string{"delete", "cm-*", "cm-a", "cm-b"},
			want: "2 image configurations match cm-*:\n  cm-a\n  cm-b\n",
			left: []string{"experiment/running", "image/kali?", "topology/cm-a"},
		},
		{
			name: "every image", args: []string{"delete", "*"},
			want: "3 image configurations match *:\n  cm-a\n  cm-b\n  kali?\n",
			left: []string{"experiment/running", "topology/cm-a"},
		},
		{
			name: "dry run", args: []string{"delete", "kali?", "cm-*", "--dry-run"},
			want: "would delete image kali?\n2 image configurations match cm-*:\n  cm-a\n  cm-b\n" +
				"would delete image cm-a\nwould delete image cm-b\n",
			left: all,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deletePatternStore(t, stored...)

			out, err := imageCmdRun(tt.args...)
			if got := errorText(err); (tt.wantErr == "") != (got == "") || !strings.HasPrefix(got, tt.wantErr) {
				t.Errorf("phenix image %q error = %v, want %q", tt.args, err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, out); diff != "" {
				t.Errorf("phenix image %q output mismatch (-want +got):\n%s", tt.args, diff)
			}

			if diff := cmp.Diff(tt.left, deletePatternLeft(t)); diff != "" {
				t.Errorf("configs after phenix image %q mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}

	t.Run("every image, none stored", func(t *testing.T) {
		storetest.UseBolt(t)

		if out, err := imageCmdRun("delete", "*"); err != nil || out != "" {
			t.Errorf("phenix image delete * with no images = %q, %v, want no output and no error", out, err)
		}
	})
}

// A pattern argument does not change the completion of the next argument.
func TestDeletePatternCompletion(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	deletePatternStore(t, "topology/cm-a", "image/cm-b")

	got, _ := newConfigDeleteCmd().ValidArgsFunction(nil, []string{"topology/cm-*"}, "topology/")
	if diff := cmp.Diff([]string{"topology/cm-a"}, got); diff != "" {
		t.Errorf("config delete completion after a pattern mismatch (-want +got):\n%s", diff)
	}

	got, _ = newImageDeleteCmd().ValidArgsFunction(nil, []string{"*"}, "cm")
	if diff := cmp.Diff([]string{"cm-b"}, got); diff != "" {
		t.Errorf("image delete completion after a pattern mismatch (-want +got):\n%s", diff)
	}
}
