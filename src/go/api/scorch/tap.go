package scorch

import (
	"context"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"inet.af/netaddr"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/types"
	"phenix/util"
	"phenix/util/tap"
)

const tapCompSuffixLen = 7

type Tap struct {
	options Options
}

func (t *Tap) Init(opts ...Option) error {
	t.options = NewOptions(opts...)

	return nil
}

func (Tap) Type() string {
	return "tap"
}

func (Tap) Configure(context.Context) error {
	return nil
}

func (t Tap) Start(ctx context.Context) error {
	var tp *tap.Tap

	err := mapstructure.Decode(t.options.Meta, &tp)
	if err != nil {
		return fmt.Errorf("decoding tap component metadata: %w", err)
	}

	// backwards compatibility (doesn't support external access firewall rules)
	if v, ok := tp.Other["internetAccess"]; ok {
		enabled, _ := v.(bool)
		tp.External.Enabled = enabled
	}

	// tap names cannot be longer than 15 characters
	// (dictated by max length of Linux interface names)
	tp.Name = util.RandomString(tapCompSuffixLen) + "-tapcomp"

	return createOwnedTap(ctx, t.options, tp)
}

func (t Tap) Stop(ctx context.Context) error {
	return deleteOwnedTap(ctx, t.options)
}

func (Tap) Cleanup(context.Context) error {
	return nil
}

func discoverUsedPairs() ([]netaddr.IPPrefix, error) {
	var pairs []netaddr.IPPrefix

	running, err := types.Experiments(true)
	if err != nil {
		return nil, err
	}

	for _, exp := range running {
		var scorch scorchmd.ScorchStatus

		err = exp.Status.ParseAppStatus("scorch", &scorch)
		if err == nil {
			for _, execution := range scorch.Executions {
				for _, tp := range execution.Taps {
					if pair, err := netaddr.ParseIPPrefix(tp.Subnet); err == nil {
						pairs = append(pairs, pair)
					}
				}
			}
			for _, tap := range scorch.Taps {
				if pair, parseErr := netaddr.ParseIPPrefix(tap.Subnet); parseErr == nil {
					pairs = append(pairs, pair)
				}
			}
		}

		var tap app.TapAppStatus

		err = exp.Status.ParseAppStatus("tap", &tap)
		if err == nil {
			for _, tap := range tap.Taps {
				if pair, parseErr := netaddr.ParseIPPrefix(tap.Subnet); parseErr == nil {
					pairs = append(pairs, pair)
				}
			}
		}
	}

	return pairs, nil
}
