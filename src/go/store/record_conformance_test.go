package store_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
)

// The record tests run over each store of txnStores, so the in-memory fake
// keeps the behavior of BoltDB.

func TestRecordCreateGetUpdateDelete(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
			if err != nil {
				t.Fatalf("CreateRecord returned error: %v", err)
			}

			if created.Revision <= 0 {
				t.Errorf("CreateRecord(drafts, draft-1) revision = %d, want > 0", created.Revision)
			}

			want := store.Record{Namespace: "drafts", Key: "draft-1", Value: []byte("v1")}
			if diff := cmp.Diff(want, created, cmpopts.IgnoreFields(store.Record{}, "Revision", "Created", "Updated")); diff != "" {
				t.Errorf("CreateRecord(drafts, draft-1) mismatch (-want +got):\n%s", diff)
			}

			// The revision a write returns is the one every later read reports, or no
			// caller could ever pass it back as an expected revision.
			if _, err := checkRecord(s, "drafts", "draft-1", "v1", created.Revision); err != nil {
				t.Error(err)
			}

			updated, err := s.UpdateRecord("drafts", "draft-1", []byte("v2"), created.Revision)
			if err != nil {
				t.Fatalf("UpdateRecord returned error: %v", err)
			}

			if updated.Revision <= created.Revision {
				t.Errorf("UpdateRecord(drafts, draft-1) revision = %d, want > %d", updated.Revision, created.Revision)
			}

			if !updated.Created.Equal(created.Created) {
				t.Errorf("UpdateRecord(drafts, draft-1) created = %v, want %v", updated.Created, created.Created)
			}

			if updated.Updated.Before(created.Updated) {
				t.Errorf("UpdateRecord(drafts, draft-1) updated = %v, want >= %v", updated.Updated, created.Updated)
			}

			stored := store.Record{
				Namespace: "drafts", Key: "draft-1", Value: []byte("v2"),
				Revision: updated.Revision, Created: created.Created, Updated: updated.Updated,
			}

			got, err := s.GetRecord("drafts", "draft-1")
			if err != nil {
				t.Errorf("GetRecord(drafts, draft-1) error = %v", err)
			} else if diff := cmp.Diff(stored, got); diff != "" {
				t.Errorf("GetRecord(drafts, draft-1) mismatch (-want +got):\n%s", diff)
			}

			listed, err := s.ListRecords("drafts", "")
			if err != nil {
				t.Errorf("ListRecords(drafts, \"\") error = %v", err)
			} else if diff := cmp.Diff(store.Records{stored}, listed); diff != "" {
				t.Errorf("ListRecords(drafts, \"\") mismatch (-want +got):\n%s", diff)
			}

			if err := s.DeleteRecord("drafts", "draft-1", updated.Revision); err != nil {
				t.Fatalf("DeleteRecord returned error: %v", err)
			}

			if _, err := s.GetRecord("drafts", "draft-1"); !errors.Is(err, store.ErrRecordNotExist) {
				t.Errorf("GetRecord(drafts, draft-1) after delete error = %v, want store.ErrRecordNotExist", err)
			}
		})
	}
}

