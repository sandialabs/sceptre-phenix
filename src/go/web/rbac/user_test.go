package rbac

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/sync/errgroup"

	"phenix/store"
	v1 "phenix/types/version/v1"
)

// useBoltStore points the config store at a new BoltDB for the test.
func useBoltStore(t *testing.T) {
	t.Helper()

	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	previous := store.DefaultStore
	store.DefaultStore = db //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store
}

// TestParallelSignInsKeepEveryToken signs one user in from many requests at
// once while the user signs out of an earlier session and has their role
// changed, and asserts every new token is still valid afterwards.
func TestParallelSignInsKeepEveryToken(t *testing.T) {
	useBoltStore(t)

	if NewUser("alice", "Testpass1!", "Alice", "Tester") == nil {
		t.Fatal("NewUser returned nil")
	}

	earlier, err := GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}

	if err := earlier.AddToken("earlier", "signed in earlier"); err != nil {
		t.Fatalf("AddToken returned error: %v", err)
	}

	const signIns = 16

	// Every request reads the user before any of them saves, as requests
	// that arrive together do, then changes it.
	var (
		read  sync.WaitGroup
		group errgroup.Group
	)

	read.Add(signIns + 2)

	request := func(change func(u *User) error) {
		group.Go(func() error {
			u, err := GetUser("alice")
			read.Done()

			if err != nil {
				return err
			}

			read.Wait()

			return change(u)
		})
	}

	for i := range signIns {
		request(func(u *User) error { return u.AddToken("token-"+strconv.Itoa(i), "signed in") })
	}

	request(func(u *User) error { return u.DeleteToken("earlier") })
	request(func(u *User) error { return u.SetRole(&Role{Spec: &v1.RoleSpec{Name: "Experiment User"}}) })

	if err := group.Wait(); err != nil {
		t.Fatalf("a request returned error: %v", err)
	}

	stored, err := GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}

	for i := range signIns {
		if err := stored.ValidateToken("token-" + strconv.Itoa(i)); err != nil {
			t.Errorf("token-%d: %v", i, err)
		}
	}

	if stored.ValidateToken("earlier") == nil {
		t.Error("the token signed out of is still valid")
	}

	if got := stored.RoleName(); got != "Experiment User" {
		t.Errorf("role = %q, want Experiment User", got)
	}

	if stored.FirstName() != "Alice" || stored.LastName() != "Tester" {
		t.Errorf("name = %q %q, want Alice Tester", stored.FirstName(), stored.LastName())
	}
}
