package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"inet.af/netaddr"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/types"
	"phenix/util/cache"
	"phenix/util/mm"
	"phenix/util/plog"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

var marshaler = protojson.MarshalOptions{EmitUnpopulated: true} //nolint:gochecknoglobals // global marshaler

// clientDeps are what a client's requests ask of the store and minimega.
type clientDeps struct {
	experiment func(name string) (*types.Experiment, error)
	vms        func(exp *types.Experiment) []mm.VM
	// warms the experiment's topology search cache
	topology   func(exp string) error
	screenshot func(ctx context.Context, exp, vm, size string) ([]byte, error)
}

//nolint:gochecknoglobals // replaced in tests
var deps = clientDeps{
	experiment: experiment.Get,
	vms:        vm.ListFor,
	topology: func(exp string) error {
		_, err := vm.Topology(exp, nil)

		return err //nolint:wrapcheck // logged as is
	},
	screenshot: util.GetScreenshotContext,
}

type vmScope struct {
	exp  string
	name string
	// only running VMs are screenshotted; kept current by the VM messages sent
	// to the client
	running bool
}

// vmState is the state an experiment/vm message leaves a VM in.
type vmState struct {
	exp     string
	name    string
	running bool
}

const (
	writeWait             = 10 * time.Second
	pongWait              = 60 * time.Second
	pingPeriodNumerator   = 9
	pingPeriodDenominator = 10
	// how long a dropped client gets to take its close frame before its
	// connection is closed under it.
	closeWait            = 1 * time.Second
	maxMsgSize           = 2048
	socketBufferSize     = 4096
	publishChannelBuffer = 256
	// requests waiting for the client's worker; more are dropped.
	clientJobQueue = 8
	// just longer than the screenshot cache, so each round takes fresh
	// screenshots while clients whose rounds fall within one cache life share
	// them; every one is a minimega command, which blocks all others.
	screenshotTickerInterval    = util.ScreenshotCacheDuration + time.Second
	defaultBrokerScreenshotSize = "200"
)

// pingPeriod is how often the client is pinged; shortened in tests.
var pingPeriod = (pongWait * pingPeriodNumerator) / pingPeriodDenominator //nolint:gochecknoglobals // see above

var (
	newline  = []byte{'\n'}        //nolint:gochecknoglobals // global constant
	upgrader = websocket.Upgrader{ //nolint:gochecknoglobals,exhaustruct // global upgrader
		ReadBufferSize:  socketBufferSize,
		WriteBufferSize: socketBufferSize,
	}
)

type Client struct {
	role rbac.Role
	// only write writes data frames to it
	conn wsConn

	publish chan any
	done    chan struct{}
	once    sync.Once

	// requests read hands to work
	jobs chan bt.Request

	// Track the VMs this client currently has in view, if any, so we know
	// what screenshots need to periodically be pushed to the client over
	// the WebSocket connection.
	vms  []vmScope
	vmMu sync.RWMutex

	// held while screenshots are being pushed; shotAgain asks for another round
	shotMu    sync.Mutex
	shotAgain atomic.Bool

	// the size this client wants its screenshots in, set by its VNC zoom
	shotSize   string
	shotSizeMu sync.RWMutex

	// whether the client shows the logs page and wants log lines
	logsSubscribed atomic.Bool
}

func NewClient(role rbac.Role, conn *websocket.Conn) *Client {
	return &Client{ //nolint:exhaustruct // partial initialization
		role:     role,
		conn:     conn,
		publish:  make(chan any, publishChannelBuffer),
		done:     make(chan struct{}),
		jobs:     make(chan bt.Request, clientJobQueue),
		shotSize: defaultBrokerScreenshotSize,
	}
}

// send queues msg for the client, giving up once the client is gone so the
// sender does not block forever on a publish channel no one drains.
func (c *Client) send(msg bt.Publish) bool {
	select {
	case c.publish <- msg:
		return true
	case <-c.done:
		return false
	}
}

