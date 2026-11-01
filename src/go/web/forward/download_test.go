package forward

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"

	"phenix/web/weberror"
)

// The tunneler routes serve the builds in the downloads directory of the
// directory phenix runs in: a sorted list of the files, and each file by name.
//
//nolint:paralleltest // changes the working directory
func TestTunnelerDownloads(t *testing.T) {
	router := mux.NewRouter()
	router.Handle("/downloads/tunneler", weberror.ErrorHandler(ListTunnelers))
	router.Handle("/downloads/tunneler/{name}", weberror.ErrorHandler(GetTunneler))

	tests := map[string]struct {
		path        string
		noDownloads bool
		status      int
		// files is the listing a list route answers; body is a file's contents
		files       []string
		body        string
		disposition string
	}{
		"lists the installed builds": {
			path: "/downloads/tunneler", status: http.StatusOK,
			files: []string{"phenix-tunneler-darwin-arm64", "phenix-tunneler-linux-amd64", "phenix-tunneler-windows-amd64.exe"},
		},
		"lists none without a downloads directory": {
			path: "/downloads/tunneler", noDownloads: true, status: http.StatusOK, files: []string{},
		},
		"serves a build as an attachment": {
			path: "/downloads/tunneler/phenix-tunneler-linux-amd64", status: http.StatusOK, body: "linux",
			disposition: "attachment; filename=phenix-tunneler-linux-amd64",
		},
		"serves a build linked from elsewhere": {
			path: "/downloads/tunneler/phenix-tunneler-windows-amd64.exe", status: http.StatusOK, body: "windows",
			disposition: "attachment; filename=phenix-tunneler-windows-amd64.exe",
		},
		"answers 404 for a build not installed": {
			path: "/downloads/tunneler/phenix-tunneler-linux-arm64", status: http.StatusNotFound,
		},
		"answers 404 for a directory":   {path: "/downloads/tunneler/subdir", status: http.StatusNotFound},
		"answers 404 for a broken link": {path: "/downloads/tunneler/phenix-tunneler-broken", status: http.StatusNotFound},
		"rejects a hidden file":         {path: "/downloads/tunneler/.hidden", status: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)

			if !tc.noDownloads {
				installTunnelers(t, filepath.Join(dir, tunnelerDir))
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			if rec.Code != tc.status {
				t.Fatalf("GET %s = %d %s, want %d", tc.path, rec.Code, rec.Body, tc.status)
			}

			if tc.files != nil {
				var got struct {
					Files []string `json:"files"`
				}

				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decoding GET %s: %v", tc.path, err)
				}

				// a JSON null where the route promises an empty list fails here
				if diff := cmp.Diff(tc.files, got.Files); diff != "" {
					t.Errorf("GET %s files mismatch (-want +got):\n%s", tc.path, diff)
				}
			}

			if tc.body != "" && rec.Body.String() != tc.body {
				t.Errorf("GET %s body = %q, want %q", tc.path, rec.Body, tc.body)
			}

			if got := rec.Header().Get("Content-Disposition"); got != tc.disposition {
				t.Errorf("GET %s Content-Disposition = %q, want %q", tc.path, got, tc.disposition)
			}
		})
	}
}

// installTunnelers fills dir with two builds, a link to a third build outside
// dir, a broken link, a hidden file and a directory.
func installTunnelers(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(filepath.Dir(dir), "phenix-tunneler-windows-amd64.exe")

	for path, content := range map[string]string{
		filepath.Join(dir, "phenix-tunneler-linux-amd64"):  "linux",
		filepath.Join(dir, "phenix-tunneler-darwin-arm64"): "darwin",
		filepath.Join(dir, ".hidden"):                      "hidden",
		outside:                                            "windows",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	links := map[string]string{
		"phenix-tunneler-windows-amd64.exe": outside,
		"phenix-tunneler-broken":            filepath.Join(filepath.Dir(dir), "missing"),
	}

	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// A build download answers HEAD and resumes with a range against its ETag.
//
//nolint:paralleltest // changes the working directory
func TestTunnelerDownloadResumes(t *testing.T) {
	router := mux.NewRouter()
	router.Handle("/downloads/tunneler/{name}", weberror.ErrorHandler(GetTunneler)).Methods("GET", "HEAD")

	dir := t.TempDir()
	t.Chdir(dir)
	installTunnelers(t, filepath.Join(dir, tunnelerDir))

	const path = "/downloads/tunneler/phenix-tunneler-linux-amd64"

	serve := func(method string, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		return rec
	}

	head := serve(http.MethodHead)
	etag := head.Header().Get("ETag")

	if head.Code != http.StatusOK || etag == "" || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "5" {
		t.Fatalf("HEAD %s = status %d, ETag %q, %d body bytes, Content-Length %q; want 200, an ETag, no body, 5",
			path, head.Code, etag, head.Body.Len(), head.Header().Get("Content-Length"))
	}

	resumed := serve(http.MethodGet, "Range", "bytes=2-", "If-Range", etag)
	if resumed.Code != http.StatusPartialContent || resumed.Body.String() != "nux" {
		t.Errorf("GET %s (Range bytes=2-, If-Range %s) = %d %q, want 206 nux", path, etag, resumed.Code, resumed.Body)
	}
}
