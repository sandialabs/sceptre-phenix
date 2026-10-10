package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	bapi "phenix/api/builder"
	"phenix/api/disk"
	"phenix/app"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// The sections a package carries when the request to build it includes
// them. What a package needs is listed in its requirements whatever it
// carries.
const (
	builderPackageScenarios  = "scenarios"
	builderPackageTopologies = "topologies"
	builderPackageIcons      = "icons"
	builderPackageImages     = "images"
)

// The kinds of what a package's diagram needs, besides the Scenario and
// Topology configs (builderSourceScenario and builderSourceTopology).
const (
	builderDependencyTemplate = "template"
	builderDependencyIcon     = "icon"
	builderDependencyImage    = "image"
	builderDependencyApp      = "app"
	builderDependencyFile     = "file"
)

// Whether this server has what a package's diagram needs: present, missing,
// different (a config or an icon of that name whose content is not the
// package's), or unknown (it cannot be checked).
const (
	builderDependencyPresent   = "present"
	builderDependencyMissing   = "missing"
	builderDependencyDifferent = "different"
	builderDependencyUnknown   = "unknown"
)

// errBuilderPackageUnreadable is why a config a document names is not in its
// package: it does not exist, or the caller may not read it. The package's
// warnings never tell the two apart.
var errBuilderPackageUnreadable = errors.New("the config does not exist or may not be read")

// builderPackageRequest asks for the package of a document.
type builderPackageRequest struct {
	// Document is the document, with the edits not saved yet.
	Document json.RawMessage `json:"document"`
	// Include names the sections the package carries: scenarios,
	// topologies, icons and images.
	Include []string `json:"include"`
}

// builderPackageResponse is a package, and what it names but does not carry.
type builderPackageResponse struct {
	// Package is the package, ready to be saved as a file.
	Package *bdoc.Package `json:"package"`
	// Warnings say what the package names but does not carry, or leaves out
	// of its requirements, and why, each with its code.
	Warnings []bdoc.Issue `json:"warnings"`
}

// builderPackageResolveResponse is what a package's diagram needs, and
// whether this server has each.
type builderPackageResolveResponse struct {
	// Dependencies are in the order of the package's requirements, by kind:
	// scenarios, topologies, templates, icons, images, apps, then files.
	Dependencies []builderPackageDependency `json:"dependencies"`
}

// builderPackageDependency is one thing a package's diagram needs.
type builderPackageDependency struct {
	// Kind is scenario, topology, template, icon, image, app or file.
	Kind string `json:"kind"`
	// Name names it as the package's requirements do.
	Name string `json:"name"`
	// Status is present, missing, different or unknown.
	Status string `json:"status"`
	// Packaged says whether the package carries it.
	Packaged bool `json:"packaged"`
	// Detail says why the status is what it is, when that needs saying.
	Detail string `json:"detail,omitempty"`
}

// newBuilderDependency returns a dependency whose status is not known yet.
func newBuilderDependency(kind, name string, packaged bool) builderPackageDependency {
	return builderPackageDependency{Kind: kind, Name: name, Status: "", Packaged: packaged, Detail: ""}
}

// with returns the dependency with the given status and detail.
func (d builderPackageDependency) with(status, detail string) builderPackageDependency {
	d.Status = status
	d.Detail = detail

	return d
}

// builderDiskImages lists this server's disk images, as GET /disks lists
// them.
func builderDiskImages() ([]disk.Details, error) {
	return disk.GetImages("")
}

// builderAppNames lists the apps this server runs: the apps a scenario can
// name, as GET /applications lists them, and the default apps every
// experiment runs.
func builderAppNames() []string {
	return slices.Concat(app.List(), app.DefaultApps())
}

// withBuilderDiskImages sets how the package routes list this server's disk
// images.
func withBuilderDiskImages(list func() ([]disk.Details, error)) builderOption {
	return func(api *builderAPI) { api.diskImages = list }
}

// withBuilderApps sets how the package routes list the apps this server
// runs.
func withBuilderApps(list func() []string) builderOption {
	return func(api *builderAPI) { api.appNames = list }
}

