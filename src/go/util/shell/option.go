package shell

import (
	"bufio"
	"os"
)

type Option func(*options)

type options struct {
	cmd   string
	env   []string
	args  []string
	stdin []byte

	stdout chan []byte
	stderr chan []byte

	splitter bufio.SplitFunc
}

func newOptions(opts ...Option) options {
	o := options{
		env:      os.Environ(),
		splitter: bufio.ScanLines,
	}

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func Command(c string) Option {
	return func(o *options) {
		o.cmd = c
	}
}

func Env(e ...string) Option {
	return func(o *options) {
		o.env = append(o.env, e...)
	}
}

func Args(a ...string) Option {
	return func(o *options) {
		o.args = a
	}
}

func Stdin(s []byte) Option {
	return func(o *options) {
		o.stdin = s
	}
}

func StreamStdout(s chan []byte) Option {
	return func(o *options) {
		o.stdout = s
	}
}

func StreamStderr(s chan []byte) Option {
	return func(o *options) {
		o.stderr = s
	}
}

func SplitLines() Option {
	return func(o *options) {
		o.splitter = bufio.ScanLines
	}
}

func SplitWords() Option {
	return func(o *options) {
		o.splitter = bufio.ScanWords
	}
}
