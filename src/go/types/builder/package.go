package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"phenix/util"
)

// PackageSchemaURI identifies the format of a Builder package: one file that
// holds a Builder document, the Scenario and Topology configs and the custom
// icons it names that the user chose to put in, and the list of everything
// the diagram needs on a phenix server.
const PackageSchemaURI = "https://phenix.sandia.gov/schemas/builder/package/v1"

// Bounds on a package.
const (
	// MaxPackageBytes bounds the text of a package. It is the bound on the
	// text of a Builder document, so a package goes wherever its document
	// goes.
	MaxPackageBytes = 5 << 20

	// MaxPackageTopologies is the most included Topology configs a package
	// carries.
	MaxPackageTopologies = 100

	// MaxPackageRequirements is the most entries one list of a package's
	// requirements holds.
	MaxPackageRequirements = 1000

	// MaxRequirementBytes bounds one entry of those lists: a name, a path or
	// a hostname.
	MaxRequirementBytes = 4096
)

// The kinds of the configs a package carries.
const (
	PackageKindScenario = "Scenario"
	PackageKindTopology = "Topology"
)

// The keys of a package and of its parts.
const (
	keyDocument     = "document"
	keyTopologies   = "topologies"
	keyRequirements = "requirements"
	keyImages       = "images"
	keyApps         = "apps"
	keyFiles        = "files"
	keyUsedBy       = "usedBy"
	keyAnnotations  = "annotations"
)

// The keys of a phenix config spec a package's requirements are read from.
const (
	specKeyHardware   = "hardware"
	specKeyDrives     = "drives"
	specKeyImage      = "image"
	specKeyInjections = "injections"
	specKeySource     = "src"
	specKeyAssetDir   = "assetDir"
	specKeyExperiment = "experiment"
	specKeyHost       = "host"
)

// ErrInvalidPackage is what a [PackageError] unwraps to, and what
// [ParsePackage] and [DecodePackage] wrap every refusal of a package in.
var ErrInvalidPackage = errors.New("invalid Builder package")

// packageAPIVersion matches the apiVersion of a phenix config.
var packageAPIVersion = regexp.MustCompile(`^phenix\.sandia\.gov/v[0-9]+$`)

// Package is a Builder document as one file holds it to move to another
// phenix server, as JSON or YAML: the document, the Scenario and Topology
// configs it names that the user chose to put in, and the list of what the
// diagram needs. It never holds the content of a file, a script or an app.
// Its requirements only name them.
type Package struct {
	// Schema is [PackageSchemaURI].
	Schema string `json:"$schema"`
	// Document is the diagram, with copies of the custom icons it carries.
	Document *Document `json:"document"`
	// Scenarios are the Scenario configs the document names, by name, that
	// the package carries.
	Scenarios map[string]PackageConfig `json:"scenarios,omitempty"`
	// Topologies are the Topology configs the document includes, by name,
	// that the package carries.
	Topologies map[string]PackageConfig `json:"topologies,omitempty"`
	// Requirements list what the diagram needs on a server, whether the
	// package carries it or not.
	Requirements PackageRequirements `json:"requirements"`
}

// PackageConfig is a phenix config a package carries: what it is and its
// spec, without the times and the Builder annotations a server stores with
// it. It is data only. A server that creates it applies its own config
// checks.
type PackageConfig struct {
	// APIVersion is the config's apiVersion, such as phenix.sandia.gov/v1.
	APIVersion string `json:"apiVersion"`
	// Kind is [PackageKindScenario] or [PackageKindTopology].
	Kind string `json:"kind"`
	// Metadata names the config.
	Metadata PackageConfigMetadata `json:"metadata"`
	// Spec is the config's spec as the server stores it.
	Spec map[string]any `json:"spec"`
}