// getPackageSchema - GET /schemas/builder/package/v1.
//
// The JSON Schema of a package, under the permission of the document schema:
// schemas get on the resource name builder.
func (b *builderAPI) getPackageSchema(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetPackageSchema")

	actor, ok := builderRequestActor(r)
	if !ok || !actor.role.Allowed("schemas", "get", "builder") {
		return builderForbidden(actor, "getting the builder package schema")
	}

	body, err := bdoc.PackageSchemaJSON()
	if err != nil {
		return weberror.NewWebError(err, "unable to build the builder package schema").
			SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", mimeJSON)

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// buildPackage - POST /builder/package.
//
// The package of a document (see [bdoc.Package]): the document, which comes
// with the request and so holds the edits not saved yet, the sections the
// request includes, and the requirements read from them. Each Scenario
// config the document names, and with the topologies section each topology
// it includes, is read from the config store under the caller's
// permissions, as generating a document reads them: configs get on it and
// the list permission of its kind. One that does not exist, or that the
// caller may not read, is named in the requirements but not carried, and a
// warning, in the same words for both, says so. An include that is a file
// path is never read. With the icons section, the document gets a copy of
// each custom icon it names and does not carry, from the icon library.
// Nothing is written, and no file is read.
//
// The package answered is always one the resolve route takes. The document
// and the stored configs may name things a package cannot list: an
// injection source with a newline, a name longer than
// [bdoc.MaxRequirementBytes], or more files than
// [bdoc.MaxPackageRequirements]. Each such entry is left out of the
// requirements, and a warning names it ([bdoc.Package.TrimRequirements]),
// so the Download dialog shows it before the file is saved; nothing is left
// out silently, and the user can still move the diagram. The package is
// then checked as the resolve route checks one, and anything else it would
// refuse, such as a stored config without a spec, is answered 422 with the
// code package.invalid and the issues, which name each. A package larger
// than [bdoc.MaxPackageBytes] is answered 413 with the code
// package.too-large. Each warning has its code (see [bdoc.Code]).
func (b *builderAPI) buildPackage(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBuildPackage")

	actor, err := builderAuthorize(r, builderVerbGet, "packaging a builder document")
	if err != nil {
		return err
	}

	var request builderPackageRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	sections, err := builderPackageSections(request.Include)
	if err != nil {
		return err
	}

	data, err := builderDocumentBytes(request.Document)
	if err != nil {
		return err
	}

	document, err := bapi.ParseDocument(data)
	if err != nil {
		return builderWebError(err, "unable to package the builder document")
	}

	contents, warnings, err := b.packageContents(actor, document, sections)
	if err != nil {
		return err
	}

	if sections[builderPackageIcons] {
		missing, err := b.packageIcons(r.Context(), document)
		if err != nil {
			return err
		}

		warnings = append(warnings, missing...)
	}

	pkg := bdoc.NewPackage(document, contents)
	warnings = append(warnings, pkg.TrimRequirements()...)

	if err := pkg.Validate(); err != nil {
		return weberror.NewWebError(err, "unable to package the builder document: the package would not load").
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePackageInvalid))
	}

	encoded, err := json.Marshal(pkg)
	if err != nil {
		return weberror.NewWebError(err, "unable to encode the builder package").
			SetStatus(http.StatusInternalServerError)
	}

	if len(encoded) > bdoc.MaxPackageBytes {
		return weberror.NewWebError(
			nil, "the package is larger than %d bytes; leave out some of its sections", bdoc.MaxPackageBytes,
		).SetStatus(http.StatusRequestEntityTooLarge).WithCode(string(bdoc.CodePackageTooLarge))
	}

	return builderWriteJSON(w, http.StatusOK, "", builderPackageResponse{Package: pkg, Warnings: warnings})
}

// builderPackageSections returns the sections a request includes. A
// section the package format does not have, and one named twice, are
// refused with 400.
func builderPackageSections(include []string) (map[string]bool, error) {
	known := []string{builderPackageScenarios, builderPackageTopologies, builderPackageIcons, builderPackageImages}
	sections := map[string]bool{}

	for _, section := range include {
		switch {
		case !slices.Contains(known, section):
			return nil, weberror.NewWebError(nil, "include names %q, which is not a section of a package", section).
				SetStatus(http.StatusBadRequest)
		case sections[section]:
			return nil, weberror.NewWebError(nil, "include names %s twice", section).
				SetStatus(http.StatusBadRequest)
		}

		sections[section] = true
	}

	return sections, nil
}

