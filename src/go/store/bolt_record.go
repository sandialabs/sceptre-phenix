package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"go.etcd.io/bbolt"
)

// boltRecordsBucket is the dedicated top-level bucket that holds all
// non-config records. Record namespaces are nested buckets in it, which keeps
// records separate from config kind buckets.
const boltRecordsBucket = "phenix_records"

// The on-disk form of a record: a header of [boltRecordHeaderSize] bytes,
// then the value as it is. The header is the format byte, then the revision,
// the creation time and the update time, each as a big-endian 64-bit number.
// The times are nanoseconds since the Unix epoch. The bucket and key that the
// record is stored under give the namespace and key.
const (
	boltRecordFormat     byte = 1
	boltRecordHeaderSize      = 1 + 3*8
)

var (
	_ RecordStore = (*BoltDB)(nil)
	_ RecordTx    = boltRecordTx{tx: nil}

	errBoltRecordFormat = errors.New("record is not in a known format")
)

// boltRecord is a record as it is stored, without its namespace and key.
type boltRecord struct {
	value    []byte
	revision int64
	created  time.Time
	updated  time.Time
}

// boltRecordTx is the [RecordTx] of one Bolt write transaction.
type boltRecordTx struct {
	tx *bbolt.Tx
}

// ListRecords returns every record in the given namespace whose key starts with
// the given prefix, ordered by key.
func (b *BoltDB) ListRecords(namespace, prefix string) (Records, error) {
	if err := validateRecordNamespaceAndPrefix(namespace, prefix); err != nil {
		return nil, err
	}

	records := Records{}

	err := b.viewRecords(func(tx *bbolt.Tx) error {
		bucket := boltNamespaceBucket(tx, namespace)
		if bucket == nil {
			return nil
		}

		return boltForEachPrefix(bucket, prefix, func(key []byte, stored boltRecord) error {
			records = append(records, stored.record(namespace, string(key)))

			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("listing records in namespace %s: %w", namespace, err)
	}

	return records, nil
}

// ListRecordKeys returns only the ordered record keys matching namespace and
// prefix without decoding or copying record values.
func (b *BoltDB) ListRecordKeys(namespace, prefix string) ([]string, error) {
	if err := validateRecordNamespaceAndPrefix(namespace, prefix); err != nil {
		return nil, err
	}

	keys := []string{}

	err := b.viewRecords(func(tx *bbolt.Tx) error {
		bucket := boltNamespaceBucket(tx, namespace)
		if bucket == nil {
			return nil
		}

		cursor := bucket.Cursor()
		for key, _ := cursor.Seek([]byte(prefix)); key != nil && bytes.HasPrefix(key, []byte(prefix)); key, _ = cursor.Next() {
			keys = append(keys, string(key))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing record keys in namespace %s: %w", namespace, err)
	}

	return keys, nil
}

// GetRecord returns the record stored at the given namespace and key.
func (b *BoltDB) GetRecord(namespace, key string) (Record, error) {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return Record{}, err
	}

	var record Record

	err := b.viewRecords(func(tx *bbolt.Tx) error {
		stored, err := boltGetRecord(tx, namespace, key)
		if err != nil {
			return err
		}

		record = stored.record(namespace, key)

		return nil
	})
	if err != nil {
		return Record{}, fmt.Errorf("getting record %s/%s: %w", namespace, key, err)
	}

	return record, nil
}

// CreateRecord persists the given value at the given namespace and key if no
// record already exists there.
func (b *BoltDB) CreateRecord(namespace, key string, value []byte) (Record, error) {
	var record Record

	err := b.RecordTxn(func(tx RecordTx) error {
		var err error

		record, err = tx.CreateRecord(namespace, key, value)

		return err
	})

	return record, err
}

// UpdateRecord replaces the value stored at the given namespace and key if the
// stored revision matches the expected revision.
func (b *BoltDB) UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (Record, error) {
	var record Record

	err := b.RecordTxn(func(tx RecordTx) error {
		var err error

		record, err = tx.UpdateRecord(namespace, key, value, expectedRevision)

		return err
	})

	return record, err
}

// DeleteRecord removes the record stored at the given namespace and key if the
// stored revision matches the expected revision.
func (b *BoltDB) DeleteRecord(namespace, key string, expectedRevision int64) error {
	return b.RecordTxn(func(tx RecordTx) error {
		return tx.DeleteRecord(namespace, key, expectedRevision)
	})
}

// DeleteRecordPrefix removes every record in the given namespace whose key
// starts with the given non-empty prefix.
func (b *BoltDB) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	var deleted int

	err := b.RecordTxn(func(tx RecordTx) error {
		var err error

		deleted, err = tx.DeleteRecordPrefix(namespace, prefix)

		return err
	})

	return deleted, err
}

// RecordTxn runs fn in one Bolt write transaction. Bolt runs one write
// transaction at a time, so no other write comes between the reads and the
// writes of fn.
func (b *BoltDB) RecordTxn(fn func(RecordTx) error) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	var fnErr error

	err := b.db.Update(func(tx *bbolt.Tx) error {
		fnErr = fn(boltRecordTx{tx: tx})

		return fnErr
	})

	switch {
	case fnErr != nil:
		return fnErr
	case err != nil:
		return fmt.Errorf("committing records to Bolt: %w", err)
	}

	return nil
}

// GetRecord returns the record stored at the given namespace and key.
func (t boltRecordTx) GetRecord(namespace, key string) (Record, error) {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return Record{}, err
	}

	stored, err := boltGetRecord(t.tx, namespace, key)
	if err != nil {
		return Record{}, fmt.Errorf("getting record %s/%s: %w", namespace, key, err)
	}

	return stored.record(namespace, key), nil
}

// CreateRecord persists the given value at the given namespace and key if no
// record already exists there.
func (t boltRecordTx) CreateRecord(namespace, key string, value []byte) (Record, error) {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return Record{}, err
	}

	record, err := t.create(namespace, key, value)
	if err != nil {
		return Record{}, fmt.Errorf("creating record %s/%s: %w", namespace, key, err)
	}

	return record, nil
}

