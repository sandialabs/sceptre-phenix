package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/disk"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
)

// The device of the package tests' document, its drive image and its custom icon.
const (
	builderPackageHost  = "pkg-host"
	builderPackageImage = "pkg.qc2"
	builderPackageIcon  = "pkg-icon"
)

// builderPackageDocument returns a valid document of one device that names
// the given scenarios and includes the given topologies.
func builderPackageDocument(t *testing.T, scenarios, includes []string) *bdoc.Document {
	t.Helper()

	document := bdoc.NewDocument("package test")
	document.Scenarios = scenarios
	document.Source = &bdoc.Source{Kind: bdoc.SourceKindManual, IncludeTopologies: includes}
	document.Nodes = append(document.Nodes, bdoc.Node{
		ID:       bdoc.DeviceNodeID(builderPackageHost),
		Kind:     bdoc.NodeKindDevice,
		Position: bdoc.Position{X: 0, Y: 0},
		Device: &bdoc.Device{
			Hostname: builderPackageHost,
			Icon:     builderPackageIcon,
			Spec: map[string]any{
				"general":  map[string]any{"hostname": builderPackageHost},
				"hardware": map[string]any{"drives": []any{map[string]any{"image": builderPackageImage}}},
			},
			Interfaces: []bdoc.InterfaceHandle{},
		},
	})

	if err := document.Validate(); err != nil {
		t.Fatalf("the test document is not valid: %v", err)
	}

	return document
}

// buildBuilderPackage asks the harness for the package of a document.
func buildBuilderPackage(
	t *testing.T,
	harness *builderHarness,
	role *rbac.Role,
	document *bdoc.Document,
	include ...string,
) (int, builderPackageResponse) {
	t.Helper()

	if include == nil {
		include = []string{}
	}

	body, err := json.Marshal(map[string]any{"document": document, "include": include})
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/package", body: string(body), user: builderAlice, role: role,
	})

	var response builderPackageResponse

	if recorder.Code == http.StatusOK {
		decodeBuilderAnswer(t, recorder, &response)
	}

	return recorder.Code, response
}

// builderPackageWarnings returns each warning by its message, and an error
// for each one that is not a warning with a registered code.
func builderPackageWarnings(warnings []bdoc.Issue) (map[string]bdoc.Code, error) {
	byMessage := make(map[string]bdoc.Code, len(warnings))

	var errs []error

	for _, warning := range warnings {
		if _, known := bdoc.LookupCode(warning.Code); !known || warning.Severity != bdoc.SeverityWarning {
			errs = append(errs, fmt.Errorf("warning %+v is not a warning with a registered code", warning))
		}

		byMessage[warning.Message] = warning.Code
	}

	return byMessage, errors.Join(errs...)
}

// builderPackageRefusal sends body to the package route at path and returns
// the status and the refusal it answers.
func builderPackageRefusal(t *testing.T, harness *builderHarness, path, body string) (int, builderErrorBody) {
	t.Helper()

	recorder := harness.do(builderRequest{method: http.MethodPost, path: path, body: body, user: builderAlice})

	var refusal builderErrorBody

	if recorder.Code != http.StatusOK {
		decodeBuilderAnswer(t, recorder, &refusal)
	}

	return recorder.Code, refusal
}

// TestBuilderPackageRoutePermissions asserts the package schema takes the
// Builder alone, and building and resolving a package also take configs get.
func TestBuilderPackageRoutePermissions(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)
	schemasOnly := builderRole(builderPolicy([]string{"schemas"}, []string{"*"}, []string{"get"}))
	build := builderJSON(t, map[string]any{"document": builderPackageDocument(t, nil, nil), "include": []string{}})

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{
			name: "the schema with the Builder alone", user: builderAlice, role: builderOnlyRole(),
			request: builderRequest{method: http.MethodGet, path: builderPackageSchemaPath}, status: http.StatusOK,
		},
		{
			name: "a build without configs get", user: builderAlice, role: schemasOnly,
			request: builderRequest{method: http.MethodPost, path: "/builder/package", body: build},
			status:  http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{
			name: "a resolve without configs get", user: builderAlice, role: schemasOnly,
			request: builderRequest{
				method: http.MethodPost, path: "/builder/package/resolve", body: builderJSON(t, builderResolvePackage(t, harness)),
			},
			status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
	})
}

