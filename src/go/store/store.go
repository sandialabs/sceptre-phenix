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

	// Create persists the given config to the store if it doesn't already exist.
	Create(*Config) error

	// Update persists the given config to the store if it already exists.
	Update(*Config) error

	// Delete removes the given config from the config store.
	Delete(*Config) error

	// IsInitialized checks if the given phenix component has been initialized.
	// This is used to avoid re-initializing the default configs.
	IsInitialized(Component) bool

	// InitializeComponent marks the given phenix component as initialized in
	// the store.
	InitializeComponent(Component) error
}
