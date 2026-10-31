package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.etcd.io/bbolt"
)

func newTestBolt(t *testing.T, path string) *BoltDB {
	t.Helper()

	b, _ := NewBoltDB().(*BoltDB)
	if err := b.Init(Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("BoltDB.Init(bolt://%s) error = %v", path, err)
	}

	return b
}

// newTestConfig returns a new config for name, given as kind/name.
func newTestConfig(t *testing.T, name string) *Config {
	t.Helper()

	c, err := NewConfig(name)
	if err != nil {
		t.Fatalf("NewConfig(%q) error = %v", name, err)
	}

	return c
}

// listedRevisions returns each config as kind/name@revision, in order.
func listedRevisions(configs Configs) []string {
	out := make([]string, 0, len(configs))

	for _, c := range configs {
		out = append(out, fmt.Sprintf("%s/%s@%d", c.Kind, c.Metadata.Name, c.Metadata.Revision))
	}

	return out
}

func TestBoltReadsAndWrites(t *testing.T) {
	b := newTestBolt(t, filepath.Join(t.TempDir(), "store.bdb"))

	if b.IsInitialized(ComponentConfigs) {
		t.Errorf("IsInitialized(%s) on a new store = true, want false", ComponentConfigs)
	}

	if err := b.InitializeComponent(ComponentConfigs); err != nil {
		t.Fatalf("InitializeComponent(%s) error = %v", ComponentConfigs, err)
	}

	if !b.IsInitialized(ComponentConfigs) {
		t.Errorf("IsInitialized(%s) after InitializeComponent = false, want true", ComponentConfigs)
	}

	if b.IsInitialized(Component("other")) {
		t.Error("IsInitialized(other) never initialized = true, want false")
	}

	// nothing of this kind has been stored yet
	missing := newTestConfig(t, "topology/none")
	if err := b.Get(missing); !errors.Is(err, ErrNotExist) {
		t.Errorf("Get(topology/none) from an empty kind error = %v, want %v", err, ErrNotExist)
	}

	if err := b.Update(missing); !errors.Is(err, ErrNotExist) {
		t.Errorf("Update(topology/none) in an empty kind error = %v, want %v", err, ErrNotExist)
	}

	if missing.Metadata.Updated != "" {
		t.Errorf("Update(topology/none) in an empty kind set updated = %q, want it unchanged", missing.Metadata.Updated)
	}

	if err := b.Delete(missing); !errors.Is(err, ErrNotExist) {
		t.Errorf("Delete(topology/none) from an empty kind error = %v, want %v", err, ErrNotExist)
	}

	if configs, err := b.List("Topology"); err != nil || len(configs) != 0 {
		t.Errorf("List(Topology) of an empty kind = %v, %v; want none, nil", configs, err)
	}

	for _, name := range []string{"topology/a", "topology/b"} {
		c := newTestConfig(t, name)
		if err := b.Create(c); err != nil {
			t.Fatalf("Create(%s) error = %v", name, err)
		}

		if c.Metadata.Created == "" || c.Metadata.Updated == "" {
			t.Errorf("Create(%s) metadata = %+v, want created and updated set", name, c.Metadata)
		}
	}

	duplicate := newTestConfig(t, "topology/a")
	if err := b.Create(duplicate); !errors.Is(err, ErrExist) {
		t.Errorf("Create(topology/a) of an existing name error = %v, want %v", err, ErrExist)
	}

	if diff := cmp.Diff(ConfigMetadata{Name: "a"}, duplicate.Metadata); diff != "" {
		t.Errorf("Create(topology/a) of an existing name changed the metadata (-want +got):\n%s", diff)
	}

	// gets and updates from many goroutines at once all succeed
	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			got, _ := NewConfig("topology/a")
			if err := b.Get(got); err != nil || got.Metadata.Created == "" {
				t.Errorf("concurrent Get(topology/a) = %+v, %v; want it created, nil", got.Metadata, err)

				return
			}

			if i%4 == 0 {
				if err := b.Update(got); err != nil {
					t.Errorf("concurrent Update(topology/a) error = %v", err)
				}
			}
		}()
	}

	wg.Wait()

	configs, err := b.List("Topology", "Scenario")
	if err != nil {
		t.Fatalf("List(Topology, Scenario) error = %v", err)
	}

	names := make([]string, 0, len(configs))
	for _, c := range configs {
		names = append(names, c.Metadata.Name)
	}

	if diff := cmp.Diff([]string{"a", "b"}, names); diff != "" {
		t.Errorf("List(Topology, Scenario) names mismatch (-want +got):\n%s", diff)
	}

	gone := newTestConfig(t, "topology/b")
	if err := b.Delete(gone); err != nil {
		t.Fatalf("Delete(topology/b) error = %v", err)
	}

	if err := b.Get(gone); !errors.Is(err, ErrNotExist) {
		t.Errorf("Get(topology/b) after Delete error = %v, want %v", err, ErrNotExist)
	}
}

