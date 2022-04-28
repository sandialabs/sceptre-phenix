package config

import "phenix/store"

type DataType int

const (
	DataTypeUnknown DataType = iota
	DataTypeJSON
	DataTypeYAML
)

type CreateOption func(*createOptions)

type createOptions struct {
	config   *store.Config
	path     string
	data     []byte
	dataType DataType
	validate bool
	scope    string

	scopeVariables []string
}

func newCreateOptions(opts ...CreateOption) createOptions {
	o := createOptions{
		scopeVariables: []string{"{{BRANCH_NAME}}"},
	}

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

func CreateFromJSON(d []byte) CreateOption {
	return func(o *createOptions) {
		o.data = d
		o.dataType = DataTypeJSON
	}
}

func CreateFromYAML(d []byte) CreateOption {
	return func(o *createOptions) {
		o.data = d
		o.dataType = DataTypeYAML
	}
}

func CreateWithValidation() CreateOption {
	return func(o *createOptions) {
		o.validate = true
	}
}

func CreateWithScope(s string) CreateOption {
	return func(o *createOptions) {
		o.scope = s
	}
}
