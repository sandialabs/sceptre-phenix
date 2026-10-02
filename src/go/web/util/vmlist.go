package util

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/web/proto"
	"phenix/web/rbac"
)

// MaxPageValue bounds page numbers and sizes so paging math cannot overflow.
const MaxPageValue = math.MaxInt32

var ErrInvalidPage = errors.New("invalid pageNum or perPage")

// VMQuery is how a client narrows, orders and pages an experiment's VMs.
type VMQuery struct {
	Filter  string
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

		if role.Allowed("vms", "list", fmt.Sprintf("%s/%s", expName, vm.Name)) {
			selected = append(selected, vm)
		}
	}

	switch q.SortCol {
	case "":
	case "delayed":
		sortByDelay(selected, IndexTopology(topo), q.SortAsc)
	default:
		selected.SortBy(q.SortCol, q.SortAsc)
	}

	total := len(selected)

	if q.Page > 0 && q.Size > 0 {
		selected = selected.Paginate(q.Page, q.Size)
	}

	return selected, total
}

// VMListToProtobuf converts a page of an experiment's VMs; total is the count
// before paging.
func VMListToProtobuf(expName string, vms mm.VMs, total int, topo ifaces.TopologySpec) *proto.VMList {
	list := &proto.VMList{
		Total: uint32(total), //nolint:gosec // integer overflow conversion int -> uint32
		Vms:   make([]*proto.VM, len(vms)),
	}
	nodes := IndexTopology(topo)

	for i, v := range vms {
		list.Vms[i] = VMToProtobufIndexed(expName, v, nodes)
	}

	return list
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

func delayLess(a, b string) bool {
	ta, aTimer := strings.CutPrefix(a, "timer:")
	tb, bTimer := strings.CutPrefix(b, "timer:")

	if aTimer && bTimer {
		da, errA := time.ParseDuration(ta)
		db, errB := time.ParseDuration(tb)

		if errA == nil && errB == nil {
			return da < db
		}
	}

	return a < b
}