// Every operation on a store whose file cannot be opened fails, and leaves
// the store free for the next one.
func TestBoltFailsWithoutItsFile(t *testing.T) {
	dir := t.TempDir()
	b := newTestBolt(t, filepath.Join(dir, "store.bdb"))

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("os.RemoveAll(%s) error = %v", dir, err)
	}

	c := newTestConfig(t, "topology/a")

	operations := map[string]func() error{
		"Create(topology/a)": func() error { return b.Create(c) },
		"Update(topology/a)": func() error { return b.Update(c) },
		"Delete(topology/a)": func() error { return b.Delete(c) },
		"Get(topology/a)":    func() error { return b.Get(c) },
		"List(Topology)": func() error {
			_, err := b.List("Topology")

			return err
		},
	}

	for range 2 { // an operation that kept the store locked would hang the next
		for name, operation := range operations {
			if err := operation(); err == nil {
				t.Errorf("%s without the store file error = nil, want one", name)
			}
		}

		if b.IsInitialized(ComponentConfigs) {
			t.Errorf("IsInitialized(%s) without the store file = true, want false", ComponentConfigs)
		}
	}
}

// A store written without the free page list (NoFreelistSync, as earlier releases wrote it) reads
// and updates, and stays readable by those releases.
func TestBoltOpensStoreWithoutFreelist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.bdb")
	withoutFreelist := &bbolt.Options{NoFreelistSync: true}

	old, err := bbolt.Open(path, boltFileMode, withoutFreelist)
	if err != nil {
		t.Fatalf("bbolt.Open(NoFreelistSync) error = %v", err)
	}

	c := newTestConfig(t, "topology/a")
	c.Metadata.Created = "then"

	v, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal(topology/a) error = %v", err)
	}

	err = old.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(c.Kind))
		if err != nil {
			return err
		}

		return bucket.Put([]byte(c.Metadata.Name), v)
	})
	if err != nil {
		t.Fatalf("writing topology/a with NoFreelistSync error = %v", err)
	}

	_ = old.Close()

	b := newTestBolt(t, path)

	got := newTestConfig(t, "topology/a")
	if err := b.Get(got); err != nil {
		t.Fatalf("Get(topology/a) error = %v", err)
	}

	if got.Metadata.Created != "then" {
		t.Errorf("Get(topology/a) created = %q, want %q", got.Metadata.Created, "then")
	}

	if err := b.Update(got); err != nil {
		t.Fatalf("Update(topology/a) error = %v", err)
	}

	again, err := bbolt.Open(path, boltFileMode, withoutFreelist)
	if err != nil {
		t.Fatalf("bbolt.Open(NoFreelistSync) after the update error = %v", err)
	}

	defer func() { _ = again.Close() }()

	var read Config

	err = again.View(func(tx *bbolt.Tx) error {
		return json.Unmarshal(tx.Bucket([]byte(c.Kind)).Get([]byte(c.Metadata.Name)), &read)
	})
	if err != nil {
		t.Fatalf("reading topology/a with NoFreelistSync error = %v", err)
	}

	if diff := cmp.Diff(got.Metadata, read.Metadata); diff != "" {
		t.Errorf("topology/a metadata read with NoFreelistSync after Update mismatch (-want +got):\n%s", diff)
	}
}

