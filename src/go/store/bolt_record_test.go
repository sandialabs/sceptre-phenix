package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"go.etcd.io/bbolt"
)

func TestRecordCreateGetUpdateDelete(t *testing.T) {
	s, _ := newTestBoltDB(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	if created.Revision <= 0 {
		t.Fatalf("created revision = %d, want > 0", created.Revision)
	}

	if string(created.Value) != "v1" || created.Namespace != "drafts" || created.Key != "draft-1" {
		t.Fatalf("created record = %+v, want drafts/draft-1 = v1", created)
	}

	// The revision a write returns is the one every later read reports, or no
	// caller could ever pass it back as an expected revision.
	requireRecord(t, s, "drafts", "draft-1", "v1", created.Revision)

	updated, err := s.UpdateRecord("drafts", "draft-1", []byte("v2"), created.Revision)
	if err != nil {
		t.Fatalf("UpdateRecord returned error: %v", err)
	}

	if updated.Revision <= created.Revision {
		t.Fatalf("updated revision = %d, want > %d", updated.Revision, created.Revision)
	}

	if !updated.Created.Equal(created.Created) {
		t.Fatalf("updated created timestamp = %v, want %v", updated.Created, created.Created)
	}

	if updated.Updated.Before(created.Updated) {
		t.Fatalf("updated timestamp = %v, want >= %v", updated.Updated, created.Updated)
	}

	got := requireRecord(t, s, "drafts", "draft-1", "v2", updated.Revision)

	if !got.Created.Equal(created.Created) || !got.Updated.Equal(updated.Updated) {
		t.Fatalf("stored timestamps = (%v, %v), want (%v, %v)", got.Created, got.Updated, created.Created, updated.Updated)
	}

	listed, err := s.ListRecords("drafts", "")
	if err != nil {
		t.Fatalf("ListRecords returned error: %v", err)
	}

	if len(listed) != 1 || listed[0].Revision != updated.Revision {
		t.Fatalf("ListRecords = %+v, want one record at revision %d", listed, updated.Revision)
	}

	if err := s.DeleteRecord("drafts", "draft-1", updated.Revision); err != nil {
		t.Fatalf("DeleteRecord returned error: %v", err)
	}

	if _, err := s.GetRecord("drafts", "draft-1"); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("GetRecord after delete error = %v, want ErrRecordNotExist", err)
	}
}

