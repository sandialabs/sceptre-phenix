package web

import (
	"encoding/json"
	"net/http"
	"os"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/rbac"
	"phenix/web/weberror"
	"strings"
	"time"
)

type ServerOption func(*serverOptions)

type serverOptions struct {
	endpoint  string
	users     []string
	allowCORS bool

	tlsKeyPath string
	tlsCrtPath string

	logMiddleware string
	minimegaLogs  string

	unbundled       bool
	basePath        string
	minimegaConsole bool

	jwtKey      string
	jwtLifetime time.Duration

	proxyAuthHeader string

	features map[string]bool

	unixSocketGid int
}

func newServerOptions(opts ...ServerOption) serverOptions {
	o := serverOptions{
		endpoint:    ":3000",
		users:       []string{"admin@foo.com:foobar:Global Admin"},
		basePath:    "/",
		jwtLifetime: 24 * time.Hour,
		features:    make(map[string]bool),
	}

	for _, opt := range opts {
		opt(&o)
	}

	if !strings.HasPrefix(o.basePath, "/") {
		o.basePath = "/" + o.basePath
	}

	if !strings.HasSuffix(o.basePath, "/") {
		o.basePath = o.basePath + "/"
	}

	if _, err := os.Stat("downloads/tunneler"); err == nil {
		o.features["tunneler-download"] = true
	}

	return o
}

func (this serverOptions) tlsEnabled() bool {
	if this.tlsKeyPath == "" {
		return false
	}

	if this.tlsCrtPath == "" {
		return false
	}

	return true
}

func (this serverOptions) featured(f string) bool {
	_, ok := this.features[f]
	return ok
}

func ServeOnEndpoint(e string) ServerOption {
	return func(o *serverOptions) {
		o.endpoint = e
	}
}

func ServeWithJWTKey(k string) ServerOption {
	return func(o *serverOptions) {
		o.jwtKey = k
	}
}

func ServeWithUsers(u []string) ServerOption {
	return func(o *serverOptions) {
		if len(u) > 0 {
			o.users = u
		}
	}
}

func ServeWithCORS(c bool) ServerOption {
	return func(o *serverOptions) {
		o.allowCORS = c
	}
}

func ServeWithTLS(k, c string) ServerOption {
	return func(o *serverOptions) {
		o.tlsKeyPath = k
		o.tlsCrtPath = c
	}
}

func ServeWithMiddlewareLogging(l string) ServerOption {
	return func(o *serverOptions) {
		o.logMiddleware = l
	}
}

func ServeMinimegaLogs(m string) ServerOption {
	return func(o *serverOptions) {
		o.minimegaLogs = m
	}
}

func ServeUnbundled() ServerOption {
	return func(o *serverOptions) {
		o.unbundled = true
	}
}

func ServeBasePath(p string) ServerOption {
	return func(o *serverOptions) {
		o.basePath = p
	}
}

func ServeMinimegaConsole(c bool) ServerOption {
	return func(o *serverOptions) {
		o.minimegaConsole = c
	}
}

func ServeWithJWTLifetime(l time.Duration) ServerOption {
	return func(o *serverOptions) {
		o.jwtLifetime = l
	}
}

func ServeWithProxyAuthHeader(h string) ServerOption {
	return func(o *serverOptions) {
		o.proxyAuthHeader = h
	}
}

func ServeWithFeatures(f []string) ServerOption {
	return func(o *serverOptions) {
		if f == nil {
			for k, v := range o.features {
				if !v {
					delete(o.features, k)
				}
			}
		} else {
			for _, feature := range f {
				o.features[feature] = false
			}
		}
	}
}

func ServeWithUnixSocketGid(g int) ServerOption {
	return func(o *serverOptions) {
		o.unixSocketGid = g
	}
}

// GET /options
func GetOptions(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "GetOptions")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("options", "list") {
		err := weberror.NewWebError(nil, "listing options not allowed for %s", ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	options := map[string]any{
		"bridge-mode":  common.BridgeMode,
		"deploy-mode":  common.DeployMode,
		"use-gre-mesh": common.UseGREMesh,
	}

	body, err := json.Marshal(options)
	if err != nil {
		err := weberror.NewWebError(err, "unable to process options")
		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)

	return nil
}
