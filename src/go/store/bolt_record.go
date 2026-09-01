package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"go.etcd.io/bbolt"
)

// boltRecordsBucket is the dedicated top-level bucket holding all non-config
// records. Record namespaces are nested buckets within it, keeping records
// isolated from config kind buckets.
const boltRecordsBucket = "phenix_records"

// boltRecordEnvelope is the on-disk representation of a record. The namespace
// and key are implied by the bucket and key the envelope is stored under.
type boltRecordEnvelope struct {
	Value    []byte    `json:"value"`
	Revision int64     `json:"revision"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
}

// ListRecords returns every record in the given namespace whose key starts with
// the given prefix, ordered by key.
func (b *BoltDB) ListRecords(namespace, prefix string) (Records, error) {
	if err := validateRecordNamespaceAndPrefix(namespace, prefix); err != nil {
		return nil, err
	}

	if err := b.open(); err != nil {
		return nil, err
	}

	defer func() { _ = b.Close() }()

	records := Records{}

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := boltNamespaceBucket(tx, namespace)
		if bucket == nil {
			return nil
		}

		return boltForEachPrefix(bucket, prefix, func(key []byte, envelope boltRecordEnvelope) error {
			records = append(records, envelope.record(namespace, string(key)))

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

	if err := b.open(); err != nil {
		return nil, err
	}

	defer func() { _ = b.Close() }()

	keys := []string{}

	err := b.db.View(func(tx *bbolt.Tx) error {
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

	if err := b.open(); err != nil {
		return Record{}, err
	}

	defer func() { _ = b.Close() }()

	var record Record

	err := b.db.View(func(tx *bbolt.Tx) error {
		envelope, err := boltGetRecord(tx, namespace, key)
		if err != nil {
			return err
		}

		record = envelope.record(namespace, key)

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
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return Record{}, err
	}

	if err := b.open(); err != nil {
		return Record{}, err
	}

	defer func() { _ = b.Close() }()

	var record Record

	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := boltEnsureNamespaceBucket(tx, namespace)
		if err != nil {
			return err
		}

		if bucket.Get([]byte(key)) != nil {
			return NewRecordExistError(namespace, key)
		}

		revision, err := boltNextRevision(tx)
		if err != nil {
			return err
		}

		now := time.Now().UTC()

		envelope := boltRecordEnvelope{
			Value:    CopyRecordValue(value),
			Revision: revision,
			Created:  now,
			Updated:  now,
		}

		if err := boltPutRecord(bucket, key, envelope); err != nil {
			return err
		}

		record = envelope.record(namespace, key)

		return nil
	})
	if err != nil {
		return Record{}, fmt.Errorf("creating record %s/%s: %w", namespace, key, err)
	}

	return record, nil
}

// UpdateRecord replaces the value stored at the given namespace and key if the
// stored revision matches the expected revision.
func (b *BoltDB) UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (Record, error) {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return Record{}, err
	}

	if err := b.open(); err != nil {
		return Record{}, err
	}

	defer func() { _ = b.Close() }()

	var record Record

	err := b.db.Update(func(tx *bbolt.Tx) error {
		existing, err := boltGetRecord(tx, namespace, key)
		if err != nil {
			return err
		}

		if err := checkRecordRevision(namespace, key, expectedRevision, existing.Revision); err != nil {
			return err
		}

		revision, err := boltNextRevision(tx)
		if err != nil {
			return err
		}

		envelope := boltRecordEnvelope{
			Value:    CopyRecordValue(value),
			Revision: revision,
			Created:  existing.Created,
			Updated:  time.Now().UTC(),
		}

		bucket, err := boltEnsureNamespaceBucket(tx, namespace)
		if err != nil {
			return err
		}

		if err := boltPutRecord(bucket, key, envelope); err != nil {
			return err
		}

		record = envelope.record(namespace, key)

		return nil
	})
	if err != nil {
		return Record{}, fmt.Errorf("updating record %s/%s: %w", namespace, key, err)
	}

	return record, nil
}

// DeleteRecord removes the record stored at the given namespace and key if the
// stored revision matches the expected revision.
func (b *BoltDB) DeleteRecord(namespace, key string, expectedRevision int64) error {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return err
	}

	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	err := b.db.Update(func(tx *bbolt.Tx) error {
		existing, err := boltGetRecord(tx, namespace, key)
		if err != nil {
			return err
		}

		if err := checkRecordRevision(namespace, key, expectedRevision, existing.Revision); err != nil {
			return err
		}

		bucket := boltNamespaceBucket(tx, namespace)
		if bucket == nil {
			return NewRecordNotExistError(namespace, key)
		}

		if err := bucket.Delete([]byte(key)); err != nil {
			return fmt.Errorf("deleting record from Bolt: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("deleting record %s/%s: %w", namespace, key, err)
	}

	return nil
}

// DeleteRecordPrefix removes every record in the given namespace whose key
// starts with the given non-empty prefix.
func (b *BoltDB) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	if err := ValidateRecordNamespace(namespace); err != nil {
		return 0, err
	}

	if err := ValidateRecordDeletePrefix(prefix); err != nil {
		return 0, err
	}

	if err := b.open(); err != nil {
		return 0, err
	}

	defer func() { _ = b.Close() }()

	var deleted int

	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket := boltNamespaceBucket(tx, namespace)
		if bucket == nil {
			return nil
		}

		var keys [][]byte

		err := boltForEachPrefix(bucket, prefix, func(key []byte, _ boltRecordEnvelope) error {
			keys = append(keys, append([]byte(nil), key...))

			return nil
		})
		if err != nil {
			return err
		}

		for _, key := range keys {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("deleting record from Bolt: %w", err)
			}

			deleted++
		}

		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("deleting records in namespace %s with prefix %s: %w", namespace, prefix, err)
	}

	return deleted, nil
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

// boltNextRevision returns the next monotonically increasing revision. The
// sequence is stored by Bolt in the records bucket, so revisions keep
// increasing across reopens of the database file.
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

func boltGetRecord(tx *bbolt.Tx, namespace, key string) (boltRecordEnvelope, error) {
	bucket := boltNamespaceBucket(tx, namespace)
	if bucket == nil {
		return boltRecordEnvelope{}, NewRecordNotExistError(namespace, key)
	}

	v := bucket.Get([]byte(key))
	if v == nil {
		return boltRecordEnvelope{}, NewRecordNotExistError(namespace, key)
	}

	return decodeBoltRecord(v)
}

func decodeBoltRecord(v []byte) (boltRecordEnvelope, error) {
	var envelope boltRecordEnvelope

	if err := json.Unmarshal(v, &envelope); err != nil {
		return boltRecordEnvelope{}, fmt.Errorf("unmarshaling record JSON: %w", err)
	}

	return envelope, nil
}

func boltPutRecord(bucket *bbolt.Bucket, key string, envelope boltRecordEnvelope) error {
	v, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshaling record JSON: %w", err)
	}

	if err := bucket.Put([]byte(key), v); err != nil {
		return fmt.Errorf("writing record to Bolt: %w", err)
	}

	return nil
}

func boltForEachPrefix(bucket *bbolt.Bucket, prefix string, fn func([]byte, boltRecordEnvelope) error) error {
	cursor := bucket.Cursor()
	seek := []byte(prefix)

	for k, v := cursor.Seek(seek); k != nil && bytes.HasPrefix(k, seek); k, v = cursor.Next() {
		if v == nil { // nested bucket, not a record
			continue
		}

		envelope, err := decodeBoltRecord(v)
		if err != nil {
			return err
		}

		if err := fn(k, envelope); err != nil {
			return err
		}
	}

	return nil
}

func (e boltRecordEnvelope) record(namespace, key string) Record {
	return Record{
		Namespace: namespace,
		Key:       key,
		Value:     CopyRecordValue(e.Value),
		Revision:  e.Revision,
		Created:   e.Created,
		Updated:   e.Updated,
	}
}
