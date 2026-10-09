package builder

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"phenix/api/config"
	"phenix/store"
	"phenix/types/builder"
	"phenix/util/plog"
)

// TopologyOutcome is what publishing a document as a topology did to the
// Topology config, or on a dry run would do.
type TopologyOutcome string

const (
	// TopologyCreated stored a topology of a name no topology had.
	TopologyCreated TopologyOutcome = "created"
	// TopologyUpdated replaced the spec and the document reference of an
	// existing topology.
	TopologyUpdated TopologyOutcome = "updated"
	// TopologyUnchanged left the topology as it was: it already held the
	// document's publication.
	TopologyUnchanged TopologyOutcome = "unchanged"
)

// PublishRefusal says which check of [Service.PublishTopology] refused a
// document.
type PublishRefusal string

const (
	// PublishRefusedName is a topology name that is not a config name.
	PublishRefusedName PublishRefusal = "name"
	// PublishRefusedPath is a file path a document reference may not hold
	// (see [ValidateDocumentPath]).
	PublishRefusedPath PublishRefusal = "path"
	// PublishRefusedBlocked is a document whose topology is not publishable:
	// an interface without a VLAN, addresses interfaces share, a hostname
	// phenix refuses, or a spec phenix's config validation refuses.
	PublishRefusedBlocked PublishRefusal = "blocked"
	// PublishRefusedIncludes is a topology that defines a hostname one of its
	// included topologies defines too.
	PublishRefusedIncludes PublishRefusal = "includes"
	// PublishRefusedExists is an existing topology the request did not ask to
	// update.
	PublishRefusedExists PublishRefusal = "exists"
	// PublishRefusedChanged is an existing topology an update would take
	// something from: it is no longer what the document it names publishes,
	// or it names none that is stored, and the document was not generated
	// from it as it is now.
	PublishRefusedChanged PublishRefusal = "changed"
)

// PublishRefusedError is why [Service.PublishTopology] did not publish a
// document although nothing failed: something for the caller to fix in the
// document, in the request or in the store. Its text is meant to be shown
// as it is. It matches [ErrInvalid] when the document or the request is at
// fault, and [ErrConflict] when what is stored is.
type PublishRefusedError struct {
	Refusal PublishRefusal
	// Topology is the name of the topology the document would be published
	// as.
	Topology string
	// Message says why, in one sentence without a full stop.
	Message string
	// Problems names each thing to fix, when there are several.
	Problems []string
}

func (e *PublishRefusedError) Error() string {
	if len(e.Problems) == 0 {
		return e.Message
	}

	return e.Message + ":\n  " + strings.Join(e.Problems, "\n  ")
}

// Unwrap lets [errors.Is] match [ErrInvalid] or [ErrConflict].
func (e *PublishRefusedError) Unwrap() error {
	switch e.Refusal {
	case PublishRefusedName, PublishRefusedPath, PublishRefusedBlocked:
		return ErrInvalid
	case PublishRefusedIncludes, PublishRefusedExists, PublishRefusedChanged:
	}

	return ErrConflict
}

// PublishTopologyRequest asks [Service.PublishTopology] to publish a Builder
// document as a Topology config.
type PublishTopologyRequest struct {
	// Document is the document as JSON, as [ParseDocumentText],
	// [LoadDocumentFile] and [EncodeDocument] return it. It is stored as it
	// is: none of its provenance fields is set.
	Document []byte
	// Name is the topology's name. Empty, it is the name the Builder's
	// Publish dialog proposes for the document (see [builder.TopologyName]).
	Name string
	// Actor is recorded as who published the document.
	Actor string
	// Update lets an existing topology of that name be replaced.
	Update bool
	// DryRun makes every check and writes nothing.
	DryRun bool
	// Path is the absolute, clean path of the file the document was read
	// from, when it was read from one.
	Path string
	// RecordPath stores Path as the path of the topology's document
	// reference, replacing one the topology already names.
	RecordPath bool
}

