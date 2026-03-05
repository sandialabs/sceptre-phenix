package util

import "slices"

func StringSliceContains(slice []string, s string) bool {
	return slices.Contains(slice, s)
}
