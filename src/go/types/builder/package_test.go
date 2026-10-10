package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"phenix/types/builder"
)

// packageScenario is a Scenario config as a package carries it, with an app
// that has an asset directory.
func packageScenario(name string) builder.PackageConfig {
	return builder.PackageConfig{
		APIVersion: "phenix.sandia.gov/v2",
		Kind:       builder.PackageKindScenario,
		Metadata: builder.PackageConfigMetadata{
			Name:        name,
			Annotations: map[string]string{"topology": "pkg-topology"},
		},
		Spec: map[string]any{"apps": []any{
			map[string]any{"name": "ntp", "assetDir": "/phenix/assets/ntp"},
			map[string]any{"name": "vrouter"},
		}},
	}
}

// packageTopology is a Topology config as a package carries it, with one
// node that boots from shared.qc2 and has an injection.
func packageTopology(name string) builder.PackageConfig {
	return builder.PackageConfig{
		APIVersion: "phenix.sandia.gov/v1",
		Kind:       builder.PackageKindTopology,
		Metadata:   builder.PackageConfigMetadata{Name: name},
		Spec: map[string]any{"nodes": []any{map[string]any{
			"general":    map[string]any{"hostname": "shared-host"},
			"hardware":   map[string]any{"drives": []any{map[string]any{"image": "shared.qc2"}}},
			"injections": []any{map[string]any{"src": "/phenix/injects/shared.boot", "dst": "/etc/shared.boot"}},
		}}},
	}
}

// packageDocument is the docs' Pump station document, naming two scenarios
// and an included topology, its station router naming a custom icon and
// injecting a file.
func packageDocument(t *testing.T) *builder.Document {
	t.Helper()

	document, err := builder.Parse(readDocsExample(t, "pump-station.builder.json"))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	document.Scenarios = []string{"pkg-scenario", "pkg-unread"}
	document.Source.IncludeTopologies = []string{"pkg-shared"}

	router := document.FindDevice("station-rtr").Device
	router.Icon = "pkg-icon"
	router.Spec["injections"] = []any{map[string]any{"src": "/phenix/injects/router.boot", "dst": "/etc/router.boot"}}

	return document
}

// samplePackage is the package of packageDocument with every section.
func samplePackage(t *testing.T) *builder.Package {
	t.Helper()

	return builder.NewPackage(packageDocument(t), builder.PackageContents{
		Scenarios:      map[string]builder.PackageConfig{"pkg-scenario": packageScenario("pkg-scenario")},
		CarryScenarios: true,
		Topologies:     map[string]builder.PackageConfig{"pkg-shared": packageTopology("pkg-shared")},
		Images:         true,
	})
}

// packageValue returns a package as its JSON decodes into plain values.
func packageValue(t *testing.T, pkg *builder.Package) map[string]any {
	t.Helper()

	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("encoding the package: %v", err)
	}

	var value map[string]any

	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decoding the package: %v", err)
	}

	return value
}

// carryConfigs makes the configs of one list of a package value (key is
// scenarios or topologies) count copies of its first config, named pkg-0,
// pkg-1 and so on, and has the requirements name them.
func carryConfigs(value map[string]any, key string, count int) {
	configs := value[key].(map[string]any)

	var first map[string]any

	for name, config := range configs {
		first = config.(map[string]any)

		delete(configs, name)
	}

	names := make([]any, 0, count)

	for i := range count {
		name := fmt.Sprintf("pkg-%d", i)
		configs[name] = map[string]any{
			"apiVersion": first["apiVersion"],
			"kind":       first["kind"],
			"metadata":   map[string]any{"name": name},
			"spec":       first["spec"],
		}
		names = append(names, name)
	}

	value["requirements"].(map[string]any)[key] = names
}

// numbered returns count entries of the given format, numbered from 0.
func numbered(format string, count int) []any {
	values := make([]any, 0, count)

	for i := range count {
		values = append(values, fmt.Sprintf(format, i))
	}

	return values
}

// packageText returns a value as JSON text.
func packageText(t *testing.T, value any) []byte {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding the package: %v", err)
	}

	return data
}

