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
	"github.com/google/go-cmp/cmp"

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
		t.Fatalf("reading GET %s: %v", url, err)
	}

	if got := resp.Header.Get("Content-Type"); got != health.MediaType {
		t.Fatalf("GET %s Content-Type = %q, want %q (body %q)", url, got, health.MediaType, raw)
	}

	var body healthBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("GET %s = %q, not a health response: %v", url, raw, err)
	}

	return resp, body
}

// oneResult returns the check's result, and reports whether the body has
// exactly one.
func oneResult(body healthBody, key string) (health.Result, bool) {
	if results := body.Checks[key]; len(results) == 1 {
		return results[0], true
	}

	return health.Result{}, false
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
		t.Fatalf("GET %s = %d, want %d: %+v", readyz, resp.StatusCode, http.StatusOK, body)
	}

	if r, ok := oneResult(body, health.KeyMinimegaNamespaces); !ok {
		t.Errorf("GET %s %s = %+v, want one result", readyz, health.KeyMinimegaNamespaces, body.Checks[health.KeyMinimegaNamespaces])
	} else if r.ObservedValue != float64(2) {
		t.Errorf("GET %s %s observed value = %v, want 2", readyz, health.KeyMinimegaNamespaces, r.ObservedValue)
	}

	if r, ok := oneResult(body, health.KeyMinimegaVersion); !ok {
		t.Errorf("GET %s %s = %+v, want one result", readyz, health.KeyMinimegaVersion, body.Checks[health.KeyMinimegaVersion])
	} else {
		if r.ObservedValue != "2.9" {
			t.Errorf("GET %s %s observed value = %v, want 2.9", readyz, health.KeyMinimegaVersion, r.ObservedValue)
		}

		if r.Output == "" {
			t.Errorf("GET %s %s output is empty, want minimega's version (auth is disabled)", readyz, health.KeyMinimegaVersion)
		}
	}

	if r, ok := oneResult(body, health.KeyStore); !ok {
		t.Errorf("GET %s %s = %+v, want one result", readyz, health.KeyStore, body.Checks[health.KeyStore])
	} else if r.Status != health.Pass {
		t.Errorf("GET %s %s status = %s, want %s", readyz, health.KeyStore, r.Status, health.Pass)
	}

	for _, key := range []string{health.KeyUptime, health.KeyGoroutines, health.KeyWebsocketQueue, health.KeyMeshNodes} {
		if _, ok := oneResult(body, key); !ok {
			t.Errorf("GET %s %s = %+v, want one result", readyz, key, body.Checks[key])
		}
	}

	// a second probe within the TTL reuses the first's results
	getHealth(t, server.URL+readyz)

	cmds := received()

	for _, base := range []string{"version", "namespace", "mesh status"} {
		if n := mmtest.Count(cmds, base); n != 1 {
			t.Errorf("two GET %s sent minimega %q %d times, want 1", readyz, base, n)
		}
	}

	// listing namespaces must not switch to one
	for _, cmd := range cmds {
		if cmd.Namespace != "" {
			t.Errorf("GET %s sent minimega %q in namespace %q, want none", readyz, cmd.Base, cmd.Namespace)
		}
	}
}

