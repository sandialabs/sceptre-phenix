package builder_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types/builder"
)

func TestSourceDigestContract(t *testing.T) {
	config := loadConfig(t, "topology.json")

	digest, err := builder.SourceDigest(config)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}

	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("digest %q is not a sha256 digest", digest)
	}

	again, err := builder.SourceDigest(loadConfig(t, "topology.json"))
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}

	if again != digest {
		t.Fatalf("digest is not deterministic: %q != %q", again, digest)
	}

	// Mutable bookkeeping must not affect the digest.
	stable := loadConfig(t, "topology.json")
	stable.Metadata.Created = "2020-01-01T00:00:00Z"
	stable.Metadata.Updated = "2031-01-01T00:00:00Z"
	stable.Metadata.Annotations = store.Annotations{"note": "changed"}
	stable.Status = map[string]any{"phase": "whatever"}

	stableDigest, err := builder.SourceDigest(stable)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}

	if stableDigest != digest {
		t.Fatal("digest changed after mutating status, timestamps, or annotations")
	}

	// Identity and spec must affect the digest.
	for name, mutate := range map[string]func(*store.Config){
		"apiVersion": func(c *store.Config) { c.Version = "phenix.sandia.gov/v0" },
		"kind":       func(c *store.Config) { c.Kind = "Experiment" },
		"name":       func(c *store.Config) { c.Metadata.Name = "renamed" },
		"spec":       func(c *store.Config) { c.Spec["extra"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			changed := loadConfig(t, "topology.json")
			mutate(&changed)

			changedDigest, err := builder.SourceDigest(changed)
			if err != nil {
				t.Fatalf("SourceDigest: %v", err)
			}

			if changedDigest == digest {
				t.Fatalf("digest did not change after mutating %s", name)
			}
		})
	}
}

func TestFromConfigRecordsSourceDigestAndUpdatedAt(t *testing.T) {
	for _, fixture := range []string{"topology.json", "experiment.json"} {
		t.Run(fixture, func(t *testing.T) {
			config := loadConfig(t, fixture)
			config.Metadata.Updated = "2026-08-28T12:00:00Z"

			doc, _ := documentFromConfig(t, config)

			if doc.Source == nil {
				t.Fatal("generated document has no source")
			}

			want, err := builder.SourceDigest(config)
			if err != nil {
				t.Fatalf("SourceDigest: %v", err)
			}

			if doc.Source.Digest != want {
				t.Fatalf("source digest = %q, want %q", doc.Source.Digest, want)
			}

			if doc.Source.UpdatedAt != "2026-08-28T12:00:00Z" {
				t.Fatalf("source updatedAt = %q, want the config timestamp", doc.Source.UpdatedAt)
			}

			if doc.Source.ImportedAt != "" {
				t.Fatalf("source importedAt = %q, want it left to the caller", doc.Source.ImportedAt)
			}
		})
	}
}

func TestFromConfigKeepsAnnotationsButTheBuilders(t *testing.T) {
	config := loadConfig(t, "topology.json")

	plain, err := builder.SourceDigest(config)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}

	config.Metadata.Annotations = store.Annotations{
		"builder-xml":        "<mxGraphModel>" + strings.Repeat("<mxCell/>", 1000) + "</mxGraphModel>",
		"builder-doc":        `{"id":"doc"}`,
		"builder-experiment": `{"draft":"alice/d1"}`,
		"builder-notes":      "any other key of the Builders",
		"owner":              "alice",
		"notes":              "two\nlines",
	}

	doc, warnings := documentFromConfig(t, config)

	want := map[string]string{"owner": "alice", "notes": "two\nlines"}
	if !reflect.DeepEqual(doc.Source.Annotations, want) {
		t.Fatalf("annotations = %v, want %v", doc.Source.Annotations, want)
	}

	if containsSubstring(warnings, "annotation") {
		t.Fatalf("warnings = %q, want none about annotations", warnings)
	}

	// Annotations are not part of the source's identity.
	if doc.Source.Digest != plain {
		t.Fatalf("source digest = %q, want %q, the digest without annotations", doc.Source.Digest, plain)
	}

	// An experiment keeps the topology and scenario it names.
	experiment, _ := documentFromConfig(t, loadConfig(t, "experiment.json"))

	want = map[string]string{"topology": "builder-fixture", "scenario": "builder-scenario"}
	if !reflect.DeepEqual(experiment.Source.Annotations, want) {
		t.Fatalf("experiment annotations = %v, want %v", experiment.Source.Annotations, want)
	}

	// Only the Builders' own: none are kept, and the field is left out.
	config.Metadata.Annotations = store.Annotations{"builder-xml": "<mxGraphModel/>"}
	doc, _ = documentFromConfig(t, config)

	encoded, err := json.Marshal(doc.Source)
	if err != nil {
		t.Fatalf("encoding the source: %v", err)
	}

	if doc.Source.Annotations != nil || strings.Contains(string(encoded), `"annotations"`) {
		t.Fatalf("source = %s, want no annotations", encoded)
	}
}

