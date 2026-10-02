package util

import "slices"

func StringSliceContains(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

// Unique returns s without repeats, in the order each first appears, leaving
// s as it is.
func Unique[T comparable](s []T) []T {
	seen := make(map[T]bool, len(s))
	unique := make([]T, 0, len(s))

	for _, v := range s {
		if !seen[v] {
			seen[v] = true

			unique = append(unique, v)
		}
	}

	return unique
}