// Every write gives a config the store's next revision, across kinds and
// names, and reads return it.
func TestBoltRevisionsIncreaseAcrossConfigs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.bdb")
	b := newTestBolt(t, path)

	a := newTestConfig(t, "topology/a")
	if err := b.Create(a); err != nil {
		t.Fatalf("Create(topology/a) error = %v", err)
	}

	if a.Metadata.Revision != 1 {
		t.Errorf("Create(topology/a) revision = %d, want 1", a.Metadata.Revision)
	}

	if err := b.Update(a); err != nil {
		t.Fatalf("Update(topology/a) error = %v", err)
	}

	if a.Metadata.Revision != 2 {
		t.Errorf("Update(topology/a) revision = %d, want 2", a.Metadata.Revision)
	}

	s := newTestConfig(t, "scenario/b")
	if err := b.Create(s); err != nil {
		t.Fatalf("Create(scenario/b) error = %v", err)
	}

	if s.Metadata.Revision != 3 {
		t.Errorf("Create(scenario/b) revision = %d, want 3", s.Metadata.Revision)
	}

	got := newTestConfig(t, "topology/a")
	if err := b.Get(got); err != nil {
		t.Fatalf("Get(topology/a) error = %v", err)
	}

	if got.Metadata.Revision != 2 {
		t.Errorf("Get(topology/a) revision = %d, want 2", got.Metadata.Revision)
	}

	configs, err := b.List("Topology", "Scenario")
	if err != nil {
		t.Fatalf("List(Topology, Scenario) error = %v", err)
	}

	if diff := cmp.Diff([]string{"Topology/a@2", "Scenario/b@3"}, listedRevisions(configs)); diff != "" {
		t.Errorf("List(Topology, Scenario) revisions mismatch (-want +got):\n%s", diff)
	}

	// a config written before revisions existed reads as revision 0, and gets
	// the next revision on its next write
	legacy := newTestConfig(t, "topology/legacy")

	v, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("json.Marshal(topology/legacy) error = %v", err)
	}

	if err := b.open(); err != nil {
		t.Fatalf("BoltDB.open() error = %v", err)
	}

	err = b.put("Topology", "legacy", v)
	_ = b.close()

	if err != nil {
		t.Fatalf("BoltDB.put(Topology, legacy) error = %v", err)
	}

	if err := b.Get(legacy); err != nil {
		t.Fatalf("Get(topology/legacy) error = %v", err)
	}

	if legacy.Metadata.Revision != 0 {
		t.Errorf("Get(topology/legacy) revision = %d, want 0", legacy.Metadata.Revision)
	}

	if err := b.Update(legacy); err != nil {
		t.Fatalf("Update(topology/legacy) error = %v", err)
	}

	if legacy.Metadata.Revision != 4 {
		t.Errorf("Update(topology/legacy) revision = %d, want 4", legacy.Metadata.Revision)
	}

	// the counter is in the file, so another store on it continues from there
	other := newTestBolt(t, path)

	c := newTestConfig(t, "topology/c")
	if err := other.Create(c); err != nil {
		t.Fatalf("Create(topology/c) through a second store error = %v", err)
	}

	if c.Metadata.Revision != 5 {
		t.Errorf("Create(topology/c) through a second store revision = %d, want 5", c.Metadata.Revision)
	}
}

