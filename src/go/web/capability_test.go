package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/util/mm"
	"phenix/util/polltest"
	"phenix/web/audit"
	"phenix/web/capability"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// testJWTKey is the JWT signing key of servers that sign users in.
const testJWTKey = "test-signing-key"

// grantCmp compares capture grants leaving out their times and where the headnode has the file.
var grantCmp = cmp.Options{ //nolint:gochecknoglobals // a constant set of options
	cmpopts.IgnoreFields(capability.Grant{}, "Created", "Expires", "UseBy", "LastUsed", "Checked", "Done"),
	cmpopts.IgnoreFields(capability.Target{}, "Local"),
}

// savedGrant is the grant of WebShark to read test-experiment's saved a.pcap, used uses times.
func savedGrant(id, user string, uses int) capability.Grant {
	return capability.Grant{
		ID: id, User: user, Target: capability.Target{Exp: "test-experiment", File: "a.pcap"},
		Ops: viewOps, IdleTimeout: viewGrantIdle, Uses: uses,
	}
}

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
		t.Fatalf("POST /api/v1/experiments/test-experiment/webshark a.pcap = %d, want %d: %s",
			resp.StatusCode, http.StatusOK, readBody(t, resp))
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
				t.Fatalf("POST webshark a.pcap = %+v, want an ID and expiry", capture)
			}

			url := server + "/webshark/captures/" + capture.Capture

			if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusOK {
				t.Fatalf("GET the capture before the change = %d, want %d", resp.StatusCode, http.StatusOK)
			}

			change(t, session)

			for _, use := range []string{"first", "second"} {
				if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
					t.Errorf("GET the capture after the change, %s time = %d, want %d", use, resp.StatusCode, http.StatusNotFound)
				}
			}

			if list := captureGrants.List("alice", "test-experiment"); len(list) != 0 {
				t.Errorf("captureGrants.List(alice, test-experiment) = %+v, want the name revoked", list)
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

	for _, use := range []string{"first", "second"} {
		if resp := do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture, ""); resp.StatusCode != http.StatusOK {
			t.Errorf("GET the capture as alice, %s time = %d, want %d", use, resp.StatusCode, http.StatusOK)
		}
	}

	want := []capability.Grant{savedGrant(capture.ID, "alice", 2)}
	if diff := cmp.Diff(want, captureGrants.List("alice", "test-experiment"), grantCmp); diff != "" {
		t.Errorf("captureGrants.List(alice, test-experiment) mismatch (-want +got):\n%s", diff)
	}

	// a server signing in another dev user does not honor alice's grant
	bob := serveRouter(t, testPublic(), "dev|bob|global-viewer").URL
	if resp := do(t, http.MethodGet, bob+"/webshark/captures/"+capture.Capture, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET alice's capture from bob's server = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	if list := captureGrants.List("alice", "test-experiment"); len(list) != 0 {
		t.Errorf("captureGrants.List(alice, test-experiment) after bob's use = %+v, want the name revoked", list)
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
		t.Fatalf("POST webshark a.pcap expires = %q, not RFC 3339: %v", capture.Expires, err)
	}

	if ends := sessionExpiry(session); expires.After(ends) || ends.Sub(expires) > time.Second {
		t.Errorf("POST webshark a.pcap expires = %v, want the session's end %v", expires, ends)
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
			t.Errorf("GET the capture as proxy user %q = %d, want %d", tc.user, resp.StatusCode, tc.want)
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
	useGlobalAdmin(t)

	server := serveRouter(t, testPublic(), "").URL
	capture := openInWebShark(t, server, `{"path":"a.pcap"}`)

	if resp := do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("GET the capture without sign-in = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	want := []capability.Grant{savedGrant(capture.ID, "global-admin", 1)}
	if diff := cmp.Diff(want, captureGrants.List("global-admin", "test-experiment"), grantCmp); diff != "" {
		t.Errorf("captureGrants.List(global-admin, test-experiment) mismatch (-want +got):\n%s", diff)
	}
}

// A download through a WebShark grant succeeds and counts as one use of the grant.
func TestCaptureGrantOperations(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	server := serveAs(t, "alice", sharkRole()).URL
	capture := openInWebShark(t, server, `{"path":"a.pcap"}`)

	download := "/webshark/json?method=download&capture=" + capture.Capture
	if resp := do(t, http.MethodGet, server+download, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", download, resp.StatusCode, http.StatusOK)
	}

	want := []capability.Grant{savedGrant(capture.ID, "alice", 1)}
	if diff := cmp.Diff(want, captureGrants.List("alice", "test-experiment"), grantCmp); diff != "" {
		t.Errorf("captureGrants.List(alice, test-experiment) mismatch (-want +got):\n%s", diff)
	}
}

// createCaptureToken asks the server for a capture token of test-experiment
// and returns the response and, if it is a 201, the token.
func createCaptureToken(t *testing.T, server, body string) (*http.Response, captureToken) {
	t.Helper()

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/captureTokens", body)

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
		t.Fatalf("POST captureTokens for test-vm's stream = %d, want %d: %s",
			resp.StatusCode, http.StatusCreated, readBody(t, resp))
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
		"invalid body":  {sharkRole(), `not json`, http.StatusBadRequest},
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
				t.Errorf("POST captureTokens %s = %d, want %d: %s", tt.body, resp.StatusCode, tt.want, readBody(t, resp))
			}
		})
	}

	// streaming needs no WebShark; viewing does
	o.features = map[string]bool{}
	server := serveAs(t, "alice", sharkRole()).URL

	resp, _ := createCaptureToken(t, server, `{"path":"saved.pcap","operations":["view"]}`)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("POST captureTokens to view without WebShark = %d, want %d", resp.StatusCode, http.StatusNotImplemented)
	}

	streamToken(t, server)
}