// PackageConfigMetadata is the metadata a package keeps of a config.
type PackageConfigMetadata struct {
	// Name is the config's name, which is also its key in the package.
	Name string `json:"name"`
	// Annotations are the config's annotations, other than the Builder's own
	// (see [IsBuilderAnnotation]), which [Package.Validate] refuses.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// PackageRequirements is what a diagram needs on a phenix server. Every list
// is present, empty when there is nothing in it.
type PackageRequirements struct {
	// Scenarios names the Scenario configs the document names.
	Scenarios []string `json:"scenarios"`
	// Topologies names the topologies the document includes.
	Topologies []string `json:"topologies"`
	// Templates names the device templates the document carries.
	Templates []string `json:"templates"`
	// Icons names the custom icons the document's nodes and templates name.
	Icons []string `json:"icons"`
	// Images are the disk images the devices boot from, when the package
	// lists them.
	Images []PackageImage `json:"images"`
	// Apps names the apps of the Scenario configs the document names that
	// could be read.
	Apps []string `json:"apps"`
	// Files are the paths on the server the packaged configs and the
	// document's devices name: injection sources and app asset directories.
	Files []string `json:"files"`
}

// PackageImage is one disk image a package's diagram needs.
type PackageImage struct {
	// Name is the image as the devices' drives name it.
	Name string `json:"name"`
	// UsedBy are the hostnames of the devices that use it.
	UsedBy []string `json:"usedBy"`
}

// PackageContents is the content, other than the document, from which
// [NewPackage] makes a package.
type PackageContents struct {
	// Scenarios are the Scenario configs the document names that could be
	// read, by name. Their apps are listed in the requirements.
	Scenarios map[string]PackageConfig
	// CarryScenarios puts those Scenario configs in the package.
	CarryScenarios bool
	// Topologies are the included Topology configs the package carries, by
	// name.
	Topologies map[string]PackageConfig
	// Images lists the disk images the devices use, with the hostnames of
	// the devices that use each.
	Images bool
}

// PackageError lists everything that makes a package unusable, each issue
// with its code: a package-* code, or the code of the rule of a document
// that its document breaks, at a path below "document". It unwraps to
// [ErrInvalidPackage].
type PackageError struct {
	Issues []Issue
}

func (e *PackageError) Error() string {
	messages := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		messages[i] = issue.String()
	}

	return fmt.Sprintf("%s: %s", ErrInvalidPackage.Error(), strings.Join(messages, "; "))
}

// Unwrap allows [errors.Is](err, ErrInvalidPackage).
func (e *PackageError) Unwrap() error {
	return ErrInvalidPackage
}

// NewPackage returns the package of a document: the document as it is, the
// configs of contents it carries, and the requirements read from all of them.
func NewPackage(document *Document, contents PackageContents) *Package {
	var scenarios, topologies map[string]PackageConfig

	if contents.CarryScenarios && len(contents.Scenarios) > 0 {
		scenarios = maps.Clone(contents.Scenarios)
	}

	if len(contents.Topologies) > 0 {
		topologies = maps.Clone(contents.Topologies)
	}

	includes := []string{}
	if document.Source != nil {
		includes = requirementList(document.Source.IncludeTopologies)
	}

	templates := make([]string, 0, len(document.Templates))
	for i := range document.Templates {
		templates = append(templates, document.Templates[i].Name)
	}

	images := []PackageImage{}
	if contents.Images {
		images = packageImages(document, topologies)
	}

	requirements := PackageRequirements{
		Scenarios:  requirementList(document.Scenarios),
		Topologies: includes,
		Templates:  templates,
		Icons:      document.IconNames(),
		Images:     images,
		Apps:       packageApps(contents.Scenarios),
		Files:      packageFiles(document, scenarios, topologies),
	}

	return &Package{
		Schema:       PackageSchemaURI,
		Document:     document,
		Scenarios:    scenarios,
		Topologies:   topologies,
		Requirements: requirements,
	}
}

