package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"

	"phenix/store"
	"phenix/web/capability"
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

// useTestUser stores the user alice with the role, signed in with a new
// session, and returns the session's JWT, signed with testJWTKey.
func useTestUser(t *testing.T, role rbac.Role) string {
	t.Helper()

	const name = "alice"

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

			session := useTestUser(t, sharkRole())
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

// With dev auth the grant is the dev user's, and each use checks that the
// server still signs that user in.
func TestCaptureGrantDevAuth(t *testing.T) {
	useWebShark(t)
	useCapRecheck(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})
	useTestStore(t, store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Role",
		Metadata: store.ConfigMetadata{Name: "global-viewer"},
		Spec:     map[string]any{"roleName": "Global Viewer", "policies": rolePolicies(sharkRole())},
	})

	server := serveRouter(t, testPublic(), "dev|alice|global-viewer").URL
	capture := openInWebShark(t, server, `{"path":"a.pcap"}`)

	for range 2 {
		if resp := do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture, ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("got %d", resp.StatusCode)
		}
	}

	list := captureGrants.List("alice", "test-experiment")
	if len(list) != 1 || list[0].User != "alice" || list[0].ID != capture.ID || list[0].Session != "" {
		t.Fatalf("alice's grants: %+v", list)
	}

	// a server signing in another dev user does not honor alice's grant
	bob := serveRouter(t, testPublic(), "dev|bob|global-viewer").URL
	if resp := do(t, http.MethodGet, bob+"/webshark/captures/"+capture.Capture, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("as bob's server: got %d, want 404", resp.StatusCode)
	}

	if n := len(captureGrants.List("alice", "test-experiment")); n != 0 {
		t.Fatalf("%d grants left, want the name revoked", n)
	}
}

// A grant ends no later than the session it was given under.
func TestCaptureGrantEndsWithSession(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	session := useTestUser(t, sharkRole())
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
	events := captureAudit(t)

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

	// the grant's user, refused because another user used it
	wantAudit(t, events(), "capture-webshark-read", "failure", map[string]any{
		"user.name": "alice", "phenix.proxy_user": "bob", "event.reason": "another proxy user",
		"phenix.capability.id": capture.ID,
	})
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

// createCaptureToken asks the server for a capture token of test-experiment
// and returns the response and, if it is a 201, the token.
func createCaptureToken(t *testing.T, server, body string) (*http.Response, captureToken) {
	t.Helper()

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/capture-tokens", body)

	var token captureToken

	if resp.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
			t.Fatalf("decoding token: %v", err)
		}
	}

	return resp, token
}