// UpdateRecord replaces the value stored at the given namespace and key if the
// stored revision matches the expected revision.
func (t boltRecordTx) UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (Record, error) {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return Record{}, err
	}

	record, err := t.update(namespace, key, value, expectedRevision)
	if err != nil {
		return Record{}, fmt.Errorf("updating record %s/%s: %w", namespace, key, err)
	}

	return record, nil
}

// DeleteRecord removes the record stored at the given namespace and key if the
// stored revision matches the expected revision.
func (t boltRecordTx) DeleteRecord(namespace, key string, expectedRevision int64) error {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return err
	}

	if err := t.delete(namespace, key, expectedRevision); err != nil {
		return fmt.Errorf("deleting record %s/%s: %w", namespace, key, err)
	}

	return nil
}

// DeleteRecordPrefix removes every record in the given namespace whose key
// starts with the given non-empty prefix.
func (t boltRecordTx) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	if err := ValidateRecordNamespace(namespace); err != nil {
		return 0, err
	}

	if err := ValidateRecordDeletePrefix(prefix); err != nil {
		return 0, err
	}

	deleted, err := t.deletePrefix(namespace, prefix)
	if err != nil {
		return 0, fmt.Errorf("deleting records in namespace %s with prefix %s: %w", namespace, prefix, err)
	}

	return deleted, nil
}

func (t boltRecordTx) create(namespace, key string, value []byte) (Record, error) {
	bucket, err := boltEnsureNamespaceBucket(t.tx, namespace)
	if err != nil {
		return Record{}, err
	}

	if bucket.Get([]byte(key)) != nil {
		return Record{}, NewRecordExistError(namespace, key)
	}

	revision, err := boltNextRevision(t.tx)
	if err != nil {
		return Record{}, err
	}

	now := time.Now().UTC()
	stored := boltRecord{value: value, revision: revision, created: now, updated: now}

	if err := boltPutRecord(bucket, key, stored); err != nil {
		return Record{}, err
	}

	return stored.record(namespace, key), nil
}

