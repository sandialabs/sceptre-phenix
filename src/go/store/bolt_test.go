package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go.etcd.io/bbolt"
)

func newTestBolt(t *testing.T, path string) *BoltDB {
	t.Helper()

	b, _ := NewBoltDB().(*BoltDB)
	if err := b.Init(Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("initializing store: %v", err)
	}

	return b
}

func TestBoltReadsAndWrites(t *testing.T) {
	b := newTestBolt(t, filepath.Join(t.TempDir(), "store.bdb"))

	if !b.IsInitialized(ComponentStore) {
		t.Fatal("store not marked initialized")
	}

	if b.IsInitialized(Component("other")) {
		t.Fatal("a component never initialized is marked initialized")
	}

	// nothing of this kind has been stored yet
	missing, _ := NewConfig("topology/none")
	if err := b.Get(missing); !errors.Is(err, ErrNotExist) {
		t.Fatalf("get from an empty kind: err = %v, want ErrNotExist", err)
	}

	if err := b.Update(missing); !errors.Is(err, ErrNotExist) || missing.Metadata.Updated != "" {
		t.Fatalf("update in an empty kind: err = %v, updated %q; want ErrNotExist, unchanged",
			err, missing.Metadata.Updated)
	}

	if err := b.Delete(missing); !errors.Is(err, ErrNotExist) {
		t.Fatalf("delete from an empty kind: err = %v, want ErrNotExist", err)
	}

	if configs, err := b.List("Topology"); err != nil || len(configs) != 0 {
		t.Fatalf("list of an empty kind = %v, %v", configs, err)
	}

	for _, name := range []string{"topology/a", "topology/b"} {
		c, _ := NewConfig(name)
		if err := b.Create(c); err != nil || c.Metadata.Created == "" || c.Metadata.Updated == "" {
			t.Fatalf("creating %s: %+v, %v", name, c.Metadata, err)
		}
	}

	duplicate, _ := NewConfig("topology/a")
	if err := b.Create(duplicate); !errors.Is(err, ErrExist) {
		t.Fatalf("creating a duplicate: err = %v, want ErrExist", err)
	}

	if duplicate.Metadata.Created != "" || duplicate.Metadata.Updated != "" {
		t.Fatalf("a create of an existing name changed the config's metadata to %+v", duplicate.Metadata)
	}

	// gets and updates from many goroutines at once all succeed
	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			got, _ := NewConfig("topology/a")
			if err := b.Get(got); err != nil || got.Metadata.Created == "" {
				t.Errorf("concurrent get: %+v, %v", got.Metadata, err)

				return
			}

			if i%4 == 0 {
				if err := b.Update(got); err != nil {
					t.Errorf("concurrent update: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	configs, err := b.List("Topology", "Scenario")
	if err != nil || len(configs) != 2 {
		t.Fatalf("list = %d configs, %v; want 2", len(configs), err)
	}

	gone, _ := NewConfig("topology/b")
	if err := b.Delete(gone); err != nil {
		t.Fatalf("deleting: %v", err)
	}

	if err := b.Get(gone); !errors.Is(err, ErrNotExist) {
		t.Fatalf("get after delete: err = %v, want ErrNotExist", err)
	}
}

// Every operation on a store whose file cannot be opened fails, and leaves
// the store free for the next one.
func TestBoltFailsWithoutItsFile(t *testing.T) {
	dir := t.TempDir()
	b := newTestBolt(t, filepath.Join(dir, "store.bdb"))

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	c, _ := NewConfig("topology/a")

	operations := map[string]func() error{
		"create": func() error { return b.Create(c) },
		"update": func() error { return b.Update(c) },
		"delete": func() error { return b.Delete(c) },
		"get":    func() error { return b.Get(c) },
		"list": func() error {
			_, err := b.List("Topology")

			return err
		},
	}

	for range 2 { // an operation that kept the store locked would hang the next
		for name, operation := range operations {
			if err := operation(); err == nil {
				t.Errorf("%s succeeded", name)
			}
		}

		if b.IsInitialized(ComponentStore) {
			t.Error("store reported initialized")
		}
	}
}

// A store written without the free page list in the file (NoFreelistSync, as
// earlier releases wrote it) reads and updates, and stays readable by those
// releases.
func TestBoltOpensStoreWithoutFreelist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.bdb")
	withoutFreelist := &bbolt.Options{NoFreelistSync: true}

	old, err := bbolt.Open(path, boltFileMode, withoutFreelist)
	if err != nil {
		t.Fatalf("opening as an earlier release: %v", err)
	}

	c, _ := NewConfig("topology/a")
	c.Metadata.Created = "then"

	v, _ := json.Marshal(c)

	err = old.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(c.Kind))
		if err != nil {
			return err
		}

		return bucket.Put([]byte(c.Metadata.Name), v)
	})
	if err != nil {
		t.Fatalf("writing as an earlier release: %v", err)
	}

	_ = old.Close()

	b := newTestBolt(t, path)

	got, _ := NewConfig("topology/a")
	if err := b.Get(got); err != nil || got.Metadata.Created != "then" {
		t.Fatalf("get = %+v, %v", got.Metadata, err)
	}

	if err := b.Update(got); err != nil {
		t.Fatalf("update: %v", err)
	}

	again, err := bbolt.Open(path, boltFileMode, withoutFreelist)
	if err != nil {
		t.Fatalf("reopening as an earlier release: %v", err)
	}

	defer func() { _ = again.Close() }()

	err = again.View(func(tx *bbolt.Tx) error {
		var read Config

		if err := json.Unmarshal(tx.Bucket([]byte(c.Kind)).Get([]byte(c.Metadata.Name)), &read); err != nil {
			return err
		}

		if read.Metadata.Updated != got.Metadata.Updated {
			return errors.New("it reads " + read.Metadata.Updated + ", not the update")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("reading as an earlier release: %v", err)
	}
}

// Every write gives a config the store's next revision, across kinds and
// names, and reads return it.
func TestBoltRevisionsIncreaseAcrossConfigs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.bdb")
	b := newTestBolt(t, path)

	a, _ := NewConfig("topology/a")
	if err := b.Create(a); err != nil || a.Metadata.Revision != 1 {
		t.Fatalf("create a: revision %d, %v; want 1", a.Metadata.Revision, err)
	}

	if err := b.Update(a); err != nil || a.Metadata.Revision != 2 {
		t.Fatalf("update a: revision %d, %v; want 2", a.Metadata.Revision, err)
	}

	s, _ := NewConfig("scenario/b")
	if err := b.Create(s); err != nil || s.Metadata.Revision != 3 {
		t.Fatalf("create b: revision %d, %v; want 3", s.Metadata.Revision, err)
	}

	got, _ := NewConfig("topology/a")
	if err := b.Get(got); err != nil || got.Metadata.Revision != 2 {
		t.Fatalf("get a: revision %d, %v; want 2", got.Metadata.Revision, err)
	}

	configs, err := b.List("Topology", "Scenario")
	if err != nil || len(configs) != 2 ||
		configs[0].Metadata.Revision != 2 || configs[1].Metadata.Revision != 3 {
		t.Fatalf("list = %+v, %v; want revisions 2 and 3", configs, err)
	}

	// a config written before revisions existed reads as revision 0, and gets
	// the next revision on its next write
	legacy, _ := NewConfig("topology/legacy")
	v, _ := json.Marshal(legacy)

	if err := b.open(); err != nil {
		t.Fatal(err)
	}

	err = b.put("Topology", "legacy", v)
	_ = b.Close()

	if err != nil {
		t.Fatal(err)
	}

	if err := b.Get(legacy); err != nil || legacy.Metadata.Revision != 0 {
		t.Fatalf("get legacy: revision %d, %v; want 0", legacy.Metadata.Revision, err)
	}

	if err := b.Update(legacy); err != nil || legacy.Metadata.Revision != 4 {
		t.Fatalf("update legacy: revision %d, %v; want 4", legacy.Metadata.Revision, err)
	}

	// the counter is in the file, so another store on it continues from there
	other := newTestBolt(t, path)

	c, _ := NewConfig("topology/c")
	if err := other.Create(c); err != nil || c.Metadata.Revision != 5 {
		t.Fatalf("create through a second store: revision %d, %v; want 5", c.Metadata.Revision, err)
	}
}

