package store

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.etcd.io/etcd/v3/clientv3"
	"go.etcd.io/etcd/v3/embed"
	"go.etcd.io/etcd/v3/etcdserver/api/v3rpc/rpctypes"
	pb "go.etcd.io/etcd/v3/etcdserver/etcdserverpb"
	"go.etcd.io/etcd/v3/mvcc/mvccpb"

	"phenix/util/plog/plogtest"
)

const (
	// etcdRecordTestQuota is a space quota an embedded etcd reaches after a few
	// record writes: the database of a new server already takes about 100 KiB.
	etcdRecordTestQuota = 1 << 20

	// etcdRecordFillTimeout bounds how long a test writes to fill etcd.
	etcdRecordFillTimeout = 30 * time.Second

	// etcdRecordNoSpaceLog is the message an out of space write is logged with.
	etcdRecordNoSpaceLog = "writing to Etcd"
)

// etcdRecordFailingKV is a clientv3.KV whose reads find every record and whose
// writes all fail with err, as they do once etcd is out of space.
type etcdRecordFailingKV struct {
	clientv3.KV

	envelope []byte
	err      error
}

func (f etcdRecordFailingKV) Get(_ context.Context, key string, _ ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	return &clientv3.GetResponse{
		Count: 1,
		Kvs:   []*mvccpb.KeyValue{{Key: []byte(key), Value: f.envelope, ModRevision: 1}},
	}, nil
}

func (f etcdRecordFailingKV) Delete(context.Context, string, ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	return nil, f.err
}

func (f etcdRecordFailingKV) Txn(context.Context) clientv3.Txn { //nolint:ireturn // implements clientv3.KV
	return etcdRecordFailingTxn{err: f.err}
}

type etcdRecordFailingTxn struct {
	err error
}

func (t etcdRecordFailingTxn) If(...clientv3.Cmp) clientv3.Txn { //nolint:ireturn // implements clientv3.Txn
	return t
}

func (t etcdRecordFailingTxn) Then(...clientv3.Op) clientv3.Txn { //nolint:ireturn // implements clientv3.Txn
	return t
}

func (t etcdRecordFailingTxn) Else(...clientv3.Op) clientv3.Txn { //nolint:ireturn // implements clientv3.Txn
	return t
}

func (t etcdRecordFailingTxn) Commit() (*clientv3.TxnResponse, error) {
	return nil, t.err
}

func newEtcdRecordFailingStore(t *testing.T, err error) *Etcd {
	t.Helper()

	envelope, encodeErr := encodeRecordEnvelope(etcdRecordEnvelope{Value: []byte("value")})
	if encodeErr != nil {
		t.Fatalf("encodeRecordEnvelope returned error: %v", encodeErr)
	}

	return &Etcd{cli: &clientv3.Client{KV: etcdRecordFailingKV{envelope: envelope, err: err}}}
}

// captureEtcdRecordLogs records what plog logs, and forgets any out of space
// write logged before, so each test sees its own outage.
func captureEtcdRecordLogs(t *testing.T) *plogtest.Logs {
	t.Helper()

	logs := plogtest.Capture(t)
	etcdNoSpaceLogged.Store(false)

	t.Cleanup(func() { etcdNoSpaceLogged.Store(false) })

	return logs
}

// etcdRecordNoSpaceWarnings returns how many out of space writes were logged.
func etcdRecordNoSpaceWarnings(t *testing.T, logs *plogtest.Logs) int {
	t.Helper()

	return len(logs.Records(t, func(record map[string]any) bool {
		return record["msg"] == etcdRecordNoSpaceLog && record["level"] == slog.LevelWarn.String()
	}))
}

// requireEtcdRecordNoSpace fails the test unless err says etcd is out of space
// and still wraps etcd's own error.
func requireEtcdRecordNoSpace(t *testing.T, err error, operation string) {
	t.Helper()

	if !errors.Is(err, ErrNoSpace) || !errors.Is(err, rpctypes.ErrNoSpace) {
		t.Fatalf("%s error = %v, want it to wrap ErrNoSpace and etcd's ErrNoSpace", operation, err)
	}
}

func TestEtcdRecordWritesSayEtcdIsOutOfSpace(t *testing.T) {
	logs := captureEtcdRecordLogs(t)
	e := newEtcdRecordFailingStore(t, rpctypes.ErrNoSpace)
	reason := ErrNoSpace.Error() + ": " + rpctypes.ErrNoSpace.Error()

	writes := []struct {
		name  string
		want  string
		write func() error
	}{
		{
			name: "create",
			want: "creating record drafts/d1 in Etcd: " + reason,
			write: func() error {
				_, err := e.CreateRecord("drafts", "d1", []byte("value"))

				return err
			},
		},
		{
			name: "update",
			want: "updating record drafts/d1 in Etcd: " + reason,
			write: func() error {
				_, err := e.UpdateRecord("drafts", "d1", []byte("value"), AnyRevision)

				return err
			},
		},
		{
			name:  "delete",
			want:  "deleting record drafts/d1 in Etcd: " + reason,
			write: func() error { return e.DeleteRecord("drafts", "d1", AnyRevision) },
		},
	}

	for _, tt := range writes {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.write()

			requireEtcdRecordNoSpace(t, err, tt.name)

			if err.Error() != tt.want {
				t.Fatalf("%s error = %q, want %q", tt.name, err, tt.want)
			}
		})
	}

	// Refused writes in a row are one outage, logged once.
	if got := etcdRecordNoSpaceWarnings(t, logs); got != 1 {
		t.Fatalf("logged %d out of space warnings, want 1", got)
	}

	// Reads still work while etcd is out of space.
	if _, err := e.GetRecord("drafts", "d1"); err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}
}

