package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"phenix/util/common"
	"phenix/util/mm"
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
	// DefaultCaptureMaxSize is the most MiB of each packet capture file that minimega 3.4.0 or later writes.
	DefaultCaptureMaxSize = 10240

	// TunnelerDownloadDir holds the phenix-tunneler builds the server offers,
	// relative to the directory it starts in.
	TunnelerDownloadDir = "downloads/tunneler"
)

// The ui.features names. FeatureVMMount and FeatureMinimegaCaptures are set by
// hand; the server turns on the others itself when it finds what each one
// needs, and FeatureMinimegaCaptures when minimega supports experiment captures.
const (
	FeatureVMMount          = "vm-mount"
	FeatureWebShark         = "webshark"
	FeatureTunnelerDownload = "tunneler-download"
	FeatureFileServer       = "file-server"
	FeatureMinimegaCaptures = "minimega-captures"
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

	// captureMaxSize is the most MiB of each packet capture file; 0 sets no limit
	captureMaxSize int64
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
		captureMaxSize: DefaultCaptureMaxSize,
	}

	for _, opt := range opts {
		opt(&so)
	}

	so.basePath = NormalizeBasePath(so.basePath)

	if _, err := os.Stat(TunnelerDownloadDir); err == nil {
		so.features[FeatureTunnelerDownload] = true
	}

	if dir, ok := FindWebShark(os.Stat); ok {
		so.websharkDir = dir
		so.features[FeatureWebShark] = true
	}

	return so
}

// checkFeatures refuses a feature set by hand that cannot work: webshark
// without a WebShark build would serve files from no directory.
func (o serverOptions) checkFeatures() error {
	if o.featured(FeatureWebShark) && o.websharkDir == "" {
		return fmt.Errorf(
			"invalid ui.features: %s needs a WebShark build in %s, and phenix ui enables it automatically when it finds one",
			FeatureWebShark, strings.Join(websharkDirs, " or "),
		)
	}

	return nil
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

// featureNames lists the features that are on, with FeatureMinimegaCaptures
// once minimega answers with a version that supports experiment captures.
func (o serverOptions) featureNames() []string {
	features := make([]string, 0, len(o.features)+1)

	for f := range o.features {
		features = append(features, f)
	}

	if !o.featured(FeatureMinimegaCaptures) && mm.CaptureFeatures() {
		features = append(features, FeatureMinimegaCaptures)
	}

	return features
}

// startCaptureSupport forces the capture support on for FeatureMinimegaCaptures,
// and asks minimega for its version until it answers or ctx is done.
func (o serverOptions) startCaptureSupport(ctx context.Context) {
	if o.featured(FeatureMinimegaCaptures) {
		mm.SetCaptureFeaturesForced(true)
	}

	mm.StartVersionCheck(ctx)
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

// ServeWithCaptureMaxSize sets the most MiB of each packet capture file that
// minimega 3.4.0 or later writes; 0 sets no limit.
func ServeWithCaptureMaxSize(mib int64) ServerOption {
	return func(o *serverOptions) {
		o.captureMaxSize = mib
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
