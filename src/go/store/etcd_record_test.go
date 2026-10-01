package store

import (
	"strings"
	"testing"
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
