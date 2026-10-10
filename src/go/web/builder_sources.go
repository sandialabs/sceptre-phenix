package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// Kind specific RBAC resources that gate the source kinds elsewhere in the API.
// The calls to [rbac.Role.Allowed] still pass string literals so the policy
// generator records them; these constants name the same values everywhere else.
const (
	builderTopologies       = "topologies"
	builderExperiments      = "experiments"
	builderScenarios        = "scenarios"
	builderKindTopology     = "Topology"
	builderKindScenario     = "Scenario"
	builderSourceTopology   = "topology"
	builderSourceExperiment = "experiment"
	builderSourceScenario   = "scenario"
)

// builderSourceKind is one config kind the builder offers as a source.
//
// Topologies and experiments are what a document is generated from; scenarios
// are offered so a publish can name one, and images so node properties can be
// edited against the images that actually exist. VLANs are derived from the
// document itself and are deliberately not a config kind.
type builderSourceKind struct {
	// kind is the canonical config kind, as stored.
	kind string
	// list is the argument [phenix/api/config.List] takes for the kind.
	list string
	// resource is the kind specific RBAC resource that already gates the kind
	// elsewhere in the API, or empty when the kind has none. It is checked in
	// addition to, never instead of, the config permission.
	resource string
	// key is the response field the kind is reported under.
	key string
	// generatable reports whether [bdoc.FromConfig] can build a document from
	// the kind.
	generatable bool
}

// builderSourceKinds is the registry of source kinds, in response order.
var builderSourceKinds = []builderSourceKind{ //nolint:gochecknoglobals // immutable registry
	{
		kind: builderKindTopology, list: builderSourceTopology,
		resource: builderTopologies, key: builderTopologies, generatable: true,
	},
	{
		kind: kindExperiment, list: builderSourceExperiment,
		resource: builderExperiments, key: builderExperiments, generatable: true,
	},
	{
		kind: builderKindScenario, list: builderSourceScenario,
		resource: builderScenarios, key: builderScenarios, generatable: false,
	},
	// Image configs have no kind specific RBAC vocabulary; the config
	// permission is their only gate.
	{kind: "Image", list: "image", resource: "", key: "images", generatable: false},
}

// builderSourceKindFor returns the registry entry for a canonical config
// kind.
func builderSourceKindFor(kind string) (builderSourceKind, bool) {
	for _, entry := range builderSourceKinds {
		if entry.kind == kind {
			return entry, true
		}
	}

	return builderSourceKind{kind: "", list: "", resource: "", key: "", generatable: false}, false
}

// builderKindAllowed reports whether the role holds the kind specific list
// permission that already gates a config kind elsewhere in the API. The checks
// are written as literal calls so the RBAC policy generator (see
// web/rbac/known_policy_gen.go) records them.
func builderKindAllowed(role rbac.Role, resource string, names ...string) bool {
	switch resource {
	case builderTopologies:
		return role.Allowed("topologies", "list", names...)
	case builderExperiments:
		return role.Allowed("experiments", "list", names...)
	case builderScenarios:
		return role.Allowed("scenarios", "list", names...)
	case "":
		return true
	}

	return false
}

// What a generate request may ask to become of a topology's included
// topologies (see [builderGenerateRequest]).
const (
	builderIncludesKeep    = "keep"
	builderIncludesCombine = "combine"
)

// builderGenerateRequest asks for a builder document. Exactly one of Source and
// Content must be set: Source names a stored config, Content carries an
// uploaded config as JSON or YAML text.
type builderGenerateRequest struct {
	Source  string `json:"source"`
	Content string `json:"content"`
	// Includes says what becomes of a topology's included topologies: "keep",
	// which an empty value means too, shows their nodes read only, and
	// "combine" copies them into the document as its own.
	Includes string `json:"includes"`
	// Copy asks for a document that is not linked to the topology it is
	// generated from, so that publishing it creates a new topology.
	// Combining implies it. A copy gets the diagram note
	// "Copied from <topology name>".
	Copy bool `json:"copy"`
	// Name is the name of the new topology, and so of the document, of a
	// copy or a combined document. Empty means the name of the source with
	// "-combined" or "-copy" added.
	Name string `json:"name"`
}

// combine reports whether the request asks for the included topologies'
// nodes to be copied into the document.
func (r builderGenerateRequest) combine() bool {
	return r.Includes == builderIncludesCombine
}

// detach reports whether the request asks for a document that is not linked
// to its source: a copy, or a combined document.
func (r builderGenerateRequest) detach() bool {
	return r.Copy || r.combine()
}

