package util

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
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
		{Name: "c", Host: "h1", Tags: map[string]string{"role": "web"}},
		{Name: "a", Host: "h2", Tags: map[string]string{"tier": "db", "__notes_1": "hi"}},
		{Name: "dnb", DoNotBoot: true, Tags: map[string]string{"role": "web"}},
		{Name: "hidden", Tags: map[string]string{"role": "web"}},
		{Name: "b", Host: "h1", Tags: map[string]string{"role": "app"}},
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
		{
			"any of the labels",
			VMQuery{Labels: []VMLabel{{"role", "web"}, {"tier", "db"}}, SortCol: "name", SortAsc: true},
			[]string{"a", "c"}, 2,
		},
		{"labels and filter both", VMQuery{Filter: "h1", Labels: []VMLabel{{"role", "web"}}}, []string{"c"}, 1},
		{"a label's value must match", VMQuery{Labels: []VMLabel{{"role", "db"}}}, []string{}, 0},
		{"a label with no key matches nothing", VMQuery{Labels: []VMLabel{{}}}, []string{}, 0},
		{"notes are not labels", VMQuery{Labels: []VMLabel{{"__notes_1", "hi"}}}, []string{}, 0},
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

// Sorting by labels counts each VM's labels and notes, as the Labels column
// shows them, and breaks ties by name A to Z in either direction.
func TestSortByLabels(t *testing.T) {
	tags := func(keys ...string) map[string]string {
		m := map[string]string{}
		for _, k := range keys {
			m[k] = "v"
		}

		return m
	}

	vms := []mm.VM{
		{Name: "b2", Tags: tags("x", "y")},
		{Name: "none"},
		{Name: "B1", Tags: tags("x", "__notes_1")},
		{Name: "a2", Tags: tags("x", "y", "__internal")},
		{Name: "one", Tags: tags("x")},
	}

	tests := []struct {
		name string
		asc  bool
		want []string
	}{
		{"ascending", true, []string{"none", "one", "a2", "B1", "b2"}},
		{"descending", false, []string{"a2", "B1", "b2", "one", "none"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			query := VMQuery{SortCol: "labels", SortAsc: tc.asc}

			page, _ := SelectVMs("exp", vms, nil, query, listRole())
			if diff := cmp.Diff(tc.want, vmNames(page)); diff != "" {
				t.Errorf("SelectVMs(exp, %+v) mismatch (-want +got):\n%s", query, diff)
			}
		})
	}
}

// genLabeledVMs draws VMs with distinct names and a few labels and notes each.
func genLabeledVMs() *rapid.Generator[[]mm.VM] {
	vm := rapid.Custom(func(t *rapid.T) mm.VM {
		keys := rapid.SliceOfDistinct(
			rapid.SampledFrom([]string{"a", "b", "c", "__notes_1", "__hidden"}),
			rapid.ID[string],
		).Draw(t, "keys")
		tags := map[string]string{}

		for _, k := range keys {
			tags[k] = rapid.SampledFrom([]string{"x", "y"}).Draw(t, "value")
		}

		return mm.VM{Name: rapid.StringMatching(`[a-cA-C]{1,3}`).Draw(t, "name"), Tags: tags}
	})

	return rapid.SliceOfDistinct(vm, func(vm mm.VM) string { return vm.Name })
}

// In either direction, the label sort orders VMs by count and then by name A
// to Z, and keeps every VM.
func TestSortByLabelsOrdersByCountThenName(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		vms := genLabeledVMs().Draw(t, "vms")
		asc := rapid.Bool().Draw(t, "asc")

		sorted := slices.Clone(mm.VMs(vms))
		sortByLabels(sorted, asc)

		if diff := cmp.Diff(vmNames(vms), vmNames(sorted), cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
			t.Fatalf("sortByLabels changed the VMs (-before +after):\n%s", diff)
		}

		for i := 1; i < len(sorted); i++ {
			prev, next := sorted[i-1], sorted[i]
			pc, nc := labelCount(prev), labelCount(next)

			if pc == nc {
				if strings.ToLower(prev.Name) > strings.ToLower(next.Name) {
					t.Fatalf("VMs %q and %q have %d labels each but are not in name order", prev.Name, next.Name, pc)
				}

				continue
			}

			if (pc < nc) != asc {
				t.Fatalf("VM %q (%d labels) before %q (%d labels) with ascending %v", prev.Name, pc, next.Name, nc, asc)
			}
		}
	})
}

func TestVMLabels(t *testing.T) {
	vms := []mm.VM{
		{Name: "b", Tags: map[string]string{"role": "web", "tier": "db", "__notes_1": "hi"}},
		{Name: "a", Tags: map[string]string{"role": "app", "tier": "db"}},
		{Name: "dnb", DoNotBoot: true, Tags: map[string]string{"zone": "dmz"}},
		{Name: "hidden", Tags: map[string]string{"secret": "yes"}},
	}

	tests := []struct {
		name    string
		showDNB bool
		want    []VMLabel
	}{
		{"booting VMs only", false, []VMLabel{{"role", "app"}, {"role", "web"}, {"tier", "db"}}},
		{"do-not-boot VMs too", true, []VMLabel{{"role", "app"}, {"role", "web"}, {"tier", "db"}, {"zone", "dmz"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VMLabels("exp", vms, tc.showDNB, listRole())
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("VMLabels(exp, showDNB %v) mismatch (-want +got):\n%s", tc.showDNB, diff)
			}
		})
	}
}

