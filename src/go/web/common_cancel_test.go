package web

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// takeCancelers takes the cancelers and the wait group of the experiment, and
// the cancelers of the app stages triggered from the UI for it, which register
// under "<exp>/<app>". It leaves those of other experiments, whose names can
// start with the same text.
func TestTakeCancelersIncludesTriggeredApps(t *testing.T) {
	var (
		mu       sync.Mutex
		canceled []string
		wg       sync.WaitGroup
	)

	keys := []string{"tc-exp", "tc-exp/soh", "tc-exp/other", "tc-exp2/soh", "tc-exp-two", "tc-ex"}
	handles := map[string]*canceler{}

	for _, key := range keys {
		handles[key] = addCanceler(key, func() {
			mu.Lock()
			defer mu.Unlock()

			canceled = append(canceled, key)
		})
	}

	commonMu.Lock()
	waiters["tc-exp"] = &wg
	commonMu.Unlock()

	t.Cleanup(func() {
		for _, key := range keys {
			dropCanceler(key, handles[key])
		}
	})

	cancels, got := takeCancelers("tc-exp")

	for _, c := range cancels {
		c.cancel()
	}

	slices.Sort(canceled)

	if diff := cmp.Diff([]string{"tc-exp", "tc-exp/other", "tc-exp/soh"}, canceled); diff != "" {
		t.Errorf("takeCancelers(tc-exp) canceled mismatch (-want +got):\n%s", diff)
	}

	if got != &wg {
		t.Errorf("takeCancelers(tc-exp) wait group = %p, want %p", got, &wg)
	}

	commonMu.Lock()
	defer commonMu.Unlock()

	if _, ok := waiters["tc-exp"]; ok {
		t.Error("takeCancelers(tc-exp) kept the wait group of tc-exp")
	}

	for _, key := range keys {
		_, kept := cancelers[key]
		if want := key != "tc-exp" && !strings.HasPrefix(key, "tc-exp/"); kept != want {
			t.Errorf("canceler %s kept after takeCancelers(tc-exp) = %t, want %t", key, kept, want)
		}
	}
}

// For any registered keys, takeCancelers takes exactly the keys that are the
// experiment's name or start with the name and a slash.
func TestTakeCancelersProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		names := rapid.SampledFrom([]string{"tp-a", "tp-ab", "tp-a-b", "tp-b"})
		name := names.Draw(t, "name")
		keys := rapid.SliceOfDistinct(rapid.Custom(func(t *rapid.T) string {
			key := names.Draw(t, "experiment")
			if rapid.Bool().Draw(t, "app") {
				key += "/" + rapid.SampledFrom([]string{"soh", "scorch", "a/b"}).Draw(t, "app name")
			}

			return key
		}), rapid.ID[string]).Draw(t, "keys")

		var want []string

		for _, key := range keys {
			addCanceler(key, func() {})

			if key == name || strings.HasPrefix(key, name+"/") {
				want = append(want, key)
			}
		}

		taken, _ := takeCancelers(name)

		commonMu.Lock()

		var left []string

		for _, key := range keys {
			if _, ok := cancelers[key]; ok {
				left = append(left, key)
				delete(cancelers, key)
			}
		}

		commonMu.Unlock()

		if len(taken) != len(want) {
			t.Fatalf("takeCancelers(%s) of %q took %d cancelers, want %d (%q)", name, keys, len(taken), len(want), want)
		}

		for _, key := range left {
			if slices.Contains(want, key) {
				t.Fatalf("takeCancelers(%s) of %q left %s", name, keys, key)
			}
		}

		if len(left)+len(taken) != len(keys) {
			t.Fatalf("takeCancelers(%s) of %q took %d and left %q, want all %d accounted for", name, keys, len(taken), left, len(keys))
		}
	})
}