// packageContents reads the configs a document's package needs: each
// Scenario config the document names, whose apps the requirements list and
// which the package carries with the scenarios section, and, with the
// topologies section, each topology the document includes. It returns a
// warning for each it cannot read.
func (b *builderAPI) packageContents(
	actor builderActor,
	document *bdoc.Document,
	sections map[string]bool,
) (bdoc.PackageContents, []bdoc.Issue, error) {
	contents := bdoc.PackageContents{
		Scenarios:      map[string]bdoc.PackageConfig{},
		CarryScenarios: sections[builderPackageScenarios],
		Topologies:     map[string]bdoc.PackageConfig{},
		Images:         sections[builderPackageImages],
	}
	warnings := []bdoc.Issue{}

	for _, name := range document.Scenarios {
		config, err := b.packagedConfig(actor, builderKindScenario, builderScenarios, name)

		switch {
		case errors.Is(err, errBuilderPackageUnreadable) && contents.CarryScenarios:
			warnings = append(warnings, builderPackageWarning(
				bdoc.CodePackageConfigUnreadable, builderUnreadableWarning("Scenario config", name),
			))
		case errors.Is(err, errBuilderPackageUnreadable):
			warnings = append(warnings, builderPackageWarning(bdoc.CodePackageConfigUnreadable, fmt.Sprintf(
				"Scenario config %s does not exist on this server, or your role cannot read it: "+
					"the package lists none of its apps.",
				name,
			)))
		case err != nil:
			return contents, nil, err
		default:
			contents.Scenarios[name] = *config
		}
	}

	if !sections[builderPackageTopologies] || document.Source == nil {
		return contents, warnings, nil
	}

	for _, name := range document.Source.IncludeTopologies {
		if strings.ContainsAny(name, `/\`) {
			warnings = append(warnings, builderPackageWarning(bdoc.CodePackageIncludeFilePath, fmt.Sprintf(
				"Included topology %s is a file path: the package names it but does not carry it.", name,
			)))

			continue
		}

		config, err := b.packagedConfig(actor, builderKindTopology, builderTopologies, name)

		switch {
		case errors.Is(err, errBuilderPackageUnreadable):
			warnings = append(warnings, builderPackageWarning(
				bdoc.CodePackageConfigUnreadable, builderUnreadableWarning("Included topology", name),
			))
		case err != nil:
			return contents, nil, err
		default:
			contents.Topologies[name] = *config
		}
	}

	return contents, warnings, nil
}

// builderPackageWarning is a warning of POST /builder/package: the issue of
// code saying message, about the package as a whole, so it has no path.
func builderPackageWarning(code bdoc.Code, message string) bdoc.Issue {
	return bdoc.NewIssue(code, "", message)
}

// builderUnreadableWarning says that a package names a config it does not
// carry, because the config does not exist or the caller may not read it.
func builderUnreadableWarning(what, name string) string {
	return fmt.Sprintf(
		"%s %s does not exist on this server, or your role cannot read it: the package names it but does not carry it.",
		what, name,
	)
}

// packagedConfig reads a config a package names, under the caller's
// permissions: configs get on it, and the list permission of its kind. A
// config that does not exist, and one the caller may not read, are both
// errBuilderPackageUnreadable.
func (b *builderAPI) packagedConfig(actor builderActor, kind, resource, name string) (*bdoc.PackageConfig, error) {
	full := store.ConfigFullName(kind, name)
	if full == "" || !bdoc.IsConfigName(name) ||
		!builderBaseAllowed(actor.role, builderVerbGet, full) || !builderKindAllowed(actor.role, resource, name) {
		return nil, errBuilderPackageUnreadable
	}

	config, exists, err := b.configIfExists(kind, name)

	switch {
	case err != nil:
		return nil, err
	case !exists:
		return nil, errBuilderPackageUnreadable
	}

	packaged := builderPackageConfig(config)

	return &packaged, nil
}

// builderPackageConfig returns a stored config as a package carries it:
// without its times and the Builder's annotations, which name records of
// this server.
func builderPackageConfig(config *store.Config) bdoc.PackageConfig {
	annotations := map[string]string{}

	for key, value := range config.Metadata.Annotations {
		if !bdoc.IsBuilderAnnotation(key) {
			annotations[key] = value
		}
	}

	if len(annotations) == 0 {
		annotations = nil
	}

	return bdoc.PackageConfig{
		APIVersion: config.Version,
		Kind:       config.Kind,
		Metadata:   bdoc.PackageConfigMetadata{Name: config.Metadata.Name, Annotations: annotations},
		Spec:       config.Spec,
	}
}

// packageIcons gives the document a copy of each custom icon it names and
// does not carry, from the server's icon library, at most as many as a
// document carries. It returns a warning for each icon it gives no copy of.
func (b *builderAPI) packageIcons(ctx context.Context, document *bdoc.Document) ([]bdoc.Issue, error) {
	warnings := []bdoc.Issue{}
	icons := map[string]bdoc.Icon{}

	maps.Copy(icons, document.Icons)

	for _, name := range document.IconNames() {
		if _, carried := icons[name]; carried {
			continue
		}

		if len(icons) >= bdoc.MaxDocumentIcons {
			warnings = append(warnings, builderPackageWarning(bdoc.CodePackageIconsTooMany, fmt.Sprintf(
				"A package carries at most %d custom icons, so it names custom icon %s but does not carry it.",
				bdoc.MaxDocumentIcons, name,
			)))

			continue
		}

		icon, err := b.drafts.GetIcon(ctx, name)

		switch {
		case errors.Is(err, bapi.ErrNotFound):
			warnings = append(warnings, builderPackageWarning(bdoc.CodePackageIconMissing, fmt.Sprintf(
				"Custom icon %s is not in the server's icon library: the package names it but does not carry it.", name,
			)))
		case err != nil:
			return nil, builderWebError(err, "unable to read custom icon %s", name)
		default:
			icons[name] = bdoc.Icon{Data: icon.Data}
		}
	}

	if len(icons) > 0 {
		document.Icons = icons
	}

	return warnings, nil
}

// resolvePackage - POST /builder/package/resolve.
//
// What a package's diagram needs, and whether this server has each, for the
// caller to choose what to create before the diagram is opened. The package
// is the request's body, at most [bdoc.MaxPackageBytes] (413 beyond, before
// anything is decoded), decoded strictly and checked as [bdoc.Package]
// checks one. A Scenario or Topology config is present when the server has
// one of that name with the package's spec (or the package carries none),
// different when its spec is another, and missing when there is none; one
// the caller may not read (configs get on it, and the list permission of
// its kind) is unknown, so its existence is not disclosed. An icon is
// compared by its bytes the same way. Disk images are looked for among
// those GET /disks lists the caller (disks list, then by name), so one the
// caller may not see reads missing, as an absent one does; without disks
// list, or when the disk images cannot be listed, they are unknown. Apps
// are looked for the same way among those GET /applications lists the
// caller (applications list, then by name), and are unknown only without
// applications list. Templates travel in the document and
// are present. Files are never checked. Nothing is written. A body larger
// than the bound is answered 413 with the code package.too-large, and a
// package that does not decode or validate 422 with the code
// package.invalid and, for one that does not validate, its issues.
func (b *builderAPI) resolvePackage(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderResolvePackage")

	actor, err := builderAuthorize(r, builderVerbGet, "checking a builder package")
	if err != nil {
		return err
	}

	var body json.RawMessage

	if err := builderDecodeLimit(w, r, &body, bdoc.MaxPackageBytes); err != nil {
		var refusal *weberror.WebError

		if errors.As(err, &refusal) && refusal.Status == http.StatusRequestEntityTooLarge {
			refusal.Code = string(bdoc.CodePackageTooLarge)
		}

		return err
	}

	pkg, err := bdoc.DecodePackage(body)
	if err == nil {
		err = pkg.Validate()
	}

	if err != nil {
		return weberror.NewWebError(err, "unable to read the builder package").
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePackageInvalid))
	}

	dependencies, err := b.packageDependencies(r.Context(), actor, pkg)
	if err != nil {
		return err
	}

	return builderWriteJSON(w, http.StatusOK, "", builderPackageResolveResponse{Dependencies: dependencies})
}

// packageDependencies returns what a package's diagram needs, with whether
// this server has each, in the order of [builderPackageResolveResponse].
func (b *builderAPI) packageDependencies(
	ctx context.Context,
	actor builderActor,
	pkg *bdoc.Package,
) ([]builderPackageDependency, error) {
	requirements := pkg.Requirements
	dependencies := make([]builderPackageDependency, 0)

	for _, name := range requirements.Scenarios {
		dependency, err := b.configDependency(actor, builderSourceScenario, name, carriedConfig(pkg.Scenarios, name))
		if err != nil {
			return nil, err
		}

		dependencies = append(dependencies, dependency)
	}

	for _, name := range requirements.Topologies {
		dependency, err := b.configDependency(actor, builderSourceTopology, name, carriedConfig(pkg.Topologies, name))
		if err != nil {
			return nil, err
		}

		dependencies = append(dependencies, dependency)
	}

	for _, name := range requirements.Templates {
		template := newBuilderDependency(builderDependencyTemplate, name, true)
		dependencies = append(dependencies, template.with(builderDependencyPresent, "The diagram carries it."))
	}

	icons, err := b.iconDependencies(ctx, actor, pkg)
	if err != nil {
		return nil, err
	}

	dependencies = append(dependencies, icons...)
	dependencies = append(dependencies, b.imageDependencies(actor, requirements.Images)...)
	dependencies = append(dependencies, b.appDependencies(actor, requirements.Apps)...)

	for _, path := range requirements.Files {
		file := newBuilderDependency(builderDependencyFile, path, false)
		dependencies = append(dependencies, file.with(builderDependencyUnknown, "Files on the phenix server are not checked."))
	}

	return dependencies, nil
}

// carriedConfig returns the config of a package's list that has the given
// name, or nil.
func carriedConfig(configs map[string]bdoc.PackageConfig, name string) *bdoc.PackageConfig {
	config, ok := configs[name]
	if !ok {
		return nil
	}

	return &config
}

// configDependency reports whether this server has a Scenario or Topology
// config a package's diagram needs (kind is builderSourceScenario or
// builderSourceTopology), and the package's copy of it, if it carries one.
func (b *builderAPI) configDependency(
	actor builderActor,
	kind, name string,
	carried *bdoc.PackageConfig,
) (builderPackageDependency, error) {
	configKind, resource := builderKindScenario, builderScenarios
	if kind == builderSourceTopology {
		configKind, resource = builderKindTopology, builderTopologies
	}

	dependency := newBuilderDependency(kind, name, carried != nil)
	full := store.ConfigFullName(configKind, name)

	switch {
	case full == "" || !bdoc.IsConfigName(name):
		return dependency.with(builderDependencyUnknown, "It is not the name of a config, so it is not checked."), nil
	case !builderBaseAllowed(actor.role, builderVerbGet, full) || !builderKindAllowed(actor.role, resource, name):
		return dependency.with(
			builderDependencyUnknown, fmt.Sprintf("Your role cannot read the %s config of this name.", configKind),
		), nil
	}

	stored, exists, err := b.configIfExists(configKind, name)

	switch {
	case err != nil:
		return dependency, err
	case !exists && carried != nil:
		return dependency.with(builderDependencyMissing, "The package carries it, so it can be created on this server."), nil
	case !exists:
		return dependency.with(builderDependencyMissing, "The package does not carry it."), nil
	case carried == nil || sameBuilderSpec(stored.Spec, carried.Spec):
		return dependency.with(builderDependencyPresent, ""), nil
	}

	return dependency.with(builderDependencyDifferent, fmt.Sprintf(
		"This server's %s config of this name has another spec, which is left as it is.", configKind,
	)), nil
}

// sameBuilderSpec reports whether two config specs hold the same content.
func sameBuilderSpec(left, right map[string]any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)

	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

// iconDependencies reports whether the server's icon library has each custom
// icon a package's diagram names, with the bytes of the document's copy, if
// it carries one.
func (b *builderAPI) iconDependencies(
	ctx context.Context,
	actor builderActor,
	pkg *bdoc.Package,
) ([]builderPackageDependency, error) {
	dependencies := make([]builderPackageDependency, 0, len(pkg.Requirements.Icons))
	readable := builderBaseAllowed(actor.role, builderVerbList)

	for _, name := range pkg.Requirements.Icons {
		carried, packaged := pkg.Document.Icons[name]
		dependency := newBuilderDependency(builderDependencyIcon, name, packaged)

		if !readable {
			dependencies = append(dependencies, dependency.with(
				builderDependencyUnknown, "Your role cannot read the icon library.",
			))

			continue
		}

		icon, err := b.drafts.GetIcon(ctx, name)

		switch {
		case errors.Is(err, bapi.ErrNotFound) && packaged:
			dependency = dependency.with(
				builderDependencyMissing, "The diagram carries a copy, which the upload adds to the icon library.",
			)
		case errors.Is(err, bapi.ErrNotFound):
			dependency = dependency.with(builderDependencyMissing, "The nodes that name it show their built-in icon.")
		case err != nil:
			return nil, builderWebError(err, "unable to read custom icon %s", name)
		case !packaged || icon.Data == carried.Data:
			dependency = dependency.with(builderDependencyPresent, "")
		default:
			dependency = dependency.with(
				builderDependencyDifferent, "The icon library's icon of this name differs; the diagram keeps its own copy.",
			)
		}

		dependencies = append(dependencies, dependency)
	}

	return dependencies, nil
}

// imageDependencies reports whether this server has each disk image a
// package's diagram needs, among the images GET /disks lists the caller:
// with disks list, those whose own name the role allows. One the caller
// may not see reads missing, as one this server lacks does, so its
// existence is not disclosed.
func (b *builderAPI) imageDependencies(actor builderActor, images []bdoc.PackageImage) []builderPackageDependency {
	dependencies := make([]builderPackageDependency, 0, len(images))
	if len(images) == 0 {
		return dependencies
	}

	listed, problem := b.listedDiskImages(actor)

	for _, image := range images {
		dependency := newBuilderDependency(builderDependencyImage, image.Name, false)
		used := ""

		if len(image.UsedBy) > 0 {
			used = "Used by " + strings.Join(image.UsedBy, ", ") + "."
		}

		found, ok := builderFindDisk(listed, image.Name)

		switch {
		case problem != "":
			dependency = dependency.with(builderDependencyUnknown, strings.TrimSpace(problem+" "+used))
		case !ok:
			dependency = dependency.with(
				builderDependencyMissing, strings.TrimSpace("This server has no disk image of this name. "+used),
			)
		default:
			dependency = dependency.with(
				builderDependencyPresent, strings.TrimSpace(builderDiskMatch(image.Name, found)+" "+used),
			)
		}

		dependencies = append(dependencies, dependency)
	}

	return dependencies
}

// listedDiskImages returns the disk images of this server GET /disks lists
// the caller: with disks list, each whose name the role allows; or why
// they cannot be listed for the caller.
func (b *builderAPI) listedDiskImages(actor builderActor) ([]disk.Details, string) {
	if !actor.role.Allowed("disks", "list") {
		return nil, "Your role cannot list disk images."
	}

	images, err := b.diskImages()
	if err != nil {
		plog.Warn(plog.TypeSystem, "listing disk images for a builder package", "err", err)

		return nil, "The server's disk images could not be listed."
	}

	allowed := make([]disk.Details, 0, len(images))

	for _, image := range images {
		if actor.role.Allowed("disks", "list", image.Name) {
			allowed = append(allowed, image)
		}
	}

	return allowed, ""
}

// builderFindDisk returns the disk image a drive names: by its name or its
// full path, or else by its file name, as the Builder's diagram checks find
// a drive's image.
func builderFindDisk(images []disk.Details, name string) (disk.Details, bool) {
	for _, image := range images {
		if image.Name == name || image.FullPath == name {
			return image, true
		}
	}

	for _, image := range images {
		if image.Name == filepath.Base(name) {
			return image, true
		}
	}

	return disk.Details{}, false
}

// builderDiskMatch says so when a drive's image was found by its file name
// alone, and which image of this server it is: the drive names a path that
// is not that image's. It is "" for an image found by its name or its full
// path.
func builderDiskMatch(name string, found disk.Details) string {
	if name == found.Name || name == found.FullPath {
		return ""
	}

	if found.FullPath == "" {
		return fmt.Sprintf("Matched by file name %s.", found.Name)
	}

	return fmt.Sprintf("Matched by file name %s; this server's image is %s.", found.Name, found.FullPath)
}

// appDependencies reports whether this server runs each app a package's
// diagram needs, among the apps GET /applications lists the caller: with
// applications list, those whose own name the role allows. One the caller
// may not see reads missing, as one this server lacks does, so its
// existence is not disclosed. Without applications list at all, apps are
// unknown.
func (b *builderAPI) appDependencies(actor builderActor, names []string) []builderPackageDependency {
	dependencies := make([]builderPackageDependency, 0, len(names))
	listable := actor.role.Allowed("applications", "list")

	var listed []string

	if listable && len(names) > 0 {
		listed = b.listedAppNames(actor)
	}

	for _, name := range names {
		dependency := newBuilderDependency(builderDependencyApp, name, false)

		switch {
		case !listable:
			dependency = dependency.with(builderDependencyUnknown, "Your role cannot list apps.")
		case !slices.Contains(listed, name):
			dependency = dependency.with(builderDependencyMissing, "This server has no app of this name.")
		default:
			dependency = dependency.with(builderDependencyPresent, "")
		}

		dependencies = append(dependencies, dependency)
	}

	return dependencies
}

// listedAppNames returns the apps of this server GET /applications lists
// the caller: each whose name the role allows applications list on.
func (b *builderAPI) listedAppNames(actor builderActor) []string {
	names := b.appNames()
	allowed := make([]string, 0, len(names))

	for _, name := range names {
		if actor.role.Allowed("applications", "list", name) {
			allowed = append(allowed, name)
		}
	}

	return allowed
}
