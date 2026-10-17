package mm

import (
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// refs returns how many callers hold or wait on key's lock.
func (k *keyedMutex) refs(key string) int {
	k.mu.Lock()
	defer k.mu.Unlock()

	if l, ok := k.locks[key]; ok {
		return l.refs
	}

	return 0
}

func TestKeyedMutex(t *testing.T) {
	t.Parallel()

	var (
		k     keyedMutex
		order []string // appended to only while holding "a"
		done  = make(chan struct{})
	)

	unlockA := k.lock("a")

	// Another key is independent.
	k.lock("b")()

	go func() {
		defer close(done)

		unlock := k.lock("a")
		order = append(order, "second")

		unlock()
	}()

	polltest.Until(t, "the second caller waits for the lock", func() bool { return k.refs("a") == 2 })

	order = append(order, "first")

	unlockA()
	unlockA() // a second call is harmless

	<-done

	if diff := cmp.Diff([]string{"first", "second"}, order); diff != "" {
		t.Errorf("keyedMutex.lock(a) holders order mismatch (-want +got):\n%s", diff)
	}

	k.mu.Lock()
	left := len(k.locks)
	k.mu.Unlock()

	if left != 0 {
		t.Errorf("keyedMutex has %d keys after every lock was released, want 0", left)
	}
}

// From one goroutine, with unlocks called again later: a key is tracked only while held,
// and a stale unlock never releases the key's next holder.
func TestKeyedMutexProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var (
			k        keyedMutex
			held     = make(map[string]func())
			released []func()
			keys     = []string{"a", "b", "c"}
		)

		t.Repeat(map[string]func(*rapid.T){
			"lock": func(t *rapid.T) {
				key := rapid.SampledFrom(keys).Draw(t, "key")
				if held[key] != nil {
					t.Skip("key is held")
				}

				held[key] = k.lock(key)
			},
			"unlock": func(t *rapid.T) {
				key := rapid.SampledFrom(keys).Draw(t, "key")
				if held[key] == nil {
					t.Skip("key is not held")
				}

				held[key]()
				released = append(released, held[key])
				delete(held, key)
			},
			"unlock again": func(t *rapid.T) {
				if len(released) == 0 {
					t.Skip("nothing released")
				}

				rapid.SampledFrom(released).Draw(t, "unlock")()
			},
			"": func(t *rapid.T) {
				for _, key := range keys {
					want := 0
					if held[key] != nil {
						want = 1
					}

					if got := k.refs(key); got != want {
						t.Fatalf("keyedMutex refs(%q) = %d, want %d", key, got, want)
					}

					if want == 1 && k.locks[key].mu.TryLock() {
						t.Fatalf("keyedMutex lock of %q is free while it is held", key)
					}
				}

				if len(k.locks) != len(held) {
					t.Fatalf("keyedMutex tracks %d keys, want the %d held", len(k.locks), len(held))
				}
			},
		})
	})
}

var filteredName = regexp.MustCompile(`\.filter name=(\S+) `)

// redeployCluster answers the commands RedeployVM sends.
func redeployCluster(cmd mmtest.Command) []*minicli.Response {
	if cmd.Base == vmInfoCmd {
		name := filteredName.FindStringSubmatch(cmd.Raw)[1]

		return []*minicli.Response{mmtest.Tabular("head", []string{"name", "disks"}, []string{name, "/d.qcow2"})}
	}

	return nil
}

