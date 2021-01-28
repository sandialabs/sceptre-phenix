package mm

import "time"

type Option func(*options)

type options struct {
	ns   string
	vm   string
	cpu  int
	mem  int
	disk string

	injectPart int
	injects    []string

	connectIface int
	connectVLAN  string

	captureIface int
	captureFile  string

	screenshotSize string
	c2Command      string
	c2CommandID    string
}

func NewOptions(opts ...Option) options {
	var o options

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func NS(n string) Option {
	return func(o *options) {
		o.ns = n
	}
}

func VMName(v string) Option {
	return func(o *options) {
		o.vm = v
	}
}

func CPU(c int) Option {
	return func(o *options) {
		o.cpu = c
	}
}

func Mem(m int) Option {
	return func(o *options) {
		o.mem = m
	}
}

func Disk(d string) Option {
	return func(o *options) {
		o.disk = d
	}
}

func InjectPartition(p int) Option {
	return func(o *options) {
		o.injectPart = p
	}
}

func Injects(i ...string) Option {
	return func(o *options) {
		o.injects = i
	}
}

func ConnectInterface(i int) Option {
	return func(o *options) {
		o.connectIface = i
	}
}

func ConnectVLAN(v string) Option {
	return func(o *options) {
		o.connectVLAN = v
	}
}

func DisonnectInterface(i int) Option {
	return func(o *options) {
		o.connectIface = i
	}
}

func CaptureInterface(i int) Option {
	return func(o *options) {
		o.captureIface = i
	}
}

func CaptureFile(f string) Option {
	return func(o *options) {
		o.captureFile = f
	}
}

func ScreenshotSize(s string) Option {
	return func(o *options) {
		o.screenshotSize = s
	}
}

type C2Option func(*c2Options)

type c2Options struct {
	ns string
	vm string

	command   string
	commandID string

	timeout time.Duration
}

func NewC2Options(opts ...C2Option) c2Options {
	o := c2Options{
		// Default timeout to 5m if user doesn't set it.
		timeout: 5 * time.Minute,
	}

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func C2NS(n string) C2Option {
	return func(o *c2Options) {
		o.ns = n
	}
}

func C2VM(v string) C2Option {
	return func(o *c2Options) {
		o.vm = v
	}
}

func C2Command(c string) C2Option {
	return func(o *c2Options) {
		o.command = c
	}
}

func C2CommandID(i string) C2Option {
	return func(o *c2Options) {
		o.commandID = i
	}
}

func C2Timeout(d time.Duration) C2Option {
	return func(o *c2Options) {
		o.timeout = d
	}
}
