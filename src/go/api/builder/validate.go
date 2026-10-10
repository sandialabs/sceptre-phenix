package builder

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"phenix/types/builder"
	"phenix/util"
)

var (
	// idPattern bounds identifiers this package generates or accepts so they are
	// always safe, unambiguous record key segments.
	idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// canonicalDocument decodes untrusted document bytes strictly, validates the
// document semantically, and re-encodes it canonically. This package hashes,
// chunks, and stores only the canonical encoding. Thus two callers that send
// the same document with different formatting or key order produce the same
// digest, and no invalid document ever reaches the store.
func canonicalDocument(data []byte) ([]byte, *builder.Document, error) {
	doc, err := parseDocument(data)
	if err != nil {
		return nil, nil, err
	}

	canonical, err := encodeDocument(doc)
	if err != nil {
		return nil, nil, err
	}

	return canonical, doc, nil
}

// parseDocument is the first half of [canonicalDocument]. It bounds, decodes
// and validates untrusted document bytes. The document of a draft is stamped
// between the two halves (see [Service.CreateDraft]). Thus what the caller
// sent is validated whole before any of it is replaced.
func parseDocument(data []byte) (*builder.Document, error) {
	if len(data) == 0 {
		return nil, newValidationError("document", "must not be empty")
	}

	if int64(len(data)) > MaxDocumentBytes {
		return nil, newTooLargeError("document", int64(len(data)), MaxDocumentBytes)
	}

	doc, err := builder.Parse(data)
	if err != nil {
		return nil, newValidationCause("document", "is not a valid builder document", err)
	}

	return doc, nil
}

// encodeDocument is the second half of [canonicalDocument]: the canonical
// encoding of a valid document, checked against [MaxDocumentBytes].
func encodeDocument(doc *builder.Document) ([]byte, error) {
	canonical, err := builder.Encode(doc)
	if err != nil {
		return nil, newValidationCause("document", "could not be canonicalized", err)
	}

	if int64(len(canonical)) > MaxDocumentBytes {
		return nil, newTooLargeError("document", int64(len(canonical)), MaxDocumentBytes)
	}

	return canonical, nil
}

// parseStored validates document bytes that were read back from the store.
// Integrity (digests, sizes, chunk order) is checked first. parseStored
// catches content that is intact but no longer a document this package can
// serve.
func parseStored(kind, id string, data []byte) (*builder.Document, error) {
	doc, err := builder.Parse(data)
	if err != nil {
		return nil, newCorruptError(kind, id, "stored document is not a valid builder document: "+err.Error())
	}

	return doc, nil
}

// documentTitle returns the title to record for a document. It prefers the
// name of the document, so a rename in the builder updates draft metadata. It
// validates the title and never truncates it. Thus a caller learns that its
// document name is unusable, and no different title is silently stored.
func documentTitle(doc *builder.Document, fallback string) (string, error) {
	title := fallback
	if doc != nil && doc.Metadata.Name != "" {
		title = doc.Metadata.Name
	}

	if err := validateText("title", title, MaxTitleLength, false); err != nil {
		return "", err
	}

	return title, nil
}

// ValidID reports whether id is a well-formed draft, snapshot or document
// identifier. One that is not can never name a stored record.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

func validateID(field, id string) error {
	if !idPattern.MatchString(id) {
		return newValidationError(field, fmt.Sprintf("must be 1-%d characters of letters, digits, '.', '-', or '_'", MaxIDLength))
	}

	return nil
}

// validateOptionalID accepts an empty identifier.
func validateOptionalID(field, id string) error {
	if id == "" {
		return nil
	}

	return validateID(field, id)
}

// validateText bounds an untrusted string. It rejects text and never silently
// truncates it, so a caller never believes that it stored something it did
// not.
func validateText(field, value string, maxLength int, required bool) error {
	switch {
	case value == "" && required:
		return newValidationError(field, "must not be empty")
	case value == "":
		return nil
	case len(value) > maxLength:
		return newValidationError(field, fmt.Sprintf("must be at most %d bytes", maxLength))
	case !utf8.ValidString(value):
		return newValidationError(field, "must be valid UTF-8")
	}

	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return newValidationError(field, "must not contain control characters")
		}
	}

	return nil
}