// newBuilderPackageHarness returns a harness with scenario pkg-sc, topology
// pkg-inc and icon pkg-icon, and a document naming those and some it lacks.
func newBuilderPackageHarness(t *testing.T) (*builderHarness, *bdoc.Document) {
	t.Helper()

	scenario := namedScenario(t, "pkg-sc", "topo")
	scenario.Metadata.Annotations[bapiDocumentAnnotationForTest] = `{"id":"x"}`

	harness := newBuilderHarness(t, scenario, builderConfig(t, builderKindTopology, "pkg-inc"))

	if recorder := harness.postIcon(builderAlice, builderPackageIcon, builderIconPNG(t, 4, 4, 7)); recorder.Code != http.StatusCreated {
		t.Fatalf("adding the icon: status = %d", recorder.Code)
	}

	t.Cleanup(func() {
		if harness.configWrites != 0 {
			t.Errorf("building packages wrote %d configs, want none", harness.configWrites)
		}
	})

	return harness, builderPackageDocument(t, []string{"pkg-sc", "pkg-gone"}, []string{"pkg-inc", "pkg-hidden", "/phenix/x.yml"})
}

func TestBuilderBuildPackageWithEverySection(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)

	status, response := buildBuilderPackage(t, harness, nil, document, "scenarios", "topologies", "icons", "images")
	if status != http.StatusOK {
		t.Fatalf("POST /builder/package with every section: status = %d, want %d", status, http.StatusOK)
	}

	pkg := response.Package
	if err := pkg.Validate(); err != nil {
		t.Errorf("Validate(the package) error: %v", err)
	}

	if got, want := slices.Sorted(maps.Keys(pkg.Scenarios)), []string{"pkg-sc"}; !slices.Equal(got, want) {
		t.Errorf("POST /builder/package with every section: scenarios = %q, want %q", got, want)
	}

	// The scenario's own annotations, and none of the Builder's.
	carried := pkg.Scenarios["pkg-sc"]
	if diff := cmp.Diff(map[string]string{"keep": "yes", "topology": "topo"}, carried.Metadata.Annotations); diff != "" {
		t.Errorf("POST /builder/package with every section: annotations of pkg-sc mismatch (-want +got):\n%s", diff)
	}

	if got, want := slices.Sorted(maps.Keys(pkg.Topologies)), []string{"pkg-inc"}; !slices.Equal(got, want) {
		t.Errorf("POST /builder/package with every section: topologies = %q, want %q", got, want)
	}

	if want := []string{"pkg-sc-app"}; !slices.Equal(pkg.Requirements.Apps, want) {
		t.Errorf("POST /builder/package with every section: apps = %q, want %q", pkg.Requirements.Apps, want)
	}

	wantImages := []bdoc.PackageImage{{Name: builderPackageImage, UsedBy: []string{builderPackageHost}}}
	if diff := cmp.Diff(wantImages, pkg.Requirements.Images); diff != "" {
		t.Errorf("POST /builder/package with every section: images mismatch (-want +got):\n%s", diff)
	}

	// A copy of the library's icon.
	icon, ok := pkg.Document.Icons[builderPackageIcon]
	if !ok {
		t.Errorf("POST /builder/package with every section: icons = %v, want %s among them", pkg.Document.Icons, builderPackageIcon)
	} else if want := base64.StdEncoding.EncodeToString(builderIconPNG(t, 4, 4, 7)); icon.Data != want {
		t.Errorf("POST /builder/package with every section: data of icon %s = %q, want %q", builderPackageIcon, icon.Data, want)
	}

	warnings, err := builderPackageWarnings(response.Warnings)
	if err != nil {
		t.Errorf("POST /builder/package with every section: %v", err)
	}
	unreadableScenario := "Scenario config pkg-gone does not exist on this server, or your role cannot read it. " +
		"The package names it but does not carry it."
	unreadableTopology := "Included topology pkg-hidden does not exist on this server, or your role cannot read it. " +
		"The package names it but does not carry it."
	filePath := "Included topology /phenix/x.yml is a file path. The package names it but does not carry it."

	for _, want := range []struct {
		message string
		code    bdoc.Code
	}{
		{message: unreadableScenario, code: bdoc.CodePackageConfigUnreadable},
		{message: unreadableTopology, code: bdoc.CodePackageConfigUnreadable},
		{message: filePath, code: bdoc.CodePackageIncludeFilePath},
	} {
		if got, found := warnings[want.message]; !found || got != want.code {
			t.Errorf("POST /builder/package with every section: warnings = %+v, want %q with the code %s",
				response.Warnings, want.message, want.code)
		}
	}
}

