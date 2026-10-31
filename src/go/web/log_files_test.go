package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/web/util"
	"phenix/web/weberror"
)

// logFilesServer serves the API as alice with logs get, the system log at
// phenix and minimega's log at minimega ("" for none), and returns its URL.
func logFilesServer(t *testing.T, phenix, minimega string) string {
	t.Helper()

	return serveContractAs(t, testRole("logs", "get"), func(o *serverOptions) {
		o.systemLog = func() string { return phenix }
		o.minimegaLogs = minimega
	}).URL
}

// writeLogFile writes contents to name in a new temporary directory and
// returns its path.
func writeLogFile(t *testing.T, name, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func ptr[T any](v T) *T { return &v }

// GET /logs/files lists phenix's log file and minimega's, each available or
// with why not: not configured, not written yet, or not a file.
func TestGetLogFiles(t *testing.T) {
	var (
		phenix   = writeLogFile(t, "phenix.log", "phenix\n")
		minimega = writeLogFile(t, "minimega.log", "minimega log\n")
		phenixOK = LogFile{Name: "phenix", Available: true, Size: ptr[int64](7)}
	)

	for name, tc := range map[string]struct {
		phenix, minimega string
		want             []LogFile
	}{
		"both": {
			phenix, minimega,
			[]LogFile{phenixOK, {Name: "minimega", Available: true, Size: ptr[int64](13)}},
		},
		"minimega not configured": {
			phenix, "",
			[]LogFile{phenixOK, {
				Name: "minimega", Reason: "the minimega log file is not configured (set ui.logs.minimega-path)",
			}},
		},
		"minimega not written yet": {
			phenix, filepath.Join(t.TempDir(), "minimega.log"),
			[]LogFile{phenixOK, {Name: "minimega", Reason: "the minimega log file does not exist yet"}},
		},
		"minimega a directory": {
			phenix, t.TempDir(),
			[]LogFile{phenixOK, {Name: "minimega", Reason: "the minimega log file is not a regular file"}},
		},
		"no system log": {
			"", minimega,
			[]LogFile{
				{Name: "phenix", Reason: "the phenix server writes no system log file"},
				{Name: "minimega", Available: true, Size: ptr[int64](13)},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodGet, logFilesServer(t, tc.phenix, tc.minimega)+"/api/v1/logs/files", "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /api/v1/logs/files = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
			}

			wantDocumentedBody(t, http.MethodGet, "/api/v1/logs/files", resp)

			var got logFileList
			if err := json.Unmarshal([]byte(readBody(t, resp)), &got); err != nil {
				t.Fatalf("decoding GET /api/v1/logs/files: %v", err)
			}

			for _, f := range got.Files {
				if f.Available != (f.Modified != nil) {
					t.Errorf("log file %s: available %t but modified %v", f.Name, f.Available, f.Modified)
				}
			}

			if diff := cmp.Diff(tc.want, got.Files, cmpopts.IgnoreFields(LogFile{}, "Modified")); diff != "" {
				t.Errorf("GET /api/v1/logs/files mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A log file downloads as a text attachment named as on disk, audited once:
// for the GET, not for HEAD or a resumed range.
func TestDownloadLogFile(t *testing.T) {
	var (
		phenix   = writeLogFile(t, "phenix.log", `{"msg":"one"}`+"\n")
		minimega = writeLogFile(t, "minimega.log", "2026/01/01 10:00:00 INFO main.go:1: started\n")
		server   = logFilesServer(t, phenix, minimega)
	)

	for name, path := range map[string]string{"phenix": phenix, "minimega": minimega} {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)
			url := server + "/api/v1/logs/files/" + name

			if resp := do(t, http.MethodHead, url, ""); resp.StatusCode != http.StatusOK {
				t.Fatalf("HEAD /api/v1/logs/files/%s = %d, want %d", name, resp.StatusCode, http.StatusOK)
			}

			resp := do(t, http.MethodGet, url, "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /api/v1/logs/files/%s = %d, want %d: %s", name, resp.StatusCode, http.StatusOK, readBody(t, resp))
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if got := readBody(t, resp); got != string(want) {
				t.Errorf("GET /api/v1/logs/files/%s body = %q, want %q", name, got, want)
			}

			headers := map[string]string{
				"Content-Type":        resp.Header.Get("Content-Type"),
				"Content-Disposition": resp.Header.Get("Content-Disposition"),
			}
			wantHeaders := map[string]string{
				"Content-Type":        "text/plain; charset=utf-8",
				"Content-Disposition": util.Attachment(filepath.Base(path)),
			}

			if diff := cmp.Diff(wantHeaders, headers); diff != "" {
				t.Errorf("GET /api/v1/logs/files/%s headers mismatch (-want +got):\n%s", name, diff)
			}

			resumed := do(t, http.MethodGet, url, "", "Range", "bytes=2-", "If-Range", resp.Header.Get("ETag"))
			if resumed.StatusCode != http.StatusPartialContent {
				t.Errorf("resumed GET /api/v1/logs/files/%s = %d, want %d", name, resumed.StatusCode, http.StatusPartialContent)
			}

			fields := map[string]any{"user.name": "alice", "phenix.log.name": name, "file.path": path}
			if n := countAudit(events(), "log-download", "success", fields); n != 1 {
				t.Errorf("log-download success events for %s = %d, want 1", name, n)
			}
		})
	}
}

// Without logs get, a download is refused and audited as refused; an unknown
// or unconfigured log file is a 404 audited as a failure.
func TestDownloadLogFileRefusals(t *testing.T) {
	phenix := writeLogFile(t, "phenix.log", "phenix\n")

	t.Run("without logs get", func(t *testing.T) {
		events := captureAudit(t)
		server := serveContractAs(t, testRole("experiments", "list"), func(o *serverOptions) {
			o.systemLog = func() string { return phenix }
		}).URL

		for _, path := range []string{"/api/v1/logs/files", "/api/v1/logs/files/phenix"} {
			resp := do(t, http.MethodGet, server+path, "")
			if got := errorMessage(t, resp.StatusCode, readBody(t, resp)); resp.StatusCode != http.StatusForbidden ||
				got != "forbidden" {
				t.Errorf("GET %s without logs get = %d %q, want %d forbidden", path, resp.StatusCode, got, http.StatusForbidden)
			}
		}

		wantDenied(t, events(), "log-download")
	})

	for name, tc := range map[string]struct {
		path, wantMsg string
	}{
		"unknown":                 {"/api/v1/logs/files/other", "no such log file: other"},
		"minimega not configured": {"/api/v1/logs/files/minimega", "the minimega log file is not configured (set ui.logs.minimega-path)"},
	} {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			resp := do(t, http.MethodGet, logFilesServer(t, phenix, "")+tc.path, "")
			if got := errorMessage(t, resp.StatusCode, readBody(t, resp)); resp.StatusCode != http.StatusNotFound ||
				got != tc.wantMsg {
				t.Errorf("GET %s = %d %q, want %d %q", tc.path, resp.StatusCode, got, http.StatusNotFound, tc.wantMsg)
			}

			wantAudit(t, events(), "log-download", "failure", map[string]any{"error.message": tc.wantMsg})
		})
	}
}

// Any name but phenix and minimega is a 404 not_found, and either file comes
// back byte for byte whatever it holds.
func TestLogFilesProperties(t *testing.T) {
	var (
		phenix = filepath.Join(t.TempDir(), "phenix.log")
		server = logFilesServer(t, phenix, "")
	)

	rapid.Check(t, func(rt *rapid.T) {
		name := rapid.StringMatching(`[A-Za-z0-9_-]{1,20}`).
			Filter(func(s string) bool { return s != logFilePhenix && s != logFileMinimega }).
			Draw(rt, "name")

		resp := do(t, http.MethodGet, server+"/api/v1/logs/files/"+name, "")

		var body struct {
			Code weberror.Code `json:"code"`
		}

		_ = json.Unmarshal([]byte(readBody(t, resp)), &body)

		if resp.StatusCode != http.StatusNotFound || body.Code != weberror.CodeNotFound {
			rt.Fatalf("GET /api/v1/logs/files/%s = %d %q, want %d %q",
				name, resp.StatusCode, body.Code, http.StatusNotFound, weberror.CodeNotFound)
		}

		contents := rapid.StringOf(rapid.Rune()).Draw(rt, "contents")
		if err := os.WriteFile(phenix, []byte(contents), 0o600); err != nil {
			rt.Fatal(err)
		}

		got := readBody(t, do(t, http.MethodGet, server+"/api/v1/logs/files/phenix", ""))
		if got != contents {
			rt.Fatalf("GET /api/v1/logs/files/phenix = %q, want %q", got, contents)
		}
	})
}