// ParsePackage decodes and validates the text of a package: JSON, or YAML
// read as [JSONFromYAML] reads a Builder document. The content decides,
// never the name of the file. Text that is empty or longer than
// [MaxPackageBytes], that does not decode strictly, or that
// [Package.Validate] refuses is an error matching [ErrInvalidPackage].
func ParsePackage(text []byte) (*Package, error) {
	switch {
	case len(bytes.TrimSpace(text)) == 0:
		return nil, fmt.Errorf("%w: the file is empty", ErrInvalidPackage)
	case len(text) > MaxPackageBytes:
		return nil, fmt.Errorf("%w: the file is %d bytes; the limit is %d", ErrInvalidPackage, len(text), MaxPackageBytes)
	}

	data, err := JSONFromText(text)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPackage, err)
	}

	pkg, err := DecodePackage(data)
	if err != nil {
		return nil, err
	}

	if err := pkg.Validate(); err != nil {
		return nil, err
	}

	return pkg, nil
}

// packageText is a package as its JSON holds it, before its document is
// decoded.
type packageText struct {
	Schema       string                   `json:"$schema"`
	Document     json.RawMessage          `json:"document"`
	Scenarios    map[string]PackageConfig `json:"scenarios"`
	Topologies   map[string]PackageConfig `json:"topologies"`
	Requirements PackageRequirements      `json:"requirements"`
}

// DecodePackage strictly decodes a package from JSON. It refuses unknown
// fields and trailing content, in the package and in its document. It
// decodes the document as [Decode] does. A document that [Decode] refuses
// gives a *[PackageError]: the issues of its *[ValidationError] at their
// paths below "document", or one issue with the decoder's error. It does not
// validate the package: see [ParsePackage] and [Package.Validate].
func DecodePackage(data []byte) (*Package, error) {
	var text packageText

	if err := util.DecodeJSONStrict(bytes.NewReader(data), &text); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPackage, err)
	}

	pkg := &Package{
		Schema:       text.Schema,
		Document:     nil,
		Scenarios:    text.Scenarios,
		Topologies:   text.Topologies,
		Requirements: text.Requirements,
	}

	if raw := bytes.TrimSpace(text.Document); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		document, err := Decode(raw)
		if err != nil {
			return nil, &PackageError{Issues: packageDocumentIssues(err)}
		}

		pkg.Document = document
	}

	return pkg, nil
}

// packageDocumentIssues returns the issues of the error a package's document
// was refused with, by [Decode] or [Document.Validate]: those of its
// *[ValidationError], each at its path below "document", or else one issue
// of [CodePackageDocumentInvalid] with the error's text.
func packageDocumentIssues(err error) []Issue {
	var invalid *ValidationError

	if !errors.As(err, &invalid) {
		return []Issue{NewIssue(CodePackageDocumentInvalid, keyDocument, err.Error())}
	}

	issues := make([]Issue, 0, len(invalid.Issues))

	for _, issue := range invalid.Issues {
		if issue.Path == "" {
			issue.Path = keyDocument
		} else {
			issue.Path = keyDocument + "." + issue.Path
		}

		issues = append(issues, issue)
	}

	return issues
}

// Validate checks a decoded package, and returns nil or a *[PackageError]
// listing every issue:
//
//   - a "$schema" other than [PackageSchemaURI],
//   - no document, or one [Document.Validate] refuses,
//   - more than [MaxScenarios] Scenario configs or [MaxPackageTopologies]
//     Topology configs,
//   - a config whose key is not a config name, whose kind is not the one of
//     its list, whose metadata.name is not its key, whose apiVersion is not
//     phenix.sandia.gov/v<number>, that has no spec, or that the
//     requirements do not name,
//   - a Builder annotation on a config (see [IsBuilderAnnotation]), one
//     issue for each. Such an annotation names a record of the server that
//     wrote it. This rule makes sure that a config created from a package
//     never claims a Builder document or diagram,
//   - a list of the requirements that is missing or holds more than
//     [MaxPackageRequirements] entries, and an entry that is blank, longer
//     than [MaxRequirementBytes] or holds control characters.
func (p *Package) Validate() error {
	if issues := p.Issues(); len(issues) > 0 {
		return &PackageError{Issues: issues}
	}

	return nil
}

