package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"phenix/api/disk"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
)

// builderPackageHost is the hostname of the device of the package tests'
// document, which boots from builderPackageImage and names the custom icon
// builderPackageIcon.
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
		method: http.MethodPost, path: "/builder/package", body: string(body), user: builderTestOwner, role: role,
	})

	var response builderPackageResponse

	if recorder.Code == http.StatusOK {
		harness.decode(recorder, &response)
	}

	return recorder.Code, response
}

// builderPackageWarnings returns each warning of a package by what it says,
// and fails the test for one that is not a warning with a code of the
// registry.
func builderPackageWarnings(t *testing.T, warnings []bdoc.Issue) map[string]bdoc.Code {
	t.Helper()

	byMessage := make(map[string]bdoc.Code, len(warnings))

	for _, warning := range warnings {
		if _, known := bdoc.LookupCode(warning.Code); !known || warning.Severity != bdoc.SeverityWarning {
			t.Errorf("warning %+v is not a warning with a registered code", warning)
		}

		byMessage[warning.Message] = warning.Code
	}

	return byMessage
}

// builderPackageRefusal sends body to the package route at path and returns
// the status and the refusal it answers.
func builderPackageRefusal(t *testing.T, harness *builderHarness, path, body string) (int, builderErrorBody) {
	t.Helper()

	recorder := harness.do(builderRequest{method: http.MethodPost, path: path, body: body, user: builderTestOwner})

	var refusal builderErrorBody

	if recorder.Code != http.StatusOK {
		harness.decode(recorder, &refusal)
	}

	return recorder.Code, refusal
}

func TestBuilderPackageSchemaRoute(t *testing.T) {
	harness := newBuilderHarness(t)

	recorder := harness.do(builderRequest{method: http.MethodGet, path: builderPackageSchemaPath, user: builderTestOwner})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	want, err := bdoc.PackageSchemaJSON()
	if err != nil {
		t.Fatalf("PackageSchemaJSON returned error: %v", err)
	}

	if recorder.Body.String() != string(want) {
		t.Fatal("the route does not serve the package schema")
	}
}

// TestBuilderPackageRoutePermissions asks the package routes as callers
// with and without the permission each takes: the schema takes that of the
// document schema, schemas get on builder, and building and resolving a
// package take configs get, without which nothing is read or checked.
func TestBuilderPackageRoutePermissions(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)

	resolveBody, err := json.Marshal(builderResolvePackage(t, harness))
	if err != nil {
		t.Fatalf("encoding the package: %v", err)
	}

	buildBody, err := json.Marshal(map[string]any{"document": builderPackageDocument(t, nil, nil), "include": []string{}})
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	var (
		configsOnly = builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}))
		schemasOnly = builderRole(builderPolicy([]string{"schemas"}, []string{"*"}, []string{"get"}))
		schema      = builderRequest{method: http.MethodGet, path: builderPackageSchemaPath}
		build       = builderRequest{method: http.MethodPost, path: "/builder/package", body: string(buildBody)}
		resolve     = builderRequest{method: http.MethodPost, path: "/builder/package/resolve", body: string(resolveBody)}
	)

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{name: "the schema with every permission", user: builderTestOwner, request: schema, status: http.StatusOK},
		{
			name: "the schema without schemas get", user: builderTestOwner, role: &configsOnly,
			request: schema, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{name: "the schema with no identity", request: schema, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden},
		{name: "a build with every permission", user: builderTestOwner, request: build, status: http.StatusOK},
		{
			name: "a build without configs get", user: builderTestOwner, role: &schemasOnly,
			request: build, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{name: "a build with no identity", request: build, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden},
		{name: "a resolve with every permission", user: builderTestOwner, request: resolve, status: http.StatusOK},
		{
			name: "a resolve without configs get", user: builderTestOwner, role: &schemasOnly,
			request: resolve, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{name: "a resolve with no identity", request: resolve, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden},
	})
}

