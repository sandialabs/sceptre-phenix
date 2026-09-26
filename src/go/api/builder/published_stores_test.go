package builder

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"phenix/store"
	"phenix/store/recordtest"
)

// hookedStore wraps a real record store with the interleavings the fake store
// injects, so settling ambiguous writes and cleanup races can be checked
// against the revisions and timestamps the real stores produce.
type hookedStore struct {
	store.RecordStore

	// createFails, when set and returning a non-nil error, fails a create
	// without applying it; createApplied does the same after applying it.
	createFails   func(namespace string) error
	createApplied func(namespace string) error
	// beforeDeletePrefix runs before a prefix deletion.
	beforeDeletePrefix func(namespace string)
}

func (h *hookedStore) CreateRecord(namespace, key string, value []byte) (store.Record, error) {
	if h.createFails != nil {
		if err := h.createFails(namespace); err != nil {
			return store.Record{}, err
		}
	}

	record, err := h.RecordStore.CreateRecord(namespace, key, value)
	if err == nil && h.createApplied != nil {
		if err := h.createApplied(namespace); err != nil {
			return store.Record{}, err
		}
	}

	return record, err
}

func (h *hookedStore) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	if h.beforeDeletePrefix != nil {
		h.beforeDeletePrefix(namespace)
	}

	return h.RecordStore.DeleteRecordPrefix(namespace, prefix)
}

func TestPublishedDocumentsOnRecordStores(t *testing.T) {
	stores := map[string]func(t *testing.T) store.Store{
		"bolt": func(t *testing.T) store.Store {
			t.Helper()

			s := store.NewBoltDB()
			if err := s.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
				t.Fatalf("initializing BoltDB store: %v", err)
			}

			return s
		},
		"etcd": func(t *testing.T) store.Store {
			t.Helper()

			s := store.NewEtcd()
			if err := s.Init(store.Endpoint(recordtest.StartEtcd(t).Endpoint)); err != nil {
				t.Fatalf("initializing Etcd store: %v", err)
			}

			t.Cleanup(func() { _ = s.Close() })

			return s
		},
	}

	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			testPublishedDocumentsOnRecordStore(t, &hookedStore{RecordStore: open(t)})
		})
	}
}

func testPublishedDocumentsOnRecordStore(t *testing.T, hooked *hookedStore) {
	t.Helper()

	ctx := context.Background()

	// The service reads the wall clock the stores stamp records with, shifted
	// by skew to let time pass.
	var skew atomic.Int64

	service, err := New(
		WithStore(hooked), WithChunkSize(1024),
		WithClock(func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	put := func(target string, data []byte) (*PublishedDocument, error) {
		return service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
			Target: target, Kind: "Topology", Actor: testActor, Document: data, DraftID: "", SnapshotID: "",
		})
	}

	data := testRandomDocument(t, "topo", 2000)

	// A create the store applied but reported as failed is settled as stored.
	hooked.createApplied = func(namespace string) error {
		if namespace == NamespacePublished {
			return errPublishTimeout
		}

		return nil
	}

	doc, err := put("topo", data)
	if err != nil {
		t.Fatalf("PutPublishedDocument error = %s, want the applied document returned", fmtErr(err))
	}

	hooked.createApplied = nil

	if _, _, err := service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
		t.Fatalf("the applied document's content must be kept: %s", fmtErr(err))
	}

	// The chunks of a create that was not applied are kept until they are
	// older than the grace period, by the store's own timestamps.
	hooked.createFails = func(namespace string) error {
		if namespace == NamespacePublished {
			return errPublishTimeout
		}

		return nil
	}

	if _, err := put("other", testRandomDocument(t, "other", 2000)); !errors.Is(err, errPublishTimeout) {
		t.Fatalf("PutPublishedDocument error = %s, want the store error", fmtErr(err))
	}

	hooked.createFails = nil

	if removed, err := service.CleanupOrphanedChunks(ctx); err != nil || removed != 0 {
		t.Fatalf("CleanupOrphanedChunks of recent chunks = %d, %s; want them kept", removed, fmtErr(err))
	}

	skew.Store(int64(2 * OrphanGracePeriod))

	if removed, err := service.CleanupOrphanedChunks(ctx); err != nil || removed == 0 {
		t.Fatalf("CleanupOrphanedChunks of settled chunks = %d, %s; want them removed", removed, fmtErr(err))
	}

	// Deleting a document never removes a copy stored again meanwhile.
	var (
		again    *PublishedDocument
		againErr error
	)

	hooked.beforeDeletePrefix = func(namespace string) {
		if namespace == NamespaceChunks && again == nil {
			again, againErr = put("topo", data)
		}
	}

	if err := service.deletePublishedDocument(ctx, doc.ID); err != nil {
		t.Fatalf("deletePublishedDocument returned error: %s", fmtErr(err))
	}

	hooked.beforeDeletePrefix = nil

	if again == nil || againErr != nil {
		t.Fatalf("republishing during the delete = %+v, %s", again, fmtErr(againErr))
	}

	if _, _, err := service.GetPublishedDocumentData(ctx, again.ID); err != nil {
		t.Fatalf("the copy published during the delete must stay readable: %s", fmtErr(err))
	}
}
