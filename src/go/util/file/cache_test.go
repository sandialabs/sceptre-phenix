package file

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
)

// newTestCache returns a listing cache whose listings expire only when a test
// ages them.
func newTestCache() *listingCache {
	return newListingCache(time.Hour)
}

// fill runs a listing for key, for tests that only need it cached.
func fill(c *listingCache, key string, list func() (Files, error)) error {
	_, _, err := c.get(key, list)

	return err
}

// age makes key's listing look as if it started d earlier.
func (c *listingCache) age(key string, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key].started = c.entries[key].started.Add(-d)
}

// countingList returns a listing function that counts its calls.
func countingList(calls *atomic.Int32, files Files) func() (Files, error) {
	return func() (Files, error) {
		calls.Add(1)

		return files, nil
	}
}

func TestListingCacheReusesUntilExpired(t *testing.T) {
	cache := newTestCache()

	var calls atomic.Int32

	list := countingList(&calls, Files{{Name: "a.pcap", Path: "a.pcap"}})

	for range 3 {
		files, _, err := cache.get("exp/files/", list)
		if err != nil || len(files) != 1 {
			t.Fatalf("expected one file, got %v (err %v)", files, err)
		}
	}

	if calls.Load() != 1 {
		t.Fatalf("expected one listing within the TTL, got %d", calls.Load())
	}

	cache.age("exp/files/", cache.ttl)

	if _, _, err := cache.get("exp/files/", list); err != nil {
		t.Fatal(err)
	}

	if calls.Load() != 2 {
		t.Fatalf("expected a new listing once expired, got %d listings", calls.Load())
	}
}

func TestListingCacheKeysByExperiment(t *testing.T) {
	cache := newTestCache()

	var calls atomic.Int32

	_ = fill(cache, listingKey("a"), countingList(&calls, nil))
	_ = fill(cache, listingKey("b"), countingList(&calls, nil))

	if calls.Load() != 2 {
		t.Fatalf("expected a listing per experiment, got %d", calls.Load())
	}
}

func TestListingCacheMergesConcurrentListings(t *testing.T) {
	cache := newTestCache()

	var (
		calls   atomic.Int32
		entered = make(chan struct{})
		release = make(chan struct{})
	)

	list := func() Files {
		if calls.Add(1) == 1 {
			close(entered)
		}

		<-release

		return Files{{Name: "a", Path: "a"}}
	}

	const callers = 8

	var wg sync.WaitGroup

	results := make([]Files, callers)

	wg.Add(1)

	go func() {
		defer wg.Done()

		results[0], _, _ = cache.get("k", func() (Files, error) { return list(), nil })
	}()

	<-entered

	for i := 1; i < callers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			results[i], _, _ = cache.get("k", func() (Files, error) { return list(), nil })
		}()
	}

	close(release)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("expected one listing for concurrent callers, got %d", calls.Load())
	}

	for i, files := range results {
		if len(files) != 1 {
			t.Fatalf("caller %d got %v", i, files)
		}
	}
}

// A listing that was in flight when the files changed may miss the change, so
// it must not be reused by callers who ask after the change.
func TestListingCacheDropsListingInvalidatedInFlight(t *testing.T) {
	cache := newTestCache()

	var (
		calls   atomic.Int32
		entered = make(chan struct{})
		release = make(chan struct{})
		done    = make(chan struct{})
	)

	go func() {
		defer close(done)

		_ = fill(cache, "k", func() (Files, error) {
			calls.Add(1)
			close(entered)
			<-release

			return nil, nil
		})
	}()

	<-entered
	cache.invalidate("k")
	close(release)
	<-done

	_ = fill(cache, "k", countingList(&calls, nil))

	if calls.Load() != 2 {
		t.Fatalf("expected a new listing after an in-flight invalidate, got %d listings", calls.Load())
	}
}

func TestListingCacheDoesNotReuseFailures(t *testing.T) {
	cache := newTestCache()

	var calls atomic.Int32

	failing := func() (Files, error) {
		calls.Add(1)

		return nil, errors.New("minimega unavailable")
	}

	if _, _, err := cache.get("k", failing); err == nil {
		t.Fatal("expected the listing's error")
	}

	if _, _, err := cache.get("k", countingList(&calls, nil)); err != nil {
		t.Fatalf("expected a fresh listing after a failure, got %v", err)
	}

	if calls.Load() != 2 {
		t.Fatalf("expected a new listing after a failure, got %d listings", calls.Load())
	}
}

