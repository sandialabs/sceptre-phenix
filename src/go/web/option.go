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

const (
	defaultJWTLifetime = 24 * time.Hour
	// DefaultShutdownGracePeriod is how long shutting the server down may take.
	DefaultShutdownGracePeriod = 5 * time.Second
	// DefaultNetflowMaxSize is the most bytes of each netflow capture recorded.
	DefaultNetflowMaxSize = 100 << 20
)

type ServerOption func(*serverOptions)

type serverOptions struct {
	endpoint string
	users    []string

	tlsKeyPath string
	tlsCrtPath string

	logMiddleware string
	minimegaLogs  string
	// systemLog is the phenix system log file's current path, or "" for none
	systemLog func() string

	unbundled          bool
	basePath           string
	fileServerEndpoint string
	minimegaConsole    bool

	jwtKey      string
	jwtLifetime time.Duration

	proxyAuthHeader string

	features map[string]bool

	websharkDir string

	unixSocketGID int

	shutdownGrace time.Duration

	netflowMaxSize int64
}

func newServerOptions(opts ...ServerOption) serverOptions {
	so := serverOptions{ //nolint:exhaustruct // partial initialization
		endpoint:    ":3000",
		users:       []string{DefaultAdminName + ":foobar:Global Admin"},
		basePath:    "/",
		jwtLifetime: defaultJWTLifetime,
		features:    make(map[string]bool),
		systemLog:   plog.LogFilePath,

		shutdownGrace:  DefaultShutdownGracePeriod,
		netflowMaxSize: DefaultNetflowMaxSize,
	}

	for _, opt := range opts {
		opt(&so)
	}

	so.basePath = NormalizeBasePath(so.basePath)

	if _, err := os.Stat("downloads/tunneler"); err == nil {
		so.features["tunneler-download"] = true
	}

	if dir, ok := findWebShark(); ok {
		so.websharkDir = dir
		so.features["webshark"] = true
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

// NormalizeBasePath returns the path the server serves under for p: p with a
// leading and a trailing slash added when either is missing.
func NormalizeBasePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	if !strings.HasSuffix(p, "/") {
		p += "/"
	}

	return p
}

// ServeFileServerEndpoint sets the optional experiment file upload server endpoint.
func ServeFileServerEndpoint(e string) ServerOption {
	return func(o *serverOptions) {
		o.fileServerEndpoint = e
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
		for _, feature := range f {
			o.features[feature] = true
		}
	}
}

func ServeWithUnixSocketGID(g int) ServerOption {
	return func(o *serverOptions) {
		o.unixSocketGID = g
	}
}

// ServeWithShutdownGracePeriod sets how long shutting the server down may
// take; 0 closes everything without waiting.
func ServeWithShutdownGracePeriod(d time.Duration) ServerOption {
	return func(o *serverOptions) {
		o.shutdownGrace = d
	}
}

// ServeWithNetflowMaxSize sets the most bytes of each netflow capture
// recorded; 0 does not record captures.
func ServeWithNetflowMaxSize(n int64) ServerOption {
	return func(o *serverOptions) {
		o.netflowMaxSize = n
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

	// workflow-dry-run tells clients the workflow endpoints honor dryRun,
	// pending and expect; older servers ignore them and apply for real.
	options := map[string]any{
		"bridge-mode":      common.BridgeMode,
		"deploy-mode":      common.DeployMode,
		"use-gre-mesh":     common.UseGREMesh,
		"base-dir.injects": common.InjectsBase,
		"workflow-dry-run": true,
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
