package scorch

import (
	"fmt"
	"sync"
	"testing"
)

// TestGetComponentReturnsFreshInstances: components carry the options of the
// run that initialised them, so pipelines of different experiments executing
// the same component type concurrently must not share one instance.
func TestGetComponentReturnsFreshInstances(t *testing.T) {
	t.Parallel()

	if GetComponent("soh") == GetComponent("soh") {
		t.Fatal("GetComponent returned the same instance twice")
	}

	if _, ok := GetComponent("no-such-type").(*UserComponent); !ok {
		t.Fatal("unknown component types must fall back to user-shell")
	}

	var wg sync.WaitGroup

	for i := range 4 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			var (
				name = fmt.Sprintf("cmp-%d", i)
				cmp  = GetComponent("soh")
			)

			_ = cmp.Init(Name(name), RunID(i))

			soh, ok := cmp.(*SOH)
			if !ok {
				t.Errorf("soh component has type %T", cmp)

				return
			}

			if soh.options.Name != name || soh.options.Run != i {
				t.Errorf("component initialised for %s/%d holds %s/%d", name, i, soh.options.Name, soh.options.Run)
			}
		}()
	}

	wg.Wait()
}
