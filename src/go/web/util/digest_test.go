package util

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// startAndWait starts computing the file's checksum in c and waits for it,
// returning what Start then reports.
func startAndWait(t *testing.T, c *DigestCache, path string) ([]byte, error) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if sum, err := c.Start(path, info); sum != nil || err != nil {
		return sum, err
	}

	if !c.Wait(path, info, 10*time.Second) {
		t.Fatalf("DigestCache.Wait(%q) = false after 10s, want true", path)
	}

	return c.Start(path, info)
}

func TestDigestCacheComputesSHA256(t *testing.T) {
	t.Parallel()

	contents := strings.Repeat("phenix", 400_000) // more than one read buffer
	path := downloadFile(t, contents)
	c := NewDigestCache(4, 1)

	info, _ := os.Stat(path)
	if got, ok := c.Lookup(path, info); ok {
		t.Errorf("DigestCache.Lookup(%q) = %x, true before any computation, want false", path, got)
	}

	sum, err := startAndWait(t, c, path)
	if want := sha256.Sum256([]byte(contents)); err != nil || hex.EncodeToString(sum) != hex.EncodeToString(want[:]) {
		t.Fatalf("DigestCache.Start(%q) = %x, %v; want %x", path, sum, err, want)
	}

	if got, ok := c.Lookup(path, info); !ok || string(got) != string(sum) {
		t.Errorf("DigestCache.Lookup(%q) = %x, %v after computing it; want %x, true", path, got, ok, sum)
	}

	// a changed file is another file
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, _ = os.Stat(path)
	if got, ok := c.Lookup(path, info); ok {
		t.Errorf("DigestCache.Lookup(%q) of a changed file = %x, true; want false", path, got)
	}
}

// However many requests ask, a file is hashed once at a time.
func TestDigestCacheStartsOneComputationPerFile(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "contents")
	info, _ := os.Stat(path)
	c := NewDigestCache(4, 1)

	// hold the only hasher, so the computation cannot finish yet
	c.hashers <- struct{}{}

	for range 5 {
		if sum, err := c.Start(path, info); sum != nil || err != nil {
			t.Errorf("DigestCache.Start(%q) while hashing = %x, %v; want nil, nil", path, sum, err)
		}
	}

	c.mu.Lock()
	running := len(c.running)
	c.mu.Unlock()

	if running != 1 {
		t.Errorf("after 5 DigestCache.Start(%q) calls, %d computations running, want 1", path, running)
	}

	if c.Wait(path, info, 10*time.Millisecond) {
		t.Errorf("DigestCache.Wait(%q) with no free hasher = true, want false", path)
	}

	<-c.hashers

	if sum, err := startAndWait(t, c, path); sum == nil || err != nil {
		t.Errorf("DigestCache.Start(%q) = %x, %v; want a checksum", path, sum, err)
	}
}

func TestDigestCacheDiscardsAFileThatChanged(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "before")
	info, _ := os.Stat(path)
	c := NewDigestCache(4, 1)

	c.hashers <- struct{}{}

	if sum, err := c.Start(path, info); sum != nil || err != nil {
		t.Fatalf("DigestCache.Start(%q) = %x, %v; want nil, nil", path, sum, err)
	}

	// changes between the request and the hashing
	if err := os.WriteFile(path, []byte("after, and longer"), 0o600); err != nil {
		t.Fatal(err)
	}

	<-c.hashers

	if !c.Wait(path, info, 10*time.Second) {
		t.Fatalf("DigestCache.Wait(%q) = false after 10s, want true", path)
	}

	if _, err := c.Start(path, info); !errors.Is(err, ErrChangedWhileHashing) {
		t.Errorf("DigestCache.Start(%q) = %v, want ErrChangedWhileHashing", path, err)
	}

	if got, ok := c.Lookup(path, info); ok {
		t.Errorf("DigestCache.Lookup(%q) of a file that changed while hashing = %x, true; want false", path, got)
	}

	// reported once: asking again starts over
	if sum, err := c.Start(path, info); sum != nil || err != nil {
		t.Errorf("DigestCache.Start(%q) after the failure = %x, %v; want nil, nil", path, sum, err)
	}
}