func (c *Client) screenshotSize() string {
	c.shotSizeMu.RLock()
	defer c.shotSizeMu.RUnlock()

	return c.shotSize
}

func (c *Client) setScreenshotSize(size string) {
	c.shotSizeMu.Lock()
	defer c.shotSizeMu.Unlock()

	c.shotSize = size
}

func (c *Client) Go() {
	register <- c

	go c.write()
	go c.read()
	go c.work()
	go c.setScreenshotsTicker()
}

// Stop disconnects the client. The client's own goroutines call it when they
// end; the broker uses shutdown instead.
func (c *Client) Stop() {
	c.shutdown(websocket.CloseNormalClosure, "")

	// the broker also forgets a client once it sees done closed, so this can
	// give up when the broker is behind
	select {
	case unregister <- c:
	default:
	}
}

// shutdown marks the client done and closes its connection on another
// goroutine, so the caller, which may be the broker, never waits on the
// socket. Only the first call does anything.
func (c *Client) shutdown(code int, reason string) {
	c.once.Do(func() {
		close(c.done)

		go c.closeConn(code, reason)
	})
}

// closeConn sends the close frame, then closes the connection, which ends a
// write stuck on a client that stopped reading.
func (c *Client) closeConn(code int, reason string) {
	msg := websocket.FormatCloseMessage(code, reason)

	err := c.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(closeWait))
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) {
		plog.Debug(plog.TypeSystem, "sending close to WebSocket client", "code", code, "err", err)
	}

	_ = c.conn.Close()

	if code == websocket.CloseTryAgainLater {
		plog.Debug(plog.TypeSystem, "dropped WebSocket client", "reason", reason)
	}
}

// read takes the client's requests. It does only cheap work itself and hands
// requests that ask minimega or the store to the client's worker, so it keeps
// reading, and answering pings, however long those take.
//
//nolint:funlen // one case per request type
func (c *Client) read() {
	defer c.Stop()

	c.conn.SetReadLimit(maxMsgSize)

	err := c.conn.SetReadDeadline(time.Now().Add(pongWait))
	if err != nil {
		plog.Error(plog.TypeSystem, "setting read deadline for client connection", "err", err)

		return
	}

	ponger := func(string) error {
		err := c.conn.SetReadDeadline(time.Now().Add(pongWait))
		if err != nil {
			plog.Error(
				plog.TypeSystem,
				"setting read deadline in pong handler for client connection",
				"err",
				err,
			)

			return err
		}

		return nil
	}

	c.conn.SetPongHandler(ponger)

	for {
		select {
		case <-c.done:
			return
		default:
			_, msg, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(
					err,
					websocket.CloseGoingAway,
					websocket.CloseAbnormalClosure,
				) {
					plog.Debug(plog.TypeSystem, "reading from WebSocket client", "err", err)
				}

				return
			}

			var req bt.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				plog.Error(plog.TypeSystem, "cannot unmarshal request JSON", "err", err)

				continue
			}

			if req.Resource == nil {
				plog.Error(plog.TypeSystem, "WebSocket request missing resource")

				continue
			}

			switch req.Resource.Type {
			case logResourceType:
				// the broker checks the role before it sends each log line
				switch req.Resource.Action {
				case "subscribe":
					c.logsSubscribed.Store(true)
				case "unsubscribe":
					c.logsSubscribed.Store(false)
				default:
					plog.Error(
						plog.TypeSystem,
						"unexpected WebSocket request resource action for log resource type",
						"action",
						req.Resource.Action,
					)
				}
			case vmsResourceType:
				switch req.Resource.Action {
				case "unsubscribe":
					// Sent when the client leaves the experiment's VM table. Without
					// it the screenshot ticker keeps queuing minimega commands for
					// VMs no longer on screen, delaying every other API request.
					c.clearVMs()
				case "list":
					c.queue(req)
				default:
					plog.Error(
						plog.TypeSystem,
						"unexpected WebSocket request resource action",
						"action",
						req.Resource.Action,
					)
				}
			case "metadata/screenshot":
				var payload map[string]string

				err := json.Unmarshal(req.Payload, &payload)
				if err != nil {
					plog.Error(
						plog.TypeSystem,
						"cannot unmarshal WebSocket request payload JSON",
						"err",
						err,
					)

					continue
				}

				size, ok := payload["size"]
				if ok {
					if !util.ValidScreenshotSize(size) {
						plog.Error(plog.TypeSystem, "invalid screenshot resolution", "size", size)

						continue
					}

					plog.Debug(plog.TypeSystem, "updated screenshot resolution", "size", size)
					c.setScreenshotSize(size)

					go c.updateScreenshots()
				}
			case "experiment/topology":
				// Same permission as GET /experiments/{name}/topology.
				if !c.role.Allowed("experiments/topology", "get", req.Resource.Name) {
					plog.Warn(
						plog.TypeSecurity,
						"topology search denied for WebSocket client",
						"exp",
						req.Resource.Name,
					)

					continue
				}

				if req.Resource.Action != "search" {
					plog.Error(
						plog.TypeSystem,
						"unexpected WebSocket request resource action for experiment/topology resource type",
						"action",
						req.Resource.Action,
					)

					continue
				}

				c.queue(req)
			default:
				plog.Error(
					plog.TypeSystem,
					"unexpected WebSocket request resource type",
					"type",
					req.Resource.Type,
				)
			}
		}
	}
}