// Issues returns what [Package.Validate] finds, each with its code: one of
// the package-* codes, or, for its document, the code of the rule of a
// document it breaks.
func (p *Package) Issues() []Issue {
	var issues []Issue

	if p.Schema != PackageSchemaURI {
		issues = append(issues, NewIssue(CodePackageSchemaMismatch, schemaKey,
			fmt.Sprintf("package schema must be %q, not %q", PackageSchemaURI, truncate(p.Schema))))
	}

	if p.Document == nil {
		issues = append(issues, NewIssue(CodePackageDocumentMissing, keyDocument, "a package holds a Builder document"))
	} else if err := p.Document.Validate(); err != nil {
		issues = append(issues, packageDocumentIssues(err)...)
	}

	issues = append(issues, packageConfigIssues(
		keyScenarios, PackageKindScenario, p.Scenarios, MaxScenarios, p.Requirements.Scenarios,
	)...)
	issues = append(issues, packageConfigIssues(
		keyTopologies, PackageKindTopology, p.Topologies, MaxPackageTopologies, p.Requirements.Topologies,
	)...)

	return append(issues, p.Requirements.issues()...)
}

// packageConfigIssues returns what is wrong with one list of the configs a
// package carries.
func packageConfigIssues(key, kind string, configs map[string]PackageConfig, limit int, listed []string) []Issue {
	var issues []Issue

	addf := func(code Code, at, format string, args ...any) {
		issues = append(issues, NewIssue(code, at, fmt.Sprintf(format, args...)))
	}

	if len(configs) > limit {
		addf(CodePackageConfigsTooMany, key, "a package carries at most %d %s configs, not %d", limit, kind, len(configs))
	}

	for _, name := range slices.Sorted(maps.Keys(configs)) {
		config := configs[name]
		path := key + "." + name

		if !IsConfigName(name) {
			addf(CodePackageConfigKeyInvalid, path, "%q is not a config name", truncate(name))
		}

		if config.Kind != kind {
			addf(CodePackageConfigKindMismatch, path+"."+keyKind, "must be %q, not %q", kind, truncate(config.Kind))
		}

		if config.Metadata.Name != name {
			addf(CodePackageConfigNameMismatch, path+"."+keyMetadata+"."+keyName,
				"must be %q, the name the package keeps it under, not %q", truncate(name), truncate(config.Metadata.Name))
		}

		if !packageAPIVersion.MatchString(config.APIVersion) {
			addf(CodePackageConfigVersionInvalid, path+"."+keyAPIVersion,
				"must be phenix.sandia.gov/v<number>, not %q", truncate(config.APIVersion))
		}

		if config.Spec == nil {
			addf(CodePackageConfigSpecMissing, path+"."+keySpec, "a config needs a spec")
		}

		if !slices.Contains(listed, name) {
			addf(CodePackageConfigUnlisted, path, "the package carries it, but its requirements do not name it")
		}

		for _, annotation := range slices.Sorted(maps.Keys(config.Metadata.Annotations)) {
			if IsBuilderAnnotation(annotation) {
				addf(CodePackageConfigBuilderNote, path+"."+keyMetadata+"."+keyAnnotations,
					"%q is a Builder annotation, which a package never carries", truncate(annotation))
			}
		}
	}

	return issues
}

// issues returns what is wrong with the lists of the requirements.
func (r *PackageRequirements) issues() []Issue {
	var issues []Issue

	lists := []struct {
		key    string
		values []string
	}{
		{key: keyScenarios, values: r.Scenarios},
		{key: keyTopologies, values: r.Topologies},
		{key: keyTemplates, values: r.Templates},
		{key: keyIcons, values: r.Icons},
		{key: keyApps, values: r.Apps},
		{key: keyFiles, values: r.Files},
	}

	for _, list := range lists {
		issues = append(issues, requirementListIssues(keyRequirements+"."+list.key, list.values)...)
	}

	path := keyRequirements + "." + keyImages

	switch {
	case r.Images == nil:
		issues = append(issues, requirementListMissing(path))
	case len(r.Images) > MaxPackageRequirements:
		issues = append(issues, requirementListTooLong(path, len(r.Images)))
	}

	for i, image := range r.Images {
		at := fmt.Sprintf("%s[%d]", path, i)

		if code, problem := requirementProblem(image.Name); problem != "" {
			issues = append(issues, NewIssue(code, at+"."+keyName, problem))
		}

		issues = append(issues, requirementListIssues(at+"."+keyUsedBy, image.UsedBy)...)
	}

	return issues
}

