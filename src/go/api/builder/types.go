package builder

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"phenix/types/builder"
)

// PublishMode records which publication operation a draft was published with.
type PublishMode string

const (
	// PublishModeTopology published the draft as a topology only.
	PublishModeTopology PublishMode = "topology"
	// PublishModeTopologyExperiment published the draft as a topology and
	// created an experiment from it.
	PublishModeTopologyExperiment PublishMode = "topology-experiment"
)

// Valid reports whether the publish mode is one this package understands.
func (m PublishMode) Valid() bool {
	switch m {
	case PublishModeTopology, PublishModeTopologyExperiment:
		return true
	}

	return false
}

// PublishStatus is the outcome a publication reports, overall and for each of
// its stages.
type PublishStatus string

const (
	// PublishSucceeded reports that every requested stage succeeded.
	PublishSucceeded PublishStatus = "succeeded"
	// PublishPartial reports that some stages succeeded before one failed.
	PublishPartial PublishStatus = "partial"
	// PublishFailed reports a failed stage or publication.
	PublishFailed PublishStatus = "failed"
	// PublishSkipped reports a stage that did not run.
	PublishSkipped PublishStatus = "skipped"
)

// TopologyAction records whether publishing created or updated the topology
// config named by [PublicationState.TopologyTarget].
type TopologyAction string

const (
	// TopologyActionCreate created a new topology config.
	TopologyActionCreate TopologyAction = "create"
	// TopologyActionUpdate replaced the spec of an existing topology config.
	TopologyActionUpdate TopologyAction = "update"
)

// Valid reports whether the topology action is one this package understands.
func (a TopologyAction) Valid() bool {
	switch a {
	case TopologyActionCreate, TopologyActionUpdate:
		return true
	}

	return false
}

// SnapshotManifest describes one immutable snapshot of a draft document. It
// records everything needed to reassemble and verify the document bytes, but
// never the bytes themselves: those live in content chunks.
type SnapshotManifest struct {
	ID string `json:"id"`
	// Digest is the "sha256:<hex>" digest of the canonical JSON document bytes.
	Digest string `json:"digest"`
	// Size is the length of the canonical JSON document bytes.
	Size int64 `json:"size"`
	// CompressedSize is the length of the gzip compressed payload the chunks
	// hold.
	CompressedSize int64 `json:"compressedSize"`
	// ChunkDigests holds the "sha256:<hex>" digest of every chunk, in order. The
	// order is authoritative: reassembly reads exactly this many chunks and
	// verifies each one against its digest.
	ChunkDigests []string `json:"chunkDigests"`
	// ChunkSize is the chunk size used when the snapshot was written.
	ChunkSize int       `json:"chunkSize"`
	CreatedAt time.Time `json:"createdAt"`
	// CreatedBy is the actor that created the snapshot. It may differ from the
	// draft owner; cross-user edits are recorded, not rejected.
	CreatedBy string `json:"createdBy"`
	Summary   string `json:"summary,omitempty"`
	// OpID is the client's id for the save that stored the snapshot, so a
	// client that never saw the response can tell the snapshot was stored.
	OpID string `json:"opId,omitempty"`
}

// PublicationState records the last publication of a draft. It is set by
// [Service.MarkPublished] after the caller has published a config, and is used
// to derive whether a draft has unpublished changes.
type PublicationState struct {
	// Mode is the publication operation that was performed.
	Mode PublishMode `json:"mode"`
	// TopologyTarget is the name of the topology config the draft was published
	// to. It is always set.
	TopologyTarget string `json:"topologyTarget"`
	// TopologyAction records whether the topology config was created or
	// updated.
	TopologyAction TopologyAction `json:"topologyAction"`
	// ExperimentTarget is the name of the experiment created from the topology.
	// It is set only for [PublishModeTopologyExperiment].
	ExperimentTarget string `json:"experimentTarget,omitempty"`
	// ScenarioTarget is the name of the scenario the experiment was created
	// with, when one was used.
	ScenarioTarget string `json:"scenarioTarget,omitempty"`
	// SnapshotID is the snapshot that was published.
	SnapshotID string `json:"snapshotId"`
	// Digest is the document digest of the published snapshot.
	Digest string `json:"digest"`
	// Revision is the draft record revision observed when publishing. It is
	// audit information: cleanliness is derived from the snapshot, which is
	// immutable, not from the revision.
	Revision int64 `json:"revision"`
	// DocumentID optionally links the publication to an immutable published
	// document stored by [Service.PutPublishedDocument].
	DocumentID  string    `json:"documentId,omitempty"`
	PublishedAt time.Time `json:"publishedAt"`
	PublishedBy string    `json:"publishedBy"`
}

