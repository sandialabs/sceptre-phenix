package forward

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"phenix/web/weberror"
)

// GetTunneler serves only a file directly in the downloads directory: a name
// it accepts never leads out of it, and is neither empty nor hidden.
func FuzzGetTunnelerName(f *testing.F) {
	for _, seed := range []string{
		"", ".", "..", ".hidden", "../phenix", "a/b", "/etc/passwd", "/", "phenix-tunneler-linux-amd64",
		"phenix-tunneler-windows-amd64.exe", "subdir", strings.Repeat("x", 300), "a\x00b", "..\\x",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/downloads/tunneler/x", nil), map[string]string{"name": name})
		rec := httptest.NewRecorder()

		var webErr *weberror.WebError

		err := GetTunneler(rec, req)
		if errors.As(err, &webErr) && webErr.Status == http.StatusBadRequest {
			return
		}

		if err == nil {
			t.Fatalf("GetTunneler(%q) served %d bytes, want no file outside the test's downloads", name, rec.Body.Len())
		}

		rel, relErr := filepath.Rel(tunnelerDir, filepath.Join(tunnelerDir, name))
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/") {
			t.Fatalf("GetTunneler(%q) accepted a name at %q, want one directly in %s", name, rel, tunnelerDir)
		}

		if name == "" || name[0] == '.' {
			t.Fatalf("GetTunneler(%q) accepted an empty or hidden name", name)
		}
	})
}
