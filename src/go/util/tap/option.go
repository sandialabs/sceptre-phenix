package tap

import "inet.af/netaddr"

type Option func(*options)

type options struct {
	exp    string
	netns  bool
	subnet netaddr.IPPrefix
	used   []netaddr.IPPrefix
}

func NewOptions(opts ...Option) options {
	o := options{netns: true}

	for _, opt := range opts {
		opt(&o)
	}

	if o.subnet.IsZero() {
		o.subnet = netaddr.MustParseIPPrefix("10.213.47.0/30")
	}

	return o
}

func Experiment(e string) Option {
	return func(o *options) {
		o.exp = e
	}
}

func UseNetNS(u bool) Option {
	return func(o *options) {
		o.netns = u
	}
}

func PairSubnet(s netaddr.IPPrefix) Option {
	return func(o *options) {
		o.subnet = s
	}
}

func UsedPairs(u []netaddr.IPPrefix) Option {
	return func(o *options) {
		o.used = u
	}
}
