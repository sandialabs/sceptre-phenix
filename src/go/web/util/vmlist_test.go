package util

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/web/rbac"
)

func TestParsePage(t *testing.T) {
	maxValue := strconv.Itoa(MaxPageValue)

	tests := []struct {
		name             string
		pageNum, perPage string
		page, size       int
		err              error
	}{
		{"asks for every item without paging", "", "", 0, 0, nil},
		{"asks for every item without a page size", "2", "", 0, 0, nil},
		{"asks for every item without a page", "", "10", 0, 0, nil},
		{"reads a page and size", "2", "10", 2, 10, nil},
		{"accepts the largest page and size", maxValue, maxValue, MaxPageValue, MaxPageValue, nil},
		{"rejects page 0", "0", "10", 0, 0, ErrInvalidPage},
		{"rejects size 0", "1", "0", 0, 0, ErrInvalidPage},
		{"rejects a negative page", "-1", "10", 0, 0, ErrInvalidPage},
		{"rejects a negative size", "1", "-10", 0, 0, ErrInvalidPage},
		{"rejects a non-numeric page", "x", "10", 0, 0, ErrInvalidPage},
		{"rejects a non-numeric size", "1", "ten", 0, 0, ErrInvalidPage},
		{"rejects a fractional size", "1", "2.5", 0, 0, ErrInvalidPage},
		{"rejects a page past the largest", "2147483648", "10", 0, 0, ErrInvalidPage},
		{"rejects a size past the largest", "1", "4294967296", 0, 0, ErrInvalidPage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, size, err := ParsePage(tc.pageNum, tc.perPage)
			if page != tc.page || size != tc.size || !errors.Is(err, tc.err) {
				t.Errorf("ParsePage(%q, %q) = %d, %d, %v; want %d, %d, %v",
					tc.pageNum, tc.perPage, page, size, err, tc.page, tc.size, tc.err)
			}
		})
	}
}

// listRole may list every VM of experiment exp but hidden.
func listRole() rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"vms"}, ResourceNames: []string{"exp/*", "!exp/hidden"}, Verbs: []string{"list"}},
	}}}
}

func TestSelectVMs(t *testing.T) {
	vms := []mm.VM{
		{Name: "c", Host: "h1"},
		{Name: "a", Host: "h2"},
		{Name: "dnb", DoNotBoot: true},
		{Name: "hidden"},
		{Name: "b", Host: "h1"},
	}

	tests := []struct {
		name  string
		query VMQuery
		want  []string
		total int
	}{
		{"everything allowed", VMQuery{SortAsc: true}, []string{"c", "a", "b"}, 3},
		{"do-not-boot shown", VMQuery{ShowDNB: true}, []string{"c", "a", "dnb", "b"}, 4},
		{"sorted", VMQuery{SortCol: "name", SortAsc: false}, []string{"c", "b", "a"}, 3},
		// the total counts every match, not just the page
		{"paged", VMQuery{SortCol: "name", SortAsc: true, Page: 2, Size: 2}, []string{"c"}, 3},
		{"past the last page", VMQuery{Page: 3, Size: 2}, []string{}, 3},
		{"filtered", VMQuery{Filter: "h1", SortCol: "name", SortAsc: true}, []string{"b", "c"}, 2},
		{"unparsable filter matches nothing", VMQuery{Filter: "(("}, []string{}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, total := SelectVMs("exp", vms, nil, tc.query, listRole())

			if diff := cmp.Diff(tc.want, vmNames(page)); diff != "" {
				t.Errorf("SelectVMs(exp, %+v) mismatch (-want +got):\n%s", tc.query, diff)
			}

			if total != tc.total {
				t.Errorf("SelectVMs(exp, %+v) total = %d, want %d", tc.query, total, tc.total)
			}
		})
	}
}

// Sorting by delay puts VMs with no pending delay first, in their own order,
// then orders delays by their text, except that two timers compare by duration.
func TestSortByDelay(t *testing.T) {
	topo := &v1.TopologySpec{NodesF: []*v1.Node{
		{GeneralF: &v1.General{HostnameF: "ten"}, DelayF: &v1.Delay{TimerF: "10m"}},
		{GeneralF: &v1.General{HostnameF: "nine"}, DelayF: &v1.Delay{TimerF: "9m"}},
		{GeneralF: &v1.General{HostnameF: "user"}, DelayF: &v1.Delay{UserF: true}},
		{GeneralF: &v1.General{HostnameF: "cc"}, DelayF: &v1.Delay{C2F: []v1.C2Delay{{HostnameF: "ten"}}}},
		{GeneralF: &v1.General{HostnameF: "started"}, DelayF: &v1.Delay{TimerF: "1s"}},
		{GeneralF: &v1.General{HostnameF: "none"}},
	}}

	vms := []mm.VM{
		{Name: "user", State: "BUILDING"},
		{Name: "ten", State: "BUILDING"},
		// a running VM has already started, whatever its delay
		{Name: "started", State: "RUNNING"},
		{Name: "nine", State: "BUILDING"},
		{Name: "cc", State: "BUILDING"},
		{Name: "none", State: "BUILDING"},
		{Name: "no-node", State: "BUILDING"},
	}

	tests := []struct {
		name string
		asc  bool
		want []string
	}{
		{"ascending", true, []string{"started", "none", "no-node", "cc", "nine", "ten", "user"}},
		// VMs with no delay keep their order at either end
		{"descending", false, []string{"user", "ten", "nine", "cc", "started", "none", "no-node"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			query := VMQuery{SortCol: "delayed", SortAsc: tc.asc}

			page, _ := SelectVMs("exp", vms, topo, query, listRole())
			if diff := cmp.Diff(tc.want, vmNames(page)); diff != "" {
				t.Errorf("SelectVMs(exp, %+v) mismatch (-want +got):\n%s", query, diff)
			}
		})
	}
}