func TestNewPackageListsRequirements(t *testing.T) {
	pkg := samplePackage(t)
	requirements := pkg.Requirements

	if err := pkg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	for _, tt := range []struct {
		name      string
		got, want []string
	}{
		{name: "scenarios", got: requirements.Scenarios, want: []string{"pkg-scenario", "pkg-unread"}},
		{name: "topologies", got: requirements.Topologies, want: []string{"pkg-shared"}},
		{name: "templates", got: requirements.Templates, want: []string{}},
		{name: "icons", got: requirements.Icons, want: []string{"pkg-icon"}},
		{name: "apps", got: requirements.Apps, want: []string{"ntp", "vrouter"}},
		{name: "files", got: requirements.Files, want: []string{
			"/phenix/assets/ntp", "/phenix/injects/router.boot", "/phenix/injects/shared.boot",
		}},
	} {
		if tt.got == nil || !slices.Equal(tt.got, tt.want) {
			t.Errorf("requirements.%s = %#v, want %#v", tt.name, tt.got, tt.want)
		}
	}

	images := map[string][]string{}
	for _, image := range requirements.Images {
		images[image.Name] = image.UsedBy
	}

	want := map[string][]string{
		"bennu.qc2":      {"rtu-01"},
		"minirouter.qc2": {"station-rtr"},
		"shared.qc2":     {"shared-host"},
		"windows10.qc2":  {"eng-ws-01"},
	}

	if asJSON(t, images) != asJSON(t, want) {
		t.Errorf("requirements.images = %s, want %s", asJSON(t, images), asJSON(t, want))
	}

	if len(pkg.Scenarios) != 1 || len(pkg.Topologies) != 1 {
		t.Errorf("the package carries %d scenarios and %d topologies, want 1 of each", len(pkg.Scenarios), len(pkg.Topologies))
	}
}

func TestNewPackageCarriesOnlyWhatIsIncluded(t *testing.T) {
	pkg := builder.NewPackage(packageDocument(t), builder.PackageContents{
		Scenarios:      map[string]builder.PackageConfig{"pkg-scenario": packageScenario("pkg-scenario")},
		CarryScenarios: false,
		Topologies:     nil,
		Images:         false,
	})

	if err := pkg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	if pkg.Scenarios != nil || pkg.Topologies != nil {
		t.Errorf("the package carries %v and %v, want no config", pkg.Scenarios, pkg.Topologies)
	}

	// The apps of a scenario read but not carried are still listed, and the
	// files are the document's own.
	if want := []string{"ntp", "vrouter"}; !slices.Equal(pkg.Requirements.Apps, want) {
		t.Errorf("apps = %v, want %v", pkg.Requirements.Apps, want)
	}

	if want := []string{"/phenix/injects/router.boot"}; !slices.Equal(pkg.Requirements.Files, want) {
		t.Errorf("files = %v, want %v", pkg.Requirements.Files, want)
	}

	if pkg.Requirements.Images == nil || len(pkg.Requirements.Images) != 0 {
		t.Errorf("images = %#v, want an empty list", pkg.Requirements.Images)
	}

	encoded := string(packageText(t, pkg))

	for _, want := range []string{`"images":[]`, `"templates":[]`} {
		if !strings.Contains(encoded, want) {
			t.Errorf("the package encodes as %s, want %s in it", encoded, want)
		}
	}

	for _, unwanted := range []string{`"scenarios":{`, `"topologies":{`} {
		if strings.Contains(encoded, unwanted) {
			t.Errorf("the package encodes as %s, want no %s", encoded, unwanted)
		}
	}
}

func TestParsePackageReadsJSONAndYAML(t *testing.T) {
	pkg := samplePackage(t)
	value := packageValue(t, pkg)

	fromJSON, err := builder.ParsePackage(packageText(t, value))
	if err != nil {
		t.Fatalf("ParsePackage(JSON) returned error: %v", err)
	}

	text, err := yaml.Marshal(value)
	if err != nil {
		t.Fatalf("encoding the package as YAML: %v", err)
	}

	fromYAML, err := builder.ParsePackage(text)
	if err != nil {
		t.Fatalf("ParsePackage(YAML) returned error: %v", err)
	}

	for name, parsed := range map[string]*builder.Package{"JSON": fromJSON, "YAML": fromYAML} {
		if asJSON(t, parsed.Requirements) != asJSON(t, pkg.Requirements) {
			t.Errorf("%s requirements = %s, want %s", name, asJSON(t, parsed.Requirements), asJSON(t, pkg.Requirements))
		}

		if parsed.Document.Metadata.ID != pkg.Document.Metadata.ID || len(parsed.Scenarios) != 1 {
			t.Errorf("%s package = %s, want the document and its scenario", name, asJSON(t, parsed))
		}
	}
}

