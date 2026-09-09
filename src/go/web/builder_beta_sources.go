package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	bapi "phenix/api/builder"
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
	builderBetaTopologies       = "topologies"
	builderBetaExperiments      = "experiments"
	builderBetaScenarios        = "scenarios"
	builderBetaKindTopology     = "Topology"
	builderBetaKindScenario     = "Scenario"
	builderBetaSourceTopology   = "topology"
	builderBetaSourceExperiment = "experiment"
	builderBetaSourceScenario   = "scenario"
)

// builderBetaSourceKind is one config kind the builder offers as a source.
//
// Topologies and experiments are what a document is generated from; scenarios
// are offered so a publish can name one, and images so node properties can be
// edited against the images that actually exist. VLANs are derived from the
// document itself and are deliberately not a config kind.
type builderBetaSourceKind struct {
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

// builderBetaSourceKinds is the registry of source kinds, in response order.
var builderBetaSourceKinds = []builderBetaSourceKind{ //nolint:gochecknoglobals // immutable registry
	{
		kind: builderBetaKindTopology, list: builderBetaSourceTopology,
		resource: builderBetaTopologies, key: builderBetaTopologies, generatable: true,
	},
	{
		kind: kindExperiment, list: builderBetaSourceExperiment,
		resource: builderBetaExperiments, key: builderBetaExperiments, generatable: true,
	},
	{
		kind: builderBetaKindScenario, list: builderBetaSourceScenario,
		resource: builderBetaScenarios, key: builderBetaScenarios, generatable: false,
	},
	// Image configs have no kind specific RBAC vocabulary; the config
	// permission is their only gate.
	{kind: "Image", list: "image", resource: "", key: "images", generatable: false},
}

// builderBetaSourceKindFor returns the registry entry for a canonical config
// kind.
func builderBetaSourceKindFor(kind string) (builderBetaSourceKind, bool) {
	for _, entry := range builderBetaSourceKinds {
		if entry.kind == kind {
			return entry, true
		}
	}

	return builderBetaSourceKind{kind: "", list: "", resource: "", key: "", generatable: false}, false
}

// builderBetaKindAllowed reports whether the role holds the kind specific list
// permission that already gates a config kind elsewhere in the API. The checks
// are written as literal calls so the RBAC policy generator (see
// web/rbac/known_policy_gen.go) records them.
func builderBetaKindAllowed(role rbac.Role, resource string, names ...string) bool {
	switch resource {
	case builderBetaTopologies:
		return role.Allowed("topologies", "list", names...)
	case builderBetaExperiments:
		return role.Allowed("experiments", "list", names...)
	case builderBetaScenarios:
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
	// UI can tell beta authored configs from legacy ones.
	Builder string `json:"builder,omitempty"`
	// Generatable reports whether POST /builder/generate accepts this source.
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
func newBuilderSourceResponse(config *store.Config, stored bool) (builderSourceResponse, error) {
	entry, _ := builderBetaSourceKindFor(config.Kind)

	source := builderSourceResponse{ //nolint:exhaustruct // the builder annotation is optional
		Kind:        config.Kind,
		Name:        config.Metadata.Name,
		FullName:    config.FullName(),
		APIVersion:  config.Version,
		Created:     config.Metadata.Created,
		Updated:     config.Metadata.Updated,
		Generatable: entry.generatable,
		Stored:      stored,
	}

	if config.Kind == builderBetaKindScenario {
		digest, err := bdoc.ContentDigest(config.Spec)
		if err != nil {
			return builderSourceResponse{}, fmt.Errorf("digesting scenario %s: %w", config.FullName(), err)
		}

		source.Digest = digest
	} else if entry.generatable {
		digest, err := bdoc.SourceDigest(*config)
		if err != nil {
			return builderSourceResponse{}, fmt.Errorf("digesting source %s: %w", config.FullName(), err)
		}

		source.Digest = digest
	}

	switch {
	case config.HasAnnotation(bapi.DocumentAnnotation):
		source.Builder = bapi.DocumentAnnotation
	case config.HasAnnotation(builderBetaXMLAnnotation):
		source.Builder = builderBetaXMLAnnotation
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
func (b *builderBetaAPI) listSources(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaListSources")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "listing builder sources")
	}

	if !builderBetaBaseAllowed(actor.role, builderBetaVerbList) {
		return builderBetaForbidden(actor, "listing builder sources")
	}

	response := make(map[string][]builderSourceResponse, len(builderBetaSourceKinds))

	for _, entry := range builderBetaSourceKinds {
		sources, err := b.sourcesOfKind(actor, entry)
		if err != nil {
			return err
		}

		response[entry.key] = sources
	}

	return builderBetaWriteJSON(w, http.StatusOK, "", response)
}

// sourcesOfKind returns the configs of one kind the caller may see. A caller
// holding no list permission for the kind at all is answered with an empty
// group rather than an error: the other groups are still usable.
func (b *builderBetaAPI) sourcesOfKind(
	actor builderBetaActor,
	entry builderBetaSourceKind,
) ([]builderSourceResponse, error) {
	sources := []builderSourceResponse{}

	if !builderBetaKindAllowed(actor.role, entry.resource) {
		return sources, nil
	}

	configs, err := b.listConfigs(entry.list)
	if err != nil {
		return nil, weberror.NewWebError(err, "unable to list %s configs", entry.kind).
			SetStatus(http.StatusInternalServerError)
	}

	for i := range configs {
		config := &configs[i]

		if !builderBetaBaseAllowed(actor.role, builderBetaVerbList, config.FullName()) {
			continue
		}

		if !builderBetaKindAllowed(actor.role, entry.resource, config.Metadata.Name) {
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

// generateDocument - POST /builder/generate.
//
// Generation is a pure transform: a stored config is read, or an uploaded one
// is parsed, and the resulting document is returned to the caller. Nothing is
// written, so a generated document only becomes durable once the caller stores
// it in a draft.
func (b *builderBetaAPI) generateDocument(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaGenerate")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "generating a builder document")
	}

	// Generation reads configs and writes nothing, so it needs the config read
	// permission. Stored sources are additionally authorized by name below.
	if !builderBetaBaseAllowed(actor.role, builderBetaVerbGet) {
		return builderBetaForbidden(actor, "generating a builder document")
	}

	var request builderGenerateRequest

	if err := builderBetaDecode(w, r, &request); err != nil {
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

	document, warnings, err := bdoc.FromConfig(*config)
	if err != nil {
		if errors.Is(err, bdoc.ErrUnsupportedKind) {
			return weberror.NewWebError(err, "%s configs cannot be opened in the builder", config.Kind).
				SetStatus(http.StatusUnprocessableEntity)
		}

		return weberror.NewWebError(err, "unable to generate a builder document from %s", config.FullName()).
			SetStatus(http.StatusUnprocessableEntity)
	}

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		return builderBetaWebError(err, "unable to encode the generated builder document")
	}

	if warnings == nil {
		warnings = []string{}
	}

	source, err := newBuilderSourceResponse(config, request.Source != "")
	if err != nil {
		return weberror.NewWebError(err, "unable to describe generated source").
			SetStatus(http.StatusInternalServerError)
	}

	return builderBetaWriteJSON(w, http.StatusOK, "", builderGenerateResponse{
		Document: data,
		Warnings: warnings,
		Source:   source,
	})
}

// generationSource returns the config a document is generated from, either read
// from the store or parsed from the uploaded content.
func (b *builderBetaAPI) generationSource(
	actor builderBetaActor,
	request builderGenerateRequest,
) (*store.Config, error) {
	if request.Source != "" {
		return b.storedSource(actor, request.Source)
	}

	return builderBetaUploadedSource(request.Content)
}

// storedSource reads a stored config, authorized by its canonical name.
func (b *builderBetaAPI) storedSource(
	actor builderBetaActor,
	source string,
) (*store.Config, error) {
	name := store.ConfigFullName(source)
	if name == "" {
		return nil, weberror.NewWebError(nil, "source must name a stored config as <kind>/<name>").
			SetStatus(http.StatusBadRequest)
	}

	kind, configName, _ := strings.Cut(name, "/")

	entry, known := builderBetaSourceKindFor(kind)
	if !known || !entry.generatable {
		return nil, weberror.NewWebError(nil, "%s configs cannot be opened in the builder", kind).
			SetStatus(http.StatusUnprocessableEntity)
	}

	if !builderBetaBaseAllowed(actor.role, builderBetaVerbGet, name) {
		return nil, builderBetaForbidden(actor, "generating a builder document from "+name)
	}

	// A config the caller may not see listed may not be generated from either.
	if !builderBetaKindAllowed(actor.role, entry.resource, configName) {
		return nil, builderBetaForbidden(actor, "generating a builder document from "+name)
	}

	config, err := b.getConfig(name)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil, builderBetaNotFound("config", name)
		}

		return nil, weberror.NewWebError(err, "unable to get config %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	return config, nil
}

// builderBetaUploadedSource parses an uploaded config. JSON is tried first and
// YAML second, matching how configs are accepted elsewhere in the API. Nothing
// is persisted.
func builderBetaUploadedSource(content string) (*store.Config, error) {
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
