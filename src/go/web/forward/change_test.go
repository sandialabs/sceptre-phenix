package forward

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/app"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
	"phenix/util/pubsub"
	ft "phenix/web/forward/forwardtypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// changeMM is the real minimega for tunnel commands, which go to the fake
// minimega, with the VM's host, VNC endpoint and tunnels given. Every host is
// the headnode.
type changeMM struct {
	mm.Minimega

	host    string
	hostErr error
	vnc     string
	vncErr  error
	tunnels []map[string]string
}

func (*changeMM) IsHeadnode(string) bool { return true }

func (m *changeMM) GetVMHost(...mm.Option) (string, error) { return m.host, m.hostErr }

func (m *changeMM) GetVNCEndpoint(...mm.Option) (string, error) { return m.vnc, m.vncErr }

func (m *changeMM) GetTunnels(...mm.Option) []map[string]string { return m.tunnels }

// useChangeMM installs fake as minimega, with the fake minimega answering
// each tunnel command with the error of failing, if any; it returns the
// commands the fake minimega received.
func useChangeMM(t *testing.T, fake *changeMM, failing map[string]string) func() []string {
	t.Helper()

	mm.DefaultMM = fake //nolint:reassign // install test double; useForwards restores minimega

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		for prefix, msg := range failing {
			if strings.HasPrefix(strings.TrimSpace(cmd.Base), prefix) {
				return []*minicli.Response{mmtest.Error("head", msg)}
			}
		}

		return nil
	})

	return func() []string {
		bases := mmtest.Bases(received())
		for i, base := range bases {
			bases[i] = strings.TrimSpace(base)
		}

		return bases
	}
}

// serveChange serves a forward change of VM vm in experiment exp as user u,
// who may create and delete every forward, or none when forbidden.
func serveChange(t *testing.T, handler weberror.ErrorHandler, method, target string, forbidden bool) *httptest.ResponseRecorder {
	t.Helper()

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"vms/forwards"}, ResourceNames: []string{"*/*"}, Verbs: []string{"create", "delete"}},
	}}}

	if forbidden {
		role = rbac.Role{Spec: &v1.RoleSpec{}}
	}

	ctx := context.WithValue(t.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "u")

	req := httptest.NewRequestWithContext(ctx, method, target, nil)
	req = mux.SetURLVars(req, map[string]string{"exp": "exp", "name": "vm"})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// heldForwards returns the forwards held, sorted by key.
func heldForwards() []ft.Listener {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	held := make([]ft.Listener, 0, len(forwards))
	for _, l := range forwards {
		held = append(held, l)
	}

	slices.SortFunc(held, func(a, b ft.Listener) int { return strings.Compare(a.ToKey(), b.ToKey()) })

	return held
}

// tunnelPort returns the cluster port of the only forward held, 0 if there is
// not exactly one.
func tunnelPort() int {
	held := heldForwards()
	if len(held) != 1 {
		return 0
	}

	return held[0].ClusterPort
}