// newBuilderPackageHarness returns a harness with the Scenario config
// pkg-sc, the topology pkg-inc and the icon pkg-icon, and a document that
// names the scenarios pkg-sc and pkg-gone and includes pkg-inc, pkg-hidden
// and a file. The test fails if it writes a config.
func newBuilderPackageHarness(t *testing.T) (*builderHarness, *bdoc.Document) {
	t.Helper()

	scenario := namedScenario(t, "pkg-sc", "topo")
	scenario.Metadata.Annotations[bapiDocumentAnnotationForTest] = `{"id":"x"}`

	harness := newBuilderHarness(t, scenario, builderConfig(t, builderKindTopology, "pkg-inc"))

	if recorder := harness.postIcon(builderTestOwner, builderPackageIcon, builderIconPNG(t, 4, 4, 7)); recorder.Code != http.StatusCreated {
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
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	pkg := response.Package
	if err := pkg.Validate(); err != nil {
		t.Fatalf("the package is not valid: %v", err)
	}

	if _, ok := pkg.Scenarios["pkg-sc"]; !ok || len(pkg.Scenarios) != 1 {
		t.Fatalf("scenarios = %v, want pkg-sc", pkg.Scenarios)
	}

	carried := pkg.Scenarios["pkg-sc"]
	if carried.Metadata.Annotations["keep"] != "yes" || carried.Metadata.Annotations["topology"] != "topo" {
		t.Errorf("annotations = %v, want the scenario's own", carried.Metadata.Annotations)
	}

	if _, builderOwn := carried.Metadata.Annotations[bapiDocumentAnnotationForTest]; builderOwn {
		t.Errorf("annotations = %v, want none of the Builder's", carried.Metadata.Annotations)
	}

	if _, ok := pkg.Topologies["pkg-inc"]; !ok || len(pkg.Topologies) != 1 {
		t.Fatalf("topologies = %v, want pkg-inc", pkg.Topologies)
	}

	if want := []string{"pkg-sc-app"}; !slices.Equal(pkg.Requirements.Apps, want) {
		t.Errorf("apps = %v, want %v", pkg.Requirements.Apps, want)
	}

	if len(pkg.Requirements.Images) != 1 || pkg.Requirements.Images[0].Name != builderPackageImage ||
		!slices.Equal(pkg.Requirements.Images[0].UsedBy, []string{builderPackageHost}) {
		t.Errorf("images = %+v, want %s used by %s", pkg.Requirements.Images, builderPackageImage, builderPackageHost)
	}

	icon, ok := pkg.Document.Icons[builderPackageIcon]
	if !ok || icon.Data != base64.StdEncoding.EncodeToString(builderIconPNG(t, 4, 4, 7)) {
		t.Errorf("icons = %v, want a copy of the library's %s", pkg.Document.Icons, builderPackageIcon)
	}

	warnings := builderPackageWarnings(t, response.Warnings)
	unreadableScenario := "Scenario config pkg-gone does not exist on this server, or your role cannot read it: " +
		"the package names it but does not carry it."
	unreadableTopology := "Included topology pkg-hidden does not exist on this server, or your role cannot read it: " +
		"the package names it but does not carry it."
	filePath := "Included topology /phenix/x.yml is a file path: the package names it but does not carry it."

	for _, want := range []struct {
		message string
		code    bdoc.Code
	}{
		{message: unreadableScenario, code: bdoc.CodePackageConfigUnreadable},
		{message: unreadableTopology, code: bdoc.CodePackageConfigUnreadable},
		{message: filePath, code: bdoc.CodePackageIncludeFilePath},
	} {
		if got, found := warnings[want.message]; !found || got != want.code {
			t.Errorf("warnings = %+v, want %q with the code %s", response.Warnings, want.message, want.code)
		}
	}
}

func TestBuilderBuildPackageWithNoSection(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)

	status, response := buildBuilderPackage(t, harness, nil, document)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	pkg := response.Package
	if pkg.Scenarios != nil || pkg.Topologies != nil || pkg.Document.Icons != nil {
		t.Errorf("the package carries %v, %v and %v, want nothing but the document",
			pkg.Scenarios, pkg.Topologies, pkg.Document.Icons)
	}

	// What it needs is listed all the same, and the apps of a scenario read
	// but not carried too.
	if want := []string{"pkg-sc", "pkg-gone"}; !slices.Equal(pkg.Requirements.Scenarios, want) {
		t.Errorf("scenarios = %v, want %v", pkg.Requirements.Scenarios, want)
	}

	if want := []string{"pkg-inc", "pkg-hidden", "/phenix/x.yml"}; !slices.Equal(pkg.Requirements.Topologies, want) {
		t.Errorf("topologies = %v, want %v", pkg.Requirements.Topologies, want)
	}

	if want := []string{"pkg-sc-app"}; !slices.Equal(pkg.Requirements.Apps, want) {
		t.Errorf("apps = %v, want %v", pkg.Requirements.Apps, want)
	}

	if len(pkg.Requirements.Images) != 0 || !slices.Equal(pkg.Requirements.Icons, []string{builderPackageIcon}) {
		t.Errorf("requirements = %+v, want no images and the icon", pkg.Requirements)
	}

	want := "Scenario config pkg-gone does not exist on this server, or your role cannot read it: " +
		"the package lists none of its apps."
	if warnings := builderPackageWarnings(t, response.Warnings); len(warnings) != 1 ||
		warnings[want] != bdoc.CodePackageConfigUnreadable {
		t.Errorf("warnings = %+v, want only %q with the code %s", response.Warnings, want, bdoc.CodePackageConfigUnreadable)
	}
}

