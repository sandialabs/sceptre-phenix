package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/common"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// uploadToFileServer posts a file to the file server's upload handler for
// test-experiment, as alice with role, and returns the response.
func uploadToFileServer(t *testing.T, role rbac.Role, name, contents string) *httptest.ResponseRecorder {
	t.Helper()

	body, contentType := formUpload(t, "files", name, contents, "experiment", "test-experiment")

	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)

	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, "alice")
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)

	rec := httptest.NewRecorder()
	uploadExperimentFiles(false).ServeHTTP(rec, req.WithContext(ctx))

	return rec
}

// Uploading experiment files through the file server is audited per file, refusals
// included, and drops the cached file listing so the Files tab shows the new file at once.
func TestUploadExperimentFilesIsAudited(t *testing.T) {
	useTestStore(t, testExperiment(t, ""))
	useExperimentFilesDir(t, nil)

	var invalidated []string

	original := invalidateExperimentFiles
	invalidateExperimentFiles = func(exp string) { invalidated = append(invalidated, exp) }

	t.Cleanup(func() { invalidateExperimentFiles = original })

	events := captureAudit(t)

	rec := uploadToFileServer(t, testRole("experiments/files", "create", "test-experiment"), "notes.txt", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /upload of notes.txt as a files creator = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}

	target := filepath.Join(common.PhenixBase, "images", "test-experiment", "files", "notes.txt")

	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) after POST /upload error = %v", target, err)
	}

	if string(b) != "hello" {
		t.Errorf("POST /upload of notes.txt wrote %q, want %q", b, "hello")
	}

	wantAudit(t, events(), "file-upload", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "test-experiment",
		"phenix.file.target_path": target, "file.size": 5,
	})

	if diff := cmp.Diff([]string{"test-experiment"}, invalidated); diff != "" {
		t.Errorf("POST /upload invalidated file listings mismatch (-want +got):\n%s", diff)
	}

	rec = uploadToFileServer(t, testRole("experiments/files", "get", "test-experiment"), "other.txt", "no")
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /upload of other.txt as a files getter = %d, want %d", rec.Code, http.StatusForbidden)
	}

	wantDenied(t, events(), "file-upload")
}
