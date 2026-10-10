package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
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

	// Annotations are not part of the source's identity, but for the diagram
	// of the legacy Builder, which publishing to the topology removes: the
	// digest of an import tells one diagram from another, and from none.
	withDiagram := doc.Source.Digest

	if want, err := builder.ImportDigest(config); err != nil || withDiagram != want || withDiagram == plain {
		t.Fatalf("source digest = %q, want %q (%v), which is not %q", withDiagram, want, err, plain)
	}

	config.Metadata.Annotations["builder-xml"] = "<mxGraphModel/>"

	if doc, _ := documentFromConfig(t, config); doc.Source.Digest == withDiagram || doc.Source.Digest == plain {
		t.Fatalf("source digest = %q, want one of its own for another diagram", doc.Source.Digest)
	}

	delete(config.Metadata.Annotations, "builder-xml")

	if doc, _ := documentFromConfig(t, config); doc.Source.Digest != plain {
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

// scenarioResolver returns a resolver that finds the scenarios named in
// stored, and records every name it is asked about in asked.
func scenarioResolver(asked *[]string, stored ...string) builder.ScenarioResolver {
	return func(name string) (bool, error) {
		*asked = append(*asked, name)

		if slices.Contains(stored, name) {
			return true, nil
		}

		return false, nil
	}
}

// TestFromExperimentListsStoredScenario checks that an experiment whose
// scenario annotation names a stored Scenario config lists it, and holds
// nothing of the scenario content the experiment carries.
func TestFromExperimentListsStoredScenario(t *testing.T) {
	var asked []string

	doc, warnings, err := builder.FromConfig(
		loadConfig(t, "experiment.json"),
		builder.WithScenarioResolver(scenarioResolver(&asked, "builder-scenario")),
	)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	if !reflect.DeepEqual(doc.Scenarios, []string{"builder-scenario"}) {
		t.Fatalf("scenarios = %q, want [builder-scenario]", doc.Scenarios)
	}

	if !reflect.DeepEqual(asked, []string{"builder-scenario"}) {
		t.Fatalf("resolver asked about %q, want only builder-scenario", asked)
	}

	if containsSubstring(warnings, "scenario") {
		t.Fatalf("warnings = %q, want none about the scenario", warnings)
	}

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if strings.Contains(string(data), `"apps"`) {
		t.Fatalf("document holds scenario content:\n%s", data)
	}
}

// TestFromExperimentWarnsOfScenarioNotStored checks that an experiment whose
// scenario is not a stored Scenario config the caller may list, or that
// generation cannot look up, lists none and says so.
func TestFromExperimentWarnsOfScenarioNotStored(t *testing.T) {
	const want = `the experiment's scenario "builder-scenario" is not a stored Scenario config and was not attached`

	var asked []string

	for name, options := range map[string][]builder.GenerateOption{
		"not found":   {builder.WithScenarioResolver(scenarioResolver(&asked, "other"))},
		"no resolver": nil,
	} {
		doc, warnings, err := builder.FromConfig(loadConfig(t, "experiment.json"), options...)
		if err != nil {
			t.Fatalf("%s: FromConfig: %v", name, err)
		}

		if len(doc.Scenarios) != 0 {
			t.Errorf("%s: scenarios = %q, want none", name, doc.Scenarios)
		}

		if !containsSubstring(warnings, want) || !containsSubstring(doc.Source.Warnings, want) {
			t.Errorf("%s: warnings = %q, source warnings = %q, want %q", name, warnings, doc.Source.Warnings, want)
		}
	}
}

// TestFromExperimentScenarioLookups checks which scenario names generation
// looks up: only a config name, and only for an experiment that names one.
func TestFromExperimentScenarioLookups(t *testing.T) {
	for name, test := range map[string]struct {
		annotation string
		// warning is the scenario warning generation gives, "" for none.
		warning string
	}{
		"a name that is not a config name": {
			annotation: "two words",
			warning:    `the experiment's scenario "two words" is not a stored Scenario config and was not attached`,
		},
		"scenario content and no name": {
			annotation: "",
			warning:    "the experiment's scenario is not a stored Scenario config and was not attached",
		},
	} {
		config := loadConfig(t, "experiment.json")
		config.Metadata.Annotations["scenario"] = test.annotation

		if test.annotation == "" {
			delete(config.Metadata.Annotations, "scenario")
		}

		var asked []string

		doc, warnings, err := builder.FromConfig(
			config, builder.WithScenarioResolver(scenarioResolver(&asked, test.annotation)),
		)
		if err != nil {
			t.Fatalf("%s: FromConfig: %v", name, err)
		}

		if len(asked) != 0 || len(doc.Scenarios) != 0 {
			t.Errorf("%s: asked about %q and listed %q, want neither", name, asked, doc.Scenarios)
		}

		if !containsSubstring(warnings, test.warning) {
			t.Errorf("%s: warnings = %q, want %q", name, warnings, test.warning)
		}
	}
}

// TestFromExperimentFailsWhenScenarioLookupFails checks that a resolver's
// error fails generation rather than leaving the scenario out.
func TestFromExperimentFailsWhenScenarioLookupFails(t *testing.T) {
	failure := errors.New("store unavailable")

	_, _, err := builder.FromConfig(
		loadConfig(t, "experiment.json"),
		builder.WithScenarioResolver(func(string) (bool, error) { return false, failure }),
	)
	if !errors.Is(err, failure) {
		t.Fatalf("FromConfig error = %v, want the resolver's", err)
	}
}

// TestFromTopologyListsNoScenario checks that a topology, which names no
// scenario, never asks the resolver.
func TestFromTopologyListsNoScenario(t *testing.T) {
	var asked []string

	doc, _, err := builder.FromConfig(
		loadConfig(t, "topology.json"),
		builder.WithScenarioResolver(scenarioResolver(&asked, "builder-scenario")),
	)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	if len(asked) != 0 || len(doc.Scenarios) != 0 {
		t.Fatalf("asked about %q and listed %q, want neither", asked, doc.Scenarios)
	}
}

// TestDecodeRefusesScenarioObject checks that the scenario object documents
// once held is an unknown field, as every other key the schema lacks.
func TestDecodeRefusesScenarioObject(t *testing.T) {
	data, err := builder.Encode(loadDocumentFixture(t, "document.json"))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}

	delete(raw, "scenarios")
	raw["scenario"] = map[string]any{"kind": "stored", "name": "builder-scenario"}

	old, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	if _, err := builder.Decode(old); err == nil || !strings.Contains(err.Error(), `unknown field "scenario"`) {
		t.Fatalf("Decode error = %v, want the scenario key refused as unknown", err)
	}
}
