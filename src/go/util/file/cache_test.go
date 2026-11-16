package file

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
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

	l := c.entries[key]
	l.started = l.started.Add(-d)
	c.entries[key] = l
}

// names returns the name of each file, in order.
func names(files Files) []string {
	out := make([]string, 0, len(files))

	for _, f := range files {
		out = append(out, f.Name)
	}

	return out
}

// countingList returns a listing function that counts its calls.
func countingList(calls *atomic.Int32, files Files) func() (Files, error) {
	return func() (Files, error) {
		calls.Add(1)

		return files, nil
	}
}

func TestListingCacheReusesUntilExpired(t *testing.T) {
	t.Parallel()

	cache := newTestCache()

	var calls atomic.Int32

	list := countingList(&calls, Files{{Name: "a.pcap", Path: "a.pcap"}})

	for range 3 {
		files, _, err := cache.get("exp/files/", list)
		if err != nil {
			t.Fatalf("get(exp/files/) error = %v", err)
		}

		if diff := cmp.Diff([]string{"a.pcap"}, names(files)); diff != "" {
			t.Errorf("get(exp/files/) mismatch (-want +got):\n%s", diff)
		}
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("listings after 3 get(exp/files/) within the TTL = %d, want 1", got)
	}

	cache.age("exp/files/", cache.ttl)

	if _, _, err := cache.get("exp/files/", list); err != nil {
		t.Fatalf("get(exp/files/) once expired error = %v", err)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("listings after get(exp/files/) once expired = %d, want 2", got)
	}
}

func TestListingCacheKeysByExperiment(t *testing.T) {
	t.Parallel()

	cache := newTestCache()

	var calls atomic.Int32

	if got := listingKey("a"); got != "a/files/" {
		t.Errorf("listingKey(a) = %q, want a/files/", got)
	}

	_ = fill(cache, listingKey("a"), countingList(&calls, nil))
	_ = fill(cache, listingKey("b"), countingList(&calls, nil))
	_ = fill(cache, listingKey("a"), countingList(&calls, nil))

	if got := calls.Load(); got != 2 {
		t.Errorf("listings after filling experiments a, b and a again = %d, want 2", got)
	}
}

// A listing that was in flight when the files changed may miss the change, so
// it must not be reused by callers who ask after the change, who list anew at
// once rather than wait for it.
func TestListingCacheDropsListingInvalidatedInFlight(t *testing.T) {
	t.Parallel()

	for name, drop := range map[string]func(*listingCache){
		"invalidate":    func(c *listingCache) { c.invalidate("k") },
		"invalidateAll": func(c *listingCache) { c.invalidateAll() },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				cache   = newTestCache()
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

					return Files{{Name: "stale", Path: "stale"}}, nil
				})
			}()

			<-entered
			drop(cache)

			var fresh Files

			polltest.Within(t, "listing while the dropped listing runs", func() {
				var err error

				fresh, _, err = cache.get("k", countingList(&calls, Files{{Name: "fresh", Path: "fresh"}}))
				if err != nil {
					t.Errorf("get(k) after %s error = %v", name, err)
				}
			})

			close(release)
			<-done

			again, _, err := cache.get("k", countingList(&calls, nil))
			if err != nil {
				t.Fatalf("get(k) once the dropped listing ended error = %v", err)
			}

			if diff := cmp.Diff([]string{"fresh", "fresh"}, append(names(fresh), names(again)...)); diff != "" {
				t.Errorf("get(k) after %s, then again, mismatch (-want +got):\n%s", name, diff)
			}

			if got := calls.Load(); got != 2 {
				t.Errorf("listings after %s during the first = %d, want 2", name, got)
			}
		})
	}
}

