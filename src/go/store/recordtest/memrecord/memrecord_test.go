package memrecord_test

import (
	"errors"
	"testing"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
)

var errRefused = errors.New("refused")

func TestStoreComparesRevisions(t *testing.T) {
	s := memrecord.New()

	created, err := s.CreateRecord("drafts", "a", []byte("1"))
	if err != nil || created.Revision != 1 || !created.Updated.Equal(memrecord.Time(1)) {
		t.Fatalf("CreateRecord = %+v, %v; want revision 1 stamped with Time(1)", created, err)
	}

	if _, err := s.CreateRecord("drafts", "a", []byte("2")); !errors.Is(err, store.ErrRecordExist) {
		t.Fatalf("second CreateRecord error = %v, want ErrRecordExist", err)
	}

	if _, err := s.UpdateRecord("drafts", "a", []byte("2"), 7); !errors.Is(err, store.ErrRecordConflict) {
		t.Fatalf("stale UpdateRecord error = %v, want ErrRecordConflict", err)
	}

	if updated, err := s.UpdateRecord("drafts", "a", []byte("2"), store.AnyRevision); err != nil || updated.Revision != 2 {
		t.Fatalf("UpdateRecord = %+v, %v; want revision 2", updated, err)
	}

	if err := s.DeleteRecord("drafts", "a", 1); !errors.Is(err, store.ErrRecordConflict) {
		t.Fatalf("stale DeleteRecord error = %v, want ErrRecordConflict", err)
	}
}

// FailPrefixDelete refuses prefix deletions alone; FailDelete both kinds.
func TestStoreDeleteHooks(t *testing.T) {
	s := newChunks(t, "scope/1", "other")
	s.FailPrefixDelete = func(string, string) error { return errRefused }

	if _, err := s.DeleteRecordPrefix("chunks", "scope/"); !errors.Is(err, errRefused) {
		t.Fatalf("DeleteRecordPrefix error = %v, want the refusal", err)
	}

	if err := s.DeleteRecord("chunks", "other", store.AnyRevision); err != nil {
		t.Fatalf("DeleteRecord error = %v, want the record deleted", err)
	}

	s.FailPrefixDelete = nil
	s.FailDelete = func(string, string) error { return errRefused }

	if _, err := s.DeleteRecordPrefix("chunks", "scope/"); !errors.Is(err, errRefused) {
		t.Fatalf("DeleteRecordPrefix error = %v, want the refusal", err)
	}

	if err := s.DeleteRecord("chunks", "scope/1", store.AnyRevision); !errors.Is(err, errRefused) {
		t.Fatalf("DeleteRecord error = %v, want the refusal", err)
	}
}

// AfterWrite reports an applied write as failed, and may land another
// writer's change first.
func TestStoreAfterWrite(t *testing.T) {
	s := newChunks(t, "scope/1")
	s.AfterWrite = func(namespace, key string) error {
		if err := s.RewriteLocked(namespace, key, func(value []byte) ([]byte, error) {
			return append(value, '!'), nil
		}); err != nil {
			return err
		}

		return errRefused
	}

	if _, err := s.UpdateRecord("chunks", "scope/1", []byte("new"), store.AnyRevision); !errors.Is(err, errRefused) {
		t.Fatalf("UpdateRecord error = %v, want the refusal", err)
	}

	s.AfterWrite = nil

	if record, err := s.GetRecord("chunks", "scope/1"); err != nil || string(record.Value) != "new!" || record.Revision != 3 {
		t.Fatalf("GetRecord = %+v, %v; want the other writer's value at revision 3", record, err)
	}
}

func newChunks(t *testing.T, keys ...string) *memrecord.Store {
	t.Helper()

	s := memrecord.New()

	for _, key := range keys {
		if _, err := s.CreateRecord("chunks", key, []byte(key)); err != nil {
			t.Fatalf("CreateRecord(%s) error = %v", key, err)
		}
	}

	return s
}
