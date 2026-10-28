package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

// packageDocument is the Pump station example document: two scenarios, an included topology, and a
// router with a custom icon and an injected file.
func packageDocument(t *testing.T) *builder.Document {
	t.Helper()

	document, err := builder.Parse(readExample(t, "pump-station.builder.json"))
	if err != nil {
		t.Fatalf("Parse(pump-station.builder.json): %v", err)
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

// carryConfigs makes the key list (scenarios or topologies) of a package value hold count copies of
// its first config, named pkg-0, pkg-1 and so on, and names them in the requirements.
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

	if err := pkg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}

	want := builder.PackageRequirements{
		Scenarios:  []string{"pkg-scenario", "pkg-unread"},
		Topologies: []string{"pkg-shared"},
		Templates:  []string{},
		Icons:      []string{"pkg-icon"},
		Images: []builder.PackageImage{
			{Name: "bennu.qc2", UsedBy: []string{"rtu-01"}},
			{Name: "minirouter.qc2", UsedBy: []string{"station-rtr"}},
			{Name: "shared.qc2", UsedBy: []string{"shared-host"}},
			{Name: "windows10.qc2", UsedBy: []string{"eng-ws-01"}},
		},
		Apps:  []string{"ntp", "vrouter"},
		Files: []string{"/phenix/assets/ntp", "/phenix/injects/router.boot", "/phenix/injects/shared.boot"},
	}
	byName := cmpopts.SortSlices(func(a, b builder.PackageImage) bool { return a.Name < b.Name })

	if diff := cmp.Diff(want, pkg.Requirements, byName); diff != "" {
		t.Errorf("NewPackage() requirements mismatch (-want +got):\n%s", diff)
	}

	if len(pkg.Scenarios) != 1 || len(pkg.Topologies) != 1 {
		t.Errorf("NewPackage() carries %d scenarios and %d topologies, want 1 of each", len(pkg.Scenarios), len(pkg.Topologies))
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
		t.Errorf("Validate() = %v, want nil", err)
	}

	if pkg.Scenarios != nil || pkg.Topologies != nil {
		t.Errorf("NewPackage() carries %v and %v, want no config", pkg.Scenarios, pkg.Topologies)
	}

	// The apps of a scenario read but not carried are still listed, and the
	// files are the document's own.
	if want := []string{"ntp", "vrouter"}; !slices.Equal(pkg.Requirements.Apps, want) {
		t.Errorf("NewPackage() requirements.apps = %v, want %v", pkg.Requirements.Apps, want)
	}

	if want := []string{"/phenix/injects/router.boot"}; !slices.Equal(pkg.Requirements.Files, want) {
		t.Errorf("NewPackage() requirements.files = %v, want %v", pkg.Requirements.Files, want)
	}

	if diff := cmp.Diff([]builder.PackageImage{}, pkg.Requirements.Images); diff != "" {
		t.Errorf("NewPackage() requirements.images mismatch (-want +got):\n%s", diff)
	}

	value := packageValue(t, pkg)

	requirements, ok := value["requirements"].(map[string]any)
	if !ok {
		t.Fatalf("encoded package requirements = %v, want an object", value["requirements"])
	}

	for _, key := range []string{"images", "templates"} {
		if diff := cmp.Diff([]any{}, requirements[key]); diff != "" {
			t.Errorf("encoded package requirements.%s mismatch (-want +got):\n%s", key, diff)
		}
	}

	for _, key := range []string{"scenarios", "topologies"} {
		if config, ok := value[key]; ok {
			t.Errorf("encoded package %s = %v, want it left out", key, config)
		}
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
		"in the metadata of a config": func(value map[string]any) {
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
			if err == nil {
				t.Fatal("DecodePackage() error = nil, want an unknown field refused")
			}

			if !errors.Is(err, builder.ErrInvalidPackage) {
				t.Errorf("DecodePackage() error = %v, want one wrapping ErrInvalidPackage", err)
			}

			if !strings.Contains(err.Error(), "unknown field") {
				t.Errorf("DecodePackage() error = %q, want one naming an unknown field", err.Error())
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
			want:   "requirements.images: the package needs this list, even when the list is empty",
			code:   builder.CodePackageRequirementsMissing,
		},
		{
			name:   "a null list",
			change: func(value map[string]any) { requirements(value)["apps"] = nil },
			want:   "requirements.apps: the package needs this list, even when the list is empty",
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
			want: "requirements.images[0].usedBy: the package needs this list, even when the list is empty",
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
				t.Fatalf("DecodePackage(): %v", err)
			}

			found := pkg.Issues()
			issues := make([]string, 0, len(found))

			for _, issue := range found {
				issues = append(issues, issue.String())

				if info, ok := builder.LookupCode(issue.Code); !ok || issue.Severity != info.Severity {
					t.Errorf("Issues() issue %+v: LookupCode(%s) = %+v, %t, want the code registered with severity %q",
						issue, issue.Code, info, ok, issue.Severity)
				}
			}

			if !slices.ContainsFunc(found, func(issue builder.Issue) bool {
				return strings.HasPrefix(issue.String(), tt.want) && issue.Code == tt.code
			}) {
				t.Errorf("Issues() = %q (%+v), want one starting %q with the code %s", issues, found, tt.want, tt.code)
			}

			var invalid *builder.PackageError
			if err := pkg.Validate(); !errors.As(err, &invalid) || !errors.Is(err, builder.ErrInvalidPackage) {
				t.Errorf("Validate() = %v, want a PackageError wrapping ErrInvalidPackage", err)
			}
		})
	}
}

