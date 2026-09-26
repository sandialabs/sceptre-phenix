package store

import (
	"testing"

	"phenix/store/recordtest"
)

// embeddedEtcd is an in-process etcd server for the Etcd store's tests.
type embeddedEtcd struct {
	*recordtest.EtcdServer
}

func startEmbeddedEtcd(t *testing.T) *embeddedEtcd {
	t.Helper()

	return &embeddedEtcd{EtcdServer: recordtest.StartEtcd(t)}
}

// open returns a store connected to the server, closed when the test ends.
func (e *embeddedEtcd) open(t *testing.T) Store { //nolint:ireturn // mirrors the store factory
	t.Helper()

	s := NewEtcd()
	if err := s.Init(Endpoint(e.Endpoint)); err != nil {
		t.Fatalf("initializing Etcd store returned error: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s
}
