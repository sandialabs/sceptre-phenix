package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/web/capability"
	"phenix/web/rbac"
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

// A WebShark grant does not stream, and its operations are counted.
func TestCaptureGrantOperations(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	server := serveAs(t, "alice", sharkRole()).URL
	capture := openInWebShark(t, server, `{"path":"a.pcap"}`)

	_ = do(t, http.MethodGet, server+"/webshark/json?method=download&capture="+capture.Capture, "")

	want := []capability.Grant{savedGrant(capture.ID, "alice", 1)}
	if diff := cmp.Diff(want, captureGrants.List("alice", "test-experiment"), grantCmp); diff != "" {
		t.Errorf("captureGrants.List(alice, test-experiment) mismatch (-want +got):\n%s", diff)
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
		t.Fatalf("POST capture-tokens for test-vm's stream = %d, want %d: %s",
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
				t.Errorf("POST capture-tokens %s = %d, want %d: %s", tt.body, resp.StatusCode, tt.want, readBody(t, resp))
			}
		})
	}

	// streaming needs no WebShark; viewing does
	o.features = map[string]bool{}
	server := serveAs(t, "alice", sharkRole()).URL

	resp, _ := createCaptureToken(t, server, `{"path":"saved.pcap","operations":["view"]}`)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("POST capture-tokens to view without WebShark = %d, want %d", resp.StatusCode, http.StatusNotImplemented)
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
		t.Fatalf("POST capture-tokens token = %q, want a pcs_ token", token.Token)
	}

	if token.ID == "" || token.UseBy == "" || token.Expires == "" {
		t.Errorf("POST capture-tokens = %+v, want an ID, use-by and expiry", token)
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
		t.Errorf("POST capture-tokens mismatch (-want +got):\n%s", diff)
	}

	// listed without the token
	resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/capture-tokens", "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET capture-tokens = %d, want %d: %s", resp.StatusCode, http.StatusOK, body)
	}

	if strings.Contains(body, token.Token) {
		t.Errorf("GET capture-tokens = %s, want it without the token", body)
	}

	if !strings.Contains(body, token.ID) {
		t.Errorf("GET capture-tokens = %s, want the token's ID %s", body, token.ID)
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

	revoke := server + "/api/v1/experiments/test-experiment/capture-tokens/" + token.ID

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
		t, http.MethodPost, server+"/api/v1/experiments/test-experiment/capture-tokens",
		`{"vm":"test-vm","interface":0,"operations":["stream"]}`, session,
	)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST capture-tokens signed in = %d, want %d: %s", resp.StatusCode, http.StatusCreated, readBody(t, resp))
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

	if resp := signedIn(t, http.MethodGet, server+"/api/v1/logout", "", session); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("GET /api/v1/logout = %d, want %d", resp.StatusCode, http.StatusNoContent)
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
