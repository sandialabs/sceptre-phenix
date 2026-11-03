package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"phenix/api/disk"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

// diskTestMM reports a fixed minimega files directory.
type diskTestMM struct {
	mm.MM

	filesDir string
}

func (m *diskTestMM) GetMMArgs() (map[string]string, error) {
	return map[string]string{"filepath": m.filesDir}, nil
}

// qcow2Header returns a qcow2 v3 header with the given incompatible feature bits.
func qcow2Header(incompatible uint64) string {
	header := make([]byte, 104)
	copy(header, "QFI\xfb")
	binary.BigEndian.PutUint32(header[4:], 3)
	binary.BigEndian.PutUint64(header[72:], incompatible)

	return string(header)
}

// installDiskTestFiles creates a files directory holding images, plus a file
// in a sibling directory and one in the parent that must never be served, and
// symlinks in the files directory that lead to each.
func installDiskTestFiles(t *testing.T) string {
	t.Helper()

	base := t.TempDir()
	filesDir := filepath.Join(base, "images")

	for path, body := range map[string]string{
		filepath.Join(filesDir, "ok.qc2"):              "image",
		filepath.Join(filesDir, "base.qc2"):            "image",
		filepath.Join(filesDir, "plain.qc2"):           qcow2Header(0),
		filepath.Join(filesDir, "data.qc2"):            qcow2Header(1 << 2), // the external data file bit
		filepath.Join(filesDir, "copydir.qc2", "keep"): "",
		filepath.Join(base, "images-old", "x.qc2"):     "sibling",
		filepath.Join(base, "secret"):                  "secret",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for link, target := range map[string]string{
		"alias.qc2":          "ok.qc2",
		"abs-alias.qc2":      filepath.Join(filesDir, "ok.qc2"),
		"escape.qc2":         filepath.Join(base, "secret"),
		"outside":            base,
		"dangling.qc2":       filepath.Join(base, "missing.qc2"),
		"copydir.qc2/ok.qc2": filepath.Join(base, "secret"),
	} {
		if err := os.Symlink(target, filepath.Join(filesDir, link)); err != nil {
			t.Fatal(err)
		}
	}

	original := mm.DefaultMM

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = &diskTestMM{filesDir: filesDir} //nolint:reassign // install test double

	return base
}

func downloadDiskRequest(disk string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/disks/download?disk="+url.QueryEscape(disk), nil)
	req = mux.SetURLVars(req, map[string]string{"disk": disk})

	// Global Viewer grants disks get on every name.
	role := rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{
				{Resources: []string{"disks"}, ResourceNames: []string{"*"}, Verbs: []string{"get"}},
			},
		},
	}

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	return req.WithContext(ctx)
}

func TestDownloadDiskStaysInFilesDirectory(t *testing.T) {
	base := installDiskTestFiles(t)

	// On macOS the temporary directory is reached through the /var symlink.
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		disk     string
		wantCode int
	}{
		"real path of files directory": {disk: filepath.Join(realBase, "images", "ok.qc2"), wantCode: http.StatusOK},
		"relative image":               {disk: "ok.qc2", wantCode: http.StatusOK},
		"absolute image":               {disk: filepath.Join(base, "images", "ok.qc2"), wantCode: http.StatusOK},
		"relative parent":              {disk: "../secret", wantCode: http.StatusBadRequest},
		"relative deep parent":         {disk: "../../../../../../" + filepath.Join(base, "secret"), wantCode: http.StatusBadRequest},
		"absolute with dot dot":        {disk: base + "/images/../secret", wantCode: http.StatusBadRequest},
		"absolute sibling prefix":      {disk: filepath.Join(base, "images-old", "x.qc2"), wantCode: http.StatusBadRequest},
		"absolute outside":             {disk: filepath.Join(base, "secret"), wantCode: http.StatusBadRequest},
		"link inside":                  {disk: "alias.qc2", wantCode: http.StatusOK},
		"absolute link inside":         {disk: "abs-alias.qc2", wantCode: http.StatusOK},
		"link outside":                 {disk: "escape.qc2", wantCode: http.StatusBadRequest},
		"through linked dir":           {disk: "outside/secret", wantCode: http.StatusBadRequest},
		"dollar expands to parent":     {disk: ".$PHENIX_TEST_UNSET./secret", wantCode: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			DownloadDisk(rec, downloadDiskRequest(tc.disk))

			if rec.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
			}

			if tc.wantCode != http.StatusOK {
				// Paths are checked before authorization, so the answer must not reveal them.
				if strings.Contains(rec.Body.String(), base) {
					t.Errorf("rejection reveals a host path: %q", rec.Body.String())
				}

				return
			}

			if rec.Body.String() != "image" {
				t.Fatalf("expected image contents, got %q", rec.Body.String())
			}

			want := "attachment; filename=" + strconv.Quote(filepath.Base(tc.disk))
			if got := rec.Header().Get("Content-Disposition"); got != want {
				t.Errorf("Content-Disposition = %q, want %q", got, want)
			}
		})
	}
}