func TestRecordCreateIfAbsent(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			created, err := s.CreateRecord("drafts", "draft-1", []byte("v1"))
			if err != nil {
				t.Fatalf("CreateRecord returned error: %v", err)
			}

			_, err = s.CreateRecord("drafts", "draft-1", []byte("v2"))

			var exists *store.RecordExistError
			if !errors.As(err, &exists) {
				t.Errorf("second CreateRecord(drafts, draft-1) error = %v, want *store.RecordExistError", err)
			} else if diff := cmp.Diff(&store.RecordExistError{Namespace: "drafts", Key: "draft-1"}, exists); diff != "" {
				t.Errorf("second CreateRecord(drafts, draft-1) error mismatch (-want +got):\n%s", diff)
			}

			// A failed create must neither overwrite the value nor move the revision.
			if _, err := checkRecord(s, "drafts", "draft-1", "v1", created.Revision); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRecordStaleRevisionsConflict(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
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
			if err := checkConflict(err, stale, updated.Revision); err != nil {
				t.Errorf("UpdateRecord(drafts, draft-1) at a stale revision: %v", err)
			}

			if err := checkConflict(s.DeleteRecord("drafts", "draft-1", stale), stale, updated.Revision); err != nil {
				t.Errorf("DeleteRecord(drafts, draft-1) at a stale revision: %v", err)
			}

			// Neither conflicting write may apply.
			if _, err := checkRecord(s, "drafts", "draft-1", "v2", updated.Revision); err != nil {
				t.Error(err)
			}

			forced, err := s.UpdateRecord("drafts", "draft-1", []byte("v3"), store.AnyRevision)
			if err != nil {
				t.Fatalf("UpdateRecord with store.AnyRevision returned error: %v", err)
			}

			if forced.Revision <= updated.Revision {
				t.Errorf("UpdateRecord(drafts, draft-1, store.AnyRevision) revision = %d, want > %d", forced.Revision, updated.Revision)
			}

			if err := s.DeleteRecord("drafts", "draft-1", store.AnyRevision); err != nil {
				t.Errorf("DeleteRecord(drafts, draft-1, store.AnyRevision) error = %v", err)
			}
		})
	}
}

func TestRecordRecreateGetsNewRevision(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
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
				t.Errorf("CreateRecord(published, doc-1) after delete revision = %d, want > %d", second.Revision, first.Revision)
			}

			// A writer holding the deleted record's revision must not affect the record
			// that replaced it, which is what makes deleting an observed revision safe.
			err = checkConflict(s.DeleteRecord("published", "doc-1", first.Revision), first.Revision, second.Revision)
			if err != nil {
				t.Errorf("DeleteRecord(published, doc-1) at the deleted revision: %v", err)
			}

			_, err = s.UpdateRecord("published", "doc-1", []byte("stale"), first.Revision)
			if err := checkConflict(err, first.Revision, second.Revision); err != nil {
				t.Errorf("UpdateRecord(published, doc-1) at the deleted revision: %v", err)
			}

			if _, err := checkRecord(s, "published", "doc-1", "v2", second.Revision); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRecordMissingRecordErrors(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			_, err := s.GetRecord("drafts", "missing")

			var missing *store.RecordNotExistError
			if !errors.As(err, &missing) {
				t.Errorf("GetRecord(drafts, missing) error = %v, want *store.RecordNotExistError", err)
			} else if diff := cmp.Diff(&store.RecordNotExistError{Namespace: "drafts", Key: "missing"}, missing); diff != "" {
				t.Errorf("GetRecord(drafts, missing) error mismatch (-want +got):\n%s", diff)
			}

			if _, err := s.UpdateRecord("drafts", "missing", []byte("v"), store.AnyRevision); !errors.Is(err, store.ErrRecordNotExist) {
				t.Errorf("UpdateRecord(drafts, missing, store.AnyRevision) error = %v, want store.ErrRecordNotExist", err)
			}

			if _, err := s.UpdateRecord("drafts", "missing", []byte("v"), 7); !errors.Is(err, store.ErrRecordNotExist) {
				t.Errorf("UpdateRecord(drafts, missing, 7) error = %v, want store.ErrRecordNotExist", err)
			}

			if err := s.DeleteRecord("drafts", "missing", store.AnyRevision); !errors.Is(err, store.ErrRecordNotExist) {
				t.Errorf("DeleteRecord(drafts, missing, store.AnyRevision) error = %v, want store.ErrRecordNotExist", err)
			}

			if err := s.DeleteRecord("drafts", "missing", 7); !errors.Is(err, store.ErrRecordNotExist) {
				t.Errorf("DeleteRecord(drafts, missing, 7) error = %v, want store.ErrRecordNotExist", err)
			}

			if records, err := s.ListRecords("unknown-namespace", ""); err != nil {
				t.Errorf("ListRecords(unknown-namespace, \"\") error = %v", err)
			} else if len(records) != 0 {
				t.Errorf("ListRecords(unknown-namespace, \"\") = %d records, want none", len(records))
			}

			if keys, err := s.ListRecordKeys("unknown-namespace", ""); err != nil {
				t.Errorf("ListRecordKeys(unknown-namespace, \"\") error = %v", err)
			} else if len(keys) != 0 {
				t.Errorf("ListRecordKeys(unknown-namespace, \"\") = %q, want none", keys)
			}

			if deleted, err := s.DeleteRecordPrefix("unknown-namespace", "draft-1/"); err != nil {
				t.Errorf("DeleteRecordPrefix(unknown-namespace, draft-1/) error = %v", err)
			} else if deleted != 0 {
				t.Errorf("DeleteRecordPrefix(unknown-namespace, draft-1/) = %d, want 0", deleted)
			}
		})
	}
}