// requirementListIssues returns what is wrong with one list of names or
// paths of the requirements.
func requirementListIssues(path string, values []string) []Issue {
	var issues []Issue

	switch {
	case values == nil:
		return []Issue{requirementListMissing(path)}
	case len(values) > MaxPackageRequirements:
		issues = append(issues, requirementListTooLong(path, len(values)))
	}

	for i, value := range values {
		if code, problem := requirementProblem(value); problem != "" {
			issues = append(issues, NewIssue(code, fmt.Sprintf("%s[%d]", path, i), problem))
		}
	}

	return issues
}

// requirementListMissing is the issue of a list of the requirements that is
// missing or null, at path.
func requirementListMissing(path string) Issue {
	return NewIssue(CodePackageRequirementsMissing, path, "the list is required, empty when there is nothing in it")
}

// requirementListTooLong is the issue of a list of the requirements, at
// path, that holds more than [MaxPackageRequirements] entries.
func requirementListTooLong(path string, entries int) Issue {
	return NewIssue(CodePackageRequirementsTooMany, path,
		fmt.Sprintf("the list holds at most %d entries, not %d", MaxPackageRequirements, entries))
}

// requirementProblem returns the code of the rule value breaks as an entry
// of the requirements and what is wrong with it, or "" and "".
func requirementProblem(value string) (Code, string) {
	switch {
	case strings.TrimSpace(value) == "":
		return CodePackageRequirementBlank, "must not be blank"
	case len(value) > MaxRequirementBytes:
		return CodePackageRequirementTooLong, fmt.Sprintf("must be at most %d bytes", MaxRequirementBytes)
	case strings.ContainsFunc(value, isControl):
		return CodePackageRequirementControl, "must not contain control characters"
	}

	return "", ""
}

// requirementNamesShown is the number of left-out entries that a warning of
// [Package.TrimRequirements] names. The warning counts the rest.
const requirementNamesShown = 3

// TrimRequirements makes the requirements fit [Package.Validate]. It returns
// warnings, at the path of each list, that name each entry it leaves out:
//
//   - first every entry that is blank, longer than [MaxRequirementBytes] or
//     holds control characters ([CodePackageRequirementLeftOut]),
//   - then the entries of a list after its first [MaxPackageRequirements]
//     ([CodePackageRequirementsTruncated]).
//
// [NewPackage] lists what the document and the configs name as they are, and
// a stored config can name anything. Thus whoever builds a package trims it
// before passing it on. Nothing is left out without a warning.
func (p *Package) TrimRequirements() []Issue {
	r := &p.Requirements
	warnings := []Issue{}

	trim := func(values *[]string, key, noun, nouns string) {
		var left []Issue

		*values, left = trimRequirementList(*values, keyRequirements+"."+key, noun, nouns, "")
		warnings = append(warnings, left...)
	}

	trim(&r.Scenarios, keyScenarios, "Scenario config", "Scenario configs")
	trim(&r.Topologies, keyTopologies, "included topology", "included topologies")
	trim(&r.Templates, keyTemplates, "template", "templates")
	trim(&r.Icons, keyIcons, "custom icon", "custom icons")

	var left []Issue

	r.Images, left = trimRequirementImages(r.Images)
	warnings = append(warnings, left...)

	trim(&r.Apps, keyApps, "app", "apps")
	trim(&r.Files, keyFiles, "file", "files")

	return warnings
}

