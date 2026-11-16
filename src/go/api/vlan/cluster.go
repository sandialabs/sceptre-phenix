package vlan

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// The lowest and highest VLAN IDs that phenix and minimega accept.
const (
	MinID = 1
	MaxID = 4094
)

// The states of a VLAN row.
const (
	StatePinned      = "pinned"
	StateAllocated   = "allocated"
	StateBlacklisted = "blacklisted"
)

// The kinds of VLAN issue, from most to least severe.
const (
	KindDuplicatePin   = "duplicate-pin"
	KindPinBlacklisted = "pin-blacklisted"
	KindUnowned        = "unowned"
	KindIDDiffers      = "id-differs"
	KindMinimegaOnly   = "minimega-only"
	KindRangeOverlap   = "range-overlap"
	KindOutOfRange     = "out-of-range"
)

// The severities of a VLAN issue.
const (
	SeverityConflict   = "conflict"
	SeverityDifference = "difference"
	SeverityWarning    = "warning"
)

// HiddenExperiment names an experiment that the viewer may not see.
const HiddenExperiment = "another experiment"

//nolint:gochecknoglobals // fixed order and severity of the issue kinds
var kindOrder = []string{
	KindDuplicatePin, KindPinBlacklisted, KindUnowned, KindIDDiffers, KindMinimegaOnly, KindRangeOverlap, KindOutOfRange,
}

// Severity returns the severity of an issue kind.
func Severity(kind string) string {
	switch kind {
	case KindDuplicatePin, KindPinBlacklisted, KindUnowned:
		return SeverityConflict
	case KindIDDiffers, KindMinimegaOnly:
		return SeverityDifference
	default:
		return SeverityWarning
	}
}

// Sources are the VLAN ID that each source gives a row's alias, nil for none.
type Sources struct {
	Phenix   *int `json:"phenix"`
	Minimega *int `json:"minimega"`
}

// Row is one VLAN ID of one owner and alias.
type Row struct {
	ID         int      `json:"id"`
	Alias      string   `json:"alias"`
	Experiment string   `json:"experiment"`
	Namespace  string   `json:"namespace"`
	Running    bool     `json:"running"`
	Owned      bool     `json:"owned"`
	States     []string `json:"states"`
	Sources    Sources  `json:"sources"`
	VMs        int      `json:"vms"`
	Hosts      []string `json:"hosts"`
	Issues     []string `json:"issues"`
}

// AliasSummary is one VLAN alias of an experiment: its pin and live ID (0 for
// none) and the count of VMs on it.
type AliasSummary struct {
	Alias  string `json:"alias"`
	Pinned int    `json:"pinned"`
	Live   int    `json:"live"`
	VMs    int    `json:"vms"`
}

// ExperimentSummary is the VLANs of one experiment. Min and Max are 0 when
// the experiment has no range.
type ExperimentSummary struct {
	Name    string         `json:"name"`
	Running bool           `json:"running"`
	Min     int            `json:"min"`
	Max     int            `json:"max"`
	Aliases []AliasSummary `json:"aliases"`
	Issues  []string       `json:"issues"`
}

// Issue is one difference or conflict, as its subject sees it: the first of
// Experiments, or Namespace for an unowned namespace.
type Issue struct {
	Kind        string   `json:"kind"`
	Severity    string   `json:"severity"`
	VLAN        *int     `json:"vlan"`
	Alias       string   `json:"alias"`
	Experiments []string `json:"experiments"`
	Namespace   string   `json:"namespace"`
	Range       *[2]int  `json:"range"`
	Message     string   `json:"message"`
}

// VMInterface is one VM interface on a VLAN alias. VLAN is 0 when only the
// topology gives the interface.
type VMInterface struct {
	VM        string
	Host      string
	Interface string
	Alias     string
	VLAN      int
}

// ExperimentInput is the phenix side of one experiment: its range, its pins (0 for
// an alias without one) and the VM interfaces of its topology.
type ExperimentInput struct {
	Name    string
	Running bool
	Min     int
	Max     int
	Pins    map[string]int
	VMs     []VMInterface
}

// Namespace is one minimega namespace: its live aliases and VM interfaces.
type Namespace struct {
	Name    string
	Aliases map[string]int
	VMs     []VMInterface
}

// Minimega is the minimega side of the cluster. When OK is false, Err says
// why and nothing else is known.
type Minimega struct {
	OK               bool
	Err              string
	Read             time.Time
	Namespaces       map[string]Namespace
	Blacklist        []int
	BlacklistRemoval bool
}

