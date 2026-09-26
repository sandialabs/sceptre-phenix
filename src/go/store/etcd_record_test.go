package store

import (
	"errors"
	"strings"
	"testing"

	"go.etcd.io/etcd/v3/clientv3"
	pb "go.etcd.io/etcd/v3/etcdserver/etcdserverpb"
	"go.etcd.io/etcd/v3/mvcc/mvccpb"
)

func TestEtcdRecordKeyEncoding(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		key       string
		expect    string
	}{
		{name: "simple", namespace: "drafts", key: "draft-1", expect: "phenix/records/drafts/draft-1"},
		{
			name:      "hierarchical",
			namespace: "drafts",
			key:       "draft-1/chunks/0001",
			expect:    "phenix/records/drafts/draft-1/chunks/0001",
		},
		{name: "escaped segment", namespace: "drafts", key: "draft 1/a+b", expect: "phenix/records/drafts/draft%201/a+b"},
		{name: "escaped percent", namespace: "drafts", key: "draft%2F1", expect: "phenix/records/drafts/draft%252F1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EtcdRecordKey(tt.namespace, tt.key)
			if got != tt.expect {
				t.Fatalf("EtcdRecordKey(%q, %q) = %q, want %q", tt.namespace, tt.key, got, tt.expect)
			}

			decoded, err := EtcdRecordKeyName(tt.namespace, got)
			if err != nil {
				t.Fatalf("EtcdRecordKeyName(%q) returned error: %v", got, err)
			}

			if decoded != tt.key {
				t.Fatalf("EtcdRecordKeyName(%q) = %q, want %q", got, decoded, tt.key)
			}
		})
	}
}

func TestEtcdRecordNamespacesCannotCollideOrEscape(t *testing.T) {
	if EtcdRecordNamespacePrefix("drafts") == EtcdRecordNamespacePrefix("drafts2") {
		t.Fatal("distinct namespaces must encode to distinct prefixes")
	}

	if strings.HasPrefix(EtcdRecordNamespacePrefix("drafts2"), EtcdRecordNamespacePrefix("drafts")) {
		t.Fatal("namespace prefixes must be terminated so one namespace cannot match another")
	}

	// Even if validation were bypassed, encoding keeps a namespace inside the
	// record key space.
	escaped := EtcdRecordNamespacePrefix("../../configs")
	if !strings.HasPrefix(escaped, etcdRecordRoot+"/") || strings.Count(escaped, "/") != 3 {
		t.Fatalf("namespace encoding allowed escaping the record key space: %q", escaped)
	}

	// Record keys must never collide with config keys, which are "<kind>/<name>".
	if !strings.HasPrefix(EtcdRecordKey("Topology", "test-topo"), etcdRecordRoot+"/") {
		t.Fatal("record keys must be written under the record root")
	}

	if _, err := EtcdRecordKeyName("drafts", "phenix/records/other/draft-1"); err == nil {
		t.Fatal("EtcdRecordKeyName should reject keys from other namespaces")
	}
}

func TestEtcdRecordPrefixIsEncodingPrefixPreserving(t *testing.T) {
	keys := []string{"draft-1", "draft-1/chunks/0001", "draft 1/a", "draft%2F1", "draft-10"}
	prefixes := []string{"", "draft-1", "draft-1/", "draft ", "draft%"}

	for _, prefix := range prefixes {
		encodedPrefix := EtcdRecordPrefix("drafts", prefix)

		for _, key := range keys {
			wantMatch := strings.HasPrefix(key, prefix)
			gotMatch := strings.HasPrefix(EtcdRecordKey("drafts", key), encodedPrefix)

			if wantMatch != gotMatch {
				t.Fatalf("prefix %q vs key %q: encoded match = %v, want %v", prefix, key, gotMatch, wantMatch)
			}
		}
	}
}

func TestEtcdCreateRecordOps(t *testing.T) {
	key := EtcdRecordKey("drafts", "draft-1")

	cmp, then, els := EtcdCreateRecordOps(key, []byte("value"))

	if string(cmp.Key) != key {
		t.Fatalf("cmp key = %q, want %q", cmp.Key, key)
	}

	if cmp.Target != pb.Compare_VERSION || cmp.Result != pb.Compare_EQUAL {
		t.Fatalf("cmp = (%v, %v), want version equality comparison", cmp.Target, cmp.Result)
	}

	if got := cmpVersion(cmp); got != 0 {
		t.Fatalf("cmp version = %d, want 0 (create only if absent)", got)
	}

	if !then.IsPut() || string(then.ValueBytes()) != "value" || string(then.KeyBytes()) != key {
		t.Fatal("create transaction must put the encoded value at the encoded key")
	}

	if !els.IsGet() || string(els.KeyBytes()) != key {
		t.Fatal("create transaction must read the existing record when the comparison fails")
	}
}

