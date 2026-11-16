package vlan

import (
	"errors"
	"fmt"

	"phenix/api/experiment"
	"phenix/types"
)

// Aliases collects VLAN alias details for all experiments or a given experiment.
// It returns a map of VLAN aliases mapped to experiments and any errors
// encountered while gathering them.
func Aliases(opts ...Option) (map[string]map[string]int, error) {
	var (
		o    = newOptions(opts...)
		exps []types.Experiment
		info = make(map[string]map[string]int)
	)

	if o.exp == "" {
		var err error

		exps, err = experiment.List()
		if err != nil {
			return nil, fmt.Errorf("getting list of experiments: %w", err)
		}
	} else {
		exp, err := experiment.Get(o.exp)
		if err != nil {
			return nil, fmt.Errorf("getting experiment %s: %w", o.exp, err)
		}

		exps = []types.Experiment{*exp}
	}

	for _, exp := range exps {
		if exp.Running() {
			info[exp.Metadata.Name] = exp.Status.VLANs()
		} else {
			info[exp.Metadata.Name] = exp.Spec.VLANs().Aliases()
		}
	}

	return info, nil
}

var (
	// ErrAliasNotFound is returned by AliasID when the experiment has no VLAN
	// alias with the given name.
	ErrAliasNotFound = errors.New("VLAN alias not found")

	// ErrAliasUnassigned is returned by AliasID when the alias exists but has no
	// VLAN ID yet. An alias of a stopped experiment has no ID unless one was set
	// explicitly; minimega assigns one from the VLAN range when the experiment
	// starts.
	ErrAliasUnassigned = errors.New("VLAN alias has no VLAN ID")
)

// AliasID returns the VLAN ID of the given VLAN alias in the given experiment.
// It returns an error wrapping ErrAliasNotFound if the experiment has no such
// alias, or ErrAliasUnassigned if the alias has no VLAN ID yet.
func AliasID(opts ...Option) (int, error) {
	o := newOptions(opts...)

	if o.exp == "" {
		return 0, errors.New("no experiment name provided")
	}

	if o.alias == "" {
		return 0, errors.New("no VLAN alias provided")
	}

	info, err := Aliases(Experiment(o.exp))
	if err != nil {
		return 0, err
	}

	id, ok := info[o.exp][o.alias]
	if !ok {
		return 0, fmt.Errorf("%w: experiment %s has no VLAN alias %s", ErrAliasNotFound, o.exp, o.alias)
	}

	if id == 0 {
		return 0, fmt.Errorf(
			"%w: experiment %s assigns VLAN alias %s an ID when it starts",
			ErrAliasUnassigned,
			o.exp,
			o.alias,
		)
	}

	return id, nil
}

// Ranges collects VLAN range details for all experiments or a given experiment.
// It returns a map of VLAN ranges mapped to experiments and any errors
// encountered while gathering them.
func Ranges(opts ...Option) (map[string][2]int, error) {
	var (
		o    = newOptions(opts...)
		exps []types.Experiment
		info = make(map[string][2]int)
	)

	if o.exp == "" {
		var err error

		exps, err = experiment.List()
		if err != nil {
			return nil, fmt.Errorf("getting list of experiments: %w", err)
		}
	} else {
		exp, err := experiment.Get(o.exp)
		if err != nil {
			return nil, fmt.Errorf("getting experiment %s: %w", o.exp, err)
		}

		exps = []types.Experiment{*exp}
	}

	for _, exp := range exps {
		if exp.Running() {
			var (
				minVal = 0
				maxVal = 0
			)

			for _, k := range exp.Status.VLANs() {
				if minVal == 0 || k < minVal {
					minVal = k
				}

				if maxVal == 0 || k > maxVal {
					maxVal = k
				}
			}

			info[exp.Metadata.Name] = [2]int{minVal, maxVal}
		} else {
			info[exp.Metadata.Name] = [2]int{exp.Spec.VLANs().Min(), exp.Spec.VLANs().Max()}
		}
	}

	return info, nil
}
