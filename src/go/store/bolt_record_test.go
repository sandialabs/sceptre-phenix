package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.etcd.io/bbolt"
)

func TestRecordPersistsAcrossReopen(t *testing.T) {
	s, path := newTestBoltDB(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	reopened := new(BoltDB)
	if err := reopened.Init(Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("reopening BoltDB returned error: %v", err)
	}

	if got, err := reopened.GetRecord("drafts", "draft-1"); err != nil {
		t.Errorf("GetRecord(drafts, draft-1) after reopen error = %v", err)
	} else if diff := cmp.Diff(created, got); diff != "" {
		t.Errorf("GetRecord(drafts, draft-1) after reopen mismatch (-want +got):\n%s", diff)
	}

	next, err := reopened.CreateRecord("drafts", "draft-2", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord after reopen returned error: %v", err)
	}

	if next.Revision <= created.Revision {
		t.Errorf("CreateRecord(drafts, draft-2) after reopen revision = %d, want > %d (revisions must be monotonic)",
			next.Revision, created.Revision)
	}

	if _, err := reopened.UpdateRecord("drafts", "draft-1", []byte("v2"), created.Revision); err != nil {
		t.Errorf("UpdateRecord(drafts, draft-1) after reopen at the revision read before it error = %v", err)
	}
}

func TestRecordsDoNotCollideWithConfigs(t *testing.T) {
	s, _ := newTestBoltDB(t)

	config, err := NewConfig("Topology/test-topo")
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	if err := s.Create(config); err != nil {
		t.Fatalf("Create config returned error: %v", err)
	}

	if _, err := s.CreateRecord("Topology", "test-topo", []byte("record")); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	configs, err := s.List("Topology")
	if err != nil {
		t.Fatalf("List configs returned error: %v", err)
	}

	names := make([]string, 0, len(configs))
	for _, c := range configs {
		names = append(names, c.Metadata.Name)
	}

	if diff := cmp.Diff([]string{"test-topo"}, names); diff != "" {
		t.Errorf("List(Topology) names mismatch, want a single test-topo config (-want +got):\n%s", diff)
	}

	record, err := s.GetRecord("Topology", "test-topo")
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	if string(record.Value) != "record" {
		t.Errorf("GetRecord(Topology, test-topo) value = %q, want record", record.Value)
	}
}

// rawBoltConfig returns the value the BoltDB file at path keeps for a config.
func rawBoltConfig(t *testing.T, path, kind, name string) []byte {
	t.Helper()

	db, err := bbolt.Open(path, boltFileMode, &bbolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("opening BoltDB file %s: %v", path, err)
	}

	defer func() { _ = db.Close() }()

	var value []byte

	err = db.View(func(tx *bbolt.Tx) error {
		if bucket := tx.Bucket([]byte(kind)); bucket != nil {
			value = slices.Clone(bucket.Get([]byte(name)))
		}

		return nil
	})
	if err != nil || value == nil {
		t.Fatalf("reading config %s/%s from BoltDB: %v, value %q", kind, name, err, value)
	}

	return value
}

// TestConfigStructuredAnnotation checks that a structured annotation is stored as one string.
func TestConfigStructuredAnnotation(t *testing.T) {
	s, path := newTestBoltDB(t)

	const reference = `{"digest":"sha256:aa","id":"bb","path":"/phenix/topologies/site/builder.yaml"}`

	config, err := DecodeConfigJSON([]byte(`{"apiVersion": "phenix.sandia.gov/v1", "kind": "Topology",
		"metadata": {"name": "site", "annotations": {"keep": "v", "builder-doc": ` + reference + `}},
		"spec": {"nodes": []}}`))
	if err != nil {
		t.Fatalf("DecodeConfigJSON returned error: %v", err)
	}

	if err := s.Create(config); err != nil {
		t.Fatalf("Create config returned error: %v", err)
	}

	want := map[string]string{"keep": "v", "builder-doc": reference}

	// check reads the config back as stored and through the store.
	check := func(t *testing.T) {
		t.Helper()

		var plain struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}

		raw := rawBoltConfig(t, path, "Topology", "site")
		if err := json.Unmarshal(raw, &plain); err != nil {
			t.Errorf("the stored value does not load with plain annotations: %v\n%s", err, raw)
		} else if diff := cmp.Diff(want, plain.Metadata.Annotations); diff != "" {
			t.Errorf("stored annotations mismatch (-want +got):\n%s", diff)
		}

		got, err := NewConfig("Topology/site")
		if err != nil {
			t.Fatalf("NewConfig returned error: %v", err)
		}

		if err := s.Get(got); err != nil {
			t.Errorf("Get(Topology/site) error = %v", err)
		} else if diff := cmp.Diff(Annotations(want), got.Metadata.Annotations); diff != "" {
			t.Errorf("Get(Topology/site) annotations mismatch (-want +got):\n%s", diff)
		}

		listed, err := s.List("Topology")
		if err != nil {
			t.Fatalf("List(Topology) error = %v", err)
		}

		if len(listed) != 1 {
			t.Fatalf("List(Topology) = %d configs, want the config", len(listed))
		}

		if diff := cmp.Diff(Annotations(want), listed[0].Metadata.Annotations); diff != "" {
			t.Errorf("List(Topology) annotations mismatch (-want +got):\n%s", diff)
		}
	}

	t.Run("create", check)

	config.Spec = map[string]any{"nodes": []any{}, "changed": true}
	if err := s.Update(config); err != nil {
		t.Fatalf("Update config returned error: %v", err)
	}

	t.Run("update", check)
}

