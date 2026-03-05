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

	return string(resp.Kvs[0].Value) == "true"
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

	return nil
}

func (e Etcd) Create(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	if resp, _ := e.cli.Get(context.Background(), key); resp.Count != 0 {
		return fmt.Errorf("config %s/%s already exists", c.Kind, c.Metadata.Name)
	}

	now := time.Now().Format(time.RFC3339)

	c.Metadata.Created = now
	c.Metadata.Updated = now

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	if _, err := e.cli.Put(context.Background(), key, string(v)); err != nil {
		return fmt.Errorf("writing config JSON to Etcd: %w", err)
	}

	return nil
}

func (e Etcd) Update(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	if resp, _ := e.cli.Get(context.Background(), key); resp.Count == 0 {
		return fmt.Errorf("config %s/%s doesn't exist", c.Kind, c.Metadata.Name)
	}

	now := time.Now().Format(time.RFC3339)

	c.Metadata.Updated = now

	v, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config JSON: %w", err)
	}

	if _, err := e.cli.Put(context.Background(), key, string(v)); err != nil {
		return fmt.Errorf("writing config JSON to Etcd: %w", err)
	}

	return nil
}

func (e Etcd) Patch(c *Config, u map[string]any) error {
	return errors.New("not implemented")
}

func (e Etcd) Delete(c *Config) error {
	key := fmt.Sprintf("%s/%s", strings.ToLower(c.Kind), c.Metadata.Name)

	if _, err := e.cli.Delete(context.Background(), key); err != nil {
		return fmt.Errorf("deleting key %s: %w", key, err)
	}

	return nil
}
