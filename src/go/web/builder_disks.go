package web

import (
	"errors"
	"sync"
	"time"

	"phenix/api/disk"
)

// builderDiskListTTL is how long a listing of the server disk images stays
// in use. A listing runs minimega commands, and the Publish dialog asks for
// a dry run after each pause in editing. Thus the routes that compare a
// document with the server images share one listing for this long.
const builderDiskListTTL = 10 * time.Second

// builderDisksUnlisted is why a package's disk images are not checked when
// the server's images cannot be listed.
const builderDisksUnlisted = "The server's disk images could not be listed."

// errBuilderDiskListAborted is what the requests sharing a listing of the
// disk images get when the listing ended without returning.
var errBuilderDiskListAborted = errors.New("listing the disk images did not finish")

// builderDiskLister lists the server disk images for the Builder routes that
// compare a document with them: package resolve, the dry run of a
// publication and the preflight disks check. A listing that succeeded is
// reused for ttl. A request that asks while a listing runs waits for it and
// shares its result, and does not start another. Thus concurrent requests
// run the minimega commands once. A failed listing is not reused. Callers
// must not change the slice they get, because other requests share it.
type builderDiskLister struct {
	// source lists the images, as GET /disks lists them.
	source func() ([]disk.Details, error)
	// now is the time a listing is made and reused at.
	now func() time.Time
	ttl time.Duration

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
		mu:      sync.Mutex{},
		running: nil,
		last:    nil,
		lastAt:  time.Time{},
		hasLast: false,
	}
}

// images returns the server disk images. It returns the last listing when
// it succeeded less than ttl ago. Else it returns the result of the listing
// in progress, else that of a new listing.
func (l *builderDiskLister) images() ([]disk.Details, error) {
	l.mu.Lock()

	if l.hasLast && l.now().Sub(l.lastAt) < l.ttl {
		images := l.last
		l.mu.Unlock()

		return images, nil
	}

	if listing := l.running; listing != nil {
		l.mu.Unlock()
		<-listing.done

		return listing.images, listing.err
	}

	listing := &builderDiskListing{done: make(chan struct{}), images: nil, err: errBuilderDiskListAborted}
	l.running = listing
	l.mu.Unlock()

	defer l.finish(listing)

	listing.images, listing.err = l.source()

	return listing.images, listing.err
}

// finish ends a listing: it keeps one that succeeded for the requests of
// the next ttl, and lets the requests waiting for it go on. A listing whose
// source never returned keeps errBuilderDiskListAborted.
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

// withBuilderDisks sets how the server's disk images are listed.
func withBuilderDisks(list func() ([]disk.Details, error)) builderOption {
	return func(api *builderAPI) { api.disks = newBuilderDiskLister(list, time.Now) }
}