// Creating a forward opens a tunnel from a free cluster port to the host
// asked for (127.0.0.1 by default), or uses the VM's VNC port, and keeps it as
// the user's. A refused forward keeps nothing.
//
//nolint:paralleltest // replaces the store, minimega, the fake minimega and the forwards
func TestCreatePortForward(t *testing.T) {
	const path = "/experiments/exp/vms/vm/forwards"

	tunnelTo := func(host string, dst, src int) ft.Listener {
		return ft.Listener{
			Exp: "exp", VM: "vm", SrcPort: src, DstHost: host, DstPort: dst, Owner: "u", ClusterHost: "node1",
		}
	}

	existing := ft.Listener{Exp: "exp", VM: "vm", DstHost: "127.0.0.1", DstPort: 5900, Owner: "u", QEMU: true}

	tests := map[string]struct {
		query     string
		fake      changeMM
		failing   map[string]string
		forwarded []ft.Listener
		forbidden bool

		status int
		// the forwards held after, with the cluster port of a new tunnel 0
		want []ft.Listener
		// the tunnel command, with %d for the new tunnel's cluster port
		command string
	}{
		"tunnel to the default host": {
			query: "?src=2222&dst=22", status: http.StatusNoContent,
			want: []ft.Listener{tunnelTo("127.0.0.1", 22, 2222)}, command: "cc tunnel vm %d 127.0.0.1 22",
		},
		"tunnel to a host": {
			query: "?src=8080&host=10.0.0.5&dst=80", status: http.StatusNoContent,
			want: []ft.Listener{tunnelTo("10.0.0.5", 80, 8080)}, command: "cc tunnel vm %d 10.0.0.5 80",
		},
		"VNC console": {
			query: "?src=5901&dst=vnc", fake: changeMM{vnc: "node2:5905"}, status: http.StatusNoContent,
			want: []ft.Listener{{
				Exp: "exp", VM: "vm", SrcPort: 5901, DstHost: "127.0.0.1", DstPort: 5900, Owner: "u",
				ClusterHost: "node2", ClusterPort: 5905, QEMU: true,
			}},
		},
		"host minimega would expand":    {query: "?src=2222&host=%24HOST&dst=22", status: http.StatusBadRequest},
		"host not a host name":          {query: "?src=2222&host=a%3Bb&dst=22", status: http.StatusBadRequest},
		"source port not a number":      {query: "?src=x&dst=22", status: http.StatusBadRequest},
		"destination port not a number": {query: "?src=2222&dst=ssh", status: http.StatusBadRequest},
		"VM not found": {
			query: "?src=2222&dst=22", fake: changeMM{hostErr: errors.New("vm vm not found")}, status: http.StatusBadRequest,
		},
		"no VNC endpoint": {
			query: "?src=5901&dst=VNC", fake: changeMM{vncErr: errors.New("not found")}, status: http.StatusBadRequest,
		},
		"the user's forward exists": {
			query: "?src=5901&dst=vnc", fake: changeMM{vnc: "node2:5905"}, forwarded: []ft.Listener{existing},
			status: http.StatusBadRequest, want: []ft.Listener{existing},
		},
		"tunnel fails": {
			query: "?src=2222&dst=22", failing: map[string]string{"cc tunnel vm": "vm not running"},
			status: http.StatusBadRequest, command: "cc tunnel vm %d 127.0.0.1 22",
		},
		"forbidden": {query: "?src=2222&dst=22", forbidden: true, status: http.StatusForbidden},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useForwards(t, []string{"exp"}, nil, tc.forwarded...)

			fake := tc.fake
			if fake.host == "" {
				fake.host = "node1"
			}

			received := useChangeMM(t, &fake, tc.failing)
			before := reaperQueued()

			rec := serveChange(t, CreatePortForward, http.MethodPost, path+tc.query, tc.forbidden)
			if rec.Code != tc.status {
				t.Errorf("POST %s%s = %d %s, want %d", path, tc.query, rec.Code, rec.Body, tc.status)
			}

			commands := received()

			var wantCommands []string

			if tc.command != "" {
				port := tunnelPort()
				if tc.status != http.StatusNoContent && len(commands) == 1 {
					// a refused tunnel leaves no forward to read its port from
					_, _ = fmt.Sscanf(commands[0], tc.command, &port)
				}

				if port < forwardPortBase || port >= forwardPortBase+forwardPortRange {
					t.Errorf("POST %s%s tunnel cluster port = %d, want one in [%d, %d)", path, tc.query, port,
						forwardPortBase, forwardPortBase+forwardPortRange)
				}

				wantCommands = []string{fmt.Sprintf(tc.command, port)}
			}

			if diff := cmp.Diff(wantCommands, commands, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("POST %s%s minimega commands mismatch (-want +got):\n%s", path, tc.query, diff)
			}

			ignorePort := cmpopts.IgnoreFields(ft.Listener{}, "ClusterPort")
			if tc.fake.vnc != "" {
				ignorePort = nil
			}

			if diff := cmp.Diff(tc.want, heldForwards(), cmpopts.EquateEmpty(), ignorePort); diff != "" {
				t.Errorf("POST %s%s forwards mismatch (-want +got):\n%s", path, tc.query, diff)
			}

			wantAnnounced := 0
			if tc.status == http.StatusNoContent {
				wantAnnounced = 1
			}

			if got := reaperQueued() - before; got != wantAnnounced {
				t.Errorf("POST %s%s announced %d forwards, want %d", path, tc.query, got, wantAnnounced)
			}

			forwardsMu.Lock()
			defer forwardsMu.Unlock()

			if len(pending) != 0 {
				t.Errorf("POST %s%s left forwards pending: %v", path, tc.query, pending)
			}
		})
	}
}

