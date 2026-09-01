package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestBoltRecordCreateGetUpdateDelete(t *testing.T) {
	s, _ := newTestBoltDB(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	if created.Revision <= 0 {
		t.Fatalf("created revision = %d, want > 0", created.Revision)
	}

	if string(created.Value) != "v1" {
		t.Fatalf("created value = %q, want %q", created.Value, "v1")
	}

	got, err := s.GetRecord("drafts", "draft-1")
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	if got.Revision != created.Revision || string(got.Value) != "v1" {
		t.Fatalf("GetRecord = %+v, want revision %d and value v1", got, created.Revision)
	}

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

	if err := s.DeleteRecord("drafts", "draft-1", updated.Revision); err != nil {
		t.Fatalf("DeleteRecord returned error: %v", err)
	}

	if _, err := s.GetRecord("drafts", "draft-1"); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("GetRecord after delete error = %v, want ErrRecordNotExist", err)
	}
}

func TestBoltRecordCreateIfAbsent(t *testing.T) {
	s, _ := newTestBoltDB(t)

	if _, err := s.CreateRecord("drafts", "draft-1", []byte("v1")); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	_, err := s.CreateRecord("drafts", "draft-1", []byte("v2"))
	if !errors.Is(err, ErrRecordExist) {
		t.Fatalf("second CreateRecord error = %v, want ErrRecordExist", err)
	}

	got, err := s.GetRecord("drafts", "draft-1")
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	if string(got.Value) != "v1" {
		t.Fatalf("value = %q, want v1 (failed create must not overwrite)", got.Value)
	}
}

func TestBoltRecordCompareAndSwapConflicts(t *testing.T) {
	s, _ := newTestBoltDB(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	_, err = s.UpdateRecord("drafts", "draft-1", []byte("stale"), created.Revision+1)

	var conflict *RecordConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("UpdateRecord with stale revision error = %v, want *RecordConflictError", err)
	}

	if conflict.Expected != created.Revision+1 || conflict.Actual != created.Revision {
		t.Fatalf("conflict = %+v, want expected %d actual %d", conflict, created.Revision+1, created.Revision)
	}

	got, err := s.GetRecord("drafts", "draft-1")
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	if string(got.Value) != "v1" {
		t.Fatalf("value = %q, want v1 (conflicting update must not apply)", got.Value)
	}

	if err := s.DeleteRecord("drafts", "draft-1", created.Revision+1); !errors.Is(err, ErrRecordConflict) {
		t.Fatalf("DeleteRecord with stale revision error = %v, want ErrRecordConflict", err)
	}

	if _, err := s.UpdateRecord("drafts", "draft-1", []byte("v2"), AnyRevision); err != nil {
		t.Fatalf("UpdateRecord with AnyRevision returned error: %v", err)
	}

	if err := s.DeleteRecord("drafts", "draft-1", AnyRevision); err != nil {
		t.Fatalf("DeleteRecord with AnyRevision returned error: %v", err)
	}
}

func TestBoltRecordMissingRecordErrors(t *testing.T) {
	s, _ := newTestBoltDB(t)

	if _, err := s.GetRecord("drafts", "missing"); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("GetRecord error = %v, want ErrRecordNotExist", err)
	}

	if _, err := s.UpdateRecord("drafts", "missing", []byte("v"), AnyRevision); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("UpdateRecord error = %v, want ErrRecordNotExist", err)
	}

	if err := s.DeleteRecord("drafts", "missing", AnyRevision); !errors.Is(err, ErrRecordNotExist) {
		t.Fatalf("DeleteRecord error = %v, want ErrRecordNotExist", err)
	}

	records, err := s.ListRecords("unknown-namespace", "")
	if err != nil {
		t.Fatalf("ListRecords on unknown namespace returned error: %v", err)
	}

	if len(records) != 0 {
		t.Fatalf("ListRecords on unknown namespace = %d records, want 0", len(records))
	}
}

func TestBoltRecordListPrefixIsolation(t *testing.T) {
	s, _ := newTestBoltDB(t)

	keys := []string{"draft-1/chunks/0001", "draft-1/chunks/0002", "draft-1/meta", "draft-2/meta"}

	for _, key := range keys {
		if _, err := s.CreateRecord("drafts", key, []byte(key)); err != nil {
			t.Fatalf("CreateRecord(%q) returned error: %v", key, err)
		}
	}

	if _, err := s.CreateRecord("chunks", "draft-1/chunks/0001", []byte("other-namespace")); err != nil {
		t.Fatalf("CreateRecord in second namespace returned error: %v", err)
	}

	all, err := s.ListRecords("drafts", "")
	if err != nil {
		t.Fatalf("ListRecords returned error: %v", err)
	}

	if len(all) != len(keys) {
		t.Fatalf("ListRecords(all) = %d records, want %d", len(all), len(keys))
	}

	for i, record := range all {
		if record.Key != keys[i] {
			t.Fatalf("ListRecords(all)[%d].Key = %q, want %q (results must be key ordered)", i, record.Key, keys[i])
		}

		if record.Namespace != "drafts" {
			t.Fatalf("ListRecords(all)[%d].Namespace = %q, want drafts", i, record.Namespace)
		}
	}

	chunks, err := s.ListRecords("drafts", "draft-1/chunks/")
	if err != nil {
		t.Fatalf("ListRecords with prefix returned error: %v", err)
	}

	if len(chunks) != 2 {
		t.Fatalf("ListRecords(prefix) = %d records, want 2", len(chunks))
	}

	other, err := s.ListRecords("chunks", "")
	if err != nil {
		t.Fatalf("ListRecords in second namespace returned error: %v", err)
	}

	if len(other) != 1 || string(other[0].Value) != "other-namespace" {
		t.Fatalf("namespaces are not isolated: %+v", other)
	}
}