// However concurrent callers interleave with a listing, a key that is never
// invalidated is listed once, and every caller gets that listing.
func TestListingCacheListsOnceForConcurrentCallers(t *testing.T) {
	t.Parallel()

	const callers = 16

	for round := range 20 {
		var (
			cache = newTestCache()
			calls atomic.Int32
			start = make(chan struct{})
			got   = make(chan []string, callers)
		)

		for range callers {
			go func() {
				<-start

				files, _, err := cache.get("k", countingList(&calls, Files{{Name: "a", Path: "a"}}))
				if err != nil {
					t.Errorf("round %d: get(k) error = %v", round, err)
				}

				got <- names(files)
			}()
		}

		close(start)

		for range callers {
			if diff := cmp.Diff([]string{"a"}, <-got); diff != "" {
				t.Errorf("round %d: get(k) mismatch (-want +got):\n%s", round, diff)
			}
		}

		if n := calls.Load(); n != 1 {
			t.Errorf("round %d: listings for %d concurrent get(k) = %d, want 1", round, callers, n)
		}
	}
}

func TestListingCacheDoesNotReuseFailures(t *testing.T) {
	t.Parallel()

	cache := newTestCache()

	var calls atomic.Int32

	errUnavailable := errors.New("minimega unavailable")
	failing := func() (Files, error) {
		calls.Add(1)

		return nil, errUnavailable
	}

	if _, _, err := cache.get("k", failing); !errors.Is(err, errUnavailable) {
		t.Errorf("get(k) with a failing listing error = %v, want %v", err, errUnavailable)
	}

	if _, _, err := cache.get("k", countingList(&calls, nil)); err != nil {
		t.Errorf("get(k) after a failure error = %v, want nil", err)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("listings after get(k) failed then succeeded = %d, want 2", got)
	}

	// unlike a failure, a success is kept even though its only caller left during it
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	_, _, err := cache.getContext(ctx, "kept", func(listCtx context.Context) (Files, error) {
		cancel()
		<-listCtx.Done()

		return Files{{Name: "a", Path: "a"}}, nil
	})
	if err != nil {
		t.Fatalf("getContext(kept) whose caller left error = %v", err)
	}

	var relisted atomic.Int32

	files, _, err := cache.get("kept", countingList(&relisted, nil))
	if err != nil {
		t.Fatalf("get(kept) error = %v", err)
	}

	if diff := cmp.Diff([]string{"a"}, names(files)); diff != "" {
		t.Errorf("get(kept) mismatch (-want +got):\n%s", diff)
	}

	if got := relisted.Load(); got != 0 {
		t.Errorf("new listings for get(kept) = %d, want 0", got)
	}
}

func TestListingCacheHandsOutCopies(t *testing.T) {
	t.Parallel()

	cache := newTestCache()

	list := func() (Files, error) {
		return Files{
			{Name: "b", Path: "b", Categories: []string{"Packet Capture"}},
			{Name: "a", Path: "a", Categories: []string{"Unknown"}},
		}, nil
	}

	first, _, err := cache.get("k", list)
	if err != nil {
		t.Fatalf("get(k) error = %v", err)
	}

	first.SortByName(true)
	first[0].Categories[0] = "changed"

	second, _, err := cache.get("k", list)
	if err != nil {
		t.Fatalf("get(k) again error = %v", err)
	}

	want, _ := list()
	if diff := cmp.Diff(want, second, cmpopts.IgnoreUnexported(File{})); diff != "" {
		t.Errorf("get(k) after the first caller changed its copy mismatch (-want +got):\n%s", diff)
	}
}

