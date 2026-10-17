package file

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"phenix/util/flight"
)

// listingTTL is how long an experiment's file listing is reused. Listing asks
// every mesh node for its files (`mesh send all file list ... recursive`) and
// then the headnode, over the minimega connection every other request waits
// on, while the Files tab asks again for each search, sort, and page, and the
// VM details modal asks for the snapshots. Changes phenix itself makes to the
// files invalidate the listing; changes made elsewhere (another phenix
// process, SCORCH components, a VM writing to a mount) show within this long.
const listingTTL = 5 * time.Second

// experimentListings caches the MMClusterFiles listing of each experiment.
var experimentListings = newListingCache(listingTTL) //nolint:gochecknoglobals // process-wide cache

// InvalidateExperimentFiles drops the cached file listing of the experiment,
// so the next listing asks minimega again. Call it after phenix creates,
// moves, or deletes files in the experiment's files directory.
func InvalidateExperimentFiles(exp string) {
	experimentListings.invalidate(listingKey(exp))
}

// invalidateAllExperimentFiles drops every cached file listing.
func invalidateAllExperimentFiles() {
	experimentListings.invalidateAll()
}

// listingKey is the cache key for an experiment's listing: the directory
// listed, relative to minimega's files directory.
func listingKey(exp string) string {
	return exp + "/files/"
}

// listingCache reuses file listings for a short while and merges concurrent
// identical listings into one. It is safe for concurrent use.
type listingCache struct {
	ttl     time.Duration
	flights flight.Group[listing]

	mu      sync.Mutex
	entries map[string]listing  // listings that succeeded
	running map[string]*listing // listings in flight that may still be kept
}

// listing is one listing's files, as they were when it started.
type listing struct {
	files   Files
	started time.Time
}

func newListingCache(ttl time.Duration) *listingCache {
	return &listingCache{
		ttl: ttl, flights: flight.Group[listing]{}, mu: sync.Mutex{},
		entries: make(map[string]listing), running: make(map[string]*listing),
	}
}

// getContext returns a copy of a recent listing for key, or of one from list,
// shared with concurrent callers (see flight.Group), with when it started. A
// failed listing is never reused.
func (c *listingCache) getContext(
	ctx context.Context,
	key string,
	list func(ctx context.Context) (Files, error),
) (Files, time.Time, error) {
	if l, ok := c.recent(key); ok {
		return cloneFiles(l.files), l.started, nil
	}

	l, err := c.flights.Do(ctx, key, func(ctx context.Context, _ func()) (listing, error) {
		// another caller's listing may have finished since recent was checked
		if l, ok := c.recent(key); ok {
			return l, nil
		}

		return c.run(ctx, key, list)
	})

	return cloneFiles(l.files), l.started, err
}

// run lists key's files, keeping the listing unless it failed or key was
// invalidated meanwhile.
func (c *listingCache) run(
	ctx context.Context,
	key string,
	list func(ctx context.Context) (Files, error),
) (listing, error) {
	l := &listing{files: nil, started: time.Now()}

	c.mu.Lock()
	c.running[key] = l
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		if c.running[key] == l {
			delete(c.running, key)
		}
	}()

	files, err := list(ctx)
	if err != nil {
		return listing{files: files, started: l.started}, err
	}

	l.files = files

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running[key] == l {
		c.entries[key] = *l
	}

	return *l, nil
}

// recent returns key's listing unless it is too old to reuse, pruning those
// that are.
func (c *listingCache) recent(key string) (listing, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, l := range c.entries {
		if time.Since(l.started) >= c.ttl {
			delete(c.entries, k)
		}
	}

	l, ok := c.entries[key]

	return l, ok
}

// invalidate drops the listing for key. A listing in flight still answers the
// callers already waiting on it, as they asked before the change, but is not
// kept, and later callers start a new one.
func (c *listingCache) invalidate(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	delete(c.running, key)
	c.mu.Unlock()

	c.flights.Forget(key)
}

// invalidateAll drops every listing, as invalidate does.
func (c *listingCache) invalidateAll() {
	c.mu.Lock()
	keys := slices.Collect(maps.Keys(c.running))
	clear(c.entries)
	clear(c.running)
	c.mu.Unlock()

	for _, key := range keys {
		c.flights.Forget(key)
	}
}

// cloneFiles copies a cached listing for a caller, who may sort, filter, or
// append to it.
func cloneFiles(files Files) Files {
	if files == nil {
		return nil
	}

	out := make(Files, len(files))

	for i, f := range files {
		f.Categories = slices.Clone(f.Categories)
		out[i] = f
	}

	return out
}