func TestBuilderBuildPackageWithNoSection(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)

	status, response := buildBuilderPackage(t, harness, nil, document)
	if status != http.StatusOK {
		t.Fatalf("POST /builder/package with no section: status = %d, want %d", status, http.StatusOK)
	}

	// Nothing but the document.
	type carried struct {
		Scenarios, Topologies map[string]bdoc.PackageConfig
		Icons                 map[string]bdoc.Icon
	}

	pkg := response.Package
	if diff := cmp.Diff(carried{}, carried{Scenarios: pkg.Scenarios, Topologies: pkg.Topologies, Icons: pkg.Document.Icons}); diff != "" {
		t.Errorf("POST /builder/package with no section: carried mismatch (-want +got):\n%s", diff)
	}

	// What it needs is listed all the same, with the apps of a scenario read but not carried.
	type needs struct {
		Scenarios, Topologies, Apps, Icons []string
		Images                             int
	}

	wantNeeds := needs{
		Scenarios: []string{"pkg-sc", "pkg-gone"}, Topologies: []string{"pkg-inc", "pkg-hidden", "/phenix/x.yml"},
		Apps: []string{"pkg-sc-app"}, Icons: []string{builderPackageIcon},
	}
	gotNeeds := needs{
		Scenarios: pkg.Requirements.Scenarios, Topologies: pkg.Requirements.Topologies, Apps: pkg.Requirements.Apps,
		Icons: pkg.Requirements.Icons, Images: len(pkg.Requirements.Images),
	}

	if diff := cmp.Diff(wantNeeds, gotNeeds); diff != "" {
		t.Errorf("POST /builder/package with no section: requirements mismatch (-want +got):\n%s", diff)
	}

	warnings, err := builderPackageWarnings(response.Warnings)
	if err != nil {
		t.Errorf("POST /builder/package with no section: %v", err)
	}

	want := map[string]bdoc.Code{
		"Scenario config pkg-gone does not exist on this server, or your role cannot read it. " +
			"The package lists none of its apps.": bdoc.CodePackageConfigUnreadable,
	}
	if diff := cmp.Diff(want, warnings); diff != "" {
		t.Errorf("POST /builder/package with no section: warnings mismatch (-want +got):\n%s", diff)
	}
}

func TestBuilderBuildPackageRefusals(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)

	for _, include := range [][]string{{"nodes"}, {"icons", "icons"}} {
		t.Run("include "+strings.Join(include, " and "), func(t *testing.T) {
			if status, _ := buildBuilderPackage(t, harness, nil, document, include...); status != http.StatusBadRequest {
				t.Errorf("POST /builder/package including %q: status = %d, want %d", include, status, http.StatusBadRequest)
			}
		})
	}

	invalid := builderPackageDocument(t, nil, nil)
	invalid.Nodes = nil

	if status, _ := buildBuilderPackage(t, harness, nil, invalid); status != http.StatusUnprocessableEntity {
		t.Errorf("POST /builder/package of an invalid document: status = %d, want %d", status, http.StatusUnprocessableEntity)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/package", body: `{"document": {}, "extra": 1}`, user: builderAlice,
	})
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("POST /builder/package with an unknown field: status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

// TestBuilderBuildPackageHidesUnreadableConfigs asserts a config the role may
// not read, by configs get or its kind's list, is named as one that does not exist.
func TestBuilderBuildPackageHidesUnreadableConfigs(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)
	kinds := builderPolicy([]string{"scenarios", "topologies"}, []string{"*"}, []string{"list"})

	for name, test := range map[string]struct {
		role    *rbac.Role
		include []string
		hidden  []string
	}{
		"scenarios without configs get": {
			role:    builderRole(builderPolicy([]string{"configs"}, []string{"Topology/*", "Scenario/pkg-gone"}, []string{"list", "get"}), kinds),
			include: []string{"scenarios"}, hidden: []string{"Scenario config pkg-sc", "Scenario config pkg-gone"},
		},
		"includes without configs get": {
			role:    builderRole(builderPolicy([]string{"configs"}, []string{"Scenario/*", "Topology/pkg-hidden"}, []string{"list", "get"}), kinds),
			include: []string{"topologies"}, hidden: []string{"Included topology pkg-inc", "Included topology pkg-hidden"},
		},
		"without the list permission of the kinds": {
			role:    builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"})),
			include: []string{"scenarios", "topologies"}, hidden: []string{"Scenario config pkg-sc", "Included topology pkg-inc"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			status, response := buildBuilderPackage(t, harness, test.role, document, test.include...)
			if status != http.StatusOK {
				t.Fatalf("POST /builder/package including %q: status = %d, want %d", test.include, status, http.StatusOK)
			}

			// Nothing read.
			if response.Package.Scenarios != nil || response.Package.Topologies != nil {
				t.Errorf("POST /builder/package including %q: the package carries %v and %v, want nothing",
					test.include, response.Package.Scenarios, response.Package.Topologies)
			}

			// The apps of a scenario the role may not read are not listed either.
			if apps := response.Package.Requirements.Apps; slices.Contains(test.hidden, "Scenario config pkg-sc") && len(apps) != 0 {
				t.Errorf("POST /builder/package including %q: apps = %q, want none", test.include, apps)
			}

			warnings, err := builderPackageWarnings(response.Warnings)
			if err != nil {
				t.Errorf("POST /builder/package including %q: %v", test.include, err)
			}

			for _, hidden := range test.hidden {
				what, config, _ := strings.Cut(hidden, " pkg-")
				if want := builderUnreadableWarning(what, "pkg-"+config); warnings[want] != bdoc.CodePackageConfigUnreadable {
					t.Errorf("POST /builder/package including %q: warnings = %+v, want %q with the code %s",
						test.include, response.Warnings, want, bdoc.CodePackageConfigUnreadable)
				}
			}
		})
	}
}

