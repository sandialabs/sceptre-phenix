package vlan

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/types"
	"phenix/util/freshness"
	"phenix/util/mm"
)

// minimegaTTL is how long a read of the minimega side is reused while the
// same experiments run (see Read).
const minimegaTTL = 5 * time.Second

// recent is the latest successful read of the minimega side, and the running
// experiments it was read for.
//
//nolint:gochecknoglobals // process-wide cache
var recent struct {
	mu      sync.Mutex
	key     string
	read    Minimega
	expires time.Time
}

// networkRegex matches a VM network as minimega lists it: `ALIAS (ID)`.
var networkRegex = regexp.MustCompile(`^(.*) \((\d+)\)$`)

// Forget drops the cached minimega side, so the next Read asks minimega
// again. Call it after a change to minimega's VLANs.
func Forget() {
	recent.mu.Lock()
	recent.expires = time.Time{}
	recent.mu.Unlock()
}

// Read reads both sides of the cluster's VLANs: every experiment from the
// store, and minimega's namespaces, aliases, VMs and blacklist. A minimega read
// is reused for up to 5 s while the same experiments run. It returns an error
// only when the store cannot list the experiments; a failed minimega read sets
// Minimega.OK to false.
func Read(ctx context.Context) (Inputs, freshness.Observation, error) {
	exps, err := experiment.List()
	if err != nil {
		return Inputs{}, freshness.Observation{}, fmt.Errorf("listing experiments: %w", err)
	}

	in := Inputs{Experiments: make([]ExperimentInput, 0, len(exps)), Minimega: Minimega{}} //nolint:exhaustruct // set below

	var running []string

	for _, exp := range exps {
		in.Experiments = append(in.Experiments, FromExperiment(exp))

		if exp.Running() {
			running = append(running, exp.Metadata.Name+"\x00"+exp.Status.StartTime())
		}
	}

	slices.Sort(running)
	key := strings.Join(running, "\x01")

	recent.mu.Lock()
	if recent.key == key && time.Now().Before(recent.expires) {
		in.Minimega = recent.read
		recent.mu.Unlock()

		return in, freshness.Observed(in.Minimega.Read), nil
	}
	recent.mu.Unlock()

	in.Minimega = readMinimega(ctx)

	if !in.Minimega.OK {
		obs := freshness.Observed(in.Minimega.Read)
		obs.Known, obs.Err = false, fmt.Errorf("%s", in.Minimega.Err) //nolint:err113 // minimega's own text

		return in, obs, nil
	}

	recent.mu.Lock()
	recent.key, recent.read, recent.expires = key, in.Minimega, time.Now().Add(minimegaTTL)
	recent.mu.Unlock()

	return in, freshness.Observed(in.Minimega.Read), nil
}

// FromExperiment is the phenix side of one experiment.
func FromExperiment(exp types.Experiment) ExperimentInput {
	e := ExperimentInput{
		Name: exp.Metadata.Name, Running: exp.Running(), Min: 0, Max: 0, Pins: map[string]int{}, VMs: nil,
	}

	if exp.Spec == nil {
		return e
	}

	if v := exp.Spec.VLANs(); v != nil {
		e.Min, e.Max = v.Min(), v.Max()
		e.Pins = maps.Clone(v.Aliases())
	}

	if topo := exp.Spec.Topology(); topo != nil {
		for _, node := range topo.Nodes() {
			if node.External() || node.General().Hostname() == "" {
				continue
			}

			for _, iface := range node.Network().Interfaces() {
				if iface.VLAN() == "" {
					continue
				}

				e.VMs = append(e.VMs, VMInterface{
					VM: node.General().Hostname(), Host: "", Interface: iface.Name(), Alias: iface.VLAN(), VLAN: 0,
				})
			}
		}
	}

	return e
}

// readMinimega reads minimega's side of the cluster.
func readMinimega(ctx context.Context) Minimega {
	read := Minimega{OK: false, Err: "", Read: time.Now(), Namespaces: map[string]Namespace{}, Blacklist: nil,
		BlacklistRemoval: false,
	}
	fail := func(err error) Minimega {
		return Minimega{
			OK: false, Err: err.Error(), Read: read.Read, Namespaces: nil, Blacklist: nil, BlacklistRemoval: false,
		}
	}

	namespaces, err := mm.GetNamespaces(mm.Context(ctx))
	if err != nil {
		return fail(err)
	}

	for _, n := range namespaces {
		aliases, err := mm.GetVLANs(mm.NS(n.Name), mm.Context(ctx))
		if err != nil {
			return fail(err)
		}

		ns := Namespace{Name: n.Name, Aliases: aliases, VMs: nil}

		if len(aliases) > 0 {
			vms, obs := mm.GetVMInfoObserved(mm.NS(n.Name), mm.Context(ctx))
			if obs.Err != nil {
				return fail(obs.Err)
			}

			ns.VMs = vmInterfaces(vms)
		}

		read.Namespaces[n.Name] = ns
	}

	if read.Blacklist, err = mm.GetVLANBlacklist(mm.Context(ctx)); err != nil {
		return fail(err)
	}

	if read.BlacklistRemoval, err = mm.VLANBlacklistRemovable(mm.Context(ctx)); err != nil {
		return fail(err)
	}

	read.OK = true

	return read
}

// vmInterfaces lists the interfaces of minimega's VMs that are on a VLAN.
func vmInterfaces(vms mm.VMs) []VMInterface {
	var out []VMInterface

	for _, vm := range vms {
		for i, network := range vm.Networks {
			alias, id := "", 0

			if match := networkRegex.FindStringSubmatch(network); match != nil {
				alias = match[1]
				id, _ = strconv.Atoi(match[2])
			} else if n, err := strconv.Atoi(network); err == nil {
				id = n
			}

			if id == 0 {
				continue
			}

			out = append(out, VMInterface{VM: vm.Name, Host: vm.Host, Interface: strconv.Itoa(i), Alias: alias, VLAN: id})
		}
	}

	return out
}
