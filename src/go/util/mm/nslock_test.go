package mm

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/activeshadow/libminimega/minicli"

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

	if !slices.Equal(order, []string{"first", "second"}) {
		t.Fatalf("held the lock in the order %q, want the waiter after the holder", order)
	}

	k.mu.Lock()
	left := len(k.locks)
	k.mu.Unlock()

	if left != 0 {
		t.Fatalf("%d keys left after every lock was released, want 0", left)
	}
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

	// Hold the namespace's VM config until every redeploy waits for it, so
	// all of them start their sequences at once. Release it even if the test
	// fails before it would, so every redeploy finishes.
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
				t.Errorf("RedeployVM %s: %v", vm, err)
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
				t.Fatalf("config for %s cloned while %s's sequence was open", cmd.Base, current)
			}

			current = strings.TrimPrefix(cmd.Base, "vm config clone ")
		case strings.HasPrefix(cmd.Base, "vm launch kvm "):
			if vm := strings.TrimPrefix(cmd.Base, "vm launch kvm "); vm != current {
				t.Fatalf("%s queued while %s's sequence was open", vm, current)
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
		t.Fatalf("KillVM: %v", err)
	}

	if err := (Minimega{}).RedeployVM(NS("exp"), VMName("b")); err != nil {
		t.Fatalf("RedeployVM: %v", err)
	}

	var flushes []string

	for _, cmd := range received() {
		if strings.HasPrefix(cmd.Base, "vm flush") {
			flushes = append(flushes, cmd.Base)
		}
	}

	if len(flushes) != 2 || flushes[0] != "vm flush a" || flushes[1] != "vm flush b" {
		t.Fatalf("sent flushes %q, want only `vm flush a` and `vm flush b`", flushes)
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
		t.Fatalf("KillVM error = %v, want the flush's vm not found", err)
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
		t.Fatalf("namespace's cc lock has %d holders while its cc script runs, want 1", got)
	}

	// Another namespace's cc lock is free meanwhile.
	ccLocks.lock("other")()

	finish()

	if err := <-done; err != nil {
		t.Fatalf("ReadC2ScriptFromFile: %v", err)
	}

	if got := ccLocks.refs("exp"); got != 0 {
		t.Fatalf("namespace's cc lock has %d holders after its cc script ran, want 0", got)
	}
}
