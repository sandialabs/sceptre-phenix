package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.etcd.io/etcd/v3/clientv3"
)

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

	if err := e.InitializeComponent(ComponentStore); err != nil {
		return fmt.Errorf("initializing component %s: %w", ComponentStore, err)
	}

	return nil
}

func (e *Etcd) IsInitialized(component Component) bool {
	key := fmt.Sprintf("%s/%s", "phenix", string(component))

	resp, err := e.cli.Get(context.Background(), key)
	if err != nil {
		return false
	}

	return etcdInitialized(resp)
}

// etcdInitialized reports whether a component key read says "true"; a missing
// key means the component is not initialized.
func etcdInitialized(resp *clientv3.GetResponse) bool {
	return len(resp.Kvs) > 0 && string(resp.Kvs[0].Value) == "true"
}

func (e *Etcd) InitializeComponent(component Component) error {
	key := fmt.Sprintf("%s/%s", "phenix", string(component))
	if _, err := e.cli.Put(context.Background(), key, "true"); err != nil {
		return fmt.Errorf("marking component %s as initialized: %w", component, err)
	}

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

			c.Metadata.Revision = uint64(entry.ModRevision) //nolint:gosec // etcd revisions are positive

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
		return fmt.Errorf("config %s not found", key)
	}

	entry := resp.Kvs[0]

	if err := json.Unmarshal(entry.Value, &c); err != nil {
		return fmt.Errorf("unmarshaling config JSON: %w", err)
	}

	c.Metadata.Revision = uint64(entry.ModRevision) //nolint:gosec // etcd revisions are positive

	return nil
}

func (e Etcd) Create(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)
	metadata := c.Metadata

	now := time.Now().Format(time.RFC3339)

	c.Metadata.Created = now
	c.Metadata.Updated = now
	// etcd assigns the revision; the stored JSON carries none, and reads take
	// it from the key's ModRevision
	c.Metadata.Revision = 0

	v, err := json.Marshal(c)
	if err != nil {
		c.Metadata = metadata

		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	// the put only happens while the key does not exist, so two creates cannot both succeed
	resp, err := e.cli.Txn(context.Background()).
		If(clientv3.Compare(clientv3.CreateRevision(key), "=", 0)).
		Then(clientv3.OpPut(key, string(v))).
		Commit()
	if err != nil {
		c.Metadata = metadata

		return fmt.Errorf("writing config JSON to Etcd: %w", err)
	}

	if !resp.Succeeded {
		c.Metadata = metadata

		return fmt.Errorf("config %s/%s: %w", c.Kind, c.Metadata.Name, ErrExist)
	}

	c.Metadata.Revision = uint64(resp.Header.GetRevision()) //nolint:gosec // etcd revisions are positive

	return nil
}

func (e Etcd) Update(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	metadata := c.Metadata

	c.Metadata.Updated = time.Now().Format(time.RFC3339)
	c.Metadata.Revision = 0

	v, err := json.Marshal(c)
	if err != nil {
		c.Metadata = metadata

		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	// The put only happens while the key exists (and, with IfRevision, is still
	// at that revision), so a concurrent delete or write is never overwritten.
	resp, err := e.cli.Txn(context.Background()).
		If(etcdRevisionCompare(key, c.IfRevision)).
		Then(clientv3.OpPut(key, string(v))).
		Else(clientv3.OpGet(key)).
		Commit()
	if err != nil {
		c.Metadata = metadata

		return fmt.Errorf("writing config JSON to Etcd: %w", err)
	}

	if !resp.Succeeded {
		c.Metadata = metadata

		return etcdTxnFailure(resp, c)
	}

	c.Metadata.Revision = uint64(resp.Header.GetRevision()) //nolint:gosec // etcd revisions are positive

	return nil
}

func (e Etcd) Patch(c *Config, u map[string]any) error {
	return errors.New("not implemented")
}

func (e Etcd) Delete(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	if c.IfRevision == 0 {
		if _, err := e.cli.Delete(context.Background(), key); err != nil {
			return fmt.Errorf("deleting key %s: %w", key, err)
		}

		return nil
	}

	resp, err := e.cli.Txn(context.Background()).
		If(etcdRevisionCompare(key, c.IfRevision)).
		Then(clientv3.OpDelete(key)).
		Else(clientv3.OpGet(key)).
		Commit()
	if err != nil {
		return fmt.Errorf("deleting key %s: %w", key, err)
	}

	if !resp.Succeeded {
		return etcdTxnFailure(resp, c)
	}

	return nil
}

// etcdRevisionCompare is the condition for a write to key: the key exists and,
// when ifRevision is not zero, was last written at that revision.
func etcdRevisionCompare(key string, ifRevision uint64) clientv3.Cmp {
	if ifRevision == 0 {
		// a key that exists was created at a revision above zero
		return clientv3.Compare(clientv3.CreateRevision(key), ">", 0)
	}

	return clientv3.Compare(clientv3.ModRevision(key), "=", int64(ifRevision)) //nolint:gosec // revisions fit in int64
}

// etcdTxnFailure explains why an etcdRevisionCompare transaction did not run
// its write, from the key read by its Else branch.
func etcdTxnFailure(resp *clientv3.TxnResponse, c *Config) error {
	var current int64

	if len(resp.Responses) > 0 {
		if kvs := resp.Responses[0].GetResponseRange().GetKvs(); len(kvs) > 0 {
			current = kvs[0].ModRevision
		}
	}

	if current == 0 {
		return fmt.Errorf("config %s/%s doesn't exist: %w", c.Kind, c.Metadata.Name, ErrNotExist)
	}

	return fmt.Errorf("%w: at revision %d, not %d", ErrConflict, current, c.IfRevision)
}