func TestRecordListPrefixIsolation(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
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

			// In key order, each holding its own key.
			want := make(store.Records, 0, len(keys))
			for _, key := range keys {
				want = append(want, store.Record{Namespace: "drafts", Key: key, Value: []byte(key)})
			}

			if all, err := s.ListRecords("drafts", ""); err != nil {
				t.Errorf("ListRecords(drafts, \"\") error = %v", err)
			} else if diff := cmp.Diff(want, all, cmpopts.IgnoreFields(store.Record{}, "Revision", "Created", "Updated")); diff != "" {
				t.Errorf("ListRecords(drafts, \"\") mismatch (-want +got):\n%s", diff)
			}

			if chunks, err := s.ListRecords("drafts", "draft-1/chunks/"); err != nil {
				t.Errorf("ListRecords(drafts, draft-1/chunks/) error = %v", err)
			} else if diff := cmp.Diff(keys[:2], recordKeys(chunks)); diff != "" {
				t.Errorf("ListRecords(drafts, draft-1/chunks/) keys mismatch (-want +got):\n%s", diff)
			}

			// A prefix is a byte prefix, not a path segment: "draft-1" also matches
			// "draft-10".
			for _, check := range []struct {
				namespace, prefix string
				want              []string
			}{
				{namespace: "drafts", prefix: "draft-1", want: keys[:4]},
				{namespace: "drafts", prefix: "draft-1/", want: keys[:3]},
				{namespace: "drafts", prefix: "draft-3", want: []string{}},
				{namespace: "drafts2", prefix: "", want: []string{"draft-1/meta"}},
			} {
				if err := checkKeys(s, check.namespace, check.prefix, check.want); err != nil {
					t.Error(err)
				}
			}

			wantOther := store.Records{{Namespace: "chunks", Key: "draft-1/chunks/0001", Value: []byte("draft-1/chunks/0001")}}
			if other, err := s.ListRecords("chunks", ""); err != nil {
				t.Errorf("ListRecords(chunks, \"\") error = %v", err)
			} else if diff := cmp.Diff(wantOther, other, cmpopts.IgnoreFields(store.Record{}, "Revision", "Created", "Updated")); diff != "" {
				t.Errorf("ListRecords(chunks, \"\") mismatch, namespaces are not isolated (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRecordListOrdersDecodedKeys(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			// Characters a store may escape sort differently once escaped: "é" sorts
			// after "+" as a key, but its escape "%C3%A9" sorts before it.
			keys := []string{"a b", "a!b", "a%b", "a+b", "a/b", "a~b", "aéb"}

			createRecords(t, s, "drafts", keys...)

			want := slices.Clone(keys)
			slices.Sort(want)

			if err := checkKeys(s, "drafts", "a", want); err != nil {
				t.Error(err)
			}

			records, err := s.ListRecords("drafts", "a")
			if err != nil {
				t.Fatalf("ListRecords(drafts, a) error = %v", err)
			}

			if diff := cmp.Diff(want, recordKeys(records)); diff != "" {
				t.Errorf("ListRecords(drafts, a) keys mismatch (-want +got):\n%s", diff)
			}

			for _, record := range records {
				if string(record.Value) != record.Key {
					t.Errorf("ListRecords(drafts, a) record %q holds %q, want its own key", record.Key, record.Value)
				}
			}
		})
	}
}

