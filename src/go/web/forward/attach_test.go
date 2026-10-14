package forward

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"

	v1 "phenix/types/version/v1"
	"phenix/util/plog"
	ft "phenix/web/forward/forwardtypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// echoListener accepts connections and echoes what each sends, until the
// test ends. It returns its port.
func echoListener(t *testing.T) int {
	t.Helper()

	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	addr, _ := l.Addr().(*net.TCPAddr)

	return addr.Port
}

// forwardAPI serves the port forward websocket route as user with role.
func forwardAPI(t *testing.T, user string, role rbac.Role) string {
	t.Helper()

	router := mux.NewRouter()
	router.HandleFunc("/experiments/{exp}/vms/{name}/forwards/{host}/{port}/ws", GetPortForwardWebSocket)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyRole, role)
		ctx = context.WithValue(ctx, middleware.ContextKeyUser, user)

		router.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(server.Close)

	return server.URL
}

// auditEvents records the audit events logged during the test.
func auditEvents(t *testing.T) func() []map[string]any {
	t.Helper()

	var (
		mu     sync.Mutex
		events []map[string]any
	)

	plog.AddHandler(t.Name(), slog.NewJSONHandler(writerFunc(func(p []byte) (int, error) {
		var e map[string]any
		if err := json.Unmarshal(p, &e); err == nil && e["type"] == string(plog.TypeAudit) {
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
		}

		return len(p), nil
	}), &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { plog.RemoveHandler(t.Name()) })

	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()

		return append([]map[string]any(nil), events...)
	}
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Attaching to a port forward is audited as the attaching user's, naming the
// forward's destination and owner; a refusal is audited too.
//
//nolint:paralleltest // replaces the store, minimega, the forwards and the logger
func TestPortForwardAttachIsAudited(t *testing.T) {
	useForwards(t, []string{"exp"}, nil, ft.Listener{
		Exp: "exp", VM: "vm", SrcPort: 0, DstHost: "10.0.0.1", DstPort: 22, Owner: "alice",
		ClusterHost: "127.0.0.1", ClusterPort: echoListener(t), QEMU: true,
	})

	events := auditEvents(t)

	allowed := rbac.Role{Spec: &v1.RoleSpec{Name: "Viewer", Policies: []*v1.PolicySpec{
		{Resources: []string{"vms/forwards"}, ResourceNames: []string{"exp/vm"}, Verbs: []string{"get"}},
	}}}

	// bob attaches to alice's forward
	api := forwardAPI(t, "bob", allowed)
	path := "/experiments/exp/vms/vm/forwards/10.0.0.1/22/ws"

	ws, err := websocket.Dial("ws"+strings.TrimPrefix(api, "http")+path, "", api)
	if err != nil {
		t.Fatal(err)
	}

	defer ws.Close()

	if _, err := ws.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4)
	if _, err := io.ReadFull(ws, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo: %q, %v", buf, err)
	}

	denied := rbac.Role{Spec: &v1.RoleSpec{Name: "None", Policies: nil}}

	resp, err := http.Get(forwardAPI(t, "carol", denied) + path)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("without permission: got %d, want 403", resp.StatusCode)
	}

	want := map[string]map[string]any{
		"bob": {
			"event.outcome": "success", "phenix.forward.owner": "alice", "destination.address": "10.0.0.1",
			"destination.port": float64(22), "phenix.vm.name": "vm", "url.path": path,
		},
		"carol": {"event.outcome": "failure", "event.reason": "forbidden"},
	}

	for _, e := range events() {
		user, _ := e["user.name"].(string)

		fields, ok := want[user]
		if !ok || e["event.action"] != "port-forward-attach" {
			continue
		}

		for k, v := range fields {
			if e[k] != v {
				t.Errorf("%s's event: %s = %v, want %v", user, k, e[k], v)
			}
		}

		delete(want, user)
	}

	for user := range want {
		t.Errorf("no port-forward-attach event for %s among %v", user, events())
	}
}