func TestRecordCreateIfAbsent(t *testing.T) {
	s, _ := newTestBoltDB(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	_, err = s.CreateRecord("drafts", "draft-1", []byte("v2"))

	var exists *RecordExistError
	if !errors.As(err, &exists) || exists.Namespace != "drafts" || exists.Key != "draft-1" {
		t.Fatalf("second CreateRecord error = %v, want *RecordExistError for drafts/draft-1", err)
	}

	// A failed create must neither overwrite the value nor move the revision.
	requireRecord(t, s, "drafts", "draft-1", "v1", created.Revision)
}

func TestRecordStaleRevisionsConflict(t *testing.T) {
	s, _ := newTestBoltDB(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	updated, err := s.UpdateRecord("drafts", "draft-1", []byte("v2"), created.Revision)
	if err != nil {
		t.Fatalf("UpdateRecord returned error: %v", err)
	}

	stale := created.Revision

	_, err = s.UpdateRecord("drafts", "draft-1", []byte("stale"), stale)
	requireConflict(t, "UpdateRecord", err, stale, updated.Revision)

	requireConflict(t, "DeleteRecord", s.DeleteRecord("drafts", "draft-1", stale), stale, updated.Revision)

	// Neither conflicting write may apply.
	requireRecord(t, s, "drafts", "draft-1", "v2", updated.Revision)

	forced, err := s.UpdateRecord("drafts", "draft-1", []byte("v3"), AnyRevision)
	if err != nil {
		t.Fatalf("UpdateRecord with AnyRevision returned error: %v", err)
	}

	if forced.Revision <= updated.Revision {
		t.Fatalf("revision after AnyRevision update = %d, want > %d", forced.Revision, updated.Revision)
	}

	if err := s.DeleteRecord("drafts", "draft-1", AnyRevision); err != nil {
		t.Fatalf("DeleteRecord with AnyRevision returned error: %v", err)
	}
}

func TestRecordRecreateGetsNewRevision(t *testing.T) {
	s, _ := newTestBoltDB(t)

	first, err := s.CreateRecord("published", "doc-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	if err := s.DeleteRecord("published", "doc-1", first.Revision); err != nil {
		t.Fatalf("DeleteRecord returned error: %v", err)
	}

	second, err := s.CreateRecord("published", "doc-1", []byte("v2"))
	if err != nil {
		t.Fatalf("CreateRecord after delete returned error: %v", err)
	}

	if second.Revision <= first.Revision {
		t.Fatalf("recreated revision = %d, want > %d", second.Revision, first.Revision)
	}

	// A writer holding the deleted record's revision must not affect the record
	// that replaced it, which is what makes deleting an observed revision safe.
	requireConflict(t, "DeleteRecord", s.DeleteRecord("published", "doc-1", first.Revision), first.Revision, second.Revision)

	_, err = s.UpdateRecord("published", "doc-1", []byte("stale"), first.Revision)
	requireConflict(t, "UpdateRecord", err, first.Revision, second.Revision)

	requireRecord(t, s, "published", "doc-1", "v2", second.Revision)
}

func TestRecordMissingRecordErrors(t *testing.T) {
	s, _ := newTestBoltDB(t)

	_, err := s.GetRecord("drafts", "missing")

	var missing *RecordNotExistError
	if !errors.As(err, &missing) || missing.Namespace != "drafts" || missing.Key != "missing" {
		t.Fatalf("GetRecord error = %v, want *RecordNotExistError for drafts/missing", err)
	}

	if _, err := s.UpdateRecord("drafts", "missing", []byte("v"), AnyRevision); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("UpdateRecord error = %v, want ErrRecordNotExist", err)
	}

	if _, err := s.UpdateRecord("drafts", "missing", []byte("v"), 7); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("UpdateRecord with a revision error = %v, want ErrRecordNotExist", err)
	}

	if err := s.DeleteRecord("drafts", "missing", AnyRevision); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("DeleteRecord error = %v, want ErrRecordNotExist", err)
	}

	if err := s.DeleteRecord("drafts", "missing", 7); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("DeleteRecord with a revision error = %v, want ErrRecordNotExist", err)
	}

	records, err := s.ListRecords("unknown-namespace", "")
	if err != nil || len(records) != 0 {
		t.Fatalf("ListRecords on unknown namespace = %d records, %v; want none", len(records), err)
	}

	keys, err := s.ListRecordKeys("unknown-namespace", "")
	if err != nil || len(keys) != 0 {
		t.Fatalf("ListRecordKeys on unknown namespace = %v, %v; want none", keys, err)
	}

	deleted, err := s.DeleteRecordPrefix("unknown-namespace", "draft-1/")
	if err != nil || deleted != 0 {
		t.Fatalf("DeleteRecordPrefix on unknown namespace = %d, %v; want 0", deleted, err)
	}
}

func TestRecordListPrefixIsolation(t *testing.T) {
	s, _ := newTestBoltDB(t)

	keys := []string{"draft-1/chunks/0001", "draft-1/chunks/0002", "draft-1/meta", "draft-10/meta", "draft-2/meta"}

	// Created out of order, so ordering comes from the store.
	for _, i := range []int{3, 0, 4, 2, 1} {
		if _, err := s.CreateRecord("drafts", keys[i], []byte(keys[i])); err != nil {
			t.Fatalf("CreateRecord(%q) returned error: %v", keys[i], err)
		}
	}

	// Namespaces that share a name prefix or keys must stay separate.
	createRecords(t, s, "chunks", "draft-1/chunks/0001")
	createRecords(t, s, "drafts2", "draft-1/meta")

	all, err := s.ListRecords("drafts", "")
	if err != nil {
		t.Fatalf("ListRecords returned error: %v", err)
	}

	if got := recordKeys(all); !reflect.DeepEqual(got, keys) {
		t.Fatalf("ListRecords(all) keys = %v, want %v in key order", got, keys)
	}

	for i, record := range all {
		if record.Namespace != "drafts" || string(record.Value) != keys[i] {
			t.Fatalf("ListRecords(all)[%d] = %+v, want namespace drafts and value %q", i, record, keys[i])
		}
	}

	chunks, err := s.ListRecords("drafts", "draft-1/chunks/")
	if err != nil {
		t.Fatalf("ListRecords with prefix returned error: %v", err)
	}

	if got := recordKeys(chunks); !reflect.DeepEqual(got, keys[:2]) {
		t.Fatalf("ListRecords(prefix) keys = %v, want %v", got, keys[:2])
	}

	// A prefix is a byte prefix, not a path segment: "draft-1" also matches
	// "draft-10".
	requireKeys(t, s, "drafts", "draft-1", keys[:4])
	requireKeys(t, s, "drafts", "draft-1/", keys[:3])
	requireKeys(t, s, "drafts", "draft-3", []string{})

	other, err := s.ListRecords("chunks", "")
	if err != nil {
		t.Fatalf("ListRecords in second namespace returned error: %v", err)
	}

	if len(other) != 1 || other[0].Namespace != "chunks" {
		t.Fatalf("namespaces are not isolated: %+v", other)
	}

	requireKeys(t, s, "drafts2", "", []string{"draft-1/meta"})
}

