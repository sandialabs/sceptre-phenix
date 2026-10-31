package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/mux"

	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// installMountTestFiles builds an active mount for exp/vm whose guest file
// system holds regular files, a symlink inside the mount, and symlinks that
// lead out of it, plus a second active mount for exp/vm2. It returns the host
// directory.
func installMountTestFiles(t *testing.T) string {
	t.Helper()

	base := t.TempDir()
	mounts := filepath.Join(base, "mounts")
	host := filepath.Join(base, "host")
	guest := filepath.Join(mounts, "exp", "vm")

	for path, body := range map[string]string{
		filepath.Join(guest, "guest.txt"):                "guest",
		filepath.Join(mounts, "exp", "vm2", "other.txt"): "other vm",
		filepath.Join(host, "secret"):                    "host secret",
		filepath.Join(guest, "dir", "a.txt"):             "a",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for link, target := range map[string]string{
		"escape":           host,
		"up":               "../",
		"sibling":          "../vm2",
		"in-root":          "dir/a.txt",
		"dir/host-secret":  filepath.Join(host, "secret"),
		"dir/up-to-secret": "../../../../host/secret",
	} {
		if err := os.Symlink(target, filepath.Join(guest, link)); err != nil {
			t.Fatal(err)
		}
	}

	original := common.MountBase

	t.Cleanup(func() { common.MountBase = original }) //nolint:reassign // restore test fixture

	common.MountBase = mounts //nolint:reassign // test fixture

	activeMountsMu.Lock()
	for _, vm := range []string{"vm", "vm2"} {
		activeMounts[filepath.Join(mounts, "exp", vm)] = &MountInfo{1, &sync.RWMutex{}}
	}
	activeMountsMu.Unlock()

	t.Cleanup(func() {
		activeMountsMu.Lock()
		for _, vm := range []string{"vm", "vm2"} {
			delete(activeMounts, filepath.Join(mounts, "exp", vm))
		}
		activeMountsMu.Unlock()
	})

	return host
}

func mountRequest(method, path string, body *bytes.Buffer, contentType string) *http.Request {
	if body == nil {
		body = &bytes.Buffer{}
	}

	req := httptest.NewRequest(method, "/api/v1/experiments/exp/vms/vm/files?path="+url.QueryEscape(path), body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	req = mux.SetURLVars(req, map[string]string{"exp": "exp", "name": "vm", "path": path})

	role := rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{Resources: []string{"vms/mount"}, ResourceNames: []string{"exp/vm"}, Verbs: []string{"*"}},
				{Resources: []string{"experiments/files"}, ResourceNames: []string{"exp"}, Verbs: []string{"get"}},
			},
		},
	}

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	return req.WithContext(ctx)
}

func TestDownloadMountFileStaysInMount(t *testing.T) {
	installMountTestFiles(t)

	tests := map[string]struct {
		path     string
		wantCode int
		wantBody string
	}{
		"guest file":            {path: "/guest.txt", wantCode: http.StatusOK, wantBody: "guest"},
		"guest file in dir":     {path: "/dir/a.txt", wantCode: http.StatusOK, wantBody: "a"},
		"symlink in mount":      {path: "/in-root", wantCode: http.StatusOK, wantBody: "a"},
		"symlink out of mount":  {path: "/escape/secret", wantCode: http.StatusBadRequest},
		"symlink to host file":  {path: "/dir/host-secret", wantCode: http.StatusBadRequest},
		"relative symlink up":   {path: "/dir/up-to-secret", wantCode: http.StatusBadRequest},
		"symlink to parent":     {path: "/up/vm2/other.txt", wantCode: http.StatusBadRequest},
		"symlink to sibling":    {path: "/sibling/other.txt", wantCode: http.StatusBadRequest},
		"sibling mount":         {path: "/../vm2/other.txt", wantCode: http.StatusBadRequest},
		"relative sibling":      {path: "../vm2/other.txt", wantCode: http.StatusBadRequest},
		"parent of all mounts":  {path: "../../../host/secret", wantCode: http.StatusBadRequest},
		"directory not allowed": {path: "/dir", wantCode: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			DownloadMountFile(rec, mountRequest(http.MethodGet, tc.path, nil, ""))

			if rec.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
			}

			for _, leak := range []string{"host secret", "other vm"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Fatalf("response leaked a file outside the mount: %q", rec.Body.String())
				}
			}

			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Fatalf("expected body %q, got %q", tc.wantBody, rec.Body.String())
			}
		})
	}
}