func TestParsePackageRefusesWhatItCannotRead(t *testing.T) {
	alias := "$schema: " + builder.PackageSchemaURI + "\nrequirements: &r {}\nother: *r\n"

	for name, text := range map[string]string{
		"empty text":     " \n",
		"too much text":  `{"$schema": "` + builder.PackageSchemaURI + `"}` + strings.Repeat(" ", builder.MaxPackageBytes),
		"a YAML alias":   alias,
		"trailing JSON":  `{"$schema": "` + builder.PackageSchemaURI + `"} {}`,
		"a list":         `[]`,
		"no requirement": `{"$schema": "` + builder.PackageSchemaURI + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := builder.ParsePackage([]byte(text))
			if !errors.Is(err, builder.ErrInvalidPackage) {
				t.Fatalf("ParsePackage error = %v, want one matching ErrInvalidPackage", err)
			}
		})
	}
}

func TestDecodePackageRefusesUnknownFields(t *testing.T) {
	for name, change := range map[string]func(value map[string]any){
		"at the root": func(value map[string]any) { value["extra"] = true },
		"in the requirements": func(value map[string]any) {
			value["requirements"].(map[string]any)["extra"] = []any{}
		},
		"in a config": func(value map[string]any) {
			value["scenarios"].(map[string]any)["pkg-scenario"].(map[string]any)["status"] = map[string]any{}
		},
		"in a config's metadata": func(value map[string]any) {
			value["topologies"].(map[string]any)["pkg-shared"].(map[string]any)["metadata"].(map[string]any)["created"] = "now"
		},
		"in an image": func(value map[string]any) {
			value["requirements"].(map[string]any)["images"].([]any)[0].(map[string]any)["size"] = 1
		},
		"in the document": func(value map[string]any) {
			value["document"].(map[string]any)["extra"] = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := packageValue(t, samplePackage(t))
			change(value)

			_, err := builder.DecodePackage(packageText(t, value))
			if !errors.Is(err, builder.ErrInvalidPackage) || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("DecodePackage error = %v, want an unknown field refused", err)
			}
		})
	}
}

