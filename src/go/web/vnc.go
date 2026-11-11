package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
	"golang.org/x/net/websocket"

	"phenix/api/experiment"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/util"
	"phenix/web/weberror"
)

// findTopologyNode returns the named VM's node from the experiment's topology,
// or nil if the experiment or VM does not exist.
func findTopologyNode(exp, name string) ifaces.NodeSpec {
	stored, err := experiment.Get(exp)
	if err != nil {
		return nil
	}

	topology := stored.Spec.Topology()
	if topology == nil {
		return nil
	}

	// Nodes, unlike FindNodeByName, tolerates a nil topology pointer.
	for _, node := range topology.Nodes() {
		if node.General().Hostname() == name {
			return node
		}
	}

	return nil
}

// vncProbeTimeout bounds the dial of a probe to the VM's VNC server.
const vncProbeTimeout = 3 * time.Second

// GetVNC - GET /experiments/{exp}/vms/{name}/vnc.
//
// It returns the VM's VNC session as JSON. With probe=true, it also dials the VM's VNC server, and answers 502 with
// the code vnc_unreachable when phenix cannot connect to it.
func GetVNC(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetVNC")

	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/vnc", "get", exp+"/"+name) {
		audit.Denied(r, audit.VNCAttach, "vnc access not allowed", audit.Fields{Experiment: exp, VM: name})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	probe := false

	if value := r.URL.Query().Get("probe"); value != "" {
		var err error

		if probe, err = strconv.ParseBool(value); err != nil {
			return weberror.NewWebError(err, "probe must be true or false")
		}
	}

	// Only the node's annotations are needed, so read them from the topology
	// rather than building the full VM (which also asks minimega for details).
	node := findTopologyNode(exp, name)
	if node == nil {
		return weberror.New(http.StatusNotFound, "VM not found")
	}

	info, err := mm.GetVNCInfo(mm.NS(exp), mm.VMName(name), mm.Context(ctx))
	if err != nil {
		if requestCanceled(r, err) {
			return nil
		}

		return vncInfoError(err)
	}

	if probe {
		if err := probeVNC(ctx, info); err != nil {
			if requestCanceled(r, err) {
				return nil
			}

			return weberror.NewWebError(err, "phenix cannot connect to the VNC port of the VM").
				SetStatus(http.StatusBadGateway).
				WithCode(weberror.CodeVNCUnreachable).
				WithDetail("host", info.Host).
				WithDetail("port", info.Port)
		}
	}

	session := newVNCSession(exp, name, info)
	session.setBanners(name, node.Annotations()["vncBanner"])

	// set no-cache headers
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1.
	w.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0.
	w.Header().Set("Expires", "0")                                         // Proxies.

	if !probe {
		plog.Info(plog.TypeAction, "vnc opened", "user", middleware.UserFromContext(ctx), "exp", exp, "vm", name)
	}

	return writeJSON(w, session)
}

// vncInfoError is the web error for err from mm.GetVNCInfo.
func vncInfoError(err error) *weberror.WebError {
	switch {
	case errors.Is(err, mm.ErrVMNotFound):
		return weberror.NewWebError(err, "VM is not running").SetStatus(http.StatusConflict)
	case errors.Is(err, mm.ErrOldMinimega):
		return weberror.NewWebError(err, "%s", mm.ErrOldMinimega.Error()).SetStatus(http.StatusInternalServerError)
	default:
		return weberror.NewWebError(err, "unable to get the VNC info of the VM from minimega").
			SetStatus(http.StatusBadGateway)
	}
}

// probeVNC dials the VM's VNC server, and closes the connection.
func probeVNC(ctx context.Context, info mm.VNCInfo) error {
	dialer := &net.Dialer{Timeout: vncProbeTimeout} //nolint:exhaustruct // partial initialization

	conn, err := dialer.DialContext(ctx, "tcp", info.Endpoint())
	if err != nil {
		return fmt.Errorf("probing VNC server %s: %w", info.Endpoint(), err)
	}

	_ = conn.Close()

	return nil
}