func TestConfigErrorsAreTyped(t *testing.T) {
	s, _ := newTestBoltDB(t)

	// Callers such as the Builder's publication tell a missing or duplicate
	// config apart from a failing store by these errors.
	config, err := NewConfig("Topology/test-topo")
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	if err := s.Get(config); !errors.Is(err, ErrNotExist) {
		t.Errorf("Get(missing Topology/test-topo) error = %v, want ErrNotExist", err)
	}

	if err := s.Update(config); !errors.Is(err, ErrNotExist) {
		t.Errorf("Update(missing Topology/test-topo) error = %v, want ErrNotExist", err)
	}

	if err := s.Create(config); err != nil {
		t.Fatalf("Create config returned error: %v", err)
	}

	if err := s.Create(config); !errors.Is(err, ErrExist) {
		t.Errorf("Create(existing Topology/test-topo) error = %v, want ErrExist", err)
	}
}

// newTestBoltDB returns an empty BoltDB store and the path of its file.
func newTestBoltDB(t *testing.T) (*BoltDB, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "phenix.bdb")

	s := new(BoltDB)
	if err := s.Init(Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("initializing BoltDB store returned error: %v", err)
	}

	return s, path
}

// rawBoltRecord returns the value the BoltDB file at path keeps for a record.
func rawBoltRecord(t *testing.T, path, namespace, key string) []byte {
	t.Helper()

	db, err := bbolt.Open(path, boltFileMode, &bbolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("opening BoltDB file %s: %v", path, err)
	}

	defer func() { _ = db.Close() }()

	var value []byte

	err = db.View(func(tx *bbolt.Tx) error {
		if bucket := boltNamespaceBucket(tx, namespace); bucket != nil {
			value = slices.Clone(bucket.Get([]byte(key)))
		}

		return nil
	})
	if err != nil || value == nil {
		t.Fatalf("reading record %s/%s from BoltDB: %v, value %q", namespace, key, err, value)
	}

	return value
}

// TestRecordStoresValueAsIs checks that the file holds the value of a record
// byte for byte after its header, not an encoding of it.
func TestRecordStoresValueAsIs(t *testing.T) {
	s, path := newTestBoltDB(t)

	value := []byte{0x00, 0x1f, 0x8b, 0xff, '{', '"'}

	created, err := s.CreateRecord("drafts", "raw", value)
	if err != nil {
		t.Fatalf("CreateRecord(drafts, raw) returned error: %v", err)
	}

	raw := rawBoltRecord(t, path, "drafts", "raw")

	if got, want := len(raw), boltRecordHeaderSize+len(value); got != want {
		t.Errorf("stored record drafts/raw is %d bytes, want %d", got, want)
	}

	if !bytes.HasSuffix(raw, value) {
		t.Errorf("stored record drafts/raw = %x, want it to end with the value %x", raw, value)
	}

	decoded, err := decodeBoltRecord(raw)
	if err != nil {
		t.Fatalf("decodeBoltRecord(drafts/raw) returned error: %v", err)
	}

	got := decoded.record("drafts", "raw")
	if diff := cmp.Diff(created, got); diff != "" {
		t.Errorf("decodeBoltRecord(drafts/raw) mismatch (-want +got):\n%s", diff)
	}
}

