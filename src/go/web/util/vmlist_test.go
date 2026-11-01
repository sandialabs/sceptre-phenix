package util

import (
	"errors"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"
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