func TestBoltRecordDeletePrefix(t *testing.T) {
	s, _ := newTestBoltDB(t)

	for _, key := range []string{"draft-1/chunks/0001", "draft-1/chunks/0002", "draft-1/meta", "draft-2/meta"} {
		if _, err := s.CreateRecord("drafts", key, []byte(key)); err != nil {
			t.Fatalf("CreateRecord(%q) returned error: %v", key, err)
		}
	}

	if _, err := s.CreateRecord("chunks", "draft-1/chunks/0001", []byte("other-namespace")); err != nil {
		t.Fatalf("CreateRecord in second namespace returned error: %v", err)
	}

	deleted, err := s.DeleteRecordPrefix("drafts", "draft-1/")
	if err != nil {
		t.Fatalf("DeleteRecordPrefix returned error: %v", err)
	}

	if deleted != 3 {
		t.Fatalf("DeleteRecordPrefix deleted %d records, want 3", deleted)
	}

	remaining, err := s.ListRecords("drafts", "")
	if err != nil {
		t.Fatalf("ListRecords returned error: %v", err)
	}

	if len(remaining) != 1 || remaining[0].Key != "draft-2/meta" {
		t.Fatalf("remaining records = %+v, want only draft-2/meta", remaining)
	}

	other, err := s.ListRecords("chunks", "")
	if err != nil {
		t.Fatalf("ListRecords in second namespace returned error: %v", err)
	}

	if len(other) != 1 {
		t.Fatalf("prefix deletion escaped its namespace: %+v", other)
	}

	if _, err := s.DeleteRecordPrefix("drafts", ""); !errors.Is(err, ErrInvalidRecordKey) {
		t.Fatalf("DeleteRecordPrefix with empty prefix error = %v, want ErrInvalidRecordKey", err)
	}
}

func TestBoltRecordPersistsAcrossReopen(t *testing.T) {
	s, path := newTestBoltDB(t)

	created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	reopened := NewBoltDB()
	if err := reopened.Init(Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("reopening BoltDB returned error: %v", err)
	}

	got, err := reopened.GetRecord("drafts", "draft-1")
	if err != nil {
		t.Fatalf("GetRecord after reopen returned error: %v", err)
	}

	if string(got.Value) != "v1" || got.Revision != created.Revision {
		t.Fatalf("record after reopen = %+v, want value v1 and revision %d", got, created.Revision)
	}

	next, err := reopened.CreateRecord("drafts", "draft-2", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateRecord after reopen returned error: %v", err)
	}

	if next.Revision <= created.Revision {
		t.Fatalf("revision after reopen = %d, want > %d (revisions must be monotonic)", next.Revision, created.Revision)
	}
}

func TestBoltRecordValuesAreCopied(t *testing.T) {
	s, _ := newTestBoltDB(t)

	value := []byte("v1")

	created, err := s.CreateRecord("drafts", "draft-1", value)
	if err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	value[0] = 'X'
	created.Value[1] = 'X'

	got, err := s.GetRecord("drafts", "draft-1")
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	if string(got.Value) != "v1" {
		t.Fatalf("stored value = %q, want v1 (values must be copied)", got.Value)
	}
}

func TestBoltRecordRejectsInvalidNamespacesAndKeys(t *testing.T) {
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
}

func TestBoltRecordsDoNotCollideWithConfigs(t *testing.T) {
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

func newTestBoltDB(t *testing.T) (Store, string) { //nolint:ireturn // mirrors the store factory
	t.Helper()

	path := filepath.Join(t.TempDir(), "phenix.bdb")

	s := NewBoltDB()
	if err := s.Init(Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("initializing BoltDB store returned error: %v", err)
	}

	return s, path
}

func TestBoltRecordConcurrentCreateAndUpdate(t *testing.T) {
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

		go func(i int) {
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
		}(i)
	}

	wg.Wait()

	if creates != 1 {
		t.Fatalf("successful concurrent creates = %d, want 1", creates)
	}

	if updates != 1 || conflicts != workers-1 {
		t.Fatalf("concurrent updates = %d, conflicts = %d, want 1 and %d", updates, conflicts, workers-1)
	}
}
