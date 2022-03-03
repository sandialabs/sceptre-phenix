package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"phenix/api/vm"
	"phenix/internal/mm"
	"phenix/web/rbac"
	"phenix/web/util"

	log "github.com/activeshadow/libminimega/minilog"
	assetfs "github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
	"golang.org/x/net/websocket"
)

// GET /experiments/{exp}/vms/{name}/vnc
func GetVNC(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetVNC HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/vnc", "get", fmt.Sprintf("%s_%s", exp, name)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	vm, err := vm.Get(exp, name)
	if err != nil {
		http.Error(w, "VM not found", http.StatusNotFound)
		return
	}

	config := newVNCBannerConfig(exp, name)

	if banner, ok := vm.Annotations["vncBanner"]; ok {
		switch banner := banner.(type) {
		case string:
			config.finalize(banner)
		case map[string]interface{}:
			if err := mapstructure.Decode(banner, &config); err != nil {
				log.Error("decoding vncBanner annotation for VM %s: %v", name, err)
			} else {
				config.finalize()
			}
		default:
			log.Error("unexpected interface type for vncBanner annotation")
		}
	}

	// set no-cache headers
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1.
	w.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0.
	w.Header().Set("Expires", "0")                                         // Proxies.

	if o.unbundled {
		tmpl := template.Must(template.New("vnc.html").ParseFiles("web/public/vnc.html"))
		tmpl.Execute(w, config)
	} else {
		bfs := util.NewBinaryFileSystem(
			&assetfs.AssetFS{
				Asset:     Asset,
				AssetDir:  AssetDir,
				AssetInfo: AssetInfo,
			},
		)

		bfs.ServeTemplate(w, "vnc.html", config)
	}
}

// GET /experiments/{exp}/vms/{name}/vnc/ws
func GetVNCWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetVNCWebSocket HTTP handler called")

	var (
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	endpoint, err := mm.GetVNCEndpoint(mm.NS(exp), mm.VMName(name))
	if err != nil {
		log.Error("getting VNC endpoint: %v", err)
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	websocket.Handler(util.ConnectWSHandler(endpoint)).ServeHTTP(w, r)
}

type bannerConfig struct {
	BannerLines     []string `mapstructure:"banner"`
	BackgroundColor string   `mapstructure:"backgroundColor"`
	TextColor       string   `mapstructure:"textColor"`

	// Use type interface{} here so it can either be a simple string or a
	// template.HTML string (safe HTML).
	Banner interface{} `mapstructure:"-"`
}

type vncConfig struct {
	BasePath string
	ExpName  string
	VMName   string

	TopBanner    bannerConfig `mapstructure:"topBanner"`
	BottomBanner bannerConfig `mapstructure:"bottomBanner"`
}

func newVNCBannerConfig(exp, vm string) *vncConfig {
	return &vncConfig{
		BasePath: o.basePath,
		ExpName:  exp,
		VMName:   vm,
		TopBanner: bannerConfig{
			BackgroundColor: "white",
			TextColor:       "black",
		},
		BottomBanner: bannerConfig{
			BackgroundColor: "white",
			TextColor:       "black",
		},
	}
}

func (this *vncConfig) finalize(banner ...string) {
	if len(banner) > 0 {
		this.TopBanner.Banner = template.HTML(strings.Join(banner, "<br/>"))
		this.BottomBanner.Banner = template.HTML(strings.Join(banner, "<br/>"))
		return
	}

	if len(this.TopBanner.BannerLines) > 0 {
		this.TopBanner.Banner = template.HTML(strings.Join(this.TopBanner.BannerLines, "<br/>"))
	}

	if len(this.BottomBanner.BannerLines) > 0 {
		this.BottomBanner.Banner = template.HTML(strings.Join(this.BottomBanner.BannerLines, "<br/>"))
	}
}