func TestRecordListOrdersDecodedKeys(t *testing.T) {
	s, _ := newTestBoltDB(t)

	// Characters a store may escape sort differently once escaped: "é" sorts
	// after "+" as a key, but its escape "%C3%A9" sorts before it.
	keys := []string{"a b", "a!b", "a%b", "a+b", "a/b", "a~b", "aéb"}

	createRecords(t, s, "drafts", keys...)

	want := slices.Clone(keys)
	slices.Sort(want)

	requireKeys(t, s, "drafts", "a", want)

	records, err := s.ListRecords("drafts", "a")
	if err != nil {
		t.Fatalf("ListRecords returned error: %v", err)
	}

	if got := recordKeys(records); !reflect.DeepEqual(got, want) {
		t.Fatalf("ListRecords keys = %q, want %q", got, want)
	}

	for _, record := range records {
		if string(record.Value) != record.Key {
			t.Fatalf("record %q holds %q, want its own key", record.Key, record.Value)
		}
	}
}

func TestRecordListKeysPrefixEdges(t *testing.T) {
	s, _ := newTestBoltDB(t)

	// Neighbouring keys sit just outside each prefix on either side.
	const size = 3

	exact := make([]string, 0, size)
	for i := range size {
		exact = append(exact, fmt.Sprintf("exact/%05d", i))
	}

	over := make([]string, 0, size+1)
	for i := range size + 1 {
		over = append(over, fmt.Sprintf("over/%05d", i))
	}

	createRecords(t, s, "chunks", "exact", "exact0", "over0")
	createRecords(t, s, "chunks", exact...)
	createRecords(t, s, "chunks", over...)

	requireKeys(t, s, "chunks", "exact/", exact)
	requireKeys(t, s, "chunks", "over/", over)

	all, err := s.ListRecordKeys("chunks", "")
	if err != nil {
		t.Fatalf("ListRecordKeys returned error: %v", err)
	}

	if len(all) != len(exact)+len(over)+3 || !slices.IsSorted(all) {
		t.Fatalf("ListRecordKeys(all) returned %d keys (sorted %v), want %d sorted keys",
			len(all), slices.IsSorted(all), len(exact)+len(over)+3)
	}
}

func TestRecordDeletePrefix(t *testing.T) {
	s, _ := newTestBoltDB(t)

	createRecords(t, s, "drafts", "draft-1/chunks/0001", "draft-1/chunks/0002", "draft-1/meta", "draft-10/meta", "draft-2/meta")
	createRecords(t, s, "chunks", "draft-1/chunks/0001")
	createRecords(t, s, "drafts2", "draft-1/meta")

	deleted, err := s.DeleteRecordPrefix("drafts", "draft-1/")
	if err != nil {
		t.Fatalf("DeleteRecordPrefix returned error: %v", err)
	}

	if deleted != 3 {
		t.Fatalf("DeleteRecordPrefix deleted %d records, want 3", deleted)
	}

	requireKeys(t, s, "drafts", "", []string{"draft-10/meta", "draft-2/meta"})

	// Deletion never escapes its namespace, even into one sharing its name.
	requireKeys(t, s, "chunks", "", []string{"draft-1/chunks/0001"})
	requireKeys(t, s, "drafts2", "", []string{"draft-1/meta"})

	if _, err := s.DeleteRecordPrefix("drafts", ""); !errors.Is(err, ErrInvalidRecordKey) {
		t.Fatalf("DeleteRecordPrefix with empty prefix error = %v, want ErrInvalidRecordKey", err)
	}

	if deleted, err := s.DeleteRecordPrefix("drafts", "draft-1/"); err != nil || deleted != 0 {
		t.Fatalf("repeated DeleteRecordPrefix = %d, %v; want 0 and no error", deleted, err)
	}
}

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

	got := requireRecord(t, reopened, "drafts", "draft-1", "v1", created.Revision)

	if !got.Created.Equal(created.Created) {
		t.Fatalf("created timestamp after reopen = %v, want %v", got.Created, created.Created)
	}

	next, err := reopened.CreateRecord("drafts", "draft-2", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord after reopen returned error: %v", err)
	}

	if next.Revision <= created.Revision {
		t.Fatalf("revision after reopen = %d, want > %d (revisions must be monotonic)", next.Revision, created.Revision)
	}

	if _, err := reopened.UpdateRecord("drafts", "draft-1", []byte("v2"), created.Revision); err != nil {
		t.Fatalf("UpdateRecord after reopen with the revision read before it returned error: %v", err)
	}
}

