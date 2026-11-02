package builder

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types/builder"
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
		t.Run(tt.key, func(t *testing.T) {
			if draft, ok := contentDraftOf(tt.key); draft != tt.draft || ok != tt.ok {
				t.Errorf("contentDraftOf(%q) = %q, %t, want %q, %t", tt.key, draft, ok, tt.draft, tt.ok)
			}
		})
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

// TestReadPayloadDetectsDamagedContent damages the content a manifest
// describes: each read is a corruption that says what is wrong.
func TestReadPayloadDetectsDamagedContent(t *testing.T) {
	key := snapshotKey("draft-1", "snap-1")
	otherKey := snapshotKey("draft-1", "snap-2")

	for _, test := range []struct {
		name string
		// damage changes the content stored for manifest, and manifest.
		damage func(t *testing.T, h *testHarness, manifest *SnapshotManifest)
		// reason is text the reason of the corruption holds.
		reason string
	}{
		{
			name:   "missing content",
			damage: func(_ *testing.T, h *testHarness, _ *SnapshotManifest) { h.store.Drop(NamespaceContent, key) },
			reason: "content is missing",
		},
		{
			// Only the digest tells apart the whole content of another
			// document of the same size.
			name: "the content of another document",
			damage: func(t *testing.T, h *testHarness, manifest *SnapshotManifest) {
				t.Helper()

				other := storePayload(t, h, otherKey, []byte(randomText(12, 4000)))

				record, err := h.store.GetRecord(NamespaceContent, otherKey)
				if err != nil {
					t.Fatalf("GetRecord(%s) returned error: %v", otherKey, err)
				}

				h.store.SetValue(NamespaceContent, key, record.Value)
				manifest.CompressedSize, manifest.Size = other.CompressedSize, other.Size
			},
			reason: "document digest is",
		},
		{
			name: "modified content",
			damage: func(t *testing.T, h *testHarness, _ *SnapshotManifest) {
				t.Helper()

				record, err := h.store.GetRecord(NamespaceContent, key)
				if err != nil {
					t.Fatalf("GetRecord(%s) returned error: %v", key, err)
				}

				modified := bytes.Clone(record.Value)
				modified[10] ^= 0xff

				h.store.SetValue(NamespaceContent, key, modified)
			},
			reason: "phenix cannot decompress the payload",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			manifest := storePayload(t, h, key, []byte(randomText(2, 4000)))

			test.damage(t, h, &manifest)

			data, err := readPayload(h.store, "snapshot", manifest.ID, key, manifest)
			if data != nil {
				t.Errorf("readPayload(%s) = %d bytes, want none", test.name, len(data))
			}

			var corrupt *CorruptError
			if !errors.As(err, &corrupt) {
				t.Fatalf("readPayload(%s) error = %s, want a *CorruptError", test.name, fmtErr(err))
			}

			if !strings.Contains(corrupt.Reason, test.reason) {
				t.Errorf("readPayload(%s) error reason = %q, want it to contain %q", test.name, corrupt.Reason, test.reason)
			}
		})
	}
}

// failingContentReader is a record reader whose every read fails.
type failingContentReader struct{ err error }

func (r failingContentReader) GetRecord(string, string) (store.Record, error) {
	return store.Record{}, r.err
}