// Inputs are both sources of the cluster's VLANs.
type Inputs struct {
	Experiments []ExperimentInput
	Minimega    Minimega
}

// View is what a viewer may see: the experiments that Visible allows, and,
// when All is set, the blacklist and unowned namespaces. All does not make
// an experiment visible, because a role can allow "*" and refuse "!name".
type View struct {
	Visible func(experiment string) bool
	All     bool
}

// MinimegaStatus tells whether minimega answered, and when it was read.
type MinimegaStatus struct {
	OK               bool   `json:"ok"`
	Read             string `json:"read"`
	Error            string `json:"error"`
	BlacklistRemoval bool   `json:"blacklistRemoval"`
}

// Report is the merged VLANs of the cluster, as a view sees them.
type Report struct {
	VLANs       []Row               `json:"vlans"`
	Experiments []ExperimentSummary `json:"experiments"`
	Issues      []Issue             `json:"issues"`
	Blacklist   *[]int              `json:"blacklist,omitempty"`
	Minimega    MinimegaStatus      `json:"minimega"`

	// vms are the VM interfaces of each visible row, by row index
	vms [][]VMInterface
}

// VM is one VM interface on a VLAN ID.
type VM struct {
	Experiment string `json:"experiment"`
	VM         string `json:"vm"`
	Host       string `json:"host"`
	Interface  string `json:"interface"`
}

// row is a Row with what the merge needs to know of it.
type row struct {
	Row

	pinned, live bool
	vms          []VMInterface
	issues       map[string]bool
}

// owner names the experiment of the row, or its namespace when unowned.
func (r *row) owner() string {
	if r.Owned {
		return r.Experiment
	}

	return r.Namespace
}

// merger merges the inputs for a view.
type merger struct {
	in     Inputs
	view   View
	rows   []*row
	issues []Issue
	exps   map[string]ExperimentInput
}

// Merge merges the phenix and minimega sides of the cluster into rows,
// experiment summaries and issues, as view sees them. Hidden experiments are
// named HiddenExperiment. With minimega down, it gives the phenix side only.
func Merge(in Inputs, view View) Report {
	m := &merger{in: in, view: view, rows: nil, issues: nil, exps: map[string]ExperimentInput{}}

	for _, e := range in.Experiments {
		m.exps[e.Name] = e
	}

	m.experimentRows()
	m.unownedRows()
	m.blacklistRows()
	m.duplicates()
	m.blacklisted()
	m.ranges()

	return m.report()
}

func intp(i int) *int { return &i }

// vmsOn returns the VMs on alias (VM names, unique) and their hosts, sorted.
func vmsOn(vms []VMInterface, alias string) ([]VMInterface, int, []string) {
	var (
		on    []VMInterface
		names = map[string]bool{}
		hosts = map[string]bool{}
	)

	for _, v := range vms {
		if v.Alias != alias {
			continue
		}

		on = append(on, v)
		names[v.VM] = true

		if v.Host != "" {
			hosts[v.Host] = true
		}
	}

	sorted := slices.Sorted(maps.Keys(hosts))
	if sorted == nil {
		sorted = []string{}
	}

	return on, len(names), sorted
}

// named gives minimega's VM interfaces the names that the topology gives
// the interface of each VM on the same alias.
func named(live, topology []VMInterface) []VMInterface {
	names := map[[2]string]string{}
	for _, v := range topology {
		names[[2]string{v.VM, v.Alias}] = v.Interface
	}

	out := slices.Clone(live)
	for i, v := range out {
		if name, ok := names[[2]string{v.VM, v.Alias}]; ok {
			out[i].Interface = name
		}
	}

	return out
}

// liveOf returns the minimega namespace of an experiment, if minimega answered
// and has it.
func (m *merger) liveOf(name string) (Namespace, bool) {
	if !m.in.Minimega.OK {
		return Namespace{Name: "", Aliases: nil, VMs: nil}, false
	}

	ns, ok := m.in.Minimega.Namespaces[name]

	return ns, ok
}

func (m *merger) add(r *row, kinds ...string) {
	r.issues = map[string]bool{}

	for _, k := range kinds {
		r.issues[k] = true
	}

	m.rows = append(m.rows, r)
}

