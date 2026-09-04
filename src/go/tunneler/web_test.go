package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/websocket"

	ft "phenix/web/forward/forwardtypes"
)

func TestWebInterfaceListsAndManagesListeners(t *testing.T) {
	previousManager := localListeners
	previousOrigin := origin
	localListeners = newListenerManager()
	origin = "https://phenix.example.test/base"
	defer func() { localListeners = previousManager }()
	defer func() { origin = previousOrigin }()

	listener, created := localListeners.add(ft.Listener{
		Exp: "experiment", VM: "vm", SrcPort: 12345, DstHost: "127.0.0.1", DstPort: 80,
	})

	if !created {
		t.Fatal("listener was not created")
	}

	handler, err := newWebHandler()
	if err != nil {
		t.Fatalf("creating web handler: %v", err)
	}

	response := serveWebRequest(t, handler, http.MethodGet, "/", "")

	if response.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", response.Code, http.StatusOK)
	}

	for _, want := range []string{
		"https://phenix.example.test/base",
		"experiment",
		"vm",
		"127.0.0.1:80",
		"localhost:12345",
		"Enable",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("GET / does not contain %q", want)
		}
	}

	response = serveWebRequest(t, handler, http.MethodPost, "/listeners/1/move", "port=12346")

	if response.Code != http.StatusSeeOther || listener.SrcPort != 12346 {
		t.Fatalf("move response = (%d, port %d), want (%d, port 12346)", response.Code, listener.SrcPort, http.StatusSeeOther)
	}

	port := freeTCPPort(t)
	response = serveWebRequest(t, handler, http.MethodPost, "/listeners/1/move", "port="+strconv.Itoa(port))

	if response.Code != http.StatusSeeOther {
		t.Fatalf("second move status = %d, want %d", response.Code, http.StatusSeeOther)
	}

	response = serveWebRequest(t, handler, http.MethodPost, "/listeners/1/activate", "")

	if response.Code != http.StatusSeeOther || !listener.Listening {
		t.Fatalf("activate response = (%d, listening %t), want redirect and active", response.Code, listener.Listening)
	}

	response = serveWebRequest(t, handler, http.MethodPost, "/listeners/1/deactivate", "")

	if response.Code != http.StatusSeeOther || listener.Listening {
		t.Fatalf("deactivate response = (%d, listening %t), want redirect and inactive", response.Code, listener.Listening)
	}
}

func TestWebInterfaceSendsListenerSnapshots(t *testing.T) {
	previousManager := localListeners
	localListeners = newListenerManager()

	defer func() { localListeners = previousManager }()

	handler, err := newWebHandler()
	if err != nil {
		t.Fatalf("creating web handler: %v", err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"

	conn, err := websocket.Dial(wsURL, "", server.URL)
	if err != nil {
		t.Fatalf("connecting to events websocket: %v", err)
	}

	defer func() { _ = conn.Close() }()

	var initial string

	if err := websocket.Message.Receive(conn, &initial); err != nil {
		t.Fatalf("receiving initial snapshot: %v", err)
	}

	if !strings.Contains(initial, "No listeners are registered.") {
		t.Fatalf("initial snapshot = %q, want empty listener message", initial)
	}

	if _, created := localListeners.add(ft.Listener{Exp: "experiment", VM: "vm", SrcPort: 12345}); !created {
		t.Fatal("listener was not created")
	}

	broadcastWebListeners()

	var update string

	if err := websocket.Message.Receive(conn, &update); err != nil {
		t.Fatalf("receiving listener update: %v", err)
	}

	if !strings.Contains(update, "experiment") || !strings.Contains(update, "localhost:12345") {
		t.Fatalf("listener update = %q, want one experiment listener", update)
	}
}

func TestWebInterfaceReportsInvalidListenerActions(t *testing.T) {
	previousManager := localListeners
	localListeners = newListenerManager()
	defer func() { localListeners = previousManager }()

	handler, err := newWebHandler()
	if err != nil {
		t.Fatalf("creating web handler: %v", err)
	}

	tests := []struct {
		path string
		form string
		want string
	}{
		{path: "/listeners/nope/activate", want: "malformed listener ID"},
		{path: "/listeners/1/activate", want: "listener 1 not found"},
		{path: "/listeners/1/move", form: "port=0", want: "listener port must be between 1 and 65535: 0"},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := serveWebRequest(t, handler, http.MethodPost, test.path, test.form)

			if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want bad request or not found", response.Code)
			}

			if !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("response does not contain %q", test.want)
			}
		})
	}
}

func serveWebRequest(t *testing.T, handler http.Handler, method, path, form string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, strings.NewReader(form))

	if form != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	return response
}