// An update or delete with IfRevision runs only while the stored config is at
// that revision, and a refused one changes neither the store nor the config.
func TestBoltIfRevision(t *testing.T) {
	b := newTestBolt(t, filepath.Join(t.TempDir(), "store.bdb"))

	c := newTestConfig(t, "topology/a")
	if err := b.Create(c); err != nil {
		t.Fatalf("Create(topology/a) error = %v", err)
	}

	if err := b.Update(c); err != nil { // revision 2
		t.Fatalf("Update(topology/a) error = %v", err)
	}

	stale := newTestConfig(t, "topology/a")
	stale.Spec = map[string]any{"edited": true}
	stale.IfRevision = 1

	if err := b.Update(stale); !errors.Is(err, ErrConflict) {
		t.Errorf("Update(topology/a, IfRevision 1) at revision 2 error = %v, want %v", err, ErrConflict)
	}

	if diff := cmp.Diff(ConfigMetadata{Name: "a"}, stale.Metadata); diff != "" {
		t.Errorf("refused Update(topology/a, IfRevision 1) changed the metadata (-want +got):\n%s", diff)
	}

	got := newTestConfig(t, "topology/a")
	if err := b.Get(got); err != nil {
		t.Fatalf("Get(topology/a) after a refused update error = %v", err)
	}

	if diff := cmp.Diff(c, got); diff != "" {
		t.Errorf("Get(topology/a) after a refused update mismatch (-want +got):\n%s", diff)
	}

	if err := b.Delete(stale); !errors.Is(err, ErrConflict) {
		t.Errorf("Delete(topology/a, IfRevision 1) at revision 2 error = %v, want %v", err, ErrConflict)
	}

	current := newTestConfig(t, "topology/a")
	current.IfRevision = 2

	if err := b.Update(current); err != nil {
		t.Fatalf("Update(topology/a, IfRevision 2) at revision 2 error = %v", err)
	}

	if current.Metadata.Revision != 3 {
		t.Errorf("Update(topology/a, IfRevision 2) revision = %d, want 3", current.Metadata.Revision)
	}

	current.IfRevision = 3

	if err := b.Delete(current); err != nil {
		t.Fatalf("Delete(topology/a, IfRevision 3) at revision 3 error = %v", err)
	}

	if err := b.Get(got); !errors.Is(err, ErrNotExist) {
		t.Errorf("Get(topology/a) after Delete error = %v, want %v", err, ErrNotExist)
	}
}

// A bolt:// endpoint names its file, with or without a host part, and any
// other endpoint is refused.
func TestBoltPath(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint string
		want     string
		wantErr  bool
	}{
		"absolute":  {endpoint: "bolt:///etc/phenix/store.bdb", want: "/etc/phenix/store.bdb"},
		"relative":  {endpoint: "bolt://store.bdb", want: "store.bdb"},
		"etcd":      {endpoint: "etcd://localhost:2379", wantErr: true},
		"no scheme": {endpoint: "/etc/phenix/store.bdb", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := BoltPath(tc.endpoint)
			if (err != nil) != tc.wantErr {
				t.Fatalf("BoltPath(%q) error = %v, want error %t", tc.endpoint, err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("BoltPath(%q) mismatch (-want +got):\n%s", tc.endpoint, diff)
			}
		})
	}
}

func TestBoltDBOpenFailure(t *testing.T) {
	cfg := &Config{Kind: "Topology", Metadata: ConfigMetadata{Name: "foo"}}

	tests := []struct {
		name string
		call func(*BoltDB) error
	}{
		{name: "get", call: func(b *BoltDB) error { return b.Get(cfg) }},
		{name: "update", call: func(b *BoltDB) error { return b.Update(cfg) }},
		{name: "delete", call: func(b *BoltDB) error { return b.Delete(cfg) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The parent directory does not exist, so every open fails.
			b := &BoltDB{path: filepath.Join(t.TempDir(), "missing", "store.bdb")}

			done := make(chan error, 2)

			go func() {
				done <- tt.call(b)
				done <- tt.call(b)
			}()

			for range 2 {
				select {
				case err := <-done:
					if !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("expected the BoltDB open error, got %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("call blocked after a failed open")
				}
			}
		})
	}
}

func TestBoltDBOpenTimeout(t *testing.T) {
	orig := boltOpenTimeout
	boltOpenTimeout = 100 * time.Millisecond

	t.Cleanup(func() { boltOpenTimeout = orig })

	path := filepath.Join(t.TempDir(), "store.bdb")

	// Holding the file open takes its exclusive lock, as another phenix process would.
	holder, err := bbolt.Open(path, boltFileMode, nil)
	if err != nil {
		t.Fatalf("opening BoltDB file: %v", err)
	}

	t.Cleanup(func() { _ = holder.Close() })

	b := &BoltDB{path: path}
	cfg := &Config{Kind: "Topology", Metadata: ConfigMetadata{Name: "foo"}}
	done := make(chan error, 1)

	go func() { done <- b.Get(cfg) }()

	select {
	case err := <-done:
		if !errors.Is(err, bbolt.ErrTimeout) {
			t.Fatalf("expected bbolt.ErrTimeout, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open waited for the file lock without a timeout")
	}
}
