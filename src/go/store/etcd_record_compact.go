package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.etcd.io/etcd/v3/clientv3"
	"go.etcd.io/etcd/v3/etcdserver/api/v3rpc/rpctypes"

	"phenix/util/plog"
)

// etcd keeps every revision of every key until the history is compacted, and
// etcd's own automatic compaction is off unless the operator enables it. Record
// writes are far more frequent than config writes: every builder edit writes
// new content chunks and rewrites its draft's metadata record. Without
// compaction that history grows until the backend reaches its quota (2 GiB by
// default) and etcd refuses every write, config writes included. The Etcd store
// therefore compacts its cluster periodically.
//
// Compaction is cluster wide: it discards the past revisions of every key in the
// cluster, not only phenix's. The store keeps at least a retention window of
// history, like etcd's periodic auto-compaction mode, so other clients of a
// shared cluster that read or watch recent revisions keep working. The store
// itself never reads past revisions, and a record's revision is the ModRevision
// of its current value, which compaction never changes. Several phenix processes
// sharing one cluster each compact; etcd rejects a compaction to a revision it
// already compacted, and that rejection is ignored.
const (
	// DefaultEtcdCompactionRetention is the history the Etcd store keeps when its
	// endpoint does not set a retention.
	DefaultEtcdCompactionRetention = time.Hour

	// EtcdCompactionRetentionParam is the Etcd store endpoint query parameter
	// that sets the compaction retention as a Go duration, for example
	// "etcd://localhost:2379?compaction-retention=30m". A retention of "0"
	// disables compaction, for clusters the operator compacts.
	EtcdCompactionRetentionParam = "compaction-retention"

	// etcdCompactionSamples is how many times per retention window the current
	// revision is sampled, which bounds how much more history than the retention
	// is kept.
	etcdCompactionSamples = 10

	// etcdMinCompactionInterval bounds how often a very short retention samples.
	etcdMinCompactionInterval = time.Second

	// etcdCompactionTimeout bounds one sample and compaction round trip.
	etcdCompactionTimeout = 30 * time.Second
)

// etcdRevisionSample is the cluster revision observed at a point in time.
type etcdRevisionSample struct {
	at       time.Time
	revision int64
}

// etcdCompactor compacts an etcd cluster's history to the newest revision that
// is at least a retention window old.
type etcdCompactor struct {
	cli       *clientv3.Client
	retention time.Duration
	now       func() time.Time

	// samples are ordered oldest first; compacted is the revision this compactor
	// last compacted to. Both are only used by the goroutine running compact.
	samples   []etcdRevisionSample
	compacted int64

	cancel context.CancelFunc
	done   chan struct{}
}

// EtcdCompactionRetention returns the compaction retention an Etcd store
// endpoint query sets, or [DefaultEtcdCompactionRetention] when it sets none. A
// zero retention disables compaction.
func EtcdCompactionRetention(query url.Values) (time.Duration, error) {
	if !query.Has(EtcdCompactionRetentionParam) {
		return DefaultEtcdCompactionRetention, nil
	}

	value := query.Get(EtcdCompactionRetentionParam)

	if value == "0" {
		return 0, nil
	}

	retention, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parsing Etcd endpoint %s %q: %w", EtcdCompactionRetentionParam, value, err)
	}

	if retention < 0 {
		return 0, fmt.Errorf("parsing Etcd endpoint %s %q: must not be negative", EtcdCompactionRetentionParam, value)
	}

	return retention, nil
}

// newEtcdCompactor returns a compactor that keeps retention of history. It does
// nothing until started.
func newEtcdCompactor(cli *clientv3.Client, retention time.Duration, now func() time.Time) *etcdCompactor {
	return &etcdCompactor{
		cli:       cli,
		retention: retention,
		now:       now,
		samples:   nil,
		compacted: 0,
		cancel:    nil,
		done:      nil,
	}
}

// interval is how often the compactor samples and compacts.
func (c *etcdCompactor) interval() time.Duration {
	return max(c.retention/etcdCompactionSamples, etcdMinCompactionInterval)
}

// start samples and compacts every interval, in the background, until stop is
// called.
func (c *etcdCompactor) start(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())

	c.cancel = cancel
	c.done = make(chan struct{})

	go func() {
		defer close(c.done)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.compact(ctx); err != nil && ctx.Err() == nil {
					plog.Warn(plog.TypeSystem, "compacting Etcd store history", "err", err)
				}
			}
		}
	}()
}

// stop ends background compaction and waits for an in-flight round to finish.
func (c *etcdCompactor) stop() {
	if c.cancel == nil {
		return
	}

	c.cancel()
	<-c.done
}

// compact samples the cluster's current revision, then compacts to the newest
// sampled revision that is at least one retention window old. Samples at or
// before that revision are no longer needed and are dropped.
func (c *etcdCompactor) compact(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, etcdCompactionTimeout)
	defer cancel()

	now := c.now()

	// Any read reports the cluster's current revision in its header.
	resp, err := c.cli.Get(ctx, etcdRecordRoot, clientv3.WithCountOnly())
	if err != nil {
		return fmt.Errorf("reading the current Etcd revision: %w", err)
	}

	c.samples = append(c.samples, etcdRevisionSample{at: now, revision: resp.Header.GetRevision()})

	var target int64

	expired := 0

	for _, sample := range c.samples {
		if now.Sub(sample.at) < c.retention {
			break
		}

		target = sample.revision
		expired++
	}

	c.samples = c.samples[expired:]

	if target <= c.compacted {
		return nil
	}

	if _, err := c.cli.Compact(ctx, target); err != nil && !errors.Is(err, rpctypes.ErrCompacted) {
		return fmt.Errorf("compacting Etcd history to revision %d: %w", target, err)
	}

	c.compacted = target

	return nil
}
