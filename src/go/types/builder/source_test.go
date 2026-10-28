package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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
		t.Errorf("SourceDigest(topology.json) = %q, want a sha256 digest", digest)
	}

	again, err := builder.SourceDigest(loadConfig(t, "topology.json"))
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}

	if again != digest {
		t.Errorf("SourceDigest(topology.json) a second time = %q, want %q", again, digest)
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
		t.Errorf("SourceDigest(topology.json with other status, timestamps and annotations) = %q, want %q",
			stableDigest, digest)
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
				t.Errorf("SourceDigest(topology.json with another %s) = %q, the digest of topology.json, want another",
					name, changedDigest)
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
				t.Fatalf("FromConfig(%s) source = nil, want one", fixture)
			}

			digest, err := builder.SourceDigest(config)
			if err != nil {
				t.Fatalf("SourceDigest: %v", err)
			}

			type stamps struct{ Digest, UpdatedAt, ImportedAt string }

			// The import time is left to the caller.
			want := stamps{Digest: digest, UpdatedAt: "2026-08-28T12:00:00Z"}
			got := stamps{Digest: doc.Source.Digest, UpdatedAt: doc.Source.UpdatedAt, ImportedAt: doc.Source.ImportedAt}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("FromConfig(%s) source mismatch (-want +got):\n%s", fixture, diff)
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
	if diff := cmp.Diff(want, doc.Source.Annotations); diff != "" {
		t.Errorf("FromConfig(topology.json) source annotations mismatch (-want +got):\n%s", diff)
	}

	if containsSubstring(warnings, "annotation") {
		t.Errorf("FromConfig(topology.json) warnings = %q, want none about annotations", warnings)
	}

	// Annotations are not part of the source's identity, except the legacy diagram: the digest of an
	// import tells one diagram from another, and from none.
	withDiagram := doc.Source.Digest

	importDigest, err := builder.ImportDigest(config)
	if err != nil {
		t.Fatalf("ImportDigest: %v", err)
	}

	if withDiagram != importDigest {
		t.Errorf("FromConfig(topology.json with a diagram) source digest = %q, want ImportDigest %q", withDiagram, importDigest)
	}

	if withDiagram == plain {
		t.Errorf("FromConfig(topology.json with a diagram) source digest = %q, the digest without annotations, want another",
			withDiagram)
	}

	config.Metadata.Annotations["builder-xml"] = "<mxGraphModel/>"

	if doc, _ := documentFromConfig(t, config); doc.Source.Digest == withDiagram || doc.Source.Digest == plain {
		t.Errorf("FromConfig(topology.json with another diagram) source digest = %q, want one of its own", doc.Source.Digest)
	}

	delete(config.Metadata.Annotations, "builder-xml")

	if doc, _ := documentFromConfig(t, config); doc.Source.Digest != plain {
		t.Errorf("FromConfig(topology.json without a diagram) source digest = %q, want %q, the digest without annotations",
			doc.Source.Digest, plain)
	}

	// An experiment keeps the topology and scenario it names.
	experiment, _ := documentFromConfig(t, loadConfig(t, "experiment.json"))

	want = map[string]string{"topology": "builder-fixture", "scenario": "builder-scenario"}
	if diff := cmp.Diff(want, experiment.Source.Annotations); diff != "" {
		t.Errorf("FromConfig(experiment.json) source annotations mismatch (-want +got):\n%s", diff)
	}

	// Only the Builders' own: none are kept, and the field is left out.
	config.Metadata.Annotations = store.Annotations{"builder-xml": "<mxGraphModel/>"}
	doc, _ = documentFromConfig(t, config)

	if doc.Source.Annotations != nil {
		t.Errorf("FromConfig(topology.json with only builder-xml) source annotations = %v, want nil", doc.Source.Annotations)
	}

	encoded, err := json.Marshal(doc.Source)
	if err != nil {
		t.Fatalf("encoding the source: %v", err)
	}

	var written map[string]any

	if err := json.Unmarshal(encoded, &written); err != nil {
		t.Fatalf("decoding the source: %v", err)
	}

	if value, ok := written["annotations"]; ok {
		t.Errorf("json.Marshal(source) annotations = %v, want the field left out", value)
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
		t.Errorf("FromConfig kept %d annotations, want %d", got, builder.MaxAnnotations)
	}

	// In key order: every n### but the last two, which are past the count.
	for _, key := range []string{"n000", "n099"} {
		if _, ok := doc.Source.Annotations[key]; !ok {
			t.Errorf("FromConfig left out annotation %q, want it kept", key)
		}
	}

	for _, key := range []string{" ", "bad\nkey", "a-large", "n100", "n101", "topology", "builder-xml"} {
		if _, ok := doc.Source.Annotations[key]; ok {
			t.Errorf("FromConfig kept annotation %q, want it left out", key)
		}
	}

	var found string

	for _, warning := range warnings {
		if strings.Contains(warning, "did not keep") {
			found = warning
		}
	}

	for _, part := range []string{
		`annotations " ", "a-large", "bad\nkey", "kkkk`, `"n100" and 3 more of the source config.`,
		"at most 100 annotations of 256 KiB in all",
	} {
		if !strings.Contains(found, part) {
			t.Errorf("FromConfig warning = %q, want it to contain %q", found, part)
		}
	}

	if strings.Contains(found, strings.Repeat("k", 100)) || strings.Contains(found, "builder-xml") {
		t.Errorf("FromConfig warning = %q, want no key named in full and not the Builder's own", found)
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
		{"too many", many, "the source can have at most 100 annotations, not 101"},
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
				t.Fatal("Validate() = nil, want an error")
			}

			if !strings.Contains(err.Error(), "source.annotations: ") || !strings.Contains(err.Error(), test.wantMsg) {
				t.Errorf("Validate() error = %q, want it to contain source.annotations and %q", err.Error(), test.wantMsg)
			}
		})
	}

	// Right at the bounds, with a value of several lines.
	doc := loadDocumentFixture(t, "document.json")
	doc.Source.Annotations = map[string]string{
		strings.Repeat("k", builder.MaxNameBytes): strings.Repeat("x\n", (builder.MaxAnnotationBytes-builder.MaxNameBytes)/2),
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() with annotations at the bounds error = %v, want nil", err)
	}
}