// queue hands req to the client's worker, dropping it when the worker is that
// far behind: the client asks again when it next needs the answer.
func (c *Client) queue(req bt.Request) {
	select {
	case c.jobs <- req:
	default:
		plog.Debug(
			plog.TypeSystem,
			"dropping WebSocket request while earlier ones are answered",
			"type", req.Resource.Type,
			"name", req.Resource.Name,
		)
	}
}

// work answers the requests read queues, one at a time and in order. Of the
// VM lists queued while it was busy only the newest is answered: each one
// replaces the client's view, so the older ones would be thrown away.
func (c *Client) work() {
	for {
		select {
		case <-c.done:
			return
		case req := <-c.jobs:
			batch := []bt.Request{req}

		drain:
			for {
				select {
				case req := <-c.jobs:
					batch = append(batch, req)
				default:
					break drain
				}
			}

			for _, req := range latestLists(batch) {
				if isClosed(c.done) {
					return
				}

				switch req.Resource.Type {
				case vmsResourceType:
					c.listVMs(req)
				case "experiment/topology":
					c.searchTopology(req)
				}
			}
		}
	}
}

// latestLists drops every VM list request in reqs but the last.
func latestLists(reqs []bt.Request) []bt.Request {
	last := -1

	for i, req := range reqs {
		if req.Resource.Type == vmsResourceType {
			last = i
		}
	}

	kept := make([]bt.Request, 0, len(reqs))

	for i, req := range reqs {
		if req.Resource.Type != vmsResourceType || i == last {
			kept = append(kept, req)
		}
	}

	return kept
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// searchTopology answers an experiment/topology search request.
//
//nolint:funlen // one case per search term
func (c *Client) searchTopology(req bt.Request) {
	var query map[string]string

	if err := json.Unmarshal(req.Payload, &query); err != nil {
		plog.Error(plog.TypeSystem, "cannot unmarshal request payload", "err", err)

		return
	}

	// TODO: handle multiple query terms (how? AND or OR?)
	// Do the same as in web/experiment.go@SearchExperimentTopology
	term := query["term"]
	if term == "" {
		term = "hostname"
	}

	value := query["value"]
	if value == "" {
		plog.Error(plog.TypeSystem, "missing search value for term", "term", term)

		return
	}

	cacheKey := fmt.Sprintf("experiment|%s|search", req.Resource.Name)

	val, ok := cache.Get(cacheKey)
	if !ok {
		// warm the cache (again?)
		if err := deps.topology(req.Resource.Name); err != nil {
			plog.Error(
				plog.TypeSystem,
				"getting experiment topology",
				"exp",
				req.Resource.Name,
				"err",
				err,
			)

			return
		}

		val, _ = cache.Get(cacheKey)
	}

	var (
		search, _ = val.(vm.TopologySearch)
		nodes     []int
	)

	switch strings.ToLower(term) {
	case "hostname":
		if node, ok := search.Hostname[value]; ok {
			nodes = []int{node}
		}
	case "disk":
		nodes = search.Disk[value]
	case "node-type":
		nodes = search.Type[value]
	case "os-type":
		nodes = search.OSType[value]
	case "label":
		nodes = search.Label[value]
	case "annotation":
		nodes = search.Annotation[value]
	case "vlan":
		nodes = search.VLAN[value]
	case "ip":
		if net, err := netaddr.ParseIPPrefix(value); err == nil {
			for k, v := range search.IP {
				ip, ipErr := netaddr.ParseIP(k)
				if ipErr != nil {
					continue
				}

				if net.Contains(ip) {
					nodes = append(nodes, v...)
				}
			}
		} else {
			nodes = search.IP[value]
		}
	}

	results := map[string]any{
		"term":  term,
		"value": value,
		"results": map[string]any{
			"nodes": nodes,
		},
	}

	body, err := json.Marshal(results)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"marshaling search results for WebSocket client",
			"err",
			err,
		)

		return
	}

	c.send(bt.Publish{ //nolint:exhaustruct // partial initialization
		Resource: bt.NewResource("experiment/topology", req.Resource.Name, "search"),
		Result:   body,
	})
}

