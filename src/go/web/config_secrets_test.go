package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/rbac"
)

const testToken = "header.payload.signature"

// testPasswordHash is testPassword hashed once at bcrypt's lowest cost, which checking it keeps.
var testPasswordHash = sync.OnceValue(func() string { //nolint:gochecknoglobals // computed once for every test
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	return string(hash)
})

// createUserWithToken stores the user with a role, testPassword and testToken.
func createUserWithToken(t *testing.T, username string) {
	t.Helper()

	err := store.Create(&store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "User", Metadata: store.ConfigMetadata{Name: username},
		Spec: map[string]any{"username": username, "password": testPasswordHash()},
	})
	if err != nil {
		t.Fatalf("creating user %s: %v", username, err)
	}

	user, err := rbac.GetUser(username)
	if err != nil {
		t.Fatalf("rbac.GetUser(%s) = %v", username, err)
	}

	role := testRole("experiments", "get", "*")
	role.Spec.Name = "Viewer"

	if err := user.SetRole(&role); err != nil {
		t.Fatalf("setting %s's role: %v", username, err)
	}

	if err := user.AddToken(testToken, "test token"); err != nil {
		t.Fatalf("adding %s's token: %v", username, err)
	}
}

// secretsIn names the user secrets that text holds.
func secretsIn(text string) []string {
	var found []string

	for _, secret := range []string{"$2a$", "test token", "password", "tokens"} {
		if strings.Contains(text, secret) {
			found = append(found, secret)
		}
	}

	return found
}

// The configs API returns User configs without the password hash and tokens,
// in GET as JSON or YAML and in single and zipped downloads.
//
//nolint:paralleltest // replaces the store
func TestConfigAPIHidesUserSecrets(t *testing.T) {
	storetest.Use(t)
	createUserWithToken(t, "alice")
	createUserWithToken(t, "bob")

	viewer := testRole("configs", "get", "User/*")
	vars := map[string]string{"kind": "user", "name": "alice"}

	for _, accept := range []string{mimeJSON, "application/x-yaml"} {
		w := httptest.NewRecorder()
		req := roleRequest(http.MethodGet, "/api/v1/configs/user/alice", "", vars, viewer)
		req.Header.Set("Accept", accept)

		if err := GetConfig(w, req); err != nil {
			t.Fatalf("GetConfig(GET user/alice as %s) = %v", accept, err)
		}

		if found := secretsIn(w.Body.String()); len(found) > 0 {
			t.Errorf("GetConfig(GET user/alice as %s) holds %q, want no secrets", accept, found)
		}

		if !strings.Contains(w.Body.String(), "alice") {
			t.Errorf("GetConfig(GET user/alice as %s) = %s, want the username", accept, w.Body.String())
		}
	}

	for _, body := range []string{`["User/alice"]`, `["User/alice","User/bob"]`} {
		w := httptest.NewRecorder()
		if err := DownloadConfigs(w, roleRequest(http.MethodPost, "/api/v1/configs/download", body, nil, viewer)); err != nil {
			t.Fatalf("DownloadConfigs(%s) = %v", body, err)
		}

		text := w.Body.String()

		if zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); err == nil {
			var files strings.Builder

			for _, f := range zr.File {
				rc, err := f.Open()
				if err != nil {
					t.Fatalf("opening %s in DownloadConfigs(%s): %v", f.Name, body, err)
				}

				b, err := io.ReadAll(rc)
				_ = rc.Close()

				if err != nil {
					t.Fatalf("reading %s in DownloadConfigs(%s): %v", f.Name, body, err)
				}

				files.Write(b)
			}

			text = files.String()
		}

		if found := secretsIn(text); len(found) > 0 {
			t.Errorf("DownloadConfigs(%s) holds %q, want no secrets", body, found)
		}

		if !strings.Contains(text, "alice") {
			t.Errorf("DownloadConfigs(%s) = %s, want alice's config", body, text)
		}
	}
}

