package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"go.etcd.io/etcd/v3/clientv3"
	"go.etcd.io/etcd/v3/etcdserver/api/v3rpc/rpctypes"

	"phenix/util/plog"
)

// errEtcdNoSpace is returned by a write etcd refused because its database
// reached its space quota. etcd then raises a NOSPACE alarm and refuses writes
// until an administrator frees space and clears the alarm. The error etcd
// returned stays wrapped.
var errEtcdNoSpace = errors.New(
	"etcd is out of space: phenix cannot save changes until an administrator frees space " +
		"(compact and defragment etcd, then clear its NOSPACE alarm)",
)

// etcdNoSpaceLogged is set once a write refused for lack of space is logged,
// and cleared by the next write etcd accepts, so an outage is logged once and
// not once per refused write.
var etcdNoSpaceLogged atomic.Bool //nolint:gochecknoglobals // shared by every Etcd store in the process

type Etcd struct {
	endpoints []string

	cli *clientv3.Client
}

func NewEtcd() Store { //nolint:ireturn // factory
	return new(Etcd)
}

func (e *Etcd) Init(opts ...Option) error {
	options := NewOptions(opts...)

	u, err := url.Parse(options.Endpoint)
	if err != nil {
		return fmt.Errorf("parsing Etcd endpoint: %w", err)
	}

	if u.Scheme != "etcd" {
		return fmt.Errorf("invalid scheme '%s' for Etcd endpoint", u.Scheme)
	}

	e.endpoints = []string{u.Host + u.Path}

	cfg := clientv3.Config{ //nolint:exhaustruct // partial initialization
		Endpoints: []string{u.Host + u.Path},
	}

	e.cli, err = clientv3.New(cfg)
	if err != nil {
		return fmt.Errorf("creating new Etcd client: %w", err)
	}

	return e.initializeStore()
}

// initializeStore marks the store component initialized, unless it already
// is. etcd refuses that put once it is out of space, which would stop every
// phenix command from starting, even to read.
func (e *Etcd) initializeStore() error {
	if e.IsInitialized(ComponentStore) {
		return nil
	}

	if err := e.InitializeComponent(ComponentStore); err != nil {
		return fmt.Errorf("initializing component %s: %w", ComponentStore, err)
	}

	return nil
}

func (e *Etcd) IsInitialized(component Component) bool {
	key := fmt.Sprintf("%s/%s", "phenix", string(component))

	resp, err := e.cli.Get(context.Background(), key)
	if err != nil || len(resp.Kvs) == 0 {
		return false
	}

	return string(resp.Kvs[0].Value) == "true"
}

func (e *Etcd) InitializeComponent(component Component) error {
	key := fmt.Sprintf("%s/%s", "phenix", string(component))
	if _, err := e.cli.Put(context.Background(), key, "true"); err != nil {
		return fmt.Errorf("marking component %s as initialized: %w", component, etcdWriteError(err))
	}

	etcdNoSpaceLogged.Store(false)

	return nil
}

func (e Etcd) Close() error {
	return e.cli.Close()
}

func (e Etcd) List(kinds ...string) (Configs, error) {
	var configs Configs

	for _, kind := range kinds {
		kind = strings.ToLower(kind)

		resp, err := e.cli.Get(context.Background(), kind, clientv3.WithPrefix())
		if err != nil {
			return nil, fmt.Errorf("getting list of configs from Etcd: %w", err)
		}

		for _, entry := range resp.Kvs {
			var c Config

			err := json.Unmarshal(entry.Value, &c)
			if err != nil {
				return nil, fmt.Errorf("unmarshaling config JSON: %w", err)
			}

			configs = append(configs, c)
		}
	}

	return configs, nil
}

func (e Etcd) Get(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	resp, err := e.cli.Get(context.Background(), key)
	if err != nil {
		return fmt.Errorf("getting config %s from Etcd: %w", key, err)
	}

	if resp.Count == 0 {
		return fmt.Errorf("%w: %s", ErrNotExist, key)
	}

	entry := resp.Kvs[0]

	if err := json.Unmarshal(entry.Value, &c); err != nil {
		return fmt.Errorf("unmarshaling config JSON: %w", err)
	}

	return nil
}

func (e Etcd) Create(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	resp, err := e.cli.Get(context.Background(), key)
	if err != nil {
		return fmt.Errorf("checking for existing config %s in Etcd: %w", key, err)
	}

	if resp.Count != 0 {
		return fmt.Errorf("%w: %s", ErrExist, key)
	}

	now := time.Now().Format(time.RFC3339)

	// The created timestamp may already be set if the call to Create is part of a
	// config update that includes a rename. Freshly created configs have it reset
	// (see helpers in types.go), so users cannot set it.
	if c.Metadata.Created == "" {
		c.Metadata.Created = now
	}

	c.Metadata.Updated = now

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	if _, err := e.cli.Put(context.Background(), key, string(v)); err != nil {
		return fmt.Errorf("writing config JSON to Etcd: %w", etcdWriteError(err))
	}

	etcdNoSpaceLogged.Store(false)

	return nil
}

func (e Etcd) Update(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	resp, err := e.cli.Get(context.Background(), key)
	if err != nil {
		return fmt.Errorf("checking for existing config %s in Etcd: %w", key, err)
	}

	if resp.Count == 0 {
		return fmt.Errorf("%w: %s", ErrNotExist, key)
	}

	now := time.Now().Format(time.RFC3339)

	c.Metadata.Updated = now

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	if _, err := e.cli.Put(context.Background(), key, string(v)); err != nil {
		return fmt.Errorf("writing config JSON to Etcd: %w", etcdWriteError(err))
	}

	etcdNoSpaceLogged.Store(false)

	return nil
}

func (e Etcd) Patch(c *Config, u map[string]any) error {
	return errors.New("not implemented")
}

func (e Etcd) Delete(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	resp, err := e.cli.Delete(context.Background(), key)
	if err != nil {
		return fmt.Errorf("deleting key %s: %w", key, err)
	}

	if resp.Deleted == 0 {
		return fmt.Errorf("deleting key %s: %w", key, ErrNotExist)
	}

	return nil
}

// etcdWriteError wraps a failed write in [errEtcdNoSpace] when etcd refused it
// for lack of space, since etcd's own message does not say what to do, and logs
// the first such failure since etcd last accepted a write. Any other error is
// returned as it is. etcd checks its quota only for puts and transactions, so a
// delete is never refused for lack of space.
func etcdWriteError(err error) error {
	if !errors.Is(err, rpctypes.ErrNoSpace) {
		return err
	}

	err = fmt.Errorf("%w: %w", errEtcdNoSpace, err)

	if etcdNoSpaceLogged.CompareAndSwap(false, true) {
		plog.Warn(plog.TypeSystem, "writing to Etcd", "err", err)
	}

	return err
}
