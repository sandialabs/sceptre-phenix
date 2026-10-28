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

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"pgregory.net/rapid"

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
				t.Fatalf("decoding payload %s: %v", tc.payload, err)
			}

			if diff := cmp.Diff(tc.want, parseVMListQuery(payload)); diff != "" {
				t.Errorf("parseVMListQuery(%s) mismatch (-want +got):\n%s", tc.payload, diff)
			}
		})
	}
}

// clientQueryPage is a page_number or page_size as JSON decodes it, and
// whether it is a whole number from 1 to util.MaxPageValue.
type clientQueryPage struct {
	value any
	valid bool
}

func genClientQueryPage() *rapid.Generator[clientQueryPage] {
	return rapid.Custom(func(t *rapid.T) clientQueryPage {
		if rapid.Bool().Draw(t, "valid") {
			edges := rapid.SampledFrom([]int{1, util.MaxPageValue})
			n := rapid.OneOf(edges, rapid.IntRange(1, 3), rapid.IntRange(1, util.MaxPageValue)).Draw(t, "n")

			return clientQueryPage{value: float64(n), valid: true}
		}

		n := float64(rapid.IntRange(0, util.MaxPageValue).Draw(t, "n"))
		invalid := rapid.SampledFrom([]any{
			nil, "2", true, 0.0, -n, n + 0.5, float64(util.MaxPageValue) + 1, n + float64(util.MaxPageValue) + 1, 1e300,
		}).Draw(t, "invalid")

		return clientQueryPage{value: invalid, valid: false}
	})
}

// A query is paged only when both its page and size are whole numbers from 1
// to util.MaxPageValue, and then by exactly those.
func TestParseVMListQueryPaging(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		page, size := genClientQueryPage().Draw(t, "page"), genClientQueryPage().Draw(t, "size")

		payload := map[string]any{"page_number": page.value, "page_size": size.value}

		want := [2]int{}
		if page.valid && size.valid {
			want = [2]int{int(page.value.(float64)), int(size.value.(float64))}
		}

		got := parseVMListQuery(payload)
		if paging := [2]int{got.Page, got.Size}; paging != want {
			t.Fatalf("parseVMListQuery(%v) page, size = %v, want %v", payload, paging, want)
		}
	})
}

// dialTestClient serves a client with role over a test WebSocket and returns
// it with the dialed connection.
func dialTestClient(t *testing.T, role rbac.Role) (*Client, *websocket.Conn) {
	t.Helper()

	clients := make(chan *Client, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrading connection: %v", err)

			return
		}

		c := NewClient(role, "", conn)

		go c.read()
		go c.write()
		go c.work()

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
		"rounds the screenshot size up to a shared width": {
			requests: []string{strings.Replace(resize, "400", "250", 1)},
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
					t.Fatalf("conn.WriteMessage(%s) = %v", msg, err)
				}
			}

			polltest.Until(t, "the requests are handled", func() bool { return tc.handled(c) })
		})
	}

	if diff := cmp.Diff([]vmScope{inView}, vmsInView(bystander), cmp.AllowUnexported(vmScope{})); diff != "" {
		t.Errorf("after other clients' requests, the bystander's VMs in view mismatch (-want +got):\n%s", diff)
	}

	if bystander.logsSubscribed.Load() || bystander.screenshotSize() != defaultBrokerScreenshotSize {
		t.Errorf("after other clients' requests, the bystander's logs subscription = %v, screenshot size = %q; want false, %q",
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
		t.Fatalf("store.Create(experiment/exp) = %v", err)
	}
}

// A VM list answers with the page of VMs the client may list, sorted and
// paged, and puts it in view.
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
		t.Fatalf("conn.WriteMessage(%s) = %v", request, err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("conn.SetReadDeadline() = %v", err)
	}

	var reply bt.Publish
	if err := conn.ReadJSON(&reply); err != nil {
		t.Fatalf("reading the reply to %s: %v", request, err)
	}

	if diff := cmp.Diff(bt.NewResource("experiment/vms", "exp", "list"), reply.Resource); diff != "" {
		t.Fatalf("reply to the VM list request: resource mismatch (-want +got):\n%s", diff)
	}

	var list proto.VMList
	if err := protojson.Unmarshal(reply.Result, &list); err != nil {
		t.Fatalf("decoding the VM list %s: %v", reply.Result, err)
	}

	type page struct {
		Names []string
		Total uint32
	}

	got := page{Names: make([]string, len(list.GetVms())), Total: list.GetTotal()}
	for i, v := range list.GetVms() {
		got.Names[i] = v.GetName()
	}

	// do-not-boot VMs are left out unless asked for; the total counts every
	// VM the page is taken from
	if diff := cmp.Diff(page{Names: []string{"c", "b"}, Total: 3}, got); diff != "" {
		t.Errorf("VM list reply to %s mismatch (-want +got):\n%s", request, diff)
	}

	want := []vmScope{{exp: "exp", name: "c", running: false}, {exp: "exp", name: "b", running: false}}
	if diff := cmp.Diff(want, vmsInView(c), cmp.AllowUnexported(vmScope{})); diff != "" {
		t.Errorf("VMs in view after the VM list mismatch (-want +got):\n%s", diff)
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
		t.Fatalf("conn.WriteMessage(%s) = %v", resize, err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("conn.SetReadDeadline() = %v", err)
	}

	var reply bt.Publish
	if err := conn.ReadJSON(&reply); err != nil {
		t.Fatalf("reading the reply to %s: %v", resize, err)
	}

	if diff := cmp.Diff(bt.NewResource("experiment/vm/screenshot", "exp/on", "update"), reply.Resource); diff != "" {
		t.Fatalf("reply to the resize: resource mismatch (-want +got):\n%s", diff)
	}

	var result map[string]any
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatalf("decoding the screenshot %s: %v", reply.Result, err)
	}

	if diff := cmp.Diff(map[string]any{"screenshot": "data:image/png;base64,cG5n"}, result); diff != "" {
		t.Errorf("screenshot reply to the resize mismatch (-want +got):\n%s", diff)
	}

	if n := fake.calls.Load(); n != 1 {
		t.Errorf("resize to 300 asked minimega for %d screenshots, want 1", n)
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
			t.Error("Client.send() with no reader after done = true, want false")
		}
	case <-time.After(2 * time.Second):
		t.Error("Client.send() still blocked 2s after the client was done, want it to return false")
	}
}
