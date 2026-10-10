package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

const (
	// builderLegacyName names the document of a diagram that came without a
	// topology, when the request gives no name.
	builderLegacyName = "legacy-diagram"

	// builderLegacyShownBytes is the most of a config's name, which is as
	// long as its author made it, that a refusal repeats.
	builderLegacyShownBytes = 64
)

// builderLegacyRequest asks for the conversion of a diagram of the legacy
// Builder. Content is the text of a file. The file is the XML of the
// diagram, or a Topology config (JSON or YAML) that carries the diagram in
// its "builder-xml" annotation. Name names the document of a diagram that
// comes without a topology.
type builderLegacyRequest struct {
	Content string `json:"content"`
	Name    string `json:"name"`
}

// builderLegacyResponse is a converted document with the warnings raised
// while converting it. Source describes the topology the diagram came with,
// and is left out for a diagram that came without one.
type builderLegacyResponse struct {
	Document json.RawMessage        `json:"document"`
	Warnings []string               `json:"warnings"`
	Source   *builderSourceResponse `json:"source,omitempty"`
}

// convertLegacy - POST /builder/legacy.
//
// Like generation, conversion is a pure transform. It reads from the store
// only the topologies that a topology includes, and it writes nothing.
//
// The content is not trusted. It is never logged. A refusal repeats nothing
// of it but the kind and the name of a config, or the name of an XML root
// element. The parser errors, which may quote the content, are not passed
// on.
func (b *builderAPI) convertLegacy(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderConvertLegacy")

	const action = "converting a legacy builder diagram"

	actor, err := builderAuthorize(r, builderVerbGet, action)
	if err != nil {
		return err
	}

	// A Topology config is parsed as POST /configs parses one, which
	// substitutes ${NAME} from the environment of the server (see
	// generationSource). Thus the conversion needs the permission to create
	// configs, whatever the content is.
	if !builderBaseAllowed(actor.role, builderVerbCreate) {
		return builderForbidden(actor, action)
	}

	var request builderLegacyRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	name, err := builderLegacyInput(&request)
	if err != nil {
		return err
	}

	var (
		document *bdoc.Document
		warnings []string
		source   *builderSourceResponse
		kind     = "diagram"
	)

	// A diagram is XML. Anything else is read as a config.
	if strings.HasPrefix(strings.TrimLeft(strings.TrimPrefix(request.Content, "\xef\xbb\xbf"), " \t\r\n"), "<") {
		document, warnings, err = builderConvertDiagram(request.Content, name)
	} else {
		kind = builderSourceTopology
		document, warnings, source, err = b.convertLegacyTopology(actor, request.Content)
	}

	if err != nil {
		return err
	}

	// The conversion leaves the time out, so it converts the same diagram
	// into the same document. The Inspector shows the time with the source.
	document.Source.ImportedAt = time.Now().UTC().Format(time.RFC3339)

	// Encode the document as [bapi.EncodeDocument] encodes one, but check its
	// size first. A diagram can convert into a document far larger than the
	// bound. Such a document is refused without a second validation.
	data, err := bdoc.Encode(document)

	switch {
	case err == nil && int64(len(data)) > bapi.MaxDocumentBytes:
		return weberror.NewWebError(nil, "the converted diagram is larger than %d bytes", bapi.MaxDocumentBytes).
			SetStatus(http.StatusRequestEntityTooLarge)
	case err == nil:
		err = document.Validate()
	}

	if err != nil {
		return builderLegacyRefusal("the legacy diagram could not be converted into a Builder document")
	}

	if warnings == nil {
		warnings = []string{}
	}

	plog.Info(
		plog.TypeAction, "converted legacy builder diagram",
		"user", actor.user, "kind", kind, "warnings", len(warnings),
	)

	return builderWriteJSON(w, http.StatusOK, "", builderLegacyResponse{
		Document: data,
		Warnings: warnings,
		Source:   source,
	})
}

// builderLegacyInput checks the content and the name of a request, and
// returns the name to give the document of a diagram.
func builderLegacyInput(request *builderLegacyRequest) (string, error) {
	if request.Content == "" {
		return "", weberror.NewWebError(nil, "content is required").SetStatus(http.StatusBadRequest)
	}

	if len(request.Content) > bdoc.MaxLegacyBytes {
		return "", weberror.NewWebError(nil, "the legacy diagram is larger than %d bytes", bdoc.MaxLegacyBytes).
			SetStatus(http.StatusRequestEntityTooLarge)
	}

	name := strings.TrimSpace(request.Name)
	control := func(r rune) bool { return r < ' ' || r == '\x7f' }

	if len(name) > bdoc.MaxNameBytes || strings.ContainsFunc(name, control) {
		return "", weberror.NewWebError(
			nil, "name must be at most %d bytes and must not contain control characters", bdoc.MaxNameBytes,
		).SetStatus(http.StatusBadRequest)
	}

	if name == "" {
		name = builderLegacyName
	}

	return name, nil
}

