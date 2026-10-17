package builder

import (
	"bytes"
	"compress/gzip"
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

	if want := digestOf(data); load.digest != want {
		t.Errorf("buildPayload(data).digest = %q, want the uncompressed document digest %q", load.digest, want)
	}

	if load.size != int64(len(data)) {
		t.Errorf("buildPayload(data).size = %d, want %d", load.size, len(data))
	}

	if len(load.chunks) < 2 {
		t.Errorf("buildPayload(data) made %d chunks, want a multi-chunk payload with a %d byte chunk size",
			len(load.chunks), h.service.chunkSize)
	}

	if len(load.chunks) != len(load.chunkDigests) {
		t.Fatalf("buildPayload(data) made %d chunks and %d chunk digests, want one digest per chunk",
			len(load.chunks), len(load.chunkDigests))
	}

	var total int

	for i, chunk := range load.chunks {
		if i < len(load.chunks)-1 && len(chunk) != h.service.chunkSize {
			t.Errorf("buildPayload(data) chunk %d is %d bytes, want %d", i, len(chunk), h.service.chunkSize)
		}

		if got := digestOf(chunk); got != load.chunkDigests[i] {
			t.Errorf("digest of chunk %d = %q, want its manifest entry %q", i, got, load.chunkDigests[i])
		}

		total += len(chunk)
	}

	if int64(total) != load.compressedSize {
		t.Errorf("buildPayload(data) chunk bytes = %d, want the compressed size %d", total, load.compressedSize)
	}
}

func TestBuildPayloadRejectsOversizedDocuments(t *testing.T) {
	if _, err := buildPayload(bytes.Repeat([]byte("a"), MaxDocumentBytes+1), ChunkBytes); !errors.Is(err, ErrTooLarge) {
		t.Errorf("buildPayload(%d bytes) error = %s, want ErrTooLarge", MaxDocumentBytes+1, fmtErr(err))
	}
}

func TestChunkKeyLayout(t *testing.T) {
	key := chunkKey(snapshotScope("draft-1", "snap-1"), 3)

	if key != "drafts/draft-1/snap-1/000003" {
		t.Errorf("chunkKey(draft-1/snap-1, 3) = %q, want drafts/draft-1/snap-1/000003", key)
	}

	if scope := chunkPayloadScopeOf(key); scope != "drafts/draft-1/snap-1/" {
		t.Errorf("chunkPayloadScopeOf(%q) = %q, want drafts/draft-1/snap-1/", key, scope)
	}

	if scope := draftScope("draft-1"); !strings.HasPrefix(key, scope) {
		t.Errorf("chunkKey(draft-1/snap-1, 3) = %q, want it under the whole draft scope %q so one prefix deletes it", key, scope)
	}

	// Two snapshots of identical content never share a chunk key, which is what
	// makes it safe for a losing writer to delete its own chunks.
	if other := chunkKey(snapshotScope("draft-1", "snap-2"), 3); other == key {
		t.Errorf("chunkKey(draft-1/snap-2, 3) = %q, want it to differ from the key of snap-1", other)
	}

	if scope := chunkPayloadScopeOf("not/a/chunk/key/at/all"); scope != "" {
		t.Errorf("chunkPayloadScopeOf(malformed key) = %q, want an empty scope", scope)
	}

	if scope := chunkPayloadScopeOf("drafts//snap/000000"); scope != "" {
		t.Errorf("chunkPayloadScopeOf(key with an empty segment) = %q, want an empty scope", scope)
	}

	published := chunkKey(publishedPayloadScope("doc-1", "payload-1"), 0)

	if published != "published/doc-1/payload-1/000000" {
		t.Errorf("chunkKey(doc-1/payload-1, 0) = %q, want published/doc-1/payload-1/000000", published)
	}

	if scope := publishedScope("doc-1"); !strings.HasPrefix(published, scope) {
		t.Errorf("chunkKey(doc-1/payload-1, 0) = %q, want it under its document scope %q", published, scope)
	}
}

func TestReadPayloadDetectsMissingChunk(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")
	manifest := storePayload(t, h, scope, []byte(randomText(1, 4000)))

	h.store.Drop(NamespaceChunks, chunkKey(scope, 1))

	_, err := h.service.readPayload("snapshot", manifest.ID, scope, manifest)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(missing chunk) error = %s, want ErrCorrupt", fmtErr(err))
	}

	var corrupt *CorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("readPayload(missing chunk) error = %s, want a *CorruptError", fmtErr(err))
	}

	if !strings.Contains(corrupt.Reason, "missing") {
		t.Errorf("readPayload(missing chunk) error reason = %q, want it to mention missing", corrupt.Reason)
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

	h.store.SetValue(NamespaceChunks, first, secondRecord.Value)
	h.store.SetValue(NamespaceChunks, second, firstRecord.Value)

	_, err = h.service.readPayload("snapshot", manifest.ID, scope, manifest)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(swapped chunks) error = %s, want ErrCorrupt", fmtErr(err))
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

	h.store.SetValue(NamespaceChunks, key, modified)

	if _, err := h.service.readPayload("snapshot", manifest.ID, scope, manifest); !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(modified chunk) error = %s, want ErrCorrupt", fmtErr(err))
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
		t.Errorf("readPayload(gzip bomb) error = %s, want ErrCorrupt", fmtErr(err))
	}

	// A manifest claiming more than the document limit is refused outright.
	manifest.Size = MaxDocumentBytes + 1
	if _, err := h.service.readPayload("snapshot", manifest.ID, scope, manifest); !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(oversized declared size) error = %s, want ErrCorrupt", fmtErr(err))
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
				t.Fatalf("readPayload(%s) error = %s, want a *CorruptError", tt.name, fmtErr(err))
			}

			if !strings.Contains(corrupt.Reason, tt.contains) {
				t.Errorf("readPayload(%s) error reason = %q, want it to mention %q", tt.name, corrupt.Reason, tt.contains)
			}
		})
	}
}

func TestDecompressRejectsNonGzipData(t *testing.T) {
	if _, err := decompress("snapshot", "id", []byte("not gzip"), 8); !errors.Is(err, ErrCorrupt) {
		t.Errorf("decompress(non-gzip data) error = %s, want ErrCorrupt", fmtErr(err))
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
		t.Errorf("decompress(truncated stream) error = %s, want ErrCorrupt", fmtErr(err))
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
		t.Errorf("writeChunks(new payload) created %d chunks, want %d", len(created), len(load.chunks))
	}

	again, err := h.service.writeChunks(scope, load)
	if err != nil {
		t.Fatalf("second writeChunks returned error: %v", err)
	}

	if len(again) != 0 {
		t.Errorf("second writeChunks(same payload) created %v, want none because it reuses the immutable chunks", again)
	}
}