// streamToken has the server give a stream token for test-vm's capture.
func streamToken(t *testing.T, server string) captureToken {
	t.Helper()

	resp, token := createCaptureToken(t, server, `{"vm":"test-vm","interface":0,"operations":["stream"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	return token
}

func TestCreateCaptureTokenRefuses(t *testing.T) {
	fake := useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"saved.pcap": string(testPcapHeader())})
	useLiveCapture(t, fake)

	tests := map[string]struct {
		role rbac.Role
		body string
		want int
	}{
		"no file permission": {
			testRole("experiments/files", "list", "test-experiment"),
			`{"path":"saved.pcap","operations":["view"]}`, http.StatusForbidden,
		},
		"no capture permission": {
			testRole("experiments/files", "get", "test-experiment"),
			`{"vm":"test-vm","interface":0,"operations":["stream"]}`, http.StatusForbidden,
		},
		"stream of a saved file": {
			sharkRole(), `{"path":"saved.pcap","operations":["stream"]}`, http.StatusBadRequest,
		},
		"no operations": {sharkRole(), `{"vm":"test-vm","interface":0}`, http.StatusBadRequest},
		"empty operations": {
			sharkRole(), `{"vm":"test-vm","interface":0,"operations":[]}`, http.StatusBadRequest,
		},
		"unknown operation": {
			sharkRole(), `{"vm":"test-vm","interface":0,"operations":["delete"]}`, http.StatusBadRequest,
		},
		"stream and view": {
			sharkRole(), `{"vm":"test-vm","interface":0,"operations":["stream","view"]}`, http.StatusBadRequest,
		},
		"not being captured": {
			sharkRole(), `{"vm":"test-vm","interface":1,"operations":["stream"]}`, http.StatusNotFound,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp, _ := createCaptureToken(t, serveAs(t, "alice", tt.role).URL, tt.body)
			if resp.StatusCode != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, resp.StatusCode, readBody(t, resp))
			}
		})
	}

	// streaming needs no WebShark; viewing does
	o.features = map[string]bool{}
	server := serveAs(t, "alice", sharkRole()).URL

	resp, _ := createCaptureToken(t, server, `{"path":"saved.pcap","operations":["view"]}`)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("view without WebShark: expected 501, got %d", resp.StatusCode)
	}

	streamToken(t, server)
}

func TestCaptureTokenStreams(t *testing.T) {
	live := useLiveCapture(t, useWebShark(t))
	header, old, fresh := testPcapHeader(), testPcapRecord(60), testPcapRecord(1500)
	live.write(t, header, old)
	followLiveCapture(t)

	server := serveAs(t, "alice", sharkRole()).URL
	events := captureAudit(t)
	token := streamToken(t, server)

	if !strings.HasPrefix(token.Token, "pcs_") || token.ID == "" || token.UseBy == "" || token.Expires == "" ||
		token.URLs["stream"] != "webshark/streams/"+token.Token || token.Target.VM != "test-vm" ||
		token.Target.Interface == nil || *token.Target.Interface != 0 || token.Experiment != "test-experiment" {
		t.Fatalf("unexpected token %+v", token)
	}

	// listed without the token
	resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/capture-tokens", "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || strings.Contains(body, token.Token) ||
		!strings.Contains(body, token.ID) {
		t.Fatalf("listing: %d %s", resp.StatusCode, body)
	}

	url := server + "/" + token.URLs["stream"]

	expect := openCaptureStream(t, url, false)
	expect(header, "the file header")
	expect(old, "the packet already captured")

	live.write(t, fresh)
	expect(fresh, "the new packet")

	// it reconnects within its window, from now if asked
	expect = openCaptureStream(t, url+"?from=now", false)
	expect(header, "the file header again")

	// another user cannot revoke it
	bob := serveAs(t, "bob", sharkRole()).URL
	resp = do(t, http.MethodDelete, bob+"/api/v1/experiments/test-experiment/capture-tokens/"+token.ID, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob revoking: got %d, want 404", resp.StatusCode)
	}

	// a WebShark route does not take it
	if resp := do(t, http.MethodGet, server+"/webshark/captures/"+token.Token, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("as a WebShark capture: got %d, want 403", resp.StatusCode)
	}

	got := events()
	grant := map[string]any{"user.name": "alice", "phenix.capability.id": token.ID, "phenix.vm.name": "test-vm"}

	wantAudit(t, got, "capture-token-create", "success", grant)

	// each stream, as the grant's user, though the request names none
	if n := countAudit(got, "capture-stream", "success", grant); n != 2 {
		t.Errorf("%d streams audited, want 2", n)
	}

	wantAudit(t, got, "capture-webshark-read", "failure", map[string]any{
		"user.name": "alice", "event.reason": "operation not granted", "phenix.capability.op": "view",
	})

	for _, e := range got {
		if strings.Contains(fmt.Sprint(e), token.Token) {
			t.Errorf("audit event carries the token: %v", e)
		}
	}
}

// Revoking a stream token ends the streams it opened.
func TestCaptureTokenRevokeEndsStream(t *testing.T) {
	live := useLiveCapture(t, useWebShark(t))
	header := testPcapHeader()
	live.write(t, header)
	followLiveCapture(t)

	server := serveAs(t, "alice", sharkRole()).URL
	token := streamToken(t, server)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server+"/"+token.URLs["stream"], nil)

	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer stream.Body.Close()

	if _, err := io.ReadFull(stream.Body, make([]byte, len(header))); err != nil {
		t.Fatalf("reading the header: %v", err)
	}

	revoke := server + "/api/v1/experiments/test-experiment/capture-tokens/" + token.ID

	events := captureAudit(t)

	resp := do(t, http.MethodDelete, revoke, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoking: got %d", resp.StatusCode)
	}

	wantAudit(t, events(), "capture-token-revoke", "success", map[string]any{
		"user.name": "alice", "phenix.capability.id": token.ID, "phenix.experiment.name": "test-experiment",
	})

	if _, err := io.ReadAll(stream.Body); err != nil || ctx.Err() != nil {
		t.Fatalf("the stream did not end: %v %v", err, ctx.Err())
	}

	if resp := do(t, http.MethodGet, server+"/"+token.URLs["stream"], ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after revoking: got %d, want 404", resp.StatusCode)
	}

	if resp := do(t, http.MethodDelete, revoke, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoking again: got %d, want 404", resp.StatusCode)
	}
}

// An open stream checks its grant as it runs, and ends once the user may no
// longer read the capture: here, when the session is deleted.
func TestCaptureTokenRecheckEndsStream(t *testing.T) {
	live := useLiveCapture(t, useWebShark(t))
	header := testPcapHeader()
	live.write(t, header)
	followLiveCapture(t)
	useCapRecheck(t)

	session := useTestUser(t, sharkRole())
	server := serveRouter(t, testPublic(), testJWTKey).URL

	resp := signedIn(
		t, http.MethodPost, server+"/api/v1/experiments/test-experiment/capture-tokens",
		`{"vm":"test-vm","interface":0,"operations":["stream"]}`, session,
	)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("minting: got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var token captureToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		t.Fatalf("decoding token: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server+"/"+token.URLs["stream"], nil)

	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer stream.Body.Close()

	if _, err := io.ReadFull(stream.Body, make([]byte, len(header))); err != nil {
		t.Fatalf("reading the header: %v", err)
	}

	u, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}

	if err := u.DeleteToken(session); err != nil {
		t.Fatal(err)
	}

	if _, err := io.ReadAll(stream.Body); err != nil || ctx.Err() != nil {
		t.Fatalf("the stream did not end: %v %v", err, ctx.Err())
	}

	if n := len(captureGrants.List("alice", "test-experiment")); n != 0 {
		t.Fatalf("%d grants left, want the token revoked", n)
	}
}

// A WebShark capture name does not stream.
func TestWebSharkNameDoesNotStream(t *testing.T) {
	live := useLiveCapture(t, useWebShark(t))
	live.write(t, testPcapHeader())

	server := serveAs(t, "alice", sharkRole()).URL
	capture := openInWebShark(t, server, `{"vm":"test-vm","interface":0}`)

	if resp := do(t, http.MethodGet, server+"/webshark/streams/"+capture.Capture, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got %d, want 403", resp.StatusCode)
	}

	if resp := do(t, http.MethodGet, server+"/webshark/streams/pcs_unknown", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown token: got %d, want 404", resp.StatusCode)
	}
}

// Stopping an experiment ends its stream grants; WebShark's go on.
// Deleting it ends them all.
func TestExperimentStopAndDeleteRevokeGrants(t *testing.T) {
	live := useLiveCapture(t, useWebShark(t))
	live.write(t, testPcapHeader())

	server := serveAs(t, "alice", sharkRole()).URL
	stream := server + "/" + streamToken(t, server).URLs["stream"]
	view := server + "/webshark/captures/" + openInWebShark(t, server, `{"vm":"test-vm","interface":0}`).Capture

	revokeOnExperimentStop("stop", "other-experiment")

	if n := len(captureGrants.List("alice", "test-experiment")); n != 2 {
		t.Fatalf("another experiment's stop left %d grants, want 2", n)
	}

	revokeOnExperimentStop("stop", "test-experiment")

	if resp := do(t, http.MethodGet, stream, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("stream after stop: got %d, want 404", resp.StatusCode)
	}

	if resp := do(t, http.MethodGet, view, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("WebShark after stop: got %d, want 200", resp.StatusCode)
	}

	revokeOnExperimentDelete("delete", "test-experiment")

	if resp := do(t, http.MethodGet, view, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("WebShark after delete: got %d, want 404", resp.StatusCode)
	}
}

// Signing out ends the grants given under the session, and only those.
func TestLogoutRevokesGrants(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	session := useTestUser(t, sharkRole())
	server := serveRouter(t, testPublic(), testJWTKey).URL
	url := server + "/webshark/captures/" + openSignedIn(t, server, session).Capture

	// another session's grant
	other := captureGrants.Mint("other.pcap", capability.Grant{
		User: "alice", Session: "other-session", Target: capability.Target{Exp: "test-experiment"},
		Ops: viewOps, Expires: time.Now().Add(time.Hour),
	})

	if resp := signedIn(t, http.MethodGet, server+"/api/v1/logout", "", session); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("signing out: got %d", resp.StatusCode)
	}

	if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after signing out: got %d, want 404", resp.StatusCode)
	}

	if list := captureGrants.List("alice", "test-experiment"); len(list) != 1 || list[0].ID != other.ID {
		t.Fatalf("alice's grants after signing out: %+v", list)
	}
}

func TestDeleteUserRevokesGrants(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})
	useTestUser(t, sharkRole())

	server := serveAs(t, "alice", sharkRole()).URL
	url := server + "/webshark/captures/" + openInWebShark(t, server, `{"path":"a.pcap"}`).Capture

	admin := serveAs(t, "admin", testRole("users", "delete", "alice")).URL
	if resp := do(t, http.MethodDelete, admin+"/api/v1/users/alice", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("deleting alice: got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after deleting the user: got %d, want 404", resp.StatusCode)
	}
}

// Deleting a capture file ends its grants: a copy fetched from a mesh node
// could otherwise still be read.
func TestDeleteExperimentFileRevokesGrants(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{
		"a.pcap": string(testPcapHeader()), "b.pcap": string(testPcapHeader()), "c.pcap": string(testPcapHeader()),
	})
	useTestFiles(t, "a.pcap", "b.pcap", "c.pcap")

	server := serveAs(t, "alice", combinedRole(sharkRole(), testRole("experiments/files", "delete", "test-experiment"))).URL

	urls := make(map[string]string)
	for _, name := range []string{"a.pcap", "b.pcap", "c.pcap"} {
		urls[name] = server + "/webshark/captures/" + openInWebShark(t, server, `{"path":"`+name+`"}`).Capture
	}

	files := server + "/api/v1/experiments/test-experiment/files"

	if resp := do(t, http.MethodDelete, files+"/a.pcap?path=a.pcap", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("deleting a.pcap: got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	if resp := do(t, http.MethodPost, files+"/delete", `{"paths":["b.pcap"]}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("deleting b.pcap: got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	for name, want := range map[string]int{"a.pcap": http.StatusNotFound, "b.pcap": http.StatusNotFound, "c.pcap": http.StatusOK} {
		if resp := do(t, http.MethodGet, urls[name], ""); resp.StatusCode != want {
			t.Errorf("%s: got %d, want %d", name, resp.StatusCode, want)
		}
	}
}
