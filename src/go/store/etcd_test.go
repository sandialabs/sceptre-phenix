package store

import (
	"errors"
	"testing"

	"go.etcd.io/etcd/v3/clientv3"
	pb "go.etcd.io/etcd/v3/etcdserver/etcdserverpb"
	"go.etcd.io/etcd/v3/mvcc/mvccpb"
)

// A refused conditional write reports a missing key as ErrNotExist and a key
// at another revision as ErrConflict. The transactions themselves need an etcd
// server, which the tests do not run.
func TestEtcdTxnFailure(t *testing.T) {
	c, _ := NewConfig("topology/a")
	c.IfRevision = 4

	read := func(kvs ...*mvccpb.KeyValue) *clientv3.TxnResponse {
		return &clientv3.TxnResponse{Responses: []*pb.ResponseOp{{
			Response: &pb.ResponseOp_ResponseRange{ResponseRange: &pb.RangeResponse{Kvs: kvs}},
		}}}
	}

	if err := etcdTxnFailure(read(), c); !errors.Is(err, ErrNotExist) {
		t.Errorf("missing key: err = %v, want ErrNotExist", err)
	}

	if err := etcdTxnFailure(read(&mvccpb.KeyValue{ModRevision: 9}), c); !errors.Is(err, ErrConflict) {
		t.Errorf("key at another revision: err = %v, want ErrConflict", err)
	}
}

func TestEtcdInitialized(t *testing.T) {
	get := func(kvs ...*mvccpb.KeyValue) *clientv3.GetResponse {
		return &clientv3.GetResponse{Kvs: kvs}
	}

	if etcdInitialized(get()) {
		t.Error("missing key: initialized = true, want false")
	}

	if etcdInitialized(get(&mvccpb.KeyValue{Value: []byte("false")})) {
		t.Error(`value "false": initialized = true, want false`)
	}

	if !etcdInitialized(get(&mvccpb.KeyValue{Value: []byte("true")})) {
		t.Error(`value "true": initialized = false, want true`)
	}
}
