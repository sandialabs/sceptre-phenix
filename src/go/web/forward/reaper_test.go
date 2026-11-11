package forward

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/web/broker"
	ft "phenix/web/forward/forwardtypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// tunnelsMM is a minimega that lists the same tunnels for every VM and counts
// the listings. Any other method call panics on the embedded nil interface.
type tunnelsMM struct {
	mm.MM

	tunnels []map[string]string
	calls   atomic.Int32 // the reaper lists the tunnels of several VMs at once
}

func (m *tunnelsMM) GetTunnels(...mm.Option) []map[string]string {
	m.calls.Add(1)

	return m.tunnels
}

// useForwards installs a store in which the named experiments are running, a
// minimega listing the given tunnels, and the given forwards.
func useForwards(t *testing.T, running []string, tunnels []map[string]string, forwarded ...ft.Listener) *tunnelsMM {
	t.Helper()

	storetest.Use(t)

	for _, name := range running {
		c, _ := store.NewConfig("experiment/" + name)
		c.Spec = map[string]any{"experimentName": name}
		c.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z"}

		if err := store.Create(c); err != nil {
			t.Fatalf("store.Create(experiment/%s) = %v", name, err)
		}
	}

	fake := &tunnelsMM{tunnels: tunnels}

	originalMM := mm.DefaultMM
	mm.DefaultMM = fake //nolint:reassign // install test double

	forwardsMu.Lock()
	saved, savedPending := forwards, pending
	forwards, pending = make(map[string]ft.Listener), make(map[string]string)

	for _, l := range forwarded {
		forwards[l.ToKey()] = l
	}

	forwardsMu.Unlock()

	t.Cleanup(func() {
		mm.DefaultMM = originalMM //nolint:reassign // restore test double

		forwardsMu.Lock()
		forwards, pending = saved, savedPending
		forwardsMu.Unlock()
	})

	return fake
}