// GetVNCWebSocket - GET /experiments/{exp}/vms/{name}/vnc/ws.
func GetVNCWebSocket(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetVNCWebSocket")

	var (
		role = middleware.RoleFromContext(r.Context())
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	fields := audit.Fields{Experiment: exp, VM: name}

	// the same permission as the page that opens it
	if !role.Allowed("vms/vnc", "get", exp+"/"+name) {
		audit.Denied(r, audit.VNCAttach, "vnc access not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	info, err := mm.GetVNCInfo(mm.NS(exp), mm.VMName(name), mm.Context(r.Context()))
	if err != nil {
		audit.Failure(r, audit.VNCAttach, "vnc attach failed", fields, err)

		if requestCanceled(r, err) {
			return nil
		}

		plog.Error(plog.TypeSystem, "getting VNC endpoint", "err", err)

		return weberror.New(http.StatusBadRequest, "unable to get VNC endpoint")
	}

	if lifecycle.RefuseWhileStopping(w) {
		return nil
	}

	proxy := util.ConnectWSHandler(info.Endpoint())

	websocket.Handler(func(ws *websocket.Conn) {
		defer lifecycle.TrackConn("VNC", ws)()

		audit.Success(r, audit.VNCAttach, "vnc attached", fields)
		proxy(ws)
	}).ServeHTTP(w, r)

	return nil
}

// vncBannerAnnotation is the map form of a VM's vncBanner annotation.
type vncBannerAnnotation struct {
	TopBanner    vncBannerSpec `mapstructure:"topBanner"`
	BottomBanner vncBannerSpec `mapstructure:"bottomBanner"`
	Disabled     bool          `mapstructure:"disabled"`
}

// vncBannerSpec is one banner in the map form of the vncBanner annotation.
type vncBannerSpec struct {
	Banner          []string `mapstructure:"banner"`
	BackgroundColor string   `mapstructure:"backgroundColor"`
	TextColor       string   `mapstructure:"textColor"`
}

// bannerConfig is a banner as the VNC console shows it: each line as text, on a line of its own. No lines means no
// banner.
type bannerConfig struct {
	Lines           []string `json:"lines"`
	BackgroundColor string   `json:"backgroundColor"`
	TextColor       string   `json:"textColor"`
}

// vncBanners are the banners above and below the VNC console.
type vncBanners struct {
	Top    bannerConfig `json:"top"`
	Bottom bannerConfig `json:"bottom"`
}

// vncDisplay is the video card that QEMU emulates for the VM, and whether a client can resize the VM's screen.
type vncDisplay struct {
	VGA          string `json:"vga"`
	RemoteResize bool   `json:"remoteResize"`
}

// The copy and paste modes of a VNC session.
const (
	// clipboardPasteAsKeys types pasted text as key presses.
	clipboardPasteAsKeys = "pasteAsKeys"
	// clipboardBidirectional shares the clipboard both ways through qemu-vdagent.
	clipboardBidirectional = "bidirectional"
)

// vncClipboard is the copy and paste mode of the VM's console.
type vncClipboard struct {
	Mode string `json:"mode"`
}

// vncSession is what the UI needs to open the VM's VNC console, as GET /experiments/{exp}/vms/{name}/vnc answers it.
type vncSession struct {
	Experiment string `json:"experiment"`
	VM         string `json:"vm"`
	// WebSocket is the path of the VM's VNC websocket, relative to the API root.
	WebSocket string       `json:"websocket"`
	Banners   vncBanners   `json:"banners"`
	Display   vncDisplay   `json:"display"`
	Clipboard vncClipboard `json:"clipboard"`
}

// newVNCSession is the VNC session of VM vm of experiment exp, with the display and clipboard mode of info and no
// banner lines.
func newVNCSession(exp, vm string, info mm.VNCInfo) *vncSession {
	defaults := bannerConfig{Lines: []string{}, BackgroundColor: defaultBannerBackground, TextColor: defaultBannerText}

	clipboard := clipboardPasteAsKeys
	if info.BidirectionalCopyPaste {
		clipboard = clipboardBidirectional
	}

	return &vncSession{
		Experiment: exp,
		VM:         vm,
		WebSocket:  "experiments/" + url.PathEscape(exp) + "/vms/" + url.PathEscape(vm) + "/vnc/ws",
		Banners:    vncBanners{Top: defaults, Bottom: defaults},
		// Only virtio-vga has the resize hook in QEMU.
		Display:   vncDisplay{VGA: info.VGA, RemoteResize: info.VGA == "virtio"},
		Clipboard: vncClipboard{Mode: clipboard},
	}
}

const (
	defaultBannerBackground = "white"
	defaultBannerText       = "black"
)

// setBanners sets the session's banners from the VM's vncBanner annotation:
// none names the experiment and VM, a string is the text of both banners (a
// newline starts a new line), and a map sets each banner's lines and colors,
// or turns both off. The console shows the text as text, never as markup.
func (c *vncSession) setBanners(vm string, annotation any) {
	switch banner := annotation.(type) {
	case nil:
		c.setText(fmt.Sprintf("EXP: %s - VM: %s", c.Experiment, vm))
	case string:
		c.setText(banner)
	case map[string]any:
		var (
			spec     vncBannerAnnotation
			metadata mapstructure.Metadata
		)

		decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			Metadata: &metadata,
			Result:   &spec,
		})
		if err == nil {
			err = decoder.Decode(banner)
		}

		if err != nil {
			plog.Error(plog.TypeSystem, "decoding vncBanner annotation for VM", "vm", vm, "err", err)

			return
		}

		if len(metadata.Unused) > 0 {
			plog.Warn(
				plog.TypeSystem,
				"ignoring unknown vncBanner annotation keys for VM",
				"vm",
				vm,
				"keys",
				metadata.Unused,
			)
		}

		if spec.Disabled {
			return
		}

		c.Banners.Top = bannerFrom(vm, spec.TopBanner)
		c.Banners.Bottom = bannerFrom(vm, spec.BottomBanner)
	default:
		plog.Error(
			plog.TypeSystem,
			"unexpected type for vncBanner annotation for VM",
			"vm",
			vm,
			"type",
			fmt.Sprintf("%T", annotation),
		)
	}
}

// setText makes text, split at its newlines, the lines of both banners.
func (c *vncSession) setText(text string) {
	lines := strings.Split(text, "\n")

	c.Banners.Top.Lines = lines
	c.Banners.Bottom.Lines = lines
}

// bannerFrom is the banner spec describes, its colors replaced by the default
// ones where they are not CSS color names or hex colors.
func bannerFrom(vm string, spec vncBannerSpec) bannerConfig {
	lines := spec.Banner
	if lines == nil {
		lines = []string{}
	}

	return bannerConfig{
		Lines:           lines,
		BackgroundColor: bannerColor(vm, spec.BackgroundColor, defaultBannerBackground),
		TextColor:       bannerColor(vm, spec.TextColor, defaultBannerText),
	}
}

// cssColor matches a CSS color name or hex color.
var cssColor = regexp.MustCompile(`^(?:[a-zA-Z]{1,32}|#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8}))$`)

// bannerColor returns color when it is a CSS color name or hex color, fallback
// when it is empty, and fallback with a warning otherwise.
func bannerColor(vm, color, fallback string) string {
	switch {
	case color == "":
		return fallback
	case cssColor.MatchString(color):
		return color
	default:
		plog.Warn(
			plog.TypeSystem,
			"ignoring vncBanner annotation color for VM that is not a CSS color",
			"vm",
			vm,
			"color",
			color,
		)

		return fallback
	}
}