// trimRequirementList returns the entries of the list of the requirements
// at path that [Package.Validate] takes, as a list that is not nil, and a
// warning for each entry it leaves out, and one for the entries past the
// first [MaxPackageRequirements]. noun and nouns name one entry and several
// in the warnings, and owner, when not empty, follows them.
func trimRequirementList(values []string, path, noun, nouns, owner string) ([]string, []Issue) {
	kept := make([]string, 0, len(values))

	var warnings []Issue

	for _, value := range values {
		if _, problem := requirementProblem(value); problem != "" {
			warnings = append(warnings, NewIssue(CodePackageRequirementLeftOut, path, fmt.Sprintf(
				"The package does not list %s %q%s: it %s.", noun, truncate(value), owner, problem,
			)))

			continue
		}

		kept = append(kept, value)
	}

	if len(kept) > MaxPackageRequirements {
		warnings = append(warnings, NewIssue(CodePackageRequirementsTruncated, path, fmt.Sprintf(
			"The package lists at most %d %s%s, so it does not list %d more: %s.",
			MaxPackageRequirements, nouns, owner, len(kept)-MaxPackageRequirements,
			quotedRequirements(kept[MaxPackageRequirements:]),
		)))
		kept = kept[:MaxPackageRequirements]
	}

	return kept, warnings
}

// trimRequirementImages is [trimRequirementList] for the disk images of the
// requirements and the hostnames that use each.
func trimRequirementImages(images []PackageImage) ([]PackageImage, []Issue) {
	path := keyRequirements + "." + keyImages
	kept := make([]PackageImage, 0, len(images))

	var warnings []Issue

	for _, image := range images {
		if _, problem := requirementProblem(image.Name); problem != "" {
			warnings = append(warnings, NewIssue(CodePackageRequirementLeftOut, path, fmt.Sprintf(
				"The package does not list disk image %q: it %s.", truncate(image.Name), problem,
			)))

			continue
		}

		owner := fmt.Sprintf(" among the users of disk image %q", truncate(image.Name))
		usedBy, left := trimRequirementList(image.UsedBy, path, "host", "hosts", owner)

		warnings = append(warnings, left...)
		kept = append(kept, PackageImage{Name: image.Name, UsedBy: usedBy})
	}

	if len(kept) > MaxPackageRequirements {
		names := make([]string, 0, len(kept)-MaxPackageRequirements)

		for _, image := range kept[MaxPackageRequirements:] {
			names = append(names, image.Name)
		}

		warnings = append(warnings, NewIssue(CodePackageRequirementsTruncated, path, fmt.Sprintf(
			"The package lists at most %d disk images, so it does not list %d more: %s.",
			MaxPackageRequirements, len(names), quotedRequirements(names),
		)))
		kept = kept[:MaxPackageRequirements]
	}

	return kept, warnings
}

// quotedRequirements names the first [requirementNamesShown] of values,
// quoted, and counts the rest.
func quotedRequirements(values []string) string {
	shown := min(len(values), requirementNamesShown)
	quoted := make([]string, 0, shown)

	for _, value := range values[:shown] {
		quoted = append(quoted, strconv.Quote(truncate(value)))
	}

	text := strings.Join(quoted, ", ")

	if rest := len(values) - shown; rest > 0 {
		text += fmt.Sprintf(" and %d more", rest)
	}

	return text
}

// IconNames returns the names of the custom icons the document's devices,
// groups, icon nodes and templates name, sorted, each once.
func (d *Document) IconNames() []string {
	names := map[string]bool{}

	add := func(name string) {
		if name != "" {
			names[name] = true
		}
	}

	for i := range d.Nodes {
		node := &d.Nodes[i]

		if node.Device != nil {
			add(node.Device.Icon)
		}

		if node.Group != nil {
			add(node.Group.Icon)
		}

		if node.Icon != nil {
			add(node.Icon.Icon)
		}
	}

	for i := range d.Templates {
		add(d.Templates[i].Device.Icon)
	}

	return sortedNames(names)
}

// sortedNames returns the keys of a set, sorted, as a list that is empty
// and not nil when the set is empty.
func sortedNames(names map[string]bool) []string {
	sorted := make([]string, 0, len(names))
	sorted = slices.AppendSeq(sorted, maps.Keys(names))

	slices.Sort(sorted)

	return sorted
}

