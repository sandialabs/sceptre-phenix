package web

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/web/capability"
	"phenix/web/rbac"
)

const fileServerProxyHeader = "X-Forwarded-User"

// useFileServerAuth sets the server options a file server with jwtKey and
// proxyHeader signs users in with, and stores test-experiment, the
// global-admin role and alice, who may upload files to test-experiment and
// signs in with testPassword.
func useFileServerAuth(t *testing.T, jwtKey, proxyHeader string) {
	t.Helper()

	original := o
	t.Cleanup(func() { o = original })

	o.jwtKey, o.proxyAuthHeader, o.jwtLifetime = jwtKey, proxyHeader, time.Hour

	useTestStore(t, testExperiment(t, ""), namedExperiment(t, "hidden-experiment", ""), store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Role", Metadata: store.ConfigMetadata{Name: "global-admin"},
		Spec: map[string]any{"roleName": "Global Admin", "policies": rolePolicies(adminRole())},
	})

	if err := store.Create(&store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "User", Metadata: store.ConfigMetadata{Name: "alice"},
		Spec: map[string]any{"username": "alice", "password": testPasswordHash()},
	}); err != nil {
		t.Fatalf("creating user alice: %v", err)
	}

	alice, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatalf("rbac.GetUser(alice) error = %v", err)
	}

	role := testRole("experiments/files", "create", "test-experiment")
	role.Spec.Name = "Uploader"

	if err := alice.SetRole(&role); err != nil {
		t.Fatalf("setting alice's role: %v", err)
	}
}

// fileServerAnswer is what the file server answered a request with.
type fileServerAnswer struct {
	Status   int
	Location string
	Cookie   string // the auth cookie's value, "cleared" when it is expired, or empty when not set
}

// serveFileServer sends the request to h, with the auth cookie holding cookie
// when it is not empty, and returns the answer and its body.
func serveFileServer(t *testing.T, h http.Handler, req *http.Request, cookie string) (fileServerAnswer, string) {
	t.Helper()

	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: fileServerTokenCookie, Value: cookie})
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := fileServerAnswer{Status: rec.Code, Location: rec.Header().Get("Location")}

	for _, c := range rec.Result().Cookies() {
		if c.Name != fileServerTokenCookie {
			continue
		}

		if c.MaxAge < 0 {
			got.Cookie = "cleared"

			continue
		}

		if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
			t.Errorf("auth cookie = %+v, want HttpOnly, SameSite=Lax and Path=/", c)
		}

		got.Cookie = c.Value
	}

	return got, rec.Body.String()
}

// loginForm is a POST /login of the sign-in form.
func loginForm(user, pass string) *http.Request {
	form := url.Values{"username": {user}, "password": {pass}}

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req
}

// cookieToken returns the JWT an auth cookie value holds.
func cookieToken(t *testing.T, value string) string {
	t.Helper()

	token, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decoding auth cookie %q: %v", value, err)
	}

	return string(token)
}

// tokenCookie is the auth cookie value that holds token.
func tokenCookie(token string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(token))
}

// fileServerToken is a token for user that expires after lifetime, signed
// with key.
func fileServerToken(t *testing.T, user, key string, lifetime time.Duration) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user, "exp": time.Now().Add(lifetime).Unix(),
	}).SignedString([]byte(key))
	if err != nil {
		t.Fatalf("signing a token for %s: %v", user, err)
	}

	return signed
}

func fileServerGet(target string) *http.Request {
	return httptest.NewRequest(http.MethodGet, target, nil)
}

// Without a JWT key the file server signs no one in: every request is
// global-admin and the upload page has no logout link.
func TestFileServerWithoutAKeyHasNoAuth(t *testing.T) {
	useFileServerAuth(t, "", "")

	h := newFileServer(":0", "", "").Handler

	got, body := serveFileServer(t, h, fileServerGet("/"), "")
	if diff := cmp.Diff(fileServerAnswer{Status: http.StatusOK}, got); diff != "" {
		t.Errorf("GET / without a key mismatch (-want +got):\n%s", diff)
	}

	for _, want := range []string{"Upload experiment files", `<option value="test-experiment">`, `<option value="hidden-experiment">`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / without a key body does not hold %q:\n%s", want, body)
		}
	}

	if strings.Contains(body, "Logout") {
		t.Errorf("GET / without a key shows a logout link:\n%s", body)
	}

	if got, _ := serveFileServer(t, h, fileServerGet("/nothing-here"), ""); got.Status != http.StatusNotFound {
		t.Errorf("GET /nothing-here without a key = %d, want %d", got.Status, http.StatusNotFound)
	}
}

