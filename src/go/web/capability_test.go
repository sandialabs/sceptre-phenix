package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"

	"phenix/store"
	"phenix/web/rbac"
)

// testJWTKey is the JWT signing key of servers that sign users in.
const testJWTKey = "test-signing-key"

// useCapRecheck has every use of a capture grant check the user's access
// again, rather than trusting a check a few seconds old.
func useCapRecheck(t *testing.T) {
	t.Helper()

	original := capRecheckInterval
	capRecheckInterval = 0

	t.Cleanup(func() { capRecheckInterval = original })
}

// useTestUser stores the user with the role, signed in with a new session,
// and returns the session's JWT, signed with testJWTKey.
func useTestUser(t *testing.T, name string, role rbac.Role) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		jwtClaimSub: name,
		jwtClaimExp: time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(testJWTKey))
	if err != nil {
		t.Fatal(err)
	}

	useTestStore(t, store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "User",
		Metadata: store.ConfigMetadata{Name: name},
		Spec: map[string]any{
			"username": name,
			"tokens":   map[string]any{base64.StdEncoding.EncodeToString([]byte(signed)): "test"},
			"rbac":     map[string]any{"roleName": "test-role", "policies": rolePolicies(role)},
		},
	})

	return signed
}

// signedIn sends a request to the server with the session JWT.
func signedIn(t *testing.T, method, url, body, session string) *http.Response {
	t.Helper()

	return do(t, method, url, body, "X-Phenix-Auth-Token", "Bearer "+session)
}

// openSignedIn has the server name test-experiment's saved capture a.pcap
// for WebShark, as the user signed in with the session.
func openSignedIn(t *testing.T, server, session string) sharkCaptureResponse {
	t.Helper()

	resp := signedIn(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/webshark", `{"path":"a.pcap"}`, session)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("opening a.pcap: got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var capture sharkCaptureResponse
	if err := json.NewDecoder(resp.Body).Decode(&capture); err != nil {
		t.Fatalf("decoding capture: %v", err)
	}

	return capture
}

// Each use of a WebShark capture name checks again that the user who was
// given it may still read the capture, and revokes it when not.
func TestCaptureGrantRechecksAccess(t *testing.T) {
	tests := map[string]func(t *testing.T, session string){
		"role changed": func(t *testing.T, _ string) {
			t.Helper()

			u, err := rbac.GetUser("alice")
			if err != nil {
				t.Fatal(err)
			}

			role := testRole("experiments/files", "list", "test-experiment")
			if err := u.SetRole(&role); err != nil {
				t.Fatal(err)
			}
		},
		"session deleted": func(t *testing.T, session string) {
			t.Helper()

			u, err := rbac.GetUser("alice")
			if err != nil {
				t.Fatal(err)
			}

			if err := u.DeleteToken(session); err != nil {
				t.Fatal(err)
			}
		},
		"user deleted": func(t *testing.T, _ string) {
			t.Helper()

			if err := store.Delete(&store.Config{Kind: "User", Metadata: store.ConfigMetadata{Name: "alice"}}); err != nil {
				t.Fatal(err)
			}
		},
		"experiment deleted": func(t *testing.T, _ string) {
			t.Helper()

			if err := store.Delete(&store.Config{
				Kind: "Experiment", Metadata: store.ConfigMetadata{Name: "test-experiment"},
			}); err != nil {
				t.Fatal(err)
			}
		},
	}

	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			useWebShark(t)
			useCapRecheck(t)
			useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

			session := useTestUser(t, "alice", sharkRole())
			server := serveRouter(t, testPublic(), testJWTKey).URL

			capture := openSignedIn(t, server, session)
			if capture.ID == "" || capture.Expires == "" {
				t.Fatalf("got %+v, want an ID and expiry", capture)
			}

			url := server + "/webshark/captures/" + capture.Capture

			if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusOK {
				t.Fatalf("before the change: got %d", resp.StatusCode)
			}

			change(t, session)

			for range 2 {
				if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
					t.Fatalf("after the change: got %d, want 404", resp.StatusCode)
				}
			}

			if n := len(captureGrants.List("alice", "test-experiment")); n != 0 {
				t.Fatalf("%d grants left, want the name revoked", n)
			}
		})
	}
}

// A grant ends no later than the session it was given under.
func TestCaptureGrantEndsWithSession(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	session := useTestUser(t, "alice", sharkRole())
	server := serveRouter(t, testPublic(), testJWTKey).URL

	capture := openSignedIn(t, server, session)

	expires, err := time.Parse(time.RFC3339, capture.Expires)
	if err != nil {
		t.Fatal(err)
	}

	if ends := sessionExpiry(session); expires.After(ends) || ends.Sub(expires) > time.Second {
		t.Fatalf("expires %v, want the session's end %v", expires, ends)
	}
}

// Behind a proxy that names its user, a request naming another user may not
// use the grant; one naming no user may.
func TestCaptureGrantProxyUser(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	o.proxyAuthHeader = "X-Remote-User" // useWebShark restores o

	server := serveAs(t, "alice", sharkRole()).URL
	capture := openInWebShark(t, server, `{"path":"a.pcap"}`)
	url := server + "/webshark/captures/" + capture.Capture

	for _, tc := range []struct {
		user string
		want int
	}{
		{"bob", http.StatusForbidden},
		{"alice", http.StatusOK},
		{"", http.StatusOK},
	} {
		if resp := do(t, http.MethodGet, url, "", "X-Remote-User", tc.user); resp.StatusCode != tc.want {
			t.Errorf("proxy user %q: got %d, want %d", tc.user, resp.StatusCode, tc.want)
		}
	}
}

// With sign-in disabled every user is global-admin, and grants work the same.
func TestCaptureGrantWithoutSignIn(t *testing.T) {
	useWebShark(t)
	useCapRecheck(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})
	useTestStore(t, store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Role",
		Metadata: store.ConfigMetadata{Name: "global-admin"},
		Spec:     map[string]any{"roleName": "Global Admin", "policies": rolePolicies(sharkRole())},
	})

	server := serveRouter(t, testPublic(), "").URL
	capture := openInWebShark(t, server, `{"path":"a.pcap"}`)

	if resp := do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}

	if list := captureGrants.List("global-admin", "test-experiment"); len(list) != 1 || list[0].Session != "" {
		t.Fatalf("global-admin's grants: %+v", list)
	}
}

// A WebShark grant does not stream, and its operations are counted.
func TestCaptureGrantOperations(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	server := serveAs(t, "alice", sharkRole()).URL
	capture := openInWebShark(t, server, `{"path":"a.pcap"}`)

	_ = do(t, http.MethodGet, server+"/webshark/json?method=download&capture="+capture.Capture, "")

	list := captureGrants.List("alice", "test-experiment")
	if len(list) != 1 || list[0].ID != capture.ID || list[0].Uses != 1 || list[0].IdleTimeout != viewGrantIdle {
		t.Fatalf("alice's grants: %+v", list)
	}
}