//nolint:paralleltest // uses the process-wide fake minimega
func TestReadyzFailsWithoutMinimega(t *testing.T) {
	useTestStore(t)

	mmtest.Use(t, func(mmtest.Command) []*minicli.Response { return mmtest.Disconnect() })

	server := serveRouter(t, testPublic(), "")

	resp, body := getHealth(t, server.URL+readyz)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET %s without minimega = %d, want %d", readyz, resp.StatusCode, http.StatusServiceUnavailable)
	}

	if body.Status != string(health.Fail) {
		t.Errorf("GET %s without minimega status = %q, want %q", readyz, body.Status, health.Fail)
	}

	r, ok := oneResult(body, health.KeyMinimegaTime)
	if !ok {
		t.Fatalf("GET %s without minimega %s = %+v, want one result", readyz, health.KeyMinimegaTime, body.Checks[health.KeyMinimegaTime])
	}

	if r.Status != health.Fail {
		t.Errorf("GET %s without minimega %s status = %s, want %s", readyz, health.KeyMinimegaTime, r.Status, health.Fail)
	}

	if r.ObservedValue != nil {
		t.Errorf("GET %s without minimega %s observed value = %v, want none", readyz, health.KeyMinimegaTime, r.ObservedValue)
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
		t.Errorf("GET %s with a hung minimega took %s, want about 3s", readyz, took)
	}

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET %s with a hung minimega = %d, want %d", readyz, resp.StatusCode, http.StatusServiceUnavailable)
	}

	if r, ok := oneResult(body, health.KeyMinimegaTime); !ok {
		t.Errorf("GET %s with a hung minimega %s = %+v, want one result",
			readyz, health.KeyMinimegaTime, body.Checks[health.KeyMinimegaTime])
	} else if r.Status != health.Fail {
		t.Errorf("GET %s with a hung minimega %s status = %s, want %s", readyz, health.KeyMinimegaTime, r.Status, health.Fail)
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
			t.Errorf("GET %s with an invalid token = %d, want %d", path, resp.StatusCode, http.StatusOK)
		}

		if diff := cmp.Diff([]string{health.OutputOmitted}, body.Notes); diff != "" {
			t.Errorf("GET %s with an invalid token notes mismatch (-want +got):\n%s", path, diff)
		}

		for key, results := range body.Checks {
			for _, r := range results {
				if r.Output != "" {
					t.Errorf("GET %s with an invalid token %s output = %q, want none", path, key, r.Output)
				}
			}
		}

		// the store check still names its kind of store
		if path == "/healthz" {
			if r, ok := oneResult(body, health.KeyStore); !ok {
				t.Errorf("GET %s %s = %+v, want one result", path, health.KeyStore, body.Checks[health.KeyStore])
			} else if r.ComponentID != "bolt" {
				t.Errorf("GET %s %s component = %q, want bolt", path, health.KeyStore, r.ComponentID)
			}
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

	user, err := rbac.NewUser("alice", "password", "", "")
	if err != nil {
		t.Fatalf("rbac.NewUser(alice) error = %v", err)
	}

	if err := user.SetRole(&rbac.Role{Spec: &v1.RoleSpec{Name: "Global Viewer"}}); err != nil {
		t.Fatalf("User.SetRole(Global Viewer) error = %v", err)
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("secret"))
	if err != nil {
		t.Fatalf("signing a token: %v", err)
	}

	if err := user.AddToken(token, "test"); err != nil {
		t.Fatalf("User.AddToken() error = %v", err)
	}

	server := serveRouter(t, testPublic(), "secret")

	resp, body := getHealth(t, server.URL+readyz, "X-Phenix-Auth-Token", "Bearer "+token)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s with a valid token = %d, want %d", readyz, resp.StatusCode, http.StatusOK)
	}

	if len(body.Notes) != 0 {
		t.Errorf("GET %s with a valid token notes = %v, want none", readyz, body.Notes)
	}

	if r, ok := oneResult(body, health.KeyMinimegaVersion); !ok {
		t.Errorf("GET %s with a valid token %s = %+v, want one result",
			readyz, health.KeyMinimegaVersion, body.Checks[health.KeyMinimegaVersion])
	} else if r.Output == "" {
		t.Errorf("GET %s with a valid token %s output is empty, want minimega's version", readyz, health.KeyMinimegaVersion)
	}

	// passing liveness checks have no output, but none is omitted either
	if _, body = getHealth(t, server.URL+"/healthz?token="+token); len(body.Notes) != 0 {
		t.Errorf("GET /healthz?token=<valid> notes = %v, want none", body.Notes)
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

	if raw := readBody(t, unknown); !strings.Contains(raw, "<title>phenix</title>") {
		t.Fatalf("GET /no-such-page = %q, want the UI index", raw)
	}

	for _, path := range []string{"/healthz", "/healthz?verbose=1"} {
		resp := do(t, http.MethodGet, server.URL+path, "")

		if raw := readBody(t, resp); strings.Contains(raw, "<title>") {
			t.Errorf("GET %s = %q, want a health response, not the UI index", path, raw)
		}

		if got := resp.Header.Get("Content-Type"); got != health.MediaType {
			t.Errorf("GET %s Content-Type = %q, want %q", path, got, health.MediaType)
		}
	}

	resp := do(t, http.MethodPost, server.URL+"/healthz", "")

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}

	if raw := readBody(t, resp); strings.Contains(raw, "<title>") {
		t.Errorf("POST /healthz = %q, want no UI index", raw)
	}
}

// Liveness checks the store, and HEAD answers without a body.
//
//nolint:paralleltest // uses the process-wide test store
func TestHealthzChecksStore(t *testing.T) {
	useTestStore(t)

	server := serveRouter(t, testPublic(), "")

	resp, body := getHealth(t, server.URL+"/healthz")

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if body.Status != string(health.Pass) {
		t.Errorf("GET /healthz status = %q, want %q", body.Status, health.Pass)
	}

	if r, ok := oneResult(body, health.KeyStore); !ok {
		t.Errorf("GET /healthz %s = %+v, want one result", health.KeyStore, body.Checks[health.KeyStore])
	} else {
		if r.Status != health.Pass {
			t.Errorf("GET /healthz %s status = %s, want %s", health.KeyStore, r.Status, health.Pass)
		}

		if r.ComponentID != "bolt" {
			t.Errorf("GET /healthz %s component = %q, want bolt", health.KeyStore, r.ComponentID)
		}
	}

	if _, ok := oneResult(body, health.KeyUptime); !ok {
		t.Errorf("GET /healthz %s = %+v, want one result", health.KeyUptime, body.Checks[health.KeyUptime])
	}

	head := do(t, http.MethodHead, server.URL+"/healthz", "")

	if head.StatusCode != http.StatusOK {
		t.Errorf("HEAD /healthz = %d, want %d", head.StatusCode, http.StatusOK)
	}

	if got := head.Header.Get("Content-Type"); !strings.HasPrefix(got, health.MediaType) {
		t.Errorf("HEAD /healthz Content-Type = %q, want %q", got, health.MediaType)
	}

	if raw := readBody(t, head); raw != "" {
		t.Errorf("HEAD /healthz body = %q, want none", raw)
	}
}

// The disk check also measures the store's directory, unless the endpoint names
// no file.
func TestDiskPathsIncludeStore(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint string
		want     []health.DiskPath
	}{
		"bolt": {
			endpoint: "bolt:///etc/phenix/store.bdb",
			want: []health.DiskPath{
				{ID: "files-dir", Path: "/phenix/images"},
				{ID: "phenix-base", Path: "/phenix"},
				{ID: "store", Path: "/etc/phenix"},
			},
		},
		"not bolt": {
			endpoint: "etcd://localhost:2379",
			want: []health.DiskPath{
				{ID: "files-dir", Path: "/phenix/images"},
				{ID: "phenix-base", Path: "/phenix"},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := diskPathsFor("/phenix/images", "/phenix", tc.endpoint)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("diskPathsFor(%q) mismatch (-want +got):\n%s", tc.endpoint, diff)
			}
		})
	}
}