// listVMs answers an experiment/vms list request and puts the listed VMs in
// view for screenshots.
func (c *Client) listVMs(req bt.Request) {
	var payload map[string]any
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		plog.Error(
			plog.TypeSystem,
			"cannot unmarshal WebSocket request payload JSON",
			"err",
			err,
		)

		return
	}

	if !c.role.Allowed("vms", "list") {
		plog.Warn(plog.TypeSecurity, "client access to vms/list forbidden")

		return
	}

	expName := req.Resource.Name

	exp, err := deps.experiment(expName)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"getting experiment for WebSocket client",
			"exp",
			expName,
			"err",
			err,
		)

		return
	}

	query := parseVMListQuery(payload)

	// Screenshots follow the list (see updateScreenshots): minimega
	// takes them one at a time, which would hold back the whole list.
	allowed, total := util.SelectVMs(expName, deps.vms(exp), exp.Spec.Topology(), query, c.role)

	c.vmMu.Lock()

	c.vms = nil

	for _, v := range allowed {
		c.vms = append(c.vms, vmScope{exp: expName, name: v.Name, running: v.Running})
	}

	c.vmMu.Unlock()

	resp := util.VMListToProtobuf(expName, allowed, total, exp.Spec.Topology())

	body, err := marshaler.Marshal(resp)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"marshaling experiment VMs for WebSocket client",
			"exp",
			exp,
			"err",
			err,
		)

		return
	}

	if !c.send(bt.Publish{ //nolint:exhaustruct // partial initialization
		Resource: bt.NewResource(vmsResourceType, expName, "list"),
		Result:   body,
	}) {
		return
	}

	go c.updateScreenshots()
}

// parseVMListQuery reads an experiment/vms list request payload. Clients may
// leave any field out, or send it with the wrong type: the list is then
// unfiltered, hides do-not-boot VMs, sorts ascending and is not paginated.
func parseVMListQuery(payload map[string]any) util.VMQuery {
	query := util.VMQuery{SortAsc: true} //nolint:exhaustruct // zero values are the defaults

	query.Filter, _ = payload["filter"].(string)
	query.ShowDNB, _ = payload["show_dnb"].(bool)
	query.SortCol, _ = payload["sort_column"].(string)

	if asc, ok := payload["sort_asc"].(bool); ok {
		query.SortAsc = asc
	}

	page, pageOK := pageValue(payload["page_number"])
	size, sizeOK := pageValue(payload["page_size"])

	if pageOK && sizeOK {
		query.Page, query.Size = page, size
	}

	return query
}

