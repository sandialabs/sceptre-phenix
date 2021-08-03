package scorch

import (
	"phenix/api/scorch/scorchmd"
	"phenix/types"
)

// Option is a function that configures options for a SCORCH component. It is
// used in `scorch.Init`.
type Option func(*Options)

// Options represents a set of options generic to all components.
type Options struct {
	Stage      Action
	Type       string // used to set the component type
	Name       string
	Exp        types.Experiment
	Meta       scorchmd.ComponentMetadata
	Run        int
	Loop       int
	Count      int
	Background bool
}

// NewOptions returns an Options struct initialized with the given option list.
func NewOptions(opts ...Option) Options {
	o := Options{}

	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// Stage sets the stage for the components.
func Stage(a Action) Option {
	return func(o *Options) {
		o.Stage = a
	}
}

// Type sets the type for the component.
func Type(t string) Option {
	return func(o *Options) {
		o.Type = t
	}
}

// Name sets the type for the component.
func Name(n string) Option {
	return func(o *Options) {
		o.Name = n
	}
}

// Experiment sets the experiment config for the component.
func Experiment(e types.Experiment) Option {
	return func(o *Options) {
		o.Exp = e
	}
}

// Metadata sets the metadata for the component.
func Metadata(m scorchmd.ComponentMetadata) Option {
	return func(o *Options) {
		o.Meta = m
	}
}

// RunID sets the index of the current pipeline.
func RunID(i int) Option {
	return func(o *Options) {
		o.Run = i
	}
}

// CurrentLoop sets the index of the currently running loop.
func CurrentLoop(l int) Option {
	return func(o *Options) {
		o.Loop = l
	}
}

// LoopCount sets the count of the currently running loop.
func LoopCount(c int) Option {
	return func(o *Options) {
		o.Count = c
	}
}

// Background marks the component to be run in the background.
func Background() Option {
	return func(o *Options) {
		o.Background = true
	}
}
