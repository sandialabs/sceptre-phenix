package builder

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"phenix/store"
)

// Chunk scopes group the chunks of one payload so they can be removed with a
// single bounded prefix deletion, and so no two writers ever share a chunk key.
//
// Draft snapshots use "drafts/<draft-id>/<snapshot-id>/", which is unique per
// snapshot: two concurrent appends of identical content never write the same
// key, so the loser of a compare-and-swap can always delete its own chunks
// without touching the winner's. Published documents work the same way: their
// ID stays content addressed, but every attempt to store one writes its own
// "published/<document-id>/<payload-id>/" scope, so no two writers ever share
// a chunk key and a loser can never delete content a winner is using.
const (
	scopeDrafts    = "drafts"
	scopePublished = "published"

	chunkKeySegments = 4
	chunkIndexDigits = 6
)

// payload is a compressed, chunked document ready to be written to the store.
type payload struct {
	digest         string
	size           int64
	compressedSize int64
	chunkDigests   []string
	chunks         [][]byte
}

// digestOf returns the "sha256:<hex>" digest of data.
func digestOf(data []byte) string {
	sum := sha256.Sum256(data)

	return "sha256:" + hex.EncodeToString(sum[:])
}

// draftScope returns the chunk scope prefix owning every snapshot of a draft.
func draftScope(draftID string) string {
	return scopeDrafts + "/" + draftID + "/"
}

// snapshotScope returns the private, immutable chunk scope of one draft
// snapshot.
func snapshotScope(draftID, snapshotID string) string {
	return draftScope(draftID) + snapshotID + "/"
}

// publishedScope returns the chunk scope prefix owning every payload of a
// published document.
func publishedScope(documentID string) string {
	return scopePublished + "/" + documentID + "/"
}

// publishedPayloadScope returns the private chunk scope one attempt at storing
// a published document wrote. The payload ID is generated per attempt and
// recorded in the document's metadata, so the chunks a winner references are
// never written or removed by anybody else.
func publishedPayloadScope(documentID, payloadID string) string {
	return publishedScope(documentID) + payloadID + "/"
}

// chunkKey returns the record key of the index-th chunk of a payload scope.
// Keys sort in chunk order.
func chunkKey(scope string, index int) string {
	return fmt.Sprintf("%s%0*d", scope, chunkIndexDigits, index)
}

// chunkPayloadScopeOf returns the payload scope a chunk key belongs to, or ""
// when the key is not shaped like a chunk key.
func chunkPayloadScopeOf(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) != chunkKeySegments {
		return ""
	}

	if slices.Contains(parts, "") {
		return ""
	}

	return parts[0] + "/" + parts[1] + "/" + parts[2] + "/"
}

// buildPayload encodes, size checks, compresses, and splits a document.
func buildPayload(data []byte, chunkSize int) (*payload, error) {
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

	compressed := buf.Bytes()

	if int64(len(compressed)) > MaxCompressedBytes {
		return nil, newTooLargeError("compressed document", int64(len(compressed)), MaxCompressedBytes)
	}

	chunks := splitChunks(compressed, chunkSize)

	digests := make([]string, len(chunks))
	for i, chunk := range chunks {
		digests[i] = digestOf(chunk)
	}

	return &payload{
		digest:         digestOf(data),
		size:           int64(len(data)),
		compressedSize: int64(len(compressed)),
		chunkDigests:   digests,
		chunks:         chunks,
	}, nil
}

func splitChunks(data []byte, chunkSize int) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}

	chunks := make([][]byte, 0, (len(data)/chunkSize)+1)

	for start := 0; start < len(data); start += chunkSize {
		end := min(start+chunkSize, len(data))

		chunks = append(chunks, data[start:end])
	}

	return chunks
}

// writeChunks creates every chunk of the payload that is not already stored. It
// returns the keys it actually created so a failed compare-and-swap can remove
// exactly the chunks the attempt added, leaving pre-existing (shared) chunks in
// place.
//
// A partial failure never leaks: the chunks this attempt created are removed
// before returning, and any failure to remove them is joined onto the returned
// error so it matches both the original failure and [ErrCleanup].
func (s *Service) writeChunks(scope string, load *payload) ([]string, error) {
	created := make([]string, 0, len(load.chunks))

	for i, chunk := range load.chunks {
		key := chunkKey(scope, i)

		_, err := s.store.CreateRecord(NamespaceChunks, key, chunk)

		switch {
		case err == nil:
			created = append(created, key)
		case errors.Is(err, store.ErrRecordExist):
			continue
		default:
			writeErr := fmt.Errorf("writing chunk %s: %w", key, err)
			cleanupErrs := s.deleteChunkKeys(created)

			return nil, errors.Join(writeErr, newCleanupError("writing chunks", cleanupErrs))
		}
	}

	return created, nil
}