// pageValue reads a page number or size, which must be a positive whole
// number; mm.VMs.Paginate panics on anything less than 1.
func pageValue(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < 1 || f > util.MaxPageValue {
		return 0, false
	}

	return int(f), true
}

func (c *Client) write() {
	ticker := time.NewTicker(pingPeriod)

	defer ticker.Stop()
	defer c.Stop()

	for {
		select {
		case <-c.done:
			return
		case msg := <-c.publish:
			err := c.publisher(msg)
			if err != nil {
				plog.Error(plog.TypeSystem, "publishing message to client", "err", err)
			}
		case <-ticker.C:
			// a control frame, which gorilla lets go alongside closeConn's
			err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
			if err != nil {
				plog.Error(plog.TypeSystem, "pinging client connection", "err", err)

				return
			}
		}
	}
}

// vmStateOf reads the state an experiment/vm message leaves its VM in: the
// running flag of the VM it carries, or failing that what its action implies.
func vmStateOf(pub bt.Publish) (vmState, bool) {
	if pub.Resource == nil || pub.Resource.Type != "experiment/vm" {
		return vmState{}, false
	}

	exp, name, ok := strings.Cut(pub.Resource.Name, "/")
	if !ok {
		return vmState{}, false
	}

	var result struct {
		Running *bool `json:"running"`
	}

	if json.Unmarshal(pub.Result, &result) == nil && result.Running != nil {
		return vmState{exp: exp, name: name, running: *result.Running}, true
	}

	switch pub.Resource.Action {
	case "start", "redeployed", "reset":
		return vmState{exp: exp, name: name, running: true}, true
	case "stop", "shutdown", "delete":
		return vmState{exp: exp, name: name, running: false}, true
	default:
		return vmState{}, false
	}
}

// trackVMState notes, from a broadcast, a VM in view starting or stopping or
// its experiment stopping, so screenshots are taken only of running VMs. The
// replies a client asks for itself change nothing here: a VM list reply's
// running states are put in view when read builds the list.
func (c *Client) trackVMState(msg any) {
	out, ok := msg.(encodedPublish)
	if !ok {
		return
	}

	if exp, stopped := stoppedExperiment(out.pub); stopped {
		c.stopExperimentVMs(exp)

		return
	}

	if out.vm == nil {
		return
	}

	c.vmMu.Lock()
	defer c.vmMu.Unlock()

	for i := range c.vms {
		if c.vms[i].exp == out.vm.exp && c.vms[i].name == out.vm.name {
			c.vms[i].running = out.vm.running
		}
	}
}

// stoppedExperiment reports the experiment an experiment message says is
// stopping, stopped or deleted. Its VMs stop with it without VM messages of
// their own.
func stoppedExperiment(pub bt.Publish) (string, bool) {
	if pub.Resource == nil || pub.Resource.Type != "experiment" {
		return "", false
	}

	switch pub.Resource.Action {
	case "stopping", "stop", "delete":
		return pub.Resource.Name, true
	default:
		return "", false
	}
}

// stopExperimentVMs stops screenshots of the VMs in view that belong to exp,
// so a client left on the page of an experiment stopped elsewhere does not
// keep asking minimega for them. A new VM list brings them back.
func (c *Client) stopExperimentVMs(exp string) {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()

	for i := range c.vms {
		if c.vms[i].exp == exp {
			c.vms[i].running = false
		}
	}
}

// encodeMessage returns msg as it goes over the wire; broadcasts come already
// encoded.
func encodeMessage(msg any) ([]byte, error) {
	if out, ok := msg.(encodedPublish); ok {
		return out.data, nil
	}

	b, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshaling message to be published: %w", err)
	}

	return b, nil
}

