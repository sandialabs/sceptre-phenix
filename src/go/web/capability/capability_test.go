package capability

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// clock is a test clock that moves only when told to.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{mu: sync.Mutex{}, now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// viewGrant is a grant of alice's for test-experiment, ending in an hour.
func viewGrant(c *clock) Grant {
	return Grant{
		User:    "alice",
		Session: "session-jwt",
		Target:  Target{Exp: "test-experiment", File: "a.pcap", VM: "", Interface: 0, Live: false, Local: "/a.pcap"},
		Ops:     []Op{OpView, OpDownload, OpWatch},
		Expires: c.Now().Add(time.Hour),
	}
}

func isDone(g Grant) bool {
	select {
	case <-g.Done:
		return true
	default:
		return false
	}
}

func TestUse(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	minted := r.Mint("router-secret.pcap", viewGrant(c))

	got, err := r.Use("router-secret.pcap", OpView)
	if err != nil {
		t.Fatalf("Use(router-secret.pcap, %v) error = %v", OpView, err)
	}

	want := minted
	want.Uses = 1

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Use(router-secret.pcap, %v) mismatch (-want +got):\n%s", OpView, diff)
	}

	if _, err := r.Use("router-secret.pcap", OpStream); !errors.Is(err, ErrOperation) {
		t.Errorf("Use(router-secret.pcap, %v) error = %v, want %v", OpStream, err, ErrOperation)
	}

	for _, token := range []string{"router-secret.pca", "Router-secret.pcap", ""} {
		if _, err := r.Use(token, OpView); !errors.Is(err, ErrNotFound) {
			t.Errorf("Use(%q, %v) error = %v, want %v", token, OpView, err, ErrNotFound)
		}
	}
}

func TestIDIsNotTheToken(t *testing.T) {
	r := New(10, 10, nil)

	token := "pcs_abcdefghijklmnopqrstuvwxyz"
	g := r.Mint(token, viewGrant(newClock()))

	if len(g.ID) != 16 {
		t.Errorf("Mint(%q).ID = %q, want 16 characters", token, g.ID)
	}

	if strings.Contains(token, g.ID) || strings.Contains(g.ID, "abcdef") {
		t.Errorf("Mint(%q).ID = %q, want no part of the token", token, g.ID)
	}

	if want := IDOf(token); g.ID != want {
		t.Errorf("Mint(%q).ID = %q, want IDOf(token) %q", token, g.ID, want)
	}
}

func TestIdleTimeout(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	g := viewGrant(c)
	g.IdleTimeout = 10 * time.Minute
	g = r.Mint("t", g)

	c.advance(9 * time.Minute)

	if _, err := r.Use("t", OpView); err != nil {
		t.Errorf("Use(t, %v) before the idle timeout error = %v", OpView, err)
	}

	// each use starts the idle timeout again
	c.advance(9 * time.Minute)

	if _, err := r.Use("t", OpView); err != nil {
		t.Errorf("Use(t, %v) again before the idle timeout error = %v", OpView, err)
	}

	c.advance(10 * time.Minute)

	if _, err := r.Use("t", OpView); !errors.Is(err, ErrNotFound) {
		t.Errorf("Use(t, %v) after the idle timeout error = %v, want %v", OpView, err, ErrNotFound)
	}

	if !isDone(g) {
		t.Error("Done of an idle grant is open, want it closed")
	}
}

func TestExpiry(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	g := viewGrant(c)
	g = r.Mint("t", g)

	c.advance(time.Hour)

	if _, err := r.Use("t", OpView); !errors.Is(err, ErrNotFound) {
		t.Errorf("Use(t, %v) at Expires error = %v, want %v", OpView, err, ErrNotFound)
	}

	if !isDone(g) {
		t.Error("Done of an expired grant is open, want it closed")
	}

	// the grant ends at Expires without being used again
	r = New(10, 10, nil)
	g = viewGrant(newClock())
	g.Expires = time.Now().Add(20 * time.Millisecond)
	g = r.Mint("t", g)

	select {
	case <-g.Done:
	case <-time.After(5 * time.Second):
		t.Error("Done is still open 5s after Expires, want it closed")
	}

	r.mu.Lock()
	n := len(r.grants)
	r.mu.Unlock()

	if n != 0 {
		t.Errorf("%d grants kept after expiry, want 0", n)
	}
}

func TestUseBy(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	g := viewGrant(c)
	g.Ops, g.UseBy = []Op{OpStream}, c.Now().Add(5*time.Minute)
	used := r.Mint("used", g)
	unused := r.Mint("unused", g)

	// any number of times before UseBy
	for range 3 {
		if _, err := r.Use("used", OpStream); err != nil {
			t.Errorf("Use(used, %v) before UseBy error = %v", OpStream, err)
		}
	}

	c.advance(5 * time.Minute)

	for _, token := range []string{"used", "unused"} {
		if _, err := r.Use(token, OpStream); !errors.Is(err, ErrNotFound) {
			t.Errorf("Use(%s, %v) after UseBy error = %v, want %v", token, OpStream, err, ErrNotFound)
		}
	}

	// what the used grant opened goes on until it is revoked or expires
	if isDone(used) {
		t.Error("Done of a used grant is closed at UseBy, want it open")
	}

	if !isDone(unused) {
		t.Error("Done of an unused grant is open at UseBy, want it closed")
	}

	if n := r.RevokeWhere(func(g Grant) bool { return g.ID == used.ID }); n != 1 {
		t.Errorf("RevokeWhere(the used grant) = %d, want 1", n)
	}

	if !isDone(used) {
		t.Error("Done of a revoked grant is open, want it closed")
	}
}

