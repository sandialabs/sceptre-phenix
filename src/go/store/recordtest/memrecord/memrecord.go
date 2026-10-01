// Package memrecord is an in-memory [store.RecordStore] for tests, with the
// revision and compare-and-swap rules of the real stores and hooks that inject
// failures or land another writer between two steps. It is imported only by
// tests. It is not part of package recordtest because it imports phenix/store,
// whose own tests import recordtest.
package memrecord

import (
	"sort"
	"strings"
	"sync"
	"time"

	"phenix/store"
)

// Store is an in-memory [store.RecordStore]. Set its hooks before the code
// under test runs, or between two of its calls.
type Store struct {
	mu       sync.Mutex
	revision int64
	records  map[string]map[string]store.Record

	// BeforeCreate, when set, runs before CreateRecord takes the store lock. It
	// may call back into the store, which is how tests interleave a concurrent
	// writer deterministically; a non-nil error fails the create.
	BeforeCreate func(namespace, key string) error
	// BeforeUpdate is BeforeCreate for UpdateRecord.
	BeforeUpdate func(namespace, key string) error
	// AfterWrite, when set and returning a non-nil error, makes CreateRecord
	// and UpdateRecord commit their write and then return that error, the way
	// an etcd transaction can time out after its proposal was applied. It runs
	// under the store lock, so it must not call back into the store, except
	// through [Store.RewriteLocked].
	AfterWrite func(namespace, key string) error
	// FailDelete, when set and returning a non-nil error, makes DeleteRecord
	// fail for the namespace and key, and DeleteRecordPrefix for the namespace
	// and prefix.
	FailDelete func(namespace, keyOrPrefix string) error
	// FailPrefixDelete is FailDelete for DeleteRecordPrefix alone.
	FailPrefixDelete func(namespace, prefix string) error
	// Stamp, when set, returns the time written records are stamped with, the
	// way a real store stamps them with its wall clock. When nil, a record is
	// stamped with [Time] of its revision.
	Stamp func() time.Time
}

// New returns an empty store with no hooks.
func New() *Store {
	return &Store{
		mu:               sync.Mutex{},
		revision:         0,
		records:          make(map[string]map[string]store.Record),
		BeforeCreate:     nil,
		BeforeUpdate:     nil,
		AfterWrite:       nil,
		FailDelete:       nil,
		FailPrefixDelete: nil,
		Stamp:            nil,
	}
}

// compile-time check that the fake keeps up with the interface it stands in for.
var _ store.RecordStore = (*Store)(nil)

// Time is the deterministic time a record written at revision n is stamped
// with when [Store.Stamp] is nil.
func Time(n int64) time.Time {
	return time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second)
}

func (s *Store) ListRecords(namespace, prefix string) (store.Records, error) {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return nil, err //nolint:wrapcheck // the store's own error, as a real store returns it
	}

	if err := store.ValidateRecordPrefix(prefix); err != nil {
		return nil, err //nolint:wrapcheck // the store's own error, as a real store returns it
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records := store.Records{}

	for key, record := range s.records[namespace] {
		if strings.HasPrefix(key, prefix) {
			records = append(records, record.Clone())
		}
	}

	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })

	return records, nil
}

func (s *Store) ListRecordKeys(namespace, prefix string) ([]string, error) {
	records, err := s.ListRecords(namespace, prefix)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(records))
	for _, record := range records {
		keys = append(keys, record.Key)
	}

	return keys, nil
}