// TestReadPayloadReturnsAStoreFailure reads content the store cannot read:
// the error is the store's, and not a corruption.
func TestReadPayloadReturnsAStoreFailure(t *testing.T) {
	h := newHarness(t)
	key := snapshotKey("draft-1", "snap-1")
	manifest := storePayload(t, h, key, []byte(randomText(6, 1000)))
	injected := errors.New("injected read failure")

	_, err := readPayload(failingContentReader{err: injected}, "snapshot", manifest.ID, key, manifest)
	if !errors.Is(err, injected) || errors.Is(err, ErrCorrupt) {
		t.Errorf("readPayload(unreadable store) error = %s, want the store's error and no ErrCorrupt", fmtErr(err))
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
			name:     "a negative size",
			mutate:   func(m SnapshotManifest) SnapshotManifest { m.Size = -1; return m },
			contains: "a size of -1",
		},
		{
			name:     "a size past the limit",
			mutate:   func(m SnapshotManifest) SnapshotManifest { m.Size = MaxDocumentBytes + 1; return m },
			contains: "a size of",
		},
		{
			name:     "a digest that is not sha256",
			mutate:   func(m SnapshotManifest) SnapshotManifest { m.Digest = "md5:" + m.Digest[len("sha256:"):]; return m },
			contains: "is not a sha256 digest",
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

func TestDecompressRejects(t *testing.T) {
	load, err := buildPayload([]byte(randomText(5, 4096)))
	if err != nil {
		t.Fatalf("buildPayload returned error: %v", err)
	}

	for _, test := range []struct {
		name       string
		compressed []byte
		size       int64
		reason     string
	}{
		{name: "data that is not gzip", compressed: []byte("not gzip"), size: 8, reason: "not valid gzip"},
		{name: "a truncated stream", compressed: load.compressed[:len(load.compressed)-16], size: 4096, reason: "cannot decompress"},
		{name: "fewer bytes than declared", compressed: load.compressed, size: 4097, reason: "is 4096 bytes, expected 4097"},
		{name: "more bytes than declared", compressed: load.compressed, size: 4095, reason: "is 4096 bytes, expected 4095"},
		{
			name: "a declared size past the limit", compressed: load.compressed, size: MaxDocumentBytes + 1,
			reason: "exceeds the document limit",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := decompress("snapshot", "id", test.compressed, test.size)
			if data != nil {
				t.Errorf("decompress(%s) = %d bytes, want none", test.name, len(data))
			}

			var corrupt *CorruptError
			if !errors.As(err, &corrupt) {
				t.Fatalf("decompress(%s) error = %s, want a *CorruptError", test.name, fmtErr(err))
			}

			if !strings.Contains(corrupt.Reason, test.reason) {
				t.Errorf("decompress(%s) error reason = %q, want it to contain %q", test.name, corrupt.Reason, test.reason)
			}
		})
	}
}

func TestDigestOf(t *testing.T) {
	for _, test := range []struct {
		data []byte
		want string
	}{
		{data: nil, want: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{data: []byte("abc"), want: "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	} {
		t.Run(string(test.data), func(t *testing.T) {
			got := digestOf(test.data)
			if got != test.want {
				t.Errorf("digestOf(%q) = %s, want %s", test.data, got, test.want)
			}

			if !builder.IsDigest(got) {
				t.Errorf("builder.IsDigest(digestOf(%q)) = false, want true", test.data)
			}
		})
	}
}

// TestPutContent writes a content record, replaces it, and returns the error
// of a write the store refuses.
func TestPutContent(t *testing.T) {
	h := newHarness(t)
	key := snapshotKey("draft-1", "snap-1")

	put := func(load *payload) error {
		return h.store.RecordTxn(func(tx store.RecordTx) error { return putContent(tx, key, load) })
	}

	stored := func() []byte {
		record, err := h.store.GetRecord(NamespaceContent, key)
		if err != nil {
			t.Fatalf("GetRecord(%s) returned error: %v", key, err)
		}

		return record.Value
	}

	loads := make([]*payload, 0, 2)

	for _, seed := range []int64{30, 31} {
		load, err := buildPayload([]byte(randomText(seed, 1000)))
		if err != nil {
			t.Fatalf("buildPayload returned error: %v", err)
		}

		loads = append(loads, load)
	}

	for i, load := range loads {
		if err := put(load); err != nil {
			t.Fatalf("putContent(payload %d) returned error: %s", i, fmtErr(err))
		}

		if !bytes.Equal(stored(), load.compressed) {
			t.Errorf("content after putContent(payload %d) differs from that payload", i)
		}
	}

	refused := errors.New("injected update failure")
	h.store.BeforeUpdate = func(string, string) error { return refused }

	if err := put(loads[0]); !errors.Is(err, refused) {
		t.Errorf("putContent refused by the store error = %s, want the store's error", fmtErr(err))
	}

	if !bytes.Equal(stored(), loads[1].compressed) {
		t.Error("content after a refused putContent differs from the payload stored before")
	}
}

// TestDropContent removes a content record. A record that is gone already
// is no failure, and a delete the store refuses is.
func TestDropContent(t *testing.T) {
	h := newHarness(t)
	key := snapshotKey("draft-1", "snap-1")
	storePayload(t, h, key, []byte(randomText(32, 1000)))

	drop := func() error {
		return h.store.RecordTxn(func(tx store.RecordTx) error { return dropContent(tx, key) })
	}

	refused := errors.New("injected delete failure")
	h.store.FailDelete = func(string, string) error { return refused }

	if err := drop(); !errors.Is(err, refused) {
		t.Errorf("dropContent refused by the store error = %s, want the store's error", fmtErr(err))
	}

	if got := h.store.Count(NamespaceContent); got != 1 {
		t.Errorf("%d content records after a refused dropContent, want 1", got)
	}

	h.store.FailDelete = nil

	for _, state := range []string{"stored", "gone"} {
		if err := drop(); err != nil {
			t.Errorf("dropContent of %s content returned error: %s", state, fmtErr(err))
		}
	}

	if got := h.store.Count(NamespaceContent); got != 0 {
		t.Errorf("%d content records after dropContent, want 0", got)
	}
}

// FuzzContentDraftOf checks that a key contentDraftOf accepts is the key that
// snapshotKey or publishedKey makes of what it returns.
func FuzzContentDraftOf(f *testing.F) {
	for _, key := range []string{
		snapshotKey("draft-1", "snap-1"), publishedKey("doc-1"), "drafts//snap-1", "published/doc-1/x", "other",
	} {
		f.Add(key)
	}

	f.Fuzz(func(t *testing.T, key string) {
		draft, ok := contentDraftOf(key)
		if !ok {
			if draft != "" {
				t.Errorf("contentDraftOf(%q) = %q, false, want no draft", key, draft)
			}

			return
		}

		last := key[strings.LastIndex(key, "/")+1:]

		rebuilt := publishedKey(last)
		if draft != "" {
			rebuilt = snapshotKey(draft, last)
		}

		if last == "" || rebuilt != key {
			t.Errorf("contentDraftOf(%q) = %q, true, want a key that names a snapshot or a document", key, draft)
		}
	})
}

// FuzzDecompress checks that decompress gives exactly the declared size, and
// that every failure is a corruption.
func FuzzDecompress(f *testing.F) {
	load, err := buildPayload([]byte("a builder document"))
	if err != nil {
		f.Fatalf("buildPayload returned error: %v", err)
	}

	f.Add(load.compressed, uint16(load.size))
	f.Add(load.compressed, uint16(load.size+1))
	f.Add([]byte("not gzip"), uint16(8))

	f.Fuzz(func(t *testing.T, compressed []byte, size uint16) {
		data, err := decompress("snapshot", "id", compressed, int64(size))

		switch {
		case err != nil && !errors.Is(err, ErrCorrupt):
			t.Errorf("decompress(%d bytes, %d) error = %s, want ErrCorrupt", len(compressed), size, fmtErr(err))
		case err == nil && len(data) != int(size):
			t.Errorf("decompress(%d bytes, %d) = %d bytes, want %d", len(compressed), size, len(data), size)
		}
	})
}