func TestFromConfigLeavesOutAnnotationsPastTheBounds(t *testing.T) {
	config := loadConfig(t, "topology.json")
	annotations := store.Annotations{
		" ":        "blank key",
		"bad\nkey": "control character",
		// Sorted first, so it would take the whole size bound.
		"a-large":                        strings.Repeat("x", builder.MaxAnnotationBytes),
		strings.Repeat("k", 513):         "long key",
		"builder-xml":                    "not counted",
		"topology":                       "kept",
		"zz-" + strings.Repeat("9", 600): "long key, listed shortened",
	}

	for i := range builder.MaxAnnotations + 2 {
		annotations[fmt.Sprintf("n%03d", i)] = "value"
	}

	config.Metadata.Annotations = annotations

	doc, warnings := documentFromConfig(t, config)

	if got := len(doc.Source.Annotations); got != builder.MaxAnnotations {
		t.Fatalf("kept %d annotations, want %d", got, builder.MaxAnnotations)
	}

	// In key order: every n### but the last two, which are past the count.
	for _, key := range []string{"n000", "n099"} {
		if _, ok := doc.Source.Annotations[key]; !ok {
			t.Errorf("annotation %q was left out", key)
		}
	}

	for _, key := range []string{" ", "bad\nkey", "a-large", "n100", "n101", "topology", "builder-xml"} {
		if _, ok := doc.Source.Annotations[key]; ok {
			t.Errorf("annotation %q was kept", key)
		}
	}

	var found string

	for _, warning := range warnings {
		if strings.Contains(warning, "left out") {
			found = warning
		}
	}

	for _, part := range []string{
		`annotations " ", "a-large", "bad\nkey", "kkkk`, `"n100" and 3 more of the source config were left out`,
		"at most 100 annotations of 256 KiB in all",
	} {
		if !strings.Contains(found, part) {
			t.Errorf("warning %q does not contain %q", found, part)
		}
	}

	if strings.Contains(found, strings.Repeat("k", 100)) || strings.Contains(found, "builder-xml") {
		t.Errorf("warning %q names a key in full or the Builder's own", found)
	}
}

func TestValidateBoundsAnnotations(t *testing.T) {
	many := map[string]string{}
	for i := range builder.MaxAnnotations + 1 {
		many[fmt.Sprintf("n%03d", i)] = ""
	}

	tests := []struct {
		name        string
		annotations map[string]string
		wantMsg     string
	}{
		{"too many", many, "at most 100 annotations are allowed, not 101"},
		{
			"too large",
			map[string]string{"a": strings.Repeat("x", builder.MaxAnnotationBytes)},
			"annotations must take at most 262144 bytes in all, not 262145",
		},
		{"blank key", map[string]string{"\t": "x"}, `annotation key "\t" must not be blank`},
		{
			"long key",
			map[string]string{strings.Repeat("k", builder.MaxNameBytes+1): "x"},
			"must be at most 512 bytes",
		},
		{"control character", map[string]string{"a\x7fb": "x"}, "must not contain control characters"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			doc.Source.Annotations = test.annotations

			err := doc.Validate()
			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !strings.Contains(err.Error(), "source.annotations: ") || !strings.Contains(err.Error(), test.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.wantMsg)
			}
		})
	}

	// Right at the bounds, with a value of several lines.
	doc := loadDocumentFixture(t, "document.json")
	doc.Source.Annotations = map[string]string{
		strings.Repeat("k", builder.MaxNameBytes): strings.Repeat("x\n", (builder.MaxAnnotationBytes-builder.MaxNameBytes)/2),
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("annotations at the bounds were refused: %v", err)
	}
}