// TestBuilderBuildPackageRefusesWhatWouldNotLoad asserts a package the resolve
// route would refuse is never answered: the refusal is package.invalid with the issue.
func TestBuilderBuildPackageRefusesWhatWouldNotLoad(t *testing.T) {
	scenario := namedScenario(t, "pkg-nospec", "")
	scenario.Spec = nil

	harness := newBuilderHarness(t, scenario)
	body := builderJSON(t, map[string]any{
		"document": builderPackageDocument(t, []string{"pkg-nospec"}, nil), "include": []string{"scenarios"},
	})

	status, refusal := builderPackageRefusal(t, harness, "/builder/package", body)
	if status != http.StatusUnprocessableEntity {
		t.Errorf("POST /builder/package carrying pkg-nospec: status = %d, want %d", status, http.StatusUnprocessableEntity)
	}

	if refusal.Code != string(bdoc.CodePackageInvalid) {
		t.Errorf("POST /builder/package carrying pkg-nospec: code = %q, want %q", refusal.Code, bdoc.CodePackageInvalid)
	}

	// An issue names the config without a spec.
	if !slices.ContainsFunc(refusal.Issues, func(issue bdoc.Issue) bool {
		return issue.Code == bdoc.CodePackageConfigSpecMissing && issue.Path == "scenarios.pkg-nospec.spec"
	}) {
		t.Errorf("POST /builder/package carrying pkg-nospec: issues = %+v, want %s at scenarios.pkg-nospec.spec",
			refusal.Issues, bdoc.CodePackageConfigSpecMissing)
	}
}

// TestBuilderPackageRefusesTooMuch asserts a package the build route would make,
// or a body the resolve route gets, larger than MaxPackageBytes is refused with package.too-large.
func TestBuilderPackageRefusesTooMuch(t *testing.T) {
	scenario := namedScenario(t, "pkg-big", "")
	scenario.Spec = map[string]any{"apps": []any{map[string]any{
		"name":     "big",
		"metadata": map[string]any{"blob": strings.Repeat("a", bdoc.MaxPackageBytes)},
	}}}

	harness := newBuilderHarness(t, scenario)
	document := builderPackageDocument(t, []string{"pkg-big"}, nil)
	request := builderJSON(t, map[string]any{"document": document, "include": []string{"scenarios"}})

	status, refusal := builderPackageRefusal(t, harness, "/builder/package", request)
	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("POST /builder/package carrying pkg-big: status = %d, want %d", status, http.StatusRequestEntityTooLarge)
	}

	if refusal.Code != string(bdoc.CodePackageTooLarge) {
		t.Errorf("POST /builder/package carrying pkg-big: code = %q, want %q", refusal.Code, bdoc.CodePackageTooLarge)
	}

	if status, _ := buildBuilderPackage(t, harness, nil, document); status != http.StatusOK {
		t.Errorf("POST /builder/package naming pkg-big: status = %d, want %d", status, http.StatusOK)
	}

	status, refusal = builderPackageRefusal(t, harness, "/builder/package/resolve",
		`{"$schema": "`+strings.Repeat("x", bdoc.MaxPackageBytes)+`"}`)
	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("POST /builder/package/resolve of a body above the limit: status = %d, want %d",
			status, http.StatusRequestEntityTooLarge)
	}

	if refusal.Code != string(bdoc.CodePackageTooLarge) {
		t.Errorf("POST /builder/package/resolve of a body above the limit: code = %q, want %q", refusal.Code, bdoc.CodePackageTooLarge)
	}
}