func TestUploadDiskRejectsMissingFile(t *testing.T) {
	installDiskTestFiles(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/disks", nil)
	role := rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{{Resources: []string{"disks"}, Verbs: []string{"upload"}}},
		},
	}
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, role))

	rec := httptest.NewRecorder()
	UploadDisk(rec, req) // used to dereference a nil file and panic

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// recordingDiskFiles records the paths the disk handlers hand to the disk API.
// Each image is backed by the image backings gives for its base name, else by
// backing, else by base.qc2 beside it, and has the format formats gives for its
// base name, or qcow2.
type recordingDiskFiles struct {
	disk.MMDiskFiles

	backing  string
	backings map[string]string
	formats  map[string]string
	paths    []string
}

func (d *recordingDiskFiles) record(paths ...string) error {
	d.paths = append(d.paths, paths...)

	return nil
}

func (d *recordingDiskFiles) format(path string) string {
	if f, ok := d.formats[filepath.Base(path)]; ok {
		return f
	}

	return "qcow2"
}

func (d *recordingDiskFiles) GetImage(path string) (disk.Details, error) {
	backing, ok := d.backings[filepath.Base(path)]
	if !ok {
		backing = d.backing
	}

	if backing == "" {
		backing = filepath.Join(filepath.Dir(path), "base.qc2")
	}

	details := disk.Details{
		BackingImages:  []string{filepath.Base(backing)},
		Format:         d.format(path),
		BackingPaths:   []string{backing},
		BackingFormats: []string{d.format(backing)},
	}

	return details, d.record(path)
}

func (d *recordingDiskFiles) CommitDisk(path string) error { return d.record(path) }

func (d *recordingDiskFiles) SnapshotDisk(src, dst string) error { return d.record(src, dst) }

func (d *recordingDiskFiles) RebaseDisk(src, dst string, _ bool) error { return d.record(src, dst) }

func (d *recordingDiskFiles) ResizeDisk(src, _ string) error { return d.record(src) }

func (d *recordingDiskFiles) CloneDisk(src, dst string) error { return d.record(src, dst) }

func (d *recordingDiskFiles) RenameDisk(src, dst string) error { return d.record(src, dst) }

func (d *recordingDiskFiles) DeleteDisk(src string) error { return d.record(src) }

