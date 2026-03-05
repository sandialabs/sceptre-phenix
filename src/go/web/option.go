package web

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

const defaultJWTLifetime = 24 * time.Hour

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

	unixSocketGID int
}

func newServerOptions(opts ...ServerOption) serverOptions {
	so := serverOptions{ //nolint:exhaustruct // partial initialization
		endpoint:    ":3000",
		users:       []string{"admin@foo.com:foobar:Global Admin"},
		basePath:    "/",
		jwtLifetime: defaultJWTLifetime,
		features:    make(map[string]bool),
	}

	for _, opt := range opts {
		opt(&so)
	}

	if !strings.HasPrefix(so.basePath, "/") {
		so.basePath = "/" + so.basePath
	}

	if !strings.HasSuffix(so.basePath, "/") {
		so.basePath += "/"
	}

	if _, err := os.Stat("downloads/tunneler"); err == nil {
		so.features["tunneler-download"] = true
	}

	return so
}

func (o serverOptions) tlsEnabled() bool {
	if o.tlsKeyPath == "" {
		return false
	}

	if o.tlsCrtPath == "" {
		return false
	}

	return true
}

func (o serverOptions) featured(f string) bool {
	_, ok := o.features[f]

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

func ServeWithUnixSocketGID(g int) ServerOption {
	return func(o *serverOptions) {
		o.unixSocketGID = g
	}
}

// GetOptions - GET /options.
func GetOptions(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	)

	if !role.Allowed("options", "list") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"listing options not allowed",
			"user",
			user,
		)
		err := weberror.NewWebError(
			nil,
			"listing options not allowed for %s",
			user,
		)

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
	_, _ = w.Write(body)

	return nil
}
