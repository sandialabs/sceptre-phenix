package broker

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/proto"
	"phenix/web/rbac"
	"phenix/web/util"

	bt "phenix/web/broker/brokertypes"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
)

var marshaler = protojson.MarshalOptions{EmitUnpopulated: true}

type vmScope struct {
	exp  string
	name string
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	maxMsgSize = 2048
)

var (
	newline  = []byte{'\n'}
	upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	}
)

type Client struct {
	role   rbac.Role
	conn   *websocket.Conn
	connMu sync.Mutex

	publish chan interface{}
	done    chan struct{}
	once    sync.Once

	// Track the VMs this client currently has in view, if any, so we know
	// what screenshots need to periodically be pushed to the client over
	// the WebSocket connection.
	vms  []vmScope
	vmMu sync.RWMutex
}

func NewClient(role rbac.Role, conn *websocket.Conn) *Client {
	return &Client{
		role:    role,
		conn:    conn,
		publish: make(chan interface{}, 256),
		done:    make(chan struct{}),
	}
}

func (this *Client) Go() {
	register <- this

	go this.write()
	go this.read()
	go this.screenshots()
}

func (this *Client) Stop() {
	this.once.Do(this.stop)
}

func (this *Client) stop() {
	unregister <- this
	close(this.done)

	this.connMu.Lock()
	defer this.connMu.Unlock()

	if err := this.conn.WriteMessage(websocket.CloseMessage, []byte{}); err != nil {
		plog.Warn("closing client connection", "err", err)
	}

	this.conn.Close()
}

func (this *Client) read() {
	defer this.Stop()

	this.conn.SetReadLimit(maxMsgSize)

	if err := this.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		plog.Error("setting read deadline for client connection", "err", err)
		return
	}

	ponger := func(string) error {
		if err := this.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			plog.Error("setting read deadline in pong handler for client connection", "err", err)
			return err
		}

		return nil
	}

	this.conn.SetPongHandler(ponger)

	for {
		select {
		case <-this.done:
			return
		default:
			_, msg, err := this.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					plog.Debug("reading from WebSocket client", "err", err)
				}

				return
			}

			var req bt.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				plog.Error("cannot unmarshal request JSON", "err", err)
				continue
			}

			switch req.Resource.Type {
			case "experiment/vms":
			default:
				plog.Error("unexpected WebSocket request resource type", "type", req.Resource.Type)
				continue
			}

			switch req.Resource.Action {
			case "list":
			default:
				plog.Error("unexpected WebSocket request resource action", "action", req.Resource.Action)
				continue
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(req.Payload, &payload); err != nil {
				plog.Error("cannot unmarshal WebSocket request payload JSON", "err", err)
				continue
			}

			if !this.role.Allowed("vms", "list") {
				plog.Warn("client access to vms/list forbidden")
				continue
			}

			expName := req.Resource.Name

			exp, err := experiment.Get(expName)
			if err != nil {
				plog.Error("getting experiment for WebSocket client", "exp", expName, "err", err)
				continue
			}

			vms, err := vm.List(expName)
			if err != nil {
				plog.Error("getting list of VMs for experiment", "exp", expName, "err", err)
				continue
			}

			// A Boolean expression tree is built and the fields that
			// should be searched are determined based on the search string
			clientFilter, _ := payload["filter"].(string)
			filterTree := mm.BuildTree(clientFilter)

			// If `show_dnb` was not provided client-side, `showDNB` will be false,
			// which is the default we want.
			showDNB, _ := payload["show_dnb"].(bool)

			allowed := mm.VMs{}

			for _, vm := range vms {
				// If the VM is marked as do not boot, and we're not showing VMs marked as
				// such, continue on to the next VM right away.
				if vm.DoNotBoot && !showDNB {
					continue
				}

				// If the filter supplied could not be
				// parsed, do not add the VM
				if len(clientFilter) > 0 {
					if filterTree == nil {
						continue
					} else {
						// If the search string could be parsed,
						// determine if the VM should be included
						if !filterTree.Evaluate(&vm) {
							continue
						}
					}
				}

				if this.role.Allowed("vms", "list", fmt.Sprintf("%s/%s", expName, vm.Name)) {
					if vm.Running {
						screenshot, err := util.GetScreenshot(expName, vm.Name, "200")
						if err != nil {
							plog.Error("getting screenshot for WebSocket client", "err", err)
						} else {
							vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
						}
					}

					allowed = append(allowed, vm)
				}
			}

			var (
				sort = payload["sort_column"].(string)
				asc  = payload["sort_asc"].(bool)
				page = int(payload["page_number"].(float64))
				size = int(payload["page_size"].(float64))
			)

			payload = map[string]interface{}{"total": len(allowed)}

			if sort != "" {
				allowed.SortBy(sort, asc)
			}

			if page != 0 && size != 0 {
				allowed = allowed.Paginate(page, size)
			}

			this.vmMu.Lock()

			this.vms = nil

			for _, v := range allowed {
				this.vms = append(this.vms, vmScope{exp: expName, name: v.Name})
			}

			this.vmMu.Unlock()

			resp := &proto.VMList{Total: uint32(len(allowed))}

			resp.Vms = make([]*proto.VM, len(allowed))
			for i, v := range allowed {
				resp.Vms[i] = util.VMToProtobuf(expName, v, exp.Spec.Topology())
			}

			body, err := marshaler.Marshal(resp)
			if err != nil {
				plog.Error("marshaling experiment VMs for WebSocket client", "exp", exp, "err", err)
				continue
			}

			this.publish <- bt.Publish{
				Resource: bt.NewResource("experiment/vms", expName, "list"),
				Result:   body,
			}
		}
	}
}