// ForkedPublication records the last publication of a draft another draft
// forks, as it was when the fork was made.
type ForkedPublication struct {
	// DocumentID is the published document of that publication.
	DocumentID string `json:"documentId"`
	// TopologyTarget and ExperimentTarget are the configs it published.
	TopologyTarget   string `json:"topologyTarget"`
	ExperimentTarget string `json:"experimentTarget,omitempty"`
}

// DraftMetadata is the persisted state of a draft. The document bytes of every
// snapshot are stored separately as immutable chunks.
type DraftMetadata struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	Title string `json:"title"`
	// SourceToken optionally records where a draft came from: the config it
	// was imported from as "<kind>/<name>", an uploaded config as
	// "uploaded/<kind>/<name>", or the published document it was opened from
	// as "builder-doc/<document id>". It is an opaque token to this package.
	SourceToken string `json:"sourceToken,omitempty"`
	// SourceFile optionally records the name of the uploaded file the draft
	// was made from: a base name, kept only to show it. Nothing is ever
	// opened by it.
	SourceFile string    `json:"sourceFile,omitempty"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
	// LastModifiedBy is the actor of the most recent mutation, which may differ
	// from Owner.
	LastModifiedBy string `json:"lastModifiedBy"`
	// History holds the snapshot manifests oldest first. Pruned snapshots are
	// removed from the front.
	History []SnapshotManifest `json:"history"`
	// Cursor is the index in History of the snapshot currently being edited.
	// Snapshots after the cursor are the redo branch.
	Cursor int `json:"cursor"`
	// Publication is the last publication of this draft, if any.
	Publication *PublicationState `json:"publication,omitempty"`
	// Forked is what the draft this one forks had published when it was
	// forked, if anything. It is an opaque record to this package.
	Forked *ForkedPublication `json:"forked,omitempty"`
	// Sharing is who the owner shared the draft with, once it has ever been
	// shared (see [Service.UpdateShares]). It lives in the draft record, so
	// changing it changes the draft's revision: a save authorized by a share
	// that has since been removed can never land.
	Sharing *SharingState `json:"sharing,omitempty"`
	// DocumentAuthor and DocumentCreatedAt are the author and createdAt the
	// document of every snapshot of this draft carries. They are fixed when
	// the draft is created (see [Service.CreateDraft]), so a save never has
	// to read the previous snapshot to keep them. Either is empty for a draft
	// whose document has none, and both for a draft stored before the
	// fields existed.
	DocumentAuthor    string `json:"documentAuthor,omitempty"`
	DocumentCreatedAt string `json:"documentCreatedAt,omitempty"`

	// Revision is the store record revision this metadata was read at. It is
	// never serialized: it is filled in from the record on read and is what
	// callers pass back as the expected revision of a mutation.
	Revision int64 `json:"-"`
	// Stamp is the author, creation time, last editor and last edit time of
	// the document the call that returned this metadata stored: what
	// [Service.CreateDraft] and [Service.AppendSnapshot] wrote into it, or,
	// for a draft created as an unchanged copy, what it already held. It is
	// never serialized, and nil in metadata any other call returns.
	Stamp *builder.Provenance `json:"-"`
}

// Snapshot is a snapshot manifest together with its reassembled, verified
// canonical JSON document bytes.
type Snapshot struct {
	Manifest SnapshotManifest
	Data     []byte

	// parsed is Data as the service read and validated it, which the first
	// Decode hands over instead of decoding Data again.
	parsed *builder.Document
}

// PublishedDocument is the immutable record of a document a config was
// published from. It is content addressed: the same document published to the
// same target always yields the same ID.
type PublishedDocument struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	// CompressedSize is the length of the gzip compressed payload the chunks
	// hold.
	CompressedSize int64    `json:"compressedSize"`
	ChunkDigests   []string `json:"chunkDigests"`
	ChunkSize      int      `json:"chunkSize"`
	// PayloadID names the private, immutable chunk scope holding this
	// document's content. It is generated by the attempt that stored the
	// document, so concurrent attempts at the same content addressed ID never
	// share chunks and can never delete each other's.
	PayloadID string `json:"payloadId"`
	// Target and Kind identify the config this document was published to.
	Target string `json:"target"`
	Kind   string `json:"kind"`
	// DraftID and SnapshotID optionally link back to the draft the document was
	// published from. Published documents remain valid after the draft is gone.
	DraftID    string    `json:"draftId,omitempty"`
	SnapshotID string    `json:"snapshotId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	CreatedBy  string    `json:"createdBy"`
	// Schema is the Builder document schema URI the document was written
	// with. It is empty on a record stored before the field existed.
	Schema string `json:"schema"`

	// Revision is the store record revision this document was read at. It is
	// never serialized.
	Revision int64 `json:"-"`
	// published is when the document was last stored or published again (see
	// [Service.PutPublishedDocument]), from its store record. It is never
	// serialized.
	published time.Time
}

