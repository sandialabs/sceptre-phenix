package web

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"phenix/api/job"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/web/util"
)

// requestFiles sends a request for test-experiment's files, under path, as a
// user who may only take the given verb on them.
func requestFiles(t *testing.T, method, path, body, verb string) *http.Response {
	t.Helper()

	server := serveAs(t, "alice", testRole("experiments/files", verb, "test-experiment")).URL

	return do(t, method, server+"/api/v1/experiments/test-experiment/files"+path, body)
}

func TestGetExperimentFilesReportsTotalBeforePaging(t *testing.T) {
	useTestFiles(t, "a", "b", "c")

	tests := map[string]struct {
		query     string
		wantCode  int
		wantFiles int
	}{
		"paged":     {query: "?pageNum=1&perPage=2", wantCode: http.StatusOK, wantFiles: 2},
		"last page": {query: "?pageNum=2&perPage=2", wantCode: http.StatusOK, wantFiles: 1},
		"not paged": {query: "", wantCode: http.StatusOK, wantFiles: 3},
		// web/util's ParsePage tests cover every invalid page
		"invalid page": {query: "?pageNum=0&perPage=2", wantCode: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resp := requestFiles(t, http.MethodGet, tc.query, "", "list")
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, resp.StatusCode, readBody(t, resp))
			}

			if tc.wantCode != http.StatusOK {
				return
			}

			var body struct {
				Files []file.File `json:"files"`
				Total int         `json:"total"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decoding response: %v", err)
			}

			if body.Total != 3 || len(body.Files) != tc.wantFiles {
				t.Fatalf("got %d files of %d, want %d of 3", len(body.Files), body.Total, tc.wantFiles)
			}
		})
	}
}

func TestExperimentFileHandlersForbidden(t *testing.T) {
	tests := map[string]struct {
		method string
		path   string
		body   string
		verb   string // the role's only verb, not the one needed
		action string // the refused action audited
	}{
		"delete one": {method: http.MethodDelete, path: "/a.log?path=a.log", verb: "get", action: "file-delete"},
		"delete several": {
			method: http.MethodPost, path: "/delete", body: `{"paths":["a.log"]}`, verb: "get", action: "file-delete",
		},
		"download one": {method: http.MethodGet, path: "/a.log?path=a.log", verb: "list", action: "file-download"},
		"download several": {
			method: http.MethodPost, path: "/download", body: `{"paths":["a.log"]}`, verb: "list", action: "file-download",
		},
		"archive several": {
			method: http.MethodPost, path: "/archive", body: `{"paths":["a.log"]}`, verb: "list", action: "file-download",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := useTestFiles(t, "a.log")
			events := captureAudit(t)

			resp := requestFiles(t, tc.method, tc.path, tc.body, tc.verb)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", resp.StatusCode, readBody(t, resp))
			}

			if len(fake.deleted) != 0 {
				t.Fatalf("deleted %v without permission", fake.deleted)
			}

			wantDenied(t, events(), tc.action)
		})
	}
}

func TestDeleteExperimentFile(t *testing.T) {
	tests := map[string]struct {
		path        string
		wantCode    int
		wantDeleted []string
	}{
		"listed": {
			path:        "scorch/run-0/a.log",
			wantCode:    http.StatusNoContent,
			wantDeleted: []string{"test-experiment/files/scorch/run-0/a.log"},
		},
		"not listed":     {path: "b.log", wantCode: http.StatusNotFound},
		"directory":      {path: "scorch", wantCode: http.StatusNotFound},
		"being captured": {path: "live.pcap", wantCode: http.StatusBadRequest},
		"escapes":        {path: "../other/files/a.log", wantCode: http.StatusBadRequest},
		"absolute":       {path: "/scorch/run-0/a.log", wantCode: http.StatusBadRequest},
		"unclean":        {path: "scorch//run-0/a.log", wantCode: http.StatusBadRequest},
		"empty":          {path: "", wantCode: http.StatusBadRequest},
		"glob":           {path: "scorch/*", wantCode: http.StatusBadRequest},
		"listed glob":    {path: "a[1].log", wantCode: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)
			useTestMM(t, &testMM{captures: []mm.Capture{
				{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/live.pcap"},
			}})

			fake := useTestFiles(t, "scorch/run-0/a.log", "a[1].log", "live.pcap")
			events := captureAudit(t)

			resp := requestFiles(t, http.MethodDelete, "/x?path="+url.QueryEscape(tc.path), "", "delete")
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, resp.StatusCode, readBody(t, resp))
			}

			if !slices.Equal(fake.deleted, tc.wantDeleted) {
				t.Fatalf("deleted %v, want %v", fake.deleted, tc.wantDeleted)
			}

			// authorized, so audited whether it worked or not
			outcome := "failure"
			if tc.wantCode == http.StatusNoContent {
				outcome = "success"
			}

			fields := map[string]any{"user.name": "alice", "phenix.experiment.name": "test-experiment"}
			if tc.path != "" {
				fields["file.path"] = tc.path
			}

			wantAudit(t, events(), "file-delete", outcome, fields)
		})
	}
}

func TestDeleteExperimentFilesReportsEachFailure(t *testing.T) {
	fake := useTestFiles(t, "a.log", "b.log")
	events := captureAudit(t)

	resp := requestFiles(t, http.MethodPost, "/delete", `{"paths":["a.log","missing.log","../x","b.log","a.log"]}`, "delete")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var body struct {
		Deleted []string `json:"deleted"`
		Failed  []struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		} `json:"failed"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if !slices.Equal(body.Deleted, []string{"a.log", "b.log"}) {
		t.Errorf("deleted %v", body.Deleted)
	}

	if len(body.Failed) != 2 || body.Failed[0].Path != "missing.log" || body.Failed[1].Path != "../x" {
		t.Errorf("failed %+v", body.Failed)
	}

	if want := []string{"test-experiment/files/a.log", "test-experiment/files/b.log"}; !slices.Equal(fake.deleted, want) {
		t.Errorf("asked minimega to delete %v, want %v", fake.deleted, want)
	}

	got := events()

	for path, outcome := range map[string]string{
		"a.log": "success", "b.log": "success", "missing.log": "failure", "../x": "failure",
	} {
		e := wantAudit(t, got, "file-delete", outcome, map[string]any{"user.name": "alice", "file.path": path})
		if outcome == "failure" && e != nil && e["error.message"] == nil {
			t.Errorf("%s: failure without error.message", path)
		}
	}

	if n := len(got); n != 4 {
		t.Errorf("%d audit events, want one per path: %v", n, got)
	}
}

func TestDeleteExperimentFilesNeedsPaths(t *testing.T) {
	useTestFiles(t)

	for _, body := range []string{``, `{}`, `{"paths":[]}`, `not json`} {
		if resp := requestFiles(t, http.MethodPost, "/delete", body, "delete"); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: expected 400, got %d", body, resp.StatusCode)
		}
	}
}