func TestDiskHandlersStayInFilesDirectory(t *testing.T) {
	base := installDiskTestFiles(t)
	filesDir := filepath.Join(base, "images")

	original := disk.DefaultDiskFiles

	t.Cleanup(func() { disk.DefaultDiskFiles = original }) //nolint:reassign // restore test double

	handlers := map[string]http.HandlerFunc{
		"commit":   CommitDisk,
		"snapshot": SnapshotDisk,
		"rebase":   RebaseDisk,
		"resize":   ResizeDisk,
		"clone":    CloneDisk,
		"rename":   RenameDisk,
		"delete":   DeleteDisk,
	}

	inside := filepath.Join(filesDir, "ok.qc2")

	// only names the handlers that use the bad value; the rest must succeed.
	tests := map[string]struct {
		vars map[string]string
		only []string
	}{
		"inside":               {vars: map[string]string{"disk": "ok.qc2"}, only: []string{}},
		"absolute inside":      {vars: map[string]string{"disk": inside}, only: []string{}},
		"absolute link inside": {vars: map[string]string{"disk": "abs-alias.qc2"}, only: []string{}},
		"new is a directory": {
			vars: map[string]string{"new": "copydir.qc2"},
			only: []string{"snapshot", "clone", "rename"},
		},
		"disk dollar expands to parent": {vars: map[string]string{"disk": ".$PHENIX_TEST_UNSET./secret"}},
		"new dollar expands to parent": {
			vars: map[string]string{"new": ".${PHENIX_TEST_UNSET}./copy"},
			only: []string{"snapshot", "clone", "rename"},
		},
		"disk outside":        {vars: map[string]string{"disk": filepath.Join(base, "secret")}},
		"disk climbs out":     {vars: map[string]string{"disk": "../secret"}},
		"disk sibling prefix": {vars: map[string]string{"disk": filepath.Join(base, "images-old", "x.qc2")}},
		"files dir itself":    {vars: map[string]string{"disk": filesDir}},
		"new climbs out":      {vars: map[string]string{"new": "../../copy"}, only: []string{"snapshot", "clone", "rename"}},
		"new absolute outside": {
			vars: map[string]string{"new": filepath.Join(base, "copy")},
			only: []string{"snapshot", "clone", "rename"},
		},
		"backing outside":    {vars: map[string]string{"backing": filepath.Join(base, "secret")}, only: []string{"rebase"}},
		"disk links out":     {vars: map[string]string{"disk": "escape.qc2"}},
		"disk in linked dir": {vars: map[string]string{"disk": "outside/secret"}},
		"new links out":      {vars: map[string]string{"new": "escape.qc2"}, only: []string{"snapshot", "clone", "rename"}},
		"new in linked dir":  {vars: map[string]string{"new": "outside/copy"}, only: []string{"snapshot", "clone", "rename"}},
		"new dangling link":  {vars: map[string]string{"new": "dangling.qc2"}, only: []string{"snapshot", "clone", "rename"}},
		"backing links out":  {vars: map[string]string{"backing": "escape.qc2"}, only: []string{"rebase"}},
	}

	role := rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{{
				Resources:     []string{"disks"},
				ResourceNames: []string{"*"},
				Verbs:         []string{"get", "create", "update", "delete"},
			}},
		},
	}

	for hName, handler := range handlers {
		for name, tc := range tests {
			t.Run(hName+"/"+name, func(t *testing.T) {
				vars := map[string]string{"disk": "ok.qc2", "new": "copy", "unsafe": "true", "size": "+1G"}
				maps.Copy(vars, tc.vars)

				wantCode := http.StatusBadRequest
				if tc.only != nil && !slices.Contains(tc.only, hName) {
					wantCode = http.StatusOK
				}

				fake := new(recordingDiskFiles)
				disk.DefaultDiskFiles = fake //nolint:reassign // install test double

				req := httptest.NewRequest(http.MethodPost, "/api/v1/disks", nil)
				req = mux.SetURLVars(req, vars)
				ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
				ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

				rec := httptest.NewRecorder()
				handler(rec, req.WithContext(ctx))

				if rec.Code != wantCode {
					t.Fatalf("expected %d, got %d: %s", wantCode, rec.Code, rec.Body.String())
				}

				for _, p := range fake.paths {
					if p == "" {
						continue // rebase onto nothing
					}

					if rel, err := filepath.Rel(filesDir, p); err != nil || strings.HasPrefix(rel, "..") {
						t.Fatalf("disk API received path outside files directory: %s", p)
					}
				}

				if wantCode != http.StatusOK && len(fake.paths) > 0 {
					t.Fatalf("rejected request still reached the disk API with %v", fake.paths)
				}
			})
		}
	}
}

