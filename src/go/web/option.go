package web

import "strings"

type ServerOption func(*serverOptions)

type serverOptions struct {
	endpoint  string
	jwtKey    string
	users     []string
	allowCORS bool

	tlsKeyPath string
	tlsCrtPath string

	logMiddleware string

	publishLogs  bool
	phenixLogs   string
	minimegaLogs string

	unbundled bool
	basePath  string
}

func newServerOptions(opts ...ServerOption) serverOptions {
	o := serverOptions{
		endpoint: ":3000",
		users:    []string{"admin@foo.com:foobar:Global Admin"},
		basePath: "/",
	}

	for _, opt := range opts {
		opt(&o)
	}

	if o.phenixLogs != "" || o.minimegaLogs != "" {
		o.publishLogs = true
	}

	if !strings.HasPrefix(o.basePath, "/") {
		o.basePath = "/" + o.basePath
	}

	if !strings.HasSuffix(o.basePath, "/") {
		o.basePath = o.basePath + "/"
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

func ServeWithUsers(u string) ServerOption {
	return func(o *serverOptions) {
		o.users = strings.Split(u, "|")
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

func ServePhenixLogs(p string) ServerOption {
	return func(o *serverOptions) {
		o.phenixLogs = p
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