func TestRedeployVMSequencesDoNotInterleave(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, redeployCluster)

	vms := []string{"a", "b", "c"}

	var wg sync.WaitGroup

	// Hold the VM config until every redeploy waits for it, so all start at once; the cleanup
	// releases it even if the test fails first.
	unlock := LockVMConfig("exp")

	t.Cleanup(func() {
		unlock()
		wg.Wait()
	})

	for _, vm := range vms {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := (Minimega{}).RedeployVM(NS("exp"), VMName(vm), CPU(2)); err != nil {
				t.Errorf("RedeployVM(%s) error = %v", vm, err)
			}
		}()
	}

	polltest.Until(t, "every launch waits for the VM config lock", func() bool { return vmConfigLocks.refs("exp") == len(vms)+1 })

	unlock()
	wg.Wait()

	// Between one VM's `vm config clone` and the bare `vm launch` that
	// launches it, no other VM's config or launch may be sent.
	var current string

	for _, cmd := range received() {
		switch {
		case strings.HasPrefix(cmd.Base, "vm config clone "):
			if current != "" {
				t.Fatalf("concurrent RedeployVM sent %q while %s's sequence was open", cmd.Base, current)
			}

			current = strings.TrimPrefix(cmd.Base, "vm config clone ")
		case strings.HasPrefix(cmd.Base, "vm launch kvm "):
			if vm := strings.TrimPrefix(cmd.Base, "vm launch kvm "); vm != current {
				t.Fatalf("concurrent RedeployVM queued %s while %s's sequence was open", vm, current)
			}
		case cmd.Base == vmLaunchCmd:
			current = ""
		}
	}
}

func TestKillAndRedeployFlushOnlyTheirVM(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.HasPrefix(cmd.Base, "vm flush") {
			// The node running the VM flushes it; another node doesn't have it.
			return []*minicli.Response{
				{Host: "head"},
				{Host: "compute1", Error: "vm not found: " + strings.TrimPrefix(cmd.Base, "vm flush ")},
			}
		}

		return redeployCluster(cmd)
	})

	if err := (Minimega{}).KillVM(NS("exp"), VMName("a")); err != nil {
		t.Errorf("KillVM(a) error = %v", err)
	}

	if err := (Minimega{}).RedeployVM(NS("exp"), VMName("b")); err != nil {
		t.Errorf("RedeployVM(b) error = %v", err)
	}

	var flushes []string

	for _, cmd := range received() {
		if strings.HasPrefix(cmd.Base, "vm flush") {
			flushes = append(flushes, cmd.Base)
		}
	}

	if diff := cmp.Diff([]string{"vm flush a", "vm flush b"}, flushes); diff != "" {
		t.Errorf("KillVM(a) and RedeployVM(b) sent flushes mismatch (-want +got):\n%s", diff)
	}
}

func TestKillVMReportsFlushFailure(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.HasPrefix(cmd.Base, "vm flush") {
			return []*minicli.Response{
				{Host: "head", Error: "vm not found: a"},
				{Host: "compute1", Error: "vm not found: a"},
			}
		}

		return nil
	})

	err := (Minimega{}).KillVM(NS("exp"), VMName("a"))
	if err == nil || !strings.Contains(err.Error(), "vm not found: a") {
		t.Errorf("KillVM(a) error = %v, want the flush's vm not found", err)
	}
}

func TestReadC2ScriptHoldsNamespaceCCLock(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	var (
		release = make(chan struct{})
		reading atomic.Bool
		done    = make(chan error, 1)
	)

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.HasPrefix(cmd.Base, "read ") {
			reading.Store(true)
			<-release
		}

		return nil
	})

	// Finish the script even if the test fails before it would, so the
	// namespace's cc lock is released.
	finish := sync.OnceFunc(func() { close(release) })
	t.Cleanup(finish)

	go func() { done <- ReadC2ScriptFromFile("exp", "/exp-cc.mm") }()

	polltest.Until(t, "the cc script is read", reading.Load)

	if got := ccLocks.refs("exp"); got != 1 {
		t.Errorf("ccLocks.refs(exp) while ReadC2ScriptFromFile runs = %d, want 1", got)
	}

	// Another namespace's cc lock is free meanwhile.
	ccLocks.lock("other")()

	finish()

	if err := <-done; err != nil {
		t.Errorf("ReadC2ScriptFromFile(exp, /exp-cc.mm) error = %v", err)
	}

	if got := ccLocks.refs("exp"); got != 0 {
		t.Errorf("ccLocks.refs(exp) after ReadC2ScriptFromFile = %d, want 0", got)
	}
}