func TestDiskBackingChainStaysInFilesDirectory(t *testing.T) {
	base := installDiskTestFiles(t)
	filesDir := filepath.Join(base, "images")

	original := disk.DefaultDiskFiles

	t.Cleanup(func() { disk.DefaultDiskFiles = original }) //nolint:reassign // restore test double

	tests := map[string]struct {
		handler  http.HandlerFunc
		vars     map[string]string
		backing  string
		backings map[string]string
		formats  map[string]string
		wantCode int
	}{
		"commit backing climbs through a link": {
			handler: CommitDisk, backing: filesDir + "/outside/../base.qc2", wantCode: http.StatusBadRequest,
		},
		"commit vmdk backing": {
			handler: CommitDisk, formats: map[string]string{"base.qc2": "vmdk"}, wantCode: http.StatusBadRequest,
		},
		"commit raw backing": {
			handler: CommitDisk, formats: map[string]string{"base.qc2": "raw"}, wantCode: http.StatusOK,
		},
		"commit qed disk": {
			handler: CommitDisk, formats: map[string]string{"ok.qc2": "qed"}, wantCode: http.StatusBadRequest,
		},
		"safe rebase qed disk": {
			handler:  RebaseDisk,
			vars:     map[string]string{"unsafe": "false"},
			formats:  map[string]string{"ok.qc2": "qed"},
			wantCode: http.StatusBadRequest,
		},
		"resize vmdk disk": {
			handler:  ResizeDisk,
			vars:     map[string]string{"size": "+1G"},
			formats:  map[string]string{"ok.qc2": "vmdk"},
			wantCode: http.StatusBadRequest,
		},
		"commit inside": {
			handler: CommitDisk, backing: filepath.Join(filesDir, "base.qc2"), wantCode: http.StatusOK,
		},
		"commit outside": {
			handler: CommitDisk, backing: filepath.Join(base, "secret"), wantCode: http.StatusBadRequest,
		},
		"commit through link": {
			handler: CommitDisk, backing: filepath.Join(filesDir, "escape.qc2"), wantCode: http.StatusBadRequest,
		},
		"commit relative": {handler: CommitDisk, backing: "base.qc2", wantCode: http.StatusBadRequest},
		"safe rebase outside": {
			handler:  RebaseDisk,
			vars:     map[string]string{"unsafe": "false"},
			backing:  filepath.Join(base, "secret"),
			wantCode: http.StatusBadRequest,
		},
		"safe rebase onto image backed outside": {
			handler:  RebaseDisk,
			vars:     map[string]string{"unsafe": "false", "backing": "alias.qc2"},
			backings: map[string]string{"alias.qc2": filepath.Join(base, "secret")},
			wantCode: http.StatusBadRequest,
		},
		"safe rebase inside": {
			handler:  RebaseDisk,
			vars:     map[string]string{"unsafe": "false", "backing": "alias.qc2"},
			wantCode: http.StatusOK,
		},
		"unsafe rebase reads no chain": {
			handler:  RebaseDisk,
			vars:     map[string]string{"unsafe": "true"},
			backing:  filepath.Join(base, "secret"),
			wantCode: http.StatusOK,
		},
		"commit backing has external data": {
			handler: CommitDisk, backing: filepath.Join(filesDir, "data.qc2"), wantCode: http.StatusBadRequest,
		},
		"commit disk has external data": {
			handler: CommitDisk, vars: map[string]string{"disk": "data.qc2"}, wantCode: http.StatusBadRequest,
		},
		"commit qcow2 without external data": {
			handler: CommitDisk, backing: filepath.Join(filesDir, "plain.qc2"), wantCode: http.StatusOK,
		},
		"safe rebase onto external data": {
			handler:  RebaseDisk,
			vars:     map[string]string{"unsafe": "false", "backing": "data.qc2"},
			wantCode: http.StatusBadRequest,
		},
		"resize external data": {
			handler:  ResizeDisk,
			vars:     map[string]string{"disk": "data.qc2", "size": "+1G"},
			wantCode: http.StatusBadRequest,
		},
		"resize qcow2 without external data": {
			handler:  ResizeDisk,
			vars:     map[string]string{"disk": "plain.qc2", "size": "+1G"},
			wantCode: http.StatusOK,
		},
	}

	role := rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{{
				Resources:     []string{"disks"},
				ResourceNames: []string{"*"},
				Verbs:         []string{"get", "update"},
			}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			vars := map[string]string{"disk": "ok.qc2", "unsafe": "true"}
			maps.Copy(vars, tc.vars)

			fake := &recordingDiskFiles{backing: tc.backing, backings: tc.backings, formats: tc.formats}
			disk.DefaultDiskFiles = fake //nolint:reassign // install test double

			req := httptest.NewRequest(http.MethodPost, "/api/v1/disks", nil)
			req = mux.SetURLVars(req, vars)
			ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
			ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

			rec := httptest.NewRecorder()
			tc.handler(rec, req.WithContext(ctx))

			if rec.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDiskSymlinkNeedsTargetPermission(t *testing.T) {
	installDiskTestFiles(t)

	original := disk.DefaultDiskFiles

	t.Cleanup(func() { disk.DefaultDiskFiles = original }) //nolint:reassign // restore test double

	tests := map[string]struct {
		handler  http.HandlerFunc
		verb     string
		names    []string
		wantCode int
	}{
		"download alias only":       {handler: DownloadDisk, verb: "get", names: []string{"alias.qc2"}, wantCode: http.StatusForbidden},
		"download alias and target": {handler: DownloadDisk, verb: "get", names: []string{"alias.qc2", "ok.qc2"}, wantCode: http.StatusOK},
		"resize alias only":         {handler: ResizeDisk, verb: "update", names: []string{"alias.qc2"}, wantCode: http.StatusForbidden},
		"resize alias and target":   {handler: ResizeDisk, verb: "update", names: []string{"alias.qc2", "ok.qc2"}, wantCode: http.StatusOK},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			disk.DefaultDiskFiles = new(recordingDiskFiles) //nolint:reassign // install test double

			role := rbac.Role{
				Spec: &v1.RoleSpec{
					Policies: []*v1.PolicySpec{{
						Resources:     []string{"disks"},
						ResourceNames: tc.names,
						Verbs:         []string{tc.verb},
					}},
				},
			}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/disks", nil)
			req = mux.SetURLVars(req, map[string]string{"disk": "alias.qc2", "size": "+1G"})
			ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
			ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

			rec := httptest.NewRecorder()
			tc.handler(rec, req.WithContext(ctx))

			if rec.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDiskSourceAndDestinationNeedPermission(t *testing.T) {
	installDiskTestFiles(t)

	original := disk.DefaultDiskFiles

	t.Cleanup(func() { disk.DefaultDiskFiles = original }) //nolint:reassign // restore test double

	// perms maps disk names to the verbs the role has on them.
	tests := map[string]struct {
		handler  http.HandlerFunc
		vars     map[string]string
		perms    map[string][]string
		wantCode int
	}{
		"clone needs get on source": {
			handler:  CloneDisk,
			perms:    map[string][]string{"copy.qcow2": {"create"}},
			wantCode: http.StatusForbidden,
		},
		"clone to new disk": {
			handler:  CloneDisk,
			perms:    map[string][]string{"ok.qc2": {"get"}, "copy.qcow2": {"create"}},
			wantCode: http.StatusOK,
		},
		"clone needs update to replace a disk": {
			handler:  CloneDisk,
			vars:     map[string]string{"new": "base.qc2"},
			perms:    map[string][]string{"ok.qc2": {"get"}, "base.qc2": {"create"}},
			wantCode: http.StatusForbidden,
		},
		"clone replaces a disk": {
			handler:  CloneDisk,
			vars:     map[string]string{"new": "base.qc2"},
			perms:    map[string][]string{"ok.qc2": {"get"}, "base.qc2": {"create", "update"}},
			wantCode: http.StatusOK,
		},
		"snapshot needs get on source": {
			handler:  SnapshotDisk,
			perms:    map[string][]string{"copy.qcow2": {"create"}},
			wantCode: http.StatusForbidden,
		},
		"rename needs create on destination": {
			handler:  RenameDisk,
			perms:    map[string][]string{"ok.qc2": {"update"}},
			wantCode: http.StatusForbidden,
		},
		"rename to new disk": {
			handler:  RenameDisk,
			perms:    map[string][]string{"ok.qc2": {"update"}, "copy.qcow2": {"create"}},
			wantCode: http.StatusOK,
		},
		"rebase needs get on new backing": {
			handler:  RebaseDisk,
			vars:     map[string]string{"backing": "base.qc2"},
			perms:    map[string][]string{"ok.qc2": {"update"}},
			wantCode: http.StatusForbidden,
		},
		"rebase onto readable backing": {
			handler:  RebaseDisk,
			vars:     map[string]string{"backing": "base.qc2"},
			perms:    map[string][]string{"ok.qc2": {"update"}, "base.qc2": {"get"}},
			wantCode: http.StatusOK,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			vars := map[string]string{"disk": "ok.qc2", "new": "copy", "unsafe": "true"}
			maps.Copy(vars, tc.vars)

			disk.DefaultDiskFiles = new(recordingDiskFiles) //nolint:reassign // install test double

			role := rbac.Role{Spec: &v1.RoleSpec{}}
			for diskName, verbs := range tc.perms {
				role.Spec.Policies = append(role.Spec.Policies, &v1.PolicySpec{
					Resources: []string{"disks"}, ResourceNames: []string{diskName}, Verbs: verbs,
				})
			}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/disks", nil)
			req = mux.SetURLVars(req, vars)
			ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
			ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

			rec := httptest.NewRecorder()
			tc.handler(rec, req.WithContext(ctx))

			if rec.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
			}
		})
	}
}

// readRecorder records whether anything read from it.
type readRecorder struct {
	io.Reader

	read bool
}

func (r *readRecorder) Read(p []byte) (int, error) {
	r.read = true

	return r.Reader.Read(p)
}

func TestUploadDiskRefusesBeforeReading(t *testing.T) {
	installDiskTestFiles(t)

	upload := uploadDiskRequest(t, "new.qc2", "up")
	body := &readRecorder{Reader: upload.Body}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/disks", body)
	req.Header.Set("Content-Type", upload.Header.Get("Content-Type"))

	role := rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{{Resources: []string{"disks"}, ResourceNames: []string{"*"}, Verbs: []string{"get"}}},
		},
	}
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, role))

	rec := httptest.NewRecorder()
	UploadDisk(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	if body.read {
		t.Error("UploadDisk read the request body before refusing the caller")
	}
}

