package config

import "phenix/store"

type CreateOption func(*createOptions)

type createOptions struct {
	config   *store.Config
	path     string
	data     []byte
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

func CreateFromYAML(d []byte) CreateOption {
	return func(o *createOptions) {
		o.data = d
	}
}

// CreateWithCheck runs check on the parsed config before it is validated or
// stored, and stops the create with check's error, such as to authorize the
// config's kind and name.
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

// DeleteOption changes how DeleteAtRevision deletes a config.
type DeleteOption func(*deleteOptions)

type deleteOptions struct {
	filesRoot string
}

func newDeleteOptions(opts ...DeleteOption) deleteOptions {
	var o deleteOptions

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// DeleteWithFilesRoot sets the FilesRoot of each config that DeleteAtRevision
// deletes, so that its delete hooks leave the folders outside root. The web
// API passes it.
func DeleteWithFilesRoot(root string) DeleteOption {
	return func(o *deleteOptions) {
		o.filesRoot = root
	}
}
