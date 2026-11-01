package util

import (
	"errors"
	"math"
	"strconv"
)

// MaxPageValue bounds page numbers and sizes so paging math cannot overflow.
const MaxPageValue = math.MaxInt32

var ErrInvalidPage = errors.New("invalid pageNum or perPage")

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