func TestDigestCacheEvictsTheOldest(t *testing.T) {
	t.Parallel()

	c := NewDigestCache(2, 2)
	contents := []string{"a", "b", "c"}
	paths := make(map[string]string, len(contents))

	for _, s := range contents {
		paths[s] = downloadFile(t, s)
		if sum, err := startAndWait(t, c, paths[s]); sum == nil || err != nil {
			t.Fatalf("DigestCache.Start(%q) = %x, %v; want a checksum", paths[s], sum, err)
		}
	}

	cached := make(map[string]bool, len(paths))
	for s, p := range paths {
		info, _ := os.Stat(p)
		_, cached[s] = c.Lookup(p, info)
	}

	want := map[string]bool{"a": false, "b": true, "c": true}
	if diff := cmp.Diff(want, cached); diff != "" {
		t.Errorf("DigestCache.Lookup of files hashed in order a, b, c mismatch (-want +got):\n%s", diff)
	}
}

func TestServeChecksum(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "contents")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	if err := ServeChecksum(rec, path, "captures/out.pcap"); err != nil {
		t.Fatalf("ServeChecksum(%q) = %v, want nil", path, err)
	}

	// a small file is hashed before the answer
	if rec.Code != http.StatusOK {
		t.Errorf("ServeChecksum(%q) status = %d, want %d", path, rec.Code, http.StatusOK)
	}

	var got Checksum
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding ServeChecksum(%q) body: %v", path, err)
	}

	sum := sha256.Sum256([]byte("contents"))
	want := Checksum{
		Path: "captures/out.pcap", Size: 8, Modified: info.ModTime(), ETag: FileETag(info),
		SHA256: hex.EncodeToString(sum[:]),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ServeChecksum(%q) body mismatch (-want +got):\n%s", path, diff)
	}

	if digest := rec.Header().Get("Repr-Digest"); digest != reprDigest(sum[:]) {
		t.Errorf("ServeChecksum(%q) Repr-Digest = %q, want %q", path, digest, reprDigest(sum[:]))
	}

	// and from now on, downloads say so
	resp, _ := serveDownload(t, http.MethodGet, Download{Path: path, Name: "out.pcap"}, "Range", "bytes=0-1")
	if digest := resp.Header.Get("Repr-Digest"); digest != reprDigest(sum[:]) {
		t.Errorf("ServeDownload(%q) Repr-Digest = %q, want %q", path, digest, reprDigest(sum[:]))
	}

	dir := t.TempDir()
	if err := ServeChecksum(httptest.NewRecorder(), dir, "dir"); !errors.Is(err, ErrNotRegular) {
		t.Errorf("ServeChecksum(%q) = %v, want ErrNotRegular", dir, err)
	}
}

// Not parallel: it holds every one of Digests' hashers.
func TestServeChecksumAnswers202WhileHashing(t *testing.T) {
	original := checksumWait
	checksumWait = 10 * time.Millisecond

	t.Cleanup(func() { checksumWait = original })

	path := downloadFile(t, "contents")

	for range cap(Digests.hashers) {
		Digests.hashers <- struct{}{}
	}

	rec := httptest.NewRecorder()
	err := ServeChecksum(rec, path, "out.pcap")

	for range cap(Digests.hashers) {
		<-Digests.hashers
	}

	if err != nil || rec.Code != http.StatusAccepted || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("ServeChecksum(%q) while hashing = %d (Retry-After %q), %v; want 202 with Retry-After",
			path, rec.Code, rec.Header().Get("Retry-After"), err)
	}

	var got Checksum
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding ServeChecksum(%q) body: %v", path, err)
	}

	want := Checksum{Path: "out.pcap", Size: 8}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(Checksum{}, "Modified", "ETag")); diff != "" {
		t.Errorf("ServeChecksum(%q) while hashing body mismatch (-want +got):\n%s", path, diff)
	}
}
