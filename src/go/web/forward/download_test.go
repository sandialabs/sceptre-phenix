package forward

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
)

// The tunneler routes serve the builds in the downloads directory of the
// directory phenix runs in: a sorted list of the files, and each file by name.
//
//nolint:paralleltest // changes the working directory
func TestTunnelerDownloads(t *testing.T) {
	router := mux.NewRouter()
	router.HandleFunc("/downloads/tunneler", ListTunnelers)
	router.HandleFunc("/downloads/tunneler/{name}", GetTunneler)

	tests := map[string]struct {
		path        string
		noDownloads bool
		status      int
		body        string
		disposition string
	}{
		"lists the installed builds": {
			path: "/downloads/tunneler", status: http.StatusOK,
			body: `{"files":["phenix-tunneler-darwin-arm64","phenix-tunneler-linux-amd64","phenix-tunneler-windows-amd64.exe"]}`,
		},
		"lists none without a downloads directory": {
			path: "/downloads/tunneler", noDownloads: true, status: http.StatusOK, body: `{"files":[]}`,
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
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}

			if tc.body != "" && rec.Body.String() != tc.body {
				t.Errorf("body = %q, want %q", rec.Body, tc.body)
			}

			if got := rec.Header().Get("Content-Disposition"); got != tc.disposition {
				t.Errorf("Content-Disposition = %q, want %q", got, tc.disposition)
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
