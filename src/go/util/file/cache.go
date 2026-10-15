package file

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// listingTTL is how long an experiment's file listing is reused. Listing asks
// every mesh node for its files (`mesh send all file list ... recursive`) and
// then the headnode, over the minimega connection every other request waits
// on, while the Files tab asks again for each search, sort, and page, and the
// VM details modal asks for the snapshots. Changes phenix itself makes to the
// files invalidate the listing; changes made elsewhere (another phenix
// process, SCORCH components, a VM writing to a mount) show within this long.
const listingTTL = 5 * time.Second

// errListingAborted is what the callers waiting on a listing get when the
// listing panicked, instead of an empty listing that would read as no files.
var errListingAborted = errors.New("shared file listing aborted before returning a result")

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
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*listing
}

// listing is one run of a listing: in flight until done is closed, then its
// result, reused until it expires or is invalidated.
type listing struct {
	done chan struct{}

	// cancel cancels the run's context; waiting counts the callers still
	// waiting for the run in flight, whose context is not done. Both are
	// guarded by the cache's mu.
	cancel  context.CancelFunc
	waiting int

	// set before done is closed, read only after
	files Files
	err   error // errListingAborted if list did not return

	// when the run started: the listing shows the files as they were then
	started time.Time
}

func newListingCache(ttl time.Duration) *listingCache {
	return &listingCache{ttl: ttl, mu: sync.Mutex{}, entries: make(map[string]*listing)}
}

// get is getContext for a caller that waits however long the listing takes.
func (c *listingCache) get(key string, list func() (Files, error)) (Files, time.Time, error) {
	return c.getContext(context.Background(), key, func(context.Context) (Files, error) { return list() })
}

// getContext returns the listing for key: a copy of a recent one, the result
// of an identical one in flight, or a fresh one from list, with when that
// listing started. A failed listing is returned to its waiters but never
// reused. If list panics, the panic carries on up the caller that ran it, and
// every caller waiting on the listing gets errListingAborted.
//
// list runs with a context of its own, canceled once every caller waiting for
// the listing has stopped because the caller's ctx is done, so a listing no
// one waits for gives up its queued minimega commands (see mmcli.RunContext).
// A caller waiting for another's listing returns ctx's error as soon as ctx
// is done; the caller running list returns once list does.
func (c *listingCache) getContext(
	ctx context.Context,
	key string,
	list func(ctx context.Context) (Files, error),
) (Files, time.Time, error) {
	c.mu.Lock()

	if l, ok := c.entries[key]; ok && !c.expired(l) {
		select {
		case <-l.done:
			c.mu.Unlock()

			return cloneFiles(l.files), l.started, l.err
		default:
		}

		stop := c.join(ctx, key, l)

		c.mu.Unlock()

		defer stop()

		select {
		case <-l.done:
			return cloneFiles(l.files), l.started, l.err
		case <-ctx.Done():
			return nil, time.Time{}, fmt.Errorf("waiting for the file listing: %w", ctx.Err())
		}
	}

	c.prune()

	listCtx, cancel := context.WithCancel(context.Background())

	l := &listing{done: make(chan struct{}), files: nil, err: nil, started: time.Now(), cancel: cancel, waiting: 0}
	c.entries[key] = l
	stop := c.join(ctx, key, l)

	c.mu.Unlock()

	defer stop()

	completed := false

	// Release the waiters even if list panics, telling them it did.
	defer func() {
		if !completed {
			l.err = errListingAborted
		}

		c.mu.Lock()

		// A failed listing is not reused. One invalidated while in flight is no
		// longer in entries (see invalidate).
		if l.err != nil && c.entries[key] == l {
			delete(c.entries, key)
		}

		c.mu.Unlock()

		cancel()
		close(l.done)
	}()

	l.files, l.err = list(listCtx)
	completed = true

	return cloneFiles(l.files), l.started, l.err
}

// join counts a caller waiting for l, a listing in flight, until the caller's
// ctx is done. Once no caller is left waiting, l is dropped, so no later
// caller joins a listing no one waits for, and its context is canceled. The
// returned function stops the count's watch on ctx, once the caller is done
// waiting. c.mu must be held.
func (c *listingCache) join(ctx context.Context, key string, l *listing) func() bool {
	l.waiting++

	return context.AfterFunc(ctx, func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		if l.waiting--; l.waiting > 0 {
			return
		}

		if c.entries[key] == l {
			delete(c.entries, key)
		}

		l.cancel()
	})
}

// expired reports whether a finished listing is too old to reuse. A listing in
// flight is never expired. c.mu must be held.
func (c *listingCache) expired(l *listing) bool {
	select {
	case <-l.done:
		return time.Since(l.started) >= c.ttl
	default:
		return false
	}
}

// prune drops expired listings, so experiments no longer looked at do not
// hold on to theirs. c.mu must be held.
func (c *listingCache) prune() {
	for key, l := range c.entries {
		if c.expired(l) {
			delete(c.entries, key)
		}
	}
}

// invalidate drops the listing for key. A listing in flight still answers the
// callers already waiting on it, as they asked before the change, but later
// callers start a new one.
func (c *listingCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key)
}

// invalidateAll drops every listing, as invalidate does.
func (c *listingCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	clear(c.entries)
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
