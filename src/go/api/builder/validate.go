package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
	"unicode/utf8"

	"phenix/types/builder"
)

var (
	// idPattern bounds identifiers this package generates or accepts so they are
	// always safe, unambiguous record key segments.
	idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

	// digestPattern bounds the digests stored in manifests and references, so a
	// tampered manifest can never be turned into a differently shaped key.
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// canonicalDocument decodes untrusted document bytes strictly, validates the
// document semantically, and re-encodes it canonically. Everything this package
// hashes, chunks, or stores is the canonical encoding, so two callers sending
// the same document with different formatting or key order produce the same
// digest, and no invalid document ever reaches the store.
func canonicalDocument(field string, data []byte) ([]byte, *builder.Document, error) {
	if len(data) == 0 {
		return nil, nil, newValidationError(field, "must not be empty")
	}

	if int64(len(data)) > MaxDocumentBytes {
		return nil, nil, newTooLargeError("document", int64(len(data)), MaxDocumentBytes)
	}

	doc, err := builder.Parse(data)
	if err != nil {
		return nil, nil, newValidationCause(field, "is not a valid builder document", err)
	}

	canonical, err := builder.Encode(doc)
	if err != nil {
		return nil, nil, newValidationCause(field, "could not be canonicalized", err)
	}

	if int64(len(canonical)) > MaxDocumentBytes {
		return nil, nil, newTooLargeError("document", int64(len(canonical)), MaxDocumentBytes)
	}

	return canonical, doc, nil
}

// parseStored validates document bytes that were read back from the store.
// Integrity (digests, sizes, chunk order) is checked first; this catches
// content that is intact but no longer a document this package can serve.
func parseStored(kind, id string, data []byte) (*builder.Document, error) {
	doc, err := builder.Parse(data)
	if err != nil {
		return nil, newCorruptError(kind, id, "stored document is not a valid builder document: "+err.Error())
	}

	return doc, nil
}

// documentTitle returns the title to record for a document, preferring the
// document's own name so a rename in the builder updates draft metadata. The
// title is validated, never truncated: a caller is told its document name is
// unusable instead of silently storing a different title than it sent.
func documentTitle(doc *builder.Document, fallback string) (string, error) {
	title := fallback
	if doc != nil && doc.Name != "" {
		title = doc.Name
	}

	if err := validateText("title", title, MaxTitleLength, false); err != nil {
		return "", err
	}

	return title, nil
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

// validateText bounds an untrusted string. Text is rejected rather than
// silently truncated so a caller never believes it stored something it did not.
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

// decodeMetadata strictly decodes a metadata record: unknown fields and
// trailing content are treated as corruption rather than ignored, so a
// tampered or foreign record is never silently accepted.
func decodeMetadata(kind string, record []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(record))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(out); err != nil {
		return errors.New(kind + " metadata is not valid JSON: " + err.Error())
	}

	var trailing json.RawMessage

	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New(kind + " metadata carries trailing content")
	}

	return nil
}

// validateDraftMetadata fully validates a decoded draft record. The record key
// must match the embedded ID, so a record cannot claim to be another draft, and
// every manifest must be well formed and within the limits this package
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
	case len(meta.History) > MaxSnapshots:
		return newCorruptError(kindDraft, key, fmt.Sprintf("history holds %d snapshots, more than %d", len(meta.History), MaxSnapshots))
	case meta.Cursor < 0 || meta.Cursor >= len(meta.History):
		return newCorruptError(kindDraft, key, fmt.Sprintf("cursor %d is outside its history", meta.Cursor))
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

	return validatePublicationState(key, meta)
}

// validatePublicationState validates recorded publication state against the
// history it refers to. The publication must name a snapshot the draft still
// holds and record that snapshot's digest, so tampered metadata can never make
// a draft look clean at content it does not have, and the targets it records
// must match the operation its mode describes.
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
	case !digestPattern.MatchString(state.Digest):
		return newCorruptError(kindDraft, key, "publication digest is not a sha256 digest")
	case state.DocumentID != "" && validateID("documentID", state.DocumentID) != nil:
		return newCorruptError(kindDraft, key, "publication names an invalid published document")
	}

	for i := range meta.History {
		if meta.History[i].ID != state.SnapshotID {
			continue
		}

		if meta.History[i].Digest != state.Digest {
			return newCorruptError(
				kindDraft, key,
				fmt.Sprintf("publication digest does not match snapshot %q", state.SnapshotID),
			)
		}

		return nil
	}

	return newCorruptError(kindDraft, key, fmt.Sprintf("publication names snapshot %q, which is not in the history", state.SnapshotID))
}

// validatePublishedMetadata fully validates a decoded published document
// record, including that its key is the content addressed ID its own target and
// digest derive.
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
	}

	return validateManifestShape(kindPublished, key, doc.manifest())
}

// validateReference validates an untrusted document reference read back from a
// config annotation.
func validateReference(ref DocumentReference) error {
	switch {
	case validateID("id", ref.ID) != nil:
		return newValidationError(DocumentAnnotation, "id is not a valid document identifier")
	case !digestPattern.MatchString(ref.Digest):
		return newValidationError(DocumentAnnotation, "digest is not a sha256 digest")
	case ref.Schema != builder.SchemaURI:
		return newValidationError(DocumentAnnotation, fmt.Sprintf("schema %q is not %q", ref.Schema, builder.SchemaURI))
	case ref.Size <= 0 || ref.Size > MaxDocumentBytes:
		return newValidationError(DocumentAnnotation, fmt.Sprintf("size %d is outside 1-%d", ref.Size, int64(MaxDocumentBytes)))
	case ref.Chunks <= 0 || ref.Chunks > MaxChunks:
		return newValidationError(DocumentAnnotation, fmt.Sprintf("chunk count %d is outside 1-%d", ref.Chunks, MaxChunks))
	case ref.ChunkSize <= 0 || ref.ChunkSize > ChunkBytes:
		return newValidationError(DocumentAnnotation, fmt.Sprintf("chunk size %d is outside 1-%d", ref.ChunkSize, ChunkBytes))
	case validateOptionalID("draftId", ref.DraftID) != nil:
		return newValidationError(DocumentAnnotation, "draftId is not a valid identifier")
	case validateOptionalID("snapshotId", ref.SnapshotID) != nil:
		return newValidationError(DocumentAnnotation, "snapshotId is not a valid identifier")
	}

	if ref.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, ref.CreatedAt); err != nil {
			return newValidationCause(DocumentAnnotation, "createdAt is not an RFC 3339 timestamp", err)
		}
	}

	return validateText(DocumentAnnotation+".createdBy", ref.CreatedBy, MaxOwnerLength, false)
}
