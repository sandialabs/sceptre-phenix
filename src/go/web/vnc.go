package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
	"golang.org/x/net/websocket"

	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

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

	vm, err := vm.Get(exp, name)
	if err != nil {
		http.Error(w, "VM not found", http.StatusNotFound)

		return
	}

	// The `token` variable will be an empty string if authentication is disabled,
	// which is okay and will not cause any issues here.
	token, _ := ctx.Value(middleware.ContextKeyJWT).(string)
	config := newVNCBannerConfig(token, exp, name)

	if banner, ok := vm.Annotations["vncBanner"]; ok {
		switch banner := banner.(type) {
		case string:
			config.finalize(banner)
		case map[string]any:
			err := mapstructure.Decode(banner, &config) //nolint:musttag // struct is used for decoding
			if err != nil {
				plog.Error(
					plog.TypeSystem,
					"decoding vncBanner annotation for VM",
					"vm",
					name,
					"err",
					err,
				)
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

	plog.Info(
		plog.TypeAction,
		"vnc opened",
		"user",

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

type bannerConfig struct {
	BannerLines     []string `mapstructure:"banner"`
	BackgroundColor string   `mapstructure:"backgroundColor"`
	TextColor       string   `mapstructure:"textColor"`

	// Use type interface{} here so it can either be a simple string or a
	// template.HTML string (safe HTML).
	Banner any `mapstructure:"-"`
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
	return &vncConfig{ //nolint:exhaustruct // partial initialization
		BasePath: o.basePath,
		Token:    token,
		ExpName:  exp,
		VMName:   vm,
		TopBanner: bannerConfig{ //nolint:exhaustruct // partial initialization
			BackgroundColor: "white",
			TextColor:       "black",
		},
		BottomBanner: bannerConfig{ //nolint:exhaustruct // partial initialization
			BackgroundColor: "white",
			TextColor:       "black",
		},
	}
}

func (c *vncConfig) finalize(banner ...string) {
	if len(banner) > 0 {
		c.TopBanner.Banner = template.HTML(strings.Join(banner, "<br/>"))    //nolint:gosec // The used method does not auto-escape HTML
		c.BottomBanner.Banner = template.HTML(strings.Join(banner, "<br/>")) //nolint:gosec // The used method does not auto-escape HTML

		return
	}

	if !c.Disabled {
		if len(c.TopBanner.BannerLines) > 0 {
			//nolint:gosec // The used method does not auto-escape HTML
			c.TopBanner.Banner = template.HTML(strings.Join(c.TopBanner.BannerLines, "<br/>"))
		}

		if len(c.BottomBanner.BannerLines) > 0 {
			//nolint:gosec // The used method does not auto-escape HTML
			c.BottomBanner.Banner = template.HTML(strings.Join(c.BottomBanner.BannerLines, "<br/>"))
		}
	}
}