// builderLegacyShown is the name of an uploaded config as a refusal repeats
// it: without control characters, and cut to builderLegacyShownBytes.
func builderLegacyShown(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < ' ' || r == '\x7f' {
			return -1
		}

		return r
	}, name)

	if len(name) > builderLegacyShownBytes {
		name = strings.ToValidUTF8(name[:builderLegacyShownBytes], "") + "..."
	}

	return name
}

// builderLegacyRefusal is the 422 of content that cannot be converted. It
// carries no cause.
func builderLegacyRefusal(format string, args ...any) *weberror.WebError {
	return weberror.NewWebError(nil, format, args...).SetStatus(http.StatusUnprocessableEntity)
}

// builderLegacyNotDiagram is the refusal of content that is neither a
// diagram nor a config with a name and a known kind. It uses the words that
// [bdoc.DecodeLegacy] uses to refuse content that is not XML.
func builderLegacyNotDiagram() *weberror.WebError {
	return builderLegacyRefusal(
		"this is not a legacy Builder diagram: expected mxGraph XML, or a Topology config with the %s annotation",
		bdoc.LegacyXMLAnnotation,
	)
}

// builderConvertDiagram converts a diagram that comes without a topology:
// the settings the diagram holds for its nodes are all there is.
func builderConvertDiagram(content, name string) (*bdoc.Document, []string, error) {
	diagram, err := bdoc.DecodeLegacy([]byte(content))
	if err != nil {
		var refused *bdoc.LegacyError
		if !errors.As(err, &refused) {
			return nil, nil, builderLegacyRefusal("the legacy diagram could not be read")
		}

		status := http.StatusUnprocessableEntity
		if refused.Reason == bdoc.LegacyTooLarge {
			status = http.StatusRequestEntityTooLarge
		}

		return nil, nil, builderLegacyRefusal("%s", refused.Message).SetStatus(status)
	}

	document, warnings, err := bdoc.FromLegacy(diagram, name)
	if err != nil {
		return nil, nil, builderLegacyRefusal("the legacy diagram could not be converted into a Builder document")
	}

	return document, warnings, nil
}

// convertLegacyTopology converts the diagram a Topology config carries. The
// config is parsed as an uploaded source is, and its spec is the truth for
// every node. A Builder document reference beside the diagram does not
// matter here: the caller asked for the legacy diagram.
func (b *builderAPI) convertLegacyTopology(
	actor builderActor,
	content string,
) (*bdoc.Document, []string, *builderSourceResponse, error) {
	config, err := builderUploadedSource(content)
	if err != nil || config.Metadata.Name == "" {
		return nil, nil, nil, builderLegacyNotDiagram()
	}

	// The kind as phenix writes it, whatever case the file has it in.
	config.Kind, _, _ = strings.Cut(store.ConfigFullName(config.Kind, config.Metadata.Name), "/")

	name := builderLegacyShown(config.Metadata.Name)

	switch {
	case config.Kind != builderKindTopology:
		return nil, nil, nil, builderLegacyRefusal(
			"only a Topology config can hold a legacy Builder diagram, not a %s config", config.Kind,
		)
	case !bdoc.HasLegacyDiagram(*config):
		return nil, nil, nil, builderLegacyRefusal(
			"topology %s has no legacy Builder diagram (no %s annotation); use Import to make a diagram from it",
			name, bdoc.LegacyXMLAnnotation,
		)
	}

	document, warnings, err := bdoc.FromLegacyTopology(*config, bdoc.WithTopologyLoader(b.includedTopologyLoader(actor)))
	if err != nil {
		return nil, nil, nil, builderLegacyRefusal(
			"unable to import a builder document from %s/%s", builderKindTopology, name,
		)
	}

	source, err := newBuilderSourceResponse(config, false)
	if err != nil {
		return nil, nil, nil, weberror.NewWebError(nil, "unable to describe the converted source").
			SetStatus(http.StatusInternalServerError)
	}

	// The diagram was converted, whatever else the config carries.
	source.Builder = bdoc.LegacyXMLAnnotation

	return document, warnings, &source, nil
}