// TestBuilderBuildPackageHidesUnreadableScenarios asserts a scenario the
// caller may not read is refused as one that does not exist.
func TestBuilderBuildPackageHidesUnreadableScenarios(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)
	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"Topology/*", "Scenario/pkg-gone"}, []string{"list", "get"}),
		builderPolicy([]string{"scenarios", "topologies"}, []string{"*"}, []string{"list"}),
	)

	status, response := buildBuilderPackage(t, harness, &role, document, "scenarios")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	if response.Package.Scenarios != nil {
		t.Errorf("scenarios = %v, want none", response.Package.Scenarios)
	}

	warnings := builderPackageWarnings(t, response.Warnings)

	for _, name := range []string{"pkg-sc", "pkg-gone"} {
		want := "Scenario config " + name + " does not exist on this server, or your role cannot read it: " +
			"the package names it but does not carry it."
		if warnings[want] != bdoc.CodePackageConfigUnreadable {
			t.Errorf("warnings = %+v, want %q", response.Warnings, want)
		}
	}
}

func TestBuilderBuildPackageRefusals(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)

	for _, include := range [][]string{{"nodes"}, {"icons", "icons"}} {
		if status, _ := buildBuilderPackage(t, harness, nil, document, include...); status != http.StatusBadRequest {
			t.Errorf("include %v: status = %d, want %d", include, status, http.StatusBadRequest)
		}
	}

	invalid := builderPackageDocument(t, nil, nil)
	invalid.Nodes = nil

	if status, _ := buildBuilderPackage(t, harness, nil, invalid); status != http.StatusUnprocessableEntity {
		t.Errorf("an invalid document: status = %d, want %d", status, http.StatusUnprocessableEntity)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/package", body: `{"document": {}, "extra": 1}`, user: builderTestOwner,
	})
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("an unknown field: status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

// TestBuilderBuildPackageWithoutKindListPermissions asserts configs get alone
// reads no scenario and no topology: each also needs the list permission of
// its kind, and is named but not carried without it.
func TestBuilderBuildPackageWithoutKindListPermissions(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)
	role := builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}))

	status, response := buildBuilderPackage(t, harness, &role, document, "scenarios", "topologies")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	pkg := response.Package
	if pkg.Scenarios != nil || pkg.Topologies != nil || len(pkg.Requirements.Apps) != 0 {
		t.Errorf("the package carries %v and %v and lists apps %v, want nothing read",
			pkg.Scenarios, pkg.Topologies, pkg.Requirements.Apps)
	}

	warnings := builderPackageWarnings(t, response.Warnings)

	for _, want := range []string{
		builderUnreadableWarning("Scenario config", "pkg-sc"),
		builderUnreadableWarning("Included topology", "pkg-inc"),
	} {
		if warnings[want] != bdoc.CodePackageConfigUnreadable {
			t.Errorf("warnings = %+v, want %q", response.Warnings, want)
		}
	}
}