func TestListingCachePrunesExpiredListings(t *testing.T) {
	t.Parallel()

	cache := newTestCache()

	_ = fill(cache, "old", countingList(new(atomic.Int32), nil))

	cache.age("old", cache.ttl)

	_ = fill(cache, "new", countingList(new(atomic.Int32), nil))

	cache.mu.Lock()
	defer cache.mu.Unlock()

	if _, ok := cache.entries["old"]; ok {
		t.Error("entries after filling new hold the expired old, want it pruned")
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

	for _, tc := range []struct {
		filter string
		want   []string
	}{
		{filter: "", want: []string{"vm_eth0.pcap", "notes.txt"}},
		{filter: "notes", want: []string{"notes.txt"}},
		{filter: "and", want: []string{}}, // unparsable
	} {
		got, err := files.GetExperimentFiles("exp", tc.filter)
		if err != nil {
			t.Errorf("GetExperimentFiles(exp, %q) error = %v", tc.filter, err)

			continue
		}

		if diff := cmp.Diff(tc.want, names(got), cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
			t.Errorf("GetExperimentFiles(exp, %q) mismatch (-want +got):\n%s", tc.filter, diff)
		}
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
	if err != nil {
		t.Fatalf("GetExperimentSnapshots(exp) error = %v", err)
	}

	if diff := cmp.Diff([]string{"vm__snap"}, snapshots); diff != "" {
		t.Errorf("GetExperimentSnapshots(exp) mismatch (-want +got):\n%s", diff)
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

// recordingExistingFiles also records the files DeleteExistingFiles deletes,
// each with the prefix "existing ".
type recordingExistingFiles struct {
	*recordingFiles
}

//nolint:unparam // existingFilesDeleter fixes the signature
func (f recordingExistingFiles) DeleteExistingFiles(names []string) error {
	for _, name := range names {
		f.deleted = append(f.deleted, "existing "+name)
	}

	return nil
}

// DeleteFile and DeleteExistingFiles, with or without a cluster that deletes
// only existing files, delete the files and drop every cached listing.
func TestDeletingFilesDropsCachedListings(t *testing.T) { //nolint:paralleltest // replaces package globals
	for name, tc := range map[string]struct {
		remove   func() error
		existing bool // the cluster deletes only existing files
		want     []string
	}{
		"DeleteFile": {
			remove: func() error { return DeleteFile("exp/files/a") },
			want:   []string{"exp/files/a"},
		},
		"DeleteExistingFiles one file at a time": {
			remove: func() error { return DeleteExistingFiles([]string{"exp/files/a", "exp/files/b"}) },
			want:   []string{"exp/files/a", "exp/files/b"},
		},
		"DeleteExistingFiles through the cluster": {
			remove:   func() error { return DeleteExistingFiles([]string{"exp/files/a"}) },
			existing: true,
			want:     []string{"existing exp/files/a"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			installListing(t, Files{{Name: "a", Path: "a"}})

			original := DefaultClusterFiles

			t.Cleanup(func() { DefaultClusterFiles = original })

			files := &recordingFiles{ClusterFiles: nil, deleted: nil}
			DefaultClusterFiles = files

			if tc.existing {
				DefaultClusterFiles = recordingExistingFiles{recordingFiles: files}
			}

			if err := tc.remove(); err != nil {
				t.Fatalf("%s error = %v", name, err)
			}

			if diff := cmp.Diff(tc.want, files.deleted); diff != "" {
				t.Errorf("%s deleted files mismatch (-want +got):\n%s", name, diff)
			}

			experimentListings.mu.Lock()
			defer experimentListings.mu.Unlock()

			if n := len(experimentListings.entries); n != 0 {
				t.Errorf("cached listings after %s = %d, want 0", name, n)
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
		t.Error("InvalidateExperimentFiles(exp) kept exp's listing, want it dropped")
	}

	if _, ok := experimentListings.entries[listingKey("other")]; !ok {
		t.Error("InvalidateExperimentFiles(exp) dropped other's listing, want it kept")
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

	listed, _, err := files.GetExperimentFilesObserved(t.Context(), "exp", "")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("GetExperimentFilesObserved(exp) with a node timing out = %v, %v; want the node's error", listed, err)
	}

	failing.Store(false)

	before := time.Now()

	listed, first, err := files.GetExperimentFilesObserved(t.Context(), "exp", "")
	if err != nil {
		t.Fatalf("GetExperimentFilesObserved(exp) once the node answers error = %v", err)
	}

	if diff := cmp.Diff([]string{"notes.txt"}, names(listed)); diff != "" {
		t.Errorf("GetExperimentFilesObserved(exp) once the node answers mismatch (-want +got):\n%s", diff)
	}

	if first.At.Before(before) || !first.Known {
		t.Errorf("GetExperimentFilesObserved(exp) once the node answers observed %+v, want known and at or after %v", first, before)
	}

	_, again, err := files.GetExperimentFilesObserved(t.Context(), "exp", "")
	if err != nil {
		t.Fatalf("GetExperimentFilesObserved(exp) again error = %v", err)
	}

	if !again.At.Equal(first.At) {
		t.Errorf("GetExperimentFilesObserved(exp) again observed at %v, want the first listing's %v", again.At, first.At)
	}
}

// A caller whose context ends returns at once while the listing goes on for those still waiting;
// once none is left, the listing's context is canceled, and a later call lists anew.
func TestListingCacheCancelsAListingNoOneWaitsFor(t *testing.T) {
	t.Parallel()

	var (
		cache      = newTestCache()
		running    = make(chan context.Context, 1)
		leaderDone = make(chan error, 1)
		joinedDone = make(chan error, 1)

		leaderCtx, cancelLeader = context.WithCancel(t.Context())
		joinedCtx, cancelJoined = context.WithCancel(t.Context())
	)

	t.Cleanup(cancelLeader)
	t.Cleanup(cancelJoined)

	go func() {
		_, _, err := cache.getContext(leaderCtx, "k", func(ctx context.Context) (Files, error) {
			running <- ctx
			<-ctx.Done()

			return nil, ctx.Err()
		})
		leaderDone <- err
	}()

	listCtx := <-running

	go func() {
		_, _, err := cache.getContext(joinedCtx, "k", func(context.Context) (Files, error) { return nil, nil })
		joinedDone <- err
	}()

	polltest.Until(t, "the second caller waits for the listing", func() bool { return cache.flights.Waiting("k") == 2 })

	cancelJoined()

	if err := <-joinedDone; !errors.Is(err, context.Canceled) {
		t.Errorf("getContext(k) of the caller that left error = %v, want %v", err, context.Canceled)
	}

	if err := listCtx.Err(); err != nil {
		t.Errorf("listing context error while its first caller waits = %v, want nil", err)
	}

	cancelLeader()

	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Errorf("getContext(k) of the first caller error = %v, want its listing's %v", err, context.Canceled)
	}

	var calls atomic.Int32

	if _, _, err := cache.get("k", countingList(&calls, nil)); err != nil {
		t.Errorf("get(k) after the canceled listing error = %v, want nil", err)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("listings for get(k) after the canceled listing = %d, want 1", got)
	}
}

// get is getContext for a caller that waits however long the listing takes.
func (c *listingCache) get(key string, list func() (Files, error)) (Files, time.Time, error) {
	return c.getContext(context.Background(), key, func(context.Context) (Files, error) { return list() })
}

// A recorded netflow capture lists as plain text, and its metadata does not
// list; other JSON files do.
//
//nolint:paralleltest // replaces the listing cache and minimega
func TestListingHidesNetflowSidecars(t *testing.T) {
	installListing(t, nil)
	experimentListings.invalidateAll()

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.HasPrefix(cmd.Base, "file list") {
			return []*minicli.Response{mmtest.Tabular("head", []string{"dir", "name", "size", "modified"},
				[]string{"", "exp/files/netflow/20261028T153012.345Z.txt", "5", "2026-01-01T00:00:00Z"},
				[]string{"", "exp/files/netflow/20261028T153012.345Z.json", "5", "2026-01-01T00:00:00Z"},
				[]string{"", "exp/files/results.json", "5", "2026-01-01T00:00:00Z"})}
		}

		return nil
	})

	var files MMClusterFiles

	listed, _, err := files.GetExperimentFilesObserved(t.Context(), "exp", "")
	if err != nil {
		t.Fatalf("GetExperimentFilesObserved(exp) error = %v", err)
	}

	plain := make(map[string]bool)

	for _, f := range listed {
		plain[f.Path] = f.PlainText
	}

	want := map[string]bool{"netflow/20261028T153012.345Z.txt": true, "results.json": true}
	if diff := cmp.Diff(want, plain); diff != "" {
		t.Errorf("GetExperimentFilesObserved(exp) paths and plain text mismatch (-want +got):\n%s", diff)
	}
}