func TestRecordValuesAreCopied(t *testing.T) {
	s, _ := newTestBoltDB(t)

	value := []byte("v1")

	created, err := s.CreateRecord("drafts", "draft-1", value)
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	value[0] = 'X'
	created.Value[1] = 'X'

	got := requireRecord(t, s, "drafts", "draft-1", "v1", created.Revision)
	got.Value[0] = 'X'

	requireRecord(t, s, "drafts", "draft-1", "v1", created.Revision)
}

func TestRecordRejectsInvalidNamespacesAndKeys(t *testing.T) {
	s, _ := newTestBoltDB(t)

	if _, err := s.CreateRecord("bad/namespace", "draft-1", nil); !errors.Is(err, ErrInvalidRecordNamespace) {
		t.Fatalf("CreateRecord with invalid namespace error = %v, want ErrInvalidRecordNamespace", err)
	}

	if _, err := s.CreateRecord("drafts", "../escape", nil); !errors.Is(err, ErrInvalidRecordKey) {
		t.Fatalf("CreateRecord with escaping key error = %v, want ErrInvalidRecordKey", err)
	}

	if _, err := s.ListRecords("drafts", "../escape"); !errors.Is(err, ErrInvalidRecordKey) {
		t.Fatalf("ListRecords with escaping prefix error = %v, want ErrInvalidRecordKey", err)
	}

	if _, err := s.ListRecordKeys("../escape", ""); !errors.Is(err, ErrInvalidRecordNamespace) {
		t.Fatalf("ListRecordKeys with escaping namespace error = %v, want ErrInvalidRecordNamespace", err)
	}

	if _, err := s.UpdateRecord("drafts", "draft-1/", nil, AnyRevision); !errors.Is(err, ErrInvalidRecordKey) {
		t.Fatalf("UpdateRecord with trailing separator error = %v, want ErrInvalidRecordKey", err)
	}

	if err := s.DeleteRecord("", "draft-1", AnyRevision); !errors.Is(err, ErrInvalidRecordNamespace) {
		t.Fatalf("DeleteRecord with empty namespace error = %v, want ErrInvalidRecordNamespace", err)
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

	if len(configs) != 1 || configs[0].Metadata.Name != "test-topo" {
		t.Fatalf("configs = %+v, want a single test-topo config", configs)
	}

	record, err := s.GetRecord("Topology", "test-topo")
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	if string(record.Value) != "record" {
		t.Fatalf("record value = %q, want record", record.Value)
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

	config, err := NewConfigFromJSON([]byte(`{"apiVersion": "phenix.sandia.gov/v1", "kind": "Topology",
		"metadata": {"name": "site", "annotations": {"keep": "v", "builder-doc": ` + reference + `}},
		"spec": {"nodes": []}}`))
	if err != nil {
		t.Fatalf("NewConfigFromJSON returned error: %v", err)
	}

	if err := s.Create(config); err != nil {
		t.Fatalf("Create config returned error: %v", err)
	}

	want := map[string]string{"keep": "v", "builder-doc": reference}

	check := func(stage string) {
		t.Helper()

		var plain struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}

		raw := rawBoltConfig(t, path, "Topology", "site")
		if err := json.Unmarshal(raw, &plain); err != nil {
			t.Fatalf("%s: the stored value does not load with plain annotations: %v\n%s", stage, err, raw)
		}

		if !reflect.DeepEqual(plain.Metadata.Annotations, want) {
			t.Fatalf("%s: stored annotations = %v, want %v", stage, plain.Metadata.Annotations, want)
		}

		got, err := NewConfig("Topology/site")
		if err != nil {
			t.Fatalf("NewConfig returned error: %v", err)
		}

		if err := s.Get(got); err != nil {
			t.Fatalf("%s: Get config returned error: %v", stage, err)
		}

		listed, err := s.List("Topology")
		if err != nil || len(listed) != 1 {
			t.Fatalf("%s: List configs = %d, %v; want the config", stage, len(listed), err)
		}

		for _, read := range []Config{*got, listed[0]} {
			if !reflect.DeepEqual(read.Metadata.Annotations, Annotations(want)) {
				t.Fatalf("%s: annotations read = %v, want %v", stage, read.Metadata.Annotations, want)
			}
		}
	}

	check("create")

	config.Spec = map[string]any{"nodes": []any{}, "changed": true}
	if err := s.Update(config); err != nil {
		t.Fatalf("Update config returned error: %v", err)
	}

	check("update")
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
		t.Fatalf("Get of a missing config error = %v, want ErrNotExist", err)
	}

	if err := s.Update(config); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Update of a missing config error = %v, want ErrNotExist", err)
	}

	if err := s.Create(config); err != nil {
		t.Fatalf("Create config returned error: %v", err)
	}

	if err := s.Create(config); !errors.Is(err, ErrExist) {
		t.Fatalf("Create of an existing config error = %v, want ErrExist", err)
	}
}

