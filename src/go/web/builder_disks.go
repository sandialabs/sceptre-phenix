package web

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"phenix/api/disk"
	"phenix/util/plog"
)

// builderDiskListTTL is how long a listing of the server disk images stays
// in use. A listing runs minimega commands, and the Publish dialog asks for
// a dry run after each pause in editing. Thus the routes that compare a
// document with the server images share one listing for this long.
const builderDiskListTTL = 10 * time.Second

// builderDiskListWait is the longest time a request waits for a listing of
// the disk images. Then it goes on as if the images could not be listed.
const builderDiskListWait = 10 * time.Second

// builderDisksUnlisted is why a package's disk images are not checked when
// the server's images cannot be listed.
const builderDisksUnlisted = "The server could not list its disk images."

var (
	// errBuilderDiskListAborted is what the requests sharing a listing of the
	// disk images get when the listing ended without returning.
	errBuilderDiskListAborted = errors.New("listing the disk images did not finish")

	// errBuilderDiskListWait is what a request gets when it stops waiting
	// for a listing: its context ended, or it waited [builderDiskListWait].
	errBuilderDiskListWait = errors.New("the server did not list the disk images in time")
)

// builderDiskLister shares one disk image listing between concurrent requests, and reuses a success for ttl.
// The minimega commands cannot be stopped, so a listing runs in its own goroutine. Callers must not change the slice.
type builderDiskLister struct {
	// source lists the images, as GET /disks lists them.
	source func() ([]disk.Details, error)
	// now is the time a listing is made and reused at.
	now  func() time.Time
	ttl  time.Duration
	wait time.Duration

	mu sync.Mutex
	// running is the listing in progress, nil for none.
	running *builderDiskListing
	// last is the last listing that succeeded, made at lastAt. hasLast is
	// false until there is one.
	last    []disk.Details
	lastAt  time.Time
	hasLast bool
}

// builderDiskListing is one run of a lister's source, which the requests
// that ask while it runs wait for.
type builderDiskListing struct {
	done   chan struct{}
	images []disk.Details
	err    error
}

// newBuilderDiskLister returns a lister of the disk images source lists,
// reading the time from now.
func newBuilderDiskLister(source func() ([]disk.Details, error), now func() time.Time) *builderDiskLister {
	return &builderDiskLister{
		source:  source,
		now:     now,
		ttl:     builderDiskListTTL,
		wait:    builderDiskListWait,
		mu:      sync.Mutex{},
		running: nil,
		last:    nil,
		lastAt:  time.Time{},
		hasLast: false,
	}
}

// images returns the server disk images. It returns the last listing when
// it succeeded less than ttl ago. Else it returns the result of the listing
// in progress, else that of a new listing. It returns an error that matches
// errBuilderDiskListWait when ctx ends, or wait passes, before the listing
// ends. The callers then report the images as not listed.
func (l *builderDiskLister) images(ctx context.Context) ([]disk.Details, error) {
	l.mu.Lock()

	if l.hasLast && l.now().Sub(l.lastAt) < l.ttl {
		images := l.last
		l.mu.Unlock()

		return images, nil
	}

	listing := l.running
	if listing == nil {
		listing = &builderDiskListing{done: make(chan struct{}), images: nil, err: errBuilderDiskListAborted}
		l.running = listing

		go l.list(listing)
	}

	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, l.wait)
	defer cancel()

	select {
	case <-listing.done:
		return listing.images, listing.err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", errBuilderDiskListWait, ctx.Err())
	}
}

// list runs the source for listing, and then ends the listing (see
// [builderDiskLister.finish]). A source that panics leaves the listing
// with errBuilderDiskListAborted.
func (l *builderDiskLister) list(listing *builderDiskListing) {
	defer l.finish(listing)

	defer func() {
		if cause := recover(); cause != nil {
			plog.Error(plog.TypeSystem, "listing disk images for the builder panicked", "panic", cause)
		}
	}()

	listing.images, listing.err = l.source()
}

// finish ends a listing: it keeps one that succeeded for the requests of
// the next ttl, and lets the requests waiting for it go on.
func (l *builderDiskLister) finish(listing *builderDiskListing) {
	l.mu.Lock()

	l.running = nil

	if listing.err == nil {
		l.last, l.lastAt, l.hasLast = listing.images, l.now(), true
	}

	l.mu.Unlock()

	close(listing.done)
}

// builderDiskImages lists this server's disk images, as GET /disks lists
// them.
func builderDiskImages() ([]disk.Details, error) {
	return disk.GetImages("")
}
