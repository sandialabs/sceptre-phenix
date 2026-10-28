// Package capability keeps one process's capability grants: random tokens that let their holder read one capture
// without the session credentials of the user they were given to. Only a token's SHA-256 hash is kept, a grant is named
// by an ID derived from it, and a restart revokes every grant.
package capability

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// Op is an operation a grant may allow.
type Op string

const (
	// OpView reads a capture's bytes for WebShark to show.
	OpView Op = "view"
	// OpDownload downloads a capture as a file.
	OpDownload Op = "download"
	// OpWatch follows a capture's growth.
	OpWatch Op = "watch"
	// OpStream streams a running capture as pcap.
	OpStream Op = "stream"
)

var (
	// ErrNotFound is returned for a token that is unknown, expired, idle too
	// long, or past the time it could be used by.
	ErrNotFound = errors.New("capability not found")
	// ErrOperation is returned for a token that does not allow the operation.
	ErrOperation = errors.New("operation not granted")
)

// Target is the capture a grant reads.
type Target struct {
	Exp string
	// the experiment file (as listed) holding the capture, if known
	File string
	// a running capture: its VM and interface index
	VM        string
	Interface int
	// whether the capture is still running
	Live bool
	// a saved capture: where the headnode has its file
	Local string
}

// Grant is what a token allows, and to whom it was given.
type Grant struct {
	// not secret: the first 8 bytes of the token's hash, in hex
	ID string
	// the user the grant was given to
	User string
	// the raw session JWT the grant was given under, empty when the server
	// does not sign users in; never logged or sent
	Session string

	Target Target
	Ops    []Op

	Created time.Time
	// when the grant ends, however it is used
	Expires time.Time
	// when set, the grant cannot be used after it, though what it opened
	// keeps going until Expires
	UseBy time.Time
	// when set, the grant ends once unused for this long
	IdleTimeout time.Duration

	LastUsed time.Time
	// when the user's access to the target was last confirmed
	Checked time.Time
	Uses    int

	// closed when the grant ends: revoked, expired or evicted
	Done <-chan struct{}
}

// Allows reports whether the grant allows op.
func (g Grant) Allows(op Op) bool {
	return slices.Contains(g.Ops, op)
}

type key [sha256.Size]byte

type entry struct {
	grant Grant
	done  chan struct{}
	timer *time.Timer
}

// Registry holds grants by their token's hash. It is safe for concurrent use
// and does no I/O while it holds its lock.
type Registry struct {
	maxTotal, maxPerUser int
	now                  func() time.Time

	mu     sync.Mutex
	grants map[key]*entry
	byID   map[string]key
}

// New returns a registry that keeps at most maxTotal grants, and at most
// maxPerUser of each user's, giving up the least recently used beyond that.
// now is its clock: [time.Now] when nil.
func New(maxTotal, maxPerUser int, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}

	return &Registry{
		maxTotal:   maxTotal,
		maxPerUser: maxPerUser,
		now:        now,
		mu:         sync.Mutex{},
		grants:     make(map[key]*entry),
		byID:       make(map[string]key),
	}
}

func hash(token string) key {
	return sha256.Sum256([]byte(token))
}

// IDOf returns the ID of the grant the token names.
func IDOf(token string) string {
	h := hash(token)

	return hex.EncodeToString(h[:8])
}

// Mint keeps g as the grant of token, returning it with its ID, creation
// time and Done channel set. g's Expires must be set; the grant ends then.
func (r *Registry) Mint(token string, g Grant) Grant {
	var (
		k    = hash(token)
		now  = r.now()
		done = make(chan struct{})
	)

	g.ID = IDOf(token)
	g.Created, g.LastUsed, g.Checked = now, now, now
	g.Uses = 0
	g.Ops = slices.Clone(g.Ops)
	g.Done = done

	e := &entry{grant: g, done: done, timer: nil}

	r.mu.Lock()
	defer r.mu.Unlock()

	if old, ok := r.grants[k]; ok {
		r.remove(k, old)
	}

	r.sweep(now)
	r.evict(func(other Grant) bool { return other.User == g.User }, r.maxPerUser-1)
	r.evict(func(Grant) bool { return true }, r.maxTotal-1)

	r.grants[k] = e
	r.byID[g.ID] = k

	e.timer = time.AfterFunc(g.Expires.Sub(now), func() {
		r.mu.Lock()
		defer r.mu.Unlock()

		if r.grants[k] == e {
			r.remove(k, e)
		}
	})

	return g
}

