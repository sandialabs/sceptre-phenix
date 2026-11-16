package middleware

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "phenix/types/version/v1"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
)

func TestRedactURI(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"/api/v1/experiments":                        "/api/v1/experiments",
		"/api/v1/vms/a/screenshot.png?token=abc.def": "/api/v1/vms/a/screenshot.png?token=REDACTED",
		"/vnc?size=1&token=abc.def":                  "/vnc?size=1&token=REDACTED",
		"/vnc?token=abc.def&size=1":                  "/vnc?token=REDACTED",
		"/forward?access_token=abc":                  "/forward?access_token=REDACTED",
		"/api/v1/experiments/token":                  "/api/v1/experiments/token",
		"/api/v1/files?name=token=abc":               "/api/v1/files?name=token=REDACTED",
		"/api/v1/login":                              "/api/v1/login",
		"":                                           "",
		"/api/v1/vms/a/screenshot.png?%74oken=abc":   "/api/v1/vms/a/screenshot.png?%74oken=REDACTED",
		"/vnc?size=1&tok%65n=abc.def&x=2":            "/vnc?size=1&tok%65n=REDACTED",
		"/vnc?%74%6F%6B%65%6E=abc.def":               "/vnc?%74%6F%6B%65%6E=REDACTED",
		"/vnc?access%5Ftok%65n=abc":                  "/vnc?access%5Ftok%65n=REDACTED",
		"/vnc?tok%65n=abc&token=def":                 "/vnc?tok%65n=REDACTED",
		"/vnc?a=token=b&%74oken=abc":                 "/vnc?a=token=REDACTED",
		"/vnc?%74oken":                               "/vnc?%74oken",
		"/vnc?%zzoken=abc&size=1":                    "/vnc?%zzoken=abc&size=1",
		"/vnc?tokens=abc":                            "/vnc?tokens=abc",
		"/api/v1/experiments?a=1&b=2":                "/api/v1/experiments?a=1&b=2",
	}

	for uri, want := range tests {
		t.Run(uri, func(t *testing.T) {
			t.Parallel()

			if got := RedactURI(uri); got != want {
				t.Errorf("RedactURI(%q) = %q, want %q", uri, got, want)
			}
		})
	}
}

// FuzzRedactURI checks that no query parameter whose decoded name ends in
// token keeps a value other than REDACTED. An empty value holds no token.
func FuzzRedactURI(f *testing.F) {
	for _, seed := range []string{
		"/vnc?token=abc", "/vnc?%74oken=abc&size=1", "/vnc?a=1&tok%65n=abc", "/x?a=token=b", "/x", "?&=",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, uri string) {
		got := RedactURI(uri)

		_, query, _ := strings.Cut(got, "?")
		values, _ := url.ParseQuery(query)

		for name, list := range values {
			if !strings.HasSuffix(name, "token") {
				continue
			}

			for _, v := range list {
				if v != "REDACTED" && v != "" {
					t.Errorf("RedactURI(%q) = %q, which keeps %s=%q, want %s=REDACTED", uri, got, name, v, name)
				}
			}
		}
	})
}

// httpRecords returns the "HTTP request" records logs holds.
func httpRecords(t *testing.T, logs *plogtest.Logs, msg string) []map[string]any {
	t.Helper()

	return logs.Records(t, plogtest.Message(msg))
}

