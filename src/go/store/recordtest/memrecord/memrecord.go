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
	commits  int

	// BeforeCreate, when set, runs before CreateRecord takes the lock and may call
	// back into the store to interleave a writer. An error fails the create.
	// It also runs for the CreateRecord of a transaction.
	BeforeCreate func(namespace, key string) error
	// BeforeUpdate is BeforeCreate for UpdateRecord.
	BeforeUpdate func(namespace, key string) error
	// FailDelete, when it returns an error, fails DeleteRecord for the key and
	// DeleteRecordPrefix for the prefix. Like BeforeCreate, it runs before the
	// delete reads the record and may call back into the store.
	FailDelete func(namespace, keyOrPrefix string) error
	// FailPrefixDelete is FailDelete for DeleteRecordPrefix alone.
	FailPrefixDelete func(namespace, prefix string) error
	// Stamp, when set, returns the time stamped on written records. When nil, a
	// record gets the [Time] of its revision.
	Stamp func() time.Time
}

// ref names one record.
type ref struct {
	namespace string
	key       string
}

// seen is what a transaction read of a record: its revision, or 0 when it
// did not exist. With anyRevision, only whether it existed counts.
type seen struct {
	revision    int64
	anyRevision bool
}

// txn is the [store.RecordTx] of [Store.RecordTxn]. It stages its writes and
// does not hold the store lock while a hook runs. The commit fails, and
// applies nothing, when a record the transaction read changed meanwhile.
type txn struct {
	s      *Store
	staged map[ref]*store.Record
	order  []ref
	seen   map[ref]seen
}

// New returns an empty store with no hooks.
func New() *Store {
	return &Store{
		mu:               sync.Mutex{},
		revision:         0,
		records:          make(map[string]map[string]store.Record),
		commits:          0,
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
	var record store.Record

	err := s.RecordTxn(func(tx store.RecordTx) error {
		var err error

		record, err = tx.CreateRecord(namespace, key, value)

		return err
	})

	return record, err
}

func (s *Store) UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (store.Record, error) {
	var record store.Record

	err := s.RecordTxn(func(tx store.RecordTx) error {
		var err error

		record, err = tx.UpdateRecord(namespace, key, value, expectedRevision)

		return err
	})

	return record, err
}

func (s *Store) DeleteRecord(namespace, key string, expectedRevision int64) error {
	return s.RecordTxn(func(tx store.RecordTx) error {
		return tx.DeleteRecord(namespace, key, expectedRevision)
	})
}

func (s *Store) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	var deleted int

	err := s.RecordTxn(func(tx store.RecordTx) error {
		var err error

		deleted, err = tx.DeleteRecordPrefix(namespace, prefix)

		return err
	})

	return deleted, err
}

// RecordTxn runs fn, then applies all its writes at once, or none (see
// [txn]). A write takes its revision when fn makes it, so a revision that a
// failed transaction took is never given again.
func (s *Store) RecordTxn(fn func(store.RecordTx) error) error {
	tx := &txn{s: s, staged: make(map[ref]*store.Record), order: nil, seen: make(map[ref]seen)}

	if err := fn(tx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for at, before := range tx.seen {
		if err := s.unchanged(at, before); err != nil {
			return err
		}
	}

	for _, at := range tx.order {
		record := tx.staged[at]

		if record == nil {
			delete(s.records[at.namespace], at.key)

			continue
		}

		if s.records[at.namespace] == nil {
			s.records[at.namespace] = make(map[string]store.Record)
		}

		s.records[at.namespace][at.key] = *record
	}

	if len(tx.order) > 0 {
		s.commits++
	}

	return nil
}

func (t *txn) GetRecord(namespace, key string) (store.Record, error) {
	if err := validate(namespace, key); err != nil {
		return store.Record{}, err
	}

	record, ok := t.read(ref{namespace, key}, false)
	if !ok {
		return store.Record{}, store.NewRecordNotExistError(namespace, key)
	}

	return clone(record), nil
}

func (t *txn) CreateRecord(namespace, key string, value []byte) (store.Record, error) {
	if err := validate(namespace, key); err != nil {
		return store.Record{}, err
	}

	if t.s.BeforeCreate != nil {
		if err := t.s.BeforeCreate(namespace, key); err != nil {
			return store.Record{}, err
		}
	}

	at := ref{namespace, key}

	if _, ok := t.read(at, false); ok {
		return store.Record{}, store.NewRecordExistError(namespace, key)
	}

	revision, now := t.s.next()
	record := store.Record{
		Namespace: namespace, Key: key, Value: store.CopyRecordValue(value),
		Revision: revision, Created: now, Updated: now,
	}

	t.stage(at, &record)

	return clone(record), nil
}

func (t *txn) UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (store.Record, error) {
	if err := validate(namespace, key); err != nil {
		return store.Record{}, err
	}

	if t.s.BeforeUpdate != nil {
		if err := t.s.BeforeUpdate(namespace, key); err != nil {
			return store.Record{}, err
		}
	}

	at := ref{namespace, key}

	existing, err := t.check(at, expectedRevision)
	if err != nil {
		return store.Record{}, err
	}

	revision, now := t.s.next()
	record := store.Record{
		Namespace: namespace, Key: key, Value: store.CopyRecordValue(value),
		Revision: revision, Created: existing.Created, Updated: now,
	}

	t.stage(at, &record)

	return clone(record), nil
}

func (t *txn) DeleteRecord(namespace, key string, expectedRevision int64) error {
	if err := validate(namespace, key); err != nil {
		return err
	}

	if t.s.FailDelete != nil {
		if err := t.s.FailDelete(namespace, key); err != nil {
			return err
		}
	}

	at := ref{namespace, key}

	if _, err := t.check(at, expectedRevision); err != nil {
		return err
	}

	t.stage(at, nil)

	return nil
}

func (t *txn) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return 0, err //nolint:wrapcheck // the store's own error, as a real store returns it
	}

	if err := store.ValidateRecordDeletePrefix(prefix); err != nil {
		return 0, err //nolint:wrapcheck // the store's own error, as a real store returns it
	}

	for _, fail := range []func(string, string) error{t.s.FailDelete, t.s.FailPrefixDelete} {
		if fail != nil {
			if err := fail(namespace, prefix); err != nil {
				return 0, err
			}
		}
	}

	var deleted int

	for _, key := range t.keys(namespace, prefix) {
		at := ref{namespace, key}

		if _, ok := t.read(at, true); ok {
			t.stage(at, nil)

			deleted++
		}
	}

	return deleted, nil
}

