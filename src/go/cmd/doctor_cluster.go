package cmd

import (
	"fmt"
)

// doctorMesh is the nodes of minimega's mesh, which a run of doctor shares.
type doctorMesh struct {
	nodes []string // every node, this node too
}

// plural is n with the words one when n is 1, else with the words many.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return fmt.Sprintf("%d %s", n, many)
}
