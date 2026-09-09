package builder

import (
	"fmt"
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

// DraftMetadata is the persisted state of a draft. The document bytes of every
// snapshot are stored separately as immutable chunks.
type DraftMetadata struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	Title string `json:"title"`
	// SourceToken optionally records the config a draft was imported from, in
	// "<kind>/<name>" form. It is an opaque token to this package.
	SourceToken string    `json:"sourceToken,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
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

	// Revision is the store record revision this metadata was read at. It is
	// never serialized: it is filled in from the record on read and is what
	// callers pass back as the expected revision of a mutation.
	Revision int64 `json:"-"`
}

// Snapshot is a snapshot manifest together with its reassembled, verified
// canonical JSON document bytes.
type Snapshot struct {
	Manifest SnapshotManifest
	Data     []byte
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

	// Revision is the store record revision this document was read at. It is
	// never serialized.
	Revision int64 `json:"-"`
}

// DocumentReference is the compact, self describing pointer a caller stores in
// a topology config's [DocumentAnnotation] annotation. It is deliberately small
// (annotations travel with every config read) and carries enough information to
// fetch and verify the document.
type DocumentReference struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Chunks    int    `json:"chunks"`
	ChunkSize int    `json:"chunkSize"`
	// Schema is the builder document schema URI the document was written with.
	Schema     string `json:"schema"`
	DraftID    string `json:"draftId,omitempty"`
	SnapshotID string `json:"snapshotId,omitempty"`
	CreatedAt  string `json:"createdAt"`
	CreatedBy  string `json:"createdBy,omitempty"`
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

// ETag returns an entity tag for the draft, derived from its store record
// revision. It is stable for as long as the draft is unmodified.
func (d *DraftMetadata) ETag() string {
	return `"` + strconv.FormatInt(d.Revision, 10) + `"`
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

// PublishedAs reports whether the draft is clean for exactly the given
// publication operation. A draft published as a topology only is not clean for
// a topology-and-experiment publication of the same content.
func (d *DraftMetadata) PublishedAs(mode PublishMode, topologyTarget string) bool {
	if d.Dirty() {
		return false
	}

	return d.Publication.Mode == mode && d.Publication.TopologyTarget == topologyTarget
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

	return &clone
}

// Decode strictly decodes the snapshot's document bytes.
func (s *Snapshot) Decode() (*builder.Document, error) {
	doc, err := builder.Decode(s.Data)
	if err != nil {
		return nil, fmt.Errorf("decoding snapshot %s: %w", s.Manifest.ID, err)
	}

	return doc, nil
}

// Reference returns the compact annotation reference for the published
// document.
func (p *PublishedDocument) Reference() DocumentReference {
	return DocumentReference{
		ID:         p.ID,
		Digest:     p.Digest,
		Size:       p.Size,
		Chunks:     len(p.ChunkDigests),
		ChunkSize:  p.ChunkSize,
		Schema:     builder.SchemaURI,
		DraftID:    p.DraftID,
		SnapshotID: p.SnapshotID,
		CreatedAt:  p.CreatedAt.UTC().Format(time.RFC3339Nano),
		CreatedBy:  p.CreatedBy,
	}
}

func (m SnapshotManifest) clone() SnapshotManifest {
	clone := m
	clone.ChunkDigests = append([]string(nil), m.ChunkDigests...)

	return clone
}
