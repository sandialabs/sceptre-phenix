package store_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/store/storetest"
)

const txnNamespace = "txn"

var errTxnStop = errors.New("stop the transaction")

// txnStores returns, by name, an empty store of each kind that implements
// transactions: BoltDB, and the in-memory fake that the Builder tests use.
func txnStores(t *testing.T) map[string]store.RecordStore {
	t.Helper()

	return map[string]store.RecordStore{"bolt": storetest.NewBolt(t), "memrecord": memrecord.New()}
}

// txnValues returns the value of every record in the test namespace, by key.
func txnValues(t *testing.T, s store.RecordStore) map[string]string {
	t.Helper()

	records, err := s.ListRecords(txnNamespace, "")
	if err != nil {
		t.Fatalf("ListRecords(%s) returned error: %v", txnNamespace, err)
	}

	values := make(map[string]string, len(records))
	for _, record := range records {
		values[record.Key] = string(record.Value)
	}

	return values
}

// txnSeed creates the records "keep", "change" and "drop", and returns the
// revision of "change".
func txnSeed(t *testing.T, s store.RecordStore) int64 {
	t.Helper()

	var changed int64

	for _, key := range []string{"keep", "change", "drop"} {
		record, err := s.CreateRecord(txnNamespace, key, []byte(key))
		if err != nil {
			t.Fatalf("CreateRecord(%s) returned error: %v", key, err)
		}

		if key == "change" {
			changed = record.Revision
		}
	}

	return changed
}

func TestRecordTxnCommitsEveryWrite(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			changed := txnSeed(t, s)

			var created, updated store.Record

			err := s.RecordTxn(func(tx store.RecordTx) error {
				var err error

				if created, err = tx.CreateRecord(txnNamespace, "new", []byte("new")); err != nil {
					return err
				}

				if updated, err = tx.UpdateRecord(txnNamespace, "change", []byte("changed"), changed); err != nil {
					return err
				}

				if err := tx.DeleteRecord(txnNamespace, "drop", store.AnyRevision); err != nil {
					return err
				}

				// A read sees what the transaction wrote before it.
				read, err := tx.GetRecord(txnNamespace, "new")
				if err != nil {
					return err
				}

				if string(read.Value) != "new" || read.Revision != created.Revision {
					t.Errorf("GetRecord(new) in the transaction = %q at %d, want new at %d", read.Value, read.Revision, created.Revision)
				}

				if _, err := tx.GetRecord(txnNamespace, "drop"); !errors.Is(err, store.ErrRecordNotExist) {
					t.Errorf("GetRecord(drop) in the transaction after its delete error = %v, want ErrRecordNotExist", err)
				}

				return nil
			})
			if err != nil {
				t.Fatalf("RecordTxn returned error: %v", err)
			}

			want := map[string]string{"keep": "keep", "change": "changed", "new": "new"}
			if diff := cmp.Diff(want, txnValues(t, s)); diff != "" {
				t.Errorf("records after RecordTxn mismatch (-want +got):\n%s", diff)
			}

			if updated.Revision <= changed || created.Revision <= changed {
				t.Errorf("RecordTxn revisions = %d and %d, want both after %d", created.Revision, updated.Revision, changed)
			}

			got, err := s.GetRecord(txnNamespace, "change")
			if err != nil {
				t.Fatalf("GetRecord(change) returned error: %v", err)
			}

			// The revision a write in a transaction returns is the one a later
			// read reports, so the caller can pass it back.
			if got.Revision != updated.Revision {
				t.Errorf("GetRecord(change) revision = %d, want the %d the transaction returned", got.Revision, updated.Revision)
			}
		})
	}
}

func TestRecordTxnAppliesNothingOnError(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			changed := txnSeed(t, s)
			before := txnValues(t, s)

			err := s.RecordTxn(func(tx store.RecordTx) error {
				if _, err := tx.CreateRecord(txnNamespace, "new", []byte("new")); err != nil {
					return err
				}

				if _, err := tx.UpdateRecord(txnNamespace, "change", []byte("changed"), changed); err != nil {
					return err
				}

				if _, err := tx.DeleteRecordPrefix(txnNamespace, "dr"); err != nil {
					return err
				}

				return errTxnStop
			})
			if !errors.Is(err, errTxnStop) {
				t.Errorf("RecordTxn error = %v, want the error of the function", err)
			}

			if diff := cmp.Diff(before, txnValues(t, s)); diff != "" {
				t.Errorf("records after a failed RecordTxn mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRecordTxnRevisionCheckFailsTheTransaction(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			changed := txnSeed(t, s)
			before := txnValues(t, s)

			err := s.RecordTxn(func(tx store.RecordTx) error {
				if _, err := tx.CreateRecord(txnNamespace, "new", []byte("new")); err != nil {
					return err
				}

				_, err := tx.UpdateRecord(txnNamespace, "change", []byte("changed"), changed+100)

				return err
			})

			var conflict *store.RecordConflictError
			if !errors.As(err, &conflict) || conflict.Key != "change" {
				t.Errorf("RecordTxn with a stale revision error = %v, want a RecordConflictError for change", err)
			}

			if diff := cmp.Diff(before, txnValues(t, s)); diff != "" {
				t.Errorf("records after a refused RecordTxn mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRecordTxnDeletePrefixCountsItsOwnWrites(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			txnSeed(t, s)

			var deleted int

			err := s.RecordTxn(func(tx store.RecordTx) error {
				if _, err := tx.CreateRecord(txnNamespace, "drop/new", []byte("new")); err != nil {
					return err
				}

				var err error

				deleted, err = tx.DeleteRecordPrefix(txnNamespace, "dr")

				return err
			})
			if err != nil {
				t.Fatalf("RecordTxn returned error: %v", err)
			}

			if deleted != 2 {
				t.Errorf("DeleteRecordPrefix(dr) in a transaction = %d, want 2", deleted)
			}

			want := map[string]string{"keep": "keep", "change": "change"}
			if diff := cmp.Diff(want, txnValues(t, s)); diff != "" {
				t.Errorf("records after RecordTxn mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMemrecordTxnFailsAfterAnInterleavedWrite checks that the fake reports
// a writer that a hook interleaves as a conflict, and commits nothing, as a
// store that runs one transaction at a time does when that writer is first.
func TestMemrecordTxnFailsAfterAnInterleavedWrite(t *testing.T) {
	s := memrecord.New()
	changed := txnSeed(t, s)

	s.BeforeCreate = func(string, string) error {
		s.BeforeCreate = nil

		_, err := s.UpdateRecord(txnNamespace, "change", []byte("other"), store.AnyRevision)

		return err
	}

	err := s.RecordTxn(func(tx store.RecordTx) error {
		if _, err := tx.GetRecord(txnNamespace, "change"); err != nil {
			return err
		}

		_, err := tx.CreateRecord(txnNamespace, "new", []byte("new"))

		return err
	})

	var conflict *store.RecordConflictError
	if !errors.As(err, &conflict) || conflict.Expected != changed {
		t.Errorf("RecordTxn after an interleaved write error = %v, want a RecordConflictError from revision %d", err, changed)
	}

	want := map[string]string{"keep": "keep", "change": "other", "drop": "drop"}
	if diff := cmp.Diff(want, txnValues(t, s)); diff != "" {
		t.Errorf("records after RecordTxn mismatch (-want +got):\n%s", diff)
	}

	if got := s.Commits(); got != 4 {
		t.Errorf("Commits() = %d, want 4: three creates and the interleaved update", got)
	}
}