func TestValidateRejectsMalformedSourceDigest(t *testing.T) {
	for _, digest := range []string{
		"nope", "sha256:", "sha256:abc", "md5:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("A", 64),
	} {
		t.Run(digest, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			doc.Source.Digest = digest

			err := doc.Validate()
			if err == nil {
				t.Fatalf("Validate() with source digest %q = nil, want an error", digest)
			}

			if !strings.Contains(err.Error(), "malformed source digest") {
				t.Errorf("Validate() with source digest %q error = %q, want a malformed digest", digest, err.Error())
			}
		})
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
		t.Errorf("Validate() with a source digest and updatedAt error = %v, want nil", err)
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

// TestFromExperimentListsStoredScenario checks that an experiment naming a stored Scenario config
// lists it and holds none of the scenario content.
func TestFromExperimentListsStoredScenario(t *testing.T) {
	var asked []string

	doc, warnings, err := builder.FromConfig(
		loadConfig(t, "experiment.json"),
		builder.WithScenarioResolver(scenarioResolver(&asked, "builder-scenario")),
	)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	if diff := cmp.Diff([]string{"builder-scenario"}, doc.Scenarios); diff != "" {
		t.Errorf("FromConfig(experiment.json) scenarios mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"builder-scenario"}, asked); diff != "" {
		t.Errorf("FromConfig(experiment.json) resolver lookups mismatch (-want +got):\n%s", diff)
	}

	if containsSubstring(warnings, "scenario") {
		t.Errorf("FromConfig(experiment.json) warnings = %q, want none about the scenario", warnings)
	}

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var encoded any

	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatalf("decoding the document: %v", err)
	}

	if findKey(encoded, "apps") {
		t.Errorf("Encode(FromConfig(experiment.json)) holds scenario apps, want none:\n%s", data)
	}
}

// TestFromExperimentWarnsOfScenarioNotStored checks that an experiment whose scenario is not a
// stored Scenario config it may list, or cannot be looked up, lists none and says so.
func TestFromExperimentWarnsOfScenarioNotStored(t *testing.T) {
	const want = `the experiment's scenario "builder-scenario" is not a stored Scenario config. The Builder did not attach it`

	var asked []string

	for name, options := range map[string][]builder.GenerateOption{
		"not found":   {builder.WithScenarioResolver(scenarioResolver(&asked, "other"))},
		"no resolver": nil,
	} {
		t.Run(name, func(t *testing.T) {
			doc, warnings, err := builder.FromConfig(loadConfig(t, "experiment.json"), options...)
			if err != nil {
				t.Fatalf("FromConfig: %v", err)
			}

			if len(doc.Scenarios) != 0 {
				t.Errorf("FromConfig(experiment.json) scenarios = %q, want none", doc.Scenarios)
			}

			if !containsSubstring(warnings, want) || !containsSubstring(doc.Source.Warnings, want) {
				t.Errorf("FromConfig(experiment.json) warnings = %q, source warnings = %q, want both to hold %q",
					warnings, doc.Source.Warnings, want)
			}
		})
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
			warning:    `the experiment's scenario "two words" is not a stored Scenario config. The Builder did not attach it`,
		},
		"scenario content and no name": {
			annotation: "",
			warning:    "the experiment's scenario is not a stored Scenario config. The Builder did not attach it",
		},
	} {
		t.Run(name, func(t *testing.T) {
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
				t.Fatalf("FromConfig: %v", err)
			}

			if len(asked) != 0 || len(doc.Scenarios) != 0 {
				t.Errorf("FromConfig asked about %q and listed %q, want neither", asked, doc.Scenarios)
			}

			if !containsSubstring(warnings, test.warning) {
				t.Errorf("FromConfig warnings = %q, want %q", warnings, test.warning)
			}
		})
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
		t.Errorf("FromConfig(experiment.json) error = %v, want the resolver error %v", err, failure)
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
		t.Errorf("FromConfig(topology.json) asked about %q and listed %q, want neither", asked, doc.Scenarios)
	}
}