func (t boltRecordTx) update(namespace, key string, value []byte, expectedRevision int64) (Record, error) {
	existing, err := boltGetRecord(t.tx, namespace, key)
	if err != nil {
		return Record{}, err
	}

	if err := checkRecordRevision(namespace, key, expectedRevision, existing.revision); err != nil {
		return Record{}, err
	}

	revision, err := boltNextRevision(t.tx)
	if err != nil {
		return Record{}, err
	}

	stored := boltRecord{value: value, revision: revision, created: existing.created, updated: time.Now().UTC()}

	bucket, err := boltEnsureNamespaceBucket(t.tx, namespace)
	if err != nil {
		return Record{}, err
	}

	if err := boltPutRecord(bucket, key, stored); err != nil {
		return Record{}, err
	}

	return stored.record(namespace, key), nil
}

func (t boltRecordTx) delete(namespace, key string, expectedRevision int64) error {
	existing, err := boltGetRecord(t.tx, namespace, key)
	if err != nil {
		return err
	}

	if err := checkRecordRevision(namespace, key, expectedRevision, existing.revision); err != nil {
		return err
	}

	bucket := boltNamespaceBucket(t.tx, namespace)
	if bucket == nil {
		return NewRecordNotExistError(namespace, key)
	}

	if err := bucket.Delete([]byte(key)); err != nil {
		return fmt.Errorf("deleting record from Bolt: %w", err)
	}

	return nil
}

func (t boltRecordTx) deletePrefix(namespace, prefix string) (int, error) {
	bucket := boltNamespaceBucket(t.tx, namespace)
	if bucket == nil {
		return 0, nil
	}

	var keys [][]byte

	err := boltForEachPrefix(bucket, prefix, func(key []byte, _ boltRecord) error {
		keys = append(keys, append([]byte(nil), key...))

		return nil
	})
	if err != nil {
		return 0, err
	}

	for _, key := range keys {
		if err := bucket.Delete(key); err != nil {
			return 0, fmt.Errorf("deleting record from Bolt: %w", err)
		}
	}

	return len(keys), nil
}

// viewRecords runs fn in one Bolt read transaction.
func (b *BoltDB) viewRecords(fn func(*bbolt.Tx) error) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	return b.db.View(fn) //nolint:wrapcheck // the callers wrap it with what they read
}

func validateRecordNamespaceAndKey(namespace, key string) error {
	if err := ValidateRecordNamespace(namespace); err != nil {
		return err
	}

	return ValidateRecordKey(key)
}

func validateRecordNamespaceAndPrefix(namespace, prefix string) error {
	if err := ValidateRecordNamespace(namespace); err != nil {
		return err
	}

	return ValidateRecordPrefix(prefix)
}

func checkRecordRevision(namespace, key string, expected, actual int64) error {
	if expected == AnyRevision || expected == actual {
		return nil
	}

	return NewRecordConflictError(namespace, key, expected, actual)
}

func boltNamespaceBucket(tx *bbolt.Tx, namespace string) *bbolt.Bucket {
	root := tx.Bucket([]byte(boltRecordsBucket))
	if root == nil {
		return nil
	}

	return root.Bucket([]byte(namespace))
}

func boltEnsureNamespaceBucket(tx *bbolt.Tx, namespace string) (*bbolt.Bucket, error) {
	root, err := tx.CreateBucketIfNotExists([]byte(boltRecordsBucket))
	if err != nil {
		return nil, fmt.Errorf("creating records bucket in Bolt: %w", err)
	}

	bucket, err := root.CreateBucketIfNotExists([]byte(namespace))
	if err != nil {
		return nil, fmt.Errorf("creating record namespace bucket %s in Bolt: %w", namespace, err)
	}

	return bucket, nil
}

