// Package memrecord is an in-memory [store.RecordStore] for tests, with BoltDB's
// revision rules and hooks that inject failures or interleave writers.
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

	// BeforeCreate, when set, runs before CreateRecord takes the lock and may call
	// back into the store to interleave a writer; an error fails the create.
	BeforeCreate func(namespace, key string) error
	// BeforeUpdate is BeforeCreate for UpdateRecord.
	BeforeUpdate func(namespace, key string) error
	// FailDelete, when it returns an error, fails DeleteRecord for the key and
	// DeleteRecordPrefix for the prefix.
	FailDelete func(namespace, keyOrPrefix string) error
	// FailPrefixDelete is FailDelete for DeleteRecordPrefix alone.
	FailPrefixDelete func(namespace, prefix string) error
	// Stamp, when set, returns the time stamped on written records; when nil, a
	// record gets the [Time] of its revision.
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
		FailDelete:       nil,
		FailPrefixDelete: nil,
		Stamp:            nil,
	}
}

// Compile-time check that the fake implements the interface it stands in for.
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
			records = append(records, clone(record))
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

	return clone(record), nil
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

	return clone(record), nil
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

	return clone(record), nil
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

func clone(record store.Record) store.Record {
	record.Value = store.CopyRecordValue(record.Value)

	return record
}