func TestRecordConcurrentCreateAndUpdate(t *testing.T) {
	s, _ := newTestBoltDB(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v0"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	const workers = 8

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		creates   int
		updates   int
		conflicts int
	)

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, createErr := s.CreateRecord("drafts", "draft-2", []byte("v1"))
			_, updateErr := s.UpdateRecord("drafts", "draft-1", []byte("v1"), created.Revision)

			mu.Lock()
			defer mu.Unlock()

			if createErr == nil {
				creates++
			} else if !errors.Is(createErr, ErrRecordExist) {
				t.Errorf("worker %d create error = %v, want ErrRecordExist", i, createErr)
			}

			switch {
			case updateErr == nil:
				updates++
			case errors.Is(updateErr, ErrRecordConflict):
				conflicts++
			default:
				t.Errorf("worker %d update error = %v, want conflict or success", i, updateErr)
			}
		}()
	}

	wg.Wait()

	if creates != 1 {
		t.Fatalf("successful concurrent creates = %d, want 1", creates)
	}

	if updates != 1 || conflicts != workers-1 {
		t.Fatalf("concurrent updates = %d, conflicts = %d, want 1 and %d", updates, conflicts, workers-1)
	}
}

// requireRecord fails the test unless the record exists with the given value
// and revision, and returns it.
func requireRecord(t *testing.T, s *BoltDB, namespace, key, value string, revision int64) Record {
	t.Helper()

	got, err := s.GetRecord(namespace, key)
	if err != nil {
		t.Fatalf("GetRecord(%s/%s) returned error: %v", namespace, key, err)
	}

	if string(got.Value) != value || got.Revision != revision || got.Namespace != namespace || got.Key != key {
		t.Fatalf("GetRecord(%s/%s) = %q at revision %d, want %q at revision %d",
			namespace, key, got.Value, got.Revision, value, revision)
	}

	return got
}

// requireConflict fails the test unless err is a *RecordConflictError that
// reports the expected and actual revisions.
func requireConflict(t *testing.T, op string, err error, expected, actual int64) {
	t.Helper()

	var conflict *RecordConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("%s error = %v, want *RecordConflictError", op, err)
	}

	if conflict.Expected != expected || conflict.Actual != actual {
		t.Fatalf("%s conflict = %+v, want expected %d actual %d", op, conflict, expected, actual)
	}
}

// requireKeys fails the test unless listing the prefix returns exactly want.
func requireKeys(t *testing.T, s *BoltDB, namespace, prefix string, want []string) {
	t.Helper()

	got, err := s.ListRecordKeys(namespace, prefix)
	if err != nil {
		t.Fatalf("ListRecordKeys(%s, %q) returned error: %v", namespace, prefix, err)
	}

	if reflect.DeepEqual(got, want) {
		return
	}

	// Page sized listings are too long to print whole.
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}

	t.Fatalf("ListRecordKeys(%s, %q) returned %d keys, want %d; first difference at index %d: got %q, want %q",
		namespace, prefix, len(got), len(want), i, got[i:min(i+3, len(got))], want[i:min(i+3, len(want))])
}

// createRecords creates each key holding its own name as its value.
func createRecords(t *testing.T, s *BoltDB, namespace string, keys ...string) {
	t.Helper()

	for _, key := range keys {
		if _, err := s.CreateRecord(namespace, key, []byte(key)); err != nil {
			t.Fatalf("CreateRecord(%s/%s) returned error: %v", namespace, key, err)
		}
	}
}

func recordKeys(records Records) []string {
	keys := make([]string, 0, len(records))
	for _, record := range records {
		keys = append(keys, record.Key)
	}

	return keys
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