func TestEtcdRecordWritesKeepOtherErrors(t *testing.T) {
	logs := captureEtcdRecordLogs(t)
	unavailable := errors.New("etcd unavailable")
	e := newEtcdRecordFailingStore(t, unavailable)

	_, err := e.CreateRecord("drafts", "d1", []byte("value"))
	if !errors.Is(err, unavailable) || errors.Is(err, ErrNoSpace) {
		t.Fatalf("CreateRecord error = %v, want it to wrap only %v", err, unavailable)
	}

	if got := etcdRecordNoSpaceWarnings(t, logs); got != 0 {
		t.Fatalf("logged %d out of space warnings for another error, want 0", got)
	}
}

// fillEtcdRecords rewrites a record until etcd refuses a write, and returns
// the error it refused it with. etcd keeps every revision until it is
// compacted, so each write grows its database. etcd checks its quota against
// the size it last committed, so it refuses writes a little after the quota is
// reached.
func fillEtcdRecords(t *testing.T, s Store, value []byte) error {
	t.Helper()

	for deadline := time.Now().Add(etcdRecordFillTimeout); time.Now().Before(deadline); {
		if _, err := s.UpdateRecord("drafts", "d1", value, AnyRevision); err != nil {
			return err
		}
	}

	t.Fatal("etcd accepted every write: its space quota was never reached")

	return nil
}

// TestEtcdRecordStoreOutOfSpace fills a real etcd to its space quota, then
// frees space the way the out of space error says to.
func TestEtcdRecordStoreOutOfSpace(t *testing.T) {
	server := startEmbeddedEtcd(t, func(cfg *embed.Config) { cfg.QuotaBackendBytes = etcdRecordTestQuota })
	s := server.open(t)
	logs := captureEtcdRecordLogs(t)
	ctx := context.Background()
	endpoint := server.Client.Endpoints()[0]
	value := bytes.Repeat([]byte("x"), etcdRecordTestQuota/16)

	if _, err := s.CreateRecord("drafts", "d1", value); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	topology := &Config{Version: "phenix.sandia.gov/v2", Kind: "Topology", Metadata: ConfigMetadata{Name: "t1"}}
	if err := s.Create(topology); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	requireEtcdRecordNoSpace(t, fillEtcdRecords(t, s, value), "UpdateRecord")

	alarms, err := server.Client.AlarmList(ctx)
	if err != nil {
		t.Fatalf("listing etcd alarms: %v", err)
	}

	if len(alarms.Alarms) != 1 || alarms.Alarms[0].GetAlarm() != pb.AlarmType_NOSPACE {
		t.Fatalf("etcd alarms = %v, want one NOSPACE alarm", alarms.Alarms)
	}

	// Every write is refused now, even a small one, and config writes too.
	_, err = s.CreateRecord("drafts", "d2", []byte("small"))
	requireEtcdRecordNoSpace(t, err, "CreateRecord")
	requireEtcdRecordNoSpace(t, s.Update(topology), "Update")
	requireEtcdRecordNoSpace(
		t, s.Create(&Config{Version: "phenix.sandia.gov/v2", Kind: "Topology", Metadata: ConfigMetadata{Name: "t2"}}),
		"Create",
	)

	if got := etcdRecordNoSpaceWarnings(t, logs); got != 1 {
		t.Fatalf("logged %d out of space warnings, want 1", got)
	}

	// Compact and defragment etcd, then clear its NOSPACE alarm.
	status, err := server.Client.Status(ctx, endpoint)
	if err != nil {
		t.Fatalf("reading etcd status: %v", err)
	}

	if _, err := server.Client.Compact(ctx, status.Header.GetRevision()); err != nil {
		t.Fatalf("compacting etcd: %v", err)
	}

	if _, err := server.Client.Defragment(ctx, endpoint); err != nil {
		t.Fatalf("defragmenting etcd: %v", err)
	}

	if _, err := server.Client.AlarmDisarm(ctx, &clientv3.AlarmMember{
		MemberID: alarms.Alarms[0].GetMemberID(),
		Alarm:    pb.AlarmType_NOSPACE,
	}); err != nil {
		t.Fatalf("clearing the NOSPACE alarm: %v", err)
	}

	if _, err := s.CreateRecord("drafts", "d2", []byte("small")); err != nil {
		t.Fatalf("CreateRecord after freeing space returned error: %v", err)
	}

	// The next outage is logged again.
	requireEtcdRecordNoSpace(t, fillEtcdRecords(t, s, value), "UpdateRecord")

	if got := etcdRecordNoSpaceWarnings(t, logs); got != 2 {
		t.Fatalf("logged %d out of space warnings after a second outage, want 2", got)
	}
}
