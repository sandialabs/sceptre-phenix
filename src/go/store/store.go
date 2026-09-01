package store

import "errors"

var (
	ErrExist    = errors.New("config already exists")
	ErrNotExist = errors.New("config does not exist")
)

type (
	Component string
)

const (
	ComponentConfigs Component = "configs"
	ComponentStore   Component = "store"
)

// RecordStore is the interface that identifies the functionality required for
// persisting generic, non-config records (e.g. builder draft metadata and
// immutable content chunks). Records are opaque byte values organized by
// namespace and key, and are versioned with a store assigned, monotonically
// increasing revision used for optimistic concurrency control.
type RecordStore interface {
	// ListRecords returns every record in the given namespace whose key starts
	// with the given prefix, ordered by key. An empty prefix matches every record
	// in the namespace. Listing an unknown namespace returns no records and no
	// error.
	ListRecords(namespace, prefix string) (Records, error)

	// GetRecord returns the record stored at the given namespace and key. A
	// *RecordNotExistError is returned if the record does not exist.
	GetRecord(namespace, key string) (Record, error)

	// CreateRecord persists the given value at the given namespace and key if no
	// record already exists there, returning the created record. A
	// *RecordExistError is returned if a record already exists.
	CreateRecord(namespace, key string, value []byte) (Record, error)

	// UpdateRecord replaces the value stored at the given namespace and key only
	// if the stored revision matches the expected revision, returning the updated
	// record. Passing AnyRevision skips the revision check. A
	// *RecordNotExistError is returned if the record does not exist and a
	// *RecordConflictError is returned if the revision does not match.
	UpdateRecord(namespace, key string, value []byte, expectedRevision int64) (Record, error)

	// DeleteRecord removes the record stored at the given namespace and key only
	// if the stored revision matches the expected revision. Passing AnyRevision
	// skips the revision check. A *RecordNotExistError is returned if the record
	// does not exist and a *RecordConflictError is returned if the revision does
	// not match.
	DeleteRecord(namespace, key string, expectedRevision int64) error

	// DeleteRecordPrefix removes every record in the given namespace whose key
	// starts with the given prefix, returning the number of records deleted. The
	// prefix must not be empty so an entire namespace cannot be deleted
	// accidentally.
	DeleteRecordPrefix(namespace, prefix string) (int, error)
}

// Store is the interface that identifies all the required functionality for a
// config store. Not all functions are required to be implemented. If not
// implemented, they should return an error stating such.
type Store interface { //nolint:interfacebloat // config and record persistence share one store
	RecordStore

	// Init is used to initialize a config store with options generic to all store
	// implementations.
	Init(...Option) error

	// Close persists any queued writes and closes the store.
	Close() error

	// List returns a list of configs of the given kind(s) from the store.
	List(...string) (Configs, error)

	// Get initializes the given config with data from the store.
	Get(*Config) error

	// Create persists the given config to the store if it doesn't already exist.
	Create(*Config) error

	// Update persists the given config to the store if it already exists.
	Update(*Config) error

	// Patch modifies the given config in the store with the given data if the
	// config already exists.
	Patch(*Config, map[string]any) error

	// Delete removes the given config from the config store.
	Delete(*Config) error

	// IsInitialized checks if the given phenix components have been
	// initialized. This is used to avoid re-initializing the store or
	// default configs.
	IsInitialized(Component) bool

	// InitializeComponent marks the given phenix component as initialized in
	// the store.
	InitializeComponent(Component) error
}