func TestListingCacheReleasesWaitersOnPanic(t *testing.T) {
	t.Parallel()

	cache := newTestCache()

	var (
		entered  = make(chan struct{})
		release  = make(chan struct{})
		panicked = make(chan any, 1)
	)

	go func() {
		defer func() { panicked <- recover() }()

		_ = fill(cache, "k", func() (Files, error) {
			close(entered)
			<-release
			panic("boom")
		})
	}()

	<-entered

	cache.mu.Lock()
	inFlight := cache.entries["k"]
	cache.mu.Unlock()

	close(release)

	// The panic still reaches the caller whose listing it was.
	if r := <-panicked; r != "boom" {
		t.Fatalf("lister recovered %v, want the original panic", r)
	}

	// What every caller that joined the listing gets once it is done.
	<-inFlight.done

	if !errors.Is(inFlight.err, errListingAborted) || inFlight.files != nil {
		t.Fatalf("joined callers get %v, %v; want no files and errListingAborted", inFlight.files, inFlight.err)
	}

	var calls atomic.Int32

	if _, _, err := cache.get("k", countingList(&calls, nil)); err != nil || calls.Load() != 1 {
		t.Fatalf("expected a fresh listing after the panic, got %d listings (err %v)", calls.Load(), err)
	}
}

func TestListingCacheHandsOutCopies(t *testing.T) {
	cache := newTestCache()

	list := func() (Files, error) {
		return Files{
			{Name: "b", Path: "b", Categories: []string{"Packet Capture"}},
			{Name: "a", Path: "a", Categories: []string{"Unknown"}},
		}, nil
	}

	first, _, _ := cache.get("k", list)
	first.SortByName(true)
	first[0].Categories[0] = "changed"

	second, _, _ := cache.get("k", list)

	if second[0].Name != "b" || second[1].Categories[0] != "Unknown" {
		t.Fatalf("a caller's changes leaked into the cached listing: %+v", second)
	}
}

func TestListingCachePrunesExpiredListings(t *testing.T) {
	cache := newTestCache()

	_ = fill(cache, "old", countingList(new(atomic.Int32), nil))

	cache.age("old", cache.ttl)

	_ = fill(cache, "new", countingList(new(atomic.Int32), nil))

	cache.mu.Lock()
	defer cache.mu.Unlock()

	if _, ok := cache.entries["old"]; ok {
		t.Fatal("expected the expired listing to be pruned")
	}
}

// installListing replaces the process-wide listing cache with one holding the
// given listing for experiment "exp", so MMClusterFiles needs no minimega.
func installListing(t *testing.T, files Files) {
	t.Helper()

	const exp = "exp"

	original := experimentListings

	t.Cleanup(func() { experimentListings = original })

	experimentListings = newListingCache(time.Hour)

	_ = fill(experimentListings, listingKey(exp), func() (Files, error) { return files, nil })
}

func TestMMClusterFilesFiltersCachedListing(t *testing.T) {
	installListing(t, Files{
		{Name: "vm_eth0.pcap", Path: "vm_eth0.pcap", Categories: []string{"Packet Capture"}},
		{Name: "notes.txt", Path: "notes.txt", Categories: []string{"Unknown"}},
	})

	var files MMClusterFiles

	all, err := files.GetExperimentFiles("exp", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("expected both files unfiltered, got %v (err %v)", all, err)
	}

	matched, err := files.GetExperimentFiles("exp", "notes")
	if err != nil || len(matched) != 1 || matched[0].Name != "notes.txt" {
		t.Fatalf("expected only notes.txt, got %v (err %v)", matched, err)
	}

	none, err := files.GetExperimentFiles("exp", "and")
	if err != nil || len(none) != 0 {
		t.Fatalf("expected nothing for an unparsable filter, got %v (err %v)", none, err)
	}
}

