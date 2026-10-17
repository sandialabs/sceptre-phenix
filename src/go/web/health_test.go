package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/dgrijalva/jwt-go"

	v1 "phenix/types/version/v1"
	"phenix/util/mm/mmtest"
	"phenix/web/health"
	"phenix/web/rbac"
)

// healthyMinimega answers the readiness check's minimega commands: version,
// two namespaces and a mesh of one node.
func healthyMinimega(cmd mmtest.Command) []*minicli.Response {
	switch cmd.Base {
	case "version":
		return []*minicli.Response{mmtest.Text("head", "minimega 2.9 abc 2026-01-01")}
	case "namespace":
		return []*minicli.Response{mmtest.Tabular("head", []string{"namespace", "vlans", "active"},
			[]string{"exp1", "101-200", "true"},
			[]string{"exp2", "201-300", "false"},
		)}
	case "mesh status":
		return []*minicli.Response{mmtest.Tabular("head", []string{"size", "degree", "peers", "context", "port"},
			[]string{"1", "0", "0", "phenix", "9000"},
		)}
	}

	return nil
}

// healthBody is the parts of a health response the tests read.
type healthBody struct {
	Status string                     `json:"status"`
	Notes  []string                   `json:"notes"`
	Checks map[string][]health.Result `json:"checks"`
}

// getHealth requests a health endpoint and decodes its answer.
func getHealth(t *testing.T, url string, header ...string) (*http.Response, healthBody) {
	t.Helper()

	resp := do(t, http.MethodGet, url, "", header...)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if got := resp.Header.Get("Content-Type"); got != health.MediaType {
		t.Fatalf("GET %s: Content-Type %q (body %q), want %q", url, got, raw, health.MediaType)
	}

	var body healthBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decoding %q: %v", raw, err)
	}

	return resp, body
}

// The disk check measures the test machine's own disks, so these tests leave
// it out.
const readyz = "/readyz?exclude=disk"

//nolint:paralleltest // uses the process-wide fake minimega
func TestReadyzReportsMinimega(t *testing.T) {
	useTestStore(t)

	received := mmtest.Use(t, healthyMinimega)
	server := serveRouter(t, testPublic(), "")

	resp, body := getHealth(t, server.URL+readyz)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %+v, want 200", resp.StatusCode, body)
	}

	if r := body.Checks[health.KeyMinimegaNamespaces]; len(r) != 1 || r[0].ObservedValue != float64(2) {
		t.Errorf("namespaces = %+v, want 2", r)
	}

	if r := body.Checks[health.KeyMinimegaVersion]; len(r) != 1 || r[0].ObservedValue != "2.9" || r[0].Output == "" {
		t.Errorf("version = %+v, want 2.9 with its output (auth is disabled)", r)
	}

	if r := body.Checks[health.KeyStore]; len(r) != 1 || r[0].Status != health.Pass {
		t.Errorf("store = %+v, want a pass", r)
	}

	for _, key := range []string{health.KeyUptime, health.KeyGoroutines, health.KeyWebsocketQueue, health.KeyMeshNodes} {
		if len(body.Checks[key]) != 1 {
			t.Errorf("%s = %+v, want one result", key, body.Checks[key])
		}
	}

	// a second probe within the TTL reuses the first's results
	getHealth(t, server.URL+readyz)

	cmds := received()

	for _, base := range []string{"version", "namespace", "mesh status"} {
		if n := mmtest.Count(cmds, base); n != 1 {
			t.Errorf("%q sent %d times, want once", base, n)
		}
	}

	// listing namespaces must not switch to one
	for _, cmd := range cmds {
		if cmd.Namespace != "" {
			t.Errorf("%q sent in namespace %q, want none", cmd.Base, cmd.Namespace)
		}
	}
}

//nolint:paralleltest // uses the process-wide fake minimega
func TestReadyzFailsWithoutMinimega(t *testing.T) {
	useTestStore(t)

	mmtest.Use(t, func(mmtest.Command) []*minicli.Response { return mmtest.Disconnect() })

	server := serveRouter(t, testPublic(), "")

	resp, body := getHealth(t, server.URL+readyz)

	if resp.StatusCode != http.StatusServiceUnavailable || body.Status != "fail" {
		t.Errorf("status %d (%s), want 503 (fail)", resp.StatusCode, body.Status)
	}

	if r := body.Checks[health.KeyMinimegaTime]; len(r) != 1 || r[0].Status != health.Fail || r[0].ObservedValue != nil {
		t.Errorf("response time = %+v, want a failure without a value", r)
	}
}

// A minimega that never answers fails the check at its timeout, rather than
// holding the probe.
//
//nolint:paralleltest // uses the process-wide fake minimega
func TestReadyzAnswersWhenMinimegaHangs(t *testing.T) {
	useTestStore(t)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	mmtest.Use(t, func(mmtest.Command) []*minicli.Response {
		<-release

		return nil
	})

	server := serveRouter(t, testPublic(), "")

	start := time.Now()
	resp, body := getHealth(t, server.URL+readyz)

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("answered after %s, want about 3s", took)
	}

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", resp.StatusCode)
	}

	if r := body.Checks[health.KeyMinimegaTime]; len(r) != 1 || r[0].Status != health.Fail {
		t.Errorf("response time = %+v, want a failure", r)
	}
}

