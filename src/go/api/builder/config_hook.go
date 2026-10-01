package builder

import (
	"context"
	"sync"
	"time"

	"phenix/api/config"
	"phenix/store"
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
// process uses, so it runs with or without the builder-v2 feature.
//
// Once a topology is deleted (DELETE /configs, `phenix config delete`, all
// included) or renamed (an update that changes its name: PUT /configs, `phenix
// config edit`), its documents are removed. A topology about to be stored
// loses a document reference that is another topology's, as a renamed
// topology's is.
func topologyConfigHook(stage string, c *store.Config) error {
	switch stage {
	case configStageCreate, configStageUpdate:
		dropOtherTopologyDocument(c)
	case configStageDelete:
		if !topologyDocumentsLeft(c.Metadata.Name) {
			deleteTopologyDocuments(c)
		}
	case configStageRename:
		deleteTopologyDocuments(c)
	}

	return nil
}

// dropOtherTopologyDocument removes a topology's [DocumentAnnotation] when
// the document it names was published to a topology of another name: a
// document's ID is made from the name it was published to (see
// [PublishedDocumentID]). A renamed topology carries such a reference, and so
// does a copy stored under a new name. That document is never listed or read
// through this topology, and while a topology names it, the startup cleanup
// keeps it. A reference that cannot be decoded is left for its readers to
// report.
func dropOtherTopologyDocument(c *store.Config) {
	value, ok := c.Metadata.Annotations[DocumentAnnotation]
	if !ok {
		return
	}

	ref, err := DecodeReference(value)
	if err != nil || ref.ID == PublishedDocumentID(c.Metadata.Name, ref.Digest) {
		return
	}

	delete(c.Metadata.Annotations, DocumentAnnotation)

	plog.Info(
		plog.TypeSystem,
		"dropped a builder document reference that belongs to another topology",
		"topology", c.Metadata.Name,
		"document", ref.ID,
	)
}

// deleteTopologyDocuments removes the published documents of a Topology
// config that no longer exists under its name (see
// [Service.DeleteConfigDocuments] for which). A failure is logged and never
// fails the delete: the config is gone, so its documents are never listed or
// read again, and the startup cleanup removes them.
func deleteTopologyDocuments(c *store.Config) {
	deleted := DeletedConfig{Kind: c.Kind, Name: c.Metadata.Name, DocumentID: "", Updated: configUpdated(c)}

	if ref, err := DecodeReference(c.Metadata.Annotations[DocumentAnnotation]); err == nil {
		deleted.DocumentID = ref.ID
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
