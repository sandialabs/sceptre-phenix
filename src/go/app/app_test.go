package app

import (
	"os"
	"testing"

	ifaces "phenix/types/interfaces"
)

// Helper test function(s) for app package.

func checkConfigureExpected(t *testing.T, nodes []ifaces.NodeSpec, expected [][]ifaces.NodeInjection) {
	for i, node := range nodes {
		inj := node.Injections()
		exp := expected[i]

		if len(inj) != len(exp) {
			t.Logf("expected %d injections for node %d, got %d", len(exp), i, len(inj))
			t.FailNow()
		}

		for j, k := range inj {
			if k.Src() != exp[j].Src() {
				t.Logf("expected src for injection %d on node %d to be %s, got %s", j, i, exp[j].Src(), k.Src())
				t.FailNow()
			}

			if k.Dst() != exp[j].Dst() {
				t.Logf("expected dst for injection %d on node %d to be %s, got %s", j, i, exp[j].Dst(), k.Dst())
				t.FailNow()
			}
		}
	}
}

func checkStartExpected(t *testing.T, nodes []ifaces.NodeSpec, expected [][]ifaces.NodeInjection) {
	for _, inj := range expected {
		for _, i := range inj {
			if _, err := os.Stat(i.Src()); err != nil {
				t.Logf("expected injection src %s to be on disk, but it's not", i.Src())
				t.FailNow()
			}
		}
	}
}