// captureOps allows stream alone or any mix of WebShark's operations, and grants each
// asked-for operation once, in the order first asked.
func TestCaptureOpsProperties(t *testing.T) {
	t.Parallel()

	known := []capability.Op{capability.OpView, capability.OpDownload, capability.OpWatch}

	rapid.Check(t, func(t *rapid.T) {
		ops := rapid.SliceOfN(rapid.SampledFrom(append(slices.Clone(known), capability.OpStream, "delete")), 0, 5).Draw(t, "ops")

		allowed := slices.Equal(ops, []capability.Op{capability.OpStream}) ||
			(len(ops) > 0 && !slices.ContainsFunc(ops, func(op capability.Op) bool { return !slices.Contains(known, op) }))

		granted, err := captureOps(ops)
		if (err == nil) != allowed {
			t.Fatalf("captureOps(%q) error = %v, want allowed = %v", ops, err, allowed)
		}

		if err != nil {
			return
		}

		firstAsked := func(a, b capability.Op) int { return slices.Index(ops, a) - slices.Index(ops, b) }
		distinct := slices.Compact(slices.SortedFunc(slices.Values(ops), firstAsked))

		if !slices.Equal(granted, distinct) {
			t.Fatalf("captureOps(%q) = %q, want each operation once, in the order first asked: %q", ops, granted, distinct)
		}
	})
}