// Use records a use of the grant token names for op and returns it. A grant
// that does not allow op is returned with ErrOperation and not counted as
// used.
func (r *Registry) Use(token string, op Op) (Grant, error) {
	k := hash(token)
	now := r.now()

	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.grants[k]
	if !ok {
		return Grant{}, ErrNotFound
	}

	g := &e.grant

	switch {
	case !now.Before(g.Expires),
		g.IdleTimeout > 0 && now.Sub(g.LastUsed) >= g.IdleTimeout:
		r.remove(k, e)

		return Grant{}, ErrNotFound
	case !g.UseBy.IsZero() && !now.Before(g.UseBy):
		// what a used grant opened may still be going, and ends with it
		if g.Uses == 0 {
			r.remove(k, e)
		}

		return Grant{}, ErrNotFound
	case !g.Allows(op):
		return *g, ErrOperation
	}

	g.Uses++
	g.LastUsed = now

	return *g, nil
}

// Update changes the grant with the given ID, if it is still kept, with f,
// which must not block. f cannot change the grant's ID or Done channel.
func (r *Registry) Update(id string, f func(*Grant)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	k, ok := r.byID[id]
	if !ok {
		return
	}

	e := r.grants[k]
	f(&e.grant)
	e.grant.ID, e.grant.Done = id, e.done
}

// Revoke ends the grant with the given ID if it was given to user, and
// reports whether it did.
func (r *Registry) Revoke(id, user string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	k, ok := r.byID[id]
	if !ok || r.grants[k].grant.User != user {
		return false
	}

	r.remove(k, r.grants[k])

	return true
}

// RevokeWhere ends every grant match reports true for, and returns how many
// it ended. match must not block.
func (r *Registry) RevokeWhere(match func(Grant) bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0

	for k, e := range r.grants {
		if match(e.grant) {
			r.remove(k, e)
			n++
		}
	}

	return n
}

// List returns the user's grants for the experiment exp that have not ended,
// oldest first, and by ID among those minted at once.
func (r *Registry) List(user, exp string) []Grant {
	now := r.now()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweep(now)

	var list []Grant

	for _, e := range r.grants {
		if e.grant.User == user && e.grant.Target.Exp == exp {
			list = append(list, e.grant)
		}
	}

	slices.SortFunc(list, func(a, b Grant) int {
		return cmp.Or(a.Created.Compare(b.Created), strings.Compare(a.ID, b.ID))
	})

	return list
}

// sweep drops the grants that can no longer be used, nor have anything open.
func (r *Registry) sweep(now time.Time) {
	for k, e := range r.grants {
		g := e.grant

		if !now.Before(g.Expires) || g.IdleTimeout > 0 && now.Sub(g.LastUsed) >= g.IdleTimeout ||
			!g.UseBy.IsZero() && !now.Before(g.UseBy) && g.Uses == 0 {
			r.remove(k, e)
		}
	}
}

// evict drops the least recently used of the grants match reports true for
// until at most keep are left.
func (r *Registry) evict(match func(Grant) bool, keep int) {
	for {
		var (
			oldest    *entry
			oldestKey key
			n         int
		)

		for k, e := range r.grants {
			if !match(e.grant) {
				continue
			}

			n++

			if oldest == nil || e.grant.LastUsed.Before(oldest.grant.LastUsed) {
				oldest, oldestKey = e, k
			}
		}

		if n <= keep || oldest == nil {
			return
		}

		r.remove(oldestKey, oldest)
	}
}

func (r *Registry) remove(k key, e *entry) {
	delete(r.grants, k)
	delete(r.byID, e.grant.ID)

	if e.timer != nil {
		e.timer.Stop()
	}

	close(e.done)
}