// DocumentReference is what a topology config's [DocumentAnnotation]
// annotation holds: the Builder document the topology was made from. Each
// field is optional, and a reference names at least one. A config's JSON and
// YAML show it as a map of these sub-keys (see phenix/store.Annotations).
type DocumentReference struct {
	// Digest is the digest of the document's canonical JSON, "sha256:" and 64
	// hex digits. It pins the content wherever the content is read from, and
	// finds the stored document when ID is empty (see
	// [DocumentReference.StoredID]).
	Digest string `json:"digest,omitempty"`
	// ID names a stored published document. It is this topology's only when
	// that document was published to this topology.
	ID string `json:"id,omitempty"`
	// Path names a Builder file on the phenix server, used when no stored
	// document is found (see [ValidateDocumentPath] and [ReadDocumentFile]).
	Path string `json:"path,omitempty"`
}

// hasSnapshot reports whether the draft still holds a snapshot.
func (d *DraftMetadata) hasSnapshot(snapshotID string) bool {
	for i := range d.History {
		if d.History[i].ID == snapshotID {
			return true
		}
	}

	return false
}

// RevisionETag is the entity tag of a draft at revision rev.
func RevisionETag(rev int64) string {
	return `"` + strconv.FormatInt(rev, 10) + `"`
}

// ETag returns an entity tag for the draft, derived from its store record
// revision. It is stable for as long as the draft is unmodified.
func (d *DraftMetadata) ETag() string {
	return RevisionETag(d.Revision)
}

// Current returns the snapshot manifest the cursor points at, or nil when the
// draft has no history (which only happens for corrupt metadata).
func (d *DraftMetadata) Current() *SnapshotManifest {
	if d.Cursor < 0 || d.Cursor >= len(d.History) {
		return nil
	}

	return &d.History[d.Cursor]
}

// Snapshot returns the manifest with the given ID, or nil.
func (d *DraftMetadata) Snapshot(id string) *SnapshotManifest {
	for i := range d.History {
		if d.History[i].ID == id {
			return &d.History[i]
		}
	}

	return nil
}

// HistoryBytes returns the total uncompressed size of the retained snapshots.
func (d *DraftMetadata) HistoryBytes() int64 {
	var total int64

	for i := range d.History {
		total += d.History[i].Size
	}

	return total
}

// Dirty reports whether the draft has changes that have not been published. A
// draft is clean only when its last publication named exactly the snapshot the
// cursor currently points at, with a matching document digest; any edit, undo,
// or redo makes it dirty again.
func (d *DraftMetadata) Dirty() bool {
	current := d.Current()
	if current == nil {
		return true
	}

	if d.Publication == nil {
		return true
	}

	return d.Publication.SnapshotID != current.ID || d.Publication.Digest != current.Digest
}

// CanUndo reports whether the cursor can move back.
func (d *DraftMetadata) CanUndo() bool {
	return d.Cursor > 0
}

// CanRedo reports whether the cursor can move forward.
func (d *DraftMetadata) CanRedo() bool {
	return d.Cursor < len(d.History)-1
}

// Clone returns a deep copy of the metadata.
func (d *DraftMetadata) Clone() *DraftMetadata {
	clone := *d

	clone.History = make([]SnapshotManifest, len(d.History))
	for i := range d.History {
		clone.History[i] = d.History[i].clone()
	}

	if d.Publication != nil {
		publication := *d.Publication
		clone.Publication = &publication
	}

	if d.Forked != nil {
		forked := *d.Forked
		clone.Forked = &forked
	}

	if d.Sharing != nil {
		sharing := *d.Sharing
		sharing.Entries = slices.Clone(d.Sharing.Entries)
		clone.Sharing = &sharing
	}

	if d.Stamp != nil {
		stamp := *d.Stamp
		clone.Stamp = &stamp
	}

	return &clone
}

// Decode strictly decodes the snapshot's document bytes.
func (s *Snapshot) Decode() (*builder.Document, error) {
	if parsed := s.parsed; parsed != nil {
		// Handed over once, so each caller still gets a document of its own.
		s.parsed = nil

		return parsed, nil
	}

	doc, err := builder.Decode(s.Data)
	if err != nil {
		return nil, fmt.Errorf("decoding snapshot %s: %w", s.Manifest.ID, err)
	}

	return doc, nil
}

// Reference returns the reference a topology holds for the published
// document: its digest and its ID.
func (p *PublishedDocument) Reference() DocumentReference {
	return DocumentReference{Digest: p.Digest, ID: p.ID, Path: ""}
}

func (m SnapshotManifest) clone() SnapshotManifest {
	clone := m
	clone.ChunkDigests = append([]string(nil), m.ChunkDigests...)

	return clone
}
