package config

import (
	"errors"
	"fmt"
	"strings"

	"phenix/store"
	"phenix/types"
)

// ErrRunningExperiment is the error of DeleteKind and Apply for the config of
// an experiment that runs, which they do not delete or change.
var ErrRunningExperiment = errors.New("the experiment is running")

// DeleteFailure is a config that DeleteKind did not delete, and why.
type DeleteFailure struct {
	Config string // the full name, such as Topology/foo
	Err    error
}

// DeleteKindOption changes what DeleteKind deletes, and how.
type DeleteKindOption func(*deleteKindOptions)

type deleteKindOptions struct {
	selected func(c store.Config) bool
	del      func(name string) error
}

// DeleteKindSelected makes DeleteKind leave out each config for which
// selected is false, such as a config the caller may not delete. It neither
// deletes nor reports it.
func DeleteKindSelected(selected func(c store.Config) bool) DeleteKindOption {
	return func(o *deleteKindOptions) {
		o.selected = selected
	}
}

// DeleteKindWith makes DeleteKind delete each config, by its full name, with
// del in place of Delete.
func DeleteKindWith(del func(name string) error) DeleteKindOption {
	return func(o *deleteKindOptions) {
		o.del = del
	}
}

// DeleteKind deletes, one at a time, every config of kind: a kind that List
// takes, in any letter case, or "all" for every kind. It does not delete the
// config of an experiment that runs (ErrRunningExperiment). It returns the full
// names of the configs it deleted and the failures, both in the order of
// List. A kind that List does not take returns an error that wraps
// ErrUnknownKind, and deletes nothing.
func DeleteKind(kind string, opts ...DeleteKindOption) ([]string, []DeleteFailure, error) {
	o := deleteKindOptions{selected: func(store.Config) bool { return true }, del: Delete}

	for _, opt := range opts {
		opt(&o)
	}

	if !knownKind(kind) {
		return nil, nil, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}

	configs, err := List(kind)
	if err != nil {
		return nil, nil, err
	}

	var (
		deleted []string
		failed  []DeleteFailure
	)

	for _, c := range configs {
		if !o.selected(c) {
			continue
		}

		name := c.FullName()

		if runningExperiment(c) {
			failed = append(failed, DeleteFailure{Config: name, Err: ErrRunningExperiment})

			continue
		}

		if err := o.del(name); err != nil {
			failed = append(failed, DeleteFailure{Config: name, Err: err})

			continue
		}

		deleted = append(deleted, name)
	}

	return deleted, failed, nil
}

// knownKind reports whether List takes kind: "all" or a config kind, in any
// letter case. The empty kind, which List takes as all, is not one.
func knownKind(kind string) bool {
	if strings.EqualFold(kind, "all") {
		return true
	}

	for _, k := range AllKinds {
		if strings.EqualFold(kind, k) {
			return true
		}
	}

	return false
}

// runningExperiment reports whether c is the config of an experiment that
// runs.
func runningExperiment(c store.Config) bool {
	if c.Kind != kindExperiment {
		return false
	}

	exp, err := types.DecodeExperimentFromConfig(c)

	return err == nil && exp.Running()
}
