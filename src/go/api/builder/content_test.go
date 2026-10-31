package builder

import (
	"bytes"
	"compress/gzip"
	"errors"
	"strings"
	"testing"
)

func TestBuildPayloadCompressesAndDigests(t *testing.T) {
	data := []byte(randomText(7, 5000))

	load, err := buildPayload(data)
	if err != nil {
		t.Fatalf("buildPayload returned error: %v", err)
	}

	if want := digestOf(data); load.digest != want {
		t.Errorf("buildPayload(data).digest = %q, want the uncompressed document digest %q", load.digest, want)
	}

	if load.size != int64(len(data)) {
		t.Errorf("buildPayload(data).size = %d, want %d", load.size, len(data))
	}

	got, err := decompress("snapshot", "id", load.compressed, load.size)
	if err != nil {
		t.Fatalf("decompress(buildPayload(data)) returned error: %v", err)
	}

	if !bytes.Equal(got, data) {
		t.Error("decompress(buildPayload(data)) differs from data")
	}
}

func TestBuildPayloadRejectsOversizedDocuments(t *testing.T) {
	if _, err := buildPayload(bytes.Repeat([]byte("a"), MaxDocumentBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("buildPayload(%d bytes) error = %s, want ErrTooLarge", MaxDocumentBytes+1, fmtErr(err))
	}
}

func TestContentKeyLayout(t *testing.T) {
	tests := []struct {
		key   string
		draft string
		ok    bool
	}{
		{key: snapshotKey("draft-1", "snap-1"), draft: "draft-1", ok: true},
		{key: publishedKey("doc-1"), draft: "", ok: true},
		{key: "drafts/draft-1/snap-1/000003", draft: "", ok: false},
		{key: "drafts//snap-1", draft: "", ok: false},
		{key: "published/doc-1/payload-1", draft: "", ok: false},
		{key: "other/thing", draft: "", ok: false},
	}

	for _, tt := range tests {
		if draft, ok := contentDraftOf(tt.key); draft != tt.draft || ok != tt.ok {
			t.Errorf("contentDraftOf(%q) = %q, %t, want %q, %t", tt.key, draft, ok, tt.draft, tt.ok)
		}
	}

	if key := snapshotKey("draft-1", "snap-1"); key != "drafts/draft-1/snap-1" {
		t.Errorf("snapshotKey(draft-1, snap-1) = %q, want drafts/draft-1/snap-1", key)
	}

	if key := publishedKey("doc-1"); key != "published/doc-1" {
		t.Errorf("publishedKey(doc-1) = %q, want published/doc-1", key)
	}

	// One prefix holds every snapshot of a draft, and no snapshot of a draft
	// whose ID it starts.
	if prefix := draftContentPrefix("draft-1"); !strings.HasPrefix(snapshotKey("draft-1", "snap-1"), prefix) ||
		strings.HasPrefix(snapshotKey("draft-10", "snap-1"), prefix) {
		t.Errorf("draftContentPrefix(draft-1) = %q, want the prefix of the snapshots of draft-1 alone", prefix)
	}
}

func TestReadPayloadDetectsMissingContent(t *testing.T) {
	h := newHarness(t)

	key := snapshotKey("draft-1", "snap-1")
	manifest := storePayload(t, h, key, []byte(randomText(1, 4000)))

	h.store.Drop(NamespaceContent, key)

	_, err := readPayload(h.store, "snapshot", manifest.ID, key, manifest)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(missing content) error = %s, want ErrCorrupt", fmtErr(err))
	}

	var corrupt *CorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("readPayload(missing content) error = %s, want a *CorruptError", fmtErr(err))
	}

	if !strings.Contains(corrupt.Reason, "missing") {
		t.Errorf("readPayload(missing content) error reason = %q, want it to mention missing", corrupt.Reason)
	}
}

func TestReadPayloadDetectsOtherContent(t *testing.T) {
	h := newHarness(t)

	key := snapshotKey("draft-1", "snap-1")
	manifest := storePayload(t, h, key, []byte(randomText(2, 4000)))
	other := storePayload(t, h, snapshotKey("draft-1", "snap-2"), []byte(randomText(12, 4000)))

	// Whole content of another document of the same size: only the digest
	// tells them apart.
	record, err := h.store.GetRecord(NamespaceContent, snapshotKey("draft-1", "snap-2"))
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	h.store.SetValue(NamespaceContent, key, record.Value)
	manifest.CompressedSize, manifest.Size = other.CompressedSize, other.Size

	if _, err := readPayload(h.store, "snapshot", manifest.ID, key, manifest); !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(content of another document) error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestReadPayloadDetectsModifiedContent(t *testing.T) {
	h := newHarness(t)

	key := snapshotKey("draft-1", "snap-1")
	manifest := storePayload(t, h, key, []byte(randomText(3, 2000)))

	record, err := h.store.GetRecord(NamespaceContent, key)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	modified := append([]byte(nil), record.Value...)
	modified[10] ^= 0xff

	h.store.SetValue(NamespaceContent, key, modified)

	if _, err := readPayload(h.store, "snapshot", manifest.ID, key, manifest); !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(modified content) error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestReadPayloadRejectsGzipBomb(t *testing.T) {
	h := newHarness(t)

	scope := snapshotKey("draft-1", "snap-1")

	// A payload that decompresses to far more than the manifest claims must be
	// rejected without being fully expanded.
	bomb := bytes.Repeat([]byte("0"), 4<<20)
	manifest := storePayload(t, h, scope, bomb)
	manifest.Size = 128

	_, err := readPayload(h.store, "snapshot", manifest.ID, scope, manifest)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(gzip bomb) error = %s, want ErrCorrupt", fmtErr(err))
	}

	// A manifest claiming more than the document limit is refused outright.
	manifest.Size = MaxDocumentBytes + 1
	if _, err := readPayload(h.store, "snapshot", manifest.ID, scope, manifest); !errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(oversized declared size) error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestReadPayloadRejectsMalformedManifests(t *testing.T) {
	h := newHarness(t)

	scope := snapshotKey("draft-1", "snap-1")
	base := storePayload(t, h, scope, []byte(randomText(4, 1000)))

	tests := []struct {
		name     string
		mutate   func(SnapshotManifest) SnapshotManifest
		contains string
	}{
		{
			name:     "no compressed size",
			mutate:   func(m SnapshotManifest) SnapshotManifest { m.CompressedSize = 0; return m },
			contains: "compressed size of 0",
		},
		{
			name: "compressed size past the limit",
			mutate: func(m SnapshotManifest) SnapshotManifest {
				m.CompressedSize = MaxCompressedBytes + 1

				return m
			},
			contains: "compressed size of",
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

			_, err := readPayload(h.store, "snapshot", manifest.ID, scope, manifest)

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
