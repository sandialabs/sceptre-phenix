package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/rbac"
)

const testToken = "header.payload.signature"

// createUserWithToken stores the user with a role, testPassword and testToken.
func createUserWithToken(t *testing.T, username string) {
	t.Helper()

	user := rbac.NewUser(username, testPassword)
	if user == nil {
		t.Fatalf("creating user %s", username)
	}

	role := testRole("experiments", "get", "*")
	role.Spec.Name = "Viewer"

	if err := user.SetRole(&role); err != nil {
		t.Fatal(err)
	}

	if err := user.AddToken(testToken, "test token"); err != nil {
		t.Fatal(err)
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
			t.Fatal(err)
		}

		if found := secretsIn(w.Body.String()); len(found) > 0 {
			t.Errorf("GET as %s returned %v", accept, found)
		}

		if !strings.Contains(w.Body.String(), "alice") {
			t.Errorf("GET as %s left out the username: %s", accept, w.Body.String())
		}
	}

	for _, body := range []string{`["User/alice"]`, `["User/alice","User/bob"]`} {
		w := httptest.NewRecorder()
		if err := DownloadConfigs(w, roleRequest(http.MethodPost, "/api/v1/configs/download", body, nil, viewer)); err != nil {
			t.Fatal(err)
		}

		text := w.Body.String()

		if zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); err == nil {
			var files strings.Builder

			for _, f := range zr.File {
				rc, _ := f.Open()
				b, _ := io.ReadAll(rc)
				_ = rc.Close()
				files.Write(b)
			}

			text = files.String()
		}

		if found := secretsIn(text); len(found) > 0 {
			t.Errorf("download of %s returned %v", body, found)
		}

		if !strings.Contains(text, "alice") {
			t.Errorf("download of %s left out alice: %s", body, text)
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
			storetest.Use(t)
			createUserWithToken(t, "alice")

			w := httptest.NewRecorder()
			if err := GetConfig(w, roleRequest(http.MethodGet, "/api/v1/configs/user/alice", "", vars, admin)); err != nil {
				t.Fatal(err)
			}

			var cfg map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
				t.Fatal(err)
			}

			spec, _ := cfg["spec"].(map[string]any)
			spec["first_name"] = "Alicia"

			if injected {
				spec["password"] = "not-a-hash"
				spec["tokens"] = map[string]any{"aW5qZWN0ZWQ=": "injected"}
			}

			body, _ := json.Marshal(cfg)
			if err := send(t, string(body)); err != nil {
				t.Fatalf("%s (injected %t): %v", route, injected, err)
			}

			user, err := rbac.GetUser("alice")
			if err != nil {
				t.Fatal(err)
			}

			if user.FirstName() != "Alicia" {
				t.Errorf("%s (injected %t): first name %q, want the update", route, injected, user.FirstName())
			}

			if err := user.ValidatePassword(testPassword); err != nil {
				t.Errorf("%s (injected %t): password not kept: %v", route, injected, err)
			}

			if err := user.ValidateToken(testToken); err != nil {
				t.Errorf("%s (injected %t): token not kept: %v", route, injected, err)
			}

			if err := user.ValidateToken("injected"); err == nil {
				t.Errorf("%s (injected %t): the update added a token", route, injected)
			}
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
		t.Fatal(err)
	}

	cfg.Metadata.Revision = 0

	if err := keepUserSecrets("User/alice", cfg); err != nil {
		t.Fatal(err)
	}

	user, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}

	if err := user.AddToken("second.token.value", "second token"); err != nil {
		t.Fatal(err)
	}

	if err := config.Update("User/alice", cfg); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("update after a concurrent token change: %v, want %v", err, store.ErrConflict)
	}

	if err := user.ValidateToken("second.token.value"); err != nil {
		t.Errorf("concurrent token lost: %v", err)
	}
}
