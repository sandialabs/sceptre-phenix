package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
	"golang.org/x/net/websocket"

	"phenix/api/experiment"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

// findTopologyNode returns the named VM's node from the experiment's topology,
// or nil if the experiment or VM does not exist.
func findTopologyNode(exp, name string) ifaces.NodeSpec { //nolint:ireturn // topology node
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

// GetVNC - GET /experiments/{exp}/vms/{name}/vnc.
func GetVNC(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetVNC")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["exp"]
		name    = vars["name"]
	)

	if !role.Allowed("vms/vnc", "get", exp+"/"+name) {
		plog.Warn(
			plog.TypeSecurity,
			"vnc access not allowed",
			"user",
			ctx.Value(middleware.ContextKeyUser),
			"exp",
			exp,
			"vm",
			name,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// Only the node's annotations are needed, so read them from the topology
	// rather than building the full VM (which also asks minimega for details).
	node := findTopologyNode(exp, name)
	if node == nil {
		http.Error(w, "VM not found", http.StatusNotFound)

		return
	}

	// The `token` variable will be an empty string if authentication is disabled,
	// which is okay and will not cause any issues here.
	token, _ := ctx.Value(middleware.ContextKeyJWT).(string)

	nonce, err := newCSPNonce()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	config := newVNCConfig(token, exp, name, nonce)
	config.setBanners(name, node.Annotations()["vncBanner"])

	// The page runs only its own scripts: the inline one that starts noVNC
	// carries the nonce, so markup that reached the page some other way cannot
	// run script or load it from another host.
	w.Header().Set("Content-Security-Policy", vncContentSecurityPolicy(nonce))

	// set no-cache headers
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1.
	w.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0.
	w.Header().Set("Expires", "0")                                         // Proxies.

	plog.Info(
		plog.TypeAction,
		"vnc opened",
		"user",
		middleware.UserFromContext(ctx),
		"exp",
		exp,
		"vm",
		name,
	)

	if o.unbundled {
		tmpl := template.Must(template.New("vnc.html").ParseFiles("web/public/vnc.html"))
		_ = tmpl.Execute(w, config)
	} else {
		assets, err := GetAssets()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		bfs := util.NewBinaryFileSystem(assets)
		bfs.ServeTemplate(w, "vnc.html", config)
	}
}

// GetVNCWebSocket - GET /experiments/{exp}/vms/{name}/vnc/ws.
func GetVNCWebSocket(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetVNCWebSocket")

	var (
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	endpoint, err := mm.GetVNCEndpoint(mm.NS(exp), mm.VMName(name))
	if err != nil {
		plog.Error(plog.TypeSystem, "getting VNC endpoint", "err", err)
		http.Error(w, "", http.StatusBadRequest)

		return
	}

	websocket.Handler(util.ConnectWSHandler(endpoint)).ServeHTTP(w, r)
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

// bannerConfig is a banner as the VNC page shows it: each line as text, on a
// line of its own.
type bannerConfig struct {
	Lines           []string
	BackgroundColor string
	TextColor       string
}

type vncConfig struct {
	BasePath string
	Token    string
	ExpName  string
	VMName   string
	Nonce    string

	TopBanner    bannerConfig
	BottomBanner bannerConfig
}

func newVNCConfig(token, exp, vm, nonce string) *vncConfig {
	return &vncConfig{
		BasePath: o.basePath,
		Token:    token,
		ExpName:  exp,
		VMName:   vm,
		Nonce:    nonce,
		TopBanner: bannerConfig{ //nolint:exhaustruct // lines set by setBanners
			BackgroundColor: defaultBannerBackground,
			TextColor:       defaultBannerText,
		},
		BottomBanner: bannerConfig{ //nolint:exhaustruct // lines set by setBanners
			BackgroundColor: defaultBannerBackground,
			TextColor:       defaultBannerText,
		},
	}
}

const (
	defaultBannerBackground = "white"
	defaultBannerText       = "black"
)

// setBanners sets the page's banners from the VM's vncBanner annotation:
// none names the experiment and VM, a string is the text of both banners (a
// newline starts a new line), and a map sets each banner's lines and colors,
// or turns both off. The page shows the text as text, never as markup.
func (c *vncConfig) setBanners(vm string, annotation any) {
	switch banner := annotation.(type) {
	case nil:
		c.setText(fmt.Sprintf("EXP: %s - VM: %s", c.ExpName, vm))
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

		c.TopBanner = bannerFrom(vm, spec.TopBanner)
		c.BottomBanner = bannerFrom(vm, spec.BottomBanner)
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
func (c *vncConfig) setText(text string) {
	lines := strings.Split(text, "\n")

	c.TopBanner.Lines = lines
	c.BottomBanner.Lines = lines
}

// bannerFrom is the banner spec describes, its colors replaced by the default
// ones where they are not CSS color names or hex colors.
func bannerFrom(vm string, spec vncBannerSpec) bannerConfig {
	return bannerConfig{
		Lines:           spec.Banner,
		BackgroundColor: bannerColor(vm, spec.BackgroundColor, defaultBannerBackground),
		TextColor:       bannerColor(vm, spec.TextColor, defaultBannerText),
	}
}

// cssColor matches a CSS color name or hex color. The page's template refuses
// rgb() and other color functions in a style anyway.
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

// newCSPNonce returns a random nonce for one page's Content-Security-Policy.
func newCSPNonce() (string, error) {
	nonce := make([]byte, 16) //nolint:mnd // 128 bits

	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generating content security policy nonce: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(nonce), nil
}

// vncContentSecurityPolicy is the VNC page's Content-Security-Policy. noVNC
// loads its scripts, styles, images and sounds from this server, draws its
// cursor from data: and blob: URLs, styles elements inline, and connects back
// over a websocket.
func vncContentSecurityPolicy(nonce string) string {
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'nonce-" + nonce + "'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
	}, "; ")
}