// read returns the record as the transaction sees it. It keeps what it read
// from the store for the commit to check (see [Store.unchanged]).
func (t *txn) read(at ref, anyRevision bool) (store.Record, bool) {
	if record, ok := t.staged[at]; ok {
		if record == nil {
			return store.Record{}, false
		}

		return *record, true
	}

	t.s.mu.Lock()
	record, ok := t.s.records[at.namespace][at.key]
	t.s.mu.Unlock()

	before, read := t.seen[at]

	switch {
	case !read:
		t.seen[at] = seen{revision: record.Revision, anyRevision: anyRevision}
	case before.anyRevision && !anyRevision:
		t.seen[at] = seen{revision: before.revision, anyRevision: false}
	}

	return record, ok
}

// check returns the record the transaction sees when it exists at the
// expected revision.
func (t *txn) check(at ref, expectedRevision int64) (store.Record, error) {
	existing, ok := t.read(at, expectedRevision == store.AnyRevision)
	if !ok {
		return store.Record{}, store.NewRecordNotExistError(at.namespace, at.key)
	}

	if expectedRevision != store.AnyRevision && existing.Revision != expectedRevision {
		return store.Record{}, store.NewRecordConflictError(at.namespace, at.key, expectedRevision, existing.Revision)
	}

	return existing, nil
}

// stage keeps a write for the commit: a record, or nil for a delete.
func (t *txn) stage(at ref, record *store.Record) {
	if _, ok := t.staged[at]; !ok {
		t.order = append(t.order, at)
	}

	t.staged[at] = record
}

// keys returns the keys in a namespace that start with prefix, in the store
// or written by the transaction.
func (t *txn) keys(namespace, prefix string) []string {
	found := make(map[string]bool)

	t.s.mu.Lock()

	for key := range t.s.records[namespace] {
		if strings.HasPrefix(key, prefix) {
			found[key] = true
		}
	}

	t.s.mu.Unlock()

	for at := range t.staged {
		if at.namespace == namespace && strings.HasPrefix(at.key, prefix) {
			found[at.key] = true
		}
	}

	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

// unchanged returns, as the error a store that runs one transaction at a
// time gives, a record that changed after a transaction read it. The caller
// holds the lock.
func (s *Store) unchanged(at ref, before seen) error {
	record, ok := s.records[at.namespace][at.key]

	switch {
	case before.revision == 0 && ok:
		return store.NewRecordExistError(at.namespace, at.key)
	case before.revision != 0 && !ok:
		return store.NewRecordNotExistError(at.namespace, at.key)
	case !before.anyRevision && ok && record.Revision != before.revision:
		return store.NewRecordConflictError(at.namespace, at.key, before.revision, record.Revision)
	}

	return nil
}

// next takes the next revision, and the time to stamp on a record written at
// it.
func (s *Store) next() (int64, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.revision++

	return s.revision, s.now()
}

// Commits returns the number of transactions that wrote at least one
// record. Each write outside a transaction is a transaction of its own.
func (s *Store) Commits() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.commits
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