func (m *merger) experimentRows() {
	for _, e := range sortedExperiments(m.in.Experiments) {
		ns, hasNS := m.liveOf(e.Name)
		compare := e.Running && hasNS

		vms := e.VMs
		if hasNS {
			vms = named(ns.VMs, e.VMs)
		}

		namespace := ""
		if hasNS {
			namespace = e.Name
		}

		aliases := map[string]bool{}
		for a := range e.Pins {
			aliases[a] = true
		}

		for a := range ns.Aliases {
			aliases[a] = true
		}

		for _, alias := range slices.Sorted(maps.Keys(aliases)) {
			pin, inSpec := e.Pins[alias]
			live := ns.Aliases[alias]
			on, count, hosts := vmsOn(vms, alias)

			base := Row{
				ID: 0, Alias: alias, Experiment: e.Name, Namespace: namespace, Running: e.Running, Owned: true,
				States: nil, Sources: Sources{Phenix: nil, Minimega: nil}, VMs: count, Hosts: hosts, Issues: nil,
			}

			if pin != 0 {
				base.Sources.Phenix = intp(pin)
			}

			if live != 0 {
				base.Sources.Minimega = intp(live)
			}

			newRow := func(id int, pinned, isLive bool) *row {
				r := &row{Row: base, pinned: pinned, live: isLive, vms: on, issues: nil}
				r.ID = id

				if pinned {
					r.States = append(r.States, StatePinned)
				}

				if isLive {
					r.States = append(r.States, StateAllocated)
				}

				return r
			}

			switch {
			case pin != 0 && live == pin:
				m.add(newRow(pin, true, true))
			case pin != 0 && live != 0:
				var kinds []string
				if compare {
					kinds = []string{KindIDDiffers}
					m.issue(KindIDDiffers, intp(pin), alias, []string{e.Name}, "", nil,
						fmt.Sprintf("phēnix pins %d. minimega allocated %d.", pin, live))
				}

				m.add(newRow(pin, true, false), kinds...)
				m.add(newRow(live, false, true), kinds...)
			case pin != 0:
				m.add(newRow(pin, true, false))
			case live != 0:
				var kinds []string
				if compare && !inSpec {
					kinds = []string{KindMinimegaOnly}
					m.issue(KindMinimegaOnly, intp(live), alias, []string{e.Name}, "", nil, "Not in the experiment spec.")
				}

				m.add(newRow(live, false, true), kinds...)
			}
		}
	}
}

func (m *merger) unownedRows() {
	if !m.in.Minimega.OK {
		return
	}

	for _, name := range slices.Sorted(maps.Keys(m.in.Minimega.Namespaces)) {
		ns := m.in.Minimega.Namespaces[name]
		if _, owned := m.exps[name]; owned || len(ns.Aliases) == 0 {
			continue
		}

		m.issue(KindUnowned, nil, "", []string{}, name, nil, fmt.Sprintf("No experiment has the namespace %s.", name))

		for _, alias := range slices.Sorted(maps.Keys(ns.Aliases)) {
			id := ns.Aliases[alias]
			on, count, hosts := vmsOn(ns.VMs, alias)

			m.add(&row{
				Row: Row{
					ID: id, Alias: alias, Experiment: "", Namespace: name, Running: false, Owned: false,
					States: []string{StateAllocated}, Sources: Sources{Phenix: nil, Minimega: intp(id)},
					VMs: count, Hosts: hosts, Issues: nil,
				},
				pinned: false, live: true, vms: on, issues: nil,
			}, KindUnowned)
		}
	}
}

func (m *merger) blacklistRows() {
	if !m.in.Minimega.OK {
		return
	}

	for _, id := range m.in.Minimega.Blacklist {
		m.add(&row{
			Row: Row{
				ID: id, Alias: "", Experiment: "", Namespace: "", Running: false, Owned: false,
				States: []string{StateBlacklisted}, Sources: Sources{Phenix: nil, Minimega: intp(id)},
				VMs: 0, Hosts: []string{}, Issues: nil,
			},
			pinned: false, live: false, vms: nil, issues: nil,
		})
	}
}

// duplicates marks each row whose ID another owner pins or uses, where at
// least one of the two pins it.
func (m *merger) duplicates() {
	byID := map[int][]*row{}

	for _, r := range m.rows {
		if r.pinned || r.live {
			byID[r.ID] = append(byID[r.ID], r)
		}
	}

	for _, id := range slices.Sorted(maps.Keys(byID)) {
		claims := byID[id]

		for _, r := range claims {
			if !r.Owned {
				continue // the unowned issue covers it
			}

			var others []*row

			for _, o := range claims {
				if o.owner() != r.owner() && (r.pinned || o.pinned) {
					others = append(others, o)
				}
			}

			if len(others) == 0 {
				continue
			}

			r.issues[KindDuplicatePin] = true

			for _, o := range others {
				if !o.Owned {
					o.issues[KindDuplicatePin] = true
				}
			}

			m.duplicateIssue(r, others)
		}
	}
}