// ValidateSourceFile checks the name of the uploaded file a draft was made
// from (see [DraftMetadata.SourceFile]). The name must be a base name, so no
// "/", no "\" and neither "." nor "..". It must be at most
// [MaxSourceFileLength] bytes, with no control characters. An empty name is
// valid: the draft has none.
func ValidateSourceFile(name string) error {
	const field = "sourceFile"

	if err := validateText(field, name, MaxSourceFileLength, false); err != nil {
		return err
	}

	switch {
	case strings.ContainsAny(name, `/\`):
		return newValidationError(field, `must be a file name, without "/" or "\"`)
	case name == "." || name == "..":
		return newValidationError(field, `must be a file name, not "." or ".."`)
	}

	return nil
}

// decodeMetadata strictly decodes a metadata record. Unknown fields and
// trailing content are corruption and are not ignored, so a tampered or
// foreign record is never silently accepted.
func decodeMetadata(kind string, record []byte, out any) error {
	err := util.DecodeJSONStrict(bytes.NewReader(record), out)

	switch {
	case err == nil:
		return nil
	case errors.Is(err, util.ErrTrailingJSON):
		return errors.New(kind + " metadata carries trailing content")
	default:
		return errors.New(kind + " metadata is not valid JSON: " + err.Error())
	}
}

// validateDraftMetadata fully validates a decoded draft record. The record key
// must match the embedded ID, so a record cannot claim to be another draft.
// Every manifest must be well formed and within the limits this package
// enforces on write.
func validateDraftMetadata(key string, meta *DraftMetadata) error {
	switch {
	case meta.ID != key:
		return newCorruptError(kindDraft, key, fmt.Sprintf("metadata claims to be draft %q", meta.ID))
	case validateID("draftID", meta.ID) != nil:
		return newCorruptError(kindDraft, key, "draft ID is not a valid identifier")
	case meta.Owner == "":
		return newCorruptError(kindDraft, key, "metadata has no owner")
	case len(meta.History) == 0:
		return newCorruptError(kindDraft, key, "history is empty")
	case len(meta.History) > maxStoredSnapshots:
		return newCorruptError(
			kindDraft, key,
			fmt.Sprintf("history holds %d snapshots, more than %d", len(meta.History), maxStoredSnapshots),
		)
	case meta.Cursor < 0 || meta.Cursor >= len(meta.History):
		return newCorruptError(kindDraft, key, fmt.Sprintf("cursor %d is outside its history", meta.Cursor))
	case ValidateSourceFile(meta.SourceFile) != nil:
		return newCorruptError(kindDraft, key, "source file is not a usable file name")
	case validateText("documentCreatedBy", meta.DocumentCreatedBy, MaxOwnerLength, false) != nil:
		return newCorruptError(kindDraft, key, "document creator is not a usable user")
	case meta.DocumentCreatedAt != "" && !builder.IsTime(meta.DocumentCreatedAt):
		return newCorruptError(kindDraft, key, "document creation time is not a time a document may hold")
	}

	seen := make(map[string]bool, len(meta.History))

	for i := range meta.History {
		manifest := meta.History[i]

		if err := validateID("snapshotID", manifest.ID); err != nil {
			return newCorruptError(kindDraft, key, fmt.Sprintf("snapshot %d has an invalid identifier", i))
		}

		if seen[manifest.ID] {
			return newCorruptError(kindDraft, key, fmt.Sprintf("snapshot %q appears more than once", manifest.ID))
		}

		seen[manifest.ID] = true

		if err := validateManifestShape(kindDraft, key, manifest); err != nil {
			return err
		}
	}

	if total := meta.HistoryBytes(); total > MaxDraftHistoryBytes {
		return newCorruptError(kindDraft, key, fmt.Sprintf("history holds %d bytes, more than %d", total, MaxDraftHistoryBytes))
	}

	if err := validatePublicationState(key, meta); err != nil {
		return err
	}

	return validateSharingState(key, meta)
}

// validatePublicationState validates recorded publication state against the
// history it refers to. A publication that names a snapshot the draft still
// holds must record the digest of that snapshot. Thus tampered metadata can
// never make a draft look clean at content it does not have. A publication
// that names a snapshot deleted or pruned from the history (see
// [Service.DeleteSnapshot] and [pruneHistory]) leaves the draft dirty, because
// the cursor always points at a snapshot the history holds. The targets it
// records must match the operation its mode describes.
func validatePublicationState(key string, meta *DraftMetadata) error {
	state := meta.Publication
	if state == nil {
		return nil
	}

	switch {
	case !state.Mode.Valid():
		return newCorruptError(kindDraft, key, fmt.Sprintf("publication has unknown mode %q", state.Mode))
	case !state.TopologyAction.Valid():
		return newCorruptError(kindDraft, key, fmt.Sprintf("publication has unknown topology action %q", state.TopologyAction))
	case validateText("topologyTarget", state.TopologyTarget, MaxTargetLength, true) != nil:
		return newCorruptError(kindDraft, key, "publication has no usable topology target")
	case validateText("experimentTarget", state.ExperimentTarget, MaxTargetLength, false) != nil:
		return newCorruptError(kindDraft, key, "publication has an unusable experiment target")
	case validateText("scenarioTarget", state.ScenarioTarget, MaxTargetLength, false) != nil:
		return newCorruptError(kindDraft, key, "publication has an unusable scenario target")
	case validateText("publishedBy", state.PublishedBy, MaxOwnerLength, true) != nil:
		return newCorruptError(kindDraft, key, "publication has no usable actor")
	case state.Mode == PublishModeTopology && (state.ExperimentTarget != "" || state.ScenarioTarget != ""):
		return newCorruptError(kindDraft, key, "a topology publication cannot name experiment or scenario targets")
	case state.Mode == PublishModeTopologyExperiment && state.ExperimentTarget == "":
		return newCorruptError(kindDraft, key, "an experiment publication has no experiment target")
	case validateID("snapshotID", state.SnapshotID) != nil:
		return newCorruptError(kindDraft, key, "publication names an invalid snapshot")
	case !builder.IsDigest(state.Digest):
		return newCorruptError(kindDraft, key, "publication digest is not a sha256 digest")
	case state.DocumentID != "" && validateID("documentID", state.DocumentID) != nil:
		return newCorruptError(kindDraft, key, "publication names an invalid published document")
	}

	if published := meta.Snapshot(state.SnapshotID); published != nil && published.Digest != state.Digest {
		return newCorruptError(
			kindDraft, key,
			fmt.Sprintf("publication digest does not match snapshot %q", state.SnapshotID),
		)
	}

	return nil
}

// validateSharingState validates who a stored draft is shared with. A record
// that fails is damaged, like any other. Shares are never read leniently, so a
// tampered list grants nobody anything.
func validateSharingState(key string, meta *DraftMetadata) error {
	state := meta.Sharing
	if state == nil {
		return nil
	}

	switch {
	case state.Version < 1:
		return newCorruptError(kindDraft, key, fmt.Sprintf("sharing has version %d", state.Version))
	case len(state.Entries) > maxStoredShares:
		return newCorruptError(
			kindDraft, key,
			fmt.Sprintf("sharing holds %d users, more than %d", len(state.Entries), maxStoredShares),
		)
	case validateText("updatedBy", state.UpdatedBy, MaxOwnerLength, true) != nil:
		return newCorruptError(kindDraft, key, "sharing has no usable actor")
	}

	for i := range state.Entries {
		entry := state.Entries[i]

		switch {
		case ValidateShareUser(entry.User) != nil:
			return newCorruptError(kindDraft, key, fmt.Sprintf("share %d names an invalid user", i))
		case entry.User == meta.Owner:
			return newCorruptError(kindDraft, key, fmt.Sprintf("share %d names the owner", i))
		case i > 0 && entry.User <= state.Entries[i-1].User:
			return newCorruptError(kindDraft, key, fmt.Sprintf("share %d is out of order or repeats a user", i))
		case !entry.Access.Valid():
			return newCorruptError(kindDraft, key, fmt.Sprintf("share %d has unknown access %q", i, entry.Access))
		case validateText("userCreated", entry.UserCreated, maxUserCreatedLength, true) != nil:
			return newCorruptError(kindDraft, key, fmt.Sprintf("share %d has no usable account binding", i))
		case validateText("grantedBy", entry.GrantedBy, MaxOwnerLength, true) != nil:
			return newCorruptError(kindDraft, key, fmt.Sprintf("share %d has no usable actor", i))
		}
	}

	return nil
}

// validatePublishedMetadata fully validates a decoded published document
// record. Its key must be the content addressed ID that its own target and
// digest derive. A record stored before records held the document schema has
// no schema. Its content still says which schema it has when it is read.
func validatePublishedMetadata(key string, doc *PublishedDocument) error {
	switch {
	case doc.ID != key:
		return newCorruptError(kindPublished, key, fmt.Sprintf("metadata claims to be document %q", doc.ID))
	case validateID("documentID", doc.ID) != nil:
		return newCorruptError(kindPublished, key, "document ID is not a valid identifier")
	case doc.Target == "":
		return newCorruptError(kindPublished, key, "metadata has no target")
	case doc.Kind == "":
		return newCorruptError(kindPublished, key, "metadata has no kind")
	case doc.ID != PublishedDocumentID(doc.Target, doc.Digest):
		return newCorruptError(kindPublished, key, "document ID does not match its target and digest")
	case validateOptionalID("draftID", doc.DraftID) != nil:
		return newCorruptError(kindPublished, key, "metadata names an invalid draft")
	case validateOptionalID("snapshotID", doc.SnapshotID) != nil:
		return newCorruptError(kindPublished, key, "metadata names an invalid snapshot")
	case validateID("payloadID", doc.PayloadID) != nil:
		return newCorruptError(kindPublished, key, "metadata names an invalid payload")
	case doc.Schema != "" && doc.Schema != builder.SchemaURI:
		return newCorruptError(kindPublished, key, fmt.Sprintf("schema %q is not %q", doc.Schema, builder.SchemaURI))
	}

	return validateManifestShape(kindPublished, key, doc.manifest())
}

// validateReference validates an untrusted document reference read back from a
// config annotation. The reference must name at least one of a digest, an ID
// and a path, and each one it names must have the right shape.
func validateReference(ref DocumentReference) error {
	switch {
	case ref == DocumentReference{Digest: "", ID: "", Path: ""}:
		return newValidationError(DocumentAnnotation, "must name a digest, an id or a path")
	case ref.Digest != "" && !builder.IsDigest(ref.Digest):
		return newValidationError(DocumentAnnotation, "digest is not a sha256 digest")
	case ref.ID != "" && validateID("id", ref.ID) != nil:
		return newValidationError(DocumentAnnotation, "id is not a valid document identifier")
	case ref.Path != "":
		return ValidateDocumentPath(ref.Path)
	}

	return nil
}

// documentPathExtensions are the file name extensions of a Builder file a
// document reference may name.
var documentPathExtensions = []string{".json", ".yaml", ".yml"} //nolint:gochecknoglobals // fixed set

// ValidateDocumentPath checks the syntax of the path of a Builder file that a
// document reference names. It does not touch the file. The path must be:
//   - absolute and already clean (no "." or ".." element, no doubled or
//     trailing slash)
//   - at most [MaxDocumentPathLength] bytes, with no control characters
//   - ending in .json, .yaml or .yml
//
// The extension rule stops a reference from naming the store file, a key file
// or a device. The process that reads the file checks, when it reads the file,
// whether the path is one the phenix server reads files from.
func ValidateDocumentPath(value string) error {
	field := DocumentAnnotation + "." + referencePath

	if err := validateText(field, value, MaxDocumentPathLength, true); err != nil {
		return err
	}

	switch {
	case !path.IsAbs(value):
		return newValidationError(field, "must be an absolute path")
	case path.Clean(value) != value:
		return newValidationError(field, `must be a clean path, with no ".", "..", "//" or trailing "/"`)
	case !slices.Contains(documentPathExtensions, path.Ext(value)):
		return newValidationError(field, "must end in .json, .yaml or .yml")
	}

	return nil
}
