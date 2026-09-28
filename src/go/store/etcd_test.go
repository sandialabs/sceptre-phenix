package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.etcd.io/etcd/v3/clientv3"
	"go.etcd.io/etcd/v3/mvcc/mvccpb"
)

var errEtcdUnavailable = errors.New("etcd unavailable")

// fakeKV is an in-memory clientv3.KV for testing the Etcd store without an etcd
// server. It ignores OpOptions, so it only models single-key operations. When
// err is set, every call fails with it, as a failed etcd request would.
// Methods the store does not use are left to the nil embedded KV.
type fakeKV struct {
	clientv3.KV

	data map[string]string
	err  error
}

func (f *fakeKV) Get(
	_ context.Context,
	key string,
	_ ...clientv3.OpOption,
) (*clientv3.GetResponse, error) {
	if f.err != nil {
		return nil, f.err
	}

	v, ok := f.data[key]
	if !ok {
		return &clientv3.GetResponse{}, nil
	}

	return &clientv3.GetResponse{
		Count: 1,
		Kvs:   []*mvccpb.KeyValue{{Key: []byte(key), Value: []byte(v)}},
	}, nil
}

func (f *fakeKV) Put(
	_ context.Context,
	key, val string,
	_ ...clientv3.OpOption,
) (*clientv3.PutResponse, error) {
	if f.err != nil {
		return nil, f.err
	}

	f.data[key] = val

	return &clientv3.PutResponse{}, nil
}

func (f *fakeKV) Delete(
	_ context.Context,
	key string,
	_ ...clientv3.OpOption,
) (*clientv3.DeleteResponse, error) {
	if f.err != nil {
		return nil, f.err
	}

	if _, ok := f.data[key]; !ok {
		return &clientv3.DeleteResponse{}, nil
	}

	delete(f.data, key)

	return &clientv3.DeleteResponse{Deleted: 1}, nil
}

func newFakeEtcd() (*Etcd, *fakeKV) {
	kv := &fakeKV{data: map[string]string{}}

	return &Etcd{cli: &clientv3.Client{KV: kv}}, kv
}

func newTestConfig(t *testing.T, name string) *Config {
	t.Helper()

	c, err := NewConfig(name)
	if err != nil {
		t.Fatalf("NewConfig(%q) returned error: %v", name, err)
	}

	return c
}

func TestEtcdReturnsClientErrors(t *testing.T) {
	ops := map[string]func(*Etcd, *Config) error{
		"Create": func(e *Etcd, c *Config) error { return e.Create(c) },
		"Update": func(e *Etcd, c *Config) error { return e.Update(c) },
		"Get":    func(e *Etcd, c *Config) error { return e.Get(c) },
		"Delete": func(e *Etcd, c *Config) error { return e.Delete(c) },
	}

	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			e, kv := newFakeEtcd()
			kv.err = errEtcdUnavailable

			err := op(e, newTestConfig(t, "topology/foo"))
			if !errors.Is(err, errEtcdUnavailable) {
				t.Fatalf("%s() error = %v, want it to wrap %v", name, err, errEtcdUnavailable)
			}
		})
	}
}

func TestEtcdIsInitialized(t *testing.T) {
	e, kv := newFakeEtcd()

	if e.IsInitialized(ComponentConfigs) {
		t.Fatal("IsInitialized() = true for a component that was never initialized")
	}

	if err := e.InitializeComponent(ComponentConfigs); err != nil {
		t.Fatalf("InitializeComponent() returned error: %v", err)
	}

	if !e.IsInitialized(ComponentConfigs) {
		t.Fatal("IsInitialized() = false after InitializeComponent()")
	}

	kv.err = errEtcdUnavailable

	if e.IsInitialized(ComponentConfigs) {
		t.Fatal("IsInitialized() = true when etcd is unavailable")
	}
}

func TestEtcdMissingAndExistingConfigErrors(t *testing.T) {
	e, _ := newFakeEtcd()

	if err := e.Get(newTestConfig(t, "topology/foo")); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Get() of missing config error = %v, want ErrNotExist", err)
	}

	if err := e.Update(newTestConfig(t, "topology/foo")); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Update() of missing config error = %v, want ErrNotExist", err)
	}

	if err := e.Delete(newTestConfig(t, "topology/foo")); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Delete() of missing config error = %v, want ErrNotExist", err)
	}

	if err := e.Create(newTestConfig(t, "topology/foo")); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	if err := e.Create(newTestConfig(t, "topology/foo")); !errors.Is(err, ErrExist) {
		t.Fatalf("Create() of existing config error = %v, want ErrExist", err)
	}

	if err := e.Delete(newTestConfig(t, "topology/foo")); err != nil {
		t.Fatalf("Delete() of existing config returned error: %v", err)
	}

	if err := e.Get(newTestConfig(t, "topology/foo")); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Get() after Delete() error = %v, want ErrNotExist", err)
	}
}

func TestEtcdCreateTimestamps(t *testing.T) {
	const created = "2020-01-02T03:04:05Z"

	e, kv := newFakeEtcd()

	renamed := newTestConfig(t, "topology/renamed")
	renamed.Metadata.Created = created

	if err := e.Create(renamed); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	var stored Config
	if err := json.Unmarshal([]byte(kv.data["topology/renamed"]), &stored); err != nil {
		t.Fatalf("unmarshaling stored config: %v", err)
	}

	if stored.Metadata.Created != created {
		t.Fatalf("stored created = %q, want %q", stored.Metadata.Created, created)
	}

	if stored.Metadata.Updated == "" || stored.Metadata.Updated == created {
		t.Fatalf("stored updated = %q, want the current time", stored.Metadata.Updated)
	}

	fresh := newTestConfig(t, "topology/fresh")

	if err := e.Create(fresh); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	if fresh.Metadata.Created == "" || fresh.Metadata.Created != fresh.Metadata.Updated {
		t.Fatalf(
			"fresh config created = %q, updated = %q, want both set to the current time",
			fresh.Metadata.Created,
			fresh.Metadata.Updated,
		)
	}
}
