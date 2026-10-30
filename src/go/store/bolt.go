package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

const boltFileMode = 0o600

// boltOpenTimeout bounds the wait for another process to release its lock on the BoltDB file.
var boltOpenTimeout = 10 * time.Second //nolint:gochecknoglobals // overridden in tests

// BoltDB opens its file for each operation, read-only for reads, so the phenix CLI and the UI server can share it (Bolt
// locks the file while it is open). Writes keep the free page list in the file, so an open never walks a large file to
// rebuild it.
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

	path, err := BoltPath(options.Endpoint)
	if err != nil {
		return err
	}

	b.path = path

	if err := b.open(); err != nil {
		return fmt.Errorf("initializing BoltDB store: %w", err)
	}

	return b.close()
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

	defer func() { _ = b.close() }()

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
	b.db, err = bbolt.Open(b.path, boltFileMode, &bbolt.Options{Timeout: boltOpenTimeout}) //nolint:exhaustruct // partial initialization
	if err != nil {
		b.mu.Unlock()

		return fmt.Errorf("opening BoltDB file %s: %w", b.path, err)
	}

	return nil
}

// view runs fn in a read transaction on a read-only open of the file.
func (b *BoltDB) view(fn func(*bbolt.Tx) error) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	opts := &bbolt.Options{ReadOnly: true, Timeout: boltOpenTimeout} //nolint:exhaustruct // partial initialization

	db, err := bbolt.Open(b.path, boltFileMode, opts)
	if err != nil {
		return fmt.Errorf("opening BoltDB file %s: %w", b.path, err)
	}

	defer func() { _ = db.Close() }()

	return db.View(fn)
}

// close closes the file open() opened and releases its lock.
func (b *BoltDB) close() error {
	defer b.mu.Unlock()

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

	defer func() { _ = b.close() }()

	// restored on failure, so a create of an existing name leaves c as it was
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

	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(c.Kind))
		if err != nil {
			return fmt.Errorf("creating bucket in Bolt: %w", err)
		}

		if bucket.Get([]byte(c.Metadata.Name)) != nil {
			return ErrExist
		}

		return putWithRevision(tx, bucket, c)
	})
	if err != nil {
		c.Metadata = metadata

		if errors.Is(err, ErrExist) {
			return ErrExist
		}

		return fmt.Errorf("writing config JSON to Bolt: %w", err)
	}

	return nil
}

func (b *BoltDB) Update(c *Config) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.close() }()

	// restored on failure, so a failed update leaves c as it was
	metadata := c.Metadata
	c.Metadata.Updated = time.Now().Format(time.RFC3339)

	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(c.Kind))
		if bucket == nil {
			return ErrNotExist
		}

		stored := bucket.Get([]byte(c.Metadata.Name))
		if stored == nil {
			return ErrNotExist
		}

		if err := checkRevision(stored, c.IfRevision); err != nil {
			return err
		}

		return putWithRevision(tx, bucket, c)
	})
	if err != nil {
		c.Metadata = metadata

		if errors.Is(err, ErrNotExist) || errors.Is(err, ErrConflict) {
			return err
		}

		return fmt.Errorf("writing config JSON to Bolt: %w", err)
	}

	return nil
}

func (b *BoltDB) Delete(c *Config) error {
	if err := b.open(); err != nil {
		return err
	}

	defer func() { _ = b.close() }()

	err := b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(c.Kind))
		if bucket == nil {
			return ErrNotExist
		}

		stored := bucket.Get([]byte(c.Metadata.Name))
		if stored == nil {
			return ErrNotExist
		}

		if err := checkRevision(stored, c.IfRevision); err != nil {
			return err
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

// revisionKey holds the last revision given to a config, in the phenix bucket.
// One counter for every kind keeps revisions unique across the store.
const revisionKey = "revision"

// putWithRevision gives c the store's next revision and writes it to bucket.
// The counter and the config are written in the same transaction, and Bolt
// opens the file exclusively for writes, so revisions are unique across
// processes sharing the file.
func putWithRevision(tx *bbolt.Tx, bucket *bbolt.Bucket, c *Config) error {
	meta, err := tx.CreateBucketIfNotExists([]byte("phenix"))
	if err != nil {
		return fmt.Errorf("creating bucket in Bolt: %w", err)
	}

	var rev uint64

	if v := meta.Get([]byte(revisionKey)); len(v) == revisionSize {
		rev = binary.BigEndian.Uint64(v)
	}

	rev++

	if err := meta.Put([]byte(revisionKey), binary.BigEndian.AppendUint64(nil, rev)); err != nil {
		return fmt.Errorf("writing revision counter: %w", err)
	}

	c.Metadata.Revision = rev

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	return bucket.Put([]byte(c.Metadata.Name), v)
}

// revisionSize is the length of the encoded revision counter.
const revisionSize = 8

// checkRevision returns ErrConflict when want is not zero and the stored
// config JSON is at another revision.
func checkRevision(stored []byte, want uint64) error {
	if want == 0 {
		return nil
	}

	var current struct {
		Metadata struct {
			Revision uint64 `json:"revision"`
		} `json:"metadata"`
	}

	if err := json.Unmarshal(stored, &current); err != nil {
		return fmt.Errorf("unmarshaling config JSON: %w", err)
	}

	if current.Metadata.Revision != want {
		return fmt.Errorf("%w: at revision %d, not %d", ErrConflict, current.Metadata.Revision, want)
	}

	return nil
}

// BoltPath returns the file a bolt:// store endpoint names.
func BoltPath(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parsing BoltDB endpoint: %w", err)
	}

	if u.Scheme != "bolt" {
		return "", fmt.Errorf("invalid scheme '%s' for store endpoint: only bolt:// is supported", u.Scheme)
	}

	return u.Host + u.Path, nil
}