// With a JWT key, a request without a session goes to the sign-in page; a
// good sign-in sets the auth cookie and goes to the upload page, which the
// cookie then opens; logout clears the cookie.
func TestFileServerSignInAndOut(t *testing.T) {
	useFileServerAuth(t, testJWTKey, "")
	events := captureAudit(t)

	h := newFileServer(":0", testJWTKey, "").Handler
	toLogin := fileServerAnswer{Status: http.StatusSeeOther, Location: "/login"}

	for _, req := range []*http.Request{fileServerGet("/"), fileServerGet("/upload"), httptest.NewRequest(http.MethodPost, "/upload", nil)} {
		if got, _ := serveFileServer(t, h, req, ""); got != toLogin {
			t.Errorf("%s %s without a session = %+v, want %+v", req.Method, req.URL.Path, got, toLogin)
		}
	}

	got, body := serveFileServer(t, h, fileServerGet("/login"), "")
	if got != (fileServerAnswer{Status: http.StatusOK}) || !strings.Contains(body, `name="password"`) {
		t.Errorf("GET /login = %+v, want 200 with the sign-in form:\n%s", got, body)
	}

	// an unknown user gets the same error as a wrong password
	for _, user := range []string{"alice", "mallory"} {
		got, body = serveFileServer(t, h, loginForm(user, "Wrongpass1!"), "")
		if got != (fileServerAnswer{Status: http.StatusOK}) || !strings.Contains(body, "invalid credentials") {
			t.Errorf("POST /login as %s with a wrong password = %+v, want 200 with an error and no cookie:\n%s", user, got, body)
		}
	}

	got, _ = serveFileServer(t, h, loginForm("alice", testPassword), "")
	if got.Status != http.StatusSeeOther || got.Location != "/" || got.Cookie == "" {
		t.Fatalf("POST /login with a good password = %+v, want 303 to / with the auth cookie", got)
	}

	cookie := got.Cookie
	session := cookieToken(t, cookie)

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(session, claims, func(*jwt.Token) (any, error) { return []byte(testJWTKey), nil }); err != nil {
		t.Fatalf("the auth cookie holds %q, not a token signed with the key: %v", session, err)
	}

	if claims["sub"] != "alice" {
		t.Errorf("the auth cookie's token names %v, want alice", claims["sub"])
	}

	wantAudit(t, events(), "sign-in", "success", map[string]any{"user.name": "alice", "phenix.auth.method": "password"})

	got, body = serveFileServer(t, h, fileServerGet("/"), cookie)
	if got != (fileServerAnswer{Status: http.StatusOK}) {
		t.Errorf("GET / with the auth cookie = %+v, want 200", got)
	}

	for _, want := range []string{"Signed in as alice", `href="/logout"`, `<option value="test-experiment">`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / with the auth cookie body does not hold %q:\n%s", want, body)
		}
	}

	if strings.Contains(body, "hidden-experiment") {
		t.Errorf("GET / as alice lists hidden-experiment, which alice may not upload to:\n%s", body)
	}

	got, _ = serveFileServer(t, h, fileServerGet("/logout"), cookie)
	if want := (fileServerAnswer{Status: http.StatusSeeOther, Location: "/login", Cookie: "cleared"}); got != want {
		t.Errorf("GET /logout = %+v, want %+v", got, want)
	}

	wantAudit(t, events(), "sign-out", "success", map[string]any{"user.name": "alice"})

	// the session ended, so the old cookie no longer opens the upload page
	toLogin = fileServerAnswer{Status: http.StatusSeeOther, Location: "/login", Cookie: "cleared"}
	if got, _ := serveFileServer(t, h, fileServerGet("/"), cookie); got != toLogin {
		t.Errorf("GET / with the auth cookie after logout = %+v, want %+v", got, toLogin)
	}

	if got, _ := serveFileServer(t, h, httptest.NewRequest(http.MethodPut, "/login", nil), ""); got.Status != http.StatusMethodNotAllowed {
		t.Errorf("PUT /login = %d, want %d", got.Status, http.StatusMethodNotAllowed)
	}
}