// TestBuilderBuildPackageHidesUnreadableIncludes asserts an included
// topology that exists but may not be read is named in the words of one
// that does not exist.
func TestBuilderBuildPackageHidesUnreadableIncludes(t *testing.T) {
	harness, document := newBuilderPackageHarness(t)
	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"Scenario/*", "Topology/pkg-hidden"}, []string{"list", "get"}),
		builderPolicy([]string{"scenarios", "topologies"}, []string{"*"}, []string{"list"}),
	)

	status, response := buildBuilderPackage(t, harness, &role, document, "topologies")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	if response.Package.Topologies != nil {
		t.Errorf("topologies = %v, want none", response.Package.Topologies)
	}

	warnings := builderPackageWarnings(t, response.Warnings)

	for _, name := range []string{"pkg-inc", "pkg-hidden"} {
		if want := builderUnreadableWarning("Included topology", name); warnings[want] != bdoc.CodePackageConfigUnreadable {
			t.Errorf("warnings = %+v, want %q", response.Warnings, want)
		}
	}
}

// TestBuilderBuildPackageLeavesOutWhatCannotBeListed asserts the entries of
// the requirements a package cannot hold, which the document and a stored
// config may name, are left out with a warning naming each, and that the
// package answered loads where the resolve route checks it.
func TestBuilderBuildPackageLeavesOutWhatCannotBeListed(t *testing.T) {
	scenario := namedScenario(t, "pkg-odd", "")
	scenario.Spec = map[string]any{"apps": []any{
		map[string]any{"name": "ok-app"},
		map[string]any{"name": "bad\napp", "assetDir": "/phenix/assets/x\ty"},
	}}

	harness := newBuilderHarness(t, scenario)
	document := builderPackageDocument(t, []string{"pkg-odd"}, nil)
	document.Nodes[0].Device.Spec["injections"] = []any{
		map[string]any{"src": "/phenix/injects/a\nb", "dst": "/etc/a"},
	}

	status, response := buildBuilderPackage(t, harness, nil, document, "scenarios")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	pkg := response.Package
	if err := pkg.Validate(); err != nil {
		t.Fatalf("the package is not valid: %v", err)
	}

	if !slices.Equal(pkg.Requirements.Apps, []string{"ok-app"}) || len(pkg.Requirements.Files) != 0 {
		t.Errorf("apps = %q, files = %q, want ok-app and no file", pkg.Requirements.Apps, pkg.Requirements.Files)
	}

	warnings := builderPackageWarnings(t, response.Warnings)

	for _, want := range []string{
		`The package does not list app "bad\napp": it must not contain control characters.`,
		`The package does not list file "/phenix/assets/x\ty": it must not contain control characters.`,
		`The package does not list file "/phenix/injects/a\nb": it must not contain control characters.`,
	} {
		if warnings[want] != bdoc.CodePackageRequirementLeftOut {
			t.Errorf("warnings = %+v, want %q with the code %s", response.Warnings, want, bdoc.CodePackageRequirementLeftOut)
		}
	}

	if status, _ := resolveBuilderPackage(t, harness, nil, pkg); status != http.StatusOK {
		t.Errorf("resolving the package: status = %d, want %d", status, http.StatusOK)
	}
}

// TestBuilderBuildPackageRefusesWhatWouldNotLoad asserts a package the
// resolve route would refuse for anything but its requirements is never
// answered: the refusal, of the code package.invalid, names the issue, with
// its code.
func TestBuilderBuildPackageRefusesWhatWouldNotLoad(t *testing.T) {
	scenario := namedScenario(t, "pkg-nospec", "")
	scenario.Spec = nil

	harness := newBuilderHarness(t, scenario)
	document := builderPackageDocument(t, []string{"pkg-nospec"}, nil)

	body, err := json.Marshal(map[string]any{"document": document, "include": []string{"scenarios"}})
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	status, refusal := builderPackageRefusal(t, harness, "/builder/package", string(body))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", status, http.StatusUnprocessableEntity)
	}

	if !strings.Contains(refusal.Cause, "scenarios.pkg-nospec.spec: a config needs a spec") {
		t.Errorf("the refusal = %+v, want it to name the config without a spec", refusal)
	}

	if refusal.Code != string(bdoc.CodePackageInvalid) || !slices.ContainsFunc(refusal.Issues, func(issue bdoc.Issue) bool {
		return issue.Code == bdoc.CodePackageConfigSpecMissing && issue.Path == "scenarios.pkg-nospec.spec" &&
			issue.Severity == bdoc.SeverityError
	}) {
		t.Errorf("the refusal = %+v, want the code %s and the issue of the config without a spec, with its code",
			refusal, bdoc.CodePackageInvalid)
	}
}

