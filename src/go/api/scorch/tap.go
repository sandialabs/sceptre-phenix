package scorch

import (
	"context"
	"fmt"

	"phenix/api/scorch/scorchmd"
	"phenix/types"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/tap"

	"github.com/mitchellh/mapstructure"
	"inet.af/netaddr"
)

type Tap struct {
	options Options
}

func (this *Tap) Init(opts ...Option) error {
	this.options = NewOptions(opts...)
	return nil
}

func (Tap) Type() string {
	return "tap"
}

func (Tap) Configure(context.Context) error {
	return nil
}

func (this Tap) Start(ctx context.Context) error {
	exp := this.options.Exp.Metadata.Name

	var t *tap.Tap

	if err := mapstructure.Decode(this.options.Meta, &t); err != nil {
		return fmt.Errorf("decoding tap component metadata: %w", err)
	}

	pairs := this.discoverUsedPairs()
	t.Init(tap.Experiment(exp), tap.UsedPairs(pairs))

	// backwards compatibility (doesn't support external access firewall rules)
	if v, ok := t.Other["internetAccess"]; ok {
		enabled, _ := v.(bool)
		t.External.Enabled = enabled
	}

	// tap names cannot be longer than 15 characters
	// (dictated by max length of Linux interface names)
	t.Name = fmt.Sprintf("%s-tapcomp", util.RandomString(7))

	if err := t.Create(mm.Headnode()); err != nil {
		return fmt.Errorf("setting up tap: %w", err)
	}

	var status scorchmd.ScorchStatus
	if err := this.options.Exp.Status.ParseAppStatus("scorch", &status); err != nil {
		return fmt.Errorf("getting experiment status for scorch app: %w", err)
	}

	status.Taps[this.options.Name] = t

	this.options.Exp.Status.SetAppStatus("scorch", status)
	this.options.Exp.WriteToStore(true)

	return nil
}

func (this Tap) Stop(ctx context.Context) error {
	exp := this.options.Exp.Metadata.Name

	var status scorchmd.ScorchStatus
	if err := this.options.Exp.Status.ParseAppStatus("scorch", &status); err != nil {
		return fmt.Errorf("getting experiment status for scorch app: %w", err)
	}

	t, ok := status.Taps[this.options.Name]
	if ok {
		t.Init(tap.Experiment(exp))

		if err := t.Delete(mm.Headnode()); err != nil {
			return fmt.Errorf("deleting host tap for VLAN %s: %w", t.VLAN, err)
		}
	}

	return nil
}

func (Tap) Cleanup(context.Context) error {
	return nil
}

func (Tap) discoverUsedPairs() []netaddr.IPPrefix {
	var pairs []netaddr.IPPrefix

	running, err := types.RunningExperiments()
	if err != nil {
		return nil
	}

	for _, exp := range running {
		var status scorchmd.ScorchStatus
		if err := exp.Status.ParseAppStatus("scorch", &status); err == nil {
			for _, tap := range status.Taps {
				if pair, err := netaddr.ParseIPPrefix(tap.Subnet); err == nil {
					pairs = append(pairs, pair)
				}
			}
		}
	}

	return pairs
}
