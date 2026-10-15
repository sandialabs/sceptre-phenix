package capability

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

	g, err := r.Use("router-secret.pcap", OpView)
	if err != nil {
		t.Fatalf("using the grant: %v", err)
	}

	if g.ID != minted.ID || g.User != "alice" || g.Uses != 1 || g.Target.File != "a.pcap" {
		t.Fatalf("got %+v", g)
	}

	if _, err := r.Use("router-secret.pcap", OpStream); !errors.Is(err, ErrOperation) {
		t.Fatalf("an operation not granted: got %v, want ErrOperation", err)
	}

	for _, token := range []string{"router-secret.pca", "Router-secret.pcap", ""} {
		if _, err := r.Use(token, OpView); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: got %v, want ErrNotFound", token, err)
		}
	}
}

func TestIDIsNotTheToken(t *testing.T) {
	r := New(10, 10, nil)

	token := "pcs_abcdefghijklmnopqrstuvwxyz"
	g := r.Mint(token, viewGrant(newClock()))

	if len(g.ID) != 16 || strings.Contains(token, g.ID) || strings.Contains(g.ID, "abcdef") || g.ID != IDOf(token) {
		t.Fatalf("ID %q of token %q", g.ID, token)
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
		t.Fatalf("before the idle timeout: %v", err)
	}

	// each use starts the idle timeout again
	c.advance(9 * time.Minute)

	if _, err := r.Use("t", OpView); err != nil {
		t.Fatalf("used again before the idle timeout: %v", err)
	}

	c.advance(10 * time.Minute)

	if _, err := r.Use("t", OpView); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after the idle timeout: got %v, want ErrNotFound", err)
	}

	if !isDone(g) {
		t.Fatal("an idle grant is not done")
	}
}

func TestExpiry(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	g := viewGrant(c)
	g = r.Mint("t", g)

	c.advance(time.Hour)

	if _, err := r.Use("t", OpView); !errors.Is(err, ErrNotFound) {
		t.Fatalf("at Expires: got %v, want ErrNotFound", err)
	}

	if !isDone(g) {
		t.Fatal("an expired grant is not done")
	}

	// the grant ends at Expires without being used again
	r = New(10, 10, nil)
	g = viewGrant(newClock())
	g.Expires = time.Now().Add(20 * time.Millisecond)
	g = r.Mint("t", g)

	select {
	case <-g.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("Done is still open after Expires")
	}

	if r.Len() != 0 {
		t.Fatalf("%d grants kept after expiry", r.Len())
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
			t.Fatalf("before UseBy: %v", err)
		}
	}

	c.advance(5 * time.Minute)

	for _, token := range []string{"used", "unused"} {
		if _, err := r.Use(token, OpStream); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s after UseBy: got %v, want ErrNotFound", token, err)
		}
	}

	// what the used grant opened goes on until it is revoked or expires
	if isDone(used) {
		t.Fatal("a used grant is done at UseBy")
	}

	if !isDone(unused) {
		t.Fatal("an unused grant is not done at UseBy")
	}

	if n := r.RevokeWhere(func(g Grant) bool { return g.ID == used.ID }); n != 1 || !isDone(used) {
		t.Fatalf("revoked %d, done %t", n, isDone(used))
	}
}

func TestRevoke(t *testing.T) {
	c := newClock()
	r := New(10, 10, c.Now)

	g := r.Mint("t", viewGrant(c))

	if r.Revoke(g.ID, "bob") {
		t.Fatal("bob revoked alice's grant")
	}

	if _, err := r.Use("t", OpView); err != nil {
		t.Fatalf("after another user's revoke: %v", err)
	}

	if !r.Revoke(g.ID, "alice") || !isDone(g) {
		t.Fatal("alice could not revoke her grant")
	}

	if _, err := r.Use("t", OpView); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after revoke: got %v, want ErrNotFound", err)
	}

	if r.Revoke(g.ID, "alice") {
		t.Fatal("revoked twice")
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

	if list := r.List("alice", "test-experiment"); len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("alice's grants for test-experiment: %+v", list)
	}

	n := r.RevokeWhere(func(g Grant) bool { return g.User == "alice" })
	if n != 2 || !isDone(a) || !isDone(b) || r.Len() != 1 {
		t.Fatalf("revoked %d, %d left", n, r.Len())
	}

	if list := r.List("alice", "test-experiment"); len(list) != 0 {
		t.Fatalf("revoked grants listed: %+v", list)
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

	used, err := r.Use("t", OpView)
	if err != nil || used.Target.Live || used.Target.Local != "/settled.pcap" || used.ID != g.ID || used.Done != g.Done {
		t.Fatalf("got %+v, %v", used, err)
	}

	r.Update("unknown", func(*Grant) { t.Fatal("updated an unknown grant") })
}

func TestEviction(t *testing.T) {
	c := newClock()
	r := New(4, 2, c.Now)

	mint := func(token, user string) Grant {
		c.advance(time.Second)

		g := viewGrant(c)
		g.User = token[:1] + user

		return r.Mint(token, g)
	}

	a1, a2 := mint("a1", "lice"), mint("a2", "lice")
	b1 := mint("b1", "ob")

	// alice's least recently used grant goes, not bob's
	c.advance(time.Second)

	if _, err := r.Use("a1", OpView); err != nil {
		t.Fatal(err)
	}

	a3 := mint("a3", "lice")

	if !isDone(a2) || isDone(a1) || isDone(b1) || isDone(a3) {
		t.Fatalf("per user: done a1 %t, a2 %t, a3 %t, b1 %t", isDone(a1), isDone(a2), isDone(a3), isDone(b1))
	}

	// past the total, the least recently used of anyone's goes
	b2 := mint("b2", "ob")
	c1 := mint("c1", "arol")

	if !isDone(b1) || isDone(a1) || isDone(a3) || isDone(b2) || isDone(c1) || r.Len() != 4 {
		t.Fatalf("overall: done b1 %t, a1 %t, a3 %t, %d kept", isDone(b1), isDone(a1), isDone(a3), r.Len())
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

	if r.Len() != 0 || uses.Load() == 0 {
		t.Fatalf("%d grants left, %d uses", r.Len(), uses.Load())
	}
}
