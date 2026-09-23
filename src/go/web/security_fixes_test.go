package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// userRequest returns a request made by username with a role built from the
// given policies.
func userRequest(
	method, target, body, username string,
	vars map[string]string,
	policies ...*v1.PolicySpec,
) *http.Request {
	req := roleRequest(method, target, body, vars, policies...)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, username)

	return req.WithContext(ctx)
}

func allowAll(resource string, verbs ...string) *v1.PolicySpec {
	return &v1.PolicySpec{
		Resources:     []string{resource},
		ResourceNames: []string{"*", "*/*"},
		Verbs:         verbs,
	}
}

func TestGetVNCWebSocketRequiresPermission(t *testing.T) {
	t.Parallel()

	vars := map[string]string{"exp": "exp-a", "name": "vm1"}
	tests := []struct {
		name     string
		policies []*v1.PolicySpec
	}{
		{name: "no vnc permission", policies: []*v1.PolicySpec{allowAll("vms", "get")}},
		{
			name: "vnc for another VM",
			policies: []*v1.PolicySpec{{
				Resources:     []string{"vms/vnc"},
				ResourceNames: []string{"exp-a/vm2"},
				Verbs:         []string{"get"},
			}},
		},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		GetVNCWebSocket(rec, roleRequest(http.MethodGet, "/", "", vars, test.policies...))

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: got status %d, want %d", test.name, rec.Code, http.StatusForbidden)
		}
	}
}

// TestGetLogsForbiddenStops verifies a denied request gets only the 403, not
// the handler's further output.
func TestGetLogsForbiddenStops(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	GetLogs(rec, roleRequest(http.MethodGet, "/logs", "", nil, allowAll("logs", "list")))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusForbidden)
	}

	if got := rec.Body.String(); got != "forbidden\n" {
		t.Fatalf("denied request got more output: %q", got)
	}
}

// capturesTestMM fakes minimega's experiment capture list.
type capturesTestMM struct {
	mm.MM

	captures []mm.Capture
}

func (m *capturesTestMM) GetExperimentCaptures(...mm.Option) []mm.Capture {
	return m.captures
}

// TestGetExperimentCapturesFiltersByExperimentVM verifies the capture list is
// filtered by <experiment>/<vm>, like every other VM check.
func TestGetExperimentCapturesFiltersByExperimentVM(t *testing.T) {
	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = &capturesTestMM{ //nolint:reassign // install test double
		captures: []mm.Capture{
			{VM: "web-server", Interface: 0, Filepath: "web.pcap"},
			{VM: "db-server", Interface: 0, Filepath: "db.pcap"},
		},
	}

	policy := &v1.PolicySpec{
		Resources:     []string{"experiments/captures"},
		ResourceNames: []string{"exp-a", "exp-a/web-server"},
		Verbs:         []string{"list"},
	}

	rec := httptest.NewRecorder()
	GetExperimentCaptures(rec, roleRequest(http.MethodGet, "/", "", map[string]string{"name": "exp-a"}, policy))

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "web-server") || strings.Contains(body, "db-server") {
		t.Fatalf("unexpected captures: %s", body)
	}
}

// createUserWithToken stores a user with a known password and one token.
func createUserWithToken(t *testing.T, username string) string {
	t.Helper()

	user := rbac.NewUser(username, "Testpass1!")
	if user == nil {
		t.Fatalf("creating user %s", username)
	}

	role := &rbac.Role{Spec: &v1.RoleSpec{Name: "Experiment User", Policies: []*v1.PolicySpec{allowAll("experiments", "get")}}}
	if err := user.SetRole(role); err != nil {
		t.Fatalf("setting user %s role: %v", username, err)
	}

	const token = "header.payload.signature"
	if err := user.AddToken(token, "test token"); err != nil {
		t.Fatalf("adding user %s token: %v", username, err)
	}

	return token
}

