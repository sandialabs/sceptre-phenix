package web

import (
	"os"
	"strings"
	"time"
)

type ServerOption func(*serverOptions)

type serverOptions struct {
	endpoint   string
	unixSocket string
	users      []string
	allowCORS  bool

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

func ServeOnUnixSocket(s string) ServerOption {
	return func(o *serverOptions) {
		o.unixSocket = s
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
