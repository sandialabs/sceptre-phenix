package util

import (
	"errors"
	"slices"
	"strconv"
	"testing"

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

			if got := vmNames(page); !slices.Equal(got, tc.want) || total != tc.total {
				t.Fatalf("SelectVMs() = %v, %d; want %v, %d", got, total, tc.want, tc.total)
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
			if got := vmNames(page); !slices.Equal(got, tc.want) {
				t.Fatalf("order = %v, want %v", got, tc.want)
			}
		})
	}
}

func vmNames(vms mm.VMs) []string {
	out := make([]string, len(vms))
	for i, vm := range vms {
		out[i] = vm.Name
	}

	return out
}