// TopologyPublication is what [Service.PublishTopology] did, or on a dry run
// would do.
type TopologyPublication struct {
	// Name is the topology's name.
	Name    string
	Outcome TopologyOutcome
	// Title is the document's own name.
	Title string
	// Digest is the digest of the document's canonical JSON.
	Digest string
	// Reference is the document reference the topology holds afterwards.
	Reference DocumentReference
	// Document is the stored published document, and nil on a dry run.
	Document *PublishedDocument
	// Config is the topology as it is stored afterwards.
	Config *store.Config
	// Warnings says what the caller should know of a publication that went
	// through: what the projection warns of, what of the document a topology
	// has no place for, and what could not be checked or cleaned up.
	Warnings []string
}

// PublishTopology publishes a Builder document as a Topology config, as the
// Builder's Publish does for a draft in its topology mode, for a caller that
// holds a document and no draft: the phenix CLI. Here, and nowhere else,
// this package stores a config.
//
// Every check is made before anything is written, and a document that fails
// one is refused with a [PublishRefusedError]:
//
//   - the name must be a config name;
//   - the document must project to a topology phenix accepts and that
//     publishing does not block (see
//     [builder.Document.ExportTopologyConfig]); every blocker is named, not
//     only the first;
//   - no hostname of the topology may be defined by a topology it includes
//     too. Included topologies are read from the config store only, and one
//     that cannot be read is a warning;
//   - an existing topology is replaced only when the request asks for an
//     update, and then only when the update takes nothing from it: it is
//     still exactly what the stored document it names publishes, so nothing
//     has changed it since it was published, or this document was generated
//     from it as it is now (its source names the topology and holds its
//     digest), so its author saw every change. That holds for a topology
//     the legacy Builder drew too, while its legacy diagram is the one the
//     document was generated from: the update removes that diagram (see
//     [ReplaceLegacyDiagram]), which a warning says.
//
// A topology that already names this document, is what it publishes, and
// would keep its path is left as it is: publishing the same document twice
// is a success that writes no config.
//
// Then the document is stored, before the config that names it, as the
// Topology config hook requires (see [checkTopologyReference]); then the
// config is created or updated, running the config hooks of this process;
// then the topology's superseded documents are removed. A failed config
// write leaves the stored document, which the next publication to the
// topology or the cleanup at startup removes. The reference written holds
// the document's digest and ID, and the path the topology already named
// unless the request records another.
//
// Nothing here serializes publications: a config has no revision to compare,
// so of two publications of one topology at once, in this process or in
// another one sharing the store, the last config written wins. The scenario
// and the VLAN aliases of the document are not published, which a warning
// says.
func (s *Service) PublishTopology(ctx context.Context, req PublishTopologyRequest) (*TopologyPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("publishing a builder document: %w", err)
	}

	plan, err := s.planTopology(ctx, req)
	if err != nil {
		return nil, err
	}

	if req.DryRun {
		return plan.publication, nil
	}

	publication := plan.publication

	published, err := s.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: publication.Name, Kind: configKindTopology, Actor: req.Actor, Document: plan.data,
		DraftID: "", SnapshotID: "",
	})

	switch {
	case published != nil && errors.Is(err, ErrCleanup):
		// The document is stored, repairing a damaged copy of it; the content
		// it replaces is left to the cleanup at startup.
		publication.Warnings = append(publication.Warnings,
			"The Builder document was stored, but content it replaces could not be removed.")
	case err != nil:
		return nil, err
	}

	publication.Document = published

	switch publication.Outcome {
	case TopologyCreated:
		written, err := config.Create(config.CreateFromConfig(plan.config), config.CreateWithValidation())
		if err != nil {
			return nil, fmt.Errorf("creating topology %s: %w", publication.Name, err)
		}

		publication.Config = written
	case TopologyUpdated:
		if err := config.Update(plan.config.FullName(), plan.config); err != nil {
			return nil, fmt.Errorf("updating topology %s: %w", publication.Name, err)
		}
	case TopologyUnchanged:
	}

	if _, err := s.DeleteSupersededDocuments(ctx, publication.Name, published.ID); err != nil {
		publication.Warnings = append(publication.Warnings,
			"The topology was published, but Builder documents it no longer names could not be removed.")

		plog.Error(plog.TypeSystem, "cleaning superseded builder documents", "topology", publication.Name, "err", err)
	}

	return publication, nil
}