func (m *merger) duplicateIssue(r *row, others []*row) {
	var (
		experiments = []string{r.Experiment}
		names       []string
		namespace   string
		allPinned   = true
	)

	for _, o := range others {
		allPinned = allPinned && o.pinned

		var name string

		switch {
		case !o.Owned && m.view.All:
			namespace = o.Namespace
			name = "the namespace " + o.Namespace
		case !o.Owned || !m.visible(o.Experiment):
			name = HiddenExperiment
		default:
			name = fmt.Sprintf("%s (%s)", o.Experiment, o.Alias)
		}

		if o.Owned || !m.view.All {
			experiments = appendNew(experiments, m.name(o.Experiment))
		}

		names = appendNew(names, name)
	}

	verb := "used"
	if allPinned {
		verb = "pinned"
	}

	m.issue(KindDuplicatePin, intp(r.ID), r.Alias, experiments, namespace, nil,
		fmt.Sprintf("Also %s by %s.", verb, joinNames(names)))
}

// appendNew appends s to list unless list holds it.
func appendNew(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}

	return append(list, s)
}

// joinNames joins names as "a", "a and b" or "a, b and c".
func joinNames(names []string) string {
	if len(names) <= 1 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func (m *merger) blacklisted() {
	if !m.in.Minimega.OK {
		return
	}

	for _, r := range m.rows {
		if !r.pinned || !slices.Contains(m.in.Minimega.Blacklist, r.ID) {
			continue
		}

		r.issues[KindPinBlacklisted] = true

		m.issue(KindPinBlacklisted, intp(r.ID), r.Alias, []string{r.Experiment}, "", nil,
			fmt.Sprintf("minimega blacklists %d.", r.ID))

		for _, b := range m.rows {
			if b.ID == r.ID && slices.Contains(b.States, StateBlacklisted) {
				b.issues[KindPinBlacklisted] = true
			}
		}
	}
}

// bounds returns the range of an experiment, and whether it has one: either
// bound set, the other taken as the lowest or highest ID.
func bounds(e ExperimentInput) (int, int, bool) {
	if e.Min == 0 && e.Max == 0 {
		return 0, 0, false
	}

	lo, hi := e.Min, e.Max
	if lo == 0 {
		lo = MinID
	}

	if hi == 0 {
		hi = MaxID
	}

	return lo, hi, true
}

func (m *merger) ranges() {
	exps := sortedExperiments(m.in.Experiments)

	for _, e := range exps {
		lo, hi, ok := bounds(e)
		if !ok {
			continue
		}

		for _, r := range m.rows {
			if r.Owned && r.Experiment == e.Name && r.pinned && (r.ID < lo || r.ID > hi) {
				r.issues[KindOutOfRange] = true

				m.issue(KindOutOfRange, intp(r.ID), r.Alias, []string{e.Name}, "", &[2]int{lo, hi},
					fmt.Sprintf("Outside the range %d–%d.", lo, hi))
			}
		}

		for _, o := range exps {
			olo, ohi, ok := bounds(o)
			if o.Name == e.Name || !ok || olo > hi || ohi < lo {
				continue
			}

			start, end := max(lo, olo), min(hi, ohi)

			m.issue(KindRangeOverlap, nil, "", []string{e.Name, m.name(o.Name)}, "", &[2]int{start, end},
				fmt.Sprintf("%d–%d overlaps %s.", start, end, m.name(o.Name)))
		}
	}
}

// name is an experiment's name as the view sees it.
func (m *merger) name(exp string) string {
	if m.visible(exp) {
		return exp
	}

	return HiddenExperiment
}

func (m *merger) visible(exp string) bool {
	return m.view.Visible != nil && m.view.Visible(exp)
}

// issue records an issue, named as the view sees it.
func (m *merger) issue(kind string, id *int, alias string, exps []string, ns string, rng *[2]int, msg string) {
	m.issues = append(m.issues, Issue{
		Kind: kind, Severity: Severity(kind), VLAN: id, Alias: alias, Experiments: exps, Namespace: ns,
		Range: rng, Message: msg,
	})
}

// seen reports whether the view sees an issue: one of a visible subject.
func (m *merger) seen(i Issue) bool {
	if len(i.Experiments) == 0 {
		return m.view.All
	}

	return m.visible(i.Experiments[0])
}

func (m *merger) seenRow(r *row) bool {
	if r.Owned {
		return m.visible(r.Experiment)
	}

	return m.view.All
}

func (m *merger) report() Report {
	rep := Report{
		VLANs: []Row{}, Experiments: []ExperimentSummary{}, Issues: []Issue{}, Blacklist: nil,
		Minimega: MinimegaStatus{
			OK: m.in.Minimega.OK, Read: formatTime(m.in.Minimega.Read), Error: m.in.Minimega.Err,
			BlacklistRemoval: m.in.Minimega.OK && m.in.Minimega.BlacklistRemoval,
		},
		vms: nil,
	}

	slices.SortStableFunc(m.rows, func(a, b *row) int {
		return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(a.Experiment, b.Experiment),
			cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Alias, b.Alias))
	})

	expKinds := map[string]map[string]bool{}

	for _, r := range m.rows {
		if !m.seenRow(r) {
			continue
		}

		r.Issues = sortedKinds(r.issues)
		rep.VLANs = append(rep.VLANs, r.Row)
		rep.vms = append(rep.vms, r.vms)
	}

	for _, i := range m.issues {
		if !m.seen(i) {
			continue
		}

		rep.Issues = append(rep.Issues, i)

		if len(i.Experiments) > 0 {
			if expKinds[i.Experiments[0]] == nil {
				expKinds[i.Experiments[0]] = map[string]bool{}
			}

			expKinds[i.Experiments[0]][i.Kind] = true
		}
	}

	slices.SortStableFunc(rep.Issues, compareIssues)

	for _, e := range sortedExperiments(m.in.Experiments) {
		if m.visible(e.Name) {
			rep.Experiments = append(rep.Experiments, m.summary(e, expKinds[e.Name]))
		}
	}

	if m.view.All && m.in.Minimega.OK {
		blacklist := slices.Clone(m.in.Minimega.Blacklist)
		if blacklist == nil {
			blacklist = []int{}
		}

		rep.Blacklist = &blacklist
	}

	return rep
}

