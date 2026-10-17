package store

import "errors"

var (
	ErrExist    = errors.New("config already exists")
	ErrNotExist = errors.New("config does not exist")
	// ErrConflict is returned by Update and Delete when Config.IfRevision is
	// set and the stored config is at another revision.
	ErrConflict = errors.New("config changed since it was read")
)

type (
	Component string
)

const (
	ComponentConfigs Component = "configs"
)

// Store is the interface that identifies all the required functionality for a
// config store.
type Store interface {
	// Init is used to initialize a config store with options generic to all store
	// implementations.
	Init(...Option) error

	// Close persists any queued writes and closes the store.
	Close() error

	// List returns a list of configs of the given kind(s) from the store.
	List(...string) (Configs, error)

	// Get initializes the given config with data from the store.
	Get(*Config) error

	// Create persists the given config to the store if it doesn't already
	// exist, and sets its Metadata.Revision. It ignores Config.IfRevision.
	Create(*Config) error

	// Update persists the given config to the store if it already exists, and
	// sets its Metadata.Revision to the new revision. When Config.IfRevision is
	// not zero, it returns ErrConflict unless the stored config is at that
	// revision.
	Update(*Config) error

	// Delete removes the given config from the config store. When
	// Config.IfRevision is not zero, it returns ErrConflict unless the stored
	// config is at that revision.
	Delete(*Config) error

	// IsInitialized checks if the given phenix component has been initialized.
	// This is used to avoid re-initializing the default configs.
	IsInitialized(Component) bool

	// InitializeComponent marks the given phenix component as initialized in
	// the store.
	InitializeComponent(Component) error
}
