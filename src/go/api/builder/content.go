package builder

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"phenix/store"
	"phenix/types/builder"
)

// Content record keys in [NamespaceContent]: "drafts/<draft-id>/<snapshot-id>"
// and "published/<document-id>". One prefix deletion removes every snapshot of
// a draft.
const (
	scopeDrafts    = "drafts"
	scopePublished = "published"
)

// payload is a compressed document ready to be written to the store.
type payload struct {
	digest     string
	size       int64
	compressed []byte
}

// recordReader reads one record: a [store.RecordStore], or a
// [store.RecordTx] inside a transaction.
type recordReader interface {
	GetRecord(namespace, key string) (store.Record, error)
}

// digestOf returns the "sha256:<hex>" digest of data.
func digestOf(data []byte) string {
	sum := sha256.Sum256(data)

	return "sha256:" + hex.EncodeToString(sum[:])
}

// draftContentPrefix returns the prefix of the content records of every
// snapshot of a draft.
func draftContentPrefix(draftID string) string {
	return scopeDrafts + "/" + draftID + "/"
}

// snapshotKey returns the key of the content record of one draft snapshot.
func snapshotKey(draftID, snapshotID string) string {
	return draftContentPrefix(draftID) + snapshotID
}

// publishedKey returns the key of the content record of a published
// document.
func publishedKey(documentID string) string {
	return scopePublished + "/" + documentID
}

// contentDraftOf returns the draft that a content key belongs to, or "" for
// the key of a published document. It returns false for a key that is not
// shaped like a content key.
func contentDraftOf(key string) (string, bool) {
	parts := strings.Split(key, "/")

	switch {
	case len(parts) == 3 && parts[0] == scopeDrafts && parts[1] != "" && parts[2] != "":
		return parts[1], true
	case len(parts) == 2 && parts[0] == scopePublished && parts[1] != "":
		return "", true
	}

	return "", false
}

// buildPayload size checks and compresses a document.
func buildPayload(data []byte) (*payload, error) {
	if int64(len(data)) > MaxDocumentBytes {
		return nil, newTooLargeError("document", int64(len(data)), MaxDocumentBytes)
	}

	var buf bytes.Buffer

	writer, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, fmt.Errorf("creating gzip writer: %w", err)
	}

	if _, err := writer.Write(data); err != nil {
		return nil, fmt.Errorf("compressing document: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("compressing document: %w", err)
	}

	if int64(buf.Len()) > MaxCompressedBytes {
		return nil, newTooLargeError("compressed document", int64(buf.Len()), MaxCompressedBytes)
	}

	return &payload{digest: digestOf(data), size: int64(len(data)), compressed: buf.Bytes()}, nil
}

// putContent writes the content record at key in a transaction. It replaces
// a record that is already there, which no metadata names.
func putContent(tx store.RecordTx, key string, load *payload) error {
	_, err := tx.CreateRecord(NamespaceContent, key, load.compressed)
	if errors.Is(err, store.ErrRecordExist) {
		_, err = tx.UpdateRecord(NamespaceContent, key, load.compressed, store.AnyRevision)
	}

	if err != nil {
		return fmt.Errorf("writing content %s: %w", key, err)
	}

	return nil
}

// dropContent removes the content record at key in a transaction. A record
// that is already gone is not a failure: the transaction removes the
// metadata that named it all the same.
func dropContent(tx store.RecordTx, key string) error {
	err := tx.DeleteRecord(NamespaceContent, key, store.AnyRevision)
	if err != nil && !errors.Is(err, store.ErrRecordNotExist) {
		return fmt.Errorf("deleting content %s: %w", key, err)
	}

	return nil
}

// readPayload reads, bounds checks, and verifies the document bytes that a
// manifest describes. The compressed record must have the size that the
// manifest records, and the document in it the size and the digest. Thus
// readPayload always reports a missing, truncated, or modified record as
// corruption, and never silently returns a different document.
func readPayload(reader recordReader, kind, id, key string, manifest SnapshotManifest) ([]byte, error) {
	if err := validateManifestShape(kind, id, manifest); err != nil {
		return nil, err
	}

	record, err := reader.GetRecord(NamespaceContent, key)
	if err != nil {
		if errors.Is(err, store.ErrRecordNotExist) {
			return nil, newCorruptError(kind, id, "content is missing")
		}

		return nil, fmt.Errorf("reading content %s: %w", key, err)
	}

	if int64(len(record.Value)) != manifest.CompressedSize {
		return nil, newCorruptError(
			kind, id,
			fmt.Sprintf("compressed payload is %d bytes, expected %d", len(record.Value), manifest.CompressedSize),
		)
	}

	data, err := decompress(kind, id, record.Value, manifest.Size)
	if err != nil {
		return nil, err
	}

	if got := digestOf(data); got != manifest.Digest {
		return nil, newCorruptError(kind, id, fmt.Sprintf("document digest is %s, expected %s", got, manifest.Digest))
	}

	return data, nil
}

// decompress inflates a payload with a hard output bound. One byte past the
// expected size is corruption. This stops a malicious or accidentally crafted
// payload from expanding without limit.
func decompress(kind, id string, compressed []byte, size int64) ([]byte, error) {
	if size > MaxDocumentBytes {
		return nil, newCorruptError(kind, id, fmt.Sprintf("declared size %d exceeds the document limit", size))
	}

	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, newCorruptError(kind, id, "payload is not valid gzip data")
	}

	defer func() { _ = reader.Close() }()

	limited := io.LimitReader(reader, size+1)

	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, newCorruptError(kind, id, "phenix cannot decompress the payload: "+err.Error())
	}

	if int64(len(data)) != size {
		return nil, newCorruptError(kind, id, fmt.Sprintf("decompressed payload is %d bytes, expected %d", len(data), size))
	}

	if err := reader.Close(); err != nil {
		return nil, newCorruptError(kind, id, "phenix cannot decompress the payload: "+err.Error())
	}

	return data, nil
}

func validateManifestShape(kind, id string, manifest SnapshotManifest) error {
	switch {
	case manifest.CompressedSize <= 0 || manifest.CompressedSize > MaxCompressedBytes:
		return newCorruptError(kind, id, fmt.Sprintf("manifest declares a compressed size of %d", manifest.CompressedSize))
	case manifest.Size < 0 || manifest.Size > MaxDocumentBytes:
		return newCorruptError(kind, id, fmt.Sprintf("manifest declares a size of %d", manifest.Size))
	case manifest.Digest == "":
		return newCorruptError(kind, id, "manifest has no document digest")
	case !builder.IsDigest(manifest.Digest):
		return newCorruptError(kind, id, fmt.Sprintf("manifest document digest %q is not a sha256 digest", manifest.Digest))
	}

	return nil
}