func TestDownloadExperimentFilesStreamsZip(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha", "scorch/run-0/b.json": "{}"})
	useTestFiles(t)

	events := captureAudit(t)

	resp := requestFiles(t, http.MethodPost, "/download", `{"paths":["a.log","scorch/run-0/b.json"]}`, "get")

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	if got := resp.Header.Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q", got)
	}

	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename=test-experiment-files.zip` {
		t.Errorf("Content-Disposition = %q", got)
	}

	// built as it is sent: never resumable
	if got := resp.Header.Get("Accept-Ranges"); got != "none" {
		t.Errorf("Accept-Ranges = %q, want none", got)
	}

	archive, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}

	got := map[string]string{}

	for _, f := range archive.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}

		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(data)
	}

	if len(got) != 2 || got["a.log"] != "alpha" || got["scorch/run-0/b.json"] != "{}" {
		t.Errorf("zip holds %v", got)
	}

	for _, path := range []string{"a.log", "scorch/run-0/b.json"} {
		wantAudit(t, events(), "file-download", "success", map[string]any{"user.name": "alice", "file.path": path})
	}
}

// A download failing part way still audits each file sent before it failed.
func TestDownloadExperimentFilesAuditsFilesSentBeforeFailing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read an unreadable file")
	}

	useExperimentFilesDir(t, map[string]string{"a.log": "alpha", "b.log": "beta"})
	useTestFiles(t)

	unreadable := filepath.Join(common.PhenixBase, "images", "test-experiment", "files", "b.log")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}

	events := captureAudit(t)

	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		server+"/api/v1/experiments/test-experiment/files/download", strings.NewReader(`{"paths":["a.log","b.log"]}`))
	if err != nil {
		t.Fatal(err)
	}

	// The aborted response fails when its headers or its body are read.
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}

	if err == nil {
		t.Fatal("a download that failed part way ended cleanly")
	}

	wantAudit(t, events(), "file-download", "success", map[string]any{"file.path": "a.log"})
	wantAudit(t, events(), "file-download", "failure", map[string]any{"phenix.file.count": 2})

	if n := countAudit(events(), "file-download", "success", map[string]any{"file.path": "b.log"}); n != 0 {
		t.Errorf("audited the file that failed as downloaded")
	}
}

func TestDownloadExperimentFilesRefusals(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha"})

	tooMany := make([]string, maxZipFiles+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("f%d.log", i)
	}

	tooManyBody, _ := json.Marshal(map[string][]string{"paths": tooMany})

	tests := map[string]struct {
		body     string
		wantCode int
	}{
		"too many files": {body: string(tooManyBody), wantCode: http.StatusRequestEntityTooLarge},
		"too many bytes": {body: `{"paths":["a.log","big.iso"]}`, wantCode: http.StatusRequestEntityTooLarge},
		"escapes":        {body: `{"paths":["../../etc/passwd"]}`, wantCode: http.StatusBadRequest},
		"missing":        {body: `{"paths":["c.log"]}`, wantCode: http.StatusNotFound},
		"no paths":       {body: `{"paths":[]}`, wantCode: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// only a mesh node has big.iso, so its size is the listing's
			fake := useTestFiles(t, "big.iso")
			fake.files[0].Size = maxZipBytes

			resp := requestFiles(t, http.MethodPost, "/download", tc.body, "get")
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, resp.StatusCode, readBody(t, resp))
			}

			if resp.Header.Get("Content-Type") == "application/zip" {
				t.Fatal("started a zip for a refused download")
			}
		})
	}
}

// getExperimentFile requests one of test-experiment's files, by its path, as
// a user who may get them, with the given headers (name, value pairs).
func getExperimentFile(t *testing.T, method, path string, header ...string) *http.Response {
	t.Helper()

	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL

	return do(t, method, server+"/api/v1/experiments/test-experiment/files/"+filepath.Base(path)+
		"?path="+url.QueryEscape(path), "", header...)
}

// rewriteExperimentFile replaces one of test-experiment's files with new
// contents and a later modification time.
func rewriteExperimentFile(t *testing.T, path, contents string) {
	t.Helper()

	local := filepath.Join(common.PhenixBase, "images", "test-experiment", "files", path)

	if err := os.WriteFile(local, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(local, later, later); err != nil {
		t.Fatal(err)
	}
}

// A browser or curl resumes an interrupted download with a range, and gets
// the rest of the file only while it is unchanged.
func TestGetExperimentFileResumes(t *testing.T) {
	data := strings.Repeat("0123456789abcdef", 4096) // 64 KiB
	useExperimentFilesDir(t, map[string]string{"scorch/run-0/out.bin": data})

	events := captureAudit(t)
	downloads := func() int {
		return countAudit(events(), "file-download", "success", map[string]any{"file.path": "scorch/run-0/out.bin"})
	}

	resp := getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin")
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || body != data {
		t.Fatalf("download: status %d, %d bytes", resp.StatusCode, len(body))
	}

	etag := resp.Header.Get("ETag")
	if etag == "" || strings.HasPrefix(etag, "W/") {
		t.Fatalf("ETag = %q, want a strong one", etag)
	}

	for k, want := range map[string]string{
		"Accept-Ranges":       "bytes",
		"Content-Disposition": "attachment; filename=out.bin",
		"Content-Length":      strconv.Itoa(len(data)),
		"Content-Encoding":    "",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	resp = getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin", "Range", "bytes=40000-", "If-Range", etag)
	if body := readBody(t, resp); resp.StatusCode != http.StatusPartialContent || body != data[40000:] {
		t.Fatalf("resume: status %d, %d bytes; want 206 with the last %d", resp.StatusCode, len(body), len(data)-40000)
	}

	// audited once, not per resumed range
	if n := downloads(); n != 1 {
		t.Errorf("%d downloads audited after one and a resume, want 1", n)
	}

	rewritten := strings.ToUpper(data)
	rewriteExperimentFile(t, "scorch/run-0/out.bin", rewritten)

	// the file changed: the whole new file, not its tail spliced onto the old
	resp = getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin", "Range", "bytes=40000-", "If-Range", etag)
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || body != rewritten {
		t.Fatalf("resume of a changed file: status %d, %d bytes; want 200 with the whole file", resp.StatusCode, len(body))
	}
}

func TestGetExperimentFileHead(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha"})

	resp := getExperimentFile(t, http.MethodHead, "a.log")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if resp.Header.Get("ETag") == "" || resp.Header.Get("Content-Length") != "5" {
		t.Errorf("ETag %q, Content-Length %q", resp.Header.Get("ETag"), resp.Header.Get("Content-Length"))
	}

	if body := readBody(t, resp); body != "" {
		t.Errorf("HEAD response has a body: %q", body)
	}
}

// The Files tab's viewer gets the file inline, compressed, and revalidates a
// file it has shown before.
func TestGetExperimentFileViewerIsInline(t *testing.T) {
	text := strings.Repeat("phenix log line\n", 256)
	useExperimentFilesDir(t, map[string]string{"a.log": text})

	resp := getExperimentFile(t, http.MethodGet, "a.log", "Accept", "text/plain", "Accept-Encoding", "gzip")
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || body != text {
		t.Fatalf("view: status %d, body %q", resp.StatusCode, body)
	}

	if got := resp.Header.Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q for the viewer", got)
	}

	if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}

	// gzipped, so no longer byte for byte the file a strong ETag names
	etag := resp.Header.Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("ETag = %q, want a weak one", etag)
	}

	resp = getExperimentFile(t, http.MethodGet, "a.log", "Accept", "text/plain", "If-None-Match", etag)
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("revalidating: status %d, want 304", resp.StatusCode)
	}
}

func TestGetExperimentFileViewerRefusesLargeFiles(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"big.log": strings.Repeat("x", maxViewBytes+1)})

	resp := getExperimentFile(t, http.MethodGet, "big.log", "Accept", "text/plain")
	if body := readBody(t, resp); resp.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(body, "download it") {
		t.Fatalf("status %d, body %q; want 413 with a hint", resp.StatusCode, body)
	}

	// downloading it is fine
	resp = getExperimentFile(t, http.MethodHead, "big.log")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("download: status %d, want 200", resp.StatusCode)
	}
}

func TestGetExperimentFileRefuses(t *testing.T) {
	tests := map[string]struct {
		path     string
		wantCode int
	}{
		"being captured": {path: "live.pcap", wantCode: http.StatusBadRequest},
		"directory":      {path: "scorch", wantCode: http.StatusNotFound},
		"missing":        {path: "missing.log", wantCode: http.StatusNotFound},
		"escapes":        {path: "../other/files/a.log", wantCode: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)
			useTestMM(t, &testMM{captures: []mm.Capture{
				{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/live.pcap"},
			}})
			useExperimentFilesDir(t, map[string]string{"live.pcap": "pcap", "scorch/run-0/a.log": "a"})
			useTestFiles(t, "live.pcap", "scorch/run-0/a.log")

			resp := getExperimentFile(t, http.MethodGet, tc.path)
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, resp.StatusCode, readBody(t, resp))
			}
		})
	}
}

func TestGetExperimentFileAudits(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha", "captures/r.pcap": "pcap"})

	tests := map[string]struct {
		path    string
		header  []string
		outcome string
		fields  map[string]any
	}{
		"download": {
			path: "a.log", outcome: "success",
			fields: map[string]any{"phenix.file.preview": false, "phenix.file.capture": false, "file.name": "a.log"},
		},
		"capture download": {
			path: "captures/r.pcap", outcome: "success",
			fields: map[string]any{"phenix.file.capture": true, "file.path": "captures/r.pcap", "file.name": "r.pcap"},
		},
		"preview": {
			path: "a.log", header: []string{"Accept", "text/plain"}, outcome: "success",
			fields: map[string]any{"phenix.file.preview": true},
		},
		"range from the start": {
			path: "a.log", header: []string{"Range", "bytes=0-1"}, outcome: "success",
			fields: map[string]any{"http.request.range": "bytes=0-1"},
		},
		"missing": {path: "gone.log", outcome: "failure", fields: map[string]any{"file.path": "gone.log"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			_ = readBody(t, getExperimentFile(t, http.MethodGet, tc.path, tc.header...))

			tc.fields["user.name"] = "alice"
			tc.fields["phenix.experiment.name"] = "test-experiment"

			wantAudit(t, events(), "file-download", tc.outcome, tc.fields)
		})
	}

	// a HEAD request reads nothing
	events := captureAudit(t)
	if _ = getExperimentFile(t, http.MethodHead, "a.log"); len(events()) != 0 {
		t.Errorf("HEAD audited: %v", events())
	}
}

// A file's checksum, once known, also comes with every download of it, so a
// resumed download can be verified.
func TestGetExperimentFileChecksum(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"scorch/run-0/out.bin": "contents"})

	resp := getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin")
	if digest := resp.Header.Get("Repr-Digest"); digest != "" {
		t.Fatalf("Repr-Digest = %q before anyone asked for the checksum", digest)
	}

	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL
	resp = do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/files/out.bin/checksum?path=scorch/run-0/out.bin", "")

	// a file this small is hashed before the answer
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checksum: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var body util.Checksum
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte("contents"))
	if body.Path != "scorch/run-0/out.bin" || body.SHA256 != hex.EncodeToString(sum[:]) || body.Size != 8 {
		t.Fatalf("got %+v", body)
	}

	want := "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"

	resp = getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin", "Range", "bytes=4-")
	if got := resp.Header.Get("Repr-Digest"); resp.StatusCode != http.StatusPartialContent || got != want {
		t.Errorf("resumed download: status %d, Repr-Digest %q; want 206 with %q", resp.StatusCode, got, want)
	}

	if got := resp.Header.Get("ETag"); got != body.ETag {
		t.Errorf("download ETag %q, checksum ETag %q", got, body.ETag)
	}
}

func TestGetExperimentFileChecksumRefuses(t *testing.T) {
	tests := map[string]struct {
		path     string
		verb     string
		wantCode int
	}{
		"not allowed":    {path: "a.log", verb: "list", wantCode: http.StatusForbidden},
		"being captured": {path: "live.pcap", verb: "get", wantCode: http.StatusBadRequest},
		"missing":        {path: "missing.log", verb: "get", wantCode: http.StatusNotFound},
		"escapes":        {path: "../other/files/a.log", verb: "get", wantCode: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)
			useTestMM(t, &testMM{captures: []mm.Capture{
				{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/live.pcap"},
			}})
			useExperimentFilesDir(t, map[string]string{"live.pcap": "pcap", "a.log": "a"})
			useTestFiles(t, "live.pcap", "a.log")

			events := captureAudit(t)

			resp := requestFiles(t, http.MethodGet, "/x/checksum?path="+url.QueryEscape(tc.path), "", tc.verb)
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, resp.StatusCode, readBody(t, resp))
			}

			if tc.wantCode == http.StatusForbidden {
				wantDenied(t, events(), "file-download")
			}
		})
	}
}

// startArchive asks for an archive of test-experiment's files at paths as a
// user who may get them, and returns the job started.
func startArchive(t *testing.T, server, body string) job.Job {
	t.Helper()

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/files/archive", body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}

	if loc := resp.Header.Get("Location"); loc != "/api/v1/jobs/"+started.ID {
		t.Errorf("Location %q", loc)
	}

	return started
}

// zipEntries reads a zip archive's entries.
func zipEntries(t *testing.T, body string) map[string]string {
	t.Helper()

	archive, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}

	got := map[string]string{}

	for _, f := range archive.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}

		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(data)
	}

	return got
}

// An archive job builds the zip, which its owner downloads from the job.
func TestArchiveExperimentFiles(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha", "scorch/run-0/b.json": "{}"})
	useTestFiles(t)

	registry := useJobs(t)
	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL
	events := captureAudit(t)

	started := startArchive(t, server, `{"paths":["a.log","scorch/run-0/b.json"]}`)

	if started.Kind != "files.archive" || started.Experiment != "test-experiment" || len(started.Items) != 2 ||
		started.Progress.BytesTotal != int64(len("alpha{}")) {
		t.Errorf("started %+v", started)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusSucceeded || j.Artifact == nil || j.Artifact.Name != "test-experiment-files.zip" ||
		j.Artifact.Incomplete {
		t.Fatalf("job %s, artifact %+v", j.Status, j.Artifact)
	}

	resp := do(t, http.MethodGet, server+"/api/v1/jobs/"+j.ID+"/artifact", "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("artifact: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	if got := zipEntries(t, body); len(got) != 2 || got["a.log"] != "alpha" || got["scorch/run-0/b.json"] != "{}" {
		t.Errorf("zip holds %v", got)
	}

	// each file is audited as downloaded when the archive is, once however
	// many requests resuming it take
	resume := do(t, http.MethodGet, server+"/api/v1/jobs/"+j.ID+"/artifact", "", "Range", "bytes=5-")
	if resume.StatusCode != http.StatusPartialContent {
		t.Errorf("resume: %d", resume.StatusCode)
	}

	got := events()
	for _, path := range []string{"a.log", "scorch/run-0/b.json"} {
		if n := countAudit(got, "file-download", "success", map[string]any{
			"user.name": "alice", "phenix.experiment.name": "test-experiment", "file.path": path,
			"phenix.job.id": j.ID, "url.path": "/api/v1/jobs/" + j.ID + "/artifact",
		}); n != 1 {
			t.Errorf("%s audited %d times, want 1: %v", path, n, got)
		}
	}

	if n := len(got); n != 2 {
		t.Errorf("%d audit events, want 2: %v", n, got)
	}
}

// A file that cannot be fetched is left out: the job ends partial, with an
// incomplete archive listing it in MISSING.txt.
func TestArchiveExperimentFilesLeavesOutFailures(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha"})

	// only a mesh node has remote.log, and it cannot be copied
	fake := useTestFiles(t, "remote.log")
	fake.files[0].Size = 6

	registry := useJobs(t)
	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL
	events := captureAudit(t)

	j := waitJob(t, registry, startArchive(t, server, `{"paths":["a.log","remote.log"]}`).ID)

	if j.Status != job.StatusPartial || j.Artifact == nil || !j.Artifact.Incomplete ||
		j.Artifact.Name != "test-experiment-files-incomplete.zip" {
		t.Fatalf("job %s, artifact %+v", j.Status, j.Artifact)
	}

	if j.Items[1].Status != job.ItemFailed || !strings.Contains(j.Items[1].Error, "remote.log") {
		t.Errorf("remote.log's outcome %+v", j.Items[1])
	}

	resp := do(t, http.MethodGet, server+"/api/v1/jobs/"+j.ID+"/artifact", "")

	got := zipEntries(t, readBody(t, resp))
	if got["a.log"] != "alpha" || !strings.Contains(got["MISSING.txt"], "remote.log: ") || len(got) != 2 {
		t.Errorf("zip holds %v", got)
	}

	// only the file archived reached the user
	downloaded := events()
	wantAudit(t, downloaded, "file-download", "success", map[string]any{"file.path": "a.log", "phenix.job.id": j.ID})

	if n := countAudit(downloaded, "file-download", "success", nil); n != 1 {
		t.Errorf("%d files audited as downloaded, want 1: %v", n, downloaded)
	}

	// with nothing archived, the job fails without an artifact, audited as a
	// failed download
	j = waitJob(t, registry, startArchive(t, server, `{"paths":["remote.log"]}`).ID)
	if j.Status != job.StatusFailed || j.Artifact != nil {
		t.Errorf("job of only failures: %s, artifact %+v", j.Status, j.Artifact)
	}

	waitAudit(t, events, "file-download", "failure", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "test-experiment", "phenix.job.id": j.ID,
		"phenix.file.count": 1,
	})
}

// A canceled archive job leaves no archive behind.
func TestArchiveExperimentFilesCanceled(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha"})

	var (
		fake     = useTestFiles(t, "remote.log")
		copying  = make(chan struct{})
		release  = make(chan struct{})
		dir      = t.TempDir()
		registry = useJobsIn(t, dir)
		server   = serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL
		events   = captureAudit(t)
	)

	fake.copy = func(string) error {
		close(copying)
		<-release

		return nil
	}

	started := startArchive(t, server, `{"paths":["a.log","remote.log"]}`)

	<-copying

	if resp := do(t, http.MethodPost, server+"/api/v1/jobs/"+started.ID+"/cancel", ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel: %d", resp.StatusCode)
	}

	close(release)

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusCanceled || j.Artifact != nil {
		t.Fatalf("job %s, artifact %+v", j.Status, j.Artifact)
	}

	entries, err := os.ReadDir(filepath.Join(dir, started.ID))
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		t.Errorf("canceled job left %s", e.Name())
	}

	waitAudit(t, events, "file-download", "failure", map[string]any{
		"user.name": "alice", "phenix.job.id": j.ID, "error.message": "canceled",
	})
}
