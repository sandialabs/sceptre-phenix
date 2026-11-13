package util

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
}

func TestServeChecksumRefuses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tests := []struct {
		name, path string
		want       error
	}{
		{name: "directory", path: dir, want: ErrNotRegular},
		{name: "missing", path: dir + "/missing", want: os.ErrNotExist},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			if err := ServeChecksum(rec, tc.path, tc.name); !errors.Is(err, tc.want) {
				t.Errorf("ServeChecksum(%q) = %v, want %v", tc.path, err, tc.want)
			}

			if rec.Body.Len() != 0 || len(rec.Header()) != 0 {
				t.Errorf("ServeChecksum(%q) wrote header %v and body %q, want nothing", tc.path, rec.Header(), rec.Body.String())
			}
		})
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

// A file that went missing while it waited to be hashed, and came back as it
// was, reports the failure once, having sent nothing. Not parallel: it holds
// every one of Digests' hashers.
func TestServeChecksumReportsAFailedHash(t *testing.T) {
	path := downloadFile(t, "contents")

	// move the directory, not the file: a rename of the file changes its inode change time
	dir := filepath.Dir(path)
	moved := dir + ".moved"

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	held := true
	release := func() {
		if held {
			held = false

			for range cap(Digests.hashers) {
				<-Digests.hashers
			}
		}
	}

	for range cap(Digests.hashers) {
		Digests.hashers <- struct{}{}
	}

	t.Cleanup(release)

	if sum, err := Digests.Start(path, info); sum != nil || err != nil {
		t.Fatalf("Digests.Start(%q) = %x, %v; want nil, nil", path, sum, err)
	}

	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}

	release()

	if !Digests.Wait(path, info, 10*time.Second) {
		t.Fatalf("Digests.Wait(%q) = false after 10s, want true", path)
	}

	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	if err := ServeChecksum(rec, path, "out.pcap"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ServeChecksum(%q) after its hash failed = %v, want %v", path, err, os.ErrNotExist)
	}

	if rec.Body.Len() != 0 || len(rec.Header()) != 0 {
		t.Errorf("ServeChecksum(%q) after its hash failed wrote header %v and body %q, want nothing",
			path, rec.Header(), rec.Body.String())
	}
}

func TestDigestCacheWaitWithNothingRunning(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "contents")
	info, _ := os.Stat(path)
	c := NewDigestCache(4, 1)

	if !c.Wait(path, info, time.Hour) {
		t.Errorf("DigestCache.Wait(%q) with nothing running = false, want true", path)
	}
}

// Failures past the bound are forgotten, so a forgotten one is computed again
// rather than reported.
func TestDigestCacheForgetsFailuresPastTheBound(t *testing.T) {
	t.Parallel()

	c := NewDigestCache(1, 1)

	// a file removed after it was stat'd fails to hash
	gone := func(name string) (string, os.FileInfo) {
		path := downloadFile(t, name)

		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}

		return path, info
	}

	pathA, infoA := gone("a")
	pathB, infoB := gone("b")

	for _, f := range []struct {
		path string
		info os.FileInfo
	}{{pathA, infoA}, {pathB, infoB}} {
		_, _ = c.Start(f.path, f.info)

		if !c.Wait(f.path, f.info, 10*time.Second) {
			t.Fatalf("DigestCache.Wait(%q) = false after 10s, want true", f.path)
		}
	}

	// B first: the recompute of A that Start(pathA) begins can forget B
	if _, err := c.Start(pathB, infoB); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("DigestCache.Start(%q) of the last failure = %v, want %v", pathB, err, os.ErrNotExist)
	}

	if sum, err := c.Start(pathA, infoA); sum != nil || err != nil {
		t.Errorf("DigestCache.Start(%q) of a forgotten failure = %x, %v; want nil, nil", pathA, sum, err)
	}

	if !c.Wait(pathA, infoA, 10*time.Second) {
		t.Fatalf("DigestCache.Wait(%q) = false after 10s, want true", pathA)
	}
}

func TestHashFile(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "contents")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte("contents"))
	stale := keyOf(path, info)
	stale.size++

	tests := []struct {
		name    string
		key     digestKey
		want    []byte
		wantErr error
	}{
		{name: "the file", key: keyOf(path, info), want: sum[:]},
		{name: "another file", key: stale, wantErr: ErrChangedWhileHashing},
		{name: "missing", key: digestKey{path: path + ".missing"}, wantErr: os.ErrNotExist},
		// it opens and stats as described, but cannot be read
		{name: "directory", key: keyOf(dir, dirInfo), wantErr: syscall.EISDIR},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := hashFile(tc.key)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("hashFile(%+v) error = %v, want %v", tc.key, err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("hashFile(%+v) mismatch (-want +got):\n%s", tc.key, diff)
			}
		})
	}
}

func TestKeyOf(t *testing.T) {
	t.Parallel()

	path := downloadFile(t, "contents")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	want := digestKey{
		path: path, inode: fileInode(info), size: 8,
		mtimeNs: info.ModTime().UnixNano(), ctimeNs: fileChangeTime(info),
	}
	if diff := cmp.Diff(want, keyOf(path, info), cmp.AllowUnexported(digestKey{})); diff != "" {
		t.Errorf("keyOf(%q) mismatch (-want +got):\n%s", path, diff)
	}

	if keyOf("other", info) == keyOf(path, info) {
		t.Errorf("keyOf(other) = keyOf(%q), want the path to tell them apart", path)
	}
}

func TestReprDigest(t *testing.T) {
	t.Parallel()

	empty := sha256.Sum256(nil)

	tests := []struct {
		name string
		sum  []byte
		want string
	}{
		// RFC 9530's encoding of the SHA-256 of no content
		{name: "empty content", sum: empty[:], want: "sha-256=:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=:"},
		{name: "no sum", sum: nil, want: "sha-256=::"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := reprDigest(tc.sum); got != tc.want {
				t.Errorf("reprDigest(%x) = %q, want %q", tc.sum, got, tc.want)
			}
		})
	}
}