func TestGetMountFilesDoesNotFollowSymlinkOutOfMount(t *testing.T) {
	installMountTestFiles(t)

	for _, path := range []string{"/escape", "/up", "/sibling"} {
		rec := httptest.NewRecorder()
		GetMountFiles(rec, mountRequest(http.MethodGet, path, nil, ""))

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 listing %s, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	GetMountFiles(rec, mountRequest(http.MethodGet, "/", nil, ""))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "guest.txt") {
		t.Fatalf("expected the mount root to list guest.txt, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetMountFilesTimesOutOnHungMount(t *testing.T) {
	// A FIFO stands in for a hung mount: opening it blocks until a writer
	// opens the other end.
	mounts := filepath.Join(t.TempDir(), "mounts")
	if err := os.MkdirAll(filepath.Join(mounts, "exp"), 0o750); err != nil {
		t.Fatal(err)
	}

	hung := filepath.Join(mounts, "exp", "vm")
	if err := syscall.Mkfifo(hung, 0o600); err != nil {
		t.Fatal(err)
	}

	original := common.MountBase

	t.Cleanup(func() { common.MountBase = original }) //nolint:reassign // restore test fixture

	common.MountBase = mounts //nolint:reassign // test fixture

	info := &MountInfo{1, &sync.RWMutex{}}

	activeMountsMu.Lock()
	activeMounts[hung] = info
	activeMountsMu.Unlock()

	t.Cleanup(func() {
		activeMountsMu.Lock()
		delete(activeMounts, hung)
		activeMountsMu.Unlock()

		// Unblock the abandoned open.
		if f, err := os.OpenFile(hung, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
	})

	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		GetMountFiles(rec, mountRequest(http.MethodGet, "/", nil, ""))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(MountPathTimeout + 2*time.Second):
		t.Fatal("listing a hung mount did not time out")
	}

	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "timeout") {
		t.Fatalf("expected a 500 timeout, got %d: %s", rec.Code, rec.Body.String())
	}

	if !info.lock.TryLock() {
		t.Fatal("listing a hung mount kept the mount locked, which blocks unmounting")
	}

	info.lock.Unlock()
}

func uploadMountFile(t *testing.T, dir, name string) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer

	mw := multipart.NewWriter(&body)

	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = fw.Write([]byte("planted"))
	_ = mw.Close()

	rec := httptest.NewRecorder()
	UploadMountFile(rec, mountRequest(http.MethodPut, dir, &body, mw.FormDataContentType()))

	return rec
}

func TestUploadMountFileStaysInMount(t *testing.T) {
	host := installMountTestFiles(t)

	if rec := uploadMountFile(t, "/escape", "planted.txt"); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 uploading through a symlink, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(host, "planted.txt")); err == nil {
		t.Fatal("upload wrote a file outside the mount")
	}

	for _, name := range []string{"host-secret", "up-to-secret"} {
		if rec := uploadMountFile(t, "/dir", name); rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 uploading over symlink %s, got %d: %s", name, rec.Code, rec.Body.String())
		}
	}

	if body, err := os.ReadFile(filepath.Join(host, "secret")); err != nil || string(body) != "host secret" {
		t.Fatalf("upload changed a file outside the mount: %q, %v", body, err)
	}

	for _, name := range []string{"..", "."} {
		if rec := uploadMountFile(t, "/dir", name); rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 uploading file name %q, got %d: %s", name, rec.Code, rec.Body.String())
		}
	}

	existing := filepath.Join(common.MountBase, "exp", "vm", "dir", "long.txt")
	if err := os.WriteFile(existing, []byte("a longer original body"), 0o600); err != nil {
		t.Fatal(err)
	}

	if rec := uploadMountFile(t, "/dir", "long.txt"); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 uploading into the mount, got %d: %s", rec.Code, rec.Body.String())
	}

	if body, err := os.ReadFile(existing); err != nil || string(body) != "planted" {
		t.Fatalf("expected the upload to replace long.txt, got %q, %v", body, err)
	}
}

func TestUploadMountFileWithoutFile(t *testing.T) {
	installMountTestFiles(t)

	rec := httptest.NewRecorder()
	UploadMountFile(rec, mountRequest(http.MethodPut, "/dir", nil, "multipart/form-data; boundary=x"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 uploading without a file, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCopyExperimentFileToMountStaysInMount(t *testing.T) {
	installMountTestFiles(t)

	for _, dir := range []string{"/escape", "/up", "/sibling"} {
		rec := httptest.NewRecorder()
		CopyExperimentFileToMount(rec, mountRequest(http.MethodPost, dir, nil, ""))

		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "escapes") {
			t.Fatalf("expected 400 copying into %s, got %d: %s", dir, rec.Code, rec.Body.String())
		}
	}
}
