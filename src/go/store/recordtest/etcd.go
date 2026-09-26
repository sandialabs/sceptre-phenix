// Package recordtest runs real record store backends inside tests, so code
// built on phenix/store's RecordStore can be checked against every store
// implementation and not only against in-memory fakes. It is imported only by
// tests.
package recordtest

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/etcd/v3/clientv3"
	"go.etcd.io/etcd/v3/embed"
)

const (
	etcdDirMode      = 0o700
	etcdReadyTimeout = time.Minute
)

// EtcdServer is a single member etcd server running inside the test process.
type EtcdServer struct {
	// Endpoint is the "etcd://" phenix store endpoint of the server.
	Endpoint string
	// Client is connected to the server, for checks below the store API.
	Client *clientv3.Client
}

// StartEtcd starts an in-process etcd server on loopback ports the kernel
// picks, so it never collides with a real etcd or another test. The server
// stops when the test ends.
func StartEtcd(tb testing.TB) *EtcdServer {
	tb.Helper()

	// etcd warns about a data directory other users can read.
	dir := filepath.Join(tb.TempDir(), "etcd")
	if err := os.Mkdir(dir, etcdDirMode); err != nil {
		tb.Fatalf("creating embedded etcd directory: %v", err)
	}

	loopback := url.URL{Scheme: "http", Host: "127.0.0.1:0"}

	cfg := embed.NewConfig()
	cfg.Dir = dir
	cfg.LPUrls = []url.URL{loopback}
	cfg.LCUrls = []url.URL{loopback}
	cfg.ACUrls = []url.URL{loopback}
	cfg.LogLevel = "error"
	cfg.LogOutputs = []string{"stderr"}
	cfg.UnsafeNoFsync = true

	server, err := embed.StartEtcd(cfg)
	if err != nil {
		tb.Fatalf("starting embedded etcd: %v", err)
	}

	tb.Cleanup(server.Close)

	select {
	case <-server.Server.ReadyNotify():
	case <-time.After(etcdReadyTimeout):
		tb.Fatal("embedded etcd did not become ready")
	}

	address := server.Clients[0].Addr().String()

	cli, err := clientv3.New(clientv3.Config{Endpoints: []string{address}}) //nolint:exhaustruct // partial initialization
	if err != nil {
		tb.Fatalf("connecting to embedded etcd: %v", err)
	}

	tb.Cleanup(func() { _ = cli.Close() })

	return &EtcdServer{Endpoint: "etcd://" + address, Client: cli}
}

// Reset deletes every key, so each test can start from an empty key space
// without paying for a new server.
func (e *EtcdServer) Reset(tb testing.TB) {
	tb.Helper()

	if _, err := e.Client.Delete(context.Background(), "\x00", clientv3.WithFromKey()); err != nil {
		tb.Fatalf("clearing embedded etcd: %v", err)
	}
}
