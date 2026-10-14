package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"phenix/util/common"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// uploadToFileServer posts a file to the file server's upload handler for
// test-experiment, as alice with role, and returns the response.
func uploadToFileServer(t *testing.T, role rbac.Role, name, contents string) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer

	form := multipart.NewWriter(&body)
	_ = form.WriteField("experiment", "test-experiment")

	part, err := form.CreateFormFile("files", name)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = part.Write([]byte(contents))
	_ = form.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())

	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, "alice")
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)

	rec := httptest.NewRecorder()
	uploadExperimentFiles(false).ServeHTTP(rec, req.WithContext(ctx))

	return rec
}

// Uploading experiment files through the file server is audited per file,
// refusals included.
func TestUploadExperimentFilesIsAudited(t *testing.T) {
	useTestStore(t, testExperiment(t, ""))
	useExperimentFilesDir(t, nil)

	events := captureAudit(t)

	rec := uploadToFileServer(t, testRole("experiments/files", "create", "test-experiment"), "notes.txt", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d: %s", rec.Code, rec.Body)
	}

	target := filepath.Join(common.PhenixBase, "images", "test-experiment", "files", "notes.txt")
	if b, err := os.ReadFile(target); err != nil || string(b) != "hello" {
		t.Fatalf("uploaded file: %q, %v", b, err)
	}

	wantAudit(t, events(), "file-upload", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "test-experiment",
		"phenix.file.target_path": target, "file.size": 5,
	})

	rec = uploadToFileServer(t, testRole("experiments/files", "get", "test-experiment"), "other.txt", "no")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("upload without permission: status %d", rec.Code)
	}

	wantDenied(t, events(), "file-upload")
}
