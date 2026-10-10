package builder

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
)

const (
	testOwner = "alice"
	testActor = "alice"
	testPeer  = "bob"
)

// testHarness bundles a service with the fake store backing it.
type testHarness struct {
	service *Service
	store   *memrecord.Store
	now     *atomic.Int64
	ids     *atomic.Int64
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()

	fake := memrecord.New()
	now := new(atomic.Int64)
	ids := new(atomic.Int64)

	options := []Option{
		WithStore(fake),
		WithChunkSize(1024),
		WithClock(func() time.Time { return memrecord.Time(now.Add(1)) }),
		WithIDSource(func() (string, error) { return "id-" + strconv.FormatInt(ids.Add(1), 10), nil }),
	}

	service, err := New(options...)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	// Records are stamped with the service's clock, as a real store stamps them
	// with the same wall clock the service reads.
	fake.Stamp = func() time.Time { return memrecord.Time(now.Load()) }

	return &testHarness{service: service, store: fake, now: now, ids: ids}
}

// testDocument returns a small, decodable document whose content depends on
// name and padding.
func testDocument(t *testing.T, name string, padding int) []byte {
	t.Helper()

	doc := builder.NewDocument(name)

	doc.Nodes = append(doc.Nodes, builder.Node{
		ID:       builder.NoteNodeID(name),
		Kind:     builder.NodeKindNote,
		Position: builder.Position{X: 0, Y: 0},
		Note:     &builder.Note{Text: name + strings.Repeat("x", padding), Color: ""},
	})

	data, err := EncodeDocument(doc)
	if err != nil {
		t.Fatalf("EncodeDocument(%q) returned error: %v", name, err)
	}

	return data
}

// documentProvenance returns the creator, creation time, last editor and last
// edit time an encoded document holds.
func documentProvenance(t *testing.T, data []byte) builder.Provenance {
	t.Helper()

	doc, err := builder.Decode(data)
	if err != nil {
		t.Fatalf("decoding a document returned error: %v", err)
	}

	return doc.Provenance()
}

// stampedDocument returns the canonical encoding of an encoded document with
// its creator, creation time, last editor and last edit time replaced.
func stampedDocument(t *testing.T, data []byte, provenance builder.Provenance) []byte {
	t.Helper()

	doc, err := builder.Decode(data)
	if err != nil {
		t.Fatalf("decoding a document returned error: %v", err)
	}

	doc.SetProvenance(provenance)

	stamped, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("encoding a document returned error: %v", err)
	}

	return stamped
}

// sameContent reports whether two encoded documents differ in nothing but
// the four fields the service sets when it stores a snapshot.
func sameContent(t *testing.T, got, want []byte) bool {
	t.Helper()

	var none builder.Provenance

	return bytes.Equal(stampedDocument(t, got, none), stampedDocument(t, want, none))
}

// stampAt is what a save by actor leaves in a document at the test clock's
// tick: the time the harness clock returned on its tick-th read.
func stampAt(creator string, created int64, actor string, tick int64) builder.Provenance {
	return builder.Provenance{
		CreatedBy: creator,
		CreatedAt: builder.FormatTime(memrecord.Time(created)),
		UpdatedBy: actor,
		UpdatedAt: builder.FormatTime(memrecord.Time(tick)),
	}
}

// randomText returns deterministic, poorly compressible text so tests can
// produce multi-chunk payloads.
func randomText(seed int64, n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	state := uint64(seed)*6364136223846793005 + 1442695040888963407
	out := make([]byte, n)

	for i := range out {
		state = state*6364136223846793005 + 1442695040888963407
		out[i] = alphabet[(state>>33)%uint64(len(alphabet))]
	}

	return string(out)
}

// testRandomDocument returns a document padded with incompressible text.
func testRandomDocument(t *testing.T, name string, padding int) []byte {
	t.Helper()

	doc := builder.NewDocument(name)

	doc.Nodes = append(doc.Nodes, builder.Node{
		ID:       builder.NoteNodeID(name),
		Kind:     builder.NodeKindNote,
		Position: builder.Position{X: 0, Y: 0},
		Note:     &builder.Note{Text: name + randomText(int64(len(name)), padding), Color: ""},
	})

	data, err := EncodeDocument(doc)
	if err != nil {
		t.Fatalf("EncodeDocument(%q) returned error: %v", name, err)
	}

	return data
}

func createTestDraft(t *testing.T, h *testHarness, name string) *DraftMetadata {
	t.Helper()

	meta, err := h.service.CreateDraft(context.Background(), CreateDraftRequest{
		Owner:       testOwner,
		Actor:       testActor,
		Title:       name,
		SourceToken: "Topology/" + name,
		Document:    testDocument(t, name, 0),
		Summary:     "initial",
		ID:          "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	return meta
}

func appendTestSnapshot(t *testing.T, h *testHarness, meta *DraftMetadata, name, actor string) *DraftMetadata {
	t.Helper()

	updated, err := h.service.AppendSnapshot(context.Background(), AppendSnapshotRequest{
		DraftID:          meta.ID,
		Actor:            actor,
		ExpectedRevision: meta.Revision,
		Document:         testDocument(t, name, 0),
		Summary:          name,
	})
	if err != nil {
		t.Fatalf("AppendSnapshot(%q) returned error: %v", name, err)
	}

	return updated
}

// manifestForData builds the manifest and stores the chunks of arbitrary data
// under a scope, bypassing the service so corruption scenarios can be set up.
func storePayload(t *testing.T, h *testHarness, scope string, data []byte) SnapshotManifest {
	t.Helper()

	load, err := buildPayload(data, h.service.chunkSize)
	if err != nil {
		t.Fatalf("buildPayload returned error: %v", err)
	}

	if _, err := h.service.writeChunks(scope, load); err != nil {
		t.Fatalf("writeChunks returned error: %v", err)
	}

	return SnapshotManifest{
		ID:             "manifest",
		Digest:         load.digest,
		Size:           load.size,
		CompressedSize: load.compressedSize,
		ChunkDigests:   load.chunkDigests,
		ChunkSize:      h.service.chunkSize,
		CreatedAt:      memrecord.Time(0),
		CreatedBy:      testActor,
		Summary:        "",
	}
}

func chunkKeysOf(h *testHarness, scope string) []string {
	keys := make([]string, 0)

	for _, key := range h.store.Keys(NamespaceChunks) {
		if strings.HasPrefix(key, scope) {
			keys = append(keys, key)
		}
	}

	return keys
}

func fmtErr(err error) string {
	if err == nil {
		return "<nil>"
	}

	return fmt.Sprintf("%T: %v", err, err)
}