func TestValidateRejectsMalformedSourceDigest(t *testing.T) {
	for _, digest := range []string{
		"nope", "sha256:", "sha256:abc", "md5:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("A", 64),
	} {
		doc := loadDocumentFixture(t, "document.json")
		doc.Source.Digest = digest

		err := doc.Validate()
		if err == nil {
			t.Fatalf("digest %q was accepted", digest)
		}

		if !strings.Contains(err.Error(), "malformed source digest") {
			t.Fatalf("error %q does not report a malformed digest", err.Error())
		}
	}
}

func TestValidateAcceptsSourceDigestAndUpdatedAt(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	digest, err := builder.SourceDigest(loadConfig(t, "topology.json"))
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}

	doc.Source.Digest = digest
	doc.Source.UpdatedAt = "2026-08-28T12:00:00Z"

	if err := doc.Validate(); err != nil {
		t.Fatalf("document with a source digest is invalid: %v", err)
	}
}

func TestSourceDigestSurvivesRoundTrip(t *testing.T) {
	config := loadConfig(t, "experiment.json")

	doc, _ := documentFromConfig(t, config)

	encoded, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("encoding document: %v", err)
	}

	decoded, err := builder.Parse(encoded)
	if err != nil {
		t.Fatalf("parsing document: %v", err)
	}

	if decoded.Source.Digest != doc.Source.Digest {
		t.Fatalf("digest changed across a round trip: %q != %q",
			decoded.Source.Digest, doc.Source.Digest)
	}

	if decoded.Source.UpdatedAt != doc.Source.UpdatedAt {
		t.Fatalf("updatedAt changed across a round trip: %q != %q",
			decoded.Source.UpdatedAt, doc.Source.UpdatedAt)
	}
}

func TestValidateRequiresPositiveGeometry(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*builder.Document)
		wantMsg string
	}{
		{
			name:    "zero zoom",
			mutate:  func(d *builder.Document) { d.Viewport.Zoom = 0 },
			wantMsg: "zoom must be a positive number",
		},
		{
			name:    "zero grid size",
			mutate:  func(d *builder.Document) { d.Grid.Size = 0 },
			wantMsg: "grid size must be a positive finite number",
		},
		{
			name: "zero width",
			mutate: func(d *builder.Document) {
				d.Nodes[0].Size = &builder.Size{Width: 0, Height: 10}
			},
			wantMsg: "size values must be positive finite numbers",
		},
		{
			name: "zero height",
			mutate: func(d *builder.Document) {
				d.Nodes[0].Size = &builder.Size{Width: 10, Height: 0}
			},
			wantMsg: "size values must be positive finite numbers",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			test.mutate(doc)

			err := doc.Validate()
			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.wantMsg)
			}
		})
	}
}

func TestFromConfigRecordsScenarioAPIVersion(t *testing.T) {
	doc, _ := documentFromConfig(t, loadConfig(t, "experiment.json"))

	if doc.Scenario == nil {
		t.Fatal("generated document has no scenario reference")
	}

	if doc.Scenario.APIVersion != builder.ScenarioAPIVersion() {
		t.Fatalf("scenario apiVersion = %q, want %q",
			doc.Scenario.APIVersion, builder.ScenarioAPIVersion())
	}

	if !strings.HasPrefix(doc.Scenario.Digest, "sha256:") {
		t.Fatalf("scenario digest = %q", doc.Scenario.Digest)
	}

	if builder.ScenarioAPIVersion() == "" ||
		!strings.HasPrefix(builder.ScenarioAPIVersion(), "phenix.sandia.gov/") {
		t.Fatalf("unexpected scenario apiVersion %q", builder.ScenarioAPIVersion())
	}
}