// TestBuilderBuildPackageLeavesOutWhatCannotBeListed asserts a requirement with a
// control character is left out with a warning, and the package still resolves.
func TestBuilderBuildPackageLeavesOutWhatCannotBeListed(t *testing.T) {
	scenario := namedScenario(t, "pkg-odd", "")
	scenario.Spec = map[string]any{"apps": []any{map[string]any{"name": "ok-app"}, map[string]any{"name": "bad\napp"}}}

	harness := newBuilderHarness(t, scenario)

	status, response := buildBuilderPackage(t, harness, nil, builderPackageDocument(t, []string{"pkg-odd"}, nil), "scenarios")
	if status != http.StatusOK {
		t.Fatalf("POST /builder/package carrying pkg-odd: status = %d, want %d", status, http.StatusOK)
	}

	if apps, want := response.Package.Requirements.Apps, []string{"ok-app"}; !slices.Equal(apps, want) {
		t.Errorf("POST /builder/package carrying pkg-odd: apps = %q, want %q", apps, want)
	}

	warnings, err := builderPackageWarnings(response.Warnings)
	if err != nil {
		t.Errorf("POST /builder/package carrying pkg-odd: %v", err)
	}

	want := `The package does not list app "bad\napp": it must not contain control characters.`
	if warnings[want] != bdoc.CodePackageRequirementLeftOut {
		t.Errorf("POST /builder/package carrying pkg-odd: warnings = %+v, want %q with the code %s",
			response.Warnings, want, bdoc.CodePackageRequirementLeftOut)
	}

	if status, _ := resolveBuilderPackage(t, harness, nil, response.Package); status != http.StatusOK {
		t.Errorf("POST /builder/package/resolve of the package: status = %d, want %d", status, http.StatusOK)
	}
}

// bapiDocumentAnnotationForTest is the Builder's own annotation a stored
// topology names its published document by.
const bapiDocumentAnnotationForTest = "builder-doc"

// resolveBuilderPackage asks the harness which of what a package needs it
// has, and returns the status and the dependencies by kind and name.
func resolveBuilderPackage(
	t *testing.T,
	harness *builderHarness,
	role *rbac.Role,
	pkg *bdoc.Package,
) (int, map[string]builderPackageDependency) {
	t.Helper()

	body, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("encoding the package: %v", err)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/package/resolve", body: string(body), user: builderAlice, role: role,
	})
	if recorder.Code != http.StatusOK {
		return recorder.Code, nil
	}

	var response builderPackageResolveResponse

	decodeBuilderAnswer(t, recorder, &response)

	dependencies := map[string]builderPackageDependency{}
	for _, dependency := range response.Dependencies {
		dependencies[dependency.Kind+"/"+dependency.Name] = dependency
	}

	return recorder.Code, dependencies
}

// builderResolvePackage is the package the resolve tests send: scenarios the
// server has the same, different and not at all, icons, images, apps and a file.
func builderResolvePackage(t *testing.T, harness *builderHarness) *bdoc.Package {
	t.Helper()

	same, err := harness.getConfig("Scenario/pkg-same")
	if err != nil {
		t.Fatalf("reading pkg-same: %v", err)
	}

	different := builderPackageConfig(same)
	different.Metadata.Name = "pkg-diff"

	created := builderPackageConfig(same)
	created.Metadata.Name = "pkg-new"

	topology := builderConfig(t, builderKindTopology, "pkg-inc")

	document := builderPackageDocument(t,
		[]string{"pkg-same", "pkg-diff", "pkg-new", "pkg-listed"}, []string{"pkg-inc"})
	document.Icons = map[string]bdoc.Icon{
		builderPackageIcon: {Data: base64.StdEncoding.EncodeToString(builderIconPNG(t, 4, 4, 7))},
		"pkg-other":        {Data: base64.StdEncoding.EncodeToString(builderIconPNG(t, 4, 4, 9))},
		"pkg-absent":       {Data: base64.StdEncoding.EncodeToString(builderIconPNG(t, 4, 4, 11))},
	}
	document.Nodes[0].Device.Spec["hardware"] = map[string]any{"drives": []any{
		map[string]any{"image": "/phenix/images/" + builderPackageImage},
		map[string]any{"image": "gone.qc2"},
	}}

	pkg := bdoc.NewPackage(document, bdoc.PackageContents{
		Scenarios: map[string]bdoc.PackageConfig{
			"pkg-same": builderPackageConfig(same), "pkg-diff": different, "pkg-new": created,
		},
		CarryScenarios: true,
		Topologies:     map[string]bdoc.PackageConfig{"pkg-inc": builderPackageConfig(&topology)},
		Images:         true,
	})

	pkg.Requirements.Icons = []string{builderPackageIcon, "pkg-other", "pkg-absent", "pkg-unknown"}
	pkg.Requirements.Templates = []string{"Edge router"}
	pkg.Requirements.Apps = []string{"pkg-app", "ghost-app"}
	pkg.Requirements.Files = []string{"/phenix/injects/a.boot"}

	diff := pkg.Scenarios["pkg-diff"]
	diff.Spec = map[string]any{"apps": []any{map[string]any{"name": "another-app"}}}
	pkg.Scenarios["pkg-diff"] = diff

	if err := pkg.Validate(); err != nil {
		t.Fatalf("the test package is not valid: %v", err)
	}

	return pkg
}