// With auth enabled, probes need no token, but only a valid one shows the
// output fields.
//
//nolint:paralleltest // uses the process-wide fake minimega
func TestHealthNeedsNoToken(t *testing.T) {
	useTestStore(t)

	mmtest.Use(t, healthyMinimega)

	server := serveRouter(t, testPublic(), "secret")

	for _, path := range []string{"/healthz", readyz} {
		resp, body := getHealth(t, server.URL+path, "X-Phenix-Auth-Token", "Bearer garbage")

		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, resp.StatusCode)
		}

		if len(body.Notes) == 0 || body.Notes[0] != health.OutputOmitted {
			t.Errorf("%s: notes %v, want output omitted", path, body.Notes)
		}

		for key, results := range body.Checks {
			for _, r := range results {
				if r.Output != "" {
					t.Errorf("%s: %s has output %q", path, key, r.Output)
				}
			}
		}

		// the store check still names its kind of store
		if r := body.Checks[health.KeyStore]; path == "/healthz" && (len(r) != 1 || r[0].ComponentID != "bolt") {
			t.Errorf("liveness store = %+v, want the bolt store", r)
		}
	}
}

// With auth enabled, a valid token, in the header or the query, shows the
// output fields.
//
//nolint:paralleltest // uses the process-wide fake minimega
func TestHealthShowsOutputWithValidToken(t *testing.T) {
	useTestStore(t)

	mmtest.Use(t, healthyMinimega)

	user := rbac.NewUser("alice", "password")
	if user == nil {
		t.Fatal("creating user")
	}

	if err := user.SetRole(&rbac.Role{Spec: &v1.RoleSpec{Name: "Global Viewer"}}); err != nil {
		t.Fatal(err)
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	if err := user.AddToken(token, "test"); err != nil {
		t.Fatal(err)
	}

	server := serveRouter(t, testPublic(), "secret")

	resp, body := getHealth(t, server.URL+readyz, "X-Phenix-Auth-Token", "Bearer "+token)

	if resp.StatusCode != http.StatusOK || len(body.Notes) != 0 {
		t.Errorf("status %d, notes %v, want 200 with no notes", resp.StatusCode, body.Notes)
	}

	if r := body.Checks[health.KeyMinimegaVersion]; len(r) != 1 || r[0].Output == "" {
		t.Errorf("version = %+v, want its output", r)
	}

	// passing liveness checks have no output, but none is omitted either
	if _, body = getHealth(t, server.URL+"/healthz?token="+token); len(body.Notes) != 0 {
		t.Errorf("liveness notes %v with a token in the query, want none", body.Notes)
	}
}

// The health endpoints answer themselves, not with the UI's index.html that
// the router's NotFound fallback serves for unknown paths.
//
//nolint:paralleltest // uses the process-wide test store
func TestHealthzIsNotTheUIIndex(t *testing.T) {
	useTestStore(t)

	server := serveRouter(t, testPublic(), "")

	// the fallback is in place
	unknown := do(t, http.MethodGet, server.URL+"/no-such-page", "")

	if raw, _ := io.ReadAll(unknown.Body); !strings.Contains(string(raw), "<title>phenix</title>") {
		t.Fatalf("unknown path = %q, want the UI index", raw)
	}

	for _, path := range []string{"/healthz", "/healthz?verbose=1"} {
		resp := do(t, http.MethodGet, server.URL+path, "")
		raw, _ := io.ReadAll(resp.Body)

		if strings.Contains(string(raw), "<title>") || resp.Header.Get("Content-Type") != health.MediaType {
			t.Errorf("GET %s = %s %q, want a health response", path, resp.Header.Get("Content-Type"), raw)
		}
	}

	resp := do(t, http.MethodPost, server.URL+"/healthz", "")

	if raw, _ := io.ReadAll(resp.Body); resp.StatusCode != http.StatusMethodNotAllowed || strings.Contains(string(raw), "<title>") {
		t.Errorf("POST /healthz = %d %q, want 405 without the UI index", resp.StatusCode, raw)
	}
}

// Liveness checks the store, and HEAD answers without a body.
//
//nolint:paralleltest // uses the process-wide test store
func TestHealthzChecksStore(t *testing.T) {
	useTestStore(t)

	server := serveRouter(t, testPublic(), "")

	resp, body := getHealth(t, server.URL+"/healthz")

	if resp.StatusCode != http.StatusOK || body.Status != "pass" {
		t.Errorf("status %d (%s), want 200 (pass)", resp.StatusCode, body.Status)
	}

	if r := body.Checks[health.KeyStore]; len(r) != 1 || r[0].Status != health.Pass || r[0].ComponentID != "bolt" {
		t.Errorf("checks = %+v, want a passing bolt store check", body.Checks)
	}

	if len(body.Checks[health.KeyUptime]) != 1 {
		t.Errorf("checks = %+v, want uptime", body.Checks)
	}

	head := do(t, http.MethodHead, server.URL+"/healthz", "")

	if raw, _ := io.ReadAll(head.Body); head.StatusCode != http.StatusOK || len(raw) != 0 ||
		!strings.HasPrefix(head.Header.Get("Content-Type"), health.MediaType) {
		t.Errorf("HEAD /healthz = %d %q with %d bytes, want 200 health+json with none",
			head.StatusCode, head.Header.Get("Content-Type"), len(raw))
	}
}