func TestPackageValidateFindsIssues(t *testing.T) {
	scenario := func(value map[string]any) map[string]any {
		return value["scenarios"].(map[string]any)["pkg-scenario"].(map[string]any)
	}
	requirements := func(value map[string]any) map[string]any {
		return value["requirements"].(map[string]any)
	}

	for _, tt := range []struct {
		name   string
		change func(value map[string]any)
		want   string
		code   builder.Code
	}{
		{
			name:   "another schema",
			change: func(value map[string]any) { value["$schema"] = "https://phenix.sandia.gov/schemas/builder/v1" },
			want:   "$schema: package schema must be",
			code:   builder.CodePackageSchemaMismatch,
		},
		{
			name:   "no document",
			change: func(value map[string]any) { delete(value, "document") },
			want:   "document: a package holds a Builder document",
			code:   builder.CodePackageDocumentMissing,
		},
		{
			name:   "a document that is not valid",
			change: func(value map[string]any) { value["document"].(map[string]any)["nodes"] = nil },
			want:   "document.nodes: nodes must be an array",
			code:   builder.CodeDocumentListMissing,
		},
		{
			name:   "a scenario of another kind",
			change: func(value map[string]any) { scenario(value)["kind"] = builder.PackageKindTopology },
			want:   `scenarios.pkg-scenario.kind: must be "Scenario"`,
			code:   builder.CodePackageConfigKindMismatch,
		},
		{
			name: "a config named apart from its key",
			change: func(value map[string]any) {
				scenario(value)["metadata"].(map[string]any)["name"] = "other"
			},
			want: `scenarios.pkg-scenario.metadata.name: must be "pkg-scenario"`,
			code: builder.CodePackageConfigNameMismatch,
		},
		{
			name:   "an apiVersion of no phenix config",
			change: func(value map[string]any) { scenario(value)["apiVersion"] = "v2" },
			want:   "scenarios.pkg-scenario.apiVersion: must be phenix.sandia.gov/v<number>",
			code:   builder.CodePackageConfigVersionInvalid,
		},
		{
			name:   "a config without a spec",
			change: func(value map[string]any) { delete(scenario(value), "spec") },
			want:   "scenarios.pkg-scenario.spec: a config needs a spec",
			code:   builder.CodePackageConfigSpecMissing,
		},
		{
			name:   "a carried config the requirements do not name",
			change: func(value map[string]any) { requirements(value)["scenarios"] = []any{"pkg-unread"} },
			want:   "scenarios.pkg-scenario: the package carries it, but its requirements do not name it",
			code:   builder.CodePackageConfigUnlisted,
		},
		{
			name: "a carried config of a key that is no config name",
			change: func(value map[string]any) {
				configs := value["topologies"].(map[string]any)
				config := configs["pkg-shared"].(map[string]any)
				config["metadata"].(map[string]any)["name"] = "a b"
				configs["a b"] = config
				delete(configs, "pkg-shared")
				requirements(value)["topologies"] = []any{"a b"}
			},
			want: `topologies.a b: "a b" is not a config name`,
			code: builder.CodePackageConfigKeyInvalid,
		},
		{
			name:   "a missing list",
			change: func(value map[string]any) { delete(requirements(value), "images") },
			want:   "requirements.images: the list is required",
			code:   builder.CodePackageRequirementsMissing,
		},
		{
			name:   "a null list",
			change: func(value map[string]any) { requirements(value)["apps"] = nil },
			want:   "requirements.apps: the list is required",
			code:   builder.CodePackageRequirementsMissing,
		},
		{
			name:   "a blank entry",
			change: func(value map[string]any) { requirements(value)["files"] = []any{" "} },
			want:   "requirements.files[0]: must not be blank",
			code:   builder.CodePackageRequirementBlank,
		},
		{
			name:   "an entry with a control character",
			change: func(value map[string]any) { requirements(value)["icons"] = []any{"a\tb"} },
			want:   "requirements.icons[0]: must not contain control characters",
			code:   builder.CodePackageRequirementControl,
		},
		{
			name: "an image used by nobody listed",
			change: func(value map[string]any) {
				requirements(value)["images"].([]any)[0].(map[string]any)["usedBy"] = nil
			},
			want: "requirements.images[0].usedBy: the list is required",
			code: builder.CodePackageRequirementsMissing,
		},
		{
			name: "a Builder annotation on a config",
			change: func(value map[string]any) {
				scenario(value)["metadata"].(map[string]any)["annotations"] = map[string]any{"builder-doc": `{"id":"x"}`}
			},
			want: `scenarios.pkg-scenario.metadata.annotations: ` +
				`"builder-doc" is a Builder annotation, which a package never carries`,
			code: builder.CodePackageConfigBuilderNote,
		},
		{
			name:   "more Scenario configs than a document names",
			change: func(value map[string]any) { carryConfigs(value, "scenarios", builder.MaxScenarios+1) },
			want: fmt.Sprintf("scenarios: a package carries at most %d Scenario configs, not %d",
				builder.MaxScenarios, builder.MaxScenarios+1),
			code: builder.CodePackageConfigsTooMany,
		},
		{
			name:   "more Topology configs than a package carries",
			change: func(value map[string]any) { carryConfigs(value, "topologies", builder.MaxPackageTopologies+1) },
			want: fmt.Sprintf("topologies: a package carries at most %d Topology configs, not %d",
				builder.MaxPackageTopologies, builder.MaxPackageTopologies+1),
			code: builder.CodePackageConfigsTooMany,
		},
		{
			name: "a list longer than a package lists",
			change: func(value map[string]any) {
				requirements(value)["files"] = numbered("/phenix/f%d", builder.MaxPackageRequirements+1)
			},
			want: fmt.Sprintf("requirements.files: the list holds at most %d entries, not %d",
				builder.MaxPackageRequirements, builder.MaxPackageRequirements+1),
			code: builder.CodePackageRequirementsTooMany,
		},
		{
			name: "an entry longer than a package lists",
			change: func(value map[string]any) {
				requirements(value)["apps"] = []any{strings.Repeat("a", builder.MaxRequirementBytes+1)}
			},
			want: fmt.Sprintf("requirements.apps[0]: must be at most %d bytes", builder.MaxRequirementBytes),
			code: builder.CodePackageRequirementTooLong,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			value := packageValue(t, samplePackage(t))
			tt.change(value)

			pkg, err := builder.DecodePackage(packageText(t, value))
			if err != nil {
				t.Fatalf("DecodePackage returned error: %v", err)
			}

			found := pkg.Issues()
			issues := make([]string, 0, len(found))

			for _, issue := range found {
				issues = append(issues, issue.String())

				if info, ok := builder.LookupCode(issue.Code); !ok || issue.Severity != info.Severity {
					t.Errorf("issue %+v has a code the registry does not hold, or another severity", issue)
				}
			}

			if !slices.ContainsFunc(found, func(issue builder.Issue) bool {
				return strings.HasPrefix(issue.String(), tt.want) && issue.Code == tt.code
			}) {
				t.Fatalf("issues = %q (%+v), want one starting %q with the code %s", issues, found, tt.want, tt.code)
			}

			var invalid *builder.PackageError
			if err := pkg.Validate(); !errors.As(err, &invalid) || !errors.Is(err, builder.ErrInvalidPackage) {
				t.Fatalf("Validate error = %v, want a PackageError", err)
			}
		})
	}
}