// TestBuilderPackageRoutesRefuseTooMuch asserts a package larger than
// MaxPackageBytes is refused with 413 and the code package.too-large: the
// one the build route would make, and the body of the resolve route before
// it is decoded.
func TestBuilderPackageRoutesRefuseTooMuch(t *testing.T) {
	scenario := namedScenario(t, "pkg-big", "")
	scenario.Spec = map[string]any{"apps": []any{map[string]any{
		"name":     "big",
		"metadata": map[string]any{"blob": strings.Repeat("a", bdoc.MaxPackageBytes)},
	}}}

	harness := newBuilderHarness(t, scenario)
	document := builderPackageDocument(t, []string{"pkg-big"}, nil)

	request, err := json.Marshal(map[string]any{"document": document, "include": []string{"scenarios"}})
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	status, refusal := builderPackageRefusal(t, harness, "/builder/package", string(request))
	if status != http.StatusRequestEntityTooLarge || refusal.Code != string(bdoc.CodePackageTooLarge) {
		t.Errorf("building a package that carries pkg-big: %d %+v, want %d with the code %s",
			status, refusal, http.StatusRequestEntityTooLarge, bdoc.CodePackageTooLarge)
	}

	if status, _ := buildBuilderPackage(t, harness, nil, document); status != http.StatusOK {
		t.Errorf("building a package that only names pkg-big: status = %d, want %d", status, http.StatusOK)
	}

	status, refusal = builderPackageRefusal(t, harness, "/builder/package/resolve",
		`{"$schema": "`+strings.Repeat("x", bdoc.MaxPackageBytes)+`"}`)
	if status != http.StatusRequestEntityTooLarge || refusal.Code != string(bdoc.CodePackageTooLarge) {
		t.Errorf("resolving a body larger than the limit: %d %+v, want %d with the code %s",
			status, refusal, http.StatusRequestEntityTooLarge, bdoc.CodePackageTooLarge)
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
		method: http.MethodPost, path: "/builder/package/resolve", body: string(body), user: builderTestOwner, role: role,
	})
	if recorder.Code != http.StatusOK {
		return recorder.Code, nil
	}

	var response builderPackageResolveResponse

	harness.decode(recorder, &response)

	dependencies := map[string]builderPackageDependency{}
	for _, dependency := range response.Dependencies {
		dependencies[dependency.Kind+"/"+dependency.Name] = dependency
	}

	return recorder.Code, dependencies
}

// builderResolvePackage is the package the resolve tests send: scenarios
// the server has with the same spec (pkg-same), with another (pkg-diff), and
// not at all (pkg-new, carried, and pkg-listed, not carried); a topology the
// server lacks; three icons; two disk images; two apps; a template and a
// file.
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