// GET /logout revokes the session token that the auth cookie or header holds,
// and the capture grants given under it, but no other session or grant. A
// logout without a session, or with a bad one, only clears the cookie.
func TestFileServerLogoutEndsTheSession(t *testing.T) {
	toLogin := fileServerAnswer{Status: http.StatusSeeOther, Location: "/login", Cookie: "cleared"}

	tests := map[string]struct {
		jwtKey  string
		cookie  func(session string) string
		header  func(session string) string
		revoked bool
	}{
		"cookie": {
			jwtKey: testJWTKey, cookie: tokenCookie, revoked: true,
		},
		"header": {
			jwtKey: testJWTKey, header: func(session string) string { return "Bearer " + session }, revoked: true,
		},
		"no cookie": {
			jwtKey: testJWTKey,
		},
		"cookie that is not base64": {
			jwtKey: testJWTKey, cookie: func(string) string { return "!!not base64!!" },
		},
		"empty cookie": {
			jwtKey: testJWTKey, cookie: func(string) string { return tokenCookie("") },
		},
		"expired token": {
			jwtKey: testJWTKey, cookie: func(string) string { return tokenCookie(fileServerToken(t, "alice", testJWTKey, -time.Hour)) },
		},
		"development auth": {
			jwtKey: "dev|alice|global-admin", cookie: tokenCookie,
		},
		"without a key": {
			cookie: tokenCookie,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useFileServerAuth(t, testJWTKey, "")
			useCaptureGrants(t)

			login, _ := serveFileServer(t, newFileServer(":0", testJWTKey, "").Handler, loginForm("alice", testPassword), "")
			if login.Cookie == "" {
				t.Fatalf("POST /login as alice = %+v, want the auth cookie", login)
			}

			session := cookieToken(t, login.Cookie)

			mint := func(name, session string) capability.Grant {
				return captureGrants.Mint(name, capability.Grant{
					User: "alice", Session: session, Target: capability.Target{Exp: "test-experiment"},
					Ops: viewOps, Expires: time.Now().Add(time.Hour),
				})
			}

			grant, other := mint("session.pcap", session), mint("other.pcap", "other-session")

			req := fileServerGet("/logout")

			var cookie string
			if tc.cookie != nil {
				cookie = tc.cookie(session)
			}

			if tc.header != nil {
				req.Header.Set("X-Phenix-Auth-Token", tc.header(session))
			}

			h := newFileServer(":0", tc.jwtKey, "").Handler
			if got, body := serveFileServer(t, h, req, cookie); got != toLogin {
				t.Errorf("GET /logout = %+v, want %+v:\n%s", got, toLogin, body)
			}

			alice, err := rbac.GetUser("alice")
			if err != nil {
				t.Fatalf("rbac.GetUser(alice) error = %v", err)
			}

			if revoked := alice.ValidateToken(session) != nil; revoked != tc.revoked {
				t.Errorf("GET /logout revoked alice's session = %t, want %t", revoked, tc.revoked)
			}

			want := []capability.Grant{grant, other}
			if tc.revoked {
				want = []capability.Grant{other}
			}

			got := captureGrants.List("alice", "test-experiment")
			sort.Slice(got, func(i, j int) bool { return got[i].Session < got[j].Session })

			if diff := cmp.Diff(want, got, grantCmp); diff != "" {
				t.Errorf("capture grants after GET /logout mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A cookie or header token that is forged, expired, revoked, unsigned or not
// a token never opens the upload page and never gets a new session: the file
// server clears the cookie and sends the browser to the sign-in page. The
// sign-in page is public, so a well-signed token there only shows an error.
func TestFileServerRefusesBadTokens(t *testing.T) {
	useFileServerAuth(t, testJWTKey, "")

	h := newFileServer(":0", testJWTKey, "").Handler

	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "alice", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("making an unsigned token: %v", err)
	}

	toLogin := fileServerAnswer{Status: http.StatusSeeOther, Location: "/login", Cookie: "cleared"}
	loginError := fileServerAnswer{Status: http.StatusOK}

	tokens := map[string]struct {
		token string
		login fileServerAnswer // the answer to GET /login
	}{
		"forged":       {fileServerToken(t, "alice", "another-key", time.Hour), toLogin},
		"expired":      {fileServerToken(t, "alice", testJWTKey, -time.Hour), toLogin},
		"revoked":      {fileServerToken(t, "alice", testJWTKey, time.Hour), loginError}, // never stored as alice's
		"unknown user": {fileServerToken(t, "mallory", testJWTKey, time.Hour), loginError},
		"unsigned":     {unsigned, toLogin},
		"not a token":  {"not-a-token", toLogin},
	}

	for name, tc := range tokens {
		for _, target := range []string{"/", "/upload", "/login"} {
			want := toLogin
			if target == "/login" {
				want = tc.login
			}

			t.Run(name+" cookie "+target, func(t *testing.T) {
				if got, body := serveFileServer(t, h, fileServerGet(target), tokenCookie(tc.token)); got != want {
					t.Errorf("GET %s with a %s token cookie = %+v, want %+v:\n%s", target, name, got, want, body)
				}
			})

			t.Run(name+" header "+target, func(t *testing.T) {
				req := fileServerGet(target)
				req.Header.Set("X-Phenix-Auth-Token", "Bearer "+tc.token)

				if got, body := serveFileServer(t, h, req, ""); got != want {
					t.Errorf("GET %s with a %s token header = %+v, want %+v:\n%s", target, name, got, want, body)
				}
			})
		}
	}

	if got, _ := serveFileServer(t, h, fileServerGet("/"), "!!not base64!!"); got != toLogin {
		t.Errorf("GET / with an auth cookie that is not base64 = %+v, want %+v", got, toLogin)
	}
}

// GET /login with a valid session but no proxy makes no new session: it
// shows the error, and sets no cookie.
func TestFileServerLoginGetWithASessionMakesNoSession(t *testing.T) {
	useFileServerAuth(t, testJWTKey, "")

	h := newFileServer(":0", testJWTKey, "").Handler

	got, _ := serveFileServer(t, h, loginForm("alice", testPassword), "")
	if got.Cookie == "" {
		t.Fatalf("POST /login with a good password = %+v, want the auth cookie", got)
	}

	answer, body := serveFileServer(t, h, fileServerGet("/login"), got.Cookie)
	if answer != (fileServerAnswer{Status: http.StatusOK}) || !strings.Contains(body, "no username provided") {
		t.Errorf("GET /login with a session = %+v, want 200 with an error and no cookie:\n%s", answer, body)
	}
}

// A form that does not parse is shown as a sign-in error.
func TestFileServerLoginBadForm(t *testing.T) {
	useFileServerAuth(t, testJWTKey, "")

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	got, body := serveFileServer(t, newFileServer(":0", testJWTKey, "").Handler, req, "")
	if got != (fileServerAnswer{Status: http.StatusOK}) || !strings.Contains(body, `class="error"`) {
		t.Errorf("POST /login with a bad form = %+v, want 200 with an error:\n%s", got, body)
	}
}

// Behind an auth proxy, GET /login signs in the user the proxy header names,
// and only while the header names the token's user.
func TestFileServerProxySignIn(t *testing.T) {
	useFileServerAuth(t, testJWTKey, fileServerProxyHeader)

	h := newFileServer(":0", testJWTKey, fileServerProxyHeader).Handler

	as := func(target, user string) *http.Request {
		req := fileServerGet(target)
		if user != "" {
			req.Header.Set(fileServerProxyHeader, user)
		}

		return req
	}

	tests := map[string]struct {
		req   *http.Request
		error string // the error the sign-in page shows
	}{
		"no proxy user":      {as("/login", ""), "proxy authentication failed"},
		"unknown proxy user": {as("/login", "mallory"), "user not found"},
		"POST":               {loginForm("alice", testPassword), "log in with a GET request"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, body := serveFileServer(t, h, tc.req, "")
			if got != (fileServerAnswer{Status: http.StatusOK}) {
				t.Errorf("%s /login = %+v, want 200 and no cookie", tc.req.Method, got)
			}

			if !strings.Contains(body, tc.error) || !strings.Contains(body, "Proxy authentication is enabled") {
				t.Errorf("%s /login body does not hold %q and the proxy form:\n%s", tc.req.Method, tc.error, body)
			}
		})
	}

	got, _ := serveFileServer(t, h, as("/login", "alice"), "")
	if got.Status != http.StatusSeeOther || got.Location != "/" || got.Cookie == "" {
		t.Fatalf("GET /login as proxy user alice = %+v, want 303 to / with the auth cookie", got)
	}

	cookie := got.Cookie

	if answer, _ := serveFileServer(t, h, as("/", "alice"), cookie); answer != (fileServerAnswer{Status: http.StatusOK}) {
		t.Errorf("GET / as alice with her cookie = %+v, want 200", answer)
	}

	toLogin := fileServerAnswer{Status: http.StatusSeeOther, Location: "/login", Cookie: "cleared"}
	if answer, _ := serveFileServer(t, h, as("/", "bob"), cookie); answer != toLogin {
		t.Errorf("GET / as proxy user bob with alice's cookie = %+v, want %+v", answer, toLogin)
	}
}

func TestFileServerResponseRecorder(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		write  func(w http.ResponseWriter)
		status int
		body   string
	}{
		"nothing written is a 200": {func(http.ResponseWriter) {}, http.StatusOK, ""},
		"a write is a 200":         {func(w http.ResponseWriter) { _, _ = w.Write([]byte("hi")) }, http.StatusOK, "hi"},
		"the first status counts": {func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusCreated)
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("made"))
		}, http.StatusCreated, "made"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := newFileServerResponseRecorder()
			rec.Header().Add("X-Test", "a")
			rec.Header().Add("X-Test", "b")
			tc.write(rec)

			out := httptest.NewRecorder()
			rec.writeTo(out)

			if out.Code != tc.status || out.Body.String() != tc.body {
				t.Errorf("writeTo() = %d %q, want %d %q", out.Code, out.Body.String(), tc.status, tc.body)
			}

			if diff := cmp.Diff([]string{"a", "b"}, out.Header().Values("X-Test")); diff != "" {
				t.Errorf("writeTo() X-Test header mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
