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
// together, whatever writes the config. It removes the documents of a deleted
// or renamed topology, and checks the reference of a topology about to be
// stored (see [checkTopologyReference]).
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

// checkTopologyReference refuses a topology whose [DocumentAnnotation] does not
// decode. Every config write passes here, validated or not. It drops an id whose
// digest names a document of another topology name (a rename or a copy), and the
// digest unless a path needs it. It keeps an id without a digest, and [DocumentReference.Names]
// finds that it names nothing. It never checks a path: another host may read it.
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

// deleteTopologyDocuments removes the published documents of a Topology config
// that no longer exists under its name. It logs a failure and never fails the
// delete, because the next cleanup removes the documents.
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