// deleteChunkKeys removes the given chunk records, collecting every failure.
func (s *Service) deleteChunkKeys(keys []string) []error {
	var errs []error

	for _, key := range keys {
		err := s.store.DeleteRecord(NamespaceChunks, key, store.AnyRevision)
		if err != nil && !errors.Is(err, store.ErrRecordNotExist) {
			errs = append(errs, fmt.Errorf("deleting chunk %s: %w", key, err))
		}
	}

	return errs
}

// deleteChunkScope removes every chunk under a scope with a single bounded
// prefix deletion.
func (s *Service) deleteChunkScope(scope string) error {
	if _, err := s.store.DeleteRecordPrefix(NamespaceChunks, scope); err != nil {
		return fmt.Errorf("deleting chunks under %s: %w", scope, err)
	}

	return nil
}

// readPayload reassembles, bounds checks, and verifies the document bytes
// described by a manifest. Chunks are read strictly in manifest order and each
// one must match its recorded digest, so a missing, reordered, truncated, or
// modified chunk is always reported as corruption rather than silently
// producing a different document.
func (s *Service) readPayload(kind, id, scope string, manifest SnapshotManifest) ([]byte, error) {
	if err := validateManifestShape(kind, id, manifest); err != nil {
		return nil, err
	}

	compressed := make([]byte, 0, manifest.CompressedSize)

	for i, want := range manifest.ChunkDigests {
		key := chunkKey(scope, i)

		record, err := s.store.GetRecord(NamespaceChunks, key)
		if err != nil {
			if errors.Is(err, store.ErrRecordNotExist) {
				return nil, newCorruptError(kind, id, fmt.Sprintf("chunk %d of %d is missing", i, len(manifest.ChunkDigests)))
			}

			return nil, fmt.Errorf("reading chunk %s: %w", key, err)
		}

		if got := digestOf(record.Value); got != want {
			return nil, newCorruptError(kind, id, fmt.Sprintf("chunk %d digest is %s, expected %s", i, got, want))
		}

		if int64(len(compressed))+int64(len(record.Value)) > MaxCompressedBytes {
			return nil, newCorruptError(kind, id, "compressed payload exceeds the maximum compressed size")
		}

		compressed = append(compressed, record.Value...)
	}

	if int64(len(compressed)) != manifest.CompressedSize {
		return nil, newCorruptError(
			kind, id,
			fmt.Sprintf("compressed payload is %d bytes, expected %d", len(compressed), manifest.CompressedSize),
		)
	}

	data, err := decompress(kind, id, compressed, manifest.Size)
	if err != nil {
		return nil, err
	}

	if got := digestOf(data); got != manifest.Digest {
		return nil, newCorruptError(kind, id, fmt.Sprintf("document digest is %s, expected %s", got, manifest.Digest))
	}

	return data, nil
}

// decompress inflates a payload with a hard output bound. Reading one byte past
// the expected size is treated as corruption, which prevents a maliciously or
// accidentally crafted payload from expanding without limit.
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
		return nil, newCorruptError(kind, id, "payload could not be decompressed: "+err.Error())
	}

	if int64(len(data)) != size {
		return nil, newCorruptError(kind, id, fmt.Sprintf("decompressed payload is %d bytes, expected %d", len(data), size))
	}

	if err := reader.Close(); err != nil {
		return nil, newCorruptError(kind, id, "payload could not be decompressed: "+err.Error())
	}

	return data, nil
}

func validateManifestShape(kind, id string, manifest SnapshotManifest) error {
	switch {
	case len(manifest.ChunkDigests) == 0:
		return newCorruptError(kind, id, "manifest has no chunks")
	case len(manifest.ChunkDigests) > MaxChunks:
		return newCorruptError(kind, id, fmt.Sprintf("manifest declares %d chunks, more than %d", len(manifest.ChunkDigests), MaxChunks))
	case manifest.CompressedSize < 0 || manifest.CompressedSize > MaxCompressedBytes:
		return newCorruptError(kind, id, fmt.Sprintf("manifest declares a compressed size of %d", manifest.CompressedSize))
	case manifest.Size < 0 || manifest.Size > MaxDocumentBytes:
		return newCorruptError(kind, id, fmt.Sprintf("manifest declares a size of %d", manifest.Size))
	case manifest.Digest == "":
		return newCorruptError(kind, id, "manifest has no document digest")
	case !digestPattern.MatchString(manifest.Digest):
		return newCorruptError(kind, id, fmt.Sprintf("manifest document digest %q is not a sha256 digest", manifest.Digest))
	case manifest.ChunkSize <= 0 || manifest.ChunkSize > ChunkBytes:
		return newCorruptError(kind, id, fmt.Sprintf("manifest declares a chunk size of %d", manifest.ChunkSize))
	}

	for i, digest := range manifest.ChunkDigests {
		if !digestPattern.MatchString(digest) {
			return newCorruptError(kind, id, fmt.Sprintf("chunk %d digest %q is not a sha256 digest", i, digest))
		}
	}

	return nil
}
