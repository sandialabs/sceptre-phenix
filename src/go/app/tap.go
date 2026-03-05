package app

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/hashicorp/go-multierror"
	"inet.af/netaddr"

	"phenix/types"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/tap"
)

const tapNameRandomLength = 8

func init() { //nolint:gochecknoinits // app registration
	err := RegisterUserApp("tap", func() App { return new(Tap) })
	if err != nil {
		panic(err)
	}
}

type TapAppMetadata struct {
	Taps []*tap.Tap `mapstructure:"taps"`
}

type TapAppStatus struct {
	Host string     `mapstructure:"host" structs:"host"`
	Taps []*tap.Tap `mapstructure:"taps" structs:"taps"`
}

type Tap struct{}

func (Tap) Init(...Option) error {
	return nil
}

func (Tap) Name() string {
	return "tap"
}

func (Tap) Configure(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (Tap) PreStart(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (t *Tap) PostStart(ctx context.Context, exp *types.Experiment) error {
	app := exp.App(t.Name())
	if app == nil {
		// this should never happen...
		return fmt.Errorf("%s app not defined in experiment scenario", t.Name())
	}

	var amd TapAppMetadata
	if err := app.ParseMetadata(&amd); err != nil {
		return fmt.Errorf("decoding %s app metadata: %w", t.Name(), err)
	}

	hosts, err := mm.GetNamespaceHosts(exp.Metadata.Name)
	if err != nil {
		return fmt.Errorf("getting list of experiment hosts: %w", err)
	}

	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1)) //nolint:gosec // weak random number generator

	var (
		host  = hosts[rng.IntN(len(hosts))].Name
		pairs = t.discoverUsedPairs()
		vlans []string
	)

	status := TapAppStatus{Host: host} //nolint:exhaustruct // partial initialization

	for _, t := range amd.Taps {
		if slices.Contains(vlans, t.VLAN) {
			return fmt.Errorf("tap already created for VLAN %s", t.VLAN)
		}

		opts := []tap.Option{tap.Experiment(exp.Metadata.Name), tap.UsedPairs(pairs)}

		if subnet, err := netaddr.ParseIPPrefix(t.Subnet); err == nil {
			opts = append(opts, tap.PairSubnet(subnet))
		}

		t.Init(exp.Spec.DefaultBridge(), opts...)

		// Tap name is random, yet descriptive to the fact that it's a "tapapp" tap.
		t.Name = util.RandomString(tapNameRandomLength) + "-tapapp"

		pair, err := t.Create(host)
		if err != nil {
			return fmt.Errorf("creating host tap for VLAN %s: %w", t.VLAN, err)
		}

		if !pair.IsZero() {
			// Include pair just created for this tap to list of used pairs in case
			// more than one tap is being created for this experiment.
			pairs = append(pairs, pair)
		}

		status.Taps = append(status.Taps, t)
		vlans = append(vlans, t.VLAN)
	}

	exp.Status.SetAppStatus(t.Name(), status)

	return nil
}

func (Tap) Running(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (t *Tap) Cleanup(ctx context.Context, exp *types.Experiment) error {
	var status TapAppStatus

	err := exp.Status.ParseAppStatus(t.Name(), &status)
	if err != nil {
		return fmt.Errorf("getting experiment status for %s app: %w", t.Name(), err)
	}

	var (
		host = status.Host
		errs error
	)

	for _, t := range status.Taps {
		t.Init(exp.Spec.DefaultBridge(), tap.Experiment(exp.Metadata.Name))

		err := t.Delete(host)
		if err != nil {
			errs = multierror.Append(
				errs,
				fmt.Errorf("deleting host tap for VLAN %s: %w", t.VLAN, err),
			)
		}
	}

	return errs
}

func (t Tap) discoverUsedPairs() []netaddr.IPPrefix {
	var pairs []netaddr.IPPrefix

	running, err := types.Experiments(true)
	if err != nil {
		return nil
	}

	for _, exp := range running {
		var status TapAppStatus

		err := exp.Status.ParseAppStatus(t.Name(), &status)
		if err == nil {
			for _, tap := range status.Taps {
				if pair, err := netaddr.ParseIPPrefix(tap.Subnet); err == nil {
					pairs = append(pairs, pair)
				}
			}
		}
	}

	return pairs
}