// newBuilderResolveHarness returns a harness with the scenarios pkg-same
// and pkg-diff, the icons pkg-icon and pkg-other, the disk image pkg.qc2
// (or the error listing disks gives) and the app pkg-app.
func newBuilderResolveHarness(t *testing.T, disksErr error) *builderHarness {
	t.Helper()

	harness := newBuilderHarnessWith(t, []builderOption{
		withBuilderDiskImages(func() ([]disk.Details, error) {
			if disksErr != nil {
				return nil, disksErr
			}

			return []disk.Details{{Name: builderPackageImage, FullPath: "/phenix/images/" + builderPackageImage}}, nil
		}),
		withBuilderApps(func() []string { return []string{"pkg-app", "ntp"} }),
	}, namedScenario(t, "pkg-same", "topo"), namedScenario(t, "pkg-diff", "topo"))

	for name, seed := range map[string]int{builderPackageIcon: 7, "pkg-other": 8} {
		if recorder := harness.postIcon(builderTestOwner, name, builderIconPNG(t, 4, 4, seed)); recorder.Code != http.StatusCreated {
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
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	for key, want := range map[string]struct {
		status   string
		packaged bool
	}{
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
	} {
		got, ok := dependencies[key]
		if !ok {
			t.Errorf("%s is not listed", key)

			continue
		}

		if got.Status != want.status || got.Packaged != want.packaged {
			t.Errorf("%s = %s (packaged %t), want %s (packaged %t)", key, got.Status, got.Packaged, want.status, want.packaged)
		}
	}

	if got := dependencies["image//phenix/images/"+builderPackageImage].Detail; got != "Used by "+builderPackageHost+"." {
		t.Errorf("the image's detail = %q, want the device that uses it", got)
	}

	if harness.configWrites != 0 {
		t.Fatalf("resolving a package wrote %d configs, want none", harness.configWrites)
	}
}

func TestBuilderResolvePackageWithoutPermission(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)
	pkg := builderResolvePackage(t, harness)

	// configs get on pkg-same and pkg-new only, and no disks or
	// applications permission.
	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"Scenario/pkg-same", "Scenario/pkg-new"}, []string{"list", "get"}),
		builderPolicy([]string{"scenarios"}, []string{"*"}, []string{"list"}),
	)

	status, dependencies := resolveBuilderPackage(t, harness, &role, pkg)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	for key, want := range map[string]string{
		"scenario/pkg-same":   builderDependencyPresent,
		"scenario/pkg-new":    builderDependencyMissing,
		"scenario/pkg-diff":   builderDependencyUnknown,
		"scenario/pkg-listed": builderDependencyUnknown,
		"topology/pkg-inc":    builderDependencyUnknown,
		"image/gone.qc2":      builderDependencyUnknown,
		"app/pkg-app":         builderDependencyUnknown,
	} {
		if got := dependencies[key].Status; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	if got := dependencies["image/gone.qc2"].Detail; !strings.HasPrefix(got, "Your role cannot list disk images.") {
		t.Errorf("the image's detail = %q, want why it is not checked", got)
	}
}

func TestBuilderResolvePackageWhenDisksCannotBeListed(t *testing.T) {
	harness := newBuilderResolveHarness(t, errors.New("minimega is not running"))
	pkg := builderResolvePackage(t, harness)

	status, dependencies := resolveBuilderPackage(t, harness, nil, pkg)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	got := dependencies["image/gone.qc2"]
	if got.Status != builderDependencyUnknown || !strings.HasPrefix(got.Detail, "The server's disk images could not be listed.") {
		t.Errorf("image/gone.qc2 = %+v, want it unknown, since the disk images could not be listed", got)
	}
}

// TestBuilderResolvePackageIconsWithoutConfigsList asserts the icons are not
// checked for a caller that may not read the icon library, while the
// configs still are.
func TestBuilderResolvePackageIconsWithoutConfigsList(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)
	pkg := builderResolvePackage(t, harness)
	role := builderRole(
		builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"get"}),
		builderPolicy([]string{"scenarios", "topologies"}, []string{"*"}, []string{"list"}),
	)

	status, dependencies := resolveBuilderPackage(t, harness, &role, pkg)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	for _, name := range []string{builderPackageIcon, "pkg-other", "pkg-absent", "pkg-unknown"} {
		got := dependencies["icon/"+name]
		if got.Status != builderDependencyUnknown || got.Detail != "Your role cannot read the icon library." {
			t.Errorf("icon/%s = %+v, want it not checked", name, got)
		}
	}

	if got := dependencies["scenario/pkg-same"].Status; got != builderDependencyPresent {
		t.Errorf("scenario/pkg-same = %q, want %q", got, builderDependencyPresent)
	}
}

