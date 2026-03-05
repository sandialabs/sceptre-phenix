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

type BoltDB struct {
	mu sync.Mutex

	db   *bbolt.DB
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
	if err := b.open(); err != nil {
		return false
	}

	defer func() { _ = b.Close() }()

	v, err := b.get("phenix", string(component))
	if err != nil {
		return false
	}

	return v[0] == 1
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

	b.db, err = bbolt.Open(b.path, boltFileMode, &bbolt.Options{NoFreelistSync: true}) //nolint:exhaustruct // partial initialization
	if err != nil {
		return err
	}

	return nil
}

func (b *BoltDB) Close() error {
	defer b.mu.Unlock()

	if b.db == nil {
		return nil
	}

	return b.db.Close()
}

func (b *BoltDB) List(kinds ...string) (Configs, error) {
	err := b.open()
	if err != nil {
		return nil, err
	}

	defer func() { _ = b.Close() }()

	var configs Configs

	for _, kind := range kinds {
		if err := b.ensureBucket(kind); err != nil {
			return nil, err
		}

		err := b.db.View(func(tx *bbolt.Tx) error {
			b := tx.Bucket([]byte(kind))

			err := b.ForEach(func(_, v []byte) error {
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

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("getting configs from store: %w", err)
		}
	}

	return configs, nil
}

func (b *BoltDB) Get(c *Config) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	v, err := b.get(c.Kind, c.Metadata.Name)
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	if err := json.Unmarshal(v, c); err != nil {
		return fmt.Errorf("unmarshaling config JSON: %w", err)
	}

	return nil
}

func (b *BoltDB) Create(c *Config) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.Close() }()

	if _, err := b.get(c.Kind, c.Metadata.Name); err == nil {
		return ErrExist
	}

	now := time.Now().Format(time.RFC3339)

	// The created timestamp may already be set if the call to Create is part of a
	// config update that includes a rename (which essentially becomes a
	// Create/Delete activity). Freshly created configs are guaranteed to have
	// their created timestamp reset (see helpers in types.go) to prevent users
	// from setting them.
	if c.Metadata.Created == "" {
		c.Metadata.Created = now
	}

	c.Metadata.Updated = now

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	if err := b.put(c.Kind, c.Metadata.Name, v); err != nil {
		return fmt.Errorf("writing config JSON to Bolt: %w", err)
	}

	return nil
}

func (b *BoltDB) Update(c *Config) error {
	_ = b.open()

	defer func() { _ = b.Close() }()

	if _, err := b.get(c.Kind, c.Metadata.Name); err != nil {
		return ErrNotExist
	}

	c.Metadata.Updated = time.Now().Format(time.RFC3339)

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	if err := b.put(c.Kind, c.Metadata.Name, v); err != nil {
		return fmt.Errorf("writing config JSON to Bolt: %w", err)
	}

	return nil
}

func (b *BoltDB) Patch(*Config, map[string]any) error {
	return errors.New("boltDB.Patch not implemented")
}

func (b *BoltDB) Delete(c *Config) error {
	_ = b.open()

	defer func() { _ = b.Close() }()

	if err := b.ensureBucket(c.Kind); err != nil {
		return nil
	}

	err := b.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(c.Kind))
		v := b.Get([]byte(c.Metadata.Name))

		if v == nil {
			return ErrNotExist
		}

		return b.Delete([]byte(c.Metadata.Name))
	})
	if err != nil {
		return fmt.Errorf("deleting key %s in bucket %s: %w", c.Metadata.Name, c.Kind, err)
	}

	return nil
}

func (b *BoltDB) get(bucket, k string) ([]byte, error) {
	err := b.ensureBucket(bucket)
	if err != nil {
		return nil, err
	}

	var v []byte

	_ = b.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		v = b.Get([]byte(k))

		return nil
	})

	if v == nil {
		return nil, fmt.Errorf("%w: key %s does not exist in bucket %s", ErrNotExist, k, bucket)
	}

	return v, nil
}

func (b *BoltDB) put(bucket, k string, v []byte) error {
	if err := b.ensureBucket(bucket); err != nil {
		return err
	}

	err := b.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))

		return b.Put([]byte(k), v)
	})
	if err != nil {
		return fmt.Errorf("updating value for key %s in bucket %s: %w", k, bucket, err)
	}

	return nil
}

func (b *BoltDB) ensureBucket(name string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(name))
		if err != nil {
			return fmt.Errorf("creating bucket in Bolt: %w", err)
		}

		return nil
	})
}