// newBuilderResolveHarness returns a harness with scenarios pkg-same and
// pkg-diff, icons pkg-icon and pkg-other, image pkg.qc2 (or disksErr) and app pkg-app.
func newBuilderResolveHarness(t *testing.T, disksErr error) *builderHarness {
	t.Helper()

	return newBuilderResolveHarnessWith(t, func() ([]disk.Details, error) {
		if disksErr != nil {
			return nil, disksErr
		}

		return []disk.Details{{Name: builderPackageImage, FullPath: "/phenix/images/" + builderPackageImage}}, nil
	})
}

// newBuilderResolveHarnessWith is [newBuilderResolveHarness] whose server
// lists the disk images disks lists.
func newBuilderResolveHarnessWith(t *testing.T, disks func() ([]disk.Details, error)) *builderHarness {
	t.Helper()

	harness := newBuilderHarnessWith(t, []builderOption{
		withBuilderDisks(disks),
		withBuilderApps(func() []string { return []string{"pkg-app", "ntp"} }),
	}, namedScenario(t, "pkg-same", "topo"), namedScenario(t, "pkg-diff", "topo"))

	for name, seed := range map[string]int{builderPackageIcon: 7, "pkg-other": 8} {
		if recorder := harness.postIcon(builderAlice, name, builderIconPNG(t, 4, 4, seed)); recorder.Code != http.StatusCreated {
			t.Fatalf("adding icon %s: status = %d", name, recorder.Code)
		}
	}

	return harness
}

func TestBuilderResolvePackage(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)
	pkg := builderResolvePackage(t, harness)

	status, dependencies := resolveBuilderPackage(t, harness, nil, pkg)
	if status != http.StatusOK {
		t.Fatalf("POST /builder/package/resolve: status = %d, want %d", status, http.StatusOK)
	}

	type dependency struct {
		Status   string
		Packaged bool
	}

	want := map[string]dependency{
		"scenario/pkg-same":            {builderDependencyPresent, true},
		"scenario/pkg-diff":            {builderDependencyDifferent, true},
		"scenario/pkg-new":             {builderDependencyMissing, true},
		"scenario/pkg-listed":          {builderDependencyMissing, false},
		"topology/pkg-inc":             {builderDependencyMissing, true},
		"template/Edge router":         {builderDependencyPresent, true},
		"icon/pkg-icon":                {builderDependencyPresent, true},
		"icon/pkg-other":               {builderDependencyDifferent, true},
		"icon/pkg-absent":              {builderDependencyMissing, true},
		"icon/pkg-unknown":             {builderDependencyMissing, false},
		"image//phenix/images/pkg.qc2": {builderDependencyPresent, false},
		"image/gone.qc2":               {builderDependencyMissing, false},
		"app/pkg-app":                  {builderDependencyPresent, false},
		"app/ghost-app":                {builderDependencyMissing, false},
		"file//phenix/injects/a.boot":  {builderDependencyUnknown, false},
	}

	got := make(map[string]dependency, len(want))
	for key := range want {
		if listed, ok := dependencies[key]; ok {
			got[key] = dependency{Status: listed.Status, Packaged: listed.Packaged}
		}
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("POST /builder/package/resolve: dependencies mismatch (-want +got):\n%s", diff)
	}

	// The detail names the device that uses the image.
	if got, want := dependencies["image//phenix/images/"+builderPackageImage].Detail, "Devices that use it: "+builderPackageHost+"."; got != want {
		t.Errorf("POST /builder/package/resolve: detail of image /phenix/images/%s = %q, want %q", builderPackageImage, got, want)
	}

	if harness.configWrites != 0 {
		t.Errorf("POST /builder/package/resolve wrote %d configs, want none", harness.configWrites)
	}
}