// builderSourceResponse is the JSON view of a config a document can be
// generated from.
type builderSourceResponse struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	FullName   string `json:"fullName"`
	APIVersion string `json:"apiVersion,omitempty"`
	Created    string `json:"created,omitempty"`
	Updated    string `json:"updated,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Stored     bool   `json:"stored"`
	// Builder names the builder annotation the config carries, if any, so the
	// UI can tell Builder configs from legacy ones.
	Builder string `json:"builder,omitempty"`
	// Generatable reports whether POST /builder/generate accepts this source.
	// Scenarios and images are offered for selection, not for generation.
	Generatable bool `json:"generatable"`
	// IncludeCount is the number of topologies a Topology config includes. It
	// is reported only when the caller may get the config.
	IncludeCount int `json:"includeCount,omitempty"`
}

// builderGenerateResponse is a generated document with the warnings raised
// while generating it.
type builderGenerateResponse struct {
	Document json.RawMessage       `json:"document"`
	Warnings []string              `json:"warnings"`
	Source   builderSourceResponse `json:"source"`
}

// newBuilderSourceResponse converts a config into its JSON view. The config's
// spec is never included: sources are a picker, not a config dump.
func newBuilderSourceResponse(cfg *store.Config, stored bool) (builderSourceResponse, error) {
	entry, _ := builderSourceKindFor(cfg.Kind)

	source := builderSourceResponse{ //nolint:exhaustruct // the builder annotation is optional
		Kind:        cfg.Kind,
		Name:        cfg.Metadata.Name,
		FullName:    cfg.FullName(),
		APIVersion:  cfg.Version,
		Created:     cfg.Metadata.Created,
		Updated:     cfg.Metadata.Updated,
		Generatable: entry.generatable,
		Stored:      stored,
	}

	if entry.generatable {
		// The digest an import of the config records (see [bdoc.ImportDigest]).
		digest, err := bdoc.ImportDigest(*cfg)
		if err != nil {
			return builderSourceResponse{}, fmt.Errorf("digesting source %s: %w", cfg.FullName(), err)
		}

		source.Digest = digest
	}

	switch {
	case cfg.HasAnnotation(bapi.DocumentAnnotation):
		source.Builder = bapi.DocumentAnnotation
	case bdoc.HasLegacyDiagram(*cfg):
		source.Builder = bdoc.LegacyXMLAnnotation
	}

	return source, nil
}

// listSources - GET /builder/sources.
//
// Sources are grouped by kind: topologies and experiments to generate a
// document from, scenarios to name when publishing, and images to edit node
// properties against. Every config is filtered through the same per-config
// authorization the /configs endpoints apply, plus the kind specific list
// permission that gates the kind elsewhere in the API.
func (b *builderAPI) listSources(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListSources")

	actor, err := builderAuthorize(r, builderVerbList, "listing builder sources")
	if err != nil {
		return err
	}

	response := make(map[string][]builderSourceResponse, len(builderSourceKinds))

	for _, entry := range builderSourceKinds {
		sources, err := b.sourcesOfKind(actor, entry)
		if err != nil {
			return err
		}

		response[entry.key] = sources
	}

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// sourcesOfKind returns the configs of one kind the caller may see. A caller
// holding no list permission for the kind at all is answered with an empty
// group rather than an error: the other groups are still usable.
func (b *builderAPI) sourcesOfKind(
	actor builderActor,
	entry builderSourceKind,
) ([]builderSourceResponse, error) {
	sources := []builderSourceResponse{}

	if !builderKindAllowed(actor.role, entry.resource) {
		return sources, nil
	}

	configs, err := b.listConfigs(entry.list)
	if err != nil {
		return nil, weberror.NewWebError(err, "unable to list %s configs", entry.kind).
			SetStatus(http.StatusInternalServerError)
	}

	for i := range configs {
		config := &configs[i]

		if !builderBaseAllowed(actor.role, builderVerbList, config.FullName()) {
			continue
		}

		if !builderKindAllowed(actor.role, entry.resource, config.Metadata.Name) {
			continue
		}

		source, err := newBuilderSourceResponse(config, true)
		if err != nil {
			return nil, weberror.NewWebError(err, "unable to describe %s config", config.FullName()).
				SetStatus(http.StatusInternalServerError)
		}

		// The count says something of the spec, so it is for a caller who may
		// read the config, which is also what importing it needs.
		if entry.kind == builderKindTopology && builderBaseAllowed(actor.role, builderVerbGet, config.FullName()) {
			source.IncludeCount = bdoc.IncludeCount(*config)
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// generateDocument - POST /builder/generate.
//
// Generation is a pure transform: a stored config is read, or an uploaded one
// is parsed, and the resulting document is returned to the caller. Nothing is
// written, so a generated document only becomes durable once the caller stores
// it in a draft. A topology the legacy Builder drew, and nothing has
// published since, is converted from its diagram (see [builderAPI.generate]).
//
// A topology may be imported as a new one: with the nodes of its included
// topologies copied into the document (includes "combine"), or as it is
// (copy). Either way the document is detached from the topology (see
// [bdoc.Document.Detach]) and takes the new topology's name, so that
// publishing a draft of it creates that topology and leaves the source alone.
// The document also gets the diagram note "Copied from <name>", where name is
// the name in the source's metadata (see [bdoc.Document.NoteCopiedFrom]).
func (b *builderAPI) generateDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGenerate")

	// Generation reads configs and writes nothing, so it needs the config read
	// permission. Stored sources are additionally authorized by name, and
	// uploads by the config create permission, in generationSource.
	actor, err := builderAuthorize(r, builderVerbGet, "importing a builder document")
	if err != nil {
		return err
	}

	var request builderGenerateRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	if (request.Source == "") == (request.Content == "") {
		return weberror.NewWebError(nil, "exactly one of source and content is required").
			SetStatus(http.StatusBadRequest)
	}

	if err := builderGenerateChoices(request); err != nil {
		return err
	}

	config, err := b.generationSource(actor, request)
	if err != nil {
		return err
	}

	newName, err := builderGenerateNewName(request, config)
	if err != nil {
		return err
	}

	options := []bdoc.GenerateOption{
		bdoc.WithTopologyLoader(b.includedTopologyLoader(actor)),
		bdoc.WithScenarioResolver(b.storedScenarioResolver(actor)),
	}
	if request.combine() {
		options = append(options, bdoc.WithCombinedIncludes())
	}

	document, warnings, err := b.generate(config, options...)
	if err != nil {
		switch {
		case errors.Is(err, bdoc.ErrUnsupportedKind):
			return weberror.NewWebError(err, "%s configs cannot be opened in the builder", config.Kind).
				SetStatus(http.StatusUnprocessableEntity)
		case errors.Is(err, bdoc.ErrCombineExperiment):
			return builderGenerateOnlyTopology()
		case errors.Is(err, errBuilderScenarioLookup):
			return weberror.NewWebError(err, "unable to read the scenario of %s", config.FullName()).
				SetStatus(http.StatusInternalServerError)
		}

		return builderGenerateFailure(err, config)
	}

	if request.detach() {
		// Detach removes the source's name, so the note names the config
		// that was read, stored or uploaded.
		document.NoteCopiedFrom(config.Metadata.Name)
		document.Detach(newName)

		if err := document.Validate(); err != nil {
			return builderGenerateFailure(err, config)
		}
	}

	if request.Source == "" && len(document.Scenarios) > 0 {
		warnings = uploadedExperimentScenarioWarning(document, warnings)
	}

	// FromConfig leaves the time out, so it generates the same document from
	// the same config; the Inspector shows it with the source.
	document.Source.ImportedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		return builderWebError(err, "unable to encode the generated builder document")
	}

	if warnings == nil {
		warnings = []string{}
	}

	source, err := newBuilderSourceResponse(config, request.Source != "")
	if err != nil {
		return weberror.NewWebError(err, "unable to describe generated source").
			SetStatus(http.StatusInternalServerError)
	}

	return builderWriteJSON(w, http.StatusOK, "", builderGenerateResponse{
		Document: data,
		Warnings: warnings,
		Source:   source,
	})
}

// builderGenerateChoices checks the choices of a generate request that do
// not depend on the config it reads.
func builderGenerateChoices(request builderGenerateRequest) error {
	switch request.Includes {
	case "", builderIncludesKeep, builderIncludesCombine:
	default:
		return weberror.NewWebError(nil, "includes must be %q or %q", builderIncludesKeep, builderIncludesCombine).
			SetStatus(http.StatusBadRequest)
	}

	if request.Name != "" && !request.detach() {
		return weberror.NewWebError(nil, "name is only used with copy or with includes %q", builderIncludesCombine).
			SetStatus(http.StatusBadRequest)
	}

	return nil
}

// builderGenerateFailure is the refusal of a config no valid document can
// be made from.
func builderGenerateFailure(err error, source *store.Config) *weberror.WebError {
	return weberror.NewWebError(err, "unable to import a builder document from %s", source.FullName()).
		SetStatus(http.StatusUnprocessableEntity)
}

// builderGenerateOnlyTopology is the refusal of a copy or a combined import
// of an experiment. An experiment already holds the nodes phenix merged into
// it, and is not copied through the Builder.
func builderGenerateOnlyTopology() *weberror.WebError {
	return weberror.NewWebError(nil, "only a topology can be combined or copied on import").
		SetStatus(http.StatusUnprocessableEntity)
}

// builderGenerateNewName returns the name of the new topology a copy or a
// combined import of the source topology makes, or "" for a request that
// asks for neither. It is the name the request gives, or else the source's
// own with "-combined" or "-copy" added. It must be a config name of at most
// [bdoc.MaxNameBytes], and not the name of the stored topology it is
// imported from. The server does not look for a name no topology has: the
// client proposes one, and publishing checks it.
//
// An experiment is refused. A config of any other kind gets no name: no
// document is generated from it, and that refusal says why.
func builderGenerateNewName(request builderGenerateRequest, source *store.Config) (string, error) {
	switch {
	case !request.detach():
		return "", nil
	case strings.EqualFold(source.Kind, kindExperiment):
		return "", builderGenerateOnlyTopology()
	case !strings.EqualFold(source.Kind, builderKindTopology):
		return "", nil
	}

	name := request.Name

	switch {
	case name != "":
	case request.combine():
		name = source.Metadata.Name + "-combined"
	default:
		name = source.Metadata.Name + "-copy"
	}

	if name == "" || len(name) > bdoc.MaxNameBytes || !config.NameRegex.MatchString(name) {
		return "", weberror.NewWebError(
			nil,
			"new topology name %q is not allowed: use 1 to %d letters, numbers, underscores, at signs, periods and hyphens",
			builderLegacyShown(name), bdoc.MaxNameBytes,
		).SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodeImportNameInvalid))
	}

	if request.Source != "" && name == source.Metadata.Name {
		return "", weberror.NewWebError(
			nil, "new topology name %q is the name of the imported topology: enter another name", name,
		).SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodeImportNameSource))
	}

	return name, nil
}

// generate builds the document of a config: from its legacy Builder diagram
// when it is a topology that has one and no Builder document, and from the
// config alone otherwise.
func (b *builderAPI) generate(
	config *store.Config,
	options ...bdoc.GenerateOption,
) (*bdoc.Document, []string, error) {
	if bdoc.HasLegacyDiagram(*config) && !config.HasAnnotation(bapi.DocumentAnnotation) {
		return bdoc.FromLegacyTopology(*config, options...)
	}

	return bdoc.FromConfig(*config, options...)
}

// generationSource returns the config a document is generated from, either read
// from the store or parsed from the uploaded content.
func (b *builderAPI) generationSource(
	actor builderActor,
	request builderGenerateRequest,
) (*store.Config, error) {
	if request.Source != "" {
		return b.storedSource(actor, request.Source)
	}

	// An upload is parsed the way POST /configs parses a config, including its
	// ${NAME} substitution from the server's environment, so it needs the
	// permission that endpoint needs. A role that may only read configs could
	// otherwise read any server environment variable back from the generated
	// document. sandialabs/sceptre-phenix#436 describes the wider issue.
	if !builderBaseAllowed(actor.role, builderVerbCreate) {
		return nil, builderForbidden(actor, "importing a builder document from a config file")
	}

	return builderUploadedSource(request.Content)
}

// errBuilderScenarioLookup wraps a failure to read the scenario an imported
// experiment names, which is the server's fault and not the config's.
var errBuilderScenarioLookup = errors.New("reading the scenario failed")

// storedScenarioResolver tells generation whether the Scenario config an
// experiment names is one the caller may list on this server: the config
// permission and the scenarios permission, as GET /builder/sources lists
// scenarios, and the config is stored. A scenario the caller may not list is
// answered as one that does not exist, so its existence is not disclosed.
func (b *builderAPI) storedScenarioResolver(actor builderActor) bdoc.ScenarioResolver {
	return func(name string) (bool, error) {
		full := store.ConfigFullName(builderKindScenario, name)
		if full == "" ||
			!builderBaseAllowed(actor.role, builderVerbList, full) ||
			!builderKindAllowed(actor.role, builderScenarios, name) {
			return false, nil
		}

		_, err := b.getConfig(full)

		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, store.ErrNotExist):
			return false, nil
		}

		return false, fmt.Errorf("%w: %w", errBuilderScenarioLookup, err)
	}
}

// uploadedExperimentScenarioWarning says that the scenario an imported
// experiment file names is listed as this server's Scenario config of that
// name: the scenario the file holds is not imported, and may differ.
func uploadedExperimentScenarioWarning(document *bdoc.Document, warnings []string) []string {
	warnings = append(warnings, fmt.Sprintf(
		"scenario %q is this server's Scenario config of that name, not the copy the experiment file holds",
		document.Scenarios[0],
	))
	document.Source.Warnings = warnings

	return warnings
}

// storedSource reads a stored config, authorized by its canonical name.
func (b *builderAPI) storedSource(
	actor builderActor,
	source string,
) (*store.Config, error) {
	name := store.ConfigFullName(source)
	if name == "" {
		return nil, weberror.NewWebError(nil, "source must name a stored config as <kind>/<name>").
			SetStatus(http.StatusBadRequest)
	}

	kind, configName, _ := strings.Cut(name, "/")

	entry, known := builderSourceKindFor(kind)
	if !known || !entry.generatable {
		return nil, weberror.NewWebError(nil, "%s configs cannot be opened in the builder", kind).
			SetStatus(http.StatusUnprocessableEntity)
	}

	if !builderBaseAllowed(actor.role, builderVerbGet, name) {
		return nil, builderForbidden(actor, "importing a builder document from "+name)
	}

	// A config the caller may not see listed may not be generated from either.
	if !builderKindAllowed(actor.role, entry.resource, configName) {
		return nil, builderForbidden(actor, "importing a builder document from "+name)
	}

	config, err := b.getConfig(name)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil, builderNotFound("config", name)
		}

		return nil, weberror.NewWebError(err, "unable to get config %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	return config, nil
}

// errBuilderIncludeForbidden is the include loader's refusal of a topology
// the caller may not read.
var errBuilderIncludeForbidden = errors.New("you are not allowed to read it")

// includedTopologyLoader resolves includeTopologies for generation and for
// the checks publish makes of them. An include is read from the config store
// only, under the same authorization as a stored source: phenix also accepts
// a file path there, but the Builder never reads a file an include names.
// The one file it reads from the server on a caller's behalf is the Builder
// file a topology's "builder-doc" annotation names, under the limits of
// [bapi.ReadDocumentFile].
func (b *builderAPI) includedTopologyLoader(actor builderActor) bdoc.TopologyLoader {
	return func(name string) (*store.Config, error) {
		if strings.ContainsAny(name, `/\`) {
			return nil, errors.New("the Builder reads included topologies from the config store only, not from files")
		}

		full := builderKindTopology + "/" + name

		if !builderBaseAllowed(actor.role, builderVerbGet, full) ||
			!builderKindAllowed(actor.role, builderTopologies, name) {
			return nil, errBuilderIncludeForbidden
		}

		config, err := b.getConfig(full)
		if err != nil {
			if errors.Is(err, store.ErrNotExist) {
				return nil, errors.New("no stored topology has that name")
			}

			return nil, fmt.Errorf("reading it failed: %w", err)
		}

		return config, nil
	}
}

// builderUploadedSource parses an uploaded config. JSON is tried first and
// YAML second, matching how configs are accepted elsewhere in the API. Nothing
// is persisted.
func builderUploadedSource(content string) (*store.Config, error) {
	if int64(len(content)) > bapi.MaxDocumentBytes {
		return nil, weberror.NewWebError(nil, "the config file is larger than %d bytes", bapi.MaxDocumentBytes).
			SetStatus(http.StatusRequestEntityTooLarge)
	}

	var (
		body   = []byte(content)
		config *store.Config
		err    error
	)

	if strings.HasPrefix(strings.TrimLeft(content, " \t\r\n"), "{") {
		config, err = store.NewConfigFromJSON(body)
	} else {
		config, err = store.NewConfigFromYAML(body)
	}

	if err != nil {
		return nil, weberror.NewWebError(err, "the config file is not valid JSON or YAML").
			SetStatus(http.StatusUnprocessableEntity)
	}

	// ConfigFullName accepts an empty name, so that is checked here.
	if config.Metadata.Name == "" || store.ConfigFullName(config.Kind, config.Metadata.Name) == "" {
		return nil, weberror.NewWebError(nil, "the config file is missing a known kind or a name").
			SetStatus(http.StatusUnprocessableEntity)
	}

	return config, nil
}
