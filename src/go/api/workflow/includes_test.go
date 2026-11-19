package workflow_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/types"
)

// includesFile writes a topology file name that includes each of includes and
// returns its path.
func includesFile(t *testing.T, dir, name string, includes ...string) string {
	t.Helper()

	text := "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: " + name + "\nspec:\n"
	if len(includes) > 0 {
		text += "  includeTopologies:\n"
		var textSb25 strings.Builder
		for _, include := range includes {
			textSb25.WriteString("  - " + include + "\n")
		}
		text += textSb25.String()
	}

	text += "  nodes: []\n"

	path := filepath.Join(dir, name+".yml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

// TestReadIncludes checks which files the walk reads, and how often, with a
// stored topology, a pending topology and a pending include that is stored.
func TestReadIncludes(t *testing.T) {
	dir := t.TempDir()

	var (
		leaf    = includesFile(t, dir, "leaf")
		other   = includesFile(t, dir, "other")
		cycle   = filepath.Join(dir, "cycle.yml")
		mid     = includesFile(t, dir, "mid", leaf, other, leaf)
		missing = filepath.Join(dir, "missing.yml")
		spec    = workflow.Spec{Topology: "foo"}
	)

	if err := os.WriteFile(cycle, []byte("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: cycle\n"+
		"spec:\n  includeTopologies:\n  - "+cycle+"\n  - "+leaf+"\n  nodes: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stored := func(name string, includes ...string) store.Config {
		list := make([]any, len(includes))
		for i, include := range includes {
			list[i] = include
		}

		return store.Config{
			Version: "phenix.sandia.gov/v1", Kind: "Topology", Metadata: store.ConfigMetadata{Name: name},
			Spec: map[string]any{"includeTopologies": list, "nodes": []any{}},
		}
	}

	tests := []struct {
		name    string
		stored  []store.Config
		pending []string
		want    []string
	}{
		{name: "stored topology", stored: []store.Config{stored("foo", mid, missing)}, want: []string{mid, leaf, other}},
		{
			name: "pending topology", pending: []string{"Topology/foo?include=" + mid + "&include=" + mid},
			want: []string{mid, leaf, other},
		},
		{
			name: "pending include of pending include", pending: []string{"Topology/foo?include=inc", "Topology/inc?include=" + mid},
			want: []string{mid, leaf, other},
		},
		{
			name: "stored include of pending topology", stored: []store.Config{stored("inc", leaf)},
			pending: []string{"Topology/foo?include=inc"}, want: []string{leaf},
		},
		{
			name:    "pending replaces stored",
			stored:  []store.Config{stored("foo", mid)},
			pending: []string{"Topology/foo?include=" + leaf},
			want:    []string{leaf},
		},
		{name: "cycle", pending: []string{"Topology/foo?include=" + cycle}, want: []string{cycle, leaf}},
		{name: "no topology"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := installPrepareStore(t)
			m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
				for _, cfg := range tt.stored {
					if c.Kind == cfg.Kind && c.Metadata.Name == cfg.Metadata.Name {
						*c = cfg

						return nil
					}
				}

				return store.ErrNotExist
			}).AnyTimes()

			var got []string

			read := func(path string, text []byte) ([]byte, error) {
				got = append(got, path)

				return text, nil
			}

			s := spec
			if tt.name == "no topology" {
				s = workflow.Spec{}
			}

			if err := workflow.ReadIncludes(s, nil, read, tt.pending...); err != nil {
				t.Fatalf("ReadIncludes() unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ReadIncludes() files read mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestReadIncludesErrors checks that an error of the reader, not a missing or
// undecodable file, stops the walk, and that it wraps ErrUnresolved.
func TestReadIncludesErrors(t *testing.T) {
	dir := t.TempDir()

	var (
		leaf    = includesFile(t, dir, "leaf")
		mid     = includesFile(t, dir, "mid", leaf)
		garbage = filepath.Join(dir, "garbage.yml")
		errRead = errors.New("variable holds a line break")
	)

	if err := os.WriteFile(garbage, []byte("not: [a topology"), 0o600); err != nil {
		t.Fatal(err)
	}

	installPrepareStore(t).EXPECT().Get(gomock.Any()).Return(store.ErrNotExist).AnyTimes()

	reads := func(path string, text []byte) ([]byte, error) {
		if path == leaf {
			return nil, errRead
		}

		return text, nil
	}

	var read types.IncludeReader = reads

	err := workflow.ReadIncludes(workflow.Spec{Topology: "foo"}, nil, read, "Topology/foo?include="+mid)
	if !errors.Is(err, workflow.ErrUnresolved) || !errors.Is(err, errRead) {
		t.Errorf("ReadIncludes() error = %v, want it to wrap ErrUnresolved and the error of the reader", err)
	}

	err = workflow.ReadIncludes(workflow.Spec{Topology: "foo"}, nil, read, "Topology/foo?include="+garbage+"&include="+dir+"/none.yml")
	if err != nil {
		t.Errorf("ReadIncludes() with an undecodable and a missing file: unexpected error %v", err)
	}
}