// Closing a forward closes its tunnel and forgets it. A forward whose tunnel
// fails to close is kept while its experiment runs; only the owner closes a
// forward.
//
//nolint:paralleltest // replaces the store, minimega, the fake minimega and the forwards
func TestDeletePortForward(t *testing.T) {
	const path = "/experiments/exp/vms/vm/forwards"

	var (
		tunnel = ft.Listener{
			Exp: "exp", VM: "vm", SrcPort: 2222, DstHost: "127.0.0.1", DstPort: 22, Owner: "u",
			ClusterHost: "node1", ClusterPort: 50001,
		}
		toHost = ft.Listener{
			Exp: "exp", VM: "vm", SrcPort: 8080, DstHost: "10.0.0.5", DstPort: 80, Owner: "u",
			ClusterHost: "node1", ClusterPort: 50002,
		}
		qemu      = ft.Listener{Exp: "exp", VM: "vm", DstHost: "127.0.0.1", DstPort: 5900, Owner: "u", QEMU: true}
		othersVNC = ft.Listener{Exp: "exp", VM: "vm", DstHost: "127.0.0.1", DstPort: 5900, Owner: "v", QEMU: true}
		// the VM's tunnels, as minimega lists those of one forward
		tunnelRow = []map[string]string{{"id": "7", "src port": "50001", "dst": "127.0.0.1", "dst port": "22"}}
		toHostRow = []map[string]string{{"id": "8", "src port": "50002", "dst": "10.0.0.5", "dst port": "80"}}
	)

	tests := map[string]struct {
		query     string
		running   bool
		tunnels   []map[string]string
		fake      changeMM
		failing   map[string]string
		forwarded []ft.Listener
		forbidden bool

		status    int
		want      []ft.Listener
		commands  []string
		announced int
	}{
		"tunnel to the default host": {
			query: "?dst=22", running: true, tunnels: tunnelRow, forwarded: []ft.Listener{tunnel, othersVNC},
			status: http.StatusNoContent, want: []ft.Listener{othersVNC}, commands: []string{"cc tunnel close vm 7"},
			announced: 1,
		},
		"tunnel to a host": {
			query: "?host=10.0.0.5&dst=80", running: true, tunnels: toHostRow, forwarded: []ft.Listener{toHost, othersVNC},
			status: http.StatusNoContent, want: []ft.Listener{othersVNC}, commands: []string{"cc tunnel close vm 8"},
			announced: 1,
		},
		"VNC console": {
			query: "?dst=5900", running: true, forwarded: []ft.Listener{qemu, othersVNC},
			status: http.StatusNoContent, want: []ft.Listener{othersVNC}, announced: 1,
		},
		"tunnel fails to close": {
			query: "?dst=22", running: true, tunnels: tunnelRow, forwarded: []ft.Listener{tunnel},
			failing: map[string]string{"cc tunnel close": "tunnel busy"},
			status:  http.StatusInternalServerError, want: []ft.Listener{tunnel}, commands: []string{"cc tunnel close vm 7"},
		},
		"tunnel fails to close after the experiment stopped": {
			query: "?dst=22", tunnels: tunnelRow, forwarded: []ft.Listener{tunnel},
			failing: map[string]string{"cc tunnel close": "tunnel busy"},
			status:  http.StatusInternalServerError, commands: []string{"cc tunnel close vm 7"},
		},
		"another user's forward": {
			query: "?dst=5900", running: true, forwarded: []ft.Listener{othersVNC},
			status: http.StatusBadRequest, want: []ft.Listener{othersVNC},
		},
		"no forward": {query: "?dst=22", running: true, status: http.StatusBadRequest},
		"host minimega would expand": {
			query: "?host=%24%7BHOST%7D&dst=22", running: true, tunnels: tunnelRow, forwarded: []ft.Listener{tunnel},
			status: http.StatusBadRequest, want: []ft.Listener{tunnel},
		},
		"destination port not a number": {
			query: "?dst=ssh", running: true, tunnels: tunnelRow, forwarded: []ft.Listener{tunnel},
			status: http.StatusBadRequest, want: []ft.Listener{tunnel},
		},
		"VM not found": {
			query: "?dst=22", running: true, fake: changeMM{hostErr: errors.New("vm vm not found")},
			tunnels: tunnelRow, forwarded: []ft.Listener{tunnel}, status: http.StatusNotFound, want: []ft.Listener{tunnel},
		},
		"forbidden": {
			query: "?dst=22", running: true, tunnels: tunnelRow, forwarded: []ft.Listener{tunnel}, forbidden: true,
			status: http.StatusForbidden, want: []ft.Listener{tunnel},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var running []string
			if tc.running {
				running = []string{"exp"}
			}

			useForwards(t, running, nil, tc.forwarded...)

			fake := tc.fake
			fake.host, fake.tunnels = "node1", tc.tunnels

			received := useChangeMM(t, &fake, tc.failing)
			before := reaperQueued()

			rec := serveChange(t, DeletePortForward, http.MethodDelete, path+tc.query, tc.forbidden)
			if rec.Code != tc.status {
				t.Errorf("DELETE %s%s = %d %s, want %d", path, tc.query, rec.Code, rec.Body, tc.status)
			}

			if diff := cmp.Diff(tc.commands, received(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("DELETE %s%s minimega commands mismatch (-want +got):\n%s", path, tc.query, diff)
			}

			if diff := cmp.Diff(tc.want, heldForwards(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("DELETE %s%s forwards mismatch (-want +got):\n%s", path, tc.query, diff)
			}

			if got := reaperQueued() - before; got != tc.announced {
				t.Errorf("DELETE %s%s announced %d deletes, want %d", path, tc.query, got, tc.announced)
			}
		})
	}
}

// Taking a forward gives the user's own forward and forgets it; it refuses a
// missing forward and one held under the user's key for another owner.
//
//nolint:paralleltest // replaces the forwards
func TestTakeForward(t *testing.T) {
	own := ft.Listener{Exp: "exp", VM: "vm", DstHost: "127.0.0.1", DstPort: 22, Owner: "u"}
	other := own
	other.Owner = "v"

	tests := map[string]struct {
		held       map[string]ft.Listener
		wantStatus int
		wantHeld   []string
	}{
		"own":     {held: map[string]ft.Listener{own.ToKey(): own}, wantHeld: []string{}},
		"missing": {held: map[string]ft.Listener{}, wantStatus: http.StatusBadRequest, wantHeld: []string{}},
		"another owner under the user's key": {
			held: map[string]ft.Listener{own.ToKey(): other}, wantStatus: http.StatusForbidden, wantHeld: []string{own.ToKey()},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useForwards(t, nil, nil)

			forwardsMu.Lock()
			forwards = tc.held
			forwardsMu.Unlock()

			l, status, refusal := takeForward(own.ToKey(), "u")
			if status != tc.wantStatus || (status == 0) != (refusal == "") {
				t.Errorf("takeForward(%s, u) = status %d %q, want %d", own.ToKey(), status, refusal, tc.wantStatus)
			}

			if status == 0 && l != own {
				t.Errorf("takeForward(%s, u) = %+v, want %+v", own.ToKey(), l, own)
			}

			if diff := cmp.Diff(tc.wantHeld, reaperKeys()); diff != "" {
				t.Errorf("takeForward(%s, u) forwards left mismatch (-want +got):\n%s", own.ToKey(), diff)
			}
		})
	}
}