func uploadDiskRequest(t *testing.T, name, body string) *http.Request {
	t.Helper()

	var buf bytes.Buffer

	form := multipart.NewWriter(&buf)

	part, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := part.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}

	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/disks", &buf)
	req.Header.Set("Content-Type", form.FormDataContentType())

	role := rbac.Role{
		Spec: &v1.RoleSpec{
			Policies: []*v1.PolicySpec{{Resources: []string{"disks"}, Verbs: []string{"upload"}}},
		},
	}

	return req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, role))
}

func TestUploadDiskStaysInFilesDirectory(t *testing.T) {
	// wantFile is relative to the files directory. The body is shorter than any
	// existing image, so a replaced image must have been truncated.
	tests := map[string]struct {
		name     string
		wantCode int
		wantFile string
	}{
		"new image":            {name: "new.qc2", wantCode: http.StatusOK, wantFile: "new.qc2"},
		"replaces image":       {name: "ok.qc2", wantCode: http.StatusOK, wantFile: "ok.qc2"},
		"link inside":          {name: "alias.qc2", wantCode: http.StatusOK, wantFile: "ok.qc2"},
		"absolute link inside": {name: "abs-alias.qc2", wantCode: http.StatusOK, wantFile: "ok.qc2"},
		"link outside":         {name: "escape.qc2", wantCode: http.StatusBadRequest},
		"dangling link":        {name: "dangling.qc2", wantCode: http.StatusBadRequest},
		"directory in name":    {name: "outside/secret", wantCode: http.StatusOK, wantFile: "secret"},
	}

	const body = "up"

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			base := installDiskTestFiles(t)

			rec := httptest.NewRecorder()
			UploadDisk(rec, uploadDiskRequest(t, tc.name, body))

			if rec.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
			}

			if tc.wantFile != "" {
				path := filepath.Join(base, "images", tc.wantFile)

				got, err := os.ReadFile(path)
				if err != nil || string(got) != body {
					t.Errorf("ReadFile(%s) = %q, %v, want %q, nil", path, got, err, body)
				}
			}

			for path, want := range map[string]string{filepath.Join(base, "secret"): "secret"} {
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Errorf("ReadFile(%s) = %q, %v, want %q, nil", path, got, err, want)
				}
			}

			if _, err := os.Lstat(filepath.Join(base, "missing.qc2")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Lstat(missing.qc2) error = %v, want %v", err, fs.ErrNotExist)
			}
		})
	}
}