// TestBuilderResolvePackageByNamePermissions asserts disk images and apps
// are checked under the permission for their own names: a disk image or an
// app the caller may not list reads missing, exactly as an absent one does,
// as GET /disks and GET /applications leave it out, so the answer does not
// disclose that it exists. A drive matched by file name alone says which
// image of this server it is.
func TestBuilderResolvePackageByNamePermissions(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)
	pkg := builderResolvePackage(t, harness)
	elsewhere := "/other/images/" + builderPackageImage
	pkg.Requirements.Images = append(pkg.Requirements.Images,
		bdoc.PackageImage{Name: elsewhere, UsedBy: []string{builderPackageHost}})

	configs := builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"})
	kinds := builderPolicy([]string{"scenarios", "topologies"}, []string{"*"}, []string{"list"})
	stored := "/phenix/images/" + builderPackageImage
	used := "Used by " + builderPackageHost + "."
	absent := "This server has no disk image of this name. " + used
	noApp := "This server has no app of this name."
	matched := "Matched by file name " + builderPackageImage + "; this server's image is " + stored + ". " + used

	hidden := builderRole(configs, kinds,
		builderPolicy([]string{"disks"}, []string{"gone*"}, []string{"list"}),
		builderPolicy([]string{"applications"}, []string{"ntp"}, []string{"list"}),
	)
	allowed := builderRole(configs, kinds,
		builderPolicy([]string{"disks"}, []string{builderPackageImage}, []string{"list"}),
		builderPolicy([]string{"applications"}, []string{"pkg-app"}, []string{"list"}),
	)

	for name, tt := range map[string]struct {
		role *rbac.Role
		want map[string]builderPackageDependency
	}{
		"names not allowed": {role: &hidden, want: map[string]builderPackageDependency{
			"image/" + stored:    {Status: builderDependencyMissing, Detail: absent},
			"image/" + elsewhere: {Status: builderDependencyMissing, Detail: absent},
			"app/pkg-app":        {Status: builderDependencyMissing, Detail: noApp},
			"app/ghost-app":      {Status: builderDependencyMissing, Detail: noApp},
		}},
		"names allowed": {role: &allowed, want: map[string]builderPackageDependency{
			"image/" + stored:    {Status: builderDependencyPresent, Detail: used},
			"image/" + elsewhere: {Status: builderDependencyPresent, Detail: matched},
			"app/pkg-app":        {Status: builderDependencyPresent, Detail: ""},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			status, dependencies := resolveBuilderPackage(t, harness, tt.role, pkg)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want %d", status, http.StatusOK)
			}

			for key, want := range tt.want {
				got := dependencies[key]
				if got.Status != want.Status || got.Detail != want.Detail {
					t.Errorf("%s = %s (%q), want %s (%q)", key, got.Status, got.Detail, want.Status, want.Detail)
				}
			}
		})
	}
}

// TestBuilderResolvePackageRefusesBuilderAnnotations asserts a package whose
// carried config has Builder annotations is refused, naming each, so that
// no config created from it claims a document of this server.
func TestBuilderResolvePackageRefusesBuilderAnnotations(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)
	pkg := builderResolvePackage(t, harness)

	created := pkg.Scenarios["pkg-new"]
	created.Metadata.Annotations = map[string]string{"builder-doc": `{"id":"forged"}`, "builder-xml": "<x/>"}
	pkg.Scenarios["pkg-new"] = created

	body, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("encoding the package: %v", err)
	}

	status, refusal := builderPackageRefusal(t, harness, "/builder/package/resolve", string(body))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", status, http.StatusUnprocessableEntity)
	}

	for _, key := range []string{"builder-doc", "builder-xml"} {
		want := `scenarios.pkg-new.metadata.annotations: "` + key + `" is a Builder annotation`
		if !strings.Contains(refusal.Cause, want) {
			t.Errorf("the refusal = %+v, want it to name %s", refusal, key)
		}
	}

	annotations := 0

	for _, issue := range refusal.Issues {
		if issue.Code == bdoc.CodePackageConfigBuilderNote && issue.Path == "scenarios.pkg-new.metadata.annotations" {
			annotations++
		}
	}

	if refusal.Code != string(bdoc.CodePackageInvalid) || annotations != 2 {
		t.Errorf("the refusal = %+v, want the code %s and an issue of the code %s for each annotation",
			refusal, bdoc.CodePackageInvalid, bdoc.CodePackageConfigBuilderNote)
	}
}

