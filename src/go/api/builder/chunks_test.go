package builder

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBuildPayloadSplitsAndDigests(t *testing.T) {
	h := newHarness(t)

	data := []byte(randomText(7, 5000))

	load, err := buildPayload(data, h.service.chunkSize)
	if err != nil {
		t.Fatalf("buildPayload returned error: %v", err)
	}

	switch {
	case load.digest != digestOf(data):
		t.Fatal("payload digest must be the digest of the uncompressed document")
	case load.size != int64(len(data)):
		t.Fatalf("payload size = %d, want %d", load.size, len(data))
	case len(load.chunks) != len(load.chunkDigests):
		t.Fatal("every chunk must have a digest")
	case len(load.chunks) < 2:
		t.Fatalf("chunks = %d, want a multi-chunk payload with a %d byte chunk size", len(load.chunks), h.service.chunkSize)
	}

	var total int

	for i, chunk := range load.chunks {
		if i < len(load.chunks)-1 && len(chunk) != h.service.chunkSize {
			t.Fatalf("chunk %d is %d bytes, want %d", i, len(chunk), h.service.chunkSize)
		}

		if digestOf(chunk) != load.chunkDigests[i] {
			t.Fatalf("chunk %d digest does not match its manifest entry", i)
		}

		total += len(chunk)
	}

	if int64(total) != load.compressedSize {
		t.Fatalf("chunk bytes = %d, want the compressed size %d", total, load.compressedSize)
	}
}

func TestBuildPayloadRejectsOversizedDocuments(t *testing.T) {
	if _, err := buildPayload(bytes.Repeat([]byte("a"), MaxDocumentBytes+1), ChunkBytes); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("buildPayload error = %s, want ErrTooLarge", fmtErr(err))
	}
}

func TestChunkKeyLayout(t *testing.T) {
	key := chunkKey(snapshotScope("draft-1", "snap-1"), 3)

	if key != "drafts/draft-1/snap-1/000003" {
		t.Fatalf("chunkKey = %q, want drafts/draft-1/snap-1/000003", key)
	}

	if scope := chunkPayloadScopeOf(key); scope != "drafts/draft-1/snap-1/" {
		t.Fatalf("chunkPayloadScopeOf = %q, want drafts/draft-1/snap-1/", scope)
	}

	if !strings.HasPrefix(key, draftScope("draft-1")) {
		t.Fatalf("chunk key %q must live under the whole draft scope so it can be deleted with one prefix", key)
	}

	// Two snapshots of identical content never share a chunk key, which is what
	// makes it safe for a losing writer to delete its own chunks.
	if other := chunkKey(snapshotScope("draft-1", "snap-2"), 3); other == key {
		t.Fatal("snapshots of the same draft must not share chunk keys")
	}

	if scope := chunkPayloadScopeOf("not/a/chunk/key/at/all"); scope != "" {
		t.Fatalf("chunkPayloadScopeOf of a malformed key = %q, want an empty scope", scope)
	}

	if scope := chunkPayloadScopeOf("drafts//snap/000000"); scope != "" {
		t.Fatalf("chunkPayloadScopeOf of a key with an empty segment = %q, want an empty scope", scope)
	}

	published := chunkKey(publishedPayloadScope("doc-1", "payload-1"), 0)

	if published != "published/doc-1/payload-1/000000" {
		t.Fatalf("published chunk key = %q, want published/doc-1/payload-1/000000", published)
	}

	if !strings.HasPrefix(published, publishedScope("doc-1")) {
		t.Fatalf("published chunk key %q must live under its document scope", published)
	}
}

func TestReadPayloadDetectsMissingChunk(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")
	manifest := storePayload(t, h, scope, []byte(randomText(1, 4000)))

	h.store.drop(NamespaceChunks, chunkKey(scope, 1))

	_, err := h.service.readPayload("snapshot", manifest.ID, scope, manifest)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readPayload error = %s, want ErrCorrupt", fmtErr(err))
	}

	var corrupt *CorruptError
	if !errors.As(err, &corrupt) || !strings.Contains(corrupt.Reason, "missing") {
		t.Fatalf("readPayload error = %s, want a missing chunk reason", fmtErr(err))
	}
}