// boltNextRevision returns the next monotonically increasing revision. Bolt
// stores the sequence in the records bucket, so revisions continue to increase
// across reopens of the database file.
func boltNextRevision(tx *bbolt.Tx) (int64, error) {
	root, err := tx.CreateBucketIfNotExists([]byte(boltRecordsBucket))
	if err != nil {
		return 0, fmt.Errorf("creating records bucket in Bolt: %w", err)
	}

	seq, err := root.NextSequence()
	if err != nil {
		return 0, fmt.Errorf("generating record revision in Bolt: %w", err)
	}

	return int64(seq), nil //nolint:gosec // bolt sequences start at 1 and never exceed int64
}

func boltGetRecord(tx *bbolt.Tx, namespace, key string) (boltRecord, error) {
	bucket := boltNamespaceBucket(tx, namespace)
	if bucket == nil {
		return boltRecord{}, NewRecordNotExistError(namespace, key)
	}

	v := bucket.Get([]byte(key))
	if v == nil {
		return boltRecord{}, NewRecordNotExistError(namespace, key)
	}

	return decodeBoltRecord(v)
}

// decodeBoltRecord reads a stored record. Its value shares memory with v,
// which is valid only until the transaction ends.
func decodeBoltRecord(v []byte) (boltRecord, error) {
	if len(v) < boltRecordHeaderSize || v[0] != boltRecordFormat {
		return boltRecord{}, errBoltRecordFormat
	}

	return boltRecord{
		value:    v[boltRecordHeaderSize:],
		revision: int64(binary.BigEndian.Uint64(v[1:9])), //nolint:gosec // written from an int64
		created:  boltRecordTime(v[9:17]),
		updated:  boltRecordTime(v[17:25]),
	}, nil
}

func encodeBoltRecord(stored boltRecord) []byte {
	v := make([]byte, boltRecordHeaderSize, boltRecordHeaderSize+len(stored.value))
	v[0] = boltRecordFormat
	binary.BigEndian.PutUint64(v[1:9], uint64(stored.revision))             //nolint:gosec // revisions are never negative
	binary.BigEndian.PutUint64(v[9:17], uint64(stored.created.UnixNano()))  //nolint:gosec // read back as an int64
	binary.BigEndian.PutUint64(v[17:25], uint64(stored.updated.UnixNano())) //nolint:gosec // read back as an int64

	return append(v, stored.value...)
}

func boltRecordTime(b []byte) time.Time {
	return time.Unix(0, int64(binary.BigEndian.Uint64(b))).UTC() //nolint:gosec // written from an int64
}

func boltPutRecord(bucket *bbolt.Bucket, key string, stored boltRecord) error {
	if err := bucket.Put([]byte(key), encodeBoltRecord(stored)); err != nil {
		return fmt.Errorf("writing record to Bolt: %w", err)
	}

	return nil
}

func boltForEachPrefix(bucket *bbolt.Bucket, prefix string, fn func([]byte, boltRecord) error) error {
	cursor := bucket.Cursor()
	seek := []byte(prefix)

	for k, v := cursor.Seek(seek); k != nil && bytes.HasPrefix(k, seek); k, v = cursor.Next() {
		if v == nil { // nested bucket, not a record
			continue
		}

		stored, err := decodeBoltRecord(v)
		if err != nil {
			return fmt.Errorf("reading record %q: %w", k, err)
		}

		if err := fn(k, stored); err != nil {
			return err
		}
	}

	return nil
}

// record returns the stored record with a value of its own.
func (r boltRecord) record(namespace, key string) Record {
	return Record{
		Namespace: namespace,
		Key:       key,
		Value:     CopyRecordValue(r.value),
		Revision:  r.revision,
		Created:   r.created,
		Updated:   r.updated,
	}
}
