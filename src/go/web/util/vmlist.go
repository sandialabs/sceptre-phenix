package util

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	ifaces "phenix/types/interfaces"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/web/proto"
	"phenix/web/rbac"
)

// MaxPageValue bounds page numbers and sizes so paging math cannot overflow.
const MaxPageValue = math.MaxInt32

var ErrInvalidPage = errors.New("invalid pageNum or perPage")

// labelsColumn is the sort column ordering VMs by their label count.
const labelsColumn = "labels"

// VMLabel is one key=value label a VM may carry.
type VMLabel struct {
	Key   string
	Value string
}

// VMQuery is how a client narrows, orders and pages an experiment's VMs.
type VMQuery struct {
	Filter string
	// Labels keeps the VMs carrying any of them; a label with no key matches
	// no VM, so a malformed one never widens the list.
	Labels  []VMLabel
	ShowDNB bool
	// SortCol is empty for the VMs' own order.
	SortCol string
	SortAsc bool
	// Page and Size are both at least 1 when a page was asked for, else 0.
	Page int
	Size int
}

// ParsePage reads the pageNum and perPage query parameters. Leaving either out
// asks for every item, returned as 0, 0; otherwise both must be whole numbers
// from 1 to MaxPageValue.
func ParsePage(pageNum, perPage string) (int, int, error) {
	if pageNum == "" || perPage == "" {
		return 0, 0, nil
	}

	n, nErr := strconv.Atoi(pageNum)
	s, sErr := strconv.Atoi(perPage)

	// Paginate panics on a page or size below 1, and overflows on huge ones.
	if nErr != nil || sErr != nil || n < 1 || s < 1 || n > MaxPageValue || s > MaxPageValue {
		return 0, 0, ErrInvalidPage
	}

	return n, s, nil
}

// Page returns the items on page of the given size, as ParsePage gives them:
// every item when page is 0, and none past the last page.
func Page[T any](items []T, page, size int) []T {
	if page < 1 || size < 1 {
		return items
	}

	start := (page - 1) * size
	if start >= len(items) {
		return []T{}
	}

	return items[start:min(start+size, len(items))]
}

// SelectVMs returns the page of the experiment's VMs that q asks for and role
// may list, and how many there are before paging.
func SelectVMs(
	expName string,
	vms []mm.VM,
	topo ifaces.TopologySpec,
	q VMQuery,
	role rbac.Role,
) (mm.VMs, int) {
	filterTree := mm.BuildTree(q.Filter)
	selected := mm.VMs{}

	for _, vm := range vms {
		if vm.DoNotBoot && !q.ShowDNB {
			continue
		}

		// a filter that could not be parsed matches nothing
		if q.Filter != "" && (filterTree == nil || !filterTree.Evaluate(&vm)) {
			continue
		}

		if len(q.Labels) > 0 && !hasAnyLabel(vm, q.Labels) {
			continue
		}

		if role.Allowed("vms", "list", fmt.Sprintf("%s/%s", expName, vm.Name)) {
			selected = append(selected, vm)
		}
	}

	switch q.SortCol {
	case "":
	case "delayed":
		sortByDelay(selected, IndexTopology(topo), q.SortAsc)
	case labelsColumn:
		sortByLabels(selected, q.SortAsc)
	default:
		selected.SortBy(q.SortCol, q.SortAsc)
	}

	total := len(selected)

	if q.Page > 0 && q.Size > 0 {
		selected = selected.Paginate(q.Page, q.Size)
	}

	return selected, total
}

// ParseVMLabel reads a key=value label, split at its first "=". It reports
// false for one without an "=" or with an empty key.
func ParseVMLabel(s string) (VMLabel, bool) {
	key, value, ok := strings.Cut(s, "=")
	if !ok || key == "" {
		return VMLabel{}, false
	}

	return VMLabel{Key: key, Value: value}, true
}

// isUserLabel reports whether a minimega tag is a label people set, rather
// than one phenix keeps for itself (its key starts with "__").
func isUserLabel(key string) bool {
	return !strings.HasPrefix(key, "__")
}

// labelCount is the number the VM table's Labels column shows for a VM: its
// labels and its notes.
func labelCount(vm mm.VM) int {
	n := 0

	for key := range vm.Tags {
		if isUserLabel(key) || strings.HasPrefix(key, "__notes_") {
			n++
		}
	}

	return n
}

func hasAnyLabel(vm mm.VM, labels []VMLabel) bool {
	for _, l := range labels {
		if v, ok := vm.Tags[l.Key]; ok && l.Key != "" && isUserLabel(l.Key) && v == l.Value {
			return true
		}
	}

	return false
}