// genVMListVMs draws an experiment's VMs, some hidden from listRole or not booting.
func genVMListVMs() *rapid.Generator[[]mm.VM] {
	vm := rapid.Custom(func(t *rapid.T) mm.VM {
		return mm.VM{
			Name:      rapid.OneOf(rapid.StringMatching(`[a-z]{1,4}`), rapid.Just("hidden")).Draw(t, "name"),
			Host:      rapid.SampledFrom([]string{"h1", "h2", "H1"}).Draw(t, "host"),
			Uptime:    float64(rapid.IntRange(0, 3).Draw(t, "uptime")),
			DoNotBoot: rapid.Bool().Draw(t, "dnb"),
		}
	})

	return rapid.SliceOfDistinct(vm, func(vm mm.VM) string { return vm.Name })
}

func TestSelectVMsPagesPartitionTheList(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		vms := genVMListVMs().Draw(t, "vms")
		query := VMQuery{
			ShowDNB: rapid.Bool().Draw(t, "showDNB"),
			SortCol: rapid.SampledFrom([]string{"", "name", "host", "uptime"}).Draw(t, "sortCol"),
			SortAsc: rapid.Bool().Draw(t, "sortAsc"),
			Size:    rapid.IntRange(1, 5).Draw(t, "size"),
		}

		all, total := SelectVMs("exp", vms, nil, query, listRole())
		if total != len(all) {
			t.Fatalf("SelectVMs(exp, %+v) total = %d, want %d", query, total, len(all))
		}

		// pages 1, 2, ... hold the unpaged list in order, then nothing
		var paged mm.VMs

		for query.Page = 1; query.Page <= total/query.Size+1; query.Page++ {
			page, pageTotal := SelectVMs("exp", vms, nil, query, listRole())

			if pageTotal != total || len(page) > query.Size {
				t.Fatalf("SelectVMs(exp, %+v) = %d VMs of %d, want at most %d of %d", query, len(page), pageTotal, query.Size, total)
			}

			paged = append(paged, page...)
		}

		if diff := cmp.Diff(vmNames(all), vmNames(paged), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("SelectVMs(exp, %+v) pages mismatch (-unpaged +paged):\n%s", query, diff)
		}
	})
}

// genVMListDelay draws the delays sortByDelay compares, with timers that parse.
func genVMListDelay() *rapid.Generator[string] {
	timer := rapid.Custom(func(t *rapid.T) string {
		n := rapid.IntRange(0, 120).Draw(t, "n")

		spellings := []string{fmt.Sprintf("%ds", n), fmt.Sprintf("%dm", n), (time.Duration(n) * time.Second).String()}

		return "timer:" + rapid.SampledFrom(spellings).Draw(t, "unit")
	})

	return rapid.OneOf(rapid.Just(""), rapid.Just("user"), rapid.Just("cc:ten"), rapid.Just("cc:nine"), timer)
}

// delayLess must be a strict weak order, or [sort.SliceStable] may misorder VMs.
func TestDelayLessIsStrictWeakOrder(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		d := []string{genVMListDelay().Draw(t, "a"), genVMListDelay().Draw(t, "b"), genVMListDelay().Draw(t, "c")}
		equiv := func(x, y string) bool { return !delayLess(x, y) && !delayLess(y, x) }

		for _, p := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
			a, b, c := d[p[0]], d[p[1]], d[p[2]]

			switch {
			case delayLess(a, a):
				t.Fatalf("delayLess(%q, %q) = true, want false", a, a)
			case delayLess(a, b) && delayLess(b, a):
				t.Fatalf("delayLess(%q, %q) and delayLess(%q, %q) are both true", a, b, b, a)
			case delayLess(a, b) && delayLess(b, c) && !delayLess(a, c):
				t.Fatalf("delayLess(%q, %q) = false, want true after %q < %q < %q", a, c, a, b, c)
			case equiv(a, b) && equiv(b, c) && !equiv(a, c):
				t.Fatalf("%q and %q compare equal to %q but not to each other", a, c, b)
			}
		}
	})
}

func vmNames(vms mm.VMs) []string {
	out := make([]string, len(vms))
	for i, vm := range vms {
		out[i] = vm.Name
	}

	return out
}