func (s *Store) GetRecord(namespace, key string) (store.Record, error) {
	if err := validate(namespace, key); err != nil {
		return store.Record{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[namespace][key]
	if !ok {
		return store.Record{}, store.NewRecordNotExistError(namespace, key)
	}

	return record.Clone(), nil
}

func (s *Store) CreateRecord(namespace, key string, value []byte) (store.Record, error) {
	if err := validate(namespace, key); err != nil {
		return store.Record{}, err
	}

	if s.BeforeCreate != nil {
		if err := s.BeforeCreate(namespace, key); err != nil {
			return store.Record{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.records[namespace][key]; ok {
		return store.Record{}, store.NewRecordExistError(namespace, key)
	}

	s.revision++

	record := store.Record{
		Namespace: namespace,
		Key:       key,
		Value:     store.CopyRecordValue(value),
		Revision:  s.revision,
		Created:   s.now(),
		Updated:   s.now(),
	}

	if s.records[namespace] == nil {
		s.records[namespace] = make(map[string]store.Record)
	}

	s.records[namespace][key] = record

	return s.committed(namespace, key, record)
}

func (s *Store) UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (store.Record, error) {
	if err := validate(namespace, key); err != nil {
		return store.Record{}, err
	}

	if s.BeforeUpdate != nil {
		if err := s.BeforeUpdate(namespace, key); err != nil {
			return store.Record{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.records[namespace][key]
	if !ok {
		return store.Record{}, store.NewRecordNotExistError(namespace, key)
	}

	if expectedRevision != store.AnyRevision && existing.Revision != expectedRevision {
		return store.Record{},
			store.NewRecordConflictError(namespace, key, expectedRevision, existing.Revision)
	}

	s.revision++

	record := store.Record{
		Namespace: namespace,
		Key:       key,
		Value:     store.CopyRecordValue(value),
		Revision:  s.revision,
		Created:   existing.Created,
		Updated:   s.now(),
	}

	s.records[namespace][key] = record

	return s.committed(namespace, key, record)
}

// committed returns the result of a write that was applied, or the error
// AfterWrite reports in its place.
func (s *Store) committed(namespace, key string, record store.Record) (store.Record, error) {
	if s.AfterWrite != nil {
		if err := s.AfterWrite(namespace, key); err != nil {
			return store.Record{}, err
		}
	}

	return record.Clone(), nil
}

func (s *Store) DeleteRecord(namespace, key string, expectedRevision int64) error {
	if err := validate(namespace, key); err != nil {
		return err
	}

	if s.FailDelete != nil {
		if err := s.FailDelete(namespace, key); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.records[namespace][key]
	if !ok {
		return store.NewRecordNotExistError(namespace, key)
	}

	if expectedRevision != store.AnyRevision && existing.Revision != expectedRevision {
		return store.NewRecordConflictError(namespace, key, expectedRevision, existing.Revision)
	}

	delete(s.records[namespace], key)

	return nil
}

func (s *Store) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return 0, err //nolint:wrapcheck // the store's own error, as a real store returns it
	}

	if err := store.ValidateRecordDeletePrefix(prefix); err != nil {
		return 0, err //nolint:wrapcheck // the store's own error, as a real store returns it
	}

	for _, fail := range []func(string, string) error{s.FailDelete, s.FailPrefixDelete} {
		if fail != nil {
			if err := fail(namespace, prefix); err != nil {
				return 0, err
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var deleted int

	for key := range s.records[namespace] {
		if strings.HasPrefix(key, prefix) {
			delete(s.records[namespace], key)

			deleted++
		}
	}

	return deleted, nil
}

// RewriteLocked stores what rewrite makes of the value under namespace and
// key, at a new revision, as another writer would. It does not take the store
// lock, so only AfterWrite, which holds it, may call it. A missing record is
// left missing.
func (s *Store) RewriteLocked(namespace, key string, rewrite func(value []byte) ([]byte, error)) error {
	record, ok := s.records[namespace][key]
	if !ok {
		return store.NewRecordNotExistError(namespace, key)
	}

	value, err := rewrite(store.CopyRecordValue(record.Value))
	if err != nil {
		return err
	}

	s.revision++
	record.Value, record.Revision, record.Updated = store.CopyRecordValue(value), s.revision, s.now()
	s.records[namespace][key] = record

	return nil
}

// Count returns the number of records in a namespace.
func (s *Store) Count(namespace string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.records[namespace])
}

// Keys returns the sorted keys of a namespace.
func (s *Store) Keys(namespace string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]string, 0, len(s.records[namespace]))
	for key := range s.records[namespace] {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

// SetValue overwrites a record's value without changing its revision, as
// corruption on disk would.
func (s *Store) SetValue(namespace, key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[namespace][key]
	if !ok {
		return
	}

	record.Value = store.CopyRecordValue(value)
	s.records[namespace][key] = record
}

// Drop removes a record without running any hook, as a record lost from the
// store would be.
func (s *Store) Drop(namespace, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.records[namespace], key)
}

// now returns the time a record written at the current revision is stamped
// with.
func (s *Store) now() time.Time {
	if s.Stamp != nil {
		return s.Stamp()
	}

	return Time(s.revision)
}

func validate(namespace, key string) error {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return err //nolint:wrapcheck // the store's own error, as a real store returns it
	}

	return store.ValidateRecordKey(key) //nolint:wrapcheck // the store's own error, as a real store returns it
}