// TestPackageValidateRefusesBuilderAnnotations asserts a carried config
// with Builder annotations is refused once for each, so that no config made
// from a package claims a document of the server it is created on.
func TestPackageValidateRefusesBuilderAnnotations(t *testing.T) {
	value := packageValue(t, samplePackage(t))
	topology := value["topologies"].(map[string]any)["pkg-shared"].(map[string]any)
	topology["metadata"].(map[string]any)["annotations"] = map[string]any{
		"builder-xml": "<mxGraphModel/>",
		"builder-doc": `{"id":"forged"}`,
		"owner":       "plant",
	}

	text := packageText(t, value)

	pkg, err := builder.DecodePackage(text)
	if err != nil {
		t.Fatalf("DecodePackage returned error: %v", err)
	}

	var found []string

	for _, issue := range pkg.Issues() {
		if strings.Contains(issue.Message, "annotation") {
			found = append(found, issue.String())
		}
	}

	const at = "topologies.pkg-shared.metadata.annotations: "

	want := []string{
		at + `"builder-doc" is a Builder annotation, which a package never carries`,
		at + `"builder-xml" is a Builder annotation, which a package never carries`,
	}
	if !slices.Equal(found, want) {
		t.Fatalf("issues = %q, want %q", found, want)
	}

	if _, err := builder.ParsePackage(text); !errors.Is(err, builder.ErrInvalidPackage) {
		t.Fatalf("ParsePackage error = %v, want the package refused", err)
	}
}

// TestDecodePackageGivesDocumentIssuesTheirCodes asserts a document the
// decoder refuses makes a PackageError whose issues keep the codes of the
// document's rules, at their paths below "document".
func TestDecodePackageGivesDocumentIssuesTheirCodes(t *testing.T) {
	value := packageValue(t, samplePackage(t))
	value["document"].(map[string]any)["extra"] = true

	_, err := builder.DecodePackage(packageText(t, value))

	var invalid *builder.PackageError
	if !errors.As(err, &invalid) || !errors.Is(err, builder.ErrInvalidPackage) {
		t.Fatalf("DecodePackage error = %v, want a PackageError", err)
	}

	issues := builder.ErrorIssues(err)
	if !slices.ContainsFunc(issues, func(issue builder.Issue) bool {
		return issue.Code == builder.CodeDocumentFieldUnknown && issue.Path == "document.extra"
	}) {
		t.Fatalf("issues = %+v, want the unknown field of the document with its code", issues)
	}
}

