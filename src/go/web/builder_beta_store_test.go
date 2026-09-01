package web

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"phenix/store"
)

// builderBetaFakeStore is a focused, in-memory [store.RecordStore] with the
// same revision and compare-and-swap semantics as the real stores. It exists so
// the Builder Beta handlers can be exercised end to end without any I/O.
type builderBetaFakeStore struct {
	mu       sync.Mutex
	revision int64
	records  map[string]map[string]store.Record
	// failPrefixDelete makes every prefix deletion fail, which is how the
	// service is driven into reporting a cleanup failure after a durable write.
	failPrefixDelete bool
}

func newBuilderBetaFakeStore() *builderBetaFakeStore {
	return &builderBetaFakeStore{
		mu:               sync.Mutex{},
		revision:         0,
		records:          make(map[string]map[string]store.Record),
		failPrefixDelete: false,
	}
}

// errBuilderBetaPrefixDelete is what a store with prefix deletion disabled
// reports.
var errBuilderBetaPrefixDelete = errors.New("prefix deletion is unavailable")

// builderBetaFakeTime derives a deterministic timestamp from a revision.
func builderBetaFakeTime(n int64) time.Time {
	return time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC).
		Add(time.Duration(n) * time.Second)
}

func (f *builderBetaFakeStore) ListRecords(namespace, prefix string) (store.Records, error) {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return nil, err
	}

	if prefix != "" {
		if err := store.ValidateRecordPrefix(prefix); err != nil {
			return nil, err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	records := store.Records{}

	for key, record := range f.records[namespace] {
		if strings.HasPrefix(key, prefix) {
			records = append(records, record.Clone())
		}
	}

	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })

	return records, nil
}

func (f *builderBetaFakeStore) GetRecord(namespace, key string) (store.Record, error) {
	if err := builderBetaValidateRecord(namespace, key); err != nil {
		return store.Record{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	record, ok := f.records[namespace][key]
	if !ok {
		return store.Record{}, store.NewRecordNotExistError(namespace, key)
	}

	return record.Clone(), nil
}

func (f *builderBetaFakeStore) CreateRecord(namespace, key string, value []byte) (store.Record, error) {
	if err := builderBetaValidateRecord(namespace, key); err != nil {
		return store.Record{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.records[namespace][key]; ok {
		return store.Record{}, store.NewRecordExistError(namespace, key)
	}

	f.revision++

	record := store.Record{
		Namespace: namespace,
		Key:       key,
		Value:     store.CopyRecordValue(value),
		Revision:  f.revision,
		Created:   builderBetaFakeTime(f.revision),
		Updated:   builderBetaFakeTime(f.revision),
	}

	if f.records[namespace] == nil {
		f.records[namespace] = make(map[string]store.Record)
	}

	f.records[namespace][key] = record

	return record.Clone(), nil
}

func (f *builderBetaFakeStore) UpdateRecord(
	namespace, key string,
	value []byte,
	expectedRevision int64,
) (store.Record, error) {
	if err := builderBetaValidateRecord(namespace, key); err != nil {
		return store.Record{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	existing, ok := f.records[namespace][key]
	if !ok {
		return store.Record{}, store.NewRecordNotExistError(namespace, key)
	}

	if expectedRevision != store.AnyRevision && existing.Revision != expectedRevision {
		return store.Record{},
			store.NewRecordConflictError(namespace, key, expectedRevision, existing.Revision)
	}

	f.revision++

	record := store.Record{
		Namespace: namespace,
		Key:       key,
		Value:     store.CopyRecordValue(value),
		Revision:  f.revision,
		Created:   existing.Created,
		Updated:   builderBetaFakeTime(f.revision),
	}

	f.records[namespace][key] = record

	return record.Clone(), nil
}

func (f *builderBetaFakeStore) DeleteRecord(namespace, key string, expectedRevision int64) error {
	if err := builderBetaValidateRecord(namespace, key); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	existing, ok := f.records[namespace][key]
	if !ok {
		return store.NewRecordNotExistError(namespace, key)
	}

	if expectedRevision != store.AnyRevision && existing.Revision != expectedRevision {
		return store.NewRecordConflictError(namespace, key, expectedRevision, existing.Revision)
	}

	delete(f.records[namespace], key)

	return nil
}

func (f *builderBetaFakeStore) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return 0, err
	}

	if err := store.ValidateRecordDeletePrefix(prefix); err != nil {
		return 0, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failPrefixDelete {
		return 0, errBuilderBetaPrefixDelete
	}

	var deleted int

	for key := range f.records[namespace] {
		if strings.HasPrefix(key, prefix) {
			delete(f.records[namespace], key)

			deleted++
		}
	}

	return deleted, nil
}

// count returns the number of records in a namespace.
func (f *builderBetaFakeStore) count(namespace string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.records[namespace])
}

func builderBetaValidateRecord(namespace, key string) error {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return err
	}

	return store.ValidateRecordKey(key)
}
