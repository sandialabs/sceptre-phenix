package main

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	ft "phenix/web/forward/forwardtypes"
)

func TestListenerManagerSnapshotIsIndependent(t *testing.T) {
	var (
		manager     = newListenerManager()
		listener, _ = manager.add(ft.Listener{Exp: "experiment", VM: "vm", DstHost: "127.0.0.1", DstPort: 80})
	)

	err := manager.withListener(listener.ID, func(local *LocalListener) error {
		local.Listening = true
		return nil
	})

	if err != nil {
		t.Fatalf("updating listener: %v", err)
	}

	snapshot := manager.snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot length = %d, want 1", len(snapshot))
	}

	snapshot[0].Listening = false

	snapshot = manager.snapshot()
	if !snapshot[0].Listening {
		t.Fatal("mutating snapshot changed manager state")
	}
}

func TestListenerManagerConcurrentAccess(t *testing.T) {
	var (
		manager = newListenerManager()
		wg      sync.WaitGroup
	)

	for i := range 100 {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			listener, _ := manager.add(ft.Listener{
				Exp:     fmt.Sprintf("experiment-%d", i),
				VM:      "vm",
				DstHost: "127.0.0.1",
				DstPort: 80 + i,
			})

			err := manager.withListener(listener.ID, func(local *LocalListener) error {
				local.Listening = true
				return nil
			})

			if err != nil {
				t.Errorf("updating listener: %v", err)
			}

			manager.snapshot()
		}(i)
	}

	wg.Wait()

	if got := len(manager.snapshot()); got != 100 {
		t.Errorf("snapshot length = %d, want 100", got)
	}

	for i := range 100 {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			manager.remove(fmt.Sprintf("experiment-%d:vm:127.0.0.1:%d:", i, 80+i))
		}(i)
	}

	for i := range 100 {
		wg.Add(1)

		go func(int) {
			defer wg.Done()

			manager.snapshot()
		}(i)
	}

	wg.Wait()

	if got := len(manager.snapshot()); got != 0 {
		t.Errorf("snapshot length after removal = %d, want 0", got)
	}
}

func TestListenerManagerIDsAreUniqueAndNotReused(t *testing.T) {
	var (
		manager = newListenerManager()

		first, _  = manager.add(ft.Listener{Exp: "experiment-1", VM: "vm", DstHost: "127.0.0.1", DstPort: 80})
		second, _ = manager.add(ft.Listener{Exp: "experiment-2", VM: "vm", DstHost: "127.0.0.1", DstPort: 81})
		third, _  = manager.add(ft.Listener{Exp: "experiment-3", VM: "vm", DstHost: "127.0.0.1", DstPort: 82})
	)

	if first.ID == second.ID {
		t.Fatalf("listeners received duplicate ID %d", first.ID)
	}

	manager.remove(first.ToKey())

	if third.ID <= second.ID {
		t.Fatalf("new listener ID = %d, want greater than %d", third.ID, second.ID)
	}

	if err := manager.withListener(first.ID, func(*LocalListener) error { return nil }); !errors.Is(err, errListenerNotFound) {
		t.Fatalf("lookup of removed ID returned %v, want listener-not-found error", err)
	}
}

func TestListenerManagerSnapshotIsSorted(t *testing.T) {
	manager := newListenerManager()

	manager.add(ft.Listener{Exp: "foo", VM: "vm", DstHost: "127.0.0.1", DstPort: 83})
	manager.add(ft.Listener{Exp: "bar", VM: "vm", DstHost: "127.0.0.1", DstPort: 81})
	manager.add(ft.Listener{Exp: "sucka", VM: "vm", DstHost: "127.0.0.1", DstPort: 82})

	for range 10 {
		snapshot := manager.snapshot()

		for index, listener := range snapshot {
			wantID := index + 1

			if listener.ID != wantID {
				t.Fatalf("snapshot[%d].ID = %d, want %d", index, listener.ID, wantID)
			}
		}
	}
}

func TestListenerManagerDuplicateKeyIsIdempotent(t *testing.T) {
	var (
		manager  = newListenerManager()
		listener = ft.Listener{Exp: "experiment", VM: "vm", DstHost: "127.0.0.1", DstPort: 80}

		first, firstCreated   = manager.add(listener)
		second, secondCreated = manager.add(listener)
	)

	if !firstCreated || secondCreated {
		t.Fatalf("duplicate creation flags = (%t, %t), want (true, false)", firstCreated, secondCreated)
	}

	if first != second {
		t.Fatal("duplicate creation returned a different listener")
	}

	err := manager.withListener(first.ID, func(got *LocalListener) error {
		if got != second {
			t.Error("lookup returned the wrong listener after duplicate creation")
		}

		return nil
	})

	if err != nil {
		t.Fatalf("lookup of original listener after duplicate creation: %v", err)
	}

	if got := len(manager.snapshot()); got != 1 {
		t.Fatalf("snapshot length = %d, want 1", got)
	}
}
