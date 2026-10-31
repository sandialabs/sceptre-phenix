package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dgrijalva/jwt-go"

	"phenix/store/storetest"
	"phenix/util/plog"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// captureRecords records every log record logged during the test, as JSON
// lines; the returned function lists those logged so far.
func captureRecords(t *testing.T) func() []string {
	t.Helper()

	var (
		mu    sync.Mutex
		lines []string
	)

	key := t.Name() + "/records"

	plog.AddHandler(key, slog.NewJSONHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		lines = append(lines, string(p))
		mu.Unlock()

		return len(p), nil
	}), &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { plog.RemoveHandler(key) })

	return func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), lines...)
	}
}

// wantNoneHolds fails the test if a record holds any of secrets, or is a
// SECURITY record.
func wantNoneHolds(t *testing.T, records []string, secrets ...string) {
	t.Helper()

	for _, line := range records {
		if strings.Contains(line, `"type":"SECURITY"`) {
			t.Errorf("logged SECURITY record %s, want AUDIT events only", line)
		}

		for _, secret := range secrets {
			if strings.Contains(line, secret) {
				t.Errorf("logged record %s, want none holding %q", line, secret)
			}
		}
	}
}

// useOptions restores the server options when the test ends, and signs
// tokens with testJWTKey.
func useOptions(t *testing.T) {
	t.Helper()

	original := o
	t.Cleanup(func() { o = original })

	o.jwtKey = testJWTKey
}

// loginRequest is GET /login with the user and password.
func loginRequest(user, pass string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/login", nil)
	req.SetBasicAuth(user, pass)

	return req
}

// Signing in and out, by password or through a proxy header, is audited, and
// logs no SECURITY records, passwords or tokens.
//
//nolint:paralleltest // replaces the store and the server options
func TestSignInAndOutAreAudited(t *testing.T) {
	storetest.Use(t)
	createUserWithToken(t, "alice")
	useOptions(t)

	var (
		events  = captureAudit(t)
		records = captureRecords(t)
	)

	for _, tc := range []struct {
		name string
		req  *http.Request
		want int
	}{
		{"right password", loginRequest("alice", testPassword), http.StatusOK},
		{"wrong password", loginRequest("alice", "Wrongpass1!"), http.StatusUnauthorized},
		{"unknown user", loginRequest("mallory", testPassword), http.StatusUnauthorized},
	} {
		w := httptest.NewRecorder()
		weberror.ErrorHandler(Login).ServeHTTP(w, tc.req)

		if w.Code != tc.want {
			t.Errorf("GET /login with the %s = %d, want %d", tc.name, w.Code, tc.want)
		}
	}

	o.proxyAuthHeader = "X-Proxy-User"

	proxied := httptest.NewRequest(http.MethodGet, "/api/v1/login", nil)
	proxied.Header.Set("X-Proxy-User", "alice")

	for _, req := range []*http.Request{proxied, httptest.NewRequest(http.MethodGet, "/api/v1/login", nil)} {
		weberror.ErrorHandler(Login).ServeHTTP(httptest.NewRecorder(), req)
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/v1/logout", nil)
	ctx := context.WithValue(logout.Context(), middleware.ContextKeyUser, "alice")
	ctx = context.WithValue(ctx, middleware.ContextKeyJWT, testToken)

	w := httptest.NewRecorder()
	if weberror.ErrorHandler(Logout).ServeHTTP(w, logout.WithContext(ctx)); w.Code != http.StatusNoContent {
		t.Errorf("POST /logout = %d, want %d", w.Code, http.StatusNoContent)
	}

	got := events()

	wantAudit(t, got, "sign-in", "success", map[string]any{
		"user.name": "alice", "phenix.auth.method": "password", "event.category": "authentication",
	})
	wantAudit(t, got, "sign-in", "failure", map[string]any{"user.name": "alice", "error.message": "invalid credentials"})
	wantAudit(t, got, "sign-in", "failure", map[string]any{"user.name": "mallory", "error.message": "invalid credentials"})
	wantAudit(t, got, "sign-in", "success", map[string]any{"user.name": "alice", "phenix.auth.method": "proxy-header"})
	wantAudit(t, got, "sign-in", "failure", map[string]any{"phenix.auth.method": "proxy-header", "error.message": "no proxy user"})
	wantAudit(t, got, "sign-out", "success", map[string]any{"user.name": "alice", "url.path": "/api/v1/logout"})

	wantNoneHolds(t, records(), testPassword, "Wrongpass1!", testToken)
}

// proxyJWTRequest is a request to target with body, as the proxy JWT
// middleware passes it on: holding the proxy's token with the claims.
func proxyJWTRequest(method, target, body, raw string, claims jwt.MapClaims) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	token := &jwt.Token{Raw: raw, Claims: claims}

	return req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, token))
}