// packageImages returns the disk images the drives of the document's
// devices and of the carried topologies' nodes name, sorted, each with the
// sorted hostnames of the devices that use it.
func packageImages(document *Document, topologies map[string]PackageConfig) []PackageImage {
	users := map[string]map[string]bool{}

	add := func(spec map[string]any, hostname string) {
		for _, image := range specImages(spec) {
			if users[image] == nil {
				users[image] = map[string]bool{}
			}

			if hostname != "" {
				users[image][hostname] = true
			}
		}
	}

	for i := range document.Nodes {
		if device := document.Nodes[i].Device; device != nil {
			add(device.Spec, device.Hostname)
		}
	}

	for _, config := range topologies {
		for _, node := range topologyNodes(config.Spec) {
			add(node, specString(node, specGeneral, specHostname))
		}
	}

	images := make([]PackageImage, 0, len(users))

	for _, name := range slices.Sorted(maps.Keys(users)) {
		images = append(images, PackageImage{Name: name, UsedBy: sortedNames(users[name])})
	}

	return images
}

// packageFiles returns the paths the document's devices, the carried
// topologies' nodes and the carried scenarios' apps name, sorted, each once:
// the sources of injections and the asset directories of apps.
func packageFiles(document *Document, scenarios, topologies map[string]PackageConfig) []string {
	paths := map[string]bool{}

	add := func(values []string) {
		for _, value := range values {
			paths[value] = true
		}
	}

	for i := range document.Nodes {
		if device := document.Nodes[i].Device; device != nil {
			add(specInjections(device.Spec))
		}
	}

	for _, config := range topologies {
		for _, node := range topologyNodes(config.Spec) {
			add(specInjections(node))
		}
	}

	for _, config := range scenarios {
		for _, entry := range scenarioAppEntries(config.Spec) {
			add(textValues(entry[specKeyAssetDir]))
		}
	}

	return sortedNames(paths)
}

// packageApps returns the names of the apps of the given Scenario configs,
// sorted, each once.
func packageApps(scenarios map[string]PackageConfig) []string {
	names := map[string]bool{}

	for _, config := range scenarios {
		for _, entry := range scenarioAppEntries(config.Spec) {
			for _, name := range textValues(entry[keyName]) {
				names[name] = true
			}
		}
	}

	return sortedNames(names)
}

// scenarioAppEntries returns the app entries of a Scenario spec: its apps
// list (phenix.sandia.gov/v2), or the experiment and host lists of its apps
// (phenix.sandia.gov/v1).
func scenarioAppEntries(spec map[string]any) []map[string]any {
	switch apps := spec[keyApps].(type) {
	case []any:
		return objectsOf(apps)
	case map[string]any:
		experiment, _ := apps[specKeyExperiment].([]any)
		host, _ := apps[specKeyHost].([]any)

		return append(objectsOf(experiment), objectsOf(host)...)
	}

	return nil
}

// topologyNodes returns the node specs of a Topology spec.
func topologyNodes(spec map[string]any) []map[string]any {
	nodes, _ := spec[keyNodes].([]any)

	return objectsOf(nodes)
}

// specImages returns the images a node spec's drives name.
func specImages(spec map[string]any) []string {
	hardware, _ := spec[specKeyHardware].(map[string]any)
	drives, _ := hardware[specKeyDrives].([]any)

	images := make([]string, 0, len(drives))

	for _, drive := range objectsOf(drives) {
		images = append(images, textValues(drive[specKeyImage])...)
	}

	return images
}

// specInjections returns the sources of a node spec's injections.
func specInjections(spec map[string]any) []string {
	injections, _ := spec[specKeyInjections].([]any)

	sources := make([]string, 0, len(injections))

	for _, injection := range objectsOf(injections) {
		sources = append(sources, textValues(injection[specKeySource])...)
	}

	return sources
}

// objectsOf returns the objects of a list, leaving out what is not one.
func objectsOf(values []any) []map[string]any {
	objects := make([]map[string]any, 0, len(values))

	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			objects = append(objects, object)
		}
	}

	return objects
}

// textValues returns value, trimmed, as a list of one when it is text that
// is not blank, and as an empty list otherwise.
func textValues(value any) []string {
	text, _ := value.(string)
	if text = strings.TrimSpace(text); text == "" {
		return nil
	}

	return []string{text}
}

// requirementList returns a copy of values, empty and not nil when there is
// none.
func requirementList(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}

	return slices.Clone(values)
}