// TestBuilderResolvePackageAsTheRoleMayList asserts each dependency is checked
// under the caller's permissions, and one it may not list by name reads missing.
func TestBuilderResolvePackageAsTheRoleMayList(t *testing.T) {
	var (
		configs   = builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"})
		kinds     = builderPolicy([]string{"scenarios", "topologies"}, []string{"*"}, []string{"list"})
		stored    = "image//phenix/images/" + builderPackageImage
		elsewhere = "image//other/images/" + builderPackageImage
		used      = "Devices that use it: " + builderPackageHost + "."
		absent    = "This server has no disk image of this name. " + used
		noApp     = "This server has no app of this name."
		unlisted  = "The server could not list its disk images."
		disks     = func() ([]disk.Details, error) {
			return []disk.Details{{Name: builderPackageImage, FullPath: "/phenix/images/" + builderPackageImage}}, nil
		}
	)

	for name, test := range map[string]struct {
		role  *rbac.Role
		disks func() ([]disk.Details, error)
		// want is each dependency's status and the start of its detail.
		want map[string][2]string
	}{
		"configs get on two scenarios only": {
			role: builderRole(
				builderPolicy([]string{"configs"}, []string{"Scenario/pkg-same", "Scenario/pkg-new"}, []string{"list", "get"}),
				builderPolicy([]string{"scenarios"}, []string{"*"}, []string{"list"}),
			),
			want: map[string][2]string{
				"scenario/pkg-same": {builderDependencyPresent}, "scenario/pkg-new": {builderDependencyMissing},
				"scenario/pkg-diff": {builderDependencyUnknown}, "topology/pkg-inc": {builderDependencyUnknown},
				"app/pkg-app":    {builderDependencyUnknown},
				"image/gone.qc2": {builderDependencyUnknown, "Your role cannot list disk images."},
			},
		},
		"disks that cannot be listed": {
			disks: func() ([]disk.Details, error) { return nil, errors.New("minimega is not running") },
			want:  map[string][2]string{"image/gone.qc2": {builderDependencyUnknown, unlisted}},
		},
		"no disks listed": {
			disks: func() ([]disk.Details, error) { return nil, nil },
			want: map[string][2]string{
				"image/gone.qc2": {builderDependencyUnknown, unlisted}, stored: {builderDependencyUnknown, unlisted},
				"app/pkg-app": {builderDependencyPresent},
			},
		},
		"icons without configs list": {
			role: builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"get"}), kinds),
			want: map[string][2]string{
				"icon/" + builderPackageIcon: {builderDependencyPresent}, "icon/pkg-other": {builderDependencyDifferent},
				"icon/pkg-absent": {builderDependencyMissing}, "scenario/pkg-same": {builderDependencyPresent},
			},
		},
		"images and apps not allowed by name": {
			role: builderRole(configs, kinds,
				builderPolicy([]string{"disks"}, []string{"gone*"}, []string{"list"}),
				builderPolicy([]string{"applications"}, []string{"ntp"}, []string{"list"}),
			),
			want: map[string][2]string{
				stored: {builderDependencyMissing, absent}, elsewhere: {builderDependencyMissing, absent},
				"app/pkg-app": {builderDependencyMissing, noApp},
			},
		},
		"images and apps allowed by name": {
			role: builderRole(configs, kinds,
				builderPolicy([]string{"disks"}, []string{builderPackageImage}, []string{"list"}),
				builderPolicy([]string{"applications"}, []string{"pkg-app"}, []string{"list"}),
			),
			want: map[string][2]string{
				stored: {builderDependencyPresent, used}, "app/pkg-app": {builderDependencyPresent},
				elsewhere: {
					builderDependencyPresent,
					"Builder found it by file name " + builderPackageImage + ". This server's image is /phenix/images/" + builderPackageImage,
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if test.disks == nil {
				test.disks = disks
			}

			harness := newBuilderResolveHarnessWith(t, test.disks)
			pkg := builderResolvePackage(t, harness)
			pkg.Requirements.Images = append(pkg.Requirements.Images,
				bdoc.PackageImage{Name: strings.TrimPrefix(elsewhere, "image/"), UsedBy: []string{builderPackageHost}})

			status, dependencies := resolveBuilderPackage(t, harness, test.role, pkg)
			if status != http.StatusOK {
				t.Fatalf("POST /builder/package/resolve: status = %d, want %d", status, http.StatusOK)
			}

			for key, want := range test.want {
				if got := dependencies[key]; got.Status != want[0] || !strings.HasPrefix(got.Detail, want[1]) {
					t.Errorf("POST /builder/package/resolve: %s = %s (%q), want %s (detail starting %q)",
						key, got.Status, got.Detail, want[0], want[1])
				}
			}
		})
	}
}

