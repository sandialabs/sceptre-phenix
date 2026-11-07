package util

import (
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/mm"
)

// pageModel gives the items whose index is on page of the given size, or
// every item for page 0.
func pageModel[T any](items []T, page, size int) []T {
	if page == 0 {
		return items
	}

	var on []T

	for i, item := range items {
		if i >= (page-1)*size && i < page*size {
			on = append(on, item)
		}
	}

	return on
}

// A page that ParsePage accepts never makes Page or VMs.Paginate panic, and
// both give the items that pageModel gives.
func FuzzParsePage(f *testing.F) {
	maxValue := strconv.Itoa(MaxPageValue)

	for _, seed := range [][2]string{
		{"", ""}, {"2", ""}, {"", "10"}, {"2", "10"}, {maxValue, maxValue}, {"0", "10"}, {"1", "0"},
		{"-1", "10"}, {"1", "-10"}, {"x", "10"}, {"1", "ten"}, {"1", "2.5"}, {"2147483648", "10"},
		{"1", "4294967296"}, {"1", "1"}, {"3", "2"}, {"+2", "02"},
	} {
		f.Add(seed[0], seed[1], uint8(5))
	}

	f.Fuzz(func(t *testing.T, pageNum, perPage string, n uint8) {
		page, size, err := ParsePage(pageNum, perPage)
		if err != nil {
			return
		}

		vms := make([]mm.VM, n%16)
		for i := range vms {
			vms[i].Name = strconv.Itoa(i)
		}

		want := pageModel(vms, page, size)

		if diff := cmp.Diff(want, Page(vms, page, size), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("Page(%d VMs, %d, %d) mismatch (-want +got):\n%s", len(vms), page, size, diff)
		}

		// callers page with Paginate only when a page was asked for
		if page == 0 {
			return
		}

		if diff := cmp.Diff(want, []mm.VM(mm.VMs(vms).Paginate(page, size)), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("VMs.Paginate(%d VMs, %d, %d) mismatch (-want +got):\n%s", len(vms), page, size, diff)
		}
	})
}
