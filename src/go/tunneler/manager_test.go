package main

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	ft "phenix/web/forward/forwardtypes"
)

func TestListenerManagerSnapshotIsIndependent(t *testing.T) {
	manager := newListenerManager()
	listener := manager.add(ft.Listener{Exp: "experiment", VM: "vm", DstHost: "127.0.0.1", DstPort: 80})

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
	manager := newListenerManager()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			listener := manager.add(ft.Listener{
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

	for i := 0; i < 100; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			manager.remove(fmt.Sprintf("experiment-%d:vm:127.0.0.1:%d:", i, 80+i))
		}(i)
	}

	for i := 0; i < 100; i++ {
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

		first  = manager.add(ft.Listener{Exp: "experiment-1", VM: "vm", DstHost: "127.0.0.1", DstPort: 80})
		second = manager.add(ft.Listener{Exp: "experiment-2", VM: "vm", DstHost: "127.0.0.1", DstPort: 81})
		third  = manager.add(ft.Listener{Exp: "experiment-3", VM: "vm", DstHost: "127.0.0.1", DstPort: 82})
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

func TestListenerManagerDuplicateKeyRemovesStaleID(t *testing.T) {
	var (
		manager  = newListenerManager()
		listener = ft.Listener{Exp: "experiment", VM: "vm", DstHost: "127.0.0.1", DstPort: 80}

		first  = manager.add(listener)
		second = manager.add(listener)
	)

	if err := manager.withListener(first.ID, func(*LocalListener) error { return nil }); !errors.Is(err, errListenerNotFound) {
		t.Fatalf("lookup of replaced ID returned %v, want listener-not-found error", err)
	}

	err := manager.withListener(second.ID, func(got *LocalListener) error {
		if got != second {
			t.Error("lookup returned the wrong listener")
		}

		return nil
	})

	if err != nil {
		t.Fatalf("lookup of replacement listener: %v", err)
	}
}
