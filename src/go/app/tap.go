package app

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"phenix/types"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/tap"

	"github.com/hashicorp/go-multierror"
	"golang.org/x/exp/slices"
	"inet.af/netaddr"
)

func init() {
	RegisterUserApp("tap", func() App { return new(Tap) })
}

type TapAppMetadata struct {
	Taps []*tap.Tap `mapstructure:"taps"`
}

type TapAppStatus struct {
	Host string     `structs:"host" mapstructure:"host"`
	Taps []*tap.Tap `structs:"taps" mapstructure:"taps"`
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

func (this *Tap) PostStart(ctx context.Context, exp *types.Experiment) error {
	app := exp.App(this.Name())
	if app == nil {
		// this should never happen...
		return fmt.Errorf("%s app not defined in experiment scenario", this.Name())
	}

	var amd TapAppMetadata
	if err := app.ParseMetadata(&amd); err != nil {
		return fmt.Errorf("decoding %s app metadata: %w", this.Name(), err)
	}

	hosts, err := mm.GetClusterHosts(true)
	if err != nil {
		return fmt.Errorf("getting list of cluster hosts: %w", err)
	}

	rand.Seed(time.Now().UnixNano())

	var (
		host  = hosts[rand.Intn(len(hosts))].Name
		pairs = this.discoverUsedPairs()
		vlans []string
	)

	status := TapAppStatus{Host: host}

	for _, t := range amd.Taps {
		if slices.Contains(vlans, t.VLAN) {
			return fmt.Errorf("tap already created for VLAN %s", t.VLAN)
		}

		opts := []tap.Option{tap.Experiment(exp.Metadata.Name), tap.UsedPairs(pairs)}

		if subnet, err := netaddr.ParseIPPrefix(t.Subnet); err == nil {
			opts = append(opts, tap.PairSubnet(subnet))
		}

		t.Init(opts...)

		// Tap name is random, yet descriptive to the fact that it's a "tapapp" tap.
		t.Name = fmt.Sprintf("%s-tapapp", util.RandomString(8))

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

	exp.Status.SetAppStatus(this.Name(), status)

	return nil
}

func (Tap) Running(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (this *Tap) Cleanup(ctx context.Context, exp *types.Experiment) error {
	var status TapAppStatus
	if err := exp.Status.ParseAppStatus(this.Name(), &status); err != nil {
		return fmt.Errorf("getting experiment status for %s app: %w", this.Name(), err)
	}

	var (
		host = status.Host
		errs error
	)

	for _, t := range status.Taps {
		t.Init(tap.Experiment(exp.Metadata.Name))

		if err := t.Delete(host); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("deleting host tap for VLAN %s: %w", t.VLAN, err))
		}
	}

	return errs
}

func (this Tap) discoverUsedPairs() []netaddr.IPPrefix {
	var pairs []netaddr.IPPrefix

	running, err := types.RunningExperiments()
	if err != nil {
		return nil
	}

	for _, exp := range running {
		var status TapAppStatus
		if err := exp.Status.ParseAppStatus(this.Name(), &status); err == nil {
			for _, tap := range status.Taps {
				if pair, err := netaddr.ParseIPPrefix(tap.Subnet); err == nil {
					pairs = append(pairs, pair)
				}
			}
		}
	}

	return pairs
}