// topologyPlan is a publication every check has passed: what
// [Service.PublishTopology] goes on to write.
type topologyPlan struct {
	// data is the document's canonical JSON.
	data []byte
	// config is the topology config to create or update. For a topology
	// left as it is, it is the stored one.
	config      *store.Config
	publication *TopologyPublication
}

// planTopology makes every check of [Service.PublishTopology] and returns
// what it would write. It writes nothing.
func (s *Service) planTopology(ctx context.Context, req PublishTopologyRequest) (*topologyPlan, error) {
	data, document, err := canonicalDocument(req.Document)
	if err != nil {
		return nil, err
	}

	if err := validateText("actor", req.Actor, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	name, err := publishedName(req, document)
	if err != nil {
		return nil, err
	}

	export, err := publishableTopology(document, name)
	if err != nil {
		return nil, err
	}

	unchecked, err := checkStoredIncludes(name, export.Config.Spec)
	if err != nil {
		return nil, err
	}

	warnings := slices.Concat(export.Warnings, unchecked, unpublishedParts(document, export))

	existing, exists, err := storedTopology(name)
	if err != nil {
		return nil, err
	}

	var (
		digest       = digestOf(data)
		reference    = publishedReference(req, name, digest, existing)
		cfg, outcome = export.Config, TopologyCreated
	)

	if exists {
		outcome, err = s.existingTopologyOutcome(ctx, req, existing, document, data, reference)
		if err != nil {
			return nil, err
		}

		// The stored metadata, with its own copy of the annotations, and the
		// status are kept: an update replaces the spec and the reference.
		cfg.Metadata = existing.Metadata
		cfg.Metadata.Annotations = maps.Clone(existing.Metadata.Annotations)
		cfg.Status = existing.Status
	}

	if reference.Path != "" && reference.Path != req.Path {
		warnings = append(warnings, fmt.Sprintf(
			"Topology %s names the Builder file %s, which was not read or changed. "+
				"Replace that file with the published document to keep it in step.",
			name, reference.Path,
		))
	}

	if outcome == TopologyUnchanged {
		cfg = existing
	} else {
		encoded, err := reference.EncodeReference()
		if err != nil {
			return nil, err
		}

		if cfg.Metadata.Annotations == nil {
			cfg.Metadata.Annotations = store.Annotations{}
		}

		cfg.Metadata.Annotations[DocumentAnnotation] = encoded

		if warning, replaced := ReplaceLegacyDiagram(cfg); replaced {
			warnings = append(warnings, warning)
		}
	}

	return &topologyPlan{
		data:   data,
		config: cfg,
		publication: &TopologyPublication{
			Name: name, Outcome: outcome, Title: document.Metadata.Name, Digest: digest, Reference: reference,
			Document: nil, Config: cfg, Warnings: warnings,
		},
	}, nil
}

// ReplaceLegacyDiagram removes from a topology that is about to be written
// with a Builder document reference the diagram the legacy Builder kept on
// it (see [builder.LegacyXMLAnnotation]): the topology is a Builder topology
// from then on. It reports whether there was one, with the warning that says
// what became of it: this diagram replaces it, or, when it cannot be read
// (see [builder.DecodeLegacy]), so that an import converted nothing of it,
// it was removed. Every other annotation is left as it is.
func ReplaceLegacyDiagram(topology *store.Config) (string, bool) {
	diagram, legacy := topology.Metadata.Annotations[builder.LegacyXMLAnnotation]
	if !legacy {
		return "", false
	}

	delete(topology.Metadata.Annotations, builder.LegacyXMLAnnotation)

	if _, err := builder.DecodeLegacy([]byte(diagram)); err != nil {
		return fmt.Sprintf(
			"The legacy Builder diagram of topology %s could not be read and was removed.", topology.Metadata.Name,
		), true
	}

	return fmt.Sprintf(
		"The legacy Builder diagram of topology %s was replaced by this diagram.", topology.Metadata.Name,
	), true
}

// publishedName returns the name the request publishes the document under:
// the one it gives, or else the one the Publish dialog proposes for the
// document. It refuses a name that is not a config name and, for a request
// that records its path, a path a document reference may not hold.
func publishedName(req PublishTopologyRequest, document *builder.Document) (string, error) {
	name := req.Name
	if name == "" {
		name = builder.TopologyName(document.Metadata.Name)
	}

	if !validTopologyName(name) {
		return "", &PublishRefusedError{
			Refusal: PublishRefusedName, Topology: name, Problems: nil,
			Message: fmt.Sprintf(
				"%q is not a topology name: a name holds letters, digits and the characters _ @ . - only, and at most %d of them",
				name, MaxTargetLength,
			),
		}
	}

	if !req.RecordPath {
		return name, nil
	}

	if err := ValidateDocumentPath(req.Path); err != nil {
		return "", &PublishRefusedError{
			Refusal: PublishRefusedPath, Topology: name, Problems: nil,
			Message: fmt.Sprintf("the path %s cannot be recorded: %s", req.Path, validationReason(err)),
		}
	}

	return name, nil
}

// publishedReference is the document reference a publication of the content
// with this digest stores on the topology name: the digest, the ID of the
// document it stores, and the path the existing topology already names,
// unless the request records the path of its own file.
func publishedReference(req PublishTopologyRequest, name, digest string, existing *store.Config) DocumentReference {
	reference := DocumentReference{Digest: digest, ID: PublishedDocumentID(name, digest), Path: configReference(existing).Path}

	if req.RecordPath {
		reference.Path = req.Path
	}

	return reference
}

// validTopologyName reports whether name can name a Topology config and the
// target of a published document.
func validTopologyName(name string) bool {
	return name != "" && len(name) <= MaxTargetLength && config.NameRegex.MatchString(name)
}

// validationReason is what a [ValidationError] says is wrong, without the
// field it names, and any other error's text.
func validationReason(err error) string {
	var invalid *ValidationError
	if errors.As(err, &invalid) {
		return invalid.Reason
	}

	return err.Error()
}

// publishableTopology projects the document onto the topology config named
// name and refuses one that cannot be published, naming everything that
// blocks it: every check that only publishing makes (see
// [builder.Document.ExportTopologyConfig]), or else what phenix's config
// validation refuses for a reason of its own.
func publishableTopology(document *builder.Document, name string) (*builder.TopologyExport, error) {
	refused := func(problems []string) error {
		return &PublishRefusedError{
			Refusal: PublishRefusedBlocked, Topology: name, Problems: problems,
			Message: fmt.Sprintf("the document cannot be published as topology %s", name),
		}
	}

	export, err := document.ExportTopologyConfig(name)

	var blockers []error

	switch {
	case err == nil:
		blockers = export.PublishBlockers
	case isPublishBlocker(err):
		// A blocker the schema refuses too is returned alone, whatever the
		// other checks of publishing found.
		blockers, err = document.PublishBlockers(name)
		if err != nil {
			return nil, refused(blockerProblems(err))
		}
	default:
		return nil, refused(blockerProblems(err))
	}

	if len(blockers) == 0 {
		return export, nil
	}

	var problems []string

	for _, blocker := range blockers {
		problems = append(problems, blockerProblems(blocker)...)
	}

	return nil, refused(problems)
}

// isPublishBlocker reports whether err is a check only publishing makes:
// interfaces without a VLAN, addresses interfaces share, or hostnames phenix
// refuses.
func isPublishBlocker(err error) bool {
	var (
		vlans     *builder.InterfaceVLANError
		addresses *builder.InterfaceAddressError
		hostnames *builder.NodeHostnameError
	)

	return errors.As(err, &vlans) || errors.As(err, &addresses) || errors.As(err, &hostnames)
}

// blockerProblems names each interface without a VLAN, each address
// interfaces share and each hostname phenix refuses that err reports (see
// [builder.InterfaceVLANError], [builder.InterfaceAddressError] and
// [builder.NodeHostnameError]), or else gives the error's own text.
func blockerProblems(err error) []string {
	var (
		vlans     *builder.InterfaceVLANError
		addresses *builder.InterfaceAddressError
		hostnames *builder.NodeHostnameError
	)

	switch {
	case errors.As(err, &vlans):
		return vlans.Problems
	case errors.As(err, &addresses):
		return addresses.Problems
	case errors.As(err, &hostnames):
		return hostnames.Problems
	}

	return []string{err.Error()}
}

// checkStoredIncludes reads the topologies the spec of the topology name
// includes, and refuses a hostname the topology and one of them both define:
// phenix refuses to merge such a topology into an experiment. It returns a
// warning for each included topology that could not be read.
func checkStoredIncludes(name string, spec map[string]any) ([]string, error) {
	includes, err := builder.CheckIncludes(name, spec, storedTopologyLoader)
	if err != nil {
		return nil, &PublishRefusedError{
			Refusal: PublishRefusedBlocked, Topology: name, Problems: []string{err.Error()},
			Message: fmt.Sprintf("the document cannot be published as topology %s", name),
		}
	}

	if len(includes.Clashes) > 0 {
		problems := make([]string, 0, len(includes.Clashes))

		for _, clash := range includes.Clashes {
			problems = append(problems, fmt.Sprintf(
				"node %s is defined both here and in the included topology %s", clash.Hostname, clash.Include,
			))
		}

		return nil, &PublishRefusedError{
			Refusal: PublishRefusedIncludes, Topology: name, Problems: problems,
			Message: fmt.Sprintf(
				"topology %s cannot be published: phenix rejects duplicate hostnames, "+
					"so rename these nodes here or in the included topologies", name,
			),
		}
	}

	warnings := make([]string, 0, len(includes.Unreadable))

	for _, unreadable := range includes.Unreadable {
		warnings = append(warnings, fmt.Sprintf(
			"Included topology %s was not checked for duplicate hostnames: %v.", unreadable.Name, unreadable.Err,
		))
	}

	return warnings, nil
}

// storedTopologyLoader reads an included topology from the config store
// only, as the Builder does. phenix also accepts a file path there, which is
// never read here.
func storedTopologyLoader(name string) (*store.Config, error) {
	if strings.ContainsAny(name, `/\`) {
		return nil, errors.New("included topologies are read from the config store only, not from files")
	}

	included, exists, err := storedTopology(name)

	switch {
	case err != nil:
		return nil, err
	case !exists:
		return nil, errors.New("no stored topology has that name")
	}

	return included, nil
}

// storedTopology returns the stored Topology config named name, and whether
// there is one.
func storedTopology(name string) (*store.Config, bool, error) {
	existing, err := config.Get(configKindTopology+"/"+name, false)

	switch {
	case err == nil:
		return existing, true, nil
	case errors.Is(err, store.ErrNotExist):
		return nil, false, nil
	}

	return nil, false, fmt.Errorf("reading topology %s: %w", name, err)
}

// unpublishedParts says what of the document a topology has no place for:
// its scenario, and the VLAN aliases of its networks, which only an
// experiment holds.
func unpublishedParts(document *builder.Document, export *builder.TopologyExport) []string {
	var notices []string

	if document.Scenario != nil {
		notices = append(notices, "The document's scenario is not published: only the topology is.")
	}

	switch aliases := len(export.VLANAliases); {
	case aliases == 1:
		notices = append(notices, "The document's VLAN alias is not published: a topology holds none.")
	case aliases > 1:
		notices = append(notices, fmt.Sprintf(
			"The document's %d VLAN aliases are not published: a topology holds none.", aliases,
		))
	}

	return notices
}

// configReference returns the document reference a config holds, or one that
// names nothing for a config that holds none, or one that is not valid.
func configReference(c *store.Config) DocumentReference {
	none := DocumentReference{Digest: "", ID: "", Path: ""}

	if c == nil {
		return none
	}

	value, ok := c.Metadata.Annotations[DocumentAnnotation]
	if !ok {
		return none
	}

	reference, err := DecodeReference(value)
	if err != nil {
		return none
	}

	return reference
}

// existingTopologyOutcome decides what publishing does to a topology that
// exists: nothing, when it already holds this publication; an update, when
// the request asks for one and it takes nothing from the topology; and
// otherwise a refusal. written is the reference the publication would
// store.
func (s *Service) existingTopologyOutcome(
	ctx context.Context,
	req PublishTopologyRequest,
	existing *store.Config,
	document *builder.Document,
	data []byte,
	written DocumentReference,
) (TopologyOutcome, error) {
	name := existing.Metadata.Name

	refused := func(refusal PublishRefusal, format string, args ...any) (TopologyOutcome, error) {
		return "", &PublishRefusedError{
			Refusal: refusal, Topology: name, Message: fmt.Sprintf(format, args...), Problems: nil,
		}
	}

	reference := configReference(existing)

	// The topology is already this document's publication when it names the
	// document and its spec is what the document publishes.
	published := reference.Publishes(name, written.Digest)
	if published {
		holds, err := TopologyHoldsDocument(data, existing)
		if err != nil {
			return "", err
		}

		published = holds
	}

	switch {
	case published && reference.Path == written.Path:
		return TopologyUnchanged, nil
	case !req.Update:
		return refused(PublishRefusedExists, "topology %s already exists", name)
	case published:
		// Only the path it names changes.
		return TopologyUpdated, nil
	}

	stored, found, err := s.referencedDocument(ctx, name, reference)
	if err != nil {
		return "", err
	}

	if found {
		holds, err := TopologyHoldsDocument(stored, existing)

		switch {
		case err != nil:
			return "", err
		case holds:
			return TopologyUpdated, nil
		}
	}

	generated, err := generatedFrom(document, existing)

	switch {
	case err != nil:
		return "", err
	case generated:
		return TopologyUpdated, nil
	case found:
		return refused(PublishRefusedChanged,
			"topology %s was changed after it was published, and replacing it would discard that change", name)
	}

	return refused(PublishRefusedChanged,
		"topology %s was not published from a Builder document that is still stored, "+
			"and this document was not made from the topology as it is now", name)
}

// referencedDocument returns the content of the stored published document
// that reference, read from the topology name, names (see
// [DocumentReference.Names]). It reports false when the reference names no
// such document, and for one that can no longer be read, which vouches for
// nothing.
func (s *Service) referencedDocument(ctx context.Context, name string, reference DocumentReference) ([]byte, bool, error) {
	id := reference.StoredID(name)
	if id == "" {
		return nil, false, nil
	}

	record, data, err := s.GetPublishedDocumentData(ctx, id)

	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrCorrupt), errors.Is(err, ErrInvalid):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	case record.Kind != configKindTopology || record.Target != name || !reference.Names(record):
		return nil, false, nil
	}

	return data, true, nil
}

// generatedFrom reports whether the document was generated from the stored
// topology as it is now: its source names the topology and holds the digest
// the topology has (see [builder.ImportDigest], which covers a legacy
// Builder diagram too), so publishing the document replaces nothing its
// author did not see.
func generatedFrom(document *builder.Document, topology *store.Config) (bool, error) {
	source := document.Source
	if source == nil || source.Kind != builder.SourceKindTopology ||
		source.Name != topology.Metadata.Name || source.Digest == "" {
		return false, nil
	}

	digest, err := builder.ImportDigest(*topology)
	if err != nil {
		return false, fmt.Errorf("digesting topology %s: %w", topology.Metadata.Name, err)
	}

	return digest == source.Digest, nil
}

// TopologyHoldsDocument reports whether a stored topology's spec is exactly
// what the Builder document data publishes as that topology: nothing else
// has written the topology since the document was published to it, or, for a
// document read from a file, the topology is the file's. A document that can
// no longer be decoded, projected or digested vouches for nothing.
func TopologyHoldsDocument(data []byte, topology *store.Config) (bool, error) {
	name := topology.Metadata.Name

	document, err := builder.Decode(data)
	if err != nil {
		return false, nil //nolint:nilerr // an undecodable document cannot vouch for the topology
	}

	published, _, err := document.ToTopologyConfig(name)
	if err != nil {
		return false, nil //nolint:nilerr // nor can one that no longer projects
	}

	want, err := builder.SourceDigest(*published)
	if err != nil {
		return false, nil //nolint:nilerr // nor can one that cannot be digested
	}

	got, err := builder.SourceDigest(*topology)
	if err != nil {
		return false, fmt.Errorf("digesting topology %s: %w", name, err)
	}

	return got == want, nil
}