// sortByLabels orders VMs by their label count, ties by name A to Z in either
// direction, so that VMs with as many labels read alphabetically.
func sortByLabels(vms mm.VMs, asc bool) {
	slices.SortStableFunc(vms, func(a, b mm.VM) int {
		byCount := cmp.Compare(labelCount(a), labelCount(b))
		if !asc {
			byCount = -byCount
		}

		return cmp.Or(
			byCount,
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.Name, b.Name),
		)
	})
}

// VMLabels returns the distinct labels on the VMs role may list (do-not-boot
// ones only with showDNB), ordered by key then value, to filter by.
func VMLabels(expName string, vms []mm.VM, showDNB bool, role rbac.Role) []VMLabel {
	seen := map[VMLabel]struct{}{}

	for _, vm := range vms {
		if vm.DoNotBoot && !showDNB {
			continue
		}

		if !role.Allowed("vms", "list", fmt.Sprintf("%s/%s", expName, vm.Name)) {
			continue
		}

		for key, value := range vm.Tags {
			if key != "" && isUserLabel(key) {
				seen[VMLabel{Key: key, Value: value}] = struct{}{}
			}
		}
	}

	labels := make([]VMLabel, 0, len(seen))
	for l := range seen {
		labels = append(labels, l)
	}

	slices.SortFunc(labels, func(a, b VMLabel) int {
		return cmp.Or(cmp.Compare(a.Key, b.Key), cmp.Compare(a.Value, b.Value))
	})

	return labels
}

// VMLabelsToProtobuf converts the labels VMLabels returns.
func VMLabelsToProtobuf(labels []VMLabel) []*proto.VMLabel {
	out := make([]*proto.VMLabel, len(labels))
	for i, l := range labels {
		out[i] = &proto.VMLabel{Key: l.Key, Value: l.Value}
	}

	return out
}

// VMListToProtobuf converts a page of an experiment's VMs; total is the count
// before paging.
func VMListToProtobuf(expName string, vms mm.VMs, total int, topo ifaces.TopologySpec) *proto.VMList {
	list := &proto.VMList{
		Total: uint32(total), //nolint:gosec // G115 counts fit in a uint32
		Vms:   make([]*proto.VM, len(vms)),
		// set by SetVMListObservation
		RuntimeUnknown: false,
		ObservedAt:     "",
		// only a bulk update fills it
		Errors: nil,
		// set by the lists that offer a label filter
		Labels: nil,
	}
	nodes := IndexTopology(topo)

	for i, v := range vms {
		list.Vms[i] = VMToProtobufIndexed(expName, v, nodes)
	}

	return list
}

// SetVMListObservation records in list how its VMs were read from minimega:
// runtime_unknown when minimega did not answer for every VM, and observed_at
// when it was read at all.
func SetVMListObservation(list *proto.VMList, obs freshness.Observation) {
	list.RuntimeUnknown = !obs.Known

	if !obs.At.IsZero() {
		list.ObservedAt = freshness.FormatTime(obs.At)
	}
}

// sortByDelay orders VMs by the delay holding back their start, as the UI's
// Delay column shows it: only for VMs still waiting to start. Timer delays
// compare by duration so "timer:9m" sorts before "timer:10m".
func sortByDelay(vms mm.VMs, nodes NodeIndex, asc bool) {
	delays := make(map[string]string, len(vms))

	for _, vm := range vms {
		if vm.State != "BUILDING" {
			continue
		}

		if node, ok := nodes[vm.Name]; ok {
			delays[vm.Name] = node.Delayed()
		}
	}

	sort.SliceStable(vms, func(i, j int) bool {
		a, b := delays[vms[i].Name], delays[vms[j].Name]
		if !asc {
			a, b = b, a
		}

		return delayLess(a, b)
	})
}

// delayLess orders delays by text, except that timers, which sort together,
// put those that parse first by duration and then the rest by text.
func delayLess(a, b string) bool {
	ta, aTimer := strings.CutPrefix(a, "timer:")
	tb, bTimer := strings.CutPrefix(b, "timer:")

	if !aTimer || !bTimer {
		return a < b
	}

	da, errA := time.ParseDuration(ta)
	db, errB := time.ParseDuration(tb)

	switch {
	case errA == nil && errB == nil:
		return da < db
	case (errA == nil) != (errB == nil):
		return errA == nil
	default:
		return a < b
	}
}
