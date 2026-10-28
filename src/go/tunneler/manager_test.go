package main

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"pgregory.net/rapid"

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

// genManagerListener draws listeners from a few experiments, VMs and ports, so keys repeat.
func genManagerListener() *rapid.Generator[ft.Listener] {
	return rapid.Custom(func(t *rapid.T) ft.Listener {
		return ft.Listener{
			Exp:     rapid.SampledFrom([]string{"e", "e:v", "f"}).Draw(t, "exp"),
			VM:      rapid.SampledFrom([]string{"v", "w"}).Draw(t, "vm"),
			DstHost: "127.0.0.1",
			DstPort: rapid.IntRange(80, 81).Draw(t, "port"),
			SrcPort: rapid.IntRange(1, 3).Draw(t, "src"),
		}
	})
}

// Against a model keyed by ToKey: a key holds one listener, new IDs only grow, removed IDs
// are gone, and snapshots list every live listener once, in ID order.
func TestListenerManagerProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var (
			manager = newListenerManager()
			model   = make(map[string]int) // key -> ID
			lastID  int
			removed []int
		)

		t.Repeat(map[string]func(*rapid.T){
			"add": func(t *rapid.T) {
				listener := genManagerListener().Draw(t, "listener")
				id, existed := model[listener.ToKey()]

				local, created := manager.add(listener)
				if created == existed {
					t.Fatalf("add(%q) created = %t, want %t", listener.ToKey(), created, !existed)
				}

				if existed && local.ID != id {
					t.Fatalf("add(%q) of a known key = ID %d, want %d", listener.ToKey(), local.ID, id)
				}

				if !existed {
					if local.ID <= lastID {
						t.Fatalf("add(%q) = ID %d, want more than %d", listener.ToKey(), local.ID, lastID)
					}

					lastID = local.ID
					model[listener.ToKey()] = local.ID
				}
			},
			"remove": func(t *rapid.T) {
				key := genManagerListener().Draw(t, "listener").ToKey()
				id, existed := model[key]

				if _, ok := manager.remove(key); ok != existed {
					t.Fatalf("remove(%q) = %t, want %t", key, ok, existed)
				}

				if existed {
					delete(model, key)
					removed = append(removed, id)
				}
			},
			"": func(t *rapid.T) {
				snapshot := manager.snapshot()
				if len(snapshot) != len(model) {
					t.Fatalf("snapshot() has %d listeners, want %d", len(snapshot), len(model))
				}

				for i, listener := range snapshot {
					if i > 0 && listener.ID <= snapshot[i-1].ID {
						t.Fatalf("snapshot() IDs not increasing: %d after %d", listener.ID, snapshot[i-1].ID)
					}

					if id := model[listener.ToKey()]; id != listener.ID || !manager.hasKey(listener.ToKey()) {
						t.Fatalf("snapshot() lists %q as ID %d, want ID %d", listener.ToKey(), listener.ID, id)
					}

					err := manager.withListener(listener.ID, func(local *LocalListener) error {
						if local.ToKey() != listener.ToKey() {
							t.Fatalf("withListener(%d) = %q, want %q", listener.ID, local.ToKey(), listener.ToKey())
						}

						return nil
					})
					if err != nil {
						t.Fatalf("withListener(%d) = %v, want the listener", listener.ID, err)
					}
				}

				for _, id := range removed {
					if err := manager.withListener(id, func(*LocalListener) error { return nil }); !errors.Is(err, errListenerNotFound) {
						t.Fatalf("withListener(removed ID %d) = %v, want errListenerNotFound", id, err)
					}
				}
			},
		})
	})
}
