package store

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"go.etcd.io/etcd/v3/clientv3"
	"go.etcd.io/etcd/v3/etcdserver/api/v3rpc/rpctypes"
)

func TestEtcdCompactionRetention(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		expect time.Duration
		valid  bool
	}{
		{name: "default", query: "", expect: DefaultEtcdCompactionRetention, valid: true},
		{name: "duration", query: "compaction-retention=30m", expect: 30 * time.Minute, valid: true},
		{name: "disabled", query: "compaction-retention=0", expect: 0, valid: true},
		{name: "disabled with a unit", query: "compaction-retention=0s", expect: 0, valid: true},
		{name: "other parameters are ignored", query: "timeout=5s", expect: DefaultEtcdCompactionRetention, valid: true},
		{name: "unitless", query: "compaction-retention=5", expect: 0, valid: false},
		{name: "negative", query: "compaction-retention=-1h", expect: 0, valid: false},
		{name: "empty", query: "compaction-retention=", expect: 0, valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("parsing query %q: %v", tt.query, err)
			}

			got, err := EtcdCompactionRetention(query)

			switch {
			case tt.valid && err != nil:
				t.Fatalf("EtcdCompactionRetention(%q) returned error: %v", tt.query, err)
			case !tt.valid && err == nil:
				t.Fatalf("EtcdCompactionRetention(%q) = %v, want an error", tt.query, got)
			case got != tt.expect:
				t.Fatalf("EtcdCompactionRetention(%q) = %v, want %v", tt.query, got, tt.expect)
			}
		})
	}
}

func TestEtcdStoreCompactsByDefault(t *testing.T) {
	server := startEmbeddedEtcd(t)

	opened := server.open(t)

	// A fresh cluster has no initialized components; asking must not fail.
	if opened.IsInitialized(ComponentConfigs) || !opened.IsInitialized(ComponentStore) {
		t.Fatal("a fresh Etcd store must report only the store component as initialized")
	}

	compacting, ok := opened.(*Etcd)
	if !ok || compacting.compactor == nil || compacting.compactor.retention != DefaultEtcdCompactionRetention {
		t.Fatalf("store opened without a retention = %+v, want a compactor keeping %v", compacting, DefaultEtcdCompactionRetention)
	}

	disabled := NewEtcd()
	if err := disabled.Init(Endpoint(server.Endpoint + "?" + EtcdCompactionRetentionParam + "=0")); err != nil {
		t.Fatalf("initializing Etcd store without compaction returned error: %v", err)
	}

	if disabled.(*Etcd).compactor != nil { //nolint:forcetypeassert // NewEtcd returns *Etcd
		t.Fatal("a zero retention must disable compaction")
	}

	if err := disabled.Close(); err != nil {
		t.Fatalf("closing Etcd store returned error: %v", err)
	}

	invalid := NewEtcd()
	if err := invalid.Init(Endpoint(server.Endpoint + "?" + EtcdCompactionRetentionParam + "=soon")); err == nil {
		t.Fatal("an invalid retention must fail store initialization")
	}

	// Closing stops the background compactor without waiting for its interval.
	closed := make(chan error, 1)

	go func() { closed <- compacting.Close() }()

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("closing Etcd store returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("closing the Etcd store did not stop its compactor")
	}
}

