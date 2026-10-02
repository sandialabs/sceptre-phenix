package broker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"

	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/util/polltest"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/proto"
	"phenix/web/rbac"
	"phenix/web/util"
)

func TestParseVMListQuery(t *testing.T) {
	tests := map[string]struct {
		payload string
		want    util.VMQuery
	}{
		"empty payload": {
			payload: `{}`,
			want:    util.VMQuery{SortAsc: true},
		},
		"null payload": {
			payload: `null`,
			want:    util.VMQuery{SortAsc: true},
		},
		"only filter": {
			payload: `{"filter": "name:foo"}`,
			want:    util.VMQuery{Filter: "name:foo", SortAsc: true},
		},
		"everything": {
			payload: `{"filter": "x", "show_dnb": true, "sort_column": "name",
				"sort_asc": false, "page_number": 2, "page_size": 10}`,
			want: util.VMQuery{
				Filter: "x", ShowDNB: true, SortCol: "name", SortAsc: false, Page: 2, Size: 10,
			},
		},
		"wrong types": {
			payload: `{"filter": 1, "show_dnb": "yes", "sort_column": true,
				"sort_asc": "no", "page_number": "2", "page_size": "10"}`,
			want: util.VMQuery{SortAsc: true},
		},
		"page without size": {
			payload: `{"page_number": 2}`,
			want:    util.VMQuery{SortAsc: true},
		},
		"zero page": {
			payload: `{"page_number": 0, "page_size": 10}`,
			want:    util.VMQuery{SortAsc: true},
		},
		"negative page": {
			payload: `{"page_number": -1, "page_size": 10}`,
			want:    util.VMQuery{SortAsc: true},
		},
		"fractional size": {
			payload: `{"page_number": 1, "page_size": 2.5}`,
			want:    util.VMQuery{SortAsc: true},
		},
		"huge size": {
			payload: `{"page_number": 1e300, "page_size": 1e300}`,
			want:    util.VMQuery{SortAsc: true},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(tc.payload), &payload); err != nil {
				t.Fatal(err)
			}

			if got := parseVMListQuery(payload); got != tc.want {
				t.Fatalf("parseVMListQuery() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// dialTestClient starts a client with role on a test WebSocket server, running
// its read and write loops, and returns the server side client and the dialed
// connection.
func dialTestClient(t *testing.T, role rbac.Role) (*Client, *websocket.Conn) {
	t.Helper()

	clients := make(chan *Client, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrading connection: %v", err)

			return
		}

		c := NewClient(role, conn)

		go c.read()
		go c.write()

		clients <- c
	}))
	t.Cleanup(srv.Close)

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing websocket: %v", err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })
	t.Cleanup(func() { _ = conn.Close() })

	c := <-clients
	t.Cleanup(c.Stop)

	return c, conn
}

// setVMs puts the VMs in c's view.
func setVMs(c *Client, vms ...vmScope) {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()

	c.vms = vms
}

// vmsInView returns the VMs in c's view.
func vmsInView(c *Client) []vmScope {
	c.vmMu.RLock()
	defer c.vmMu.RUnlock()

	return slices.Clone(c.vms)
}

// The read loop acts on a client's requests in order and skips those it
// cannot use. Each request changes only the client that sent it.
func TestReadHandlesRequests(t *testing.T) {
	const (
		unsubscribeVMs = `{"resource": {"type": "experiment/vms", "name": "exp", "action": "unsubscribe"}}`
		subscribeLogs  = `{"resource": {"type": "log", "name": "phenix", "action": "subscribe"}}`
		resize         = `{"resource": {"type": "metadata/screenshot", "action": "resize"}, "request": {"size": "400"}}`
	)

	var (
		// a role that may do nothing, so list requests stop at the RBAC check
		noRole = rbac.Role{Spec: &v1.RoleSpec{}}
		inView = vmScope{exp: "exp", name: "vm1", running: false}
	)

	noVMs := func(c *Client) bool { return len(vmsInView(c)) == 0 }

	tests := map[string]struct {
		requests []string
		// true once the requests have had their effect; ending with
		// unsubscribeVMs shows the earlier requests were handled
		handled func(*Client) bool
	}{
		"unsubscribing from the VMs stops their screenshots": {
			requests: []string{unsubscribeVMs},
			handled:  noVMs,
		},
		"skips malformed requests": {
			requests: []string{
				`{}`,
				`{"resource": null}`,
				`{"resource": {"type": "experiment/vms", "name": "exp", "action": "list"}}`,
				`{"resource": {"type": "experiment/vms", "name": "exp", "action": "list"}, "request": {}}`,
				`{"resource": {"type": "metadata/screenshot"}, "request": {"size": 5}}`,
				`{"resource": {"type": "log", "action": "tail"}}`,
				unsubscribeVMs,
			},
			handled: noVMs,
		},
		"subscribes to logs": {
			requests: []string{subscribeLogs},
			handled:  func(c *Client) bool { return c.logsSubscribed.Load() },
		},
		"unsubscribes from logs": {
			requests: []string{subscribeLogs, strings.Replace(subscribeLogs, "subscribe", "unsubscribe", 1), unsubscribeVMs},
			handled:  func(c *Client) bool { return noVMs(c) && !c.logsSubscribed.Load() },
		},
		"sets the screenshot size": {
			requests: []string{resize},
			handled:  func(c *Client) bool { return c.screenshotSize() == "400" },
		},
		"rejects a screenshot size minimega cannot take": {
			requests: []string{resize, strings.Replace(resize, "400", "1; vm kill all", 1), unsubscribeVMs},
			handled:  func(c *Client) bool { return noVMs(c) && c.screenshotSize() == "400" },
		},
	}

	bystander, _ := dialTestClient(t, noRole)
	setVMs(bystander, inView)

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c, conn := dialTestClient(t, noRole)
			setVMs(c, inView)

			for _, msg := range tc.requests {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
					t.Fatalf("writing %s: %v", msg, err)
				}
			}

			polltest.Until(t, "the requests are handled", func() bool { return tc.handled(c) })
		})
	}

	if got := vmsInView(bystander); !slices.Equal(got, []vmScope{inView}) {
		t.Errorf("another client's VMs = %v, want %v", got, inView)
	}

	if bystander.logsSubscribed.Load() || bystander.screenshotSize() != defaultBrokerScreenshotSize {
		t.Errorf("another client's logs subscription = %v, screenshot size = %q; want false, %q",
			bystander.logsSubscribed.Load(), bystander.screenshotSize(), defaultBrokerScreenshotSize)
	}
}

