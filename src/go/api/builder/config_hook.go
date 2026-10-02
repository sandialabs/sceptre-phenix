package builder

import (
	"context"
	"fmt"
	"sync"
	"time"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
	"phenix/util/plog"
)

// configKindTopology is the kind of config Builder documents are published to.
const configKindTopology = "Topology"

// The stages of a config hook this package acts on (see [config.ConfigHook]).
const (
	configStageCreate = "create"
	configStageUpdate = "update"
	configStageDelete = "delete"
	configStageRename = "rename"
)

// leftTopologies counts, by topology name, the callers that asked the Topology
// config hook to leave the topology's documents to them (see
// [LeaveTopologyDocuments]).
var leftTopologies = struct { //nolint:gochecknoglobals // read by the config hook, which takes no caller state
	mu    sync.Mutex
	names map[string]int
}{mu: sync.Mutex{}, names: map[string]int{}}

func init() { //nolint:gochecknoinits // config hook
	config.RegisterConfigHook(configKindTopology, topologyConfigHook)
}

// LeaveTopologyDocuments makes the Topology config hook leave the published
// documents of the topology name alone when the topology is deleted, until
// the returned function is called. A caller that deletes the topology and
// then removes its documents itself, to learn of a failure the hook would
// only log, calls it around the delete.
func LeaveTopologyDocuments(name string) func() {
	leftTopologies.mu.Lock()
	defer leftTopologies.mu.Unlock()

	leftTopologies.names[name]++

	return sync.OnceFunc(func() {
		leftTopologies.mu.Lock()
		defer leftTopologies.mu.Unlock()

		if leftTopologies.names[name]--; leftTopologies.names[name] <= 0 {
			delete(leftTopologies.names, name)
		}
	})
}

// topologyDocumentsLeft reports whether a caller removes the documents of the
// topology name itself.
func topologyDocumentsLeft(name string) bool {
	leftTopologies.mu.Lock()
	defer leftTopologies.mu.Unlock()

	return leftTopologies.names[name] > 0
}

// topologyConfigHook keeps a Topology config and its published documents
// together, whatever writes the config. Every phenix process registers it,
// since the phenix binary links this package, and it reads the store the
// process uses.
//
// Once a topology is deleted (DELETE /configs, `phenix config delete`, all
// included) or renamed (an update that changes its name: PUT /configs, `phenix
// config edit`), its documents are removed. A topology about to be stored is
// refused when its document reference does not decode, and loses the part of
// the reference that is another topology's, as a renamed topology's is (see
// [checkTopologyReference]).
func topologyConfigHook(stage string, c *store.Config) error {
	switch stage {
	case configStageCreate, configStageUpdate:
		return checkTopologyReference(c)
	case configStageDelete:
		if !topologyDocumentsLeft(c.Metadata.Name) {
			deleteTopologyDocuments(c)
		}
	case configStageRename:
		deleteTopologyDocuments(c)
	}

	return nil
}

// checkTopologyReference checks the [DocumentAnnotation] of a topology about
// to be stored, and stores it as [DocumentReference.EncodeReference] writes
// it.
//
// A reference that does not decode refuses the write, with an error matching
// [types.ErrValidationFailed] and [ErrInvalid]: every config write passes
// here, validated or not, and a topology stored with such a reference could
// be opened neither in the Builder nor as text.
//
// A reference whose id is not the one its digest derives for this topology
// (see [PublishedDocumentID]) names a document published to a topology of
// another name. A renamed topology carries such a reference, and so does a
// copy stored under a new name. That document is never listed or read
// through this topology, so the id is dropped. The digest is dropped too,
// unless the reference names a file, whose content the digest then pins; a
// reference with nothing left is removed. An id without a digest cannot be
// checked without reading the store, and is left: readers find that it names
// nothing (see [DocumentReference.Names]). A path is never checked against
// the files of this host, which may not be the host that reads it.
func checkTopologyReference(c *store.Config) error {
	value, ok := c.Metadata.Annotations[DocumentAnnotation]
	if !ok {
		return nil
	}

	name := c.Metadata.Name

	ref, err := DecodeReference(value)
	if err != nil {
		return fmt.Errorf("%w: topology %s: %w", types.ErrValidationFailed, name, err)
	}

	if ref.ID != "" && ref.Digest != "" && ref.ID != PublishedDocumentID(name, ref.Digest) {
		plog.Info(
			plog.TypeSystem,
			"dropped a builder document reference that belongs to another topology",
			"topology", name,
			"document", ref.ID,
		)

		ref.ID = ""

		if ref.Path == "" {
			delete(c.Metadata.Annotations, DocumentAnnotation)

			return nil
		}
	}

	encoded, err := ref.EncodeReference()
	if err != nil {
		return fmt.Errorf("topology %s: %w", name, err)
	}

	c.Metadata.Annotations[DocumentAnnotation] = encoded

	return nil
}

// deleteTopologyDocuments removes the published documents of a Topology
// config that no longer exists under its name (see
// [Service.DeleteConfigDocuments] for which). A failure is logged and never
// fails the delete: the config is gone, so its documents are never listed or
// read again, and the startup cleanup removes them. A Builder file the
// config named is never read or removed.
func deleteTopologyDocuments(c *store.Config) {
	deleted := DeletedConfig{Kind: c.Kind, Name: c.Metadata.Name, DocumentID: "", Updated: configUpdated(c)}

	if ref, err := DecodeReference(c.Metadata.Annotations[DocumentAnnotation]); err == nil {
		deleted.DocumentID = ref.StoredID(c.Metadata.Name)
	}

	service, err := New()
	if err == nil {
		_, err = service.DeleteConfigDocuments(context.Background(), deleted)
	}

	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"deleting the published builder documents of a deleted topology",
			"topology", c.Metadata.Name,
			"err", err,
		)
	}
}

// configUpdated returns a config's metadata.updated time, the start of the
// second it was last written in. It is zero when the time cannot be read.
func configUpdated(c *store.Config) time.Time {
	updated, err := time.Parse(time.RFC3339, c.Metadata.Updated)
	if err != nil {
		return time.Time{}
	}

	return updated.Truncate(time.Second)
}
