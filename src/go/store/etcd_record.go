package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.etcd.io/etcd/v3/clientv3"
	"go.etcd.io/etcd/v3/mvcc/mvccpb"
)

// etcdRecordRoot is the key space records are stored under. Config keys are
// written as "<kind>/<name>", so records are namespaced separately to prevent
// collisions.
const etcdRecordRoot = "phenix/records"

// etcdRecordEnvelope is the encoded representation of a record value. The
// revision of a record is not stored in the envelope; etcd's ModRevision is
// used instead.
type etcdRecordEnvelope struct {
	Value   []byte    `json:"value"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// EtcdRecordNamespacePrefix returns the encoded key prefix all records in the
// given namespace are stored under. The trailing separator prevents a namespace
// from matching records in another namespace that shares its name as a prefix.
func EtcdRecordNamespacePrefix(namespace string) string {
	return etcdRecordRoot + "/" + url.PathEscape(namespace) + "/"
}

// EtcdRecordKey returns the encoded etcd key for the given namespace and key.
func EtcdRecordKey(namespace, key string) string {
	return EtcdRecordNamespacePrefix(namespace) + escapeRecordKey(key)
}

// EtcdRecordPrefix returns the encoded etcd key prefix for the given namespace
// and key prefix. Key encoding is per-segment and byte-wise, so the encoding of
// a key prefix is always a prefix of the encoding of any matching key.
func EtcdRecordPrefix(namespace, prefix string) string {
	return EtcdRecordNamespacePrefix(namespace) + escapeRecordKey(prefix)
}

// EtcdRecordKeyName decodes a record key from a full encoded etcd key, relative
// to the given namespace.
func EtcdRecordKeyName(namespace, etcdKey string) (string, error) {
	prefix := EtcdRecordNamespacePrefix(namespace)

	if !strings.HasPrefix(etcdKey, prefix) {
		return "", fmt.Errorf("etcd key %q is not in record namespace %q", etcdKey, namespace)
	}

	return unescapeRecordKey(strings.TrimPrefix(etcdKey, prefix))
}

// EtcdCreateRecordOps returns the comparison and operations used to create a
// record only if it does not already exist. The comparison relies on a version
// of zero, which etcd reports for keys that have never been written or that
// have been deleted.
func EtcdCreateRecordOps(etcdKey string, value []byte) (clientv3.Cmp, clientv3.Op, clientv3.Op) {
	return clientv3.Compare(clientv3.Version(etcdKey), "=", 0),
		clientv3.OpPut(etcdKey, string(value)),
		clientv3.OpGet(etcdKey)
}

// EtcdUpdateRecordOps returns the comparison and operations used to update a
// record only if its stored ModRevision matches the expected revision. Passing
// AnyRevision only requires that the record exists.
func EtcdUpdateRecordOps(etcdKey string, value []byte, expectedRevision int64) (clientv3.Cmp, clientv3.Op, clientv3.Op) {
	return etcdRecordRevisionCmp(etcdKey, expectedRevision),
		clientv3.OpPut(etcdKey, string(value)),
		clientv3.OpGet(etcdKey)
}

// EtcdDeleteRecordOps returns the comparison and operations used to delete a
// record only if its stored ModRevision matches the expected revision. Passing
// AnyRevision only requires that the record exists.
func EtcdDeleteRecordOps(etcdKey string, expectedRevision int64) (clientv3.Cmp, clientv3.Op, clientv3.Op) {
	return etcdRecordRevisionCmp(etcdKey, expectedRevision),
		clientv3.OpDelete(etcdKey),
		clientv3.OpGet(etcdKey)
}

// EtcdRecordTxnFailure converts the key/values returned by the else branch of a
// record transaction into a typed not-found or conflict error.
func EtcdRecordTxnFailure(namespace, key string, expectedRevision int64, kvs []*mvccpb.KeyValue) error {
	if len(kvs) == 0 {
		return NewRecordNotExistError(namespace, key)
	}

	return NewRecordConflictError(namespace, key, expectedRevision, kvs[0].ModRevision)
}

func etcdRecordRevisionCmp(etcdKey string, expectedRevision int64) clientv3.Cmp {
	if expectedRevision == AnyRevision {
		return clientv3.Compare(clientv3.Version(etcdKey), ">", 0)
	}

	return clientv3.Compare(clientv3.ModRevision(etcdKey), "=", expectedRevision)
}

// escapeRecordKey escapes each key segment so keys can never escape their
// namespace or collide with the namespace separator.
func escapeRecordKey(key string) string {
	segments := strings.Split(key, recordKeySeparator)

	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}

	return strings.Join(segments, recordKeySeparator)
}

func unescapeRecordKey(key string) (string, error) {
	segments := strings.Split(key, recordKeySeparator)

	for i, segment := range segments {
		unescaped, err := url.PathUnescape(segment)
		if err != nil {
			return "", fmt.Errorf("unescaping record key %q: %w", key, err)
		}

		segments[i] = unescaped
	}

	return strings.Join(segments, recordKeySeparator), nil
}

func encodeRecordEnvelope(envelope etcdRecordEnvelope) ([]byte, error) {
	v, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshaling record JSON: %w", err)
	}

	return v, nil
}

func decodeRecordEnvelope(v []byte) (etcdRecordEnvelope, error) {
	var envelope etcdRecordEnvelope

	if err := json.Unmarshal(v, &envelope); err != nil {
		return etcdRecordEnvelope{}, fmt.Errorf("unmarshaling record JSON: %w", err)
	}

	return envelope, nil
}

func (e etcdRecordEnvelope) record(namespace, key string, revision int64) Record {
	return Record{
		Namespace: namespace,
		Key:       key,
		Value:     CopyRecordValue(e.Value),
		Revision:  revision,
		Created:   e.Created,
		Updated:   e.Updated,
	}
}

// ListRecords returns every record in the given namespace whose key starts with
// the given prefix, ordered by key.
func (e Etcd) ListRecords(namespace, prefix string) (Records, error) {
	if err := validateRecordNamespaceAndPrefix(namespace, prefix); err != nil {
		return nil, err
	}

	resp, err := e.cli.Get(
		context.Background(), EtcdRecordPrefix(namespace, prefix),
		clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
	)
	if err != nil {
		return nil, fmt.Errorf("listing records in namespace %s from Etcd: %w", namespace, err)
	}

	records := make(Records, 0, len(resp.Kvs))

	for _, kv := range resp.Kvs {
		key, err := EtcdRecordKeyName(namespace, string(kv.Key))
		if err != nil {
			return nil, err
		}

		envelope, err := decodeRecordEnvelope(kv.Value)
		if err != nil {
			return nil, err
		}

		records = append(records, envelope.record(namespace, key, kv.ModRevision))
	}

	return records, nil
}

// GetRecord returns the record stored at the given namespace and key.
func (e Etcd) GetRecord(namespace, key string) (Record, error) {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return Record{}, err
	}

	resp, err := e.cli.Get(context.Background(), EtcdRecordKey(namespace, key))
	if err != nil {
		return Record{}, fmt.Errorf("getting record %s/%s from Etcd: %w", namespace, key, err)
	}

	if len(resp.Kvs) == 0 {
		return Record{}, NewRecordNotExistError(namespace, key)
	}

	envelope, err := decodeRecordEnvelope(resp.Kvs[0].Value)
	if err != nil {
		return Record{}, err
	}

	return envelope.record(namespace, key, resp.Kvs[0].ModRevision), nil
}

// CreateRecord persists the given value at the given namespace and key if no
// record already exists there.
func (e Etcd) CreateRecord(namespace, key string, value []byte) (Record, error) {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return Record{}, err
	}

	now := time.Now().UTC()

	envelope := etcdRecordEnvelope{Value: CopyRecordValue(value), Created: now, Updated: now}

	encoded, err := encodeRecordEnvelope(envelope)
	if err != nil {
		return Record{}, err
	}

	cmp, then, els := EtcdCreateRecordOps(EtcdRecordKey(namespace, key), encoded)

	resp, err := e.cli.Txn(context.Background()).If(cmp).Then(then).Else(els).Commit()
	if err != nil {
		return Record{}, fmt.Errorf("creating record %s/%s in Etcd: %w", namespace, key, err)
	}

	if !resp.Succeeded {
		return Record{}, NewRecordExistError(namespace, key)
	}

	return envelope.record(namespace, key, resp.Header.GetRevision()), nil
}

// UpdateRecord replaces the value stored at the given namespace and key if the
// stored revision matches the expected revision.
func (e Etcd) UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (Record, error) {
	existing, err := e.GetRecord(namespace, key)
	if err != nil {
		return Record{}, err
	}

	if err := checkRecordRevision(namespace, key, expectedRevision, existing.Revision); err != nil {
		return Record{}, err
	}

	envelope := etcdRecordEnvelope{Value: CopyRecordValue(value), Created: existing.Created, Updated: time.Now().UTC()}

	encoded, err := encodeRecordEnvelope(envelope)
	if err != nil {
		return Record{}, err
	}

	cmp, then, els := EtcdUpdateRecordOps(EtcdRecordKey(namespace, key), encoded, expectedRevision)

	resp, err := e.cli.Txn(context.Background()).If(cmp).Then(then).Else(els).Commit()
	if err != nil {
		return Record{}, fmt.Errorf("updating record %s/%s in Etcd: %w", namespace, key, err)
	}

	if !resp.Succeeded {
		kvs := etcdTxnResponseKvs(resp)

		return Record{}, EtcdRecordTxnFailure(namespace, key, expectedRevision, kvs)
	}

	return envelope.record(namespace, key, resp.Header.GetRevision()), nil
}

// DeleteRecord removes the record stored at the given namespace and key if the
// stored revision matches the expected revision.
func (e Etcd) DeleteRecord(namespace, key string, expectedRevision int64) error {
	if err := validateRecordNamespaceAndKey(namespace, key); err != nil {
		return err
	}

	cmp, then, els := EtcdDeleteRecordOps(EtcdRecordKey(namespace, key), expectedRevision)

	resp, err := e.cli.Txn(context.Background()).If(cmp).Then(then).Else(els).Commit()
	if err != nil {
		return fmt.Errorf("deleting record %s/%s in Etcd: %w", namespace, key, err)
	}

	if !resp.Succeeded {
		return EtcdRecordTxnFailure(namespace, key, expectedRevision, etcdTxnResponseKvs(resp))
	}

	return nil
}

// DeleteRecordPrefix removes every record in the given namespace whose key
// starts with the given non-empty prefix.
func (e Etcd) DeleteRecordPrefix(namespace, prefix string) (int, error) {
	if err := ValidateRecordNamespace(namespace); err != nil {
		return 0, err
	}

	if err := ValidateRecordDeletePrefix(prefix); err != nil {
		return 0, err
	}

	resp, err := e.cli.Delete(context.Background(), EtcdRecordPrefix(namespace, prefix), clientv3.WithPrefix())
	if err != nil {
		return 0, fmt.Errorf("deleting records in namespace %s with prefix %s from Etcd: %w", namespace, prefix, err)
	}

	return int(resp.Deleted), nil
}

func etcdTxnResponseKvs(resp *clientv3.TxnResponse) []*mvccpb.KeyValue {
	for _, opResp := range resp.Responses {
		if get := opResp.GetResponseRange(); get != nil {
			return get.GetKvs()
		}
	}

	return nil
}