func (this *Client) write() {
	ticker := time.NewTicker(pingPeriod)

	defer ticker.Stop()
	defer this.Stop()

	for {
		select {
		case <-this.done:
			return
		case msg := <-this.publish:
			if err := this.publisher(msg); err != nil {
				plog.Error("publishing message to client", "err", err)
			}
		case <-ticker.C:
			if err := this.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				plog.Error("setting write deadline for client connection", "err", err)
				return
			}

			if err := this.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				plog.Error("pinging client connection", "err", err)
				return
			}
		}
	}
}

func (this *Client) publisher(msg interface{}) error {
	this.connMu.Lock()
	defer this.connMu.Unlock()

	if err := this.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return fmt.Errorf("setting write deadline for client connection: %w", err)
	}

	w, err := this.conn.NextWriter(websocket.TextMessage)
	if err != nil {
		return fmt.Errorf("getting next writer for client connection: %w", err)
	}

	defer w.Close()

	b, err := json.Marshal(msg)
	if err != nil {
		plog.Error("marshaling message to be published", "err", err)
		return nil
	}

	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("writing message to client connection: %w", err)
	}

	for i := 0; i < len(this.publish); i++ {
		if _, err := w.Write(newline); err != nil {
			return fmt.Errorf("writing newline to client connection: %w", err)
		}

		msg := <-this.publish

		b, err := json.Marshal(msg)
		if err != nil {
			plog.Error("marshaling message to be published", "err", err)
			continue
		}

		if _, err := w.Write(b); err != nil {
			return fmt.Errorf("writing message to client connection: %w", err)
		}
	}

	return nil
}

func (this *Client) screenshots() {
	ticker := time.NewTicker(5 * time.Second)

	defer ticker.Stop()
	defer this.Stop()

	for {
		select {
		case <-this.done:
			return
		case <-ticker.C:
			names := make(map[string][]string)

			this.vmMu.RLock()

			for _, v := range this.vms {
				names[v.exp] = append(names[v.exp], v.name)
			}

			this.vmMu.RUnlock()

			for exp, vms := range names {
				for _, vm := range vms {
					screenshot, err := util.GetScreenshot(exp, vm, "200")
					if err != nil {
						if errors.Is(err, mm.ErrVMNotFound) {
							continue
						}

						if errors.Is(err, mm.ErrScreenshotNotFound) {
							continue
						}

						plog.Error("getting screenshot for WebSocket client", "err", err)
						continue
					}

					encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
					marshalled, err := json.Marshal(util.WithRoot("screenshot", encoded))
					if err != nil {
						plog.Error("marshaling VM screenshot for WebSocket client", "vm", vm, "err", err)
						continue
					}

					this.publish <- bt.Publish{
						Resource: bt.NewResource("experiment/vm/screenshot", fmt.Sprintf("%s/%s", exp, vm), "update"),
						Result:   marshalled,
					}
				}
			}
		}
	}
}

func ServeWS(w http.ResponseWriter, r *http.Request) {
	upgrader.CheckOrigin = func(*http.Request) bool { return true }

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		plog.Error("upgrading connection to WebSocket", "err", err)
		return
	}

	role := r.Context().Value("role").(rbac.Role)

	NewClient(role, conn).Go()
}