func TestRecordListKeysPrefixEdges(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
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

			if err := checkKeys(s, "chunks", "exact/", exact); err != nil {
				t.Error(err)
			}

			if err := checkKeys(s, "chunks", "over/", over); err != nil {
				t.Error(err)
			}

			all := slices.Concat([]string{"exact", "exact0", "over0"}, exact, over)
			slices.Sort(all)

			if err := checkKeys(s, "chunks", "", all); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRecordDeletePrefix(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			createRecords(t, s, "drafts", "draft-1/chunks/0001", "draft-1/chunks/0002", "draft-1/meta", "draft-10/meta", "draft-2/meta")
			createRecords(t, s, "chunks", "draft-1/chunks/0001")
			createRecords(t, s, "drafts2", "draft-1/meta")

			deleted, err := s.DeleteRecordPrefix("drafts", "draft-1/")
			if err != nil {
				t.Fatalf("DeleteRecordPrefix returned error: %v", err)
			}

			if deleted != 3 {
				t.Errorf("DeleteRecordPrefix(drafts, draft-1/) = %d, want 3", deleted)
			}

			// Deletion never escapes its namespace, even into one sharing its name.
			for namespace, want := range map[string][]string{
				"drafts":  {"draft-10/meta", "draft-2/meta"},
				"chunks":  {"draft-1/chunks/0001"},
				"drafts2": {"draft-1/meta"},
			} {
				if err := checkKeys(s, namespace, "", want); err != nil {
					t.Error(err)
				}
			}

			if _, err := s.DeleteRecordPrefix("drafts", ""); !errors.Is(err, store.ErrInvalidRecordKey) {
				t.Errorf("DeleteRecordPrefix(drafts, \"\") error = %v, want store.ErrInvalidRecordKey", err)
			}

			if deleted, err := s.DeleteRecordPrefix("drafts", "draft-1/"); err != nil {
				t.Errorf("repeated DeleteRecordPrefix(drafts, draft-1/) error = %v", err)
			} else if deleted != 0 {
				t.Errorf("repeated DeleteRecordPrefix(drafts, draft-1/) = %d, want 0", deleted)
			}
		})
	}
}

func TestRecordValuesAreCopied(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			value := []byte("v1")

			created, err := s.CreateRecord("drafts", "draft-1", value)
			if err != nil {
				t.Fatalf("CreateRecord returned error: %v", err)
			}

			value[0] = 'X'
			created.Value[1] = 'X'

			got, err := checkRecord(s, "drafts", "draft-1", "v1", created.Revision)
			if err != nil {
				t.Fatal(err)
			}

			got.Value[0] = 'X'

			if _, err := checkRecord(s, "drafts", "draft-1", "v1", created.Revision); err != nil {
				t.Errorf("after changing a read value: %v", err)
			}
		})
	}
}

func TestRecordRejectsInvalidNamespacesAndKeys(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CreateRecord("bad/namespace", "draft-1", nil); !errors.Is(err, store.ErrInvalidRecordNamespace) {
				t.Errorf("CreateRecord(bad/namespace, draft-1) error = %v, want store.ErrInvalidRecordNamespace", err)
			}

			if _, err := s.CreateRecord("drafts", "../escape", nil); !errors.Is(err, store.ErrInvalidRecordKey) {
				t.Errorf("CreateRecord(drafts, ../escape) error = %v, want store.ErrInvalidRecordKey", err)
			}

			if _, err := s.ListRecords("drafts", "../escape"); !errors.Is(err, store.ErrInvalidRecordKey) {
				t.Errorf("ListRecords(drafts, ../escape) error = %v, want store.ErrInvalidRecordKey", err)
			}

			if _, err := s.ListRecordKeys("../escape", ""); !errors.Is(err, store.ErrInvalidRecordNamespace) {
				t.Errorf("ListRecordKeys(../escape, \"\") error = %v, want store.ErrInvalidRecordNamespace", err)
			}

			if _, err := s.UpdateRecord("drafts", "draft-1/", nil, store.AnyRevision); !errors.Is(err, store.ErrInvalidRecordKey) {
				t.Errorf("UpdateRecord(drafts, draft-1/) error = %v, want store.ErrInvalidRecordKey", err)
			}

			if err := s.DeleteRecord("", "draft-1", store.AnyRevision); !errors.Is(err, store.ErrInvalidRecordNamespace) {
				t.Errorf("DeleteRecord(\"\", draft-1) error = %v, want store.ErrInvalidRecordNamespace", err)
			}
		})
	}
}

