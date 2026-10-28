package config

import "phenix/store"

type CreateOption func(*createOptions)

type createOptions struct {
	config   *store.Config
	path     string
	data     []byte
	validate bool
}

func newCreateOptions(opts ...CreateOption) createOptions {
	var o createOptions

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func CreateFromConfig(c *store.Config) CreateOption {
	return func(o *createOptions) {
		o.config = c
	}
}

func CreateFromPath(p string) CreateOption {
	return func(o *createOptions) {
		o.path = p
	}
}

func CreateFromYAML(d []byte) CreateOption {
	return func(o *createOptions) {
		o.data = d
	}
}

func CreateWithValidation() CreateOption {
	return func(o *createOptions) {
		o.validate = true
	}
}