func (m *merger) summary(e ExperimentInput, kinds map[string]bool) ExperimentSummary {
	ns, hasNS := m.liveOf(e.Name)

	vms := e.VMs
	if hasNS {
		vms = ns.VMs
	}

	aliases := map[string]bool{}
	for a := range e.Pins {
		aliases[a] = true
	}

	for a := range ns.Aliases {
		aliases[a] = true
	}

	summary := ExperimentSummary{
		Name: e.Name, Running: e.Running, Min: e.Min, Max: e.Max, Aliases: []AliasSummary{}, Issues: sortedKinds(kinds),
	}

	for _, alias := range slices.Sorted(maps.Keys(aliases)) {
		_, count, _ := vmsOn(vms, alias)
		summary.Aliases = append(summary.Aliases, AliasSummary{
			Alias: alias, Pinned: e.Pins[alias], Live: ns.Aliases[alias], VMs: count,
		})
	}

	return summary
}

func sortedExperiments(exps []ExperimentInput) []ExperimentInput {
	sorted := slices.Clone(exps)
	slices.SortFunc(sorted, func(a, b ExperimentInput) int { return strings.Compare(a.Name, b.Name) })

	return sorted
}

func sortedKinds(kinds map[string]bool) []string {
	out := []string{}

	for _, k := range kindOrder {
		if kinds[k] {
			out = append(out, k)
		}
	}

	return out
}

func compareIssues(a, b Issue) int {
	vlan := func(i Issue) int {
		if i.VLAN == nil {
			return MaxID + 1
		}

		return *i.VLAN
	}

	first := func(i Issue) string {
		if len(i.Experiments) == 0 {
			return i.Namespace
		}

		return i.Experiments[0]
	}

	return cmp.Or(
		cmp.Compare(slices.Index(kindOrder, a.Kind), slices.Index(kindOrder, b.Kind)),
		cmp.Compare(vlan(a), vlan(b)),
		cmp.Compare(first(a), first(b)),
		cmp.Compare(a.Alias, b.Alias),
		strings.Compare(a.Message, b.Message),
	)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339)
}