// TestPackageValidateRefusesBuilderAnnotations checks that each Builder annotation of a carried
// config is refused, so no config made from a package claims a document of its server.
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
		t.Fatalf("DecodePackage(): %v", err)
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
	if diff := cmp.Diff(want, found); diff != "" {
		t.Errorf("Issues() about annotations mismatch (-want +got):\n%s", diff)
	}

	if err := pkg.Validate(); !errors.Is(err, builder.ErrInvalidPackage) {
		t.Errorf("Validate() = %v, want one wrapping ErrInvalidPackage", err)
	}
}

// TestDecodePackageGivesDocumentIssuesTheirCodes checks that a refused document makes a
// PackageError whose issues keep their codes, at paths below "document".
func TestDecodePackageGivesDocumentIssuesTheirCodes(t *testing.T) {
	value := packageValue(t, samplePackage(t))
	value["document"].(map[string]any)["extra"] = true

	_, err := builder.DecodePackage(packageText(t, value))

	var invalid *builder.PackageError
	if !errors.As(err, &invalid) || !errors.Is(err, builder.ErrInvalidPackage) {
		t.Errorf("DecodePackage() error = %v, want a PackageError wrapping ErrInvalidPackage", err)
	}

	issues := builder.ErrorIssues(err)
	if !slices.ContainsFunc(issues, func(issue builder.Issue) bool {
		return issue.Code == builder.CodeDocumentFieldUnknown && issue.Path == "document.extra"
	}) {
		t.Errorf("ErrorIssues(DecodePackage()) = %+v, want %s at document.extra", issues, builder.CodeDocumentFieldUnknown)
	}
}

func TestPackageTrimRequirementsNamesWhatItLeavesOut(t *testing.T) {
	if warnings := samplePackage(t).TrimRequirements(); len(warnings) != 0 {
		t.Errorf("TrimRequirements() of a package that fits = %+v, want no warnings", warnings)
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
		t.Fatal("Validate() of the untrimmed test package = nil, want an error")
	}

	warnings := pkg.TrimRequirements()

	if err := pkg.Validate(); err != nil {
		t.Errorf("Validate() after TrimRequirements() = %v, want nil", err)
	}

	// Each is a warning with its code, at the path of the list it leaves an entry out of.
	leftOut := func(path, message string) builder.Issue {
		return builder.Issue{
			Code: builder.CodePackageRequirementLeftOut, Severity: builder.SeverityWarning, Path: path, Message: message,
		}
	}
	want := []builder.Issue{
		leftOut("requirements.images",
			`The package does not list disk image "bad\timage.qc2": it must not contain control characters.`),
		leftOut("requirements.images",
			`The package does not list host "bad\rhost" among the users of disk image "good.qc2": `+
				`it must not contain control characters.`),
		{
			Code: builder.CodePackageRequirementsTruncated, Severity: builder.SeverityWarning, Path: "requirements.apps",
			Message: fmt.Sprintf(`The package lists at most %d apps. Thus it does not list 4 more: `+
				`"app-1000", "app-1001", "app-1002" and 1 more.`, builder.MaxPackageRequirements),
		},
		leftOut("requirements.files",
			`The package does not list file "/phenix/injects/a\nb": it must not contain control characters.`),
		leftOut("requirements.files",
			fmt.Sprintf("The package does not list file %q: it must be at most %d bytes.",
				strings.Repeat("f", 64)+"...", builder.MaxRequirementBytes)),
	}
	located := cmpopts.IgnoreFields(builder.Issue{}, "NodeID", "EdgeID", "NetworkID", "Field")

	if diff := cmp.Diff(want, warnings, located); diff != "" {
		t.Errorf("TrimRequirements() mismatch (-want +got):\n%s", diff)
	}

	if len(requirements.Apps) != builder.MaxPackageRequirements {
		t.Errorf("requirements.apps after TrimRequirements() = %d entries, want the first %d",
			len(requirements.Apps), builder.MaxPackageRequirements)
	}

	if slices.Contains(requirements.Files, long) {
		t.Errorf("requirements.files after TrimRequirements() = %d entries with the long file, want none that long",
			len(requirements.Files))
	}

	if len(requirements.Images) == 0 {
		t.Fatal("requirements.images after TrimRequirements() = none, want good.qc2 last")
	}

	last := requirements.Images[len(requirements.Images)-1]
	if diff := cmp.Diff(builder.PackageImage{Name: "good.qc2", UsedBy: []string{"host-b"}}, last); diff != "" {
		t.Errorf("last of requirements.images after TrimRequirements() mismatch (-want +got):\n%s", diff)
	}
}

func TestPackageIsNoDocument(t *testing.T) {
	text := packageText(t, packageValue(t, samplePackage(t)))

	if got := builder.FileKindOf(text); got != builder.FileKindPackage {
		t.Errorf("FileKindOf(JSON package) = %q, want %q", got, builder.FileKindPackage)
	}

	if got := builder.FileKindOf(readExample(t, "pump-station.package.yaml")); got != builder.FileKindPackage {
		t.Errorf("FileKindOf(pump-station.package.yaml) = %q, want %q", got, builder.FileKindPackage)
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
	document.Layers = []builder.Layer{
		{ID: layerID(1), Label: "Control", Icon: "d-icon"},
		{ID: layerID(2), Label: "Operations", IconKey: "server"},
	}

	if got, want := document.IconNames(), []string{"a-icon", "b-icon", "c-icon", "d-icon"}; !slices.Equal(got, want) {
		t.Errorf("IconNames() = %v, want %v", got, want)
	}

	if diff := cmp.Diff([]string{}, builder.NewDocument("blank").IconNames()); diff != "" {
		t.Errorf("IconNames() of a blank document mismatch (-want +got):\n%s", diff)
	}
}
