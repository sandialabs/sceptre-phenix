package vlan

import (
	"cmp"
	"errors"
	"maps"
	"slices"
	"strings"
)

// ErrNoFreeBlock is returned by FindFree when no block of free IDs fits.
var ErrNoFreeBlock = errors.New("no block of free VLAN IDs")

// Query filters a report. Zero fields filter nothing.
type Query struct {
	Experiment string
	VLAN       int
	State      string
	IssuesOnly bool
}

// Filter returns the report with only the rows that q matches, and the
// experiments and issues of q's experiment and VLAN ID. An issue is of the ID
// when it covers the ID or a matched row carries it.
func Filter(rep Report, q Query) Report {
	out := rep
	out.VLANs, out.vms = []Row{}, nil
	out.Experiments, out.Issues = []ExperimentSummary{}, []Issue{}

	for i, r := range rep.VLANs {
		switch {
		case q.Experiment != "" && r.Experiment != q.Experiment && r.Namespace != q.Experiment:
		case q.VLAN != 0 && r.ID != q.VLAN:
		case q.State != "" && !slices.Contains(r.States, q.State):
		case q.IssuesOnly && len(r.Issues) == 0:
		default:
			out.VLANs = append(out.VLANs, r)
			out.vms = append(out.vms, rep.vms[i])
		}
	}

	for _, e := range rep.Experiments {
		if q.Experiment == "" || e.Name == q.Experiment {
			out.Experiments = append(out.Experiments, e)
		}
	}

	for _, i := range rep.Issues {
		if q.Experiment != "" && subject(i) != q.Experiment {
			continue
		}

		if q.VLAN != 0 && !issueCovers(i, q.VLAN) && !slices.ContainsFunc(out.VLANs, carries(i)) {
			continue
		}

		out.Issues = append(out.Issues, i)
	}

	return out
}

// subject is the experiment, or for an unowned namespace the namespace, that
// an issue is about.
func subject(i Issue) string {
	if len(i.Experiments) > 0 {
		return i.Experiments[0]
	}

	return i.Namespace
}

// issueCovers reports whether an issue is about the VLAN ID: its own ID, or
// one in its range.
func issueCovers(i Issue, id int) bool {
	switch {
	case i.VLAN != nil:
		return *i.VLAN == id
	case i.Range != nil:
		return id >= i.Range[0] && id <= i.Range[1]
	default:
		return false
	}
}

// carries returns whether a row carries the issue: the row has its kind, its
// subject as owner and, when the issue names one, its alias.
func carries(i Issue) func(Row) bool {
	return func(r Row) bool {
		owner := r.Namespace
		if r.Owned {
			owner = r.Experiment
		}

		return slices.Contains(r.Issues, i.Kind) && owner == subject(i) && (i.Alias == "" || i.Alias == r.Alias)
	}
}

// VMsOn lists the VM interfaces on the VLAN ID of the report's rows, sorted
// by experiment, VM and interface. An unowned namespace's VMs name the
// namespace as their experiment.
func VMsOn(rep Report, id int) []VM {
	vms := []VM{}

	for i, r := range rep.VLANs {
		if r.ID != id {
			continue
		}

		for _, v := range rep.vms[i] {
			if v.VLAN != 0 && v.VLAN != id {
				continue
			}

			vm := VM{Experiment: cmp.Or(r.Experiment, r.Namespace), VM: v.VM, Host: v.Host, Interface: v.Interface}
			if !slices.Contains(vms, vm) {
				vms = append(vms, vm)
			}
		}
	}

	slices.SortFunc(vms, func(a, b VM) int {
		return cmp.Or(strings.Compare(a.Experiment, b.Experiment), strings.Compare(a.VM, b.VM),
			strings.Compare(a.Interface, b.Interface))
	})

	return vms
}

// Used lists every VLAN ID in use in the cluster, whoever owns it: pins, live
// allocations and the blacklist.
func Used(in Inputs) map[int]bool {
	used := map[int]bool{}

	for _, e := range in.Experiments {
		for _, id := range e.Pins {
			if id != 0 {
				used[id] = true
			}
		}
	}

	if in.Minimega.OK {
		for _, ns := range in.Minimega.Namespaces {
			for _, id := range ns.Aliases {
				used[id] = true
			}
		}

		for _, id := range in.Minimega.Blacklist {
			used[id] = true
		}
	}

	return used
}

// FindFree returns the first block of count free IDs in a row within lo–hi.
// With outside set, the block also stays outside every experiment's range.
func FindFree(in Inputs, count, lo, hi int, outside bool) (int, int, error) {
	used := Used(in)

	if outside {
		for _, e := range in.Experiments {
			if elo, ehi, ok := bounds(e); ok {
				// only lo–hi matters, and a stored range can be past 4094
				for id := max(elo, lo); id <= min(ehi, hi); id++ {
					used[id] = true
				}
			}
		}
	}

	run := 0

	for id := lo; id <= hi; id++ {
		if used[id] {
			run = 0

			continue
		}

		run++

		if run == count {
			return id - count + 1, id, nil
		}
	}

	return 0, 0, ErrNoFreeBlock
}

// Proposal is a spec change to check: an experiment's pins and range.
type Proposal struct {
	Experiment string         `json:"experiment"`
	Aliases    map[string]int `json:"aliases"`
	Min        int            `json:"min"`
	Max        int            `json:"max"`
}

// Check returns the issues that the proposed spec would have in the cluster,
// as view sees them: duplicate pins, blacklisted pins, range overlaps and pins
// outside the range. The proposal replaces the experiment's pins and range.
func Check(in Inputs, view View, p Proposal) []Issue {
	proposed := ExperimentInput{
		Name: p.Experiment, Running: false, Min: p.Min, Max: p.Max, Pins: maps.Clone(p.Aliases), VMs: nil,
	}

	if proposed.Pins == nil {
		proposed.Pins = map[string]int{}
	}

	exps := make([]ExperimentInput, 0, len(in.Experiments)+1)

	for _, e := range in.Experiments {
		if e.Name == p.Experiment {
			proposed.Running, proposed.VMs = e.Running, e.VMs

			continue
		}

		exps = append(exps, e)
	}

	exps = append(exps, proposed)
	checked := in
	checked.Experiments = exps

	// the proposal's own view: it may see itself
	own := View{
		Visible: func(exp string) bool { return exp == p.Experiment || (view.Visible != nil && view.Visible(exp)) },
		All:     view.All,
	}

	issues := []Issue{}

	for _, i := range Merge(checked, own).Issues {
		switch i.Kind {
		case KindDuplicatePin, KindPinBlacklisted, KindRangeOverlap, KindOutOfRange:
			if subject(i) == p.Experiment {
				issues = append(issues, i)
			}
		}
	}

	return issues
}