// TestBuilderResolvePackageRefusesTooLongLists asserts the bounds on a
// package's lists hold on the resolve route.
func TestBuilderResolvePackageRefusesTooLongLists(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)
	pkg := builderResolvePackage(t, harness)

	pkg.Requirements.Files = make([]string, 0, bdoc.MaxPackageRequirements+1)

	for i := range bdoc.MaxPackageRequirements + 1 {
		pkg.Requirements.Files = append(pkg.Requirements.Files, "/phenix/f"+strconv.Itoa(i))
	}

	if status, _ := resolveBuilderPackage(t, harness, nil, pkg); status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", status, http.StatusUnprocessableEntity)
	}
}

func TestBuilderResolvePackageRefusesWhatIsNoPackage(t *testing.T) {
	harness := newBuilderResolveHarness(t, nil)
	pkg := builderResolvePackage(t, harness)

	value := map[string]any{}

	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("encoding the package: %v", err)
	}

	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decoding the package: %v", err)
	}

	value["extra"] = true

	unknown, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding the package: %v", err)
	}

	for name, body := range map[string]string{
		"an unknown field":       string(unknown),
		"no requirements":        `{"$schema": "` + bdoc.PackageSchemaURI + `", "document": ` + string(builderDocument(t, "x")) + `}`,
		"a document of a schema": string(builderDocument(t, "x")),
	} {
		status, refusal := builderPackageRefusal(t, harness, "/builder/package/resolve", body)
		if status != http.StatusUnprocessableEntity || refusal.Code != string(bdoc.CodePackageInvalid) {
			t.Errorf("%s: %d %+v, want %d with the code %s",
				name, status, refusal, http.StatusUnprocessableEntity, bdoc.CodePackageInvalid)
		}
	}

	// What does not validate is listed issue by issue, each with its code.
	_, refusal := builderPackageRefusal(t, harness, "/builder/package/resolve",
		`{"$schema": "`+bdoc.PackageSchemaURI+`", "document": `+string(builderDocument(t, "x"))+`}`)
	if !slices.ContainsFunc(refusal.Issues, func(issue bdoc.Issue) bool {
		return issue.Code == bdoc.CodePackageRequirementsMissing && issue.Path == "requirements.scenarios"
	}) {
		t.Errorf("no requirements: issues = %+v, want each missing list with the code %s",
			refusal.Issues, bdoc.CodePackageRequirementsMissing)
	}

	status, refusal := builderPackageRefusal(t, harness, "/builder/package/resolve", `{} {}`)
	if status != http.StatusBadRequest || refusal.Code != string(bdoc.CodeRequestInvalid) {
		t.Errorf("two JSON values: %d %+v, want %d with the code %s",
			status, refusal, http.StatusBadRequest, bdoc.CodeRequestInvalid)
	}
}

// TestBuilderPackageConfigLeavesOutTheBuilders asserts a packaged config
// keeps the config's annotations but the Builder's own, and nothing of its
// times.
func TestBuilderPackageConfigLeavesOutTheBuilders(t *testing.T) {
	config := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    builderKindTopology,
		Metadata: store.ConfigMetadata{
			Name:        "pkg",
			Created:     "2026-10-01T00:00:00Z",
			Updated:     "2026-10-02T00:00:00Z",
			Annotations: store.Annotations{"builder-xml": "<x/>", "owner": "plant"},
		},
		Spec: map[string]any{"nodes": []any{}},
	}

	packaged := builderPackageConfig(&config)

	encoded, err := json.Marshal(packaged)
	if err != nil {
		t.Fatalf("encoding the config: %v", err)
	}

	want := `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"pkg",` +
		`"annotations":{"owner":"plant"}},"spec":{"nodes":[]}}`
	if string(encoded) != want {
		t.Fatalf("the packaged config = %s, want %s", encoded, want)
	}
}
