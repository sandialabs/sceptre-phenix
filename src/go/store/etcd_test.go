package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"go.etcd.io/etcd/v3/clientv3"
	"go.etcd.io/etcd/v3/etcdserver/api/v3rpc/rpctypes"
	"go.etcd.io/etcd/v3/mvcc/mvccpb"

	"phenix/util/plog"
)

var errEtcdUnavailable = errors.New("etcd unavailable")

// fakeKV is an in-memory clientv3.KV for testing the Etcd store without an etcd
// server. It ignores OpOptions, so it only models single-key operations. When
// err is set, every call fails with it, as a failed etcd request would. When
// putErr is set, puts fail with it, as they do once etcd is out of space.
// Methods the store does not use are left to the nil embedded KV.
type fakeKV struct {
	clientv3.KV

	data   map[string]string
	err    error
	putErr error
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

	if f.putErr != nil {
		return nil, f.putErr
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

// etcdLogs collects what plog logs while a test runs.
type etcdLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *etcdLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p)
}

// noSpaceWarnings returns how many out of space warnings were logged.
func (l *etcdLogs) noSpaceWarnings(t *testing.T) int {
	t.Helper()

	l.mu.Lock()
	defer l.mu.Unlock()

	count := 0

	for line := range bytes.Lines(l.buf.Bytes()) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decoding log record %q: %v", line, err)
		}

		if record["msg"] == "writing to Etcd" && record["level"] == slog.LevelWarn.String() {
			count++
		}
	}

	return count
}

// captureEtcdLogs collects plog's records, and forgets any out of space write
// logged before, so each test sees its own outage.
func captureEtcdLogs(t *testing.T) *etcdLogs {
	t.Helper()

	logs := new(etcdLogs)
	name := "etcd-test-" + t.Name()

	plog.AddHandler(name, slog.NewJSONHandler(logs, &slog.HandlerOptions{
		AddSource: false, Level: slog.LevelDebug, ReplaceAttr: nil,
	}))
	etcdNoSpaceLogged.Store(false)

	t.Cleanup(func() {
		plog.RemoveHandler(name)
		etcdNoSpaceLogged.Store(false)
	})

	return logs
}

func TestEtcdWritesSayEtcdIsOutOfSpace(t *testing.T) {
	logs := captureEtcdLogs(t)
	e, kv := newFakeEtcd()
	reason := errEtcdNoSpace.Error() + ": " + rpctypes.ErrNoSpace.Error()

	if err := e.Create(newTestConfig(t, "topology/foo")); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	kv.putErr = rpctypes.ErrNoSpace

	writes := []struct {
		name  string
		want  string
		write func() error
	}{
		{
			name:  "Create",
			want:  "writing config JSON to Etcd: " + reason,
			write: func() error { return e.Create(newTestConfig(t, "topology/bar")) },
		},
		{
			name:  "Update",
			want:  "writing config JSON to Etcd: " + reason,
			write: func() error { return e.Update(newTestConfig(t, "topology/foo")) },
		},
		{
			name:  "InitializeComponent",
			want:  "marking component configs as initialized: " + reason,
			write: func() error { return e.InitializeComponent(ComponentConfigs) },
		},
	}

	for _, tt := range writes {
		err := tt.write()
		if !errors.Is(err, errEtcdNoSpace) || !errors.Is(err, rpctypes.ErrNoSpace) {
			t.Fatalf("%s() error = %v, want it to wrap errEtcdNoSpace and etcd's ErrNoSpace", tt.name, err)
		}

		if err.Error() != tt.want {
			t.Fatalf("%s() error = %q, want %q", tt.name, err, tt.want)
		}
	}

	// Refused writes in a row are one outage, logged once.
	if got := logs.noSpaceWarnings(t); got != 1 {
		t.Fatalf("logged %d out of space warnings, want 1", got)
	}

	// etcd never refuses a delete for lack of space.
	if err := e.Delete(newTestConfig(t, "topology/foo")); err != nil {
		t.Fatalf("Delete() returned error: %v", err)
	}

	kv.putErr = nil

	if err := e.Create(newTestConfig(t, "topology/foo")); err != nil {
		t.Fatalf("Create() after freeing space returned error: %v", err)
	}

	// The next outage is logged again.
	kv.putErr = rpctypes.ErrNoSpace

	if err := e.Create(newTestConfig(t, "topology/bar")); !errors.Is(err, errEtcdNoSpace) {
		t.Fatalf("Create() error = %v, want it to wrap errEtcdNoSpace", err)
	}

	if got := logs.noSpaceWarnings(t); got != 2 {
		t.Fatalf("logged %d out of space warnings after a second outage, want 2", got)
	}
}

// Starting marks the store initialized only the first time, so an etcd that
// is out of space still lets phenix start and read.
func TestEtcdInitializesTheStoreOnce(t *testing.T) {
	logs := captureEtcdLogs(t)
	e, kv := newFakeEtcd()
	kv.putErr = rpctypes.ErrNoSpace

	if err := e.initializeStore(); !errors.Is(err, errEtcdNoSpace) {
		t.Fatalf("initializeStore() error = %v, want it to wrap errEtcdNoSpace", err)
	}

	kv.putErr = nil

	if err := e.initializeStore(); err != nil {
		t.Fatalf("initializeStore() returned error: %v", err)
	}

	if !e.IsInitialized(ComponentStore) {
		t.Fatal("IsInitialized() = false after initializeStore()")
	}

	kv.putErr = rpctypes.ErrNoSpace

	if err := e.initializeStore(); err != nil {
		t.Fatalf("initializeStore() on an initialized store out of space returned error: %v", err)
	}

	if got := logs.noSpaceWarnings(t); got != 1 {
		t.Fatalf("logged %d out of space warnings, want 1", got)
	}
}

func TestEtcdWritesKeepOtherErrors(t *testing.T) {
	logs := captureEtcdLogs(t)
	e, kv := newFakeEtcd()
	kv.putErr = errEtcdUnavailable

	err := e.Create(newTestConfig(t, "topology/foo"))
	if !errors.Is(err, errEtcdUnavailable) || errors.Is(err, errEtcdNoSpace) {
		t.Fatalf("Create() error = %v, want it to wrap only %v", err, errEtcdUnavailable)
	}

	if got := logs.noSpaceWarnings(t); got != 0 {
		t.Fatalf("logged %d out of space warnings for another error, want 0", got)
	}
}
