package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

const boltFileMode = 0o600

// BoltDB opens its file for each operation rather than holding it open, so
// the phenix CLI and the UI server can share it (Bolt locks the file while it
// is open). Reads open it read-only, which lets them run together. Writes keep
// the free page list in the file: without it every open walks the whole file
// to rebuild the list, which takes seconds on a large store once the file has
// left the page cache, and reads wait behind it.
type BoltDB struct {
	// held for reading by reads and for writing by writes, since this process's
	// read-only and read-write opens of the file would otherwise block each other
	mu sync.RWMutex

	db   *bbolt.DB // the read-write handle, while mu is held for writing
	path string
}

func NewBoltDB() Store { //nolint:ireturn // factory
	return new(BoltDB)
}

func (b *BoltDB) Init(opts ...Option) error {
	options := NewOptions(opts...)

	u, err := url.Parse(options.Endpoint)
	if err != nil {
		return fmt.Errorf("parsing BoltDB endpoint: %w", err)
	}

	if u.Scheme != "bolt" {
		return fmt.Errorf("invalid scheme '%s' for BoltDB endpoint", u.Scheme)
	}

	b.path = u.Host + u.Path

	if err := b.InitializeComponent(ComponentStore); err != nil {
		return fmt.Errorf("initializing component %s: %w", ComponentStore, err)
	}

	return nil
}

func (b *BoltDB) IsInitialized(component Component) bool {
	var initialized bool

	err := b.view(func(tx *bbolt.Tx) error {
		if bucket := tx.Bucket([]byte("phenix")); bucket != nil {
			v := bucket.Get([]byte(component))
			initialized = len(v) > 0 && v[0] == 1
		}

		return nil
	})

	return err == nil && initialized
}

func (b *BoltDB) InitializeComponent(component Component) error {
	err := b.open()
	if err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	err = b.put("phenix", string(component), []byte{1})
	if err != nil {
		return fmt.Errorf("marking component %s as initialized: %w", component, err)
	}

	return nil
}

func (b *BoltDB) open() error {
	b.mu.Lock()

	var err error

	// The first open of a file written without a free page list walks the file
	// once and saves the list; later opens read it.
	b.db, err = bbolt.Open(b.path, boltFileMode, nil)
	if err != nil {
		// callers only Close after a successful open
		b.db = nil
		b.mu.Unlock()

		return err
	}

	return nil
}

// view runs fn in a read transaction on a read-only open of the file.
func (b *BoltDB) view(fn func(*bbolt.Tx) error) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	db, err := bbolt.Open(b.path, boltFileMode, &bbolt.Options{ReadOnly: true}) //nolint:exhaustruct // partial initialization
	if err != nil {
		return err
	}

	defer func() { _ = db.Close() }()

	return db.View(fn)
}

func (b *BoltDB) Close() error {
	defer b.mu.Unlock()

	if b.db == nil {
		return nil
	}

	return b.db.Close()
}

func (b *BoltDB) List(kinds ...string) (Configs, error) {
	var configs Configs

	err := b.view(func(tx *bbolt.Tx) error {
		for _, kind := range kinds {
			bucket := tx.Bucket([]byte(kind))
			if bucket == nil {
				continue // nothing of this kind stored yet
			}

			err := bucket.ForEach(func(_, v []byte) error {
				var c Config

				err := json.Unmarshal(v, &c)
				if err != nil {
					return fmt.Errorf("unmarshaling config JSON: %w", err)
				}

				configs = append(configs, c)

				return nil
			})
			if err != nil {
				return fmt.Errorf("iterating %s bucket: %w", kind, err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("getting configs from store: %w", err)
	}

	return configs, nil
}

func (b *BoltDB) Get(c *Config) error {
	var found bool

	err := b.view(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(c.Kind))
		if bucket == nil {
			return nil
		}

		// the value is only valid during the transaction
		v := bucket.Get([]byte(c.Metadata.Name))
		if v == nil {
			return nil
		}

		found = true

		if err := json.Unmarshal(v, c); err != nil {
			return fmt.Errorf("unmarshaling config JSON: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	if !found {
		return fmt.Errorf(
			"getting config: %w: key %s does not exist in bucket %s",
			ErrNotExist, c.Metadata.Name, c.Kind,
		)
	}

	return nil
}

func (b *BoltDB) Create(c *Config) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	// restored if the name is taken, so a create of an existing name leaves c
	// as it was
	metadata := c.Metadata

	// The created timestamp may already be set if the call to Create is part of a
	// config update that includes a rename (which essentially becomes a
	// Create/Delete activity). Freshly created configs are guaranteed to have
	// their created timestamp reset (see helpers in types.go) to prevent users
	// from setting them.
	now := time.Now().Format(time.RFC3339)

	if c.Metadata.Created == "" {
		c.Metadata.Created = now
	}

	c.Metadata.Updated = now

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	err = b.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(c.Kind))
		if err != nil {
			return fmt.Errorf("creating bucket in Bolt: %w", err)
		}

		if bucket.Get([]byte(c.Metadata.Name)) != nil {
			return ErrExist
		}

		return bucket.Put([]byte(c.Metadata.Name), v)
	})
	if errors.Is(err, ErrExist) {
		c.Metadata = metadata

		return ErrExist
	}

	if err != nil {
		return fmt.Errorf("writing config JSON to Bolt: %w", err)
	}

	return nil
}

func (b *BoltDB) Update(c *Config) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	updated := c.Metadata.Updated
	c.Metadata.Updated = time.Now().Format(time.RFC3339)

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	err = b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(c.Kind))
		if bucket == nil || bucket.Get([]byte(c.Metadata.Name)) == nil {
			return ErrNotExist
		}

		return bucket.Put([]byte(c.Metadata.Name), v)
	})
	if errors.Is(err, ErrNotExist) {
		c.Metadata.Updated = updated

		return ErrNotExist
	}

	if err != nil {
		return fmt.Errorf("writing config JSON to Bolt: %w", err)
	}

	return nil
}

func (b *BoltDB) Patch(*Config, map[string]any) error {
	return errors.New("boltDB.Patch not implemented")
}

func (b *BoltDB) Delete(c *Config) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(c.Kind))
		if bucket == nil || bucket.Get([]byte(c.Metadata.Name)) == nil {
			return ErrNotExist
		}

		return bucket.Delete([]byte(c.Metadata.Name))
	})
	if err != nil {
		return fmt.Errorf("deleting key %s in bucket %s: %w", c.Metadata.Name, c.Kind, err)
	}

	return nil
}

func (b *BoltDB) put(bucket, k string, v []byte) error {
	err := b.db.Update(func(tx *bbolt.Tx) error {
		bkt, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return fmt.Errorf("creating bucket in Bolt: %w", err)
		}

		return bkt.Put([]byte(k), v)
	})
	if err != nil {
		return fmt.Errorf("updating value for key %s in bucket %s: %w", k, bucket, err)
	}

	return nil
}