func TestEtcdCompactorKeepsRetentionWindow(t *testing.T) {
	server := startEmbeddedEtcd(t)
	ctx := context.Background()

	s := server.open(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	second, err := s.UpdateRecord("drafts", "draft-1", []byte("v2"), created.Revision)
	if err != nil {
		t.Fatalf("UpdateRecord returned error: %v", err)
	}

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	compactor := newEtcdCompactor(server.Client, time.Hour, func() time.Time { return now })

	// The first sample is the newest revision the history may be compacted to
	// one retention window later.
	if err := compactor.compact(ctx); err != nil {
		t.Fatalf("compact returned error: %v", err)
	}

	sampled := compactor.samples[0].revision

	if sampled < second.Revision {
		t.Fatalf("sampled revision %d, want at least %d", sampled, second.Revision)
	}

	updated, err := s.UpdateRecord("drafts", "draft-1", []byte("v3"), second.Revision)
	if err != nil {
		t.Fatalf("UpdateRecord returned error: %v", err)
	}

	now = now.Add(59 * time.Minute)

	if err := compactor.compact(ctx); err != nil {
		t.Fatalf("compact within the retention window returned error: %v", err)
	}

	requireEtcdRevision(t, server.Client, created.Revision, "v1")

	now = now.Add(time.Minute)

	if err := compactor.compact(ctx); err != nil {
		t.Fatalf("compact after the retention window returned error: %v", err)
	}

	if compactor.compacted != sampled {
		t.Fatalf("compacted to revision %d, want the revision sampled one retention window ago (%d)", compactor.compacted, sampled)
	}

	if len(compactor.samples) != 2 {
		t.Fatalf("kept %d samples, want the 2 still inside the retention window", len(compactor.samples))
	}

	// History before the compacted revision is gone, the retention window is
	// kept, and records keep their revisions for compare-and-swap.
	_, err = server.Client.Get(ctx, EtcdRecordKey("drafts", "draft-1"), clientv3.WithRev(created.Revision))
	if !errors.Is(err, rpctypes.ErrCompacted) {
		t.Fatalf("reading a revision before the compaction error = %v, want ErrCompacted", err)
	}

	requireEtcdRevision(t, server.Client, sampled, "v2")
	requireRecord(t, s, "drafts", "draft-1", "v3", updated.Revision)

	if _, err := s.UpdateRecord("drafts", "draft-1", []byte("v4"), updated.Revision); err != nil {
		t.Fatalf("UpdateRecord with a revision read before compaction returned error: %v", err)
	}

	// Another process may have compacted further already; that is not an error.
	current, err := server.Client.Get(ctx, etcdRecordRoot)
	if err != nil {
		t.Fatalf("reading the current revision: %v", err)
	}

	if _, err := server.Client.Compact(ctx, current.Header.GetRevision()); err != nil {
		t.Fatalf("compacting to the current revision: %v", err)
	}

	now = now.Add(time.Hour)

	if err := compactor.compact(ctx); err != nil {
		t.Fatalf("compact behind another compaction returned error: %v", err)
	}
}

func TestEtcdCompactorRunsInTheBackground(t *testing.T) {
	server := startEmbeddedEtcd(t)
	s := server.open(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	updated, err := s.UpdateRecord("drafts", "draft-1", []byte("v2"), created.Revision)
	if err != nil {
		t.Fatalf("UpdateRecord returned error: %v", err)
	}

	if got := newEtcdCompactor(nil, time.Hour, time.Now).interval(); got != 6*time.Minute {
		t.Fatalf("interval for a one hour retention = %v, want 6m", got)
	}

	if got := newEtcdCompactor(nil, time.Millisecond, time.Now).interval(); got != etcdMinCompactionInterval {
		t.Fatalf("interval for a tiny retention = %v, want %v", got, etcdMinCompactionInterval)
	}

	// With a retention shorter than the interval, each round compacts to the
	// previous round's sample.
	compactor := newEtcdCompactor(server.Client, time.Nanosecond, time.Now)
	compactor.start(10 * time.Millisecond)

	t.Cleanup(compactor.stop)

	deadline := time.Now().Add(10 * time.Second)

	for {
		_, err := server.Client.Get(context.Background(), EtcdRecordKey("drafts", "draft-1"), clientv3.WithRev(created.Revision))
		if errors.Is(err, rpctypes.ErrCompacted) {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("history was not compacted in the background; last read error = %v", err)
		}

		time.Sleep(10 * time.Millisecond)
	}

	requireRecord(t, s, "drafts", "draft-1", "v2", updated.Revision)
}

// requireEtcdRevision fails the test unless the draft-1 record read at the
// given past revision holds value.
func requireEtcdRevision(t *testing.T, cli *clientv3.Client, revision int64, value string) {
	t.Helper()

	resp, err := cli.Get(context.Background(), EtcdRecordKey("drafts", "draft-1"), clientv3.WithRev(revision))
	if err != nil {
		t.Fatalf("reading revision %d returned error: %v", revision, err)
	}

	if len(resp.Kvs) != 1 {
		t.Fatalf("reading revision %d returned %d keys, want 1", revision, len(resp.Kvs))
	}

	envelope, err := decodeRecordEnvelope(resp.Kvs[0].Value)
	if err != nil {
		t.Fatalf("decoding revision %d: %v", revision, err)
	}

	if string(envelope.Value) != value {
		t.Fatalf("revision %d holds %q, want %q", revision, envelope.Value, value)
	}
}
