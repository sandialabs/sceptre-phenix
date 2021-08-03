package store

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

type BoltDB struct {
	sync.Mutex

	db   *bbolt.DB
	path string
}

func NewBoltDB() Store {
	return new(BoltDB)
}

func (this *BoltDB) Init(opts ...Option) error {
	options := NewOptions(opts...)

	u, err := url.Parse(options.Endpoint)
	if err != nil {
		return fmt.Errorf("parsing BoltDB endpoint: %w", err)
	}

	if u.Scheme != "bolt" {
		return fmt.Errorf("invalid scheme '%s' for BoltDB endpoint", u.Scheme)
	}

	this.path = u.Host + u.Path

	return nil
}

func (this *BoltDB) open() error {
	this.Lock()

	var err error

	this.db, err = bbolt.Open(this.path, 0600, &bbolt.Options{NoFreelistSync: true})
	if err != nil {
		return err
	}

	return nil
}

func (this *BoltDB) Close() error {
	defer this.Unlock()

	if this.db == nil {
		return nil
	}

	return this.db.Close()
}

func (this *BoltDB) List(kinds ...string) (Configs, error) {
	this.open()
	defer this.Close()

	var configs Configs

	for _, kind := range kinds {
		if err := this.ensureBucket(kind); err != nil {
			return nil, err
		}

		err := this.db.View(func(tx *bbolt.Tx) error {
			b := tx.Bucket([]byte(kind))

			err := b.ForEach(func(_, v []byte) error {
				var c Config

				if err := json.Unmarshal(v, &c); err != nil {
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

func (this *BoltDB) Get(c *Config) error {
	this.open()
	defer this.Close()

	v, err := this.get(c.Kind, c.Metadata.Name)
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	if err := json.Unmarshal(v, c); err != nil {
		return fmt.Errorf("unmarshaling config JSON: %w", err)
	}

	return nil
}

func (this *BoltDB) Create(c *Config) error {
	this.open()
	defer this.Close()

	if _, err := this.get(c.Kind, c.Metadata.Name); err == nil {
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

	if err := this.put(c.Kind, c.Metadata.Name, v); err != nil {
		return fmt.Errorf("writing config JSON to Bolt: %w", err)
	}

	return nil
}

func (this *BoltDB) Update(c *Config) error {
	this.open()
	defer this.Close()

	if _, err := this.get(c.Kind, c.Metadata.Name); err != nil {
		return ErrNotExist
	}

	c.Metadata.Updated = time.Now().Format(time.RFC3339)

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	if err := this.put(c.Kind, c.Metadata.Name, v); err != nil {
		return fmt.Errorf("writing config JSON to Bolt: %w", err)
	}

	return nil
}

func (this *BoltDB) Patch(*Config, map[string]interface{}) error {
	return fmt.Errorf("BoltDB.Patch not implemented")
}

func (this *BoltDB) Delete(c *Config) error {
	this.open()
	defer this.Close()

	if err := this.ensureBucket(c.Kind); err != nil {
		return nil
	}

	err := this.db.Update(func(tx *bbolt.Tx) error {
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

func (this *BoltDB) GetEvents() (Events, error) {
	this.open()
	defer this.Close()

	if err := this.ensureBucket("events"); err != nil {
		return nil, err
	}

	var events Events

	err := this.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("events"))

		err := b.ForEach(func(_, v []byte) error {
			var e Event

			if err := json.Unmarshal(v, &e); err != nil {
				return fmt.Errorf("unmarshaling event JSON: %w", err)
			}

			events = append(events, e)

			return nil
		})

		if err != nil {
			return fmt.Errorf("iterating event bucket: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("getting events from store: %w", err)
	}

	return events, nil
}

func (this *BoltDB) GetEventsBy(e Event) (Events, error) {
	this.open()
	defer this.Close()

	if err := this.ensureBucket("events"); err != nil {
		return nil, err
	}

	if e.ID != "" {
		v, err := this.get("events", e.ID)
		if err != nil {
			return nil, fmt.Errorf("getting event: %w", err)
		}

		var event Event

		if err := json.Unmarshal(v, &event); err != nil {
			return nil, fmt.Errorf("unmarshaling event JSON: %w", err)
		}

		return []Event{event}, nil
	}

	var events Events

	err := this.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("events"))

		err := b.ForEach(func(_, v []byte) error {
			var event Event

			if err := json.Unmarshal(v, &event); err != nil {
				return fmt.Errorf("unmarshaling event JSON: %w", err)
			}

			if e.Type != EventTypeNotSet {
				if event.Type != e.Type {
					return nil
				}
			}

			if e.Source != "" {
				if event.Source != e.Source {
					return nil
				}
			}

			if e.Metadata != nil {
				if event.Metadata == nil {
					return nil
				}

				for k, v := range e.Metadata {
					if event.Metadata[k] != v {
						return nil
					}
				}
			}

			events = append(events, event)

			return nil
		})

		if err != nil {
			return fmt.Errorf("iterating event bucket: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("getting events from store: %w", err)
	}

	return events, nil
}

func (this *BoltDB) GetEvent(e *Event) error {
	this.open()
	defer this.Close()

	v, err := this.get("events", e.ID)
	if err != nil {
		return fmt.Errorf("getting event: %w", err)
	}

	if err := json.Unmarshal(v, e); err != nil {
		return fmt.Errorf("unmarshaling event JSON: %w", err)
	}

	return nil
}

func (this *BoltDB) AddEvent(e Event) error {
	this.open()
	defer this.Close()

	v, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshaling event JSON: %w", err)
	}

	if err := this.put("events", e.ID, v); err != nil {
		return fmt.Errorf("writing event JSON to Bolt: %w", err)
	}

	return nil
}

func (this *BoltDB) get(b, k string) ([]byte, error) {
	if err := this.ensureBucket(b); err != nil {
		return nil, err
	}

	var v []byte

	this.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(b))
		v = b.Get([]byte(k))
		return nil
	})

	if v == nil {
		return nil, fmt.Errorf("%w: key %s does not exist in bucket %s", ErrNotExist, k, b)
	}

	return v, nil
}

func (this *BoltDB) put(b, k string, v []byte) error {
	if err := this.ensureBucket(b); err != nil {
		return err
	}

	err := this.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(b))
		return b.Put([]byte(k), v)
	})

	if err != nil {
		return fmt.Errorf("updating value for key %s in bucket %s: %w", k, b, err)
	}

	return nil
}

func (this *BoltDB) ensureBucket(name string) error {
	return this.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(name))
		if err != nil {
			return fmt.Errorf("creating bucket in Bolt: %w", err)
		}

		return nil
	})
}
