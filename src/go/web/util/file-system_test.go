package util

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// failingFile is a file whose reads fail.
type failingFile struct{ fs.File }

func (failingFile) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// failingFS opens each file of its embedded file system as a failingFile.
type failingFS struct{ fstest.MapFS }

func (f failingFS) Open(name string) (fs.File, error) {
	file, err := f.MapFS.Open(name)
	if err != nil {
		return nil, err //nolint:wrapcheck // passes the file system error on
	}

	return failingFile{file}, nil
}

func TestServeTemplate(t *testing.T) {
	t.Parallel()

	files := fstest.MapFS{"page.html": {Data: []byte(`<p>{{.Name}}</p>`)}}

	tests := map[string]struct {
		fs     fs.FS
		name   string
		status int
		body   string // for a 200
	}{
		"fills the template and escapes HTML": {files, "page.html", http.StatusOK, "<p>&lt;b&gt;bob&lt;/b&gt;</p>"},
		"a missing template is a 500":         {files, "missing.html", http.StatusInternalServerError, ""},
		"an unreadable template is a 500":     {failingFS{files}, "page.html", http.StatusInternalServerError, ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			NewBinaryFileSystem(http.FS(tc.fs)).ServeTemplate(rec, tc.name, struct{ Name string }{"<b>bob</b>"})

			if rec.Code != tc.status {
				t.Errorf("ServeTemplate(%q) status = %d, want %d", tc.name, rec.Code, tc.status)
			}

			if tc.status == http.StatusOK {
				if got := rec.Body.String(); got != tc.body {
					t.Errorf("ServeTemplate(%q) body = %q, want %q", tc.name, got, tc.body)
				}

				return
			}

			wantJSONError(t, rec.Result())
		})
	}
}
