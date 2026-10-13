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
		t.Fatal("checksum not computed within 10s")
	}

	return c.Start(path, info)
}

func TestDigestCacheComputesSHA256(t *testing.T) {
	t.Parallel()

	contents := strings.Repeat("phenix", 400_000) // more than one read buffer
	path := downloadFile(t, contents)
	c := NewDigestCache(4, 1)

	info, _ := os.Stat(path)
	if _, ok := c.Lookup(path, info); ok {
		t.Fatal("Lookup found a checksum nobody computed")
	}

	sum, err := startAndWait(t, c, path)
	if want := sha256.Sum256([]byte(contents)); err != nil || hex.EncodeToString(sum) != hex.EncodeToString(want[:]) {
		t.Fatalf("checksum = %x, %v; want %x", sum, err, want)
	}

	if got, ok := c.Lookup(path, info); !ok || string(got) != string(sum) {
		t.Errorf("Lookup = %x, %v after computing it", got, ok)
	}

	// a changed file is another file
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, _ = os.Stat(path)
	if _, ok := c.Lookup(path, info); ok {
		t.Error("Lookup returned the checksum of the file before it changed")
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
			t.Fatalf("Start = %x, %v while hashing", sum, err)
		}
	}

	c.mu.Lock()
	running := len(c.running)
	c.mu.Unlock()

	if running != 1 {
		t.Fatalf("%d computations running, want 1", running)
	}

	if c.Wait(path, info, 10*time.Millisecond) {
		t.Fatal("Wait reported a blocked computation done")
	}

	<-c.hashers

	if sum, err := startAndWait(t, c, path); sum == nil || err != nil {
		t.Fatalf("checksum = %x, %v", sum, err)
	}
}

func TestDigestCacheDiscardsAFileThatChanged(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "before")
	info, _ := os.Stat(path)
	c := NewDigestCache(4, 1)

	c.hashers <- struct{}{}

	if sum, err := c.Start(path, info); sum != nil || err != nil {
		t.Fatalf("Start = %x, %v", sum, err)
	}

	// changes between the request and the hashing
	if err := os.WriteFile(path, []byte("after, and longer"), 0o600); err != nil {
		t.Fatal(err)
	}

	<-c.hashers

	if !c.Wait(path, info, 10*time.Second) {
		t.Fatal("checksum not computed within 10s")
	}

	if _, err := c.Start(path, info); !errors.Is(err, ErrChangedWhileHashing) {
		t.Fatalf("Start = %v, want ErrChangedWhileHashing", err)
	}

	if _, ok := c.Lookup(path, info); ok {
		t.Error("kept the checksum of a file that changed")
	}

	// reported once: asking again starts over
	if sum, err := c.Start(path, info); sum != nil || err != nil {
		t.Errorf("Start after the failure = %x, %v; want a new computation", sum, err)
	}
}

func TestDigestCacheEvictsTheOldest(t *testing.T) {
	t.Parallel()

	c := NewDigestCache(2, 2)
	paths := []string{downloadFile(t, "a"), downloadFile(t, "b"), downloadFile(t, "c")}

	for _, p := range paths {
		if sum, err := startAndWait(t, c, p); sum == nil || err != nil {
			t.Fatalf("checksum of %s = %x, %v", p, sum, err)
		}
	}

	for i, p := range paths {
		info, _ := os.Stat(p)
		if _, ok := c.Lookup(p, info); ok != (i > 0) {
			t.Errorf("file %d cached = %v, want %v", i, ok, i > 0)
		}
	}
}

func TestServeChecksum(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "contents")

	rec := httptest.NewRecorder()
	if err := ServeChecksum(rec, path, "captures/out.pcap"); err != nil {
		t.Fatalf("ServeChecksum: %v", err)
	}

	// a small file is hashed before the answer
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got Checksum
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	want := sha256.Sum256([]byte("contents"))
	if got.Path != "captures/out.pcap" || got.Size != 8 || got.SHA256 != hex.EncodeToString(want[:]) || got.ETag == "" {
		t.Errorf("got %+v", got)
	}

	if digest := rec.Header().Get("Repr-Digest"); digest != reprDigest(want[:]) {
		t.Errorf("Repr-Digest = %q, want %q", digest, reprDigest(want[:]))
	}

	// and from now on, downloads say so
	resp, _ := serveDownload(t, http.MethodGet, Download{Path: path, Name: "out.pcap"}, "Range", "bytes=0-1")
	if digest := resp.Header.Get("Repr-Digest"); digest != reprDigest(want[:]) {
		t.Errorf("download's Repr-Digest = %q, want %q", digest, reprDigest(want[:]))
	}

	if err := ServeChecksum(httptest.NewRecorder(), t.TempDir(), "dir"); !errors.Is(err, ErrNotRegular) {
		t.Errorf("a directory: %v, want ErrNotRegular", err)
	}
}

// Not parallel: it holds every one of Digests' hashers.
func TestServeChecksumAnswers202WhileHashing(t *testing.T) {
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
		t.Fatalf("ServeChecksum = %d (Retry-After %q), %v; want 202", rec.Code, rec.Header().Get("Retry-After"), err)
	}

	var got Checksum
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || got.SHA256 != "" || got.Size != 8 {
		t.Errorf("got %+v, %v", got, err)
	}
}
