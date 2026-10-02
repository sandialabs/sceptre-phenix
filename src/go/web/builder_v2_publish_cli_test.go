package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
)

// builderStoredPublication is what a publication left in the phenix store:
// the topology config, and the published document it names with its content.
type builderStoredPublication struct {
	topology *store.Config
	record   *bapi.PublishedDocument
	data     []byte
}

// storedBuilderPublication reads what the phenix store holds of the
// published topology name.
func storedBuilderPublication(t *testing.T, harness *builderV2Harness, name string) builderStoredPublication {
	t.Helper()

	topology, err := config.Get(builderV2KindTopology+"/"+name, false)
	if err != nil {
		t.Fatalf("getting topology %s returned error: %v", name, err)
	}

	reference, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("topology %s holds no valid document reference: %v", name, err)
	}

	record, data, err := harness.service.GetPublishedDocumentData(t.Context(), reference.ID)
	if err != nil {
		t.Fatalf("reading document %s returned error: %v", reference.ID, err)
	}

	return builderStoredPublication{topology: topology, record: record, data: data}
}

// TestBuilderV2PublishMatchesPublishTopology publishes one document through
// the Builder's Publish, from a draft, and through
// [bapi.Service.PublishTopology], which the CLI calls with a file, each into
// a phenix store of its own, and then an edit of the document as an update
// of the topology. The two paths share the projection and the storage calls
// but not the checks around them, so this compares what each stored: the
// same topology config, naming the same document with the same content. Only
// who published it, from which draft, and when differ.
func TestBuilderV2PublishMatchesPublishTopology(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	const (
		name   = "pump-station"
		create = `{"mode":"topology","topology":{"name":"pump-station","action":"create"}}`
		update = `{"mode":"topology","topology":{"name":"pump-station","action":"update"}}`
	)

	example, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "docs", "content", "builder-v2", "examples", "pump-station.builder.json",
	))
	if err != nil {
		t.Fatalf("reading the docs example: %v", err)
	}

	// What the Builder's Publish stored, after the create and after the
	// update.
	var published []builderStoredPublication

	t.Run("the Builder's Publish", func(t *testing.T) { //nolint:paralleltest // replaces the phenix store
		harness := newBuilderV2StoreHarness(t, nil)

		document, err := bapi.ParseDocument(example)
		if err != nil {
			t.Fatalf("ParseDocument returned error: %v", err)
		}

		draft := createBuilderPublishDraft(t, harness, document)

		response, _ := publishBuilderDraft(t, harness, draft, create, http.StatusOK)
		published = append(published, storedBuilderPublication(t, harness, name))

		draft = editBuilderDraft(t, harness, response.Draft, document, "added-host")

		publishBuilderDraft(t, harness, draft, update, http.StatusOK)
		published = append(published, storedBuilderPublication(t, harness, name))
	})

	if len(published) != 2 || bytes.Equal(published[0].data, published[1].data) {
		t.Fatalf("the Builder's Publish stored %d publications, want two of different documents", len(published))
	}

	t.Run("PublishTopology", func(t *testing.T) { //nolint:paralleltest // replaces the phenix store
		harness := newBuilderV2StoreHarness(t, nil)

		for i, want := range published {
			outcome := bapi.TopologyCreated
			if i > 0 {
				outcome = bapi.TopologyUpdated
			}

			// The document as the draft held it when it was published: the
			// function stores a document as it is given.
			publication, err := harness.service.PublishTopology(t.Context(), bapi.PublishTopologyRequest{
				Document: want.data, Name: name, Actor: "operator", Update: i > 0,
			})
			if err != nil || publication.Outcome != outcome {
				t.Fatalf("PublishTopology = %+v, %v, want the topology %s", publication, err, outcome)
			}

			got := storedBuilderPublication(t, harness, name)

			// Everything of the config but the times the store sets.
			config := func(stored builderStoredPublication) string {
				topology := *stored.topology
				topology.Metadata.Created, topology.Metadata.Updated = "", ""

				encoded, err := json.Marshal(topology)
				if err != nil {
					t.Fatalf("encoding the topology returned error: %v", err)
				}

				return string(encoded)
			}

			if config(got) != config(want) {
				t.Errorf("publication %d: PublishTopology stored\n%s\nthe Builder's Publish stored\n%s", i, config(got), config(want))
			}

			if !bytes.Equal(got.data, want.data) {
				t.Errorf("publication %d: the stored documents differ", i)
			}

			if got.record.ID != want.record.ID || got.record.Digest != want.record.Digest ||
				got.record.Target != want.record.Target || got.record.Kind != want.record.Kind ||
				got.record.Size != want.record.Size || got.record.Schema != want.record.Schema {
				t.Errorf("publication %d: record = %+v, want the content and target of %+v", i, got.record, want.record)
			}

			// What differs: Publish records the user and the draft, and the
			// function the actor it was given and no draft.
			if want.record.CreatedBy != builderV2TestOwner || want.record.DraftID == "" ||
				got.record.CreatedBy != "operator" || got.record.DraftID != "" || got.record.SnapshotID != "" {
				t.Errorf("publication %d: records name %q, draft %q and %q, draft %q", i,
					want.record.CreatedBy, want.record.DraftID, got.record.CreatedBy, got.record.DraftID)
			}
		}
	})
}
