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

// LeaveTopologyDocuments makes the Topology config hook keep the published
// documents of the topology name when the topology is deleted, until the
// caller calls the returned function. A caller that deletes the topology and
// then removes its documents itself calls it around the delete. Thus the
// caller learns of a failure that the hook would only log.
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
// because the phenix binary links this package. It reads the store the
// process uses.
//
// When a topology is deleted (DELETE /configs, `phenix config delete`, all
// included) or renamed (an update that changes its name: PUT /configs,
// `phenix config edit`), the hook removes its documents. The hook refuses a
// topology about to be stored when its document reference does not decode.
// It removes the part of the reference that belongs to another topology, as
// in the reference of a renamed topology (see [checkTopologyReference]).
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
// to be stored. It stores the annotation as [DocumentReference.EncodeReference]
// writes it.
//
// A reference that does not decode refuses the write, with an error that
// matches [types.ErrValidationFailed] and [ErrInvalid]. Every config write
// passes here, validated or not. A topology stored with such a reference
// could not be opened in the Builder or as text.
//
// A reference whose id is not the one its digest derives for this topology
// (see [PublishedDocumentID]) names a document published to a topology of
// another name. A renamed topology carries such a reference, and so does a
// copy stored under a new name. Nothing lists or reads that document through
// this topology, so checkTopologyReference drops the id. It also drops the
// digest, unless the reference names a file. Then the digest pins the content
// of that file. It removes a reference with nothing left.
//
// It cannot check an id without a digest unless it reads the store, so it
// keeps that id. Readers find that the id names nothing (see
// [DocumentReference.Names]). It never checks a path against the files of
// this host, which may not be the host that reads it.
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
// [Service.DeleteConfigDocuments] for which documents). It logs a failure and
// never fails the delete. The config is gone, so nothing lists or reads its
// documents again, and the startup cleanup removes them. It never reads or
// removes a Builder file the config named.
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