func TestMMClusterFilesSnapshotsFromCachedListing(t *testing.T) {
	installListing(t, Files{
		{Name: "vm__snap.hdd", Path: "vm__snap.hdd"},
		{Name: "vm__snap.state", Path: "vm__snap.state"},
		{Name: "vm__disk-only.hdd", Path: "vm__disk-only.hdd"},
	})

	original := DefaultClusterFiles

	t.Cleanup(func() { DefaultClusterFiles = original })

	DefaultClusterFiles = MMClusterFiles{}

	snapshots, err := GetExperimentSnapshots("exp")
	if err != nil || len(snapshots) != 1 || snapshots[0] != "vm__snap" {
		t.Fatalf("expected only the complete snapshot, got %v (err %v)", snapshots, err)
	}
}

// recordingFiles records deletions; other methods panic through the nil
// embedded interface.
type recordingFiles struct {
	ClusterFiles

	deleted []string
}

func (f *recordingFiles) DeleteFile(path string) error {
	f.deleted = append(f.deleted, path)

	return nil
}

func TestDeletingFilesDropsCachedListings(t *testing.T) { //nolint:paralleltest // replaces package globals
	for name, tc := range map[string]struct {
		remove func() error
		want   []string
	}{
		"DeleteFile": {
			remove: func() error { return DeleteFile("exp/files/a") },
			want:   []string{"exp/files/a"},
		},
		"DeleteExistingFiles through DeleteFile": {
			remove: func() error { return DeleteExistingFiles([]string{"x", "y"}) },
			want:   []string{"x", "y"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			installListing(t, Files{{Name: "a", Path: "a"}})

			original := DefaultClusterFiles

			t.Cleanup(func() { DefaultClusterFiles = original })

			files := &recordingFiles{ClusterFiles: nil, deleted: nil}
			DefaultClusterFiles = files

			if err := tc.remove(); err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(files.deleted, tc.want) {
				t.Fatalf("deleted %q, want %q", files.deleted, tc.want)
			}

			experimentListings.mu.Lock()
			defer experimentListings.mu.Unlock()

			if len(experimentListings.entries) != 0 {
				t.Fatal("expected the cached listings to be dropped")
			}
		})
	}
}

func TestInvalidateExperimentFiles(t *testing.T) {
	installListing(t, Files{{Name: "a", Path: "a"}})
	_ = fill(experimentListings, listingKey("other"), func() (Files, error) { return nil, nil })

	InvalidateExperimentFiles("exp")

	experimentListings.mu.Lock()
	defer experimentListings.mu.Unlock()

	if _, ok := experimentListings.entries[listingKey("exp")]; ok {
		t.Fatal("expected the experiment's listing to be dropped")
	}

	if _, ok := experimentListings.entries[listingKey("other")]; !ok {
		t.Fatal("expected other experiments' listings to be kept")
	}
}

// A listing a node failed to answer is an error, not fewer files, and is not
// reused; a listing reports when it was read, also when reused.
//
//nolint:paralleltest // replaces the listing cache and minimega
func TestListingErrorsPropagate(t *testing.T) {
	installListing(t, nil)
	experimentListings.invalidateAll()

	var failing atomic.Bool

	failing.Store(true)

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.HasPrefix(cmd.Base, "mesh send all file list") && failing.Load() {
			return []*minicli.Response{{Host: "compute1", Error: "timed out"}}
		}

		if strings.HasPrefix(cmd.Base, "file list") {
			return []*minicli.Response{mmtest.Tabular("head", []string{"dir", "name", "size", "modified"},
				[]string{"", "exp/files/notes.txt", "5", "2026-01-01T00:00:00Z"})}
		}

		return nil
	})

	var files MMClusterFiles

	if listed, _, err := files.GetExperimentFilesObserved("exp", ""); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("listing with a node timing out = %v, %v; want its error", listed, err)
	}

	failing.Store(false)

	before := time.Now()

	listed, first, err := files.GetExperimentFilesObserved("exp", "")
	if err != nil || len(listed) != 1 || first.At.Before(before) || !first.Known {
		t.Fatalf("listing once the node answers = %v, %+v, %v; want notes.txt, read now", listed, first, err)
	}

	_, again, err := files.GetExperimentFilesObserved("exp", "")
	if err != nil || !again.At.Equal(first.At) {
		t.Fatalf("reused listing observed at %v, %v; want the first's %v", again.At, err, first.At)
	}
}