// useExperiment stores a stopped experiment named exp with the given VMs.
func useExperiment(t *testing.T, nodes ...map[string]any) {
	t.Helper()

	storetest.Use(t)

	c, _ := store.NewConfig("experiment/exp")
	c.Spec = map[string]any{"experimentName": "exp", "topology": map[string]any{"nodes": nodes}}

	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}
}

// A VM list request answers with the page of the experiment's VMs the client
// may list, sorted and paged as asked, and puts that page in view for
// screenshots.
//
//nolint:paralleltest // replaces the store
func TestListVMs(t *testing.T) {
	node := func(name string, dnb bool) map[string]any {
		return map[string]any{"general": map[string]any{"hostname": name, "do_not_boot": dnb}}
	}

	useExperiment(t, node("a", false), node("b", false), node("hidden", false), node("c", false), node("off", true))

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"vms"}, ResourceNames: []string{"exp/*", "!exp/hidden"}, Verbs: []string{"list"}},
	}}}

	c, conn := dialTestClient(t, role)

	request := `{"resource": {"type": "experiment/vms", "name": "exp", "action": "list"},
		"request": {"sort_column": "name", "sort_asc": false, "page_number": 1, "page_size": 2}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(request)); err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	var reply bt.Publish
	if err := conn.ReadJSON(&reply); err != nil {
		t.Fatalf("reading the VM list: %v", err)
	}

	if *reply.Resource != *bt.NewResource("experiment/vms", "exp", "list") {
		t.Fatalf("reply resource = %+v, want the experiment's VM list", reply.Resource)
	}

	var list proto.VMList
	if err := protojson.Unmarshal(reply.Result, &list); err != nil {
		t.Fatal(err)
	}

	names := make([]string, len(list.GetVms()))
	for i, v := range list.GetVms() {
		names[i] = v.GetName()
	}

	// do-not-boot VMs are left out unless asked for; the total counts every
	// VM the page is taken from
	if !slices.Equal(names, []string{"c", "b"}) || list.GetTotal() != 3 {
		t.Errorf("VM list = %v of %d, want [c b] of 3", names, list.GetTotal())
	}

	want := []vmScope{{exp: "exp", name: "c", running: false}, {exp: "exp", name: "b", running: false}}
	if got := vmsInView(c); !slices.Equal(got, want) {
		t.Errorf("VMs in view = %v, want %v", got, want)
	}
}

// screenshotMM is a minimega that answers every screenshot request with the
// same image and counts the requests.
type screenshotMM struct {
	mm.MM

	calls atomic.Int32
}

func (m *screenshotMM) GetVMScreenshot(...mm.Option) ([]byte, error) {
	m.calls.Add(1)

	return []byte("png"), nil
}

// A client is sent screenshots of the running VMs in view, not of those that
// are stopped, when it asks for a new screenshot size.
//
//nolint:paralleltest // replaces minimega and the web cache
func TestScreenshotsOfRunningVMs(t *testing.T) {
	fake := new(screenshotMM)

	originalMM, originalCache := mm.DefaultMM, cache.DefaultWebCache
	mm.DefaultMM, cache.DefaultWebCache = fake, cache.NewGoWebCache() //nolint:reassign // install test doubles

	t.Cleanup(func() {
		mm.DefaultMM, cache.DefaultWebCache = originalMM, originalCache //nolint:reassign // restore test doubles
	})

	c, conn := dialTestClient(t, rbac.Role{Spec: &v1.RoleSpec{}})
	// the stopped VM comes first, so a screenshot of it would be asked for
	// before the running VM's is sent
	setVMs(c, vmScope{exp: "exp", name: "off", running: false}, vmScope{exp: "exp", name: "on", running: true})

	resize := `{"resource": {"type": "metadata/screenshot", "action": "resize"}, "request": {"size": "300"}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(resize)); err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	var reply bt.Publish
	if err := conn.ReadJSON(&reply); err != nil {
		t.Fatalf("reading the screenshot: %v", err)
	}

	if *reply.Resource != *bt.NewResource("experiment/vm/screenshot", "exp/on", "update") {
		t.Fatalf("reply resource = %+v, want the running VM's screenshot", reply.Resource)
	}

	if got := string(reply.Result); got != `{"screenshot":"data:image/png;base64,cG5n"}` {
		t.Errorf("screenshot = %s", got)
	}

	if n := fake.calls.Load(); n != 1 {
		t.Errorf("minimega was asked for %d screenshots, want 1", n)
	}
}

func TestSendGivesUpOnceClientIsDone(t *testing.T) {
	c := &Client{
		publish: make(chan any), // unbuffered and never drained
		done:    make(chan struct{}),
	}

	sent := make(chan bool)

	go func() {
		sent <- c.send(bt.Publish{})
	}()

	close(c.done)

	select {
	case ok := <-sent:
		if ok {
			t.Fatal("send reported success with no reader")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("send blocked after client was done")
	}
}