// signupBody is a sign-up request for the user.
func signupBody(user string) string {
	return `{"username":"` + user + `","password":"` + testPassword + `","first_name":"f","last_name":"l"}`
}

// Signing in and signing up through a proxy JWT or proxy header are audited,
// and log no proxy token or password.
//
//nolint:paralleltest // replaces the store and the server options
func TestProxySignInAndSignupAreAudited(t *testing.T) {
	useDefaultRoles(t) // a signed-up user has the disabled role
	createUserWithToken(t, "alice")
	useOptions(t)

	const (
		aliceJWT = "proxy.alice-jwt.signature"
		bobJWT   = "proxy.bob-jwt.signature"
		noneJWT  = "proxy.no-user-jwt.signature"
	)

	var (
		events  = captureAudit(t)
		records = captureRecords(t)
		noUser  = jwt.MapClaims{"exp": float64(0)}
	)

	for _, tc := range []struct {
		name    string
		handler weberror.ErrorHandler
		req     *http.Request
		want    int
	}{
		{"GET /login with alice's JWT", Login,
			proxyJWTRequest(http.MethodGet, "/api/v1/login", "", aliceJWT, jwt.MapClaims{"sub": "alice"}), http.StatusOK},
		{"GET /login with a JWT naming no user", Login,
			proxyJWTRequest(http.MethodGet, "/api/v1/login", "", noneJWT, noUser), http.StatusUnauthorized},
		{"POST /signup as bob with bob's JWT", Signup,
			proxyJWTRequest(http.MethodPost, "/api/v1/signup", signupBody("bob"), bobJWT, jwt.MapClaims{"user": "bob"}),
			http.StatusOK},
		{"POST /signup as mallory with bob's JWT", Signup,
			proxyJWTRequest(http.MethodPost, "/api/v1/signup", signupBody("mallory"), bobJWT, jwt.MapClaims{"sub": "bob"}),
			http.StatusUnauthorized},
		{"POST /signup with a JWT naming no user", Signup,
			proxyJWTRequest(http.MethodPost, "/api/v1/signup", signupBody("nobody"), noneJWT, noUser),
			http.StatusUnauthorized},
		{"POST /signup as carol with a password", Signup,
			httptest.NewRequest(http.MethodPost, "/api/v1/signup", strings.NewReader(signupBody("carol"))), http.StatusOK},
	} {
		w := httptest.NewRecorder()
		if tc.handler.ServeHTTP(w, tc.req); w.Code != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, w.Code, tc.want)
		}
	}

	o.proxyAuthHeader = "X-Proxy-User"

	mismatch := httptest.NewRequest(http.MethodPost, "/api/v1/signup", strings.NewReader(signupBody("eve")))
	mismatch.Header.Set("X-Proxy-User", "dave")

	w := httptest.NewRecorder()
	if weberror.ErrorHandler(Signup).ServeHTTP(w, mismatch); w.Code != http.StatusUnauthorized {
		t.Errorf("POST /signup as eve through dave's proxy header = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	got := events()

	wantAudit(t, got, "sign-in", "success", map[string]any{"user.name": "alice", "phenix.auth.method": "proxy-jwt"})
	wantAudit(t, got, "sign-in", "failure", map[string]any{
		"phenix.auth.method": "proxy-jwt", "error.message": "username not found in JWT claims",
	})
	wantAudit(t, got, "user-create", "success", map[string]any{"user.name": "bob", "user.target.name": "bob"})
	wantAudit(t, got, "sign-in", "success", map[string]any{"user.name": "bob", "phenix.auth.method": "proxy-jwt"})
	wantAudit(t, got, "user-create", "failure", map[string]any{
		"user.target.name": "mallory", "event.reason": "proxy user mismatch",
	})
	wantAudit(t, got, "user-create", "failure", map[string]any{
		"user.target.name": "nobody", "error.message": "username not found in JWT claims",
	})
	wantAudit(t, got, "user-create", "success", map[string]any{"user.target.name": "carol"})
	wantAudit(t, got, "sign-in", "success", map[string]any{"user.name": "carol", "phenix.auth.method": "password"})
	wantAudit(t, got, "user-create", "failure", map[string]any{"user.target.name": "eve", "event.reason": "proxy user mismatch"})

	wantNoneHolds(t, records(), aliceJWT, bobJWT, noneJWT, testPassword)
}

// Creating users and their tokens, changing their names, roles and passwords,
// and deleting them are audited, naming the user acted on.
//
//nolint:paralleltest // replaces the store and the server options
func TestUserChangesAreAudited(t *testing.T) {
	useDefaultRoles(t)
	createUserWithToken(t, "test-user")
	useOptions(t)

	var (
		events  = captureAudit(t)
		records = captureRecords(t)
		target  = map[string]string{"username": "real"}
		self    = map[string]string{"username": "test-user"}
	)

	weberror.ErrorHandler(CreateUser).ServeHTTP(httptest.NewRecorder(), roleRequest(http.MethodPost, "/api/v1/users",
		createUserBody("other", experimentUserRole), nil, testRole("users", "get")))
	weberror.ErrorHandler(CreateUser).ServeHTTP(httptest.NewRecorder(), roleRequest(http.MethodPost, "/api/v1/users",
		createUserBody("real", experimentUserRole), nil, testRole(resourceUsers, "create")))

	const update = `{"first_name":"R","role_name":"Experiment Admin","password":"Wrongpass1!","new_password":"Newpass1!x"}`

	w := httptest.NewRecorder()
	weberror.ErrorHandler(UpdateUser).
		ServeHTTP(w, roleRequest(http.MethodPatch, "/api/v1/users/real", update, target, testRole(resourceUsers, verbPatch, "real")))

	if w.Code != http.StatusBadRequest {
		t.Errorf("PATCH /users/real with a wrong password = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// the UI sends the user's current role with every edit, which is no change
	w = httptest.NewRecorder()
	weberror.ErrorHandler(UpdateUser).
		ServeHTTP(w, roleRequest(http.MethodPatch, "/api/v1/users/real", `{"last_name":"L","role_name":"`+experimentUserRole+`"}`,
			target, testRole(resourceUsers, verbPatch, "real")))

	if w.Code != http.StatusOK {
		t.Errorf("PATCH /users/real with its current role = %d, want %d", w.Code, http.StatusOK)
	}

	weberror.ErrorHandler(DeleteUser).ServeHTTP(httptest.NewRecorder(), roleRequest(http.MethodDelete, "/api/v1/users/test-user", "", self,
		testRole(resourceUsers, "delete", "*")))
	weberror.ErrorHandler(DeleteUser).ServeHTTP(httptest.NewRecorder(), roleRequest(http.MethodDelete, "/api/v1/users/real", "", target,
		testRole(resourceUsers, "delete", "real")))

	w = httptest.NewRecorder()
	weberror.ErrorHandler(CreateUserToken).
		ServeHTTP(w, roleRequest(http.MethodPost, "/api/v1/users/test-user/tokens", `{"lifetime":"1h"}`, self,
			testRole(resourceUsers, verbPatch, "test-user")))

	var created CreateTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Token == "" {
		t.Fatalf("POST /users/test-user/tokens = %d %s, want a token", w.Code, w.Body.String())
	}

	got := events()

	wantDenied(t, got, "user-create")
	wantAudit(t, got, "user-create", "success", map[string]any{
		"user.name": "test-user", "user.target.name": "real", "user.target.roles": []string{experimentUserRole},
		"event.category": "iam",
	})
	wantAudit(t, got, "user-update", "success", map[string]any{"user.target.name": "real", "phenix.user.changed": []string{"first_name"}})
	if n := countAudit(got, "user-role-change", "failure", map[string]any{"user.target.name": "real"}); n != 1 {
		t.Errorf("refused user-role-change events = %d, want 1 (none for the unchanged role)", n)
	}

	wantAudit(t, got, "user-role-change", "failure", map[string]any{"user.target.name": "real", "event.reason": "forbidden"})
	wantAudit(t, got, "user-password-change", "failure", map[string]any{"user.target.name": "real"})
	wantAudit(t, got, "user-delete", "failure", map[string]any{"user.target.name": "test-user", "event.reason": "own user"})
	wantAudit(t, got, "user-delete", "success", map[string]any{"user.target.name": "real"})
	wantAudit(t, got, "user-token-create", "success", map[string]any{"user.target.name": "test-user"})

	wantNoneHolds(t, records(), testPassword, "Wrongpass1!", "Newpass1!x", created.Token)
}

// Creating, updating and deleting configs are audited with the config's
// Kind/name, including a create that fails because the name is taken.
//
//nolint:paralleltest // replaces the store
func TestConfigChangesAreAudited(t *testing.T) {
	storetest.Use(t)

	events := captureAudit(t)

	for _, body := range []string{configJSON("Role", "intruder"), revisionTestTopology, revisionTestTopology} {
		w := httptest.NewRecorder()
		_ = CreateConfig(w, roleRequest(http.MethodPost, "/api/v1/configs", body, nil, topologyConfigs("create")))
	}

	demo := map[string]string{"kind": "topology", "name": "demo"}

	w := httptest.NewRecorder()
	_ = UpdateConfig(w, roleRequest(http.MethodPut, "/api/v1/configs/topology/demo", revisionTestTopology, demo,
		topologyConfigs("create")))

	w = httptest.NewRecorder()
	if err := DeleteConfig(w, roleRequest(http.MethodDelete, "/api/v1/configs/topology/demo", "", demo,
		topologyConfigs("delete"))); err != nil {
		t.Fatalf("DELETE /configs/topology/demo error = %v", err)
	}

	got := events()

	wantAudit(t, got, "config-create", "failure", map[string]any{"phenix.config.name": "Role/intruder", "event.reason": "forbidden"})
	wantAudit(t, got, "config-create", "success", map[string]any{
		"phenix.config.name": "Topology/demo", "event.category": "configuration", "event.type": []string{"creation"},
	})
	wantAudit(t, got, "config-create", "failure", map[string]any{"phenix.config.name": "Topology/demo", "event.reason": nil})
	wantAudit(t, got, "config-update", "failure", map[string]any{"phenix.config.name": "Topology/demo", "event.reason": "forbidden"})
	wantAudit(t, got, "config-delete", "success", map[string]any{"phenix.config.name": "Topology/demo"})
}

// The experiment routes audit their refusals, failures and successes.
func TestExperimentActionsAreAudited(t *testing.T) {
	stopped := namedExperiment(t, "stopped", "")
	useTestStore(t, stopped, namedExperiment(t, "running", "2024-01-01T00:00:00Z"))

	var (
		events = captureAudit(t)
		denied = serveAs(t, "bob", testRole("vms", "get", "*")).URL + "/api/v1/experiments"
		alice  = serveAs(t, "alice", combinedRole(
			testRole("experiments", "patch", "stopped"),
			testRole("experiments", "delete", "running"),
			testRole("experiments/trigger", "delete", "stopped"),
		)).URL + "/api/v1/experiments"
	)

	refusals := []struct{ method, path, action string }{
		{http.MethodPost, "", "experiment-create"},
		{http.MethodPatch, "/stopped", "experiment-update"},
		{http.MethodDelete, "/stopped", "experiment-delete"},
		{http.MethodPost, "/stopped/start", "experiment-start"},
		{http.MethodPost, "/stopped/stop", "experiment-stop"},
		{http.MethodPost, "/stopped/schedule", "experiment-schedule"},
		{http.MethodPost, "/stopped/trigger?apps=a", "app-trigger"},
		{http.MethodDelete, "/stopped/trigger?apps=a", "app-trigger-cancel"},
	}

	for _, tc := range refusals {
		if resp := do(t, tc.method, denied+tc.path, "{}"); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s experiments%s without permission = %d, want %d", tc.method, tc.path, resp.StatusCode, http.StatusForbidden)
		}
	}

	do(t, http.MethodPatch, alice+"/stopped", "{}")
	do(t, http.MethodDelete, alice+"/running", "")
	do(t, http.MethodDelete, alice+"/stopped/trigger?apps=a,b", "")

	got := events()

	for _, tc := range refusals {
		wantDenied(t, got, tc.action)
	}

	wantAudit(t, got, "experiment-update", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "stopped", "event.category": "configuration",
	})
	wantAudit(t, got, "experiment-delete", "failure", map[string]any{
		"phenix.experiment.name": "running", "error.message": "cannot delete a running experiment",
	})
	wantAudit(t, got, "app-trigger-cancel", "success", map[string]any{
		"phenix.experiment.name": "stopped", "phenix.app.names": "a,b", "event.category": "process",
	})
}

// A single-VM power action is audited when its request is answered; a bulk
// action's VMs are audited as refused when it is asked for, and the others
// when its job finishes.
func TestVMPowerActionsAreAudited(t *testing.T) {
	useVMExperiment(t, true, "a", "b", "c")

	var (
		registry = useJobs(t)
		events   = captureAudit(t)
	)

	fakeVMAction(t, "pause", map[string]error{"b": errors.New("minimega says no")}, nil)

	var (
		allowed = serveAs(t, "alice", testRole("vms/stop", "update", "exp/a", "exp/b")).URL
		denied  = serveAs(t, "bob", testRole("vms", "get", "exp/*")).URL
		vms     = "/api/v1/experiments/exp/vms/"
	)

	do(t, http.MethodPost, allowed+vms+"a/stop", "")
	do(t, http.MethodPost, allowed+vms+"b/stop", "")
	do(t, http.MethodPost, denied+vms+"a/stop", "")

	if err := cache.LockVMForStarting("exp", "a"); err != nil {
		t.Fatalf("cache.LockVMForStarting(exp, a) error = %v", err)
	}

	do(t, http.MethodPost, allowed+vms+"a/stop", "")
	cache.UnlockVM("exp", "a")

	got := events()

	wantAudit(t, got, "vm-stop", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "exp", "phenix.vm.name": "a", "event.category": "host",
		"url.path": vms + "a/stop",
	})
	wantAudit(t, got, "vm-stop", "failure", map[string]any{"phenix.vm.name": "b", "error.message": "minimega says no"})
	wantAudit(t, got, "vm-stop", "failure", map[string]any{"user.name": "bob", "event.reason": "forbidden"})
	wantAudit(t, got, "vm-stop", "failure", map[string]any{
		"phenix.vm.name": "a", "error.message": (&cache.LockedError{What: "vm", Name: "a", Status: cache.StatusStarting}).Error(),
	})

	resp := postVMActions(t, allowed, "pause", "a", "b", "c")

	var started struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding POST vms/actions: %v", err)
	}

	waitJob(t, registry, started.ID)

	job := map[string]any{"phenix.job.id": started.ID}
	got = waitAudit(t, events, "vm-stop", "failure", map[string]any{"phenix.job.id": started.ID, "phenix.vm.name": "b"})

	wantAudit(t, got, "vm-stop", "failure", map[string]any{"phenix.vm.name": "c", "event.reason": "forbidden"})
	wantAudit(t, got, "vm-stop", "success", map[string]any{"phenix.job.id": started.ID, "phenix.vm.name": "a", "user.name": "alice"})

	if n := countAudit(got, "vm-stop", "success", job) + countAudit(got, "vm-stop", "failure", job); n != 2 {
		t.Errorf("bulk pause of a, b and c audited %d VMs in its job, want 2 (c was refused)", n)
	}
}
