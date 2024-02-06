package scorch

import (
	"context"
	"fmt"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
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

	pairs := discoverUsedPairs()
	t.Init(this.options.Exp.Spec.DefaultBridge(), tap.Experiment(exp), tap.UsedPairs(pairs))

	// backwards compatibility (doesn't support external access firewall rules)
	if v, ok := t.Other["internetAccess"]; ok {
		enabled, _ := v.(bool)
		t.External.Enabled = enabled
	}

	// tap names cannot be longer than 15 characters
	// (dictated by max length of Linux interface names)
	t.Name = fmt.Sprintf("%s-tapcomp", util.RandomString(7))

	if _, err := t.Create(mm.Headnode()); err != nil {
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
		t.Init(this.options.Exp.Spec.DefaultBridge(), tap.Experiment(exp))

		if err := t.Delete(mm.Headnode()); err != nil {
			return fmt.Errorf("deleting host tap for VLAN %s: %w", t.VLAN, err)
		}
	}

	return nil
}

func (Tap) Cleanup(context.Context) error {
	return nil
}

func discoverUsedPairs() []netaddr.IPPrefix {
	var pairs []netaddr.IPPrefix

	running, err := types.Experiments(true)
	if err != nil {
		return nil
	}

	for _, exp := range running {
		var scorch scorchmd.ScorchStatus
		if err := exp.Status.ParseAppStatus("scorch", &scorch); err == nil {
			for _, tap := range scorch.Taps {
				if pair, err := netaddr.ParseIPPrefix(tap.Subnet); err == nil {
					pairs = append(pairs, pair)
				}
			}
		}

		var tap app.TapAppStatus
		if err := exp.Status.ParseAppStatus("tap", &tap); err == nil {
			for _, tap := range tap.Taps {
				if pair, err := netaddr.ParseIPPrefix(tap.Subnet); err == nil {
					pairs = append(pairs, pair)
				}
			}
		}
	}

	return pairs
}