// A forward whose tunnel failed to close comes back, unless its experiment
// stopped or its key was taken or reserved meanwhile.
//
//nolint:paralleltest // replaces the store and the forwards
func TestRestoreForward(t *testing.T) {
	l := ft.Listener{Exp: "exp", VM: "vm", DstHost: "127.0.0.1", DstPort: 22, Owner: "u", ClusterPort: 50001}
	recreated := l
	recreated.ClusterPort = 50002

	tests := map[string]struct {
		running []string
		held    []ft.Listener
		pending bool
		want    []ft.Listener
	}{
		"running":          {running: []string{"exp"}, want: []ft.Listener{l}},
		"stopped":          {want: []ft.Listener{}},
		"recreated":        {running: []string{"exp"}, held: []ft.Listener{recreated}, want: []ft.Listener{recreated}},
		"being recreated":  {running: []string{"exp"}, pending: true, want: []ft.Listener{}},
		"other experiment": {running: []string{"other"}, want: []ft.Listener{}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useForwards(t, tc.running, nil, tc.held...)

			if tc.pending {
				forwardsMu.Lock()
				pending[l.ToKey()] = l.Exp
				forwardsMu.Unlock()
			}

			restoreForward(l)

			if diff := cmp.Diff(tc.want, heldForwards(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("restoreForward(%+v) forwards mismatch (-want +got):\n%s", l, diff)
			}
		})
	}
}