func TestPackageTrimRequirementsNamesWhatItLeavesOut(t *testing.T) {
	if warnings := samplePackage(t).TrimRequirements(); len(warnings) != 0 {
		t.Fatalf("a package that fits gives warnings %+v, want none", warnings)
	}

	pkg := samplePackage(t)
	requirements := &pkg.Requirements
	long := strings.Repeat("f", builder.MaxRequirementBytes+1)

	requirements.Files = append(requirements.Files, "/phenix/injects/a\nb", long)
	requirements.Apps = make([]string, 0, builder.MaxPackageRequirements+4)

	for i := range builder.MaxPackageRequirements + 4 {
		requirements.Apps = append(requirements.Apps, fmt.Sprintf("app-%04d", i))
	}

	requirements.Images = append(requirements.Images,
		builder.PackageImage{Name: "bad\timage.qc2", UsedBy: []string{"host-a"}},
		builder.PackageImage{Name: "good.qc2", UsedBy: []string{"host-b", "bad\rhost"}},
	)

	if pkg.Validate() == nil {
		t.Fatal("the test package fits before it is trimmed")
	}

	warnings := pkg.TrimRequirements()

	if err := pkg.Validate(); err != nil {
		t.Fatalf("Validate after TrimRequirements returned error: %v", err)
	}

	want := []string{
		`The package does not list disk image "bad\timage.qc2": it must not contain control characters.`,
		`The package does not list host "bad\rhost" among the users of disk image "good.qc2": ` +
			`it must not contain control characters.`,
		fmt.Sprintf(`The package lists at most %d apps, so it does not list 4 more: `+
			`"app-1000", "app-1001", "app-1002" and 1 more.`, builder.MaxPackageRequirements),
		`The package does not list file "/phenix/injects/a\nb": it must not contain control characters.`,
		fmt.Sprintf("The package does not list file %q: it must be at most %d bytes.",
			strings.Repeat("f", 64)+"...", builder.MaxRequirementBytes),
	}
	if got := builder.IssueMessages(warnings); !slices.Equal(got, want) {
		t.Fatalf("warnings =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// Each is a warning with its code, at the path of the list it leaves an
	// entry out of.
	codes := []struct {
		code builder.Code
		path string
	}{
		{builder.CodePackageRequirementLeftOut, "requirements.images"},
		{builder.CodePackageRequirementLeftOut, "requirements.images"},
		{builder.CodePackageRequirementsTruncated, "requirements.apps"},
		{builder.CodePackageRequirementLeftOut, "requirements.files"},
		{builder.CodePackageRequirementLeftOut, "requirements.files"},
	}

	for i, warning := range warnings {
		if warning.Code != codes[i].code || warning.Path != codes[i].path || warning.Severity != builder.SeverityWarning {
			t.Errorf("warning %d = %+v, want a warning of the code %s at %s", i, warning, codes[i].code, codes[i].path)
		}
	}

	if len(requirements.Apps) != builder.MaxPackageRequirements || slices.Contains(requirements.Files, long) {
		t.Errorf("apps = %d entries, files = %d entries, want the first %d apps and no long file",
			len(requirements.Apps), len(requirements.Files), builder.MaxPackageRequirements)
	}

	last := requirements.Images[len(requirements.Images)-1]
	if last.Name != "good.qc2" || !slices.Equal(last.UsedBy, []string{"host-b"}) {
		t.Errorf("the last image = %+v, want good.qc2 used by host-b only", last)
	}
}

func TestPackageIsNoDocument(t *testing.T) {
	text := packageText(t, packageValue(t, samplePackage(t)))

	if builder.IsDocumentText(text) {
		t.Fatal("IsDocumentText takes a package for a Builder document")
	}

	if builder.IsDocumentText(readDocsExample(t, "pump-station.package.yaml")) {
		t.Fatal("IsDocumentText takes a YAML package for a Builder document")
	}
}

func TestDocsPackageExampleLoads(t *testing.T) {
	pkg, err := builder.ParsePackage(readDocsExample(t, "pump-station.package.yaml"))
	if err != nil {
		t.Fatalf("the docs example pump-station.package.yaml does not load: %v", err)
	}

	if len(pkg.Scenarios) != 1 || len(pkg.Requirements.Images) == 0 {
		t.Fatalf("the docs example = %s, want a scenario and disk images", asJSON(t, pkg))
	}

	// It lists what the package of its own document lists.
	made := builder.NewPackage(pkg.Document, builder.PackageContents{
		Scenarios:      pkg.Scenarios,
		CarryScenarios: true,
		Topologies:     nil,
		Images:         true,
	})

	if asJSON(t, made.Requirements) != asJSON(t, pkg.Requirements) {
		t.Fatalf("the docs example lists %s, want %s", asJSON(t, pkg.Requirements), asJSON(t, made.Requirements))
	}
}

func TestDocumentIconNames(t *testing.T) {
	document := builder.NewDocument("icons")
	document.Nodes = []builder.Node{
		{Kind: builder.NodeKindDevice, Device: &builder.Device{Icon: "b-icon"}},
		{Kind: builder.NodeKindGroup, Group: &builder.Group{Icon: "a-icon"}},
		{Kind: builder.NodeKindIcon, Icon: &builder.IconNode{Icon: "b-icon"}},
		{Kind: builder.NodeKindIcon, Icon: &builder.IconNode{IconKey: "router"}},
		{Kind: builder.NodeKindDevice, Device: &builder.Device{IconKey: "router"}},
	}
	document.Templates = []builder.Template{{Name: "plc", Device: builder.TemplateDevice{Icon: "c-icon"}}}

	if got, want := document.IconNames(), []string{"a-icon", "b-icon", "c-icon"}; !slices.Equal(got, want) {
		t.Errorf("IconNames = %v, want %v", got, want)
	}

	if got := builder.NewDocument("blank").IconNames(); got == nil || len(got) != 0 {
		t.Errorf("IconNames of a blank document = %#v, want an empty list", got)
	}
}

// TestPackageSchemaDocumentsEveryProperty holds the package schema to the
// documentation convention of the document schema (see
// TestSchemaDocumentsEveryProperty).
func TestPackageSchemaDocumentsEveryProperty(t *testing.T) {
	schema, err := builder.PackageSchema()
	if err != nil {
		t.Fatalf("PackageSchema returned error: %v", err)
	}

	if schema["$id"] != builder.PackageSchemaURI {
		t.Fatalf("$id = %v, want %s", schema["$id"], builder.PackageSchemaURI)
	}

	var missing []string

	for name, property := range mapAt(t, schema, "properties") {
		missing = append(missing, undocumented("properties."+name, property, true)...)
	}

	for name, def := range mapAt(t, schema, "$defs") {
		if strings.HasPrefix(name, builder.PhenixDefPrefix) {
			continue
		}

		missing = append(missing, undocumented("$defs."+name, def, true)...)
	}

	if len(missing) > 0 {
		slices.Sort(missing)
		t.Fatalf("these lack a title, a description or examples:\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestPackageSchemaReferencesResolve asserts every reference of the package
// schema, as GET /schemas/builder/package/v1 serves it, names a definition
// it holds.
func TestPackageSchemaReferencesResolve(t *testing.T) {
	encoded, err := builder.PackageSchemaJSON()
	if err != nil {
		t.Fatalf("PackageSchemaJSON returned error: %v", err)
	}

	var schema map[string]any

	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("decoding the package schema: %v", err)
	}

	defs := mapAt(t, schema, "$defs")
	refs := map[string]string{}

	schemaRefs(schema, "#", refs)

	for at, reference := range refs {
		name, local := strings.CutPrefix(reference, "#/$defs/")

		switch _, held := defs[name]; {
		case !local:
			t.Errorf("%s refers to %q, outside the schema", at, reference)
		case !held:
			t.Errorf("%s refers to $defs/%s, which the schema does not hold", at, name)
		}
	}

	for _, name := range []string{"packageDocument", "packageConfig", "packageRequirements", "packageImage", "node"} {
		if _, held := defs[name]; !held {
			t.Errorf("the schema does not hold $defs/%s", name)
		}
	}
}