// A User config sent back as GET returned it, or with other credentials,
// keeps the stored password and tokens, through PUT /configs and the workflow
// upsert alike.
//
//nolint:paralleltest // replaces the store
func TestUpdateUserConfigKeepsSecrets(t *testing.T) {
	admin := testRole("configs", "*", "User/*")
	vars := map[string]string{"kind": "user", "name": "alice"}

	update := map[string]func(t *testing.T, body string) error{
		"PUT": func(t *testing.T, body string) error {
			t.Helper()

			return UpdateConfig(httptest.NewRecorder(), roleRequest(http.MethodPut, "/api/v1/configs/user/alice", body, vars, admin))
		},
		"workflow upsert": func(t *testing.T, body string) error {
			t.Helper()

			return WorkflowUpsertConfig(httptest.NewRecorder(), roleRequest(http.MethodPost,
				"/api/v1/workflow/configs/main", body, map[string]string{"branch": "main"}, admin))
		},
	}

	for route, send := range update {
		for _, injected := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s injected=%t", route, injected), func(t *testing.T) {
				storetest.Use(t)
				createUserWithToken(t, "alice")

				w := httptest.NewRecorder()
				if err := GetConfig(w, roleRequest(http.MethodGet, "/api/v1/configs/user/alice", "", vars, admin)); err != nil {
					t.Fatalf("GetConfig(GET user/alice) = %v", err)
				}

				var cfg map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
					t.Fatalf("GetConfig(GET user/alice) = %s, not JSON: %v", w.Body.String(), err)
				}

				spec, _ := cfg["spec"].(map[string]any)
				spec["first_name"] = "Alicia"

				if injected {
					spec["password"] = "not-a-hash"
					spec["tokens"] = map[string]any{"aW5qZWN0ZWQ=": "injected"}
				}

				body, err := json.Marshal(cfg)
				if err != nil {
					t.Fatalf("json.Marshal(alice's config) = %v", err)
				}

				if err := send(t, string(body)); err != nil {
					t.Fatalf("%s of alice's config = %v", route, err)
				}

				user, err := rbac.GetUser("alice")
				if err != nil {
					t.Fatalf("rbac.GetUser(alice) = %v", err)
				}

				if got := user.FirstName(); got != "Alicia" {
					t.Errorf("%s of alice's config: first name = %q, want Alicia", route, got)
				}

				if err := user.ValidatePassword(testPassword); err != nil {
					t.Errorf("%s of alice's config: ValidatePassword(testPassword) = %v, want the password kept", route, err)
				}

				if err := user.ValidateToken(testToken); err != nil {
					t.Errorf("%s of alice's config: ValidateToken(testToken) = %v, want the token kept", route, err)
				}

				if err := user.ValidateToken("injected"); err == nil {
					t.Errorf("%s of alice's config: ValidateToken(injected) = nil, want no token added", route)
				}
			})
		}
	}
}

// An update without If-Match fails with a conflict when the user's secrets
// change after keepUserSecrets read them, so it cannot write back old tokens.
//
//nolint:paralleltest // replaces the store
func TestKeepUserSecretsRefusesConcurrentChange(t *testing.T) {
	storetest.Use(t)
	createUserWithToken(t, "alice")

	cfg, err := store.NewConfig("User/alice")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Get(cfg); err != nil {
		t.Fatalf("store.Get(User/alice) = %v", err)
	}

	cfg.Metadata.Revision = 0

	if err := keepUserSecrets("User/alice", cfg); err != nil {
		t.Fatalf("keepUserSecrets(User/alice) = %v", err)
	}

	user, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatalf("rbac.GetUser(alice) = %v", err)
	}

	if err := user.AddToken("second.token.value", "second token"); err != nil {
		t.Fatalf("adding alice's second token: %v", err)
	}

	if err := config.Update("User/alice", cfg); !errors.Is(err, store.ErrConflict) {
		t.Errorf("config.Update(User/alice) after a concurrent token change = %v, want %v", err, store.ErrConflict)
	}

	if err := user.ValidateToken("second.token.value"); err != nil {
		t.Errorf("ValidateToken(second token) after the refused update = %v, want the token kept", err)
	}
}