// A forward an app asks for on the create-tunnel topic is created as the
// app's user's.
//
//nolint:paralleltest // replaces the store, minimega, the fake minimega and the forwards
func TestAppTunnelCreatesForward(t *testing.T) {
	useForwards(t, []string{"exp"}, nil)

	fake := &changeMM{host: "node1", vnc: "node1:5901"}
	received := useChangeMM(t, fake, nil)

	pubsub.Publish("create-tunnel", app.CreateTunnel{
		Experiment: "exp", VM: "vm", Sport: "5901", Dhost: "", Dport: "vnc", User: "app-user",
	})

	polltest.Until(t, "the app's forward is created", func() bool { return len(heldForwards()) > 0 })

	want := []ft.Listener{{
		Exp: "exp", VM: "vm", SrcPort: 5901, DstHost: "127.0.0.1", DstPort: 5900, Owner: "app-user",
		ClusterHost: "node1", ClusterPort: 5901, QEMU: true,
	}}

	if diff := cmp.Diff(want, heldForwards()); diff != "" {
		t.Errorf("forwards after a create-tunnel publication mismatch (-want +got):\n%s", diff)
	}

	if got := received(); len(got) != 0 {
		t.Errorf("a VNC forward sent minimega %q, want nothing", got)
	}
}

// A forward an app asks for that cannot be created is logged with what it
// asked for, and leaves nothing behind.
//
//nolint:paralleltest // replaces the store, minimega, the fake minimega, the forwards and the logger
func TestAppTunnelFailureKeepsNothing(t *testing.T) {
	useForwards(t, []string{"exp"}, nil)

	fake := &changeMM{host: "node1"}
	_ = useChangeMM(t, fake, map[string]string{"cc tunnel vm": "vm not running"})

	logs := plogtest.Capture(t)

	createAppForward(app.CreateTunnel{Experiment: "exp", VM: "vm", Sport: "2222", Dhost: "10.0.0.5", Dport: "22", User: "u"})

	if held := heldForwards(); len(held) != 0 {
		t.Errorf("createAppForward with a failing tunnel kept %+v, want none", held)
	}

	want := []map[string]any{{"exp": "exp", "vm": "vm", "sport": "2222", "host": "10.0.0.5", "dport": "22"}}

	records := logs.Records(t, plogtest.Message("adding port forward"))
	got := make([]map[string]any, 0, len(records))

	for _, record := range records {
		got = append(got, map[string]any{
			"exp": record["exp"], "vm": record["vm"], "sport": record["sport"], "host": record["host"], "dport": record["dport"],
		})
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("createAppForward with a failing tunnel logs mismatch (-want +got):\n%s", diff)
	}

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	if len(pending) != 0 {
		t.Errorf("createAppForward with a failing tunnel left pending %v, want none", pending)
	}
}