// TestConfigAPIHidesUserSecrets verifies User config password hashes and API
// tokens never leave the server through the configs API.
func TestConfigAPIHidesUserSecrets(t *testing.T) {
	useTestStore(t)
	createUserWithToken(t, "alice")
	createUserWithToken(t, "bob")

	viewer := allowAll("configs", "get", "list")
	vars := map[string]string{"kind": "user", "name": "alice"}

	rec := httptest.NewRecorder()
	if got := status(t, rec, GetConfig(rec, roleRequest(http.MethodGet, "/", "", vars, viewer))); got != http.StatusOK {
		t.Fatalf("getting config: status %d", got)
	}

	var cfg struct {
		Spec map[string]any `json:"spec"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decoding config: %v", err)
	}

	for _, field := range []string{"password", "tokens"} {
		if _, ok := cfg.Spec[field]; ok {
			t.Errorf("GET config returned %s", field)
		}
	}

	if cfg.Spec["username"] != "alice" {
		t.Errorf("GET config dropped other fields: %v", cfg.Spec)
	}

	for _, body := range []string{`["user/alice"]`, `["user/alice","user/bob"]`} {
		rec := httptest.NewRecorder()
		if got := status(t, rec, DownloadConfigs(rec, roleRequest(http.MethodPost, "/", body, nil, viewer))); got != http.StatusOK {
			t.Fatalf("downloading %s: status %d", body, got)
		}

		out := rec.Body.String()
		if strings.Contains(out, "$2a$") || strings.Contains(out, "test token") {
			t.Errorf("download of %s returned user secrets", body)
		}
	}
}

// TestUpdateUserConfigKeepsSecrets verifies editing a User config, which the
// configs API returns without secrets, keeps the stored password and tokens.
func TestUpdateUserConfigKeepsSecrets(t *testing.T) {
	useTestStore(t)
	token := createUserWithToken(t, "alice")

	admin := allowAll("configs", "get", "update")
	vars := map[string]string{"kind": "user", "name": "alice"}

	rec := httptest.NewRecorder()
	if got := status(t, rec, GetConfig(rec, roleRequest(http.MethodGet, "/", "", vars, admin))); got != http.StatusOK {
		t.Fatalf("getting config: status %d", got)
	}

	var cfg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decoding config: %v", err)
	}

	spec, _ := cfg["spec"].(map[string]any)
	spec["first_name"] = "Alicia"
	spec["tokens"] = map[string]any{"aW5qZWN0ZWQ=": "injected"}

	body, _ := json.Marshal(cfg)

	rec = httptest.NewRecorder()
	if got := status(t, rec, UpdateConfig(rec, roleRequest(http.MethodPut, "/", string(body), vars, admin))); got != http.StatusNoContent {
		t.Fatalf("updating config: status %d (%s)", got, rec.Body.String())
	}

	user, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatalf("getting user: %v", err)
	}

	if user.FirstName() != "Alicia" {
		t.Errorf("update was not applied: first name %q", user.FirstName())
	}

	if err := user.ValidatePassword("Testpass1!"); err != nil {
		t.Errorf("password was not kept: %v", err)
	}

	if err := user.ValidateToken(token); err != nil {
		t.Errorf("token was not kept: %v", err)
	}

	if err := user.ValidateToken("injected"); err == nil {
		t.Error("update added a token")
	}
}

// TestCreateUserTokenForOtherUser verifies users patch alone only allows
// creating your own tokens.
func TestCreateUserTokenForOtherUser(t *testing.T) {
	useTestStore(t)
	createUserWithToken(t, "alice")
	createUserWithToken(t, "admin")

	const body = `{"lifetime":"1h","desc":"test"}`

	patch := allowAll("users", "patch")
	tokens := allowAll("users/tokens", "create")

	tests := []struct {
		name     string
		target   string
		policies []*v1.PolicySpec
		want     int
	}{
		{name: "own token", target: "alice", policies: []*v1.PolicySpec{patch}, want: http.StatusOK},
		{name: "other user's token", target: "admin", policies: []*v1.PolicySpec{patch}, want: http.StatusForbidden},
		{name: "other user's token with users/tokens", target: "admin", policies: []*v1.PolicySpec{patch, tokens}, want: http.StatusOK},
		{name: "users/tokens without patch", target: "admin", policies: []*v1.PolicySpec{tokens}, want: http.StatusForbidden},
	}

	for _, test := range tests {
		rec := httptest.NewRecorder()
		req := userRequest(http.MethodPost, "/", body, "alice", map[string]string{"username": test.target}, test.policies...)
		req = mux.SetURLVars(req, map[string]string{"username": test.target})

		CreateUserToken(rec, req)

		if rec.Code != test.want {
			t.Errorf("%s: got status %d, want %d (%s)", test.name, rec.Code, test.want, rec.Body.String())
		}
	}
}
