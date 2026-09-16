package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/store"
)

func TestBuilderXML(t *testing.T) {
	t.Run("embedded", func(t *testing.T) {
		cfg := store.Config{
			Metadata: store.ConfigMetadata{
				Annotations: store.Annotations{BuilderXMLAnnotation: "<mxGraphModel/>"},
			},
		}

		got, err := BuilderXML(cfg)
		if err != nil {
			t.Fatalf("BuilderXML() returned error: %v", err)
		}
		if string(got) != "<mxGraphModel/>" {
			t.Fatalf("BuilderXML() = %q, want embedded XML", got)
		}
	})

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "topology.xml")
		if err := os.WriteFile(path, []byte("<mxGraphModel/>"), 0o600); err != nil {
			t.Fatalf("writing builder XML: %v", err)
		}

		cfg := store.Config{
			Metadata: store.ConfigMetadata{
				Annotations: store.Annotations{BuilderXMLFileAnnotation: path},
			},
		}

		got, err := BuilderXML(cfg)
		if err != nil {
			t.Fatalf("BuilderXML() returned error: %v", err)
		}
		if string(got) != "<mxGraphModel/>" {
			t.Fatalf("BuilderXML() = %q, want file contents", got)
		}
	})

	t.Run("annotations are mutually exclusive", func(t *testing.T) {
		cfg := store.Config{
			Metadata: store.ConfigMetadata{
				Annotations: store.Annotations{
					BuilderXMLAnnotation:     "<mxGraphModel/>",
					BuilderXMLFileAnnotation: "topology.xml",
				},
			},
		}

		_, err := BuilderXML(cfg)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("BuilderXML() error = %v, want mutually exclusive error", err)
		}
	})
}

func TestNormalizeBuilderXMLFilePath(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "topology.yaml")
	cfg := store.Config{
		Kind: "Topology",
		Metadata: store.ConfigMetadata{
			Annotations: store.Annotations{BuilderXMLFileAnnotation: "builder/topology.xml"},
		},
	}

	if err := normalizeBuilderXMLFilePath(&cfg, source); err != nil {
		t.Fatalf("normalizeBuilderXMLFilePath() returned error: %v", err)
	}

	want := filepath.Join(dir, "builder", "topology.xml")
	if got := cfg.Metadata.Annotations[BuilderXMLFileAnnotation]; got != want {
		t.Fatalf("normalized path = %q, want %q", got, want)
	}
}

func TestWriteBuilderXMLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.xml")
	if err := os.WriteFile(path, []byte("<mxGraphModel/>"), 0o640); err != nil {
		t.Fatalf("writing builder XML: %v", err)
	}

	cfg := store.Config{
		Metadata: store.ConfigMetadata{
			Annotations: store.Annotations{BuilderXMLFileAnnotation: path},
		},
	}

	want := "<mxGraphModel><root/></mxGraphModel>"
	if err := WriteBuilderXMLFile(cfg, []byte(want)); err != nil {
		t.Fatalf("WriteBuilderXMLFile() returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading builder XML: %v", err)
	}
	if string(got) != want {
		t.Fatalf("builder XML = %q, want %q", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("statting builder XML: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("permissions = %o, want 640", got)
	}
}

func TestCreateFromPathNormalizesBuilderXMLFile(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "builder", "topology.xml")
	if err := os.Mkdir(filepath.Dir(xmlPath), 0o750); err != nil {
		t.Fatalf("creating builder directory: %v", err)
	}
	if err := os.WriteFile(xmlPath, []byte("<mxGraphModel/>"), 0o640); err != nil {
		t.Fatalf("writing builder XML: %v", err)
	}

	configPath := filepath.Join(dir, "topology.yaml")
	configYAML := `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: test
  annotations:
    builder-xml-file: ./builder/topology.xml
spec:
  nodes: []
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("writing topology config: %v", err)
	}

	ctrl := gomock.NewController(t)
	mock := store.NewMockStore(ctrl)
	mock.EXPECT().Create(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		if got := cfg.Metadata.Annotations[BuilderXMLFileAnnotation]; got != xmlPath {
			t.Fatalf("stored builder XML path = %q, want %q", got, xmlPath)
		}

		return nil
	})

	previousStore := store.DefaultStore
	store.DefaultStore = mock
	t.Cleanup(func() {
		store.DefaultStore = previousStore
	})

	if _, err := Create(CreateFromPath(configPath)); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
}
