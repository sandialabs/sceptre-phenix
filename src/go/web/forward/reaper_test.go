package forward

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gorilla/mux"

	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	ft "phenix/web/forward/forwardtypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// tunnelsMM is a minimega that lists the same tunnels for every VM and counts
// the listings. Any other method call panics on the embedded nil interface.
type tunnelsMM struct {
	mm.MM

	tunnels []map[string]string
	calls   int
}

func (m *tunnelsMM) GetTunnels(...mm.Option) []map[string]string {
	m.calls++

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
			t.Fatal(err)
		}
	}

	fake := &tunnelsMM{tunnels: tunnels}

	originalMM := mm.DefaultMM
	mm.DefaultMM = fake //nolint:reassign // install test double

	forwardsMu.Lock()
	saved := forwards
	forwards = make(map[string]ft.Listener)

	for _, l := range forwarded {
		forwards[l.ToKey()] = l
	}

	forwardsMu.Unlock()

	t.Cleanup(func() {
		mm.DefaultMM = originalMM //nolint:reassign // restore test double

		forwardsMu.Lock()
		forwards = saved
		forwardsMu.Unlock()
	})

	return fake
}

// listForwards asks GET /experiments/{exp}/vms/{name}/forwards for the VM's
// forwards, as a user who may list them, and returns their keys.
func listForwards(t *testing.T, exp, vm string) []string {
	t.Helper()

	router := mux.NewRouter()
	router.HandleFunc("/experiments/{exp}/vms/{name}/forwards", GetPortForwards)

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"vms/forwards"}, ResourceNames: []string{"*/*"}, Verbs: []string{"list"}},
	}}}

	req := httptest.NewRequest(http.MethodGet, "/experiments/"+exp+"/vms/"+vm+"/forwards", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "u")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("listing %s/%s forwards: status %d: %s", exp, vm, rec.Code, rec.Body)
	}

	var body struct {
		Listeners []ft.Listener `json:"listeners"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	keys := make([]string, len(body.Listeners))
	for i, l := range body.Listeners {
		keys[i] = l.ToKey()
	}

	slices.Sort(keys)

	return keys
}

// Listing a VM's forwards drops those whose tunnel minimega no longer lists.
// A forward's tunnel matches on each destination part the forward sets, the
// host case-insensitively, and on its own source port, so another user's
// tunnel to the same destination does not keep it. QEMU forwards need no
// tunnel.
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

			got := listForwards(t, "exp", "a")
			if kept := slices.Equal(got, []string{tc.forward.ToKey()}); kept != tc.kept {
				t.Errorf("listed %v, want the forward kept = %v", got, tc.kept)
			}
		})
	}
}

// Reaping asks minimega for the tunnels of running experiments' VMs only, once
// per VM with tunnel-backed forwards. A request reaps only the VM it is about;
// the background reaper reaps them all. A stopped experiment's forwards are
// left to its stop hook, since listing its tunnels would recreate its
// minimega namespace.
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
		before := fake.calls

		got := step.reap()

		want := make([]string, len(step.want))
		for i, l := range step.want {
			want[i] = l.ToKey()
		}

		slices.Sort(want)

		if !slices.Equal(got, want) {
			t.Errorf("%s: forwards = %v, want %v", step.name, got, want)
		}

		if n := fake.calls - before; n != step.listings {
			t.Errorf("%s: minimega listed tunnels %d times, want %d", step.name, n, step.listings)
		}
	}
}

// reapAll runs the background reaper and returns the keys of the forwards
// left.
func reapAll() []string {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	reapForwards()

	keys := make([]string, 0, len(forwards))
	for key := range forwards {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