func TestRecordConcurrentCreateAndUpdate(t *testing.T) {
	for name, s := range txnStores(t) {
		t.Run(name, func(t *testing.T) {
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
					} else if !errors.Is(createErr, store.ErrRecordExist) {
						t.Errorf("worker %d create error = %v, want store.ErrRecordExist", i, createErr)
					}

					switch {
					case updateErr == nil:
						updates++
					case errors.Is(updateErr, store.ErrRecordConflict):
						conflicts++
					default:
						t.Errorf("worker %d update error = %v, want conflict or success", i, updateErr)
					}
				}()
			}

			wg.Wait()

			type outcomes struct{ Creates, Updates, Conflicts int }

			got := outcomes{Creates: creates, Updates: updates, Conflicts: conflicts}
			if diff := cmp.Diff(outcomes{Creates: 1, Updates: 1, Conflicts: workers - 1}, got); diff != "" {
				t.Errorf("concurrent CreateRecord and UpdateRecord outcomes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// checkRecord reads a record and returns it, with an error unless it holds
// value at revision.
func checkRecord(s store.RecordStore, namespace, key, value string, revision int64) (store.Record, error) {
	got, err := s.GetRecord(namespace, key)
	if err != nil {
		return got, fmt.Errorf("GetRecord(%s, %s) error = %w", namespace, key, err)
	}

	want := store.Record{Namespace: namespace, Key: key, Value: []byte(value), Revision: revision}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(store.Record{}, "Created", "Updated")); diff != "" {
		return got, fmt.Errorf("GetRecord(%s, %s) mismatch (-want +got):\n%s", namespace, key, diff)
	}

	return got, nil
}

// checkConflict returns an error unless err is a *store.RecordConflictError that
// reports the expected and actual revisions.
func checkConflict(err error, expected, actual int64) error {
	var conflict *store.RecordConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, store.ErrRecordConflict) {
		return fmt.Errorf("error = %w, want *store.RecordConflictError", err)
	}

	got := [2]int64{conflict.Expected, conflict.Actual}
	if diff := cmp.Diff([2]int64{expected, actual}, got); diff != "" {
		return fmt.Errorf("conflict [expected actual] revisions mismatch (-want +got):\n%s", diff)
	}

	return nil
}

// checkKeys returns an error unless listing the prefix returns exactly want.
func checkKeys(s store.RecordStore, namespace, prefix string, want []string) error {
	got, err := s.ListRecordKeys(namespace, prefix)
	if err != nil {
		return fmt.Errorf("ListRecordKeys(%s, %q) error = %w", namespace, prefix, err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		return fmt.Errorf("ListRecordKeys(%s, %q) mismatch (-want +got):\n%s", namespace, prefix, diff)
	}

	return nil
}

// createRecords creates each key holding its own name as its value.
func createRecords(t *testing.T, s store.RecordStore, namespace string, keys ...string) {
	t.Helper()

	for _, key := range keys {
		if _, err := s.CreateRecord(namespace, key, []byte(key)); err != nil {
			t.Fatalf("CreateRecord(%s/%s) returned error: %v", namespace, key, err)
		}
	}
}

func recordKeys(records store.Records) []string {
	keys := make([]string, 0, len(records))
	for _, record := range records {
		keys = append(keys, record.Key)
	}

	return keys
}
