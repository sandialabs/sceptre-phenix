package vlan

type Option func(*options)

type options struct {
	exp string

	alias string
}

func newOptions(opts ...Option) options {
	var o options

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func Experiment(e string) Option {
	return func(o *options) {
		o.exp = e
	}
}

func Alias(a string) Option {
	return func(o *options) {
		o.alias = a
	}
}
