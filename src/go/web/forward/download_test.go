package forward

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
)

func TestGetTunneler(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := os.MkdirAll(filepath.Join("downloads", "tunneler"), 0o750); err != nil {
		t.Fatalf("creating downloads directory: %v", err)
	}

	err := os.WriteFile(filepath.Join("downloads", "tunneler", "phenix-tunneler-linux-amd64"), []byte("binary"), 0o600)
	if err != nil {
		t.Fatalf("writing tunneler: %v", err)
	}

	tests := []struct {
		name        string
		file        string
		want        int
		disposition string
	}{
		{
			name:        "tunneler binary",
			file:        "phenix-tunneler-linux-amd64",
			want:        http.StatusOK,
			disposition: `attachment; filename="phenix-tunneler-linux-amd64"`,
		},
		{name: "missing file", file: "phenix-tunneler-plan9-amd64", want: http.StatusBadRequest},
		{name: "current directory", file: ".", want: http.StatusBadRequest},
		{name: "parent directory", file: "..", want: http.StatusBadRequest},
	}

	for _, test := range tests {
		req := mux.SetURLVars(
			httptest.NewRequest(http.MethodGet, "/downloads/tunneler/"+test.file, nil),
			map[string]string{"name": test.file},
		)
		rec := httptest.NewRecorder()

		GetTunneler(rec, req)

		if rec.Code != test.want {
			t.Errorf("%s: got status %d, want %d", test.name, rec.Code, test.want)
		}

		if got := rec.Header().Get("Content-Disposition"); got != test.disposition {
			t.Errorf("%s: unexpected Content-Disposition %q", test.name, got)
		}
	}
}