//nolint:paralleltest // captures the process-wide log
func TestLogRequests(t *testing.T) {
	role := rbac.Role{Spec: &v1.RoleSpec{Name: "Global Viewer"}}

	tests := map[string]struct {
		ctx  func(context.Context) context.Context
		want map[string]any
	}{
		"signed in": {
			ctx: func(ctx context.Context) context.Context {
				ctx = context.WithValue(ctx, ContextKeyUser, "alice")

				return context.WithValue(ctx, ContextKeyRole, role)
			},
			want: map[string]any{"user": "alice", "role": "Global Viewer"},
		},
		"public path, no user": {
			ctx:  func(ctx context.Context) context.Context { return ctx },
			want: map[string]any{"user": nil, "role": nil},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			logs := plogtest.Capture(t)

			var reached bool

			h := LogRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true

				w.WriteHeader(http.StatusTeapot)
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/vms/a/screenshot.png?token=secret.jwt.value", nil)
			req = req.WithContext(tc.ctx(req.Context()))
			req.RemoteAddr = "192.0.2.1:1234"

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if !reached || rec.Code != http.StatusTeapot {
				t.Errorf("LogRequests: handler reached = %t, status = %d, want true, %d", reached, rec.Code, http.StatusTeapot)
			}

			if strings.Contains(logs.String(), "secret.jwt.value") {
				t.Errorf("LogRequests logged the token: %s", logs.String())
			}

			records := httpRecords(t, logs, "HTTP request")
			if len(records) != 1 {
				t.Fatalf("LogRequests logged %d HTTP request records, want 1", len(records))
			}

			got := map[string]any{}
			for _, k := range []string{"method", "url", "address", "user", "role"} {
				got[k] = records[0][k]
			}

			want := map[string]any{
				"method":  http.MethodGet,
				"url":     "/api/v1/vms/a/screenshot.png?token=REDACTED",
				"address": "192.0.2.1:1234",
				"user":    tc.want["user"],
				"role":    tc.want["role"],
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("LogRequests record mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

//nolint:paralleltest // captures the process-wide log
func TestLogFullCopiesTheResponse(t *testing.T) {
	logs := plogtest.Capture(t)

	var seenBody string

	h := LogFull(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)

		w.Header().Set("X-Result", "made")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created " + seenBody))
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/experiments", strings.NewReader(`{"name":"exp"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seenBody != `{"name":"exp"}` {
		t.Errorf("LogFull: handler read body %q, want the request body", seenBody)
	}

	if rec.Code != http.StatusCreated {
		t.Errorf("LogFull: status = %d, want %d", rec.Code, http.StatusCreated)
	}

	if got := rec.Header().Get("X-Result"); got != "made" {
		t.Errorf("LogFull: X-Result header = %q, want made", got)
	}

	if got := rec.Body.String(); got != `created {"name":"exp"}` {
		t.Errorf("LogFull: body = %q, want the handler's body", got)
	}

	if reqs := httpRecords(t, logs, "HTTP request"); len(reqs) != 1 || !strings.Contains(reqs[0]["request"].(string), `{"name":"exp"}`) {
		t.Errorf("LogFull request records = %v, want one with the body", reqs)
	}

	if res := httpRecords(t, logs, "HTTP response"); len(res) != 1 || !strings.Contains(res[0]["response"].(string), "201 Created") {
		t.Errorf("LogFull response records = %v, want one with the status", res)
	}
}

// failingBody is a request body whose reads fail.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingBody) Close() error             { return nil }

//nolint:paralleltest // captures the process-wide log
func TestLogFullLogsAnUnreadableRequest(t *testing.T) {
	logs := plogtest.Capture(t)

	h := LogFull(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = failingBody{}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("LogFull with an unreadable body: status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	if n := len(httpRecords(t, logs, "error parsing http req")); n != 1 {
		t.Errorf("LogFull logged %d request parse errors, want 1", n)
	}
}

// LogFull leaves a hijacked connection alone: it logs no response and writes
// nothing after the handler.
//
//nolint:paralleltest // captures the process-wide log
func TestLogFullAfterHijack(t *testing.T) {
	logs := plogtest.Capture(t)

	done := make(chan struct{})
	full := LogFull(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("LogFull response writer is not an http.Hijacker")

			return
		}

		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Errorf("Hijack() error = %v", err)

			return
		}

		defer func() { _ = conn.Close() }()

		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nX-Raw: yes\r\n\r\n")
		_ = buf.Flush()
	}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)

		full.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	if _, err := io.WriteString(conn, "GET /ws HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("reading the hijacked response: %v", err)
	}

	_ = resp.Body.Close()

	<-done

	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("X-Raw") != "yes" {
		t.Errorf("hijacked response = %d %v, want 101 with X-Raw", resp.StatusCode, resp.Header)
	}

	if n := len(httpRecords(t, logs, "HTTP response")); n != 0 {
		t.Errorf("LogFull logged %d responses after a hijack, want 0", n)
	}
}

func TestHijackWithoutAHijacker(t *testing.T) {
	t.Parallel()

	rec := &HijackableResponseRecorder{ResponseRecorder: httptest.NewRecorder(), writer: httptest.NewRecorder()}

	if _, _, err := rec.Hijack(); err == nil {
		t.Error("Hijack() on a writer that cannot hijack = nil error, want an error")
	}

	if rec.hijacked {
		t.Error("Hijack() on a writer that cannot hijack marked the response hijacked")
	}
}

func TestAnswerOptions(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodOptions, http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			var reached bool

			allowed := func(*http.Request) []string { return []string{http.MethodGet, http.MethodOptions} }

			h := AnswerOptions(allowed)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true

				_, _ = w.Write([]byte("ran"))
			}))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/experiments/exp", nil))

			options := method == http.MethodOptions

			if reached == options {
				t.Errorf("%s: handler reached = %t, want %t", method, reached, !options)
			}

			if options && (rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || rec.Header().Get("Allow") != "GET, OPTIONS") {
				t.Errorf("OPTIONS = %d %q, Allow %q; want 204 with no body, Allow %q",
					rec.Code, rec.Body.String(), rec.Header().Get("Allow"), "GET, OPTIONS")
			}
		})
	}
}