func TestCaptureTokenStreams(t *testing.T) {
	live := useLiveCapture(t, useWebShark(t))
	header, old, fresh := testPcapHeader(), testPcapRecord(60), testPcapRecord(1500)
	live.write(t, header, old)
	followLiveCapture(t)

	server := serveAs(t, "alice", sharkRole()).URL
	events := captureAudit(t)
	token := streamToken(t, server)

	if !strings.HasPrefix(token.Token, "pcs_") {
		t.Fatalf("POST captureTokens token = %q, want a pcs_ token", token.Token)
	}

	if token.ID == "" || token.UseBy == "" || token.Expires == "" {
		t.Errorf("POST captureTokens = %+v, want an ID, use-by and expiry", token)
	}

	iface := 0
	want := captureToken{
		Operations: []capability.Op{capability.OpStream},
		Experiment: "test-experiment",
		Target:     captureTokenTarget{Path: "live.pcap", VM: "test-vm", Interface: &iface},
		URLs:       map[string]string{"stream": "webshark/streams/" + token.Token},
	}

	ignore := cmpopts.IgnoreFields(captureToken{}, "ID", "Token", "Expires", "UseBy", "LastUsed")
	if diff := cmp.Diff(want, token, ignore); diff != "" {
		t.Errorf("POST captureTokens mismatch (-want +got):\n%s", diff)
	}

	// listed without the token
	resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/captureTokens", "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET captureTokens = %d, want %d: %s", resp.StatusCode, http.StatusOK, body)
	}

	if strings.Contains(body, token.Token) {
		t.Errorf("GET captureTokens = %s, want it without the token", body)
	}

	if !strings.Contains(body, token.ID) {
		t.Errorf("GET captureTokens = %s, want the token's ID %s", body, token.ID)
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
	resp = do(t, http.MethodDelete, bob+"/api/v1/experiments/test-experiment/captureTokens/"+token.ID, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE alice's capture token as bob = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	// a WebShark route does not take it
	if resp := do(t, http.MethodGet, server+"/webshark/captures/"+token.Token, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /webshark/captures/<stream token> = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	got := events()
	grant := map[string]any{"user.name": "alice", "phenix.capability.id": token.ID, "phenix.vm.name": "test-vm"}

	wantAudit(t, got, "capture-token-create", "success", grant)

	// each stream, as the grant's user, though the request names none
	if n := countAudit(got, "capture-stream", "success", grant); n != 2 {
		t.Errorf("capture-stream success audit events = %d, want 2", n)
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

// openStream opens the stream at url and reads its header of n bytes; the
// function it returns fails the test unless the stream then ends.
func openStream(t *testing.T, url string, n int) func() {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext(GET %s) = %v", url, err)
	}

	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}

	t.Cleanup(func() { stream.Body.Close() })

	if _, err := io.ReadFull(stream.Body, make([]byte, n)); err != nil {
		t.Fatalf("reading the header of GET %s: %v", url, err)
	}

	return func() {
		t.Helper()

		if _, err := io.ReadAll(stream.Body); err != nil || ctx.Err() != nil {
			t.Errorf("GET %s did not end: read error %v, context %v", url, err, ctx.Err())
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

	ended := openStream(t, server+"/"+token.URLs["stream"], len(header))

	revoke := server + "/api/v1/experiments/test-experiment/captureTokens/" + token.ID

	events := captureAudit(t)

	resp := do(t, http.MethodDelete, revoke, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE the capture token = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	wantAudit(t, events(), "capture-token-revoke", "success", map[string]any{
		"user.name": "alice", "phenix.capability.id": token.ID, "phenix.experiment.name": "test-experiment",
	})

	ended()

	if resp := do(t, http.MethodGet, server+"/"+token.URLs["stream"], ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET the stream after revoking = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	if resp := do(t, http.MethodDelete, revoke, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE the capture token again = %d, want %d", resp.StatusCode, http.StatusNotFound)
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
		t, http.MethodPost, server+"/api/v1/experiments/test-experiment/captureTokens",
		`{"vm":"test-vm","interface":0,"operations":["stream"]}`, session,
	)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST captureTokens signed in = %d, want %d: %s", resp.StatusCode, http.StatusCreated, readBody(t, resp))
	}

	var token captureToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		t.Fatalf("decoding token: %v", err)
	}

	ended := openStream(t, server+"/"+token.URLs["stream"], len(header))

	u, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}

	if err := u.DeleteToken(session); err != nil {
		t.Fatal(err)
	}

	ended()

	if list := captureGrants.List("alice", "test-experiment"); len(list) != 0 {
		t.Errorf("captureGrants.List(alice, test-experiment) = %+v, want the token revoked", list)
	}
}

// A WebShark capture name does not stream.
func TestWebSharkNameDoesNotStream(t *testing.T) {
	live := useLiveCapture(t, useWebShark(t))
	live.write(t, testPcapHeader())

	server := serveAs(t, "alice", sharkRole()).URL
	capture := openInWebShark(t, server, `{"vm":"test-vm","interface":0}`)

	if resp := do(t, http.MethodGet, server+"/webshark/streams/"+capture.Capture, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /webshark/streams/<WebShark name> = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	if resp := do(t, http.MethodGet, server+"/webshark/streams/pcs_unknown", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /webshark/streams/pcs_unknown = %d, want %d", resp.StatusCode, http.StatusNotFound)
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
		t.Errorf("grants of test-experiment after other-experiment stops = %d, want 2", n)
	}

	revokeOnExperimentStop("stop", "test-experiment")

	if resp := do(t, http.MethodGet, stream, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET the stream after the stop = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	if resp := do(t, http.MethodGet, view, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("GET the WebShark capture after the stop = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	revokeOnExperimentDelete("delete", "test-experiment")

	if resp := do(t, http.MethodGet, view, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET the WebShark capture after the delete = %d, want %d", resp.StatusCode, http.StatusNotFound)
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

	if resp := signedIn(t, http.MethodPost, server+"/api/v1/logout", "", session); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /api/v1/logout = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET the capture after signing out = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	if diff := cmp.Diff([]capability.Grant{other}, captureGrants.List("alice", "test-experiment"), grantCmp); diff != "" {
		t.Errorf("captureGrants.List(alice, test-experiment) after signing out mismatch (-want +got):\n%s", diff)
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
		t.Fatalf("DELETE /api/v1/users/alice = %d, want %d: %s", resp.StatusCode, http.StatusNoContent, readBody(t, resp))
	}

	if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET the capture after deleting alice = %d, want %d", resp.StatusCode, http.StatusNotFound)
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
		t.Errorf("DELETE files/a.pcap = %d, want %d: %s", resp.StatusCode, http.StatusNoContent, readBody(t, resp))
	}

	if resp := do(t, http.MethodPost, files+"/delete", `{"paths":["b.pcap"]}`); resp.StatusCode != http.StatusOK {
		t.Errorf("POST files/delete b.pcap = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	for name, want := range map[string]int{"a.pcap": http.StatusNotFound, "b.pcap": http.StatusNotFound, "c.pcap": http.StatusOK} {
		if resp := do(t, http.MethodGet, urls[name], ""); resp.StatusCode != want {
			t.Errorf("GET the capture of %s = %d, want %d", name, resp.StatusCode, want)
		}
	}
}

// capabilityFailingStore is the test store, counting the gets of experiments
// and failing them while failing is set.
type capabilityFailingStore struct {
	*testStore

	failing atomic.Bool
	gets    atomic.Int32
}

func (s *capabilityFailingStore) Get(c *store.Config) error {
	if strings.EqualFold(c.Kind, "Experiment") {
		s.gets.Add(1)

		if s.failing.Load() {
			return errors.New("store unreadable")
		}
	}

	return s.testStore.Get(c)
}

// capabilityUseFailingStore makes getting an experiment fail for the rest of
// the test, or until failing is unset. Call it after storing the test's configs.
func capabilityUseFailingStore(t *testing.T) *capabilityFailingStore {
	t.Helper()

	useTestStore(t)

	s, _ := store.DefaultStore.(*testStore)
	failing := &capabilityFailingStore{testStore: s}
	failing.failing.Store(true)

	store.DefaultStore = failing //nolint:reassign // useTestStore restores it

	return failing
}

// capabilityMint gives alice a grant of ops on target under token, ending in
// an hour.
func capabilityMint(token string, target capability.Target, ops ...capability.Op) capability.Grant {
	return captureGrants.Mint(token, capability.Grant{
		User: "alice", Target: target, Ops: ops, Expires: time.Now().Add(time.Hour),
	})
}

// capabilitySaved is test-experiment's saved capture a.pcap.
func capabilitySaved() capability.Target {
	return capability.Target{Exp: "test-experiment", File: "a.pcap"}
}

func TestSessionExpiry(t *testing.T) {
	t.Parallel()

	sign := func(claims jwt.MapClaims) string {
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testJWTKey))
		if err != nil {
			t.Fatal(err)
		}

		return signed
	}

	ends := time.Unix(1_900_000_000, 0)

	tests := map[string]struct {
		raw  string
		want time.Time
	}{
		"no session":          {raw: "", want: time.Time{}},
		"not a JWT":           {raw: "not.a.jwt", want: time.Time{}},
		"no expiry":           {raw: sign(jwt.MapClaims{jwtClaimSub: "alice"}), want: time.Time{}},
		"expiry not a number": {raw: sign(jwt.MapClaims{jwtClaimExp: "soon"}), want: time.Time{}},
		"expiry":              {raw: sign(jwt.MapClaims{jwtClaimExp: ends.Unix()}), want: ends},
		"already expired":     {raw: sign(jwt.MapClaims{jwtClaimExp: 1}), want: time.Unix(1, 0)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := sessionExpiry(tc.raw); !got.Equal(tc.want) {
				t.Errorf("sessionExpiry(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestMinTime(t *testing.T) {
	t.Parallel()

	early, late := time.Unix(100, 0), time.Unix(200, 0)

	tests := map[string]struct{ a, b, want time.Time }{
		"first earlier":  {a: early, b: late, want: early},
		"second earlier": {a: late, b: early, want: early},
		"equal":          {a: late, b: late, want: late},
		"zero first":     {a: time.Time{}, b: early, want: time.Time{}},
		"zero second":    {a: early, b: time.Time{}, want: time.Time{}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := minTime(tc.a, tc.b); !got.Equal(tc.want) {
				t.Errorf("minTime(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// The audit fields of a grant name its target, and, for a use, its user and
// the route's template rather than the path, which may hold the token.
func TestCaptureGrantAuditFields(t *testing.T) {
	t.Parallel()

	var (
		iface0, iface2 = 0, 2
		expires        = time.Date(2030, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC+1", 3600))
		live           = capability.Target{Exp: "exp", File: "live.pcap", VM: "vm", Interface: 2, Live: true}
	)

	t.Run("targets", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			target capability.Target
			want   audit.Fields
		}{
			"saved file": {capabilitySaved(), audit.Fields{Experiment: "test-experiment", File: "a.pcap"}},
			"live":       {live, audit.Fields{Experiment: "exp", VM: "vm", File: "live.pcap", Interface: &iface2}},
			"interface 0": {
				capability.Target{Exp: "exp", VM: "vm"}, audit.Fields{Experiment: "exp", VM: "vm", Interface: &iface0},
			},
			"empty": {capability.Target{}, audit.Fields{}},
		} {
			if diff := cmp.Diff(tc.want, targetAuditFields(tc.target)); diff != "" {
				t.Errorf("targetAuditFields(%s) mismatch (-want +got):\n%s", name, diff)
			}
		}
	})

	t.Run("minted", func(t *testing.T) {
		t.Parallel()

		g := capability.Grant{
			ID: "id", User: "alice", Target: live, Ops: []capability.Op{capability.OpView, capability.OpDownload},
			Expires: expires,
		}

		want := audit.Fields{
			Experiment: "exp", VM: "vm", File: "live.pcap", Interface: &iface2,
			Extra: []any{
				"phenix.capability.id", "id", "phenix.capability.ops", []string{"view", "download"},
				"phenix.capability.expires", "2030-01-02T02:04:05Z", "phenix.webshark.live", true,
			},
		}
		if diff := cmp.Diff(want, mintedAuditFields(g)); diff != "" {
			t.Errorf("mintedAuditFields() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("actions", func(t *testing.T) {
		t.Parallel()

		for op, want := range map[capability.Op]audit.Action{
			capability.OpStream:   audit.CaptureStream,
			capability.OpView:     audit.CaptureWebSharkRead,
			capability.OpDownload: audit.CaptureWebSharkRead,
			capability.OpWatch:    audit.CaptureWebSharkRead,
		} {
			if got := grantAuditAction(op); got != want {
				t.Errorf("grantAuditAction(%s) = %s, want %s", op, got, want)
			}
		}
	})

	t.Run("use", func(t *testing.T) {
		t.Parallel()

		g := capability.Grant{ID: "id", User: "alice", Target: capabilitySaved()}
		want := audit.Fields{
			User: "alice", URLPath: "/webshark/captures/{capture}", Experiment: "test-experiment", File: "a.pcap",
			Extra: []any{"phenix.capability.id", "id", "phenix.capability.op", "view"},
		}

		var got audit.Fields

		router := mux.NewRouter()
		router.HandleFunc("/webshark/captures/{capture}", func(_ http.ResponseWriter, r *http.Request) {
			got = grantAuditFields(r, g, capability.OpView)
		})
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/webshark/captures/secret", nil))

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("grantAuditFields() on a route mismatch (-want +got):\n%s", diff)
		}

		// without a route, the path is left out
		want.URLPath = "-"

		got = grantAuditFields(httptest.NewRequest(http.MethodGet, "/webshark/captures/secret", nil), g, capability.OpView)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("grantAuditFields() without a route mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestNewCaptureToken(t *testing.T) {
	t.Parallel()

	var (
		iface0   = 0
		expires  = time.Date(2030, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC+1", 3600))
		lastUsed = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	)

	tests := map[string]struct {
		grant capability.Grant
		want  captureToken
	}{
		"saved file": {
			grant: capability.Grant{
				ID: "a", Target: capabilitySaved(), Ops: viewOps, Expires: expires, LastUsed: lastUsed, Uses: 3,
				Session: "secret-session",
			},
			want: captureToken{
				ID: "a", Operations: viewOps, Expires: "2030-01-02T02:04:05Z", LastUsed: "2030-01-01T00:00:00Z",
				Uses: 3, Experiment: "test-experiment", Target: captureTokenTarget{Path: "a.pcap"},
			},
		},
		"live capture": {
			grant: capability.Grant{
				ID: "b", Target: capability.Target{Exp: "test-experiment", File: "live.pcap", VM: "test-vm", Live: true},
				Ops: []capability.Op{capability.OpStream}, Expires: expires, UseBy: lastUsed, LastUsed: lastUsed,
			},
			want: captureToken{
				ID: "b", Operations: []capability.Op{capability.OpStream}, Expires: "2030-01-02T02:04:05Z",
				UseBy: "2030-01-01T00:00:00Z", LastUsed: "2030-01-01T00:00:00Z", Experiment: "test-experiment",
				Target: captureTokenTarget{Path: "live.pcap", VM: "test-vm", Interface: &iface0},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, newCaptureToken(tc.grant)); diff != "" {
				t.Errorf("newCaptureToken() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A WebShark grant ends idle, after its lifetime, or with its session; a
// stream grant starts streams within its window and ends with its session.
func TestMintCaptureGrant(t *testing.T) {
	const slack = 2 * time.Second

	live := capability.Target{Exp: "test-experiment", VM: "test-vm", Live: true}
	stream := []capability.Op{capability.OpStream}

	tests := map[string]struct {
		target  capability.Target
		ops     []capability.Op
		session time.Duration // until the session ends; 0 for none
		expires time.Duration
		useBy   time.Duration // 0 for none
		idle    time.Duration
	}{
		"view":                  {capabilitySaved(), viewOps, 0, viewGrantLifetime, 0, viewGrantIdle},
		"view, short session":   {capabilitySaved(), viewOps, time.Hour, time.Hour, 0, viewGrantIdle},
		"view, long session":    {capabilitySaved(), viewOps, 48 * time.Hour, viewGrantLifetime, 0, viewGrantIdle},
		"stream":                {live, stream, 0, streamGrantLifetime, streamGrantWindow, 0},
		"stream, short session": {live, stream, time.Minute, time.Minute, time.Minute, 0},
		"stream, long session":  {live, stream, 48 * time.Hour, 48 * time.Hour, streamGrantWindow, 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureGrants(t)

			start := time.Now()
			ctx := context.WithValue(t.Context(), middleware.ContextKeyUser, "alice")

			var session string
			if tc.session != 0 {
				session = signedJWT(t, "alice", testJWTKey, start.Add(tc.session))
				ctx = context.WithValue(ctx, middleware.ContextKeyJWT, session)
			}

			g, token := mintCaptureGrant(httptest.NewRequestWithContext(ctx, http.MethodPost, "/", nil), tc.target, tc.ops)

			want := capability.Grant{
				ID: capability.IDOf(token), User: "alice", Session: session, Target: tc.target, Ops: tc.ops, IdleTimeout: tc.idle,
			}
			if diff := cmp.Diff(want, g, grantCmp); diff != "" {
				t.Errorf("mintCaptureGrant() mismatch (-want +got):\n%s", diff)
			}

			if d := g.Expires.Sub(start.Add(tc.expires)); d > slack || d < -slack {
				t.Errorf("mintCaptureGrant() expires %v from now, want %v", g.Expires.Sub(start), tc.expires)
			}

			if tc.useBy == 0 && !g.UseBy.IsZero() {
				t.Errorf("mintCaptureGrant() use-by = %v, want none", g.UseBy)
			}

			if d := g.UseBy.Sub(start.Add(tc.useBy)); tc.useBy != 0 && (d > slack || d < -slack) {
				t.Errorf("mintCaptureGrant() use-by %v from now, want %v", g.UseBy.Sub(start), tc.useBy)
			}

			if isStream := strings.HasPrefix(token, streamTokenPrefix); isStream != (tc.useBy != 0) {
				t.Errorf("mintCaptureGrant() token = %q, want a %s prefix only for a stream", token, streamTokenPrefix)
			}

			if diff := cmp.Diff([]capability.Grant{g}, captureGrants.List("alice", "test-experiment"), grantCmp); diff != "" {
				t.Errorf("captureGrants.List(alice, test-experiment) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// recheck tells why a grant's user may no longer read its capture, and fails
// when it cannot tell.
func TestGrantCheckerRecheck(t *testing.T) {
	vm := capability.Target{Exp: "test-experiment", VM: "test-vm"}

	tests := map[string]struct {
		role       *rbac.Role // a dev user alice's role; nil for sign-in disabled
		user       string
		target     capability.Target
		storeFails bool
		want       string
		wantErr    bool
	}{
		"allowed":         {user: "global-admin", target: capabilitySaved()},
		"allowed capture": {role: ptr(sharkRole()), user: "alice", target: vm},
		"another dev user": {
			role: ptr(sharkRole()), user: "bob", target: vm, want: "session ended: user bob is not the dev auth user",
		},
		"file not allowed": {
			role: ptr(testRole("experiments/files", "list", "test-experiment")), user: "alice", target: capabilitySaved(),
			want: "file no longer allowed",
		},
		"capture not allowed": {
			role: ptr(testRole("experiments/files", "get", "test-experiment")), user: "alice", target: vm,
			want: "capture no longer allowed",
		},
		"experiment deleted": {
			user: "global-admin", target: capability.Target{Exp: "gone-experiment"}, want: "experiment deleted",
		},
		"invalid experiment": {user: "global-admin", target: capability.Target{Exp: "a/b"}, wantErr: true},
		"store unreadable":   {user: "global-admin", target: capabilitySaved(), storeFails: true, wantErr: true},
		"store unreadable, VM": {
			role: ptr(sharkRole()), user: "alice", target: vm, storeFails: true, wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)
			useGlobalAdmin(t)

			var c grantChecker
			if tc.role != nil {
				c.jwtKey = devKey(t, "alice", *tc.role)
			}

			if tc.storeFails {
				capabilityUseFailingStore(t)
			}

			reason, err := c.recheck(capability.Grant{User: tc.user, Target: tc.target})
			if reason != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("recheck(%s) = %q, %v; want %q, error %t", name, reason, err, tc.want, tc.wantErr)
			}
		})
	}
}

// Each use of a grant checks its operation and proxy user, and, unless it was
// checked a moment ago, its user's access, revoking it once lost.
func TestGrantCheckerUse(t *testing.T) {
	tests := map[string]struct {
		target     capability.Target
		op         capability.Op
		proxyUser  string // the X-Remote-User header, with a proxy that names users
		recheck    bool
		storeFails bool
		want       int
		wantKept   bool
		reason     string // of the refusal audited
	}{
		"checked a moment ago": {target: capabilitySaved(), op: capability.OpView, storeFails: true, want: 0, wantKept: true},
		"access kept":          {target: capabilitySaved(), op: capability.OpView, recheck: true, want: 0, wantKept: true},
		"operation not granted": {
			target: capabilitySaved(), op: capability.OpStream, want: http.StatusForbidden, wantKept: true,
			reason: "operation not granted",
		},
		"another proxy user": {
			target: capabilitySaved(), op: capability.OpView, proxyUser: "bob", want: http.StatusForbidden, wantKept: true,
			reason: "another proxy user",
		},
		"the grant's proxy user": {target: capabilitySaved(), op: capability.OpView, proxyUser: "alice", want: 0, wantKept: true},
		"store unreadable": {
			target: capabilitySaved(), op: capability.OpView, recheck: true, storeFails: true,
			want: http.StatusServiceUnavailable, wantKept: true,
		},
		"access lost": {
			target: capability.Target{Exp: "gone-experiment"}, op: capability.OpView, recheck: true, want: http.StatusNotFound,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureGrants(t)
			useTestExperiment(t)
			useGlobalAdmin(t)

			if tc.recheck {
				useCapRecheck(t)
			}

			var failing *capabilityFailingStore
			if tc.storeFails {
				failing = capabilityUseFailingStore(t)
			}

			c := grantChecker{jwtKey: "", proxyAuthHeader: ""}
			if tc.proxyUser != "" {
				c.proxyAuthHeader = "X-Remote-User"
			}

			g := capabilityMint("token", tc.target, viewOps...)
			before := time.Now()
			events := captureAudit(t)

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/webshark/captures/token", nil)
			r.Header.Set("X-Remote-User", tc.proxyUser)

			used, status := c.use(r, "token", tc.op)
			if status != tc.want || used.ID != g.ID {
				t.Errorf("use(%s) = grant %q, %d; want grant %q, %d", tc.op, used.ID, status, g.ID, tc.want)
			}

			list := captureGrants.List("alice", tc.target.Exp)
			if kept := len(list) == 1; kept != tc.wantKept {
				t.Errorf("use(%s) left the grant kept = %t, want %t", tc.op, kept, tc.wantKept)
			}

			if tc.recheck && tc.want == 0 && len(list) == 1 && list[0].Checked.Before(before) {
				t.Errorf("use(%s) left the grant checked at %v, want it checked again", tc.op, list[0].Checked)
			}

			if failing != nil && !tc.recheck && failing.gets.Load() != 0 {
				t.Errorf("use(%s) of a grant checked a moment ago got the experiment %d times, want 0", tc.op, failing.gets.Load())
			}

			if tc.reason != "" {
				wantAudit(t, events(), string(grantAuditAction(tc.op)), "failure", map[string]any{
					"user.name": "alice", "event.reason": tc.reason, "phenix.capability.id": g.ID,
				})
			} else if got := events(); len(got) != 0 {
				t.Errorf("use(%s) audit events = %v, want none", tc.op, got)
			}
		})
	}

	t.Run("unknown token", func(t *testing.T) {
		useCaptureGrants(t)

		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/webshark/streams/pcs_unknown", nil)
		if _, status := (grantChecker{}).use(r, "pcs_unknown", capability.OpStream); status != http.StatusNotFound {
			t.Errorf("use(pcs_unknown) = %d, want %d", status, http.StatusNotFound)
		}
	})
}

// An open stream or watch ends when its grant is revoked or its user's access
// is lost, and keeps going, checked again and again, while the store fails.
func TestUntilRevoked(t *testing.T) {
	// each case ends the goroutine untilRevoked starts before the fakes it reads are put back
	t.Run("grant revoked", func(t *testing.T) {
		useCaptureGrants(t)

		g := capabilityMint("token", capabilitySaved(), viewOps...)

		ctx, cancel := grantChecker{}.untilRevoked(t.Context(), g)
		defer cancel()

		captureGrants.Revoke(g.ID, "alice")

		polltest.Within(t, "ending the context of a revoked grant", func() { <-ctx.Done() })
	})

	t.Run("access lost", func(t *testing.T) {
		useCaptureGrants(t)
		useCapRecheck(t)
		useGlobalAdmin(t)

		g := capabilityMint("token", capability.Target{Exp: "gone-experiment"}, viewOps...)

		ctx, cancel := grantChecker{}.untilRevoked(t.Context(), g)
		defer cancel()

		polltest.Within(t, "ending the context of a grant whose experiment is gone", func() { <-ctx.Done() })

		if list := captureGrants.List("alice", "gone-experiment"); len(list) != 0 {
			t.Errorf("captureGrants.List(alice, gone-experiment) = %+v, want the grant revoked", list)
		}
	})

	t.Run("store unreadable, then access kept, then lost", func(t *testing.T) {
		useCaptureGrants(t)
		useCapRecheck(t)
		useTestExperiment(t)
		useGlobalAdmin(t)

		failing := capabilityUseFailingStore(t)
		g := capabilityMint("token", capabilitySaved(), viewOps...)

		ctx, cancel := grantChecker{}.untilRevoked(t.Context(), g)
		defer cancel()

		polltest.Until(t, "three checks of the grant", func() bool { return failing.gets.Load() >= 3 })

		if ctx.Err() != nil {
			t.Fatalf("untilRevoked context while the store fails = %v, want it open", ctx.Err())
		}

		before := time.Now()

		failing.failing.Store(false)

		polltest.Until(t, "the grant checked again", func() bool {
			list := captureGrants.List("alice", "test-experiment")

			return len(list) == 1 && list[0].Checked.After(before)
		})

		if ctx.Err() != nil {
			t.Fatalf("untilRevoked context while access is kept = %v, want it open", ctx.Err())
		}

		if err := store.Delete(&store.Config{Kind: "Experiment", Metadata: store.ConfigMetadata{Name: "test-experiment"}}); err != nil {
			t.Fatal(err)
		}

		polltest.Within(t, "ending the context once the experiment is deleted", func() { <-ctx.Done() })
	})
}

// The stop hook ends only the stopped experiment's stream grants.
func TestExperimentStopHookRevokesStreamGrants(t *testing.T) {
	useTestStore(t, namedExperiment(t, "running", "2024-01-01T00:00:00Z"))
	useCaptureGrants(t)

	// a forced stop, as minimega cannot clear the namespace
	fake := &teardownMM{testMM: &testMM{vms: mm.VMs{}}}
	fake.failing.Store(true)

	experimentStopUseMM(t, fake)

	var (
		view   = capabilityMint("view", capability.Target{Exp: "running", File: "a.pcap"}, viewOps...)
		other  = capabilityMint("pcs_other", capability.Target{Exp: "other", VM: "vm", Live: true}, capability.OpStream)
		stream = capabilityMint("pcs_stream", capability.Target{Exp: "running", VM: "vm", Live: true}, capability.OpStream)
	)

	stop := serveAs(t, "alice", testRole("experiments/stop", "update", "running")).URL +
		"/api/v1/experiments/running/stop?force=true"
	if resp := do(t, http.MethodPost, stop, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST stop = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	got := append(captureGrants.List("alice", "running"), captureGrants.List("alice", "other")...)
	if diff := cmp.Diff([]capability.Grant{view, other}, got, grantCmp); diff != "" {
		t.Errorf("grants after stopping running (not %s) mismatch (-want +got):\n%s", stream.ID, diff)
	}
}

// The revoke functions end the grants they name and no others.
func TestRevokeCaptureGrants(t *testing.T) {
	tests := map[string]struct {
		revoke func()
		want   []string // tokens of the grants left
	}{
		"file deleted": {
			func() { revokeExperimentFileGrants("test-experiment", "a.pcap") }, []string{"b", "pcs_c", "d"},
		},
		"experiment stopped": {
			func() { revokeOnExperimentStop("stop", "test-experiment") }, []string{"a", "b", "d"},
		},
		"experiment deleted": {
			func() { revokeOnExperimentDelete("delete", "test-experiment") }, []string{"d"},
		},
		"no experiment matches": {
			func() { revokeOnExperimentDelete("delete", "none") }, []string{"a", "b", "pcs_c", "d"},
		},
		"nothing matches": {
			func() { revokeCaptureGrants("test", func(capability.Grant) bool { return false }) },
			[]string{"a", "b", "pcs_c", "d"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureGrants(t)

			capabilityMint("a", capabilitySaved(), viewOps...)
			capabilityMint("b", capability.Target{Exp: "test-experiment", File: "b.pcap"}, viewOps...)
			capabilityMint("pcs_c", capability.Target{Exp: "test-experiment", File: "live.pcap", VM: "vm", Live: true}, capability.OpStream)
			capabilityMint("d", capability.Target{Exp: "other", File: "a.pcap"}, viewOps...)

			tc.revoke()

			left := append(captureGrants.List("alice", "test-experiment"), captureGrants.List("alice", "other")...)

			got := make([]string, 0, len(left))
			for _, g := range left {
				got = append(got, g.ID)
			}

			want := make([]string, 0, len(tc.want))
			for _, token := range tc.want {
				want = append(want, capability.IDOf(token))
			}

			if diff := cmp.Diff(want, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("grants left after %s mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// capabilityTokensRequest is user's request of the captureTokens route with
// the route's variables vars.
func capabilityTokensRequest(t *testing.T, method, user string, vars map[string]string) *http.Request {
	t.Helper()

	ctx := context.WithValue(t.Context(), middleware.ContextKeyUser, user)
	r := httptest.NewRequestWithContext(ctx, method, "/api/v1/experiments/"+vars["exp"]+"/captureTokens", nil)

	return mux.SetURLVars(r, vars)
}

// The user's own tokens of the experiment are listed, without the tokens.
func TestListCaptureTokens(t *testing.T) {
	useCaptureGrants(t)

	mine := capabilityMint("secret-mine", capabilitySaved(), viewOps...)
	capabilityMint("secret-other-exp", capability.Target{Exp: "other", File: "a.pcap"}, viewOps...)
	captureGrants.Mint("secret-bobs", capability.Grant{
		User: "bob", Target: capabilitySaved(), Ops: viewOps, Expires: time.Now().Add(time.Hour),
	})

	for user, want := range map[string][]captureToken{
		"alice": {newCaptureToken(mine)},
		"carol": {},
	} {
		w := httptest.NewRecorder()
		ListCaptureTokens(w, capabilityTokensRequest(t, http.MethodGet, user, map[string]string{"exp": "test-experiment"}))

		var body struct {
			Tokens []captureToken `json:"tokens"`
		}

		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("ListCaptureTokens(%s) = %s, not JSON: %v", user, w.Body.String(), err)
		}

		if body.Tokens == nil {
			t.Errorf("ListCaptureTokens(%s) = %s, want a list, not null", user, w.Body.String())
		}

		if diff := cmp.Diff(want, body.Tokens, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("ListCaptureTokens(%s) mismatch (-want +got):\n%s", user, diff)
		}

		if strings.Contains(w.Body.String(), "secret-") {
			t.Errorf("ListCaptureTokens(%s) = %s, want no token", user, w.Body.String())
		}

		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("ListCaptureTokens(%s) Content-Type = %q, want application/json", user, got)
		}
	}
}

// A user revokes only their own token, by the experiment it is for.
func TestDeleteCaptureToken(t *testing.T) {
	tests := map[string]struct {
		user, exp string
		id        func(g capability.Grant) string
		want      int
	}{
		"own token":            {"alice", "test-experiment", func(g capability.Grant) string { return g.ID }, http.StatusNoContent},
		"another user's token": {"bob", "test-experiment", func(g capability.Grant) string { return g.ID }, http.StatusNotFound},
		"another experiment":   {"alice", "other", func(g capability.Grant) string { return g.ID }, http.StatusNotFound},
		"unknown ID":           {"alice", "test-experiment", func(capability.Grant) string { return "unknown" }, http.StatusNotFound},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useCaptureGrants(t)

			g := capabilityMint("token", capabilitySaved(), viewOps...)
			events := captureAudit(t)

			w := httptest.NewRecorder()
			weberror.ErrorHandler(DeleteCaptureToken).ServeHTTP(w, capabilityTokensRequest(
				t, http.MethodDelete, tc.user, map[string]string{"exp": tc.exp, "id": tc.id(g)},
			))

			if w.Code != tc.want {
				t.Errorf("DeleteCaptureToken(%s) = %d, want %d: %s", name, w.Code, tc.want, w.Body.String())
			}

			revoked := tc.want == http.StatusNoContent
			if left := len(captureGrants.List("alice", "test-experiment")); (left == 0) != revoked {
				t.Errorf("DeleteCaptureToken(%s) left %d grants, want revoked = %t", name, left, revoked)
			}

			n := countAudit(events(), "capture-token-revoke", "success", map[string]any{"phenix.capability.id": g.ID})
			if (n == 1) != revoked {
				t.Errorf("DeleteCaptureToken(%s) audited %d revocations, want revoked = %t", name, n, revoked)
			}
		})
	}
}

// A WebShark token links to the embedded WebShark when it may view the
// capture, and to nothing when it may only download or watch it.
func TestCreateCaptureTokenURLs(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"saved.pcap": string(testPcapHeader())})

	server := serveAs(t, "alice", sharkRole()).URL

	tests := map[string]struct {
		ops      string
		wantOps  []capability.Op
		wantView bool
	}{
		"view":                   {`["view"]`, []capability.Op{capability.OpView}, true},
		"download":               {`["download"]`, []capability.Op{capability.OpDownload}, false},
		"watch and view, repeat": {`["watch","view","watch"]`, []capability.Op{capability.OpWatch, capability.OpView}, true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resp, token := createCaptureToken(t, server, `{"path":"saved.pcap","operations":`+tc.ops+`}`)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("POST captureTokens %s = %d, want %d: %s", tc.ops, resp.StatusCode, http.StatusCreated, readBody(t, resp))
			}

			if got := resp.Header.Get("Cache-Control"); got != "no-store" {
				t.Errorf("POST captureTokens %s Cache-Control = %q, want no-store", tc.ops, got)
			}

			var want map[string]string
			if tc.wantView {
				want = map[string]string{"view": "webshark/embed?capture=" + url.QueryEscape(token.Token)}
			}

			if diff := cmp.Diff(want, token.URLs); diff != "" {
				t.Errorf("POST captureTokens %s URLs mismatch (-want +got):\n%s", tc.ops, diff)
			}

			if diff := cmp.Diff(tc.wantOps, token.Operations); diff != "" {
				t.Errorf("POST captureTokens %s operations mismatch (-want +got):\n%s", tc.ops, diff)
			}

			if token.ID != capability.IDOf(token.Token) || strings.HasPrefix(token.Token, streamTokenPrefix) {
				t.Errorf("POST captureTokens %s = ID %q, token %q; want the token's ID and a WebShark name", tc.ops, token.ID, token.Token)
			}
		})
	}
}
