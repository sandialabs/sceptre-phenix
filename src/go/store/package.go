package store

import (
	"fmt"
	"net/url"
)

var DefaultStore Store = NewBoltDB() //nolint:gochecknoglobals // default implementation

func Init(opts ...Option) error {
	options := NewOptions(opts...)

	u, err := url.Parse(options.Endpoint)
	if err != nil {
		return fmt.Errorf("parsing store endpoint: %w", err)
	}

	switch u.Scheme {
	case "bolt":
		DefaultStore = NewBoltDB()
	case "etcd":
		DefaultStore = NewEtcd()
	default:
		return fmt.Errorf("unknown store scheme '%s'", u.Scheme)
	}

	return DefaultStore.Init(opts...)
}

func Close() error {
	return DefaultStore.Close()
}

func List(kinds ...string) (Configs, error) {
	return DefaultStore.List(kinds...)
}

func Get(config *Config) error {
	return DefaultStore.Get(config)
}

func Create(config *Config) error {
	return DefaultStore.Create(config)
}

func Update(config *Config) error {
	return DefaultStore.Update(config)
}

func Patch(config *Config, data map[string]any) error {
	return DefaultStore.Patch(config, data)
}

func Delete(config *Config) error {
	return DefaultStore.Delete(config)
}

func IsInitialized(component Component) bool {
	return DefaultStore.IsInitialized(component)
}

func InitializeComponent(component Component) error {
	return DefaultStore.InitializeComponent(component)
}

// ListRecords returns every record in the given namespace whose key starts with
// the given prefix, using the default store.
func ListRecords(namespace, prefix string) (Records, error) {
	return DefaultStore.ListRecords(namespace, prefix)
}

// ListRecordKeys returns only the ordered keys matching namespace and prefix,
// using the default store.
func ListRecordKeys(namespace, prefix string) ([]string, error) {
	return DefaultStore.ListRecordKeys(namespace, prefix)
}

// GetRecord returns the record stored at the given namespace and key, using the
// default store.
func GetRecord(namespace, key string) (Record, error) {
	return DefaultStore.GetRecord(namespace, key)
}

// CreateRecord persists the given value at the given namespace and key if no
// record already exists there, using the default store.
func CreateRecord(namespace, key string, value []byte) (Record, error) {
	return DefaultStore.CreateRecord(namespace, key, value)
}

// UpdateRecord replaces the value stored at the given namespace and key if the
// stored revision matches the expected revision, using the default store.
func UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (Record, error) {
	return DefaultStore.UpdateRecord(namespace, key, value, expectedRevision)
}

// DeleteRecord removes the record stored at the given namespace and key if the
// stored revision matches the expected revision, using the default store.
func DeleteRecord(namespace, key string, expectedRevision int64) error {
	return DefaultStore.DeleteRecord(namespace, key, expectedRevision)
}

// DeleteRecordPrefix removes every record in the given namespace whose key
// starts with the given non-empty prefix, using the default store.
func DeleteRecordPrefix(namespace, prefix string) (int, error) {
	return DefaultStore.DeleteRecordPrefix(namespace, prefix)
}