// An update or delete with IfRevision runs only while the stored config is at
// that revision, and a refused one changes neither the store nor the config.
func TestBoltIfRevision(t *testing.T) {
	b := newTestBolt(t, filepath.Join(t.TempDir(), "store.bdb"))

	c, _ := NewConfig("topology/a")
	if err := b.Create(c); err != nil {
		t.Fatal(err)
	}

	if err := b.Update(c); err != nil { // revision 2
		t.Fatal(err)
	}

	stale, _ := NewConfig("topology/a")
	stale.Spec = map[string]any{"edited": true}
	stale.IfRevision = 1

	if err := b.Update(stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("update at a stale revision: err = %v, want ErrConflict", err)
	}

	if stale.Metadata.Revision != 0 || stale.Metadata.Updated != "" {
		t.Fatalf("a refused update changed the config's metadata to %+v", stale.Metadata)
	}

	got, _ := NewConfig("topology/a")
	if err := b.Get(got); err != nil || got.Metadata.Revision != 2 || got.Spec != nil {
		t.Fatalf("after a refused update the store holds %+v, %v", got, err)
	}

	if err := b.Delete(stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete at a stale revision: err = %v, want ErrConflict", err)
	}

	current, _ := NewConfig("topology/a")
	current.IfRevision = 2

	if err := b.Update(current); err != nil || current.Metadata.Revision != 3 {
		t.Fatalf("update at the current revision: revision %d, %v; want 3", current.Metadata.Revision, err)
	}

	current.IfRevision = 3

	if err := b.Delete(current); err != nil {
		t.Fatalf("delete at the current revision: %v", err)
	}

	if err := b.Get(got); !errors.Is(err, ErrNotExist) {
		t.Fatalf("get after delete: err = %v, want ErrNotExist", err)
	}
}