func TestRecordUnknownFormatFailsTheRead(t *testing.T) {
	for name, value := range map[string][]byte{
		"short":          {boltRecordFormat, 0, 0},
		"unknown format": append([]byte{2}, make([]byte, boltRecordHeaderSize)...),
		"old JSON":       []byte(`{"value":"djE=","revision":1,"created":"2024-01-01T00:00:00Z","updated":"2024-01-01T00:00:00Z"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeBoltRecord(value); !errors.Is(err, errBoltRecordFormat) {
				t.Errorf("decodeBoltRecord(%q) error = %v, want errBoltRecordFormat", value, err)
			}
		})
	}
}

// writeRawBoltRecords writes each value to the BoltDB file at path as it is,
// by key, in the namespace bucket. A nil value writes a nested bucket.
func writeRawBoltRecords(t *testing.T, path, namespace string, values map[string][]byte) {
	t.Helper()

	db, err := bbolt.Open(path, boltFileMode, nil)
	if err != nil {
		t.Fatalf("opening BoltDB file %s: %v", path, err)
	}

	defer func() { _ = db.Close() }()

	err = db.Update(func(tx *bbolt.Tx) error {
		bucket, err := boltEnsureNamespaceBucket(tx, namespace)
		if err != nil {
			return err
		}

		for key, value := range values {
			if value == nil {
				_, err = bucket.CreateBucket([]byte(key))
			} else {
				err = bucket.Put([]byte(key), value)
			}

			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("writing records to %s/%s in BoltDB: %v", path, namespace, err)
	}
}

// TestBoltRecordReadsRefuseUnknownFormat checks that each read and write of a
// record in an unknown format fails, and that record reads skip a nested bucket.
func TestBoltRecordReadsRefuseUnknownFormat(t *testing.T) {
	s, path := newTestBoltDB(t)

	good, err := s.CreateRecord("corrupt", "a-good", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	writeRawBoltRecords(t, path, "corrupt", map[string][]byte{"b-bad": []byte(`{"value":"djE="}`)})

	for name, err := range map[string]error{
		"GetRecord":    func() error { _, err := s.GetRecord("corrupt", "b-bad"); return err }(),
		"ListRecords":  func() error { _, err := s.ListRecords("corrupt", ""); return err }(),
		"UpdateRecord": func() error { _, err := s.UpdateRecord("corrupt", "b-bad", []byte("v2"), AnyRevision); return err }(),
		"DeleteRecord": s.DeleteRecord("corrupt", "b-bad", AnyRevision),
		"DeleteRecordPrefix": func() error {
			_, err := s.DeleteRecordPrefix("corrupt", "b")
			return err
		}(),
	} {
		if !errors.Is(err, errBoltRecordFormat) {
			t.Errorf("%s(corrupt, b-bad) error = %v, want errBoltRecordFormat", name, err)
		}
	}

	if keys, err := s.ListRecordKeys("corrupt", ""); err != nil {
		t.Errorf("ListRecordKeys(corrupt) error = %v", err)
	} else if diff := cmp.Diff([]string{"a-good", "b-bad"}, keys); diff != "" {
		t.Errorf("ListRecordKeys(corrupt), which decodes no value, mismatch (-want +got):\n%s", diff)
	}

	if got, err := s.GetRecord("corrupt", "a-good"); err != nil {
		t.Errorf("GetRecord(corrupt, a-good) error = %v", err)
	} else if diff := cmp.Diff(good, got); diff != "" {
		t.Errorf("GetRecord(corrupt, a-good) after the failed writes mismatch (-want +got):\n%s", diff)
	}

	if _, err := s.CreateRecord("nested", "n-record", []byte("v1")); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	writeRawBoltRecords(t, path, "nested", map[string][]byte{"n-bucket": nil})

	if records, err := s.ListRecords("nested", "n"); err != nil {
		t.Errorf("ListRecords(nested, n) error = %v", err)
	} else if diff := cmp.Diff([]string{"n-record"}, keysOfBoltTestRecords(records)); diff != "" {
		t.Errorf("ListRecords(nested, n) keys mismatch, want the nested bucket skipped (-want +got):\n%s", diff)
	}

	if deleted, err := s.DeleteRecordPrefix("nested", "n"); err != nil || deleted != 1 {
		t.Errorf("DeleteRecordPrefix(nested, n) = %d, %v, want only the record deleted", deleted, err)
	}
}

// keysOfBoltTestRecords returns the key of each record.
func keysOfBoltTestRecords(records Records) []string {
	keys := make([]string, 0, len(records))
	for _, record := range records {
		keys = append(keys, record.Key)
	}

	return keys
}

// TestBoltRecordStoreFailsToOpen checks that each record call reports a file
// that does not open, and that a failed open leaves the store usable.
func TestBoltRecordStoreFailsToOpen(t *testing.T) {
	s, path := newTestBoltDB(t)

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing %s: %v", path, err)
	}

	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("creating directory %s: %v", path, err)
	}

	ran := false

	for name, err := range map[string]error{
		"ListRecords":    func() error { _, err := s.ListRecords("drafts", ""); return err }(),
		"ListRecordKeys": func() error { _, err := s.ListRecordKeys("drafts", ""); return err }(),
		"GetRecord":      func() error { _, err := s.GetRecord("drafts", "draft-1"); return err }(),
		"CreateRecord":   func() error { _, err := s.CreateRecord("drafts", "draft-1", nil); return err }(),
		"RecordTxn":      s.RecordTxn(func(RecordTx) error { ran = true; return nil }),
	} {
		if err == nil || !strings.Contains(err.Error(), "opening BoltDB file "+path) {
			t.Errorf("%s error = %v, want the failure to open %s", name, err, path)
		}
	}

	if ran {
		t.Error("RecordTxn ran its function on a store that does not open")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing directory %s: %v", path, err)
	}

	if _, err := s.CreateRecord("drafts", "draft-1", []byte("v1")); err != nil {
		t.Errorf("CreateRecord after the file is back error = %v, want nil", err)
	}
}
