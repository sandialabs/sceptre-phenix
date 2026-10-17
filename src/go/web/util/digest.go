package util

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	// digestEntries is how many files' checksums Digests keeps.
	digestEntries = 4096

	// digestHashers is how many files Digests hashes at once, server-wide:
	// hashing reads the whole file, and disk images run to tens of GB.
	digestHashers = 2

	// digestBuffer is the read size for hashing a file.
	digestBuffer = 1 << 20

	// checksumRetry is the Retry-After, in seconds, of a 202 from
	// ServeChecksum.
	checksumRetry = "5"
)

// checksumWait is how long ServeChecksum waits for a hash it starts before
// answering 202, so a small file's checksum comes back at once.
var checksumWait = time.Second //nolint:gochecknoglobals // tests shorten it

// ErrChangedWhileHashing is returned for a file that changed while its
// checksum was computed; its checksum is not kept.
var ErrChangedWhileHashing = errors.New("file changed while its checksum was computed")

// Digests caches the SHA-256 checksums of downloadable files for the server's
// lifetime. ServeDownload sends a cached one as Repr-Digest.
var Digests = NewDigestCache(digestEntries, digestHashers) //nolint:gochecknoglobals // one cache per server

// digestKey identifies a file as it is now: a checksum is kept only for
// exactly the file that was hashed, and is never served once it changes.
type digestKey struct {
	path    string
	inode   uint64
	size    int64
	mtimeNs int64
}

func keyOf(path string, info fs.FileInfo) digestKey {
	return digestKey{path: path, inode: fileInode(info), size: info.Size(), mtimeNs: info.ModTime().UnixNano()}
}

// DigestCache computes files' SHA-256 checksums in the background, one
// computation per file at a time and a bounded number at once, and keeps the
// latest results in memory.
type DigestCache struct {
	mu      sync.Mutex
	sums    map[digestKey][]byte
	order   []digestKey // of sums, oldest first
	failed  map[digestKey]error
	running map[digestKey]chan struct{}
	hashers chan struct{}
	max     int
}

// NewDigestCache returns a cache of at most maxEntries checksums that
// computes at most hashers of them at once.
func NewDigestCache(maxEntries, hashers int) *DigestCache {
	return &DigestCache{
		mu:      sync.Mutex{},
		sums:    map[digestKey][]byte{},
		order:   nil,
		failed:  map[digestKey]error{},
		running: map[digestKey]chan struct{}{},
		hashers: make(chan struct{}, hashers),
		max:     maxEntries,
	}
}

// Lookup returns the checksum of the file at path as info describes it, if
// known. It never starts computing one.
func (c *DigestCache) Lookup(path string, info fs.FileInfo) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sum, ok := c.sums[keyOf(path, info)]

	return sum, ok
}

// Start returns the checksum of the file at path as info describes it if it
// is known. Otherwise it returns nil, having started computing it in the
// background unless that is already under way. If the last computation
// failed, it returns that error, once; the next call starts again.
func (c *DigestCache) Start(path string, info fs.FileInfo) ([]byte, error) {
	key := keyOf(path, info)

	c.mu.Lock()
	defer c.mu.Unlock()

	if sum, ok := c.sums[key]; ok {
		return sum, nil
	}

	if err, ok := c.failed[key]; ok {
		delete(c.failed, key)

		return nil, err
	}

	if _, ok := c.running[key]; !ok {
		done := make(chan struct{})
		c.running[key] = done

		go c.compute(key, done)
	}

	return nil, nil
}

// Wait waits up to timeout for a computation of the file's checksum that is
// under way, and reports whether none is (any more).
func (c *DigestCache) Wait(path string, info fs.FileInfo, timeout time.Duration) bool {
	c.mu.Lock()
	done, ok := c.running[keyOf(path, info)]
	c.mu.Unlock()

	if !ok {
		return true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (c *DigestCache) compute(key digestKey, done chan struct{}) {
	defer close(done)

	c.hashers <- struct{}{}
	sum, err := hashFile(key)
	<-c.hashers

	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.running, key)

	if err != nil {
		// a failure is reported once, to the next Start; nobody polling
		// for it, the record goes with the next overflow
		if len(c.failed) >= c.max {
			clear(c.failed)
		}

		c.failed[key] = err

		return
	}

	c.sums[key] = sum
	c.order = append(c.order, key)

	for len(c.order) > c.max {
		delete(c.sums, c.order[0])
		c.order = c.order[1:]
	}
}

// hashFile returns the SHA-256 of the file key describes, or
// ErrChangedWhileHashing if it is not that file before and after hashing.
func hashFile(key digestKey) ([]byte, error) {
	f, err := os.Open(key.path)
	if err != nil {
		return nil, fmt.Errorf("opening file for checksum: %w", err)
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("reading file for checksum: %w", err)
	}

	if keyOf(key.path, info) != key {
		return nil, fmt.Errorf("%s: %w", key.path, ErrChangedWhileHashing)
	}

	h := sha256.New()

	// hide the file's WriteTo, which would ignore the buffer
	if _, err := io.CopyBuffer(h, struct{ io.Reader }{f}, make([]byte, digestBuffer)); err != nil {
		return nil, fmt.Errorf("reading file for checksum: %w", err)
	}

	after, err := os.Stat(key.path)
	if err != nil || keyOf(key.path, after) != key {
		return nil, fmt.Errorf("%s: %w", key.path, ErrChangedWhileHashing)
	}

	return h.Sum(nil), nil
}

// reprDigest is the Repr-Digest header value (RFC 9530) for a SHA-256 sum.
func reprDigest(sum []byte) string {
	return "sha-256=:" + base64.StdEncoding.EncodeToString(sum) + ":"
}

// Checksum is a file's SHA-256 as the checksum routes report it.
type Checksum struct {
	// Path names the file as the request did.
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`

	// ETag is the file's ETag on download routes (see FileETag).
	ETag string `json:"etag"`

	// SHA256 is the hex checksum; empty while it is being computed.
	SHA256 string `json:"sha256,omitempty"`
}

// ServeChecksum answers a request for the SHA-256 checksum of the file at
// path, named display in the response, from Digests: 200 with the checksum,
// also as Repr-Digest, once it is known, or 202 with Retry-After while it is
// computed in the background. A small file's checksum is usually known at
// once.
//
// It returns an error, having sent nothing, if the file cannot be read, is not
// a regular file (ErrNotRegular), or changed while it was hashed
// (ErrChangedWhileHashing); the caller writes the error response.
func ServeChecksum(w http.ResponseWriter, path, display string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("reading file for checksum: %w", err)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", path, ErrNotRegular)
	}

	sum, err := Digests.Start(path, info)
	if sum == nil && err == nil && Digests.Wait(path, info, checksumWait) {
		sum, err = Digests.Start(path, info)
	}

	if err != nil {
		return err
	}

	body := Checksum{Path: display, Size: info.Size(), Modified: info.ModTime(), ETag: FileETag(info), SHA256: ""}
	status := http.StatusAccepted

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	if sum != nil {
		body.SHA256 = hex.EncodeToString(sum)
		status = http.StatusOK

		w.Header().Set("Repr-Digest", reprDigest(sum))
	} else {
		w.Header().Set("Retry-After", checksumRetry)
	}

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)

	return nil
}