func TestValidateAcceptsCompleteScenarioReferences(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	if doc.Scenario.Kind != builder.ScenarioRefStored {
		t.Fatalf("fixture scenario kind = %q", doc.Scenario.Kind)
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("stored scenario reference is invalid: %v", err)
	}

	// A stored reference may cache its content, as long as the digest matches.
	content := map[string]any{"apps": []any{map[string]any{"name": "ntp"}}}

	digest, err := builder.ContentDigest(content)
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}

	doc.Scenario.Content = content
	doc.Scenario.Digest = digest

	if err := doc.Validate(); err != nil {
		t.Fatalf("stored scenario reference with content is invalid: %v", err)
	}

	doc.Scenario = uploadedScenario(content)

	if err := doc.Validate(); err != nil {
		t.Fatalf("uploaded scenario reference is invalid: %v", err)
	}
}

func TestValidateChecksScenarioContentAgainstPhenixSchema(t *testing.T) {
	valid := map[string]any{
		"apps": []any{
			map[string]any{
				"name":     "ntp",
				"assetDir": "/phenix/topologies/example/assets",
				"disabled": false,
				"metadata": map[string]any{"setting0": true, "setting1": 42},
				"hosts": []any{
					map[string]any{
						"hostname": "router",
						"metadata": map[string]any{"server": true},
					},
				},
			},
		},
	}

	doc := loadDocumentFixture(t, "document.json")
	doc.Scenario = uploadedScenario(valid)

	if err := doc.Validate(); err != nil {
		t.Fatalf("complete v2 scenario content was rejected: %v", err)
	}

	// A cached stored reference is validated the same way.
	cached := loadDocumentFixture(t, "document.json")
	cached.Scenario.Content = valid

	digest, err := builder.ContentDigest(valid)
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}

	cached.Scenario.Digest = digest

	if err := cached.Validate(); err != nil {
		t.Fatalf("cached stored scenario content was rejected: %v", err)
	}
}

func TestValidateRejectsInvalidScenarioContent(t *testing.T) {
	tests := []struct {
		name    string
		content map[string]any
		wantMsg string
	}{
		{
			name:    "missing apps",
			content: map[string]any{"nope": true},
			wantMsg: "invalid scenario content",
		},
		{
			name:    "app without a name",
			content: map[string]any{"apps": []any{map[string]any{"assetDir": "/tmp"}}},
			wantMsg: "invalid scenario content",
		},
		{
			name: "host without a hostname",
			content: map[string]any{"apps": []any{map[string]any{
				"name":  "ntp",
				"hosts": []any{map[string]any{"metadata": map[string]any{"a": 1}}},
			}}},
			wantMsg: "invalid scenario content",
		},
		{
			name:    "app is not an object",
			content: map[string]any{"apps": []any{"ntp"}},
			wantMsg: "invalid scenario content",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			doc.Scenario = uploadedScenario(test.content)

			err := doc.Validate()
			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.wantMsg)
			}
		})
	}
}

func TestValidateRejectsUnsupportedScenarioAPIVersion(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	ref := uploadedScenario(map[string]any{"apps": []any{map[string]any{"name": "ntp"}}})
	ref.APIVersion = "phenix.sandia.gov/v1"
	doc.Scenario = ref

	err := doc.Validate()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), "unsupported scenario apiVersion") {
		t.Fatalf("error %q does not report an unsupported apiVersion", err.Error())
	}
}

func TestValidateSkipsContentChecksForStoredReferenceWithoutContent(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	if len(doc.Scenario.Content) != 0 {
		t.Fatal("fixture scenario unexpectedly carries content")
	}

	// An arbitrary apiVersion is tolerated while the content is not cached; the
	// referenced config is validated when it is loaded at publish time.
	doc.Scenario.APIVersion = "phenix.sandia.gov/v1"

	if err := doc.Validate(); err != nil {
		t.Fatalf("content-less stored reference was rejected: %v", err)
	}
}
