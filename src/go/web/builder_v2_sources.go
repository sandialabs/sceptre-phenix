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
	builderV2Topologies       = "topologies"
	builderV2Experiments      = "experiments"
	builderV2Scenarios        = "scenarios"
	builderV2KindTopology     = "Topology"
	builderV2KindScenario     = "Scenario"
	builderV2SourceTopology   = "topology"
	builderV2SourceExperiment = "experiment"
	builderV2SourceScenario   = "scenario"
)

// builderV2SourceKind is one config kind the builder offers as a source.
//
// Topologies and experiments are what a document is generated from; scenarios
// are offered so a publish can name one, and images so node properties can be
// edited against the images that actually exist. VLANs are derived from the
// document itself and are deliberately not a config kind.
type builderV2SourceKind struct {
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

// builderV2SourceKinds is the registry of source kinds, in response order.
var builderV2SourceKinds = []builderV2SourceKind{ //nolint:gochecknoglobals // immutable registry
	{
		kind: builderV2KindTopology, list: builderV2SourceTopology,
		resource: builderV2Topologies, key: builderV2Topologies, generatable: true,
	},
	{
		kind: kindExperiment, list: builderV2SourceExperiment,
		resource: builderV2Experiments, key: builderV2Experiments, generatable: true,
	},
	{
		kind: builderV2KindScenario, list: builderV2SourceScenario,
		resource: builderV2Scenarios, key: builderV2Scenarios, generatable: false,
	},
	// Image configs have no kind specific RBAC vocabulary; the config
	// permission is their only gate.
	{kind: "Image", list: "image", resource: "", key: "images", generatable: false},
}

// builderV2SourceKindFor returns the registry entry for a canonical config
// kind.
func builderV2SourceKindFor(kind string) (builderV2SourceKind, bool) {
	for _, entry := range builderV2SourceKinds {
		if entry.kind == kind {
			return entry, true
		}
	}

	return builderV2SourceKind{kind: "", list: "", resource: "", key: "", generatable: false}, false
}

// builderV2KindAllowed reports whether the role holds the kind specific list
// permission that already gates a config kind elsewhere in the API. The checks
// are written as literal calls so the RBAC policy generator (see
// web/rbac/known_policy_gen.go) records them.
func builderV2KindAllowed(role rbac.Role, resource string, names ...string) bool {
	switch resource {
	case builderV2Topologies:
		return role.Allowed("topologies", "list", names...)
	case builderV2Experiments:
		return role.Allowed("experiments", "list", names...)
	case builderV2Scenarios:
		return role.Allowed("scenarios", "list", names...)
	case "":
		return true
	}

	return false
}

// builderGenerateRequest asks for a builder document. Exactly one of Source and
// Content must be set: Source names a stored config, Content carries an
// uploaded config as JSON or YAML text.
type builderGenerateRequest struct {
	Source  string `json:"source"`
	Content string `json:"content"`
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
	// UI can tell Builder v2 configs from legacy ones.
	Builder string `json:"builder,omitempty"`
	// Generatable reports whether POST /builder-v2/generate accepts this source.
	// Scenarios and images are offered for selection, not for generation.
	Generatable bool `json:"generatable"`
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
	entry, _ := builderV2SourceKindFor(cfg.Kind)

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

	if cfg.Kind == builderV2KindScenario {
		digest, err := bdoc.ContentDigest(cfg.Spec)
		if err != nil {
			return builderSourceResponse{}, fmt.Errorf("digesting scenario %s: %w", cfg.FullName(), err)
		}

		source.Digest = digest
	} else if entry.generatable {
		digest, err := bdoc.SourceDigest(*cfg)
		if err != nil {
			return builderSourceResponse{}, fmt.Errorf("digesting source %s: %w", cfg.FullName(), err)
		}

		source.Digest = digest
	}

	switch {
	case cfg.HasAnnotation(bapi.DocumentAnnotation):
		source.Builder = bapi.DocumentAnnotation
	case config.HasBuilderXML(*cfg):
		source.Builder = config.BuilderXMLAnnotation
	}

	return source, nil
}

// listSources - GET /builder-v2/sources.
//
// Sources are grouped by kind: topologies and experiments to generate a
// document from, scenarios to name when publishing, and images to edit node
// properties against. Every config is filtered through the same per-config
// authorization the /configs endpoints apply, plus the kind specific list
// permission that gates the kind elsewhere in the API.
func (b *builderV2API) listSources(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2ListSources")

	actor, err := builderV2Authorize(r, builderV2VerbList, "listing builder sources")
	if err != nil {
		return err
	}

	response := make(map[string][]builderSourceResponse, len(builderV2SourceKinds))

	for _, entry := range builderV2SourceKinds {
		sources, err := b.sourcesOfKind(actor, entry)
		if err != nil {
			return err
		}

		response[entry.key] = sources
	}

	return builderV2WriteJSON(w, http.StatusOK, "", response)
}

// sourcesOfKind returns the configs of one kind the caller may see. A caller
// holding no list permission for the kind at all is answered with an empty
// group rather than an error: the other groups are still usable.
func (b *builderV2API) sourcesOfKind(
	actor builderV2Actor,
	entry builderV2SourceKind,
) ([]builderSourceResponse, error) {
	sources := []builderSourceResponse{}

	if !builderV2KindAllowed(actor.role, entry.resource) {
		return sources, nil
	}

	configs, err := b.listConfigs(entry.list)
	if err != nil {
		return nil, weberror.NewWebError(err, "unable to list %s configs", entry.kind).
			SetStatus(http.StatusInternalServerError)
	}

	for i := range configs {
		config := &configs[i]

		if !builderV2BaseAllowed(actor.role, builderV2VerbList, config.FullName()) {
			continue
		}

		if !builderV2KindAllowed(actor.role, entry.resource, config.Metadata.Name) {
			continue
		}

		source, err := newBuilderSourceResponse(config, true)
		if err != nil {
			return nil, weberror.NewWebError(err, "unable to describe %s config", config.FullName()).
				SetStatus(http.StatusInternalServerError)
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// generateDocument - POST /builder-v2/generate.
//
// Generation is a pure transform: a stored config is read, or an uploaded one
// is parsed, and the resulting document is returned to the caller. Nothing is
// written, so a generated document only becomes durable once the caller stores
// it in a draft.
func (b *builderV2API) generateDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderV2Generate")

	// Generation reads configs and writes nothing, so it needs the config read
	// permission. Stored sources are additionally authorized by name, and
	// uploads by the config create permission, in generationSource.
	actor, err := builderV2Authorize(r, builderV2VerbGet, "importing a builder document")
	if err != nil {
		return err
	}

	var request builderGenerateRequest

	if err := builderV2Decode(w, r, &request); err != nil {
		return err
	}

	if (request.Source == "") == (request.Content == "") {
		return weberror.NewWebError(nil, "exactly one of source and content is required").
			SetStatus(http.StatusBadRequest)
	}

	config, err := b.generationSource(actor, request)
	if err != nil {
		return err
	}

	document, warnings, err := bdoc.FromConfig(*config, bdoc.WithTopologyLoader(b.includedTopologyLoader(actor)))
	if err != nil {
		if errors.Is(err, bdoc.ErrUnsupportedKind) {
			return weberror.NewWebError(err, "%s configs cannot be opened in the builder", config.Kind).
				SetStatus(http.StatusUnprocessableEntity)
		}

		return weberror.NewWebError(err, "unable to import a builder document from %s", config.FullName()).
			SetStatus(http.StatusUnprocessableEntity)
	}

	warnings, err = b.bindGeneratedScenario(actor, document, warnings, request.Source != "")
	if err != nil {
		return err
	}

	// FromConfig leaves the time out, so it generates the same document from
	// the same config; the Inspector shows it with the source.
	document.Source.ImportedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		return builderV2WebError(err, "unable to encode the generated builder document")
	}

	if warnings == nil {
		warnings = []string{}
	}

	source, err := newBuilderSourceResponse(config, request.Source != "")
	if err != nil {
		return weberror.NewWebError(err, "unable to describe generated source").
			SetStatus(http.StatusInternalServerError)
	}

	return builderV2WriteJSON(w, http.StatusOK, "", builderGenerateResponse{
		Document: data,
		Warnings: warnings,
		Source:   source,
	})
}

// generationSource returns the config a document is generated from, either read
// from the store or parsed from the uploaded content.
func (b *builderV2API) generationSource(
	actor builderV2Actor,
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
	if !builderV2BaseAllowed(actor.role, builderV2VerbCreate) {
		return nil, builderV2Forbidden(actor, "importing a builder document from an uploaded config")
	}

	return builderV2UploadedSource(request.Content)
}

// bindGeneratedScenario binds the scenario an experiment names to the stored
// config. [bdoc.FromConfig] cannot read the store, so it marks that scenario
// stored with the digest of the experiment's embedded copy, which phenix
// merges and filters from the stored config and so never matches it: publish
// would refuse the reference as changed. For a stored experiment, a scenario
// the caller may list is referenced the way the Scenario dialog attaches one,
// by the stored apiVersion and digest without content. An uploaded experiment
// was not built from this server's scenario of that name, which may differ, so
// its own copy is kept, as is the copy of a stored experiment whose scenario is
// missing or hidden from the caller: either is attached as an uploaded
// scenario, with a warning.
func (b *builderV2API) bindGeneratedScenario(
	actor builderV2Actor,
	document *bdoc.Document,
	warnings []string,
	storedSource bool,
) ([]string, error) {
	ref := document.Scenario
	if ref == nil || ref.Kind != bdoc.ScenarioRefStored {
		return warnings, nil
	}

	if !storedSource {
		return keepEmbeddedScenario(document, warnings, fmt.Sprintf(
			"the uploaded experiment's copy of scenario %q is attached as an uploaded scenario, "+
				"not this server's stored scenario of that name",
			ref.Name,
		)), nil
	}

	var stored *store.Config

	// The name is an annotation, which an experiment can set to anything, so
	// only a plausible config name is looked up.
	name := store.ConfigFullName(builderV2KindScenario, ref.Name)
	plausible := ref.Name != "" && strings.TrimSpace(ref.Name) == ref.Name && !strings.Contains(ref.Name, "/")

	if plausible &&
		builderV2BaseAllowed(actor.role, builderV2VerbList, name) &&
		builderV2KindAllowed(actor.role, builderV2Scenarios, ref.Name) {
		config, err := b.getConfig(name)
		if err != nil && !errors.Is(err, store.ErrNotExist) {
			return nil, weberror.NewWebError(err, "unable to get config %s", name).
				SetStatus(http.StatusInternalServerError)
		}

		if err == nil {
			stored = config
		}
	}

	if stored == nil {
		return keepEmbeddedScenario(document, warnings, fmt.Sprintf(
			"scenario %q is not available on this server, so the experiment's copy of it is attached as an uploaded scenario",
			ref.Name,
		)), nil
	}

	digest, err := bdoc.ContentDigest(stored.Spec)
	if err != nil {
		return nil, weberror.NewWebError(err, "unable to digest scenario %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	document.Scenario = &bdoc.ScenarioRef{
		Kind:       bdoc.ScenarioRefStored,
		Name:       stored.Metadata.Name,
		Content:    nil,
		APIVersion: stored.Version,
		Digest:     digest,
	}

	return warnings, nil
}

// keepEmbeddedScenario attaches the experiment's embedded copy of its scenario
// as an uploaded scenario of the same name, and reports it with warning.
func keepEmbeddedScenario(document *bdoc.Document, warnings []string, warning string) []string {
	document.Scenario.Kind = bdoc.ScenarioRefUploaded
	warnings = append(warnings, warning)
	document.Source.Warnings = warnings

	return warnings
}

// storedSource reads a stored config, authorized by its canonical name.
func (b *builderV2API) storedSource(
	actor builderV2Actor,
	source string,
) (*store.Config, error) {
	name := store.ConfigFullName(source)
	if name == "" {
		return nil, weberror.NewWebError(nil, "source must name a stored config as <kind>/<name>").
			SetStatus(http.StatusBadRequest)
	}

	kind, configName, _ := strings.Cut(name, "/")

	entry, known := builderV2SourceKindFor(kind)
	if !known || !entry.generatable {
		return nil, weberror.NewWebError(nil, "%s configs cannot be opened in the builder", kind).
			SetStatus(http.StatusUnprocessableEntity)
	}

	if !builderV2BaseAllowed(actor.role, builderV2VerbGet, name) {
		return nil, builderV2Forbidden(actor, "importing a builder document from "+name)
	}

	// A config the caller may not see listed may not be generated from either.
	if !builderV2KindAllowed(actor.role, entry.resource, configName) {
		return nil, builderV2Forbidden(actor, "importing a builder document from "+name)
	}

	config, err := b.getConfig(name)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil, builderV2NotFound("config", name)
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
// a file path there, but the Builder never reads files from the server on a
// caller's behalf.
func (b *builderV2API) includedTopologyLoader(actor builderV2Actor) bdoc.TopologyLoader {
	return func(name string) (*store.Config, error) {
		if strings.ContainsAny(name, `/\`) {
			return nil, errors.New("the Builder reads included topologies from the config store only, not from files")
		}

		full := builderV2KindTopology + "/" + name

		if !builderV2BaseAllowed(actor.role, builderV2VerbGet, full) ||
			!builderV2KindAllowed(actor.role, builderV2Topologies, name) {
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

// builderV2UploadedSource parses an uploaded config. JSON is tried first and
// YAML second, matching how configs are accepted elsewhere in the API. Nothing
// is persisted.
func builderV2UploadedSource(content string) (*store.Config, error) {
	if int64(len(content)) > bapi.MaxDocumentBytes {
		return nil, weberror.NewWebError(nil, "uploaded config is larger than %d bytes", bapi.MaxDocumentBytes).
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
		return nil, weberror.NewWebError(err, "the uploaded config is not valid JSON or YAML").
			SetStatus(http.StatusUnprocessableEntity)
	}

	if store.ConfigFullName(config.Kind, config.Metadata.Name) == "" {
		return nil, weberror.NewWebError(nil, "the uploaded config is missing a known kind or a name").
			SetStatus(http.StatusUnprocessableEntity)
	}

	return config, nil
}