func TestReadPayloadDetectsReorderedChunks(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")
	manifest := storePayload(t, h, scope, []byte(randomText(2, 4000)))

	first := chunkKey(scope, 0)
	second := chunkKey(scope, 1)

	firstRecord, err := h.store.GetRecord(NamespaceChunks, first)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	secondRecord, err := h.store.GetRecord(NamespaceChunks, second)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	h.store.setValue(NamespaceChunks, first, secondRecord.Value)
	h.store.setValue(NamespaceChunks, second, firstRecord.Value)

	_, err = h.service.readPayload("snapshot", manifest.ID, scope, manifest)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readPayload with swapped chunks error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestReadPayloadDetectsModifiedChunk(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")
	manifest := storePayload(t, h, scope, []byte(randomText(3, 2000)))

	key := chunkKey(scope, 0)

	record, err := h.store.GetRecord(NamespaceChunks, key)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	modified := append([]byte(nil), record.Value...)
	modified[10] ^= 0xff

	h.store.setValue(NamespaceChunks, key, modified)

	if _, err := h.service.readPayload("snapshot", manifest.ID, scope, manifest); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readPayload with a modified chunk error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestReadPayloadRejectsGzipBomb(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")

	// A payload that decompresses to far more than the manifest claims must be
	// rejected without being fully expanded.
	bomb := bytes.Repeat([]byte("0"), 4<<20)
	manifest := storePayload(t, h, scope, bomb)
	manifest.Size = 128

	_, err := h.service.readPayload("snapshot", manifest.ID, scope, manifest)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readPayload of a gzip bomb error = %s, want ErrCorrupt", fmtErr(err))
	}

	// A manifest claiming more than the document limit is refused outright.
	manifest.Size = MaxDocumentBytes + 1
	if _, err := h.service.readPayload("snapshot", manifest.ID, scope, manifest); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readPayload with an oversized declared size error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestReadPayloadRejectsMalformedManifests(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")
	base := storePayload(t, h, scope, []byte(randomText(4, 1000)))

	tests := []struct {
		name     string
		mutate   func(SnapshotManifest) SnapshotManifest
		contains string
	}{
		{
			name:     "no chunks",
			mutate:   func(m SnapshotManifest) SnapshotManifest { m.ChunkDigests = nil; return m },
			contains: "no chunks",
		},
		{
			name: "too many chunks",
			mutate: func(m SnapshotManifest) SnapshotManifest {
				m.ChunkDigests = make([]string, MaxChunks+1)

				return m
			},
			contains: "more than",
		},
		{
			name:     "no digest",
			mutate:   func(m SnapshotManifest) SnapshotManifest { m.Digest = ""; return m },
			contains: "no document digest",
		},
		{
			name: "compressed size mismatch",
			mutate: func(m SnapshotManifest) SnapshotManifest {
				m.CompressedSize++

				return m
			},
			contains: "compressed payload is",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := tt.mutate(base)

			_, err := h.service.readPayload("snapshot", manifest.ID, scope, manifest)

			var corrupt *CorruptError
			if !errors.As(err, &corrupt) {
				t.Fatalf("readPayload error = %s, want *CorruptError", fmtErr(err))
			}

			if !strings.Contains(corrupt.Reason, tt.contains) {
				t.Fatalf("reason = %q, want it to mention %q", corrupt.Reason, tt.contains)
			}
		})
	}
}

func TestDecompressRejectsNonGzipData(t *testing.T) {
	if _, err := decompress("snapshot", "id", []byte("not gzip"), 8); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decompress error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestDecompressRejectsTruncatedStream(t *testing.T) {
	var buf bytes.Buffer

	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write([]byte(randomText(5, 4096))); err != nil {
		t.Fatalf("writing gzip data returned error: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("closing gzip writer returned error: %v", err)
	}

	truncated := buf.Bytes()[:buf.Len()-16]

	if _, err := decompress("snapshot", "id", truncated, 4096); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decompress of a truncated stream error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestCorruptDraftMetadataIsReported(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	h.store.setValue(NamespaceDrafts, meta.ID, []byte("{not json"))

	if _, err := h.service.GetDraft(ctx, meta.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("GetDraft of corrupt metadata error = %s, want ErrCorrupt", fmtErr(err))
	}

	h.store.setValue(NamespaceDrafts, meta.ID, []byte(`{"id":"x","history":[],"cursor":4}`))

	if _, err := h.service.GetDraft(ctx, meta.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("GetDraft with an out of range cursor error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestWriteChunksReusesExistingChunks(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")
	data := []byte(randomText(6, 3000))

	load, err := buildPayload(data, h.service.chunkSize)
	if err != nil {
		t.Fatalf("buildPayload returned error: %v", err)
	}

	created, err := h.service.writeChunks(scope, load)
	if err != nil {
		t.Fatalf("writeChunks returned error: %v", err)
	}

	if len(created) != len(load.chunks) {
		t.Fatalf("created %d chunks, want %d", len(created), len(load.chunks))
	}

	again, err := h.service.writeChunks(scope, load)
	if err != nil {
		t.Fatalf("second writeChunks returned error: %v", err)
	}

	if len(again) != 0 {
		t.Fatalf("second writeChunks created %v, want it to reuse the immutable chunks", again)
	}
}
