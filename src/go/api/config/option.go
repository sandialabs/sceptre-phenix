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
	check    func(*store.Config) error
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

// CreateWithCheck runs check on the parsed config before it is validated or
// stored, and stops the create if check returns an error. Callers use it to
// authorize the config's kind and name.
func CreateWithCheck(check func(*store.Config) error) CreateOption {
	return func(o *createOptions) {
		o.check = check
	}
}

func CreateWithValidation() CreateOption {
	return func(o *createOptions) {
		o.validate = true
	}
}