// listForwards asks GET /experiments/{exp}/vms/{name}/forwards for the VM's
// forwards, as a user who may list them, and returns their keys.
func listForwards(t *testing.T, exp, vm string) []string {
	t.Helper()

	router := mux.NewRouter()
	router.Handle("/experiments/{exp}/vms/{name}/forwards", weberror.ErrorHandler(GetPortForwards))

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"vms/forwards"}, ResourceNames: []string{"*/*"}, Verbs: []string{"list"}},
	}}}

	req := httptest.NewRequest(http.MethodGet, "/experiments/"+exp+"/vms/"+vm+"/forwards", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "u")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", req.URL.Path, rec.Code, rec.Body)
	}

	var body struct {
		Listeners []ft.Listener `json:"listeners"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding GET %s: %v", req.URL.Path, err)
	}

	keys := make([]string, len(body.Listeners))
	for i, l := range body.Listeners {
		keys[i] = l.ToKey()
	}

	slices.Sort(keys)

	return keys
}

// Listing a VM's forwards drops those without a matching minimega tunnel (by
// destination, host case-insensitively, and source port); QEMU forwards need none.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestListingForwardsDropsClosedTunnels(t *testing.T) {
	open := []map[string]string{
		{"vm": "a", "id": "1", "src port": "50001", "dst": "127.0.0.1", "dst port": "22"},
		{"vm": "a", "id": "2", "src port": "50002", "dst": "Router.Local", "dst port": "80"},
	}

	tests := map[string]struct {
		forward ft.Listener
		tunnels []map[string]string
		kept    bool
	}{
		"matching tunnel":       {ft.Listener{DstHost: "127.0.0.1", DstPort: 22}, open, true},
		"host in another case":  {ft.Listener{DstHost: "router.local", DstPort: 80}, open, true},
		"no destination port":   {ft.Listener{DstHost: "127.0.0.1"}, open, true},
		"own source port":       {ft.Listener{DstHost: "127.0.0.1", DstPort: 22, ClusterPort: 50001}, open, true},
		"QEMU without a tunnel": {ft.Listener{DstPort: 5900, QEMU: true}, nil, true},
		"another port":          {ft.Listener{DstHost: "127.0.0.1", DstPort: 80}, open, false},
		"another host":          {ft.Listener{DstHost: "10.0.0.1", DstPort: 22}, open, false},
		"another user's tunnel": {ft.Listener{DstHost: "127.0.0.1", DstPort: 22, ClusterPort: 50009}, open, false},
		"no tunnels for the VM": {ft.Listener{DstHost: "127.0.0.1", DstPort: 22}, nil, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.forward.Exp, tc.forward.VM, tc.forward.Owner = "exp", "a", "u"

			useForwards(t, []string{"exp"}, tc.tunnels, tc.forward)

			var want []string
			if tc.kept {
				want = []string{tc.forward.ToKey()}
			}

			got := listForwards(t, "exp", "a")
			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GET /experiments/exp/vms/a/forwards mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Reaping lists tunnels once per running experiment's VM with tunnel forwards; a
// stopped experiment is left to its stop hook (listing would recreate its namespace).
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestReapingAsksMinimegaOnlyWhenNeeded(t *testing.T) {
	var (
		forwardA22   = ft.Listener{Exp: "exp", VM: "a", DstHost: "127.0.0.1", DstPort: 22, Owner: "u"}
		forwardA80   = ft.Listener{Exp: "exp", VM: "a", DstHost: "127.0.0.1", DstPort: 80, Owner: "u"}
		forwardB22   = ft.Listener{Exp: "exp", VM: "b", DstHost: "127.0.0.1", DstPort: 22, Owner: "u"}
		closedB443   = ft.Listener{Exp: "exp", VM: "b", DstHost: "127.0.0.1", DstPort: 443, Owner: "u"}
		qemuC        = ft.Listener{Exp: "exp", VM: "c", DstPort: 5900, Owner: "u", QEMU: true}
		otherA22     = ft.Listener{Exp: "other", VM: "a", DstHost: "127.0.0.1", DstPort: 22, Owner: "u"}
		stoppedA443  = ft.Listener{Exp: "stopped", VM: "a", DstHost: "127.0.0.1", DstPort: 443, Owner: "u"}
		openTunnels  = []map[string]string{{"dst": "127.0.0.1", "dst port": "22"}, {"dst": "127.0.0.1", "dst port": "80"}}
		allForwarded = []ft.Listener{forwardA22, forwardA80, forwardB22, closedB443, qemuC, otherA22, stoppedA443}
	)

	fake := useForwards(t, []string{"exp", "other"}, openTunnels, allForwarded...)

	steps := []struct {
		name     string
		reap     func() []string
		want     []ft.Listener
		listings int
	}{
		{
			name: "listing a VM's forwards", reap: func() []string { return listForwards(t, "exp", "a") },
			want: []ft.Listener{forwardA22, forwardA80}, listings: 1,
		},
		{
			name: "listing a VM with only QEMU forwards", reap: func() []string { return listForwards(t, "exp", "c") },
			want: []ft.Listener{qemuC}, listings: 0,
		},
		{
			name: "listing a VM without forwards", reap: func() []string { return listForwards(t, "exp", "none") },
			want: nil, listings: 0,
		},
		{
			name: "listing a stopped experiment's VM", reap: func() []string { return listForwards(t, "stopped", "a") },
			want: []ft.Listener{stoppedA443}, listings: 0,
		},
		{
			// exp/a, exp/b and other/a
			name: "reaping in the background", reap: reapAll,
			want:     []ft.Listener{forwardA22, forwardA80, forwardB22, qemuC, otherA22, stoppedA443},
			listings: 3,
		},
	}

	for _, step := range steps {
		before := fake.calls.Load()

		got := step.reap()

		want := make([]string, len(step.want))
		for i, l := range step.want {
			want[i] = l.ToKey()
		}

		slices.Sort(want)

		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("%s: forwards mismatch (-want +got):\n%s", step.name, diff)
		}

		if n := int(fake.calls.Load() - before); n != step.listings {
			t.Errorf("%s: minimega listed tunnels %d times, want %d", step.name, n, step.listings)
		}
	}
}

// reapAll runs the background reaper and returns the keys of the forwards
// left.
func reapAll() []string {
	reapForwards()

	return reaperKeys()
}

// A VM's forwards are its own, not those of a VM whose name starts with its
// name.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestListingForwardsOfOneVM(t *testing.T) {
	forward := func(vm string) ft.Listener {
		return ft.Listener{Exp: "exp", VM: vm, DstHost: "127.0.0.1", DstPort: 5900, Owner: "u", QEMU: true}
	}

	useForwards(t, []string{"exp"}, nil, forward("a"), forward("ab"))

	if diff := cmp.Diff([]string{forward("a").ToKey()}, listForwards(t, "exp", "a")); diff != "" {
		t.Errorf("GET /experiments/exp/vms/a/forwards mismatch (-want +got):\n%s", diff)
	}
}

func genReaperListener() *rapid.Generator[ft.Listener] {
	return rapid.Custom(func(t *rapid.T) ft.Listener {
		return ft.Listener{
			Exp:         "exp",
			VM:          "a",
			DstHost:     rapid.SampledFrom([]string{"", "router.local", "Router.Local", "10.0.0.1"}).Draw(t, "dst"),
			DstPort:     rapid.SampledFrom([]int{0, 22, 80}).Draw(t, "dst port"),
			ClusterPort: rapid.SampledFrom([]int{0, 50001, 50002}).Draw(t, "cluster port"),
			QEMU:        rapid.Bool().Draw(t, "qemu"),
		}
	})
}

func genReaperTunnels() *rapid.Generator[[]map[string]string] {
	row := rapid.Custom(func(t *rapid.T) map[string]string {
		return map[string]string{
			"vm":       "a",
			"dst":      rapid.SampledFrom([]string{"ROUTER.LOCAL", "router.local", "10.0.0.1", "other"}).Draw(t, "dst"),
			"dst port": rapid.SampledFrom([]string{"22", "80", "8080"}).Draw(t, "dst port"),
			"src port": rapid.SampledFrom([]string{"50001", "50002", "50003"}).Draw(t, "src port"),
		}
	})

	return rapid.SliceOfN(row, 0, 4)
}

// A tunnel is found in any listing order, with more listed or fewer parts set,
// and when its own tunnel is listed; a QEMU forward needs none.
func TestForwardExistsProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		l, tunnels := genReaperListener().Draw(t, "listener"), genReaperTunnels().Draw(t, "tunnels")
		exists := forwardExists(l, tunnels)

		switch {
		case l.QEMU && !exists:
			t.Fatalf("forwardExists(%+v, %v) = false, want true for a QEMU forward", l, tunnels)
		case !l.QEMU && len(tunnels) == 0 && exists:
			t.Fatalf("forwardExists(%+v, no tunnels) = true, want false", l)
		}

		if got := forwardExists(l, rapid.Permutation(tunnels).Draw(t, "shuffled")); got != exists {
			t.Fatalf("forwardExists(%+v, shuffled %v) = %t, want %t", l, tunnels, got, exists)
		}

		more := append(slices.Clone(tunnels), genReaperTunnels().Draw(t, "more")...)
		if exists && !forwardExists(l, more) {
			t.Fatalf("forwardExists(%+v, %v) = false, want true as for its first %d tunnels", l, more, len(tunnels))
		}

		looser := l
		looser.DstHost = rapid.SampledFrom([]string{l.DstHost, ""}).Draw(t, "looser dst")
		looser.DstPort = rapid.SampledFrom([]int{l.DstPort, 0}).Draw(t, "looser dst port")
		looser.ClusterPort = rapid.SampledFrom([]int{l.ClusterPort, 0}).Draw(t, "looser cluster port")

		if exists && !forwardExists(looser, tunnels) {
			t.Fatalf("forwardExists(%+v, %v) = false, want true as for %+v", looser, tunnels, l)
		}

		own := map[string]string{
			"vm": l.VM, "dst": strings.ToUpper(l.DstHost), "dst port": strconv.Itoa(l.DstPort), "src port": strconv.Itoa(l.ClusterPort),
		}

		if listed := append(slices.Clone(tunnels), own); !forwardExists(l, listed) {
			t.Fatalf("forwardExists(%+v, %v) = false, want true with its own tunnel listed", l, listed)
		}
	})
}

// reaperQueued returns how many broadcasts wait for the broker, which no test
// in this package starts.
func reaperQueued() int {
	return broker.CurrentStats().Queued
}

// announceDeleted broadcasts one delete for each forward.
//
//nolint:paralleltest // counts the broker's shared queue
func TestAnnounceDeletedBroadcastsEachForward(t *testing.T) {
	for name, tc := range map[string]struct {
		forwards []ft.Listener
		want     int
	}{
		"none": {want: 0},
		"two": {
			forwards: []ft.Listener{{Exp: "exp", VM: "a", DstPort: 22}, {Exp: "exp", VM: "b", DstPort: 80}},
			want:     2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := reaperQueued()

			announceDeleted(tc.forwards)

			if got := reaperQueued() - before; got != tc.want {
				t.Errorf("announceDeleted(%d forwards) queued %d broadcasts, want %d", len(tc.forwards), got, tc.want)
			}
		})
	}
}

// reaperChangingMM lists no tunnels, and while it does replaces the forward
// with key with changed, as a user recreating it would.
type reaperChangingMM struct {
	mm.MM

	key     string
	changed ft.Listener
}

func (m *reaperChangingMM) GetTunnels(...mm.Option) []map[string]string {
	forwardsMu.Lock()
	forwards[m.key] = m.changed
	forwardsMu.Unlock()

	return nil
}

// Reaping drops a closed forward and announces it, but keeps one that
// changed while minimega was asked about it.
//
//nolint:paralleltest // replaces the store, minimega and the forwards, and counts the broker's queue
func TestReapingKeepsAForwardChangedMeanwhile(t *testing.T) {
	closed := ft.Listener{Exp: "exp", VM: "a", DstHost: "127.0.0.1", DstPort: 22, Owner: "u"}
	changed := closed
	changed.ClusterPort = 50001

	for name, tc := range map[string]struct {
		change    bool
		want      []string
		announced int
	}{
		"closed":            {want: []string{}, announced: 1},
		"changed meanwhile": {change: true, want: []string{changed.ToKey()}, announced: 0},
	} {
		t.Run(name, func(t *testing.T) {
			useForwards(t, []string{"exp"}, nil, closed)

			if tc.change {
				fake := &reaperChangingMM{key: closed.ToKey(), changed: changed}
				mm.DefaultMM = fake //nolint:reassign // install test double; useForwards restores minimega
			}

			before := reaperQueued()

			reapVMForwards("exp", "a")

			if got := reaperQueued() - before; got != tc.announced {
				t.Errorf("reapVMForwards(exp, a) announced %d deletes, want %d", got, tc.announced)
			}

			forwardsMu.Lock()
			kept, ok := forwards[closed.ToKey()]
			forwardsMu.Unlock()

			if tc.change && (!ok || kept != changed) {
				t.Errorf("reapVMForwards(exp, a) left %+v, %t; want the changed forward %+v", kept, ok, changed)
			}

			if diff := cmp.Diff(tc.want, reaperKeys(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("reapVMForwards(exp, a) forwards left mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// reaperKeys returns the keys of the forwards held, sorted.
func reaperKeys() []string {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	keys := make([]string, 0, len(forwards))
	for key := range forwards {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// removeLocked forgets only the given forward.
//
//nolint:paralleltest // replaces the forwards
func TestRemoveLockedForgetsOneForward(t *testing.T) {
	a := ft.Listener{Exp: "exp", VM: "a", DstPort: 22}
	b := ft.Listener{Exp: "exp", VM: "b", DstPort: 22}

	useForwards(t, nil, nil, a, b)

	forwardsMu.Lock()
	removeLocked(a)
	removeLocked(ft.Listener{Exp: "exp", VM: "c", DstPort: 22})
	forwardsMu.Unlock()

	if diff := cmp.Diff([]string{b.ToKey()}, reaperKeys()); diff != "" {
		t.Errorf("removeLocked(a) forwards left mismatch (-want +got):\n%s", diff)
	}
}