// Every label VMLabels offers selects at least one VM, and filtering by all
// of them lists every VM that carries a label.
func TestVMLabelsEachSelectsAVM(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		vms := genLabeledVMs().Draw(t, "vms")
		labels := VMLabels("exp", vms, true, listRole())

		for _, l := range labels {
			if page, _ := SelectVMs("exp", vms, nil, VMQuery{Labels: []VMLabel{l}, ShowDNB: true}, listRole()); len(page) == 0 {
				t.Fatalf("label %+v selects no VM", l)
			}
		}

		var labeled int

		for _, vm := range vms {
			if slices.ContainsFunc(slices.Collect(maps.Keys(vm.Tags)), isUserLabel) {
				labeled++
			}
		}

		if _, total := SelectVMs("exp", vms, nil, VMQuery{Labels: labels, ShowDNB: true}, listRole()); len(labels) > 0 && total != labeled {
			t.Fatalf("filtering by every label lists %d VMs, want %d", total, labeled)
		}
	})
}

func TestParseVMLabel(t *testing.T) {
	tests := []struct {
		in   string
		want VMLabel
		ok   bool
	}{
		{"role=web", VMLabel{"role", "web"}, true},
		{"role=", VMLabel{"role", ""}, true},
		{"url=a=b", VMLabel{"url", "a=b"}, true},
		{"role", VMLabel{}, false},
		{"=web", VMLabel{}, false},
		{"", VMLabel{}, false},
	}

	for _, tc := range tests {
		got, ok := ParseVMLabel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseVMLabel(%q) = %+v, %v; want %+v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
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
			SortCol: rapid.SampledFrom([]string{"", "name", "host", "uptime", "labels"}).Draw(t, "sortCol"),
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

// genVMListDelay draws the delays sortByDelay compares, including timers stored
// before the schema checked them, which do not parse.
func genVMListDelay() *rapid.Generator[string] {
	timer := rapid.Custom(func(t *rapid.T) string {
		n := rapid.IntRange(0, 120).Draw(t, "n")

		spellings := []string{
			fmt.Sprintf("%ds", n),
			fmt.Sprintf("%dm", n),
			(time.Duration(n) * time.Second).String(),
			fmt.Sprintf("%dx", n),
			strconv.Itoa(n),
		}

		return "timer:" + rapid.SampledFrom(spellings).Draw(t, "unit")
	})

	return rapid.OneOf(rapid.Just(""), rapid.Just("user"), rapid.Just("cc:ten"), rapid.Just("cc:nine"), rapid.Just("timer:"), timer, timer)
}

// delayLess must be a strict weak order, or [sort.SliceStable] may misorder VMs.
func TestDelayLessIsStrictWeakOrder(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		// a few close timers, one invalid, so that half the checks hit their mix
		near := rapid.SampledFrom([]string{"timer:1m", "timer:1x", "timer:2s", "timer:60s"})
		gen := rapid.SampledFrom([]*rapid.Generator[string]{genVMListDelay(), near}).Draw(t, "gen")
		d := []string{gen.Draw(t, "a"), gen.Draw(t, "b"), gen.Draw(t, "c")}
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

func TestPage(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}

	tests := []struct {
		name       string
		page, size int
		want       []int
	}{
		{name: "no page asked for", page: 0, size: 0, want: items},
		{name: "first page", page: 1, size: 2, want: []int{1, 2}},
		{name: "last, short page", page: 3, size: 2, want: []int{5}},
		{name: "past the last page", page: 4, size: 2, want: []int{}},
		{name: "one page of all", page: 1, size: MaxPageValue, want: items},
		{name: "far past the end", page: MaxPageValue, size: MaxPageValue, want: []int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Page(items, tt.page, tt.size)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Page(%v, %d, %d) mismatch (-want +got):\n%s", items, tt.page, tt.size, diff)
			}
		})
	}
}

// Walking the pages in order gives back every item once, in order, and only
// the last page is short.
func TestPageWalksEveryItem(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := rapid.SliceOf(rapid.Int()).Draw(t, "items")
		size := rapid.IntRange(1, 10).Draw(t, "size")

		var walked []int

		for page := 1; ; page++ {
			got := Page(items, page, size)
			if len(got) == 0 {
				break
			}

			if len(got) > size || len(got) < size && len(walked)+len(got) != len(items) {
				t.Fatalf("Page(%d items, %d, %d) has %d items", len(items), page, size, len(got))
			}

			walked = append(walked, got...)
		}

		if diff := cmp.Diff(items, walked, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("pages of %d mismatch (-want +got):\n%s", size, diff)
		}
	})
}
