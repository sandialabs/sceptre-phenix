package builder

import (
	"sort"
	"strings"
	"sync"

	"phenix/store"
)

// fakeStore is a focused, in-memory implementation of [store.RecordStore] with
// the same revision and compare-and-swap semantics as the real stores, plus
// hooks for injecting failures.
type fakeStore struct {
	mu       sync.Mutex
	revision int64
	records  map[string]map[string]store.Record

	// failDelete, when set and returning a non-nil error, makes DeleteRecord
	// fail for the given namespace and key.
	failDelete func(namespace, key string) error
	// failUpdate, when set and returning a non-nil error, makes UpdateRecord
	// fail for the given namespace and key.
	failUpdate func(namespace, key string) error
	// beforeCreate, when set, runs before CreateRecord takes the store lock. It
	// may call back into the store, which is how tests interleave a concurrent
	// writer deterministically; a non-nil error fails the create.
	beforeCreate func(namespace, key string) error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		records:      make(map[string]map[string]store.Record),
		failDelete:   nil,
		failUpdate:   nil,
		beforeCreate: nil,
	}
}

// compile-time check that the fake keeps up with the interface it stands in for.
var _ store.RecordStore = (*fakeStore)(nil)

func (f *fakeStore) ListRecords(namespace, prefix string) (store.Records, error) {
	if err := validateNamespaceAndPrefix(namespace, prefix); err != nil {
		return nil, err
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

func (f *fakeStore) ListRecordKeys(namespace, prefix string) ([]string, error) {
	records, err := f.ListRecords(namespace, prefix)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(records))
	for _, record := range records {
		keys = append(keys, record.Key)
	}

	return keys, nil
}

func (f *fakeStore) GetRecord(namespace, key string) (store.Record, error) {
	if err := validateNamespaceAndKey(namespace, key); err != nil {
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

func (f *fakeStore) CreateRecord(namespace, key string, value []byte) (store.Record, error) {
	if err := validateNamespaceAndKey(namespace, key); err != nil {
		return store.Record{}, err
	}

	if f.beforeCreate != nil {
		if err := f.beforeCreate(namespace, key); err != nil {
			return store.Record{}, err
		}
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
		Created:   fakeTime(f.revision),
		Updated:   fakeTime(f.revision),
	}

	if f.records[namespace] == nil {
		f.records[namespace] = make(map[string]store.Record)
	}

	f.records[namespace][key] = record

	return record.Clone(), nil
}

func (f *fakeStore) UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (store.Record, error) {
	if err := validateNamespaceAndKey(namespace, key); err != nil {
		return store.Record{}, err
	}

	if f.failUpdate != nil {
		if err := f.failUpdate(namespace, key); err != nil {
			return store.Record{}, err
		}
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
		Updated:   fakeTime(f.revision),
	}

	f.records[namespace][key] = record

	return record.Clone(), nil
}

func (f *fakeStore) DeleteRecord(namespace, key string, expectedRevision int64) error {
	if err := validateNamespaceAndKey(namespace, key); err != nil {
		return err
	}

	if f.failDelete != nil {
		if err := f.failDelete(namespace, key); err != nil {
			return err
		}
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

func (f *fakeStore) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return 0, err
	}

	if err := store.ValidateRecordDeletePrefix(prefix); err != nil {
		return 0, err
	}

	if f.failDelete != nil {
		if err := f.failDelete(namespace, prefix); err != nil {
			return 0, err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

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
func (f *fakeStore) count(namespace string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.records[namespace])
}

// keys returns the sorted keys of a namespace.
func (f *fakeStore) keys(namespace string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	keys := make([]string, 0, len(f.records[namespace]))
	for key := range f.records[namespace] {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

// setValue overwrites a record value without touching its revision. It is used
// to simulate corruption.
func (f *fakeStore) setValue(namespace, key string, value []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	record, ok := f.records[namespace][key]
	if !ok {
		return
	}

	record.Value = store.CopyRecordValue(value)
	f.records[namespace][key] = record
}

func (f *fakeStore) drop(namespace, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.records[namespace], key)
}

func validateNamespaceAndKey(namespace, key string) error {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return err
	}

	return store.ValidateRecordKey(key)
}

func validateNamespaceAndPrefix(namespace, prefix string) error {
	if err := store.ValidateRecordNamespace(namespace); err != nil {
		return err
	}

	return store.ValidateRecordPrefix(prefix)
}