func TestEtcdUpdateRecordOps(t *testing.T) {
	key := EtcdRecordKey("drafts", "draft-1")

	cmp, then, els := EtcdUpdateRecordOps(key, []byte("value"), 7)

	if cmp.Target != pb.Compare_MOD || cmp.Result != pb.Compare_EQUAL || cmpModRevision(cmp) != 7 {
		t.Fatalf("cmp = (%v, %v, %d), want ModRevision == 7", cmp.Target, cmp.Result, cmpModRevision(cmp))
	}

	if !then.IsPut() || string(then.ValueBytes()) != "value" {
		t.Fatal("update transaction must put the new value")
	}

	if !els.IsGet() {
		t.Fatal("update transaction must read the current record when the comparison fails")
	}

	anyCmp, _, _ := EtcdUpdateRecordOps(key, []byte("value"), AnyRevision)

	if anyCmp.Target != pb.Compare_VERSION || anyCmp.Result != pb.Compare_GREATER || cmpVersion(anyCmp) != 0 {
		t.Fatalf("AnyRevision cmp = (%v, %v, %d), want version > 0", anyCmp.Target, anyCmp.Result, cmpVersion(anyCmp))
	}
}

func TestEtcdDeleteRecordOps(t *testing.T) {
	key := EtcdRecordKey("drafts", "draft-1")

	cmp, then, els := EtcdDeleteRecordOps(key, 9)

	if cmp.Target != pb.Compare_MOD || cmpModRevision(cmp) != 9 {
		t.Fatalf("cmp = (%v, %d), want ModRevision == 9", cmp.Target, cmpModRevision(cmp))
	}

	if !then.IsDelete() || string(then.KeyBytes()) != key {
		t.Fatal("delete transaction must delete the encoded key")
	}

	if !els.IsGet() {
		t.Fatal("delete transaction must read the current record when the comparison fails")
	}
}

func TestEtcdRecordTxnFailure(t *testing.T) {
	err := EtcdRecordTxnFailure("drafts", "draft-1", 7, nil)
	if !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("failure with no key/values = %v, want ErrRecordNotExist", err)
	}

	kvs := []*mvccpb.KeyValue{{Key: []byte(EtcdRecordKey("drafts", "draft-1")), ModRevision: 11}}

	err = EtcdRecordTxnFailure("drafts", "draft-1", 7, kvs)

	var conflict *RecordConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("failure with existing record = %v, want *RecordConflictError", err)
	}

	if conflict.Expected != 7 || conflict.Actual != 11 {
		t.Fatalf("conflict revisions = (%d, %d), want (7, 11)", conflict.Expected, conflict.Actual)
	}
}

func TestEtcdRecordEnvelopeRoundTrip(t *testing.T) {
	value := []byte("chunk")

	encoded, err := encodeRecordEnvelope(etcdRecordEnvelope{Value: value})
	if err != nil {
		t.Fatalf("encodeRecordEnvelope returned error: %v", err)
	}

	envelope, err := decodeRecordEnvelope(encoded)
	if err != nil {
		t.Fatalf("decodeRecordEnvelope returned error: %v", err)
	}

	record := envelope.record("drafts", "draft-1", 4)

	if record.Namespace != "drafts" || record.Key != "draft-1" || record.Revision != 4 {
		t.Fatalf("record = %+v, want namespace drafts, key draft-1, revision 4", record)
	}

	if string(record.Value) != "chunk" {
		t.Fatalf("record value = %q, want chunk", record.Value)
	}

	record.Value[0] = 'X'

	if string(envelope.Value) != "chunk" {
		t.Fatal("record value must not share the envelope backing array")
	}
}

func cmpVersion(c clientv3.Cmp) int64 {
	compare := pb.Compare(c)

	return compare.GetVersion()
}

func cmpModRevision(c clientv3.Cmp) int64 {
	compare := pb.Compare(c)

	return compare.GetModRevision()
}