func (c *Client) publisher(msg any) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return fmt.Errorf("setting write deadline for client connection: %w", err)
	}

	w, err := c.conn.NextWriter(websocket.TextMessage)
	if err != nil {
		return fmt.Errorf("getting next writer for client connection: %w", err)
	}

	defer func() { _ = w.Close() }()

	c.trackVMState(msg)

	b, err := encodeMessage(msg)
	if err != nil {
		plog.Error(plog.TypeSystem, "encoding message to be published", "err", err)

		return nil
	}

	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("writing message to client connection: %w", err)
	}

	for range len(c.publish) {
		if _, err := w.Write(newline); err != nil {
			return fmt.Errorf("writing newline to client connection: %w", err)
		}

		msg := <-c.publish
		c.trackVMState(msg)

		b, err := encodeMessage(msg)
		if err != nil {
			plog.Error(plog.TypeSystem, "encoding message to be published", "err", err)

			continue
		}

		if _, err := w.Write(b); err != nil {
			return fmt.Errorf("writing message to client connection: %w", err)
		}
	}

	return nil
}

func (c *Client) setScreenshotsTicker() {
	ticker := time.NewTicker(screenshotTickerInterval)

	defer ticker.Stop()
	defer c.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.updateScreenshots()
		}
	}
}

func (c *Client) clearVMs() {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()

	c.vms = nil
}

// updateScreenshots pushes screenshots of the VMs in view. The ticker, a new
// VM list and a new screenshot size call it. A call while one is running asks
// that one to go again once it finishes, so a new list's VMs are not left
// waiting.
func (c *Client) updateScreenshots() {
	c.shotAgain.Store(true)

	// a request made just as the running round let go is picked up here
	for c.shotAgain.Load() {
		if !c.shotMu.TryLock() {
			return
		}

		for c.shotAgain.Swap(false) {
			c.pushScreenshots()
		}

		c.shotMu.Unlock()
	}
}

func (c *Client) pushScreenshots() {
	names := make(map[string][]string)

	c.vmMu.RLock()

	for _, v := range c.vms {
		if v.running {
			names[v.exp] = append(names[v.exp], v.name)
		}
	}

	c.vmMu.RUnlock()

	size := c.screenshotSize()

	// a client that leaves stops waiting for its screenshots; minimega still
	// takes the one asked for, and caches it for the next client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case <-c.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	for exp, vms := range names {
		for _, vm := range vms {
			if ctx.Err() != nil {
				return
			}

			screenshot, err := deps.screenshot(ctx, exp, vm, size)
			if err != nil {
				if ctx.Err() != nil {
					return
				}

				if errors.Is(err, mm.ErrVMNotFound) {
					continue
				}

				if errors.Is(err, mm.ErrScreenshotNotFound) {
					continue
				}

				plog.Error(plog.TypeSystem, "getting screenshot for WebSocket client", "err", err)

				continue
			}

			encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)

			marshalled, err := json.Marshal(util.WithRoot("screenshot", encoded))
			if err != nil {
				plog.Error(
					plog.TypeSystem,
					"marshaling VM screenshot for WebSocket client",
					"vm",
					vm,
					"err",
					err,
				)

				continue
			}

			if !c.send(bt.Publish{ //nolint:exhaustruct // partial initialization
				Resource: bt.NewResource("experiment/vm/screenshot", fmt.Sprintf("%s/%s", exp, vm), "update"),
				Result:   marshalled,
			}) {
				return
			}
		}
	}
}

func ServeWS(w http.ResponseWriter, r *http.Request) {
	upgrader.CheckOrigin = func(*http.Request) bool { return true }

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		plog.Error(plog.TypeSystem, "upgrading connection to WebSocket", "err", err)

		return
	}

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	NewClient(role, conn).Go()
}