// TestBuilderResolvePackageRefusals asserts a package that does not validate
// is refused with package.invalid and its issues, and two JSON values as malformed.
func TestBuilderResolvePackageRefusals(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)

	annotated := builderResolvePackage(t, harness)
	created := annotated.Scenarios["pkg-new"]
	created.Metadata.Annotations = map[string]string{"builder-doc": `{"id":"forged"}`}
	annotated.Scenarios["pkg-new"] = created

	long := builderResolvePackage(t, harness)
	for i := range bdoc.MaxPackageRequirements + 1 {
		long.Requirements.Files = append(long.Requirements.Files, "/phenix/f"+strconv.Itoa(i))
	}

	var unknown map[string]any
	if err := json.Unmarshal([]byte(builderJSON(t, builderResolvePackage(t, harness))), &unknown); err != nil {
		t.Fatalf("decoding the package: %v", err)
	}

	unknown["extra"] = true

	for name, test := range map[string]struct {
		body  string
		issue bdoc.Code
	}{
		"a Builder annotation": {body: builderJSON(t, annotated), issue: bdoc.CodePackageConfigBuilderNote},
		"a list too long":      {body: builderJSON(t, long)},
		"an unknown field":     {body: builderJSON(t, unknown)},
		"no requirements": {
			body:  `{"$schema": "` + bdoc.PackageSchemaURI + `", "document": ` + string(builderDocument(t, "x")) + `}`,
			issue: bdoc.CodePackageRequirementsMissing,
		},
		"a document of a schema": {body: string(builderDocument(t, "x"))},
	} {
		t.Run(name, func(t *testing.T) {
			status, refusal := builderPackageRefusal(t, harness, "/builder/package/resolve", test.body)
			if status != http.StatusUnprocessableEntity {
				t.Errorf("POST /builder/package/resolve: status = %d, want %d", status, http.StatusUnprocessableEntity)
			}

			if refusal.Code != string(bdoc.CodePackageInvalid) {
				t.Errorf("POST /builder/package/resolve: code = %q, want %q", refusal.Code, bdoc.CodePackageInvalid)
			}

			if test.issue != "" && !slices.ContainsFunc(refusal.Issues, func(issue bdoc.Issue) bool { return issue.Code == test.issue }) {
				t.Errorf("POST /builder/package/resolve: issues = %+v, want one of the code %s", refusal.Issues, test.issue)
			}
		})
	}

	status, refusal := builderPackageRefusal(t, harness, "/builder/package/resolve", `{} {}`)
	if status != http.StatusBadRequest {
		t.Errorf("POST /builder/package/resolve of two JSON values: status = %d, want %d", status, http.StatusBadRequest)
	}

	if refusal.Code != string(bdoc.CodeRequestInvalid) {
		t.Errorf("POST /builder/package/resolve of two JSON values: code = %q, want %q", refusal.Code, bdoc.CodeRequestInvalid)
	}
}

// TestBuilderPackageConfigLeavesOutTheBuilders asserts a packaged config keeps
// the config's annotations but not the Builder's, and none of its times.
func TestBuilderPackageConfigLeavesOutTheBuilders(t *testing.T) {
	config := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    builderKindTopology,
		Metadata: store.ConfigMetadata{
			Name:        "pkg",
			Created:     "2026-10-01T00:00:00Z",
			Updated:     "2026-10-02T00:00:00Z",
			Annotations: store.Annotations{"builder-xml": "<x/>", "team": "plant"},
		},
		Spec: map[string]any{"nodes": []any{}},
	}

	packaged := builderPackageConfig(&config)

	encoded, err := json.Marshal(packaged)
	if err != nil {
		t.Fatalf("encoding the config: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decoding the packaged config %s: %v", encoded, err)
	}

	want := map[string]any{
		"apiVersion": "phenix.sandia.gov/v1", "kind": "Topology",
		"metadata": map[string]any{"name": "pkg", "annotations": map[string]any{"team": "plant"}},
		"spec":     map[string]any{"nodes": []any{}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("builderPackageConfig(topology pkg) mismatch (-want +got):\n%s", diff)
	}
}
