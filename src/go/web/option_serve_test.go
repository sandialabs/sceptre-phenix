package web

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Each ServeWith option changes only its own setting of the defaults.
func TestServeOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts []ServerOption
		set  func(*serverOptions)
	}{
		"no options": {set: func(*serverOptions) {}},
		"endpoint": {
			opts: []ServerOption{ServeOnEndpoint("127.0.0.1:8080")},
			set:  func(o *serverOptions) { o.endpoint = "127.0.0.1:8080" },
		},
		"JWT key": {
			opts: []ServerOption{ServeWithJWTKey("secret")},
			set:  func(o *serverOptions) { o.jwtKey = "secret" },
		},
		"users": {
			opts: []ServerOption{ServeWithUsers([]string{"alice:pw:Global Admin", "bob:pw:VM Viewer"})},
			set:  func(o *serverOptions) { o.users = []string{"alice:pw:Global Admin", "bob:pw:VM Viewer"} },
		},
		"no users keeps the default admin": {
			opts: []ServerOption{ServeWithUsers(nil), ServeWithUsers([]string{})},
			set:  func(*serverOptions) {},
		},
		"TLS": {
			opts: []ServerOption{ServeWithTLS("/etc/phenix/key.pem", "/etc/phenix/crt.pem")},
			set:  func(o *serverOptions) { o.tlsKeyPath, o.tlsCrtPath = "/etc/phenix/key.pem", "/etc/phenix/crt.pem" },
		},
		"middleware logging": {
			opts: []ServerOption{ServeWithMiddlewareLogging("full")},
			set:  func(o *serverOptions) { o.logMiddleware = "full" },
		},
		"minimega logs": {
			opts: []ServerOption{ServeMinimegaLogs("/var/log/minimega.log")},
			set:  func(o *serverOptions) { o.minimegaLogs = "/var/log/minimega.log" },
		},
		"unbundled": {opts: []ServerOption{ServeUnbundled()}, set: func(o *serverOptions) { o.unbundled = true }},
		"base path": {
			opts: []ServerOption{ServeBasePath("phenix")},
			set:  func(o *serverOptions) { o.basePath = "/phenix/" },
		},
		"file server endpoint": {
			opts: []ServerOption{ServeFileServerEndpoint(":3001")},
			set:  func(o *serverOptions) { o.fileServerEndpoint = ":3001" },
		},
		"minimega console": {
			opts: []ServerOption{ServeMinimegaConsole(true)},
			set:  func(o *serverOptions) { o.minimegaConsole = true },
		},
		"JWT lifetime": {
			opts: []ServerOption{ServeWithJWTLifetime(time.Hour)},
			set:  func(o *serverOptions) { o.jwtLifetime = time.Hour },
		},
		"proxy auth header": {
			opts: []ServerOption{ServeWithProxyAuthHeader("X-Remote-User")},
			set:  func(o *serverOptions) { o.proxyAuthHeader = "X-Remote-User" },
		},
		"features": {
			opts: []ServerOption{ServeWithFeatures([]string{FeatureVMMount}), ServeWithFeatures([]string{FeatureFileServer})},
			set: func(o *serverOptions) {
				o.features[FeatureVMMount] = true
				o.features[FeatureFileServer] = true
			},
		},
		"unix socket group": {
			opts: []ServerOption{ServeWithUnixSocketGID(1001)},
			set:  func(o *serverOptions) { o.unixSocketGID = 1001 },
		},
		"shutdown grace period": {
			opts: []ServerOption{ServeWithShutdownGracePeriod(0)},
			set:  func(o *serverOptions) { o.shutdownGrace = 0 },
		},
		"netflow max size": {
			opts: []ServerOption{ServeWithNetflowMaxSize(1 << 10)},
			set:  func(o *serverOptions) { o.netflowMaxSize = 1 << 10 },
		},
		"the last of an option": {
			opts: []ServerOption{ServeOnEndpoint(":1"), ServeOnEndpoint(":2")},
			set:  func(o *serverOptions) { o.endpoint = ":2" },
		},
	}

	compare := cmp.Options{cmp.AllowUnexported(serverOptions{}), cmpopts.IgnoreFields(serverOptions{}, "systemLog")}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := newServerOptions()
			tc.set(&want)

			if diff := cmp.Diff(want, newServerOptions(tc.opts...), compare); diff != "" {
				t.Errorf("newServerOptions(%s) mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// The defaults serve on :3000 under / as the default admin, without TLS, and
// with the system log file's path.
func TestServerOptionDefaults(t *testing.T) {
	t.Parallel()

	got := newServerOptions()

	want := serverOptions{
		endpoint: ":3000", users: []string{DefaultAdminName + ":foobar:Global Admin"}, basePath: "/",
		jwtLifetime: 24 * time.Hour, features: got.features, websharkDir: got.websharkDir,
		shutdownGrace: 5 * time.Second, netflowMaxSize: 100 << 20,
	}

	if diff := cmp.Diff(want, got, cmp.AllowUnexported(serverOptions{}), cmpopts.IgnoreFields(serverOptions{}, "systemLog")); diff != "" {
		t.Errorf("newServerOptions() mismatch (-want +got):\n%s", diff)
	}

	if got.systemLog == nil {
		t.Error("newServerOptions().systemLog = nil, want the system log file's path")
	}

	if got.tlsEnabled() {
		t.Error("newServerOptions().tlsEnabled() = true, want false")
	}
}

// TLS is on only with both a key and a certificate.
func TestTLSEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key, crt string
		want     bool
	}{
		{"", "", false},
		{"key.pem", "", false},
		{"", "crt.pem", false},
		{"key.pem", "crt.pem", true},
	}

	for _, tc := range tests {
		if got := newServerOptions(ServeWithTLS(tc.key, tc.crt)).tlsEnabled(); got != tc.want {
			t.Errorf("ServeWithTLS(%q, %q) tlsEnabled() = %t, want %t", tc.key, tc.crt, got, tc.want)
		}
	}
}