func TestRevoke(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	g := r.Mint("t", viewGrant(c))

	if r.Revoke(g.ID, "bob") {
		t.Error("Revoke(alice's grant, bob) = true, want false")
	}

	if _, err := r.Use("t", OpView); err != nil {
		t.Errorf("Use(t, %v) after another user's revoke error = %v", OpView, err)
	}

	if !r.Revoke(g.ID, "alice") {
		t.Error("Revoke(alice's grant, alice) = false, want true")
	}

	if !isDone(g) {
		t.Error("Done of a revoked grant is open, want it closed")
	}

	if _, err := r.Use("t", OpView); !errors.Is(err, ErrNotFound) {
		t.Errorf("Use(t, %v) after revoke error = %v, want %v", OpView, err, ErrNotFound)
	}

	if r.Revoke(g.ID, "alice") {
		t.Error("Revoke(a revoked grant, alice) = true, want false")
	}
}

func TestRevokeWhereAndList(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	a := r.Mint("a", viewGrant(c))

	c.advance(time.Second)

	other := viewGrant(c)
	other.Target.Exp = "other"
	b := r.Mint("b", other)

	bobs := viewGrant(c)
	bobs.User = "bob"
	r.Mint("c", bobs)

	if diff := cmp.Diff([]Grant{a}, r.List("alice", "test-experiment")); diff != "" {
		t.Errorf("List(alice, test-experiment) mismatch (-want +got):\n%s", diff)
	}

	if n := r.RevokeWhere(func(g Grant) bool { return g.User == "alice" }); n != 2 {
		t.Errorf("RevokeWhere(alice's grants) = %d, want 2", n)
	}

	if !isDone(a) || !isDone(b) {
		t.Errorf("Done of alice's revoked grants: a closed %t, b closed %t, want both closed", isDone(a), isDone(b))
	}

	if n := len(r.grants); n != 1 {
		t.Errorf("%d grants kept after RevokeWhere(alice's grants), want bob's 1", n)
	}

	if list := r.List("alice", "test-experiment"); len(list) != 0 {
		t.Errorf("List(alice, test-experiment) after revoke = %+v, want none", list)
	}
}

func TestUpdate(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	g := viewGrant(c)
	g.Target.Live = true
	g = r.Mint("t", g)

	r.Update(g.ID, func(g *Grant) {
		g.Target.Live, g.Target.Local = false, "/settled.pcap"
		g.ID, g.Done = "changed", nil
	})

	got, err := r.Use("t", OpView)
	if err != nil {
		t.Fatalf("Use(t, %v) after Update error = %v", OpView, err)
	}

	// Update keeps the ID and Done channel
	want := g
	want.Target.Live, want.Target.Local = false, "/settled.pcap"
	want.Uses = 1

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Use(t, %v) after Update mismatch (-want +got):\n%s", OpView, diff)
	}

	r.Update("unknown", func(*Grant) { t.Error("Update(unknown) called f, want no call") })
}

func TestEviction(t *testing.T) {
	c := newClock()
	r := New(4, 2, c.Now)

	minted := make(map[string]Grant)

	mint := func(token, user string) {
		c.advance(time.Second)

		g := viewGrant(c)
		g.User = user
		minted[token] = r.Mint(token, g)
	}

	done := func() map[string]bool {
		got := make(map[string]bool, len(minted))
		for token, g := range minted {
			got[token] = isDone(g)
		}

		return got
	}

	mint("a1", "alice")
	mint("a2", "alice")
	mint("b1", "bob")

	// alice's least recently used grant goes, not bob's
	c.advance(time.Second)

	if _, err := r.Use("a1", OpView); err != nil {
		t.Fatalf("Use(a1, %v) error = %v", OpView, err)
	}

	mint("a3", "alice")

	want := map[string]bool{"a1": false, "a2": true, "a3": false, "b1": false}
	if diff := cmp.Diff(want, done()); diff != "" {
		t.Errorf("evicted past 2 grants per user mismatch (-want +got):\n%s", diff)
	}

	// past the total, the least recently used of anyone's goes
	mint("b2", "bob")
	mint("c1", "carol")

	want = map[string]bool{"a1": false, "a2": true, "a3": false, "b1": true, "b2": false, "c1": false}
	if diff := cmp.Diff(want, done()); diff != "" {
		t.Errorf("evicted past 4 grants mismatch (-want +got):\n%s", diff)
	}

	if n := len(r.grants); n != 4 {
		t.Errorf("%d grants kept, want 4", n)
	}
}

func TestConcurrentUse(t *testing.T) {
	r := New(64, 64, nil)

	var (
		wg   sync.WaitGroup
		uses atomic.Int32
	)

	for i := range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := range 50 {
				token := fmt.Sprintf("t%d-%d", i, j)
				g := viewGrant(newClock())
				g.Expires = time.Now().Add(time.Minute)
				g.User = fmt.Sprintf("user%d", i%3)

				minted := r.Mint(token, g)

				if _, err := r.Use(token, OpView); err == nil {
					uses.Add(1)
				}

				r.Update(minted.ID, func(g *Grant) { g.Checked = time.Now() })
				_ = r.List(g.User, g.Target.Exp)

				if j%2 == 0 {
					r.Revoke(minted.ID, g.User)
				} else {
					r.RevokeWhere(func(other Grant) bool { return other.ID == minted.ID })
				}
			}
		}()
	}

	wg.Wait()

	if n := len(r.grants); n != 0 {
		t.Errorf("%d grants kept after every grant was revoked, want 0", n)
	}

	if uses.Load() == 0 {
		t.Error("no concurrent Use succeeded, want some")
	}
}
