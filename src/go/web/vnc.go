package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/rbac"
	"phenix/web/util"

	assetfs "github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
	"golang.org/x/net/websocket"
)

// GET /experiments/{exp}/vms/{name}/vnc
func GetVNC(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetVNC")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["exp"]
		name = vars["name"]
	)

	if !role.Allowed("vms/vnc", "get", exp+"/"+name) {
		plog.Warn(plog.TypeSecurity, "vnc access not allowed", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	vm, err := vm.Get(exp, name)
	if err != nil {
		http.Error(w, "VM not found", http.StatusNotFound)
		return
	}

	// The `token` variable will be an empty string if authentication is disabled,
	// which is okay and will not cause any issues here.
	token, _ := ctx.Value("jwt").(string)
	config := newVNCBannerConfig(token, exp, name)

	if banner, ok := vm.Annotations["vncBanner"]; ok {
		switch banner := banner.(type) {
		case string:
			config.finalize(banner)
		case map[string]interface{}:
			if err := mapstructure.Decode(banner, &config); err != nil {
				plog.Error(plog.TypeSystem, "decoding vncBanner annotation for VM", "vm", name, "err", err)
			} else {
				config.finalize()
			}
		default:
			plog.Error(plog.TypeSystem, "unexpected interface type for vncBanner annotation")
		}
	} else {
		config.finalize(fmt.Sprintf("EXP: %s - VM: %s", exp, name))
	}

	// set no-cache headers
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1.
	w.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0.
	w.Header().Set("Expires", "0")                                         // Proxies.

	plog.Info(plog.TypeAction, "vnc opened", "user", ctx.Value("user").(string), "exp", exp, "vm", name)
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
	Token    string
	ExpName  string
	VMName   string

	TopBanner    bannerConfig `mapstructure:"topBanner"`
	BottomBanner bannerConfig `mapstructure:"bottomBanner"`

	Disabled bool `mapstructure:"disabled"`
}

func newVNCBannerConfig(token, exp, vm string) *vncConfig {
	return &vncConfig{
		BasePath: o.basePath,
		Token:    token,
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

	if !this.Disabled {
		if len(this.TopBanner.BannerLines) > 0 {
			this.TopBanner.Banner = template.HTML(strings.Join(this.TopBanner.BannerLines, "<br/>"))
		}

		if len(this.BottomBanner.BannerLines) > 0 {
			this.BottomBanner.Banner = template.HTML(strings.Join(this.BottomBanner.BannerLines, "<br/>"))
		}
	}
}
