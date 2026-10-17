package web

import (
	"archive/zip"
	"context"
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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/experiment"
	"phenix/api/job"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/web/rbac"
	"phenix/web/util"
)

// filesPath is the route of test-experiment's files.
const filesPath = "/api/v1/experiments/test-experiment/files"

// requestFiles sends a request for test-experiment's files, under path, as a
// user who may only take the given verb on them.
func requestFiles(t *testing.T, method, path, body, verb string) *http.Response {
	t.Helper()

	server := serveAs(t, "alice", testRole("experiments/files", verb, "test-experiment")).URL

	return do(t, method, server+filesPath+path, body)
}

func TestGetExperimentFilesReportsTotalBeforePaging(t *testing.T) {
	useTestFiles(t, "a", "b", "c")

	tests := map[string]struct {
		query     string
		wantCode  int
		wantFiles []string
	}{
		"paged":     {query: "?pageNum=1&perPage=2", wantCode: http.StatusOK, wantFiles: []string{"a", "b"}},
		"last page": {query: "?pageNum=2&perPage=2", wantCode: http.StatusOK, wantFiles: []string{"c"}},
		"not paged": {query: "", wantCode: http.StatusOK, wantFiles: []string{"a", "b", "c"}},
		// web/util's ParsePage tests cover every invalid page
		"invalid page": {query: "?pageNum=0&perPage=2", wantCode: http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resp := requestFiles(t, http.MethodGet, tc.query, "", "list")
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("GET %s%s = %d, want %d: %s", filesPath, tc.query, resp.StatusCode, tc.wantCode, readBody(t, resp))
			}

			if tc.wantCode != http.StatusOK {
				return
			}

			var body struct {
				Files []file.File `json:"files"`
				Total int         `json:"total"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("GET %s%s did not answer with a file list: %v", filesPath, tc.query, err)
			}

			if body.Total != 3 {
				t.Errorf("GET %s%s total = %d, want 3", filesPath, tc.query, body.Total)
			}

			names := make([]string, 0, len(body.Files))
			for _, f := range body.Files {
				names = append(names, f.Name)
			}

			if diff := cmp.Diff(tc.wantFiles, names); diff != "" {
				t.Errorf("GET %s%s file names mismatch (-want +got):\n%s", filesPath, tc.query, diff)
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
				t.Errorf("%s %s%s as a files %s role = %d, want %d: %s",
					tc.method, filesPath, tc.path, tc.verb, resp.StatusCode, http.StatusForbidden, readBody(t, resp))
			}

			if len(fake.deleted) != 0 {
				t.Errorf("%s %s%s as a files %s role deleted %v, want nothing", tc.method, filesPath, tc.path, tc.verb, fake.deleted)
			}

			wantDenied(t, events(), tc.action)
		})
	}
}

// useCapturingExperiment stores test-experiment, with minimega capturing to
// its file live.pcap.
func useCapturingExperiment(t *testing.T) {
	t.Helper()

	useTestExperiment(t)
	useTestMM(t, &testMM{captures: []mm.Capture{
		{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/live.pcap"},
	}})
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
			useCapturingExperiment(t)

			fake := useTestFiles(t, "scorch/run-0/a.log", "a[1].log", "live.pcap")
			events := captureAudit(t)

			resp := requestFiles(t, http.MethodDelete, "/x?path="+url.QueryEscape(tc.path), "", "delete")
			if resp.StatusCode != tc.wantCode {
				t.Errorf("DELETE %s/x?path=%s = %d, want %d: %s", filesPath, tc.path, resp.StatusCode, tc.wantCode, readBody(t, resp))
			}

			if diff := cmp.Diff(tc.wantDeleted, fake.deleted, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("DELETE %s/x?path=%s deleted files mismatch (-want +got):\n%s", filesPath, tc.path, diff)
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

	const request = `{"paths":["a.log","missing.log","../x","b.log","a.log"]}`

	resp := requestFiles(t, http.MethodPost, "/delete", request, "delete")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s/delete %s = %d, want %d: %s", filesPath, request, resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	type failure struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	}

	type deleteResult struct {
		Deleted []string  `json:"deleted"`
		Failed  []failure `json:"failed"`
	}

	var body deleteResult
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("POST %s/delete %s did not answer with a delete result: %v", filesPath, request, err)
	}

	want := deleteResult{
		Deleted: []string{"a.log", "b.log"},
		Failed:  []failure{{Path: "missing.log"}, {Path: "../x"}},
	}
	if diff := cmp.Diff(want, body, cmpopts.IgnoreFields(failure{}, "Error")); diff != "" {
		t.Errorf("POST %s/delete %s mismatch (-want +got):\n%s", filesPath, request, diff)
	}

	wantDeleted := []string{"test-experiment/files/a.log", "test-experiment/files/b.log"}
	if diff := cmp.Diff(wantDeleted, fake.deleted); diff != "" {
		t.Errorf("POST %s/delete %s files minimega deleted mismatch (-want +got):\n%s", filesPath, request, diff)
	}

	got := events()

	for path, outcome := range map[string]string{
		"a.log": "success", "b.log": "success", "missing.log": "failure", "../x": "failure",
	} {
		e := wantAudit(t, got, "file-delete", outcome, map[string]any{"user.name": "alice", "file.path": path})
		if outcome == "failure" && e != nil && e["error.message"] == nil {
			t.Errorf("POST %s/delete audit failure of %s has no error.message: %v", filesPath, path, e)
		}
	}

	if n := len(got); n != 4 {
		t.Errorf("POST %s/delete %s logged %d audit events, want one per path: %v", filesPath, request, n, got)
	}
}

func TestDeleteExperimentFilesNeedsPaths(t *testing.T) {
	useTestFiles(t)

	for _, body := range []string{``, `{}`, `{"paths":[]}`, `not json`} {
		if resp := requestFiles(t, http.MethodPost, "/delete", body, "delete"); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s/delete %q = %d, want %d", filesPath, body, resp.StatusCode, http.StatusBadRequest)
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
		t.Fatalf("POST %s/download = %d, want %d: %s", filesPath, resp.StatusCode, http.StatusOK, body)
	}

	for k, want := range map[string]string{
		"Content-Type":        "application/zip",
		"Content-Disposition": "attachment; filename=test-experiment-files.zip",
		// built as it is sent: never resumable
		"Accept-Ranges": "none",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("POST %s/download %s = %q, want %q", filesPath, k, got, want)
		}
	}

	want := map[string]string{"a.log": "alpha", "scorch/run-0/b.json": "{}"}
	if diff := cmp.Diff(want, zipEntries(t, body)); diff != "" {
		t.Errorf("POST %s/download zip entries mismatch (-want +got):\n%s", filesPath, diff)
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
		t.Fatalf("making b.log unreadable: %v", err)
	}

	events := captureAudit(t)

	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		server+filesPath+"/download", strings.NewReader(`{"paths":["a.log","b.log"]}`))
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}

	// The aborted response fails when its headers or its body are read.
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}

	if err == nil {
		t.Fatalf("POST %s/download with an unreadable b.log ended cleanly, want an aborted response", filesPath)
	}

	wantAudit(t, events(), "file-download", "success", map[string]any{"file.path": "a.log"})
	wantAudit(t, events(), "file-download", "failure", map[string]any{"phenix.file.count": 2})

	if n := countAudit(events(), "file-download", "success", map[string]any{"file.path": "b.log"}); n != 0 {
		t.Errorf("POST %s/download audited the unreadable b.log as downloaded %d times, want 0", filesPath, n)
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
				t.Errorf("POST %s/download (%s) = %d, want %d: %s", filesPath, name, resp.StatusCode, tc.wantCode, readBody(t, resp))
			}

			if got := resp.Header.Get("Content-Type"); got == "application/zip" {
				t.Errorf("POST %s/download (%s) Content-Type = %q, want no zip for a refusal", filesPath, name, got)
			}
		})
	}
}

// getExperimentFile requests one of test-experiment's files, by its path, as
// a user who may get them, with the given headers (name, value pairs).
func getExperimentFile(t *testing.T, method, path string, header ...string) *http.Response {
	t.Helper()

	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL

	return do(t, method, server+filesPath+"/"+filepath.Base(path)+"?path="+url.QueryEscape(path), "", header...)
}

// rewriteExperimentFile replaces one of test-experiment's files with new
// contents and a later modification time.
func rewriteExperimentFile(t *testing.T, path, contents string) {
	t.Helper()

	local := filepath.Join(common.PhenixBase, "images", "test-experiment", "files", path)

	if err := os.WriteFile(local, []byte(contents), 0o600); err != nil {
		t.Fatalf("rewriting %s: %v", path, err)
	}

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(local, later, later); err != nil {
		t.Fatalf("changing the times of %s: %v", path, err)
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

	const path = filesPath + "/out.bin?path=scorch/run-0/out.bin"

	resp := getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", path, resp.StatusCode, http.StatusOK)
	}

	if body := readBody(t, resp); body != data {
		t.Errorf("GET %s = %d bytes, want the file's %d", path, len(body), len(data))
	}

	etag := resp.Header.Get("ETag")
	if etag == "" || strings.HasPrefix(etag, "W/") {
		t.Fatalf("GET %s ETag = %q, want a strong one", path, etag)
	}

	for k, want := range map[string]string{
		"Accept-Ranges":       "bytes",
		"Content-Disposition": "attachment; filename=out.bin",
		"Content-Length":      strconv.Itoa(len(data)),
		"Content-Encoding":    "",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("GET %s %s = %q, want %q", path, k, got, want)
		}
	}

	resp = getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin", "Range", "bytes=40000-", "If-Range", etag)
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("GET %s from byte 40000 = %d, want %d", path, resp.StatusCode, http.StatusPartialContent)
	}

	if body := readBody(t, resp); body != data[40000:] {
		t.Errorf("GET %s from byte 40000 = %d bytes, want the last %d", path, len(body), len(data)-40000)
	}

	// audited once, not per resumed range
	if n := downloads(); n != 1 {
		t.Errorf("GET %s and a resume audited %d downloads, want 1", path, n)
	}

	rewritten := strings.ToUpper(data)
	rewriteExperimentFile(t, "scorch/run-0/out.bin", rewritten)

	// the file changed: the whole new file, not its tail spliced onto the old
	resp = getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin", "Range", "bytes=40000-", "If-Range", etag)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s of a changed file from byte 40000 = %d, want %d", path, resp.StatusCode, http.StatusOK)
	}

	if body := readBody(t, resp); body != rewritten {
		t.Errorf("GET %s of a changed file from byte 40000 = %d bytes, want the whole new file", path, len(body))
	}
}

func TestGetExperimentFileHead(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha"})

	const path = filesPath + "/a.log?path=a.log"

	resp := getExperimentFile(t, http.MethodHead, "a.log")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD %s = %d, want %d", path, resp.StatusCode, http.StatusOK)
	}

	if resp.Header.Get("ETag") == "" {
		t.Errorf("HEAD %s ETag is empty, want one", path)
	}

	if got := resp.Header.Get("Content-Length"); got != "5" {
		t.Errorf("HEAD %s Content-Length = %q, want 5", path, got)
	}

	if body := readBody(t, resp); body != "" {
		t.Errorf("HEAD %s body = %q, want none", path, body)
	}
}

// The Files tab's viewer gets the file inline, compressed, and revalidates a
// file it has shown before.
func TestGetExperimentFileViewerIsInline(t *testing.T) {
	text := strings.Repeat("phenix log line\n", 256)
	useExperimentFilesDir(t, map[string]string{"a.log": text})

	const path = filesPath + "/a.log?path=a.log"

	resp := getExperimentFile(t, http.MethodGet, "a.log", "Accept", "text/plain", "Accept-Encoding", "gzip")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s for the viewer = %d, want %d", path, resp.StatusCode, http.StatusOK)
	}

	if body := readBody(t, resp); body != text {
		t.Errorf("GET %s for the viewer = %q, want %q", path, body, text)
	}

	for k, want := range map[string]string{
		"Content-Disposition": "",
		"Content-Type":        "text/plain; charset=utf-8",
		"Content-Encoding":    "gzip",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("GET %s for the viewer %s = %q, want %q", path, k, got, want)
		}
	}

	// gzipped, so no longer byte for byte the file a strong ETag names
	etag := resp.Header.Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("GET %s for the viewer ETag = %q, want a weak one", path, etag)
	}

	resp = getExperimentFile(t, http.MethodGet, "a.log", "Accept", "text/plain", "If-None-Match", etag)
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("GET %s for the viewer If-None-Match %s = %d, want %d", path, etag, resp.StatusCode, http.StatusNotModified)
	}
}

func TestGetExperimentFileViewerRefusesLargeFiles(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"big.log": strings.Repeat("x", maxViewBytes+1)})

	const path = filesPath + "/big.log?path=big.log"

	resp := getExperimentFile(t, http.MethodGet, "big.log", "Accept", "text/plain")
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("GET %s for the viewer = %d, want %d", path, resp.StatusCode, http.StatusRequestEntityTooLarge)
	}

	if body := readBody(t, resp); !strings.Contains(body, "download it") {
		t.Errorf("GET %s for the viewer = %q, want a hint to download it", path, body)
	}

	// downloading it is fine
	resp = getExperimentFile(t, http.MethodHead, "big.log")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD %s = %d, want %d", path, resp.StatusCode, http.StatusOK)
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
			useCapturingExperiment(t)
			useExperimentFilesDir(t, map[string]string{"live.pcap": "pcap", "scorch/run-0/a.log": "a"})
			useTestFiles(t, "live.pcap", "scorch/run-0/a.log")

			resp := getExperimentFile(t, http.MethodGet, tc.path)
			if resp.StatusCode != tc.wantCode {
				t.Errorf("GET %s/<name>?path=%s = %d, want %d: %s", filesPath, tc.path, resp.StatusCode, tc.wantCode, readBody(t, resp))
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
	getExperimentFile(t, http.MethodHead, "a.log")

	if got := events(); len(got) != 0 {
		t.Errorf("HEAD %s/a.log?path=a.log audit events = %v, want none", filesPath, got)
	}
}

// A file's checksum, once known, also comes with every download of it, so a
// resumed download can be verified.
func TestGetExperimentFileChecksum(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"scorch/run-0/out.bin": "contents"})

	const (
		path     = filesPath + "/out.bin?path=scorch/run-0/out.bin"
		checksum = filesPath + "/out.bin/checksum?path=scorch/run-0/out.bin"
	)

	resp := getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin")
	if digest := resp.Header.Get("Repr-Digest"); digest != "" {
		t.Errorf("GET %s before any checksum Repr-Digest = %q, want none", path, digest)
	}

	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL
	resp = do(t, http.MethodGet, server+checksum, "")

	// a file this small is hashed before the answer
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d: %s", checksum, resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	var body util.Checksum
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET %s did not answer with a checksum: %v", checksum, err)
	}

	sum := sha256.Sum256([]byte("contents"))

	wantSum := util.Checksum{Path: "scorch/run-0/out.bin", Size: 8, SHA256: hex.EncodeToString(sum[:])}
	if diff := cmp.Diff(wantSum, body, cmpopts.IgnoreFields(util.Checksum{}, "Modified", "ETag")); diff != "" {
		t.Errorf("GET %s mismatch (-want +got):\n%s", checksum, diff)
	}

	want := "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"

	resp = getExperimentFile(t, http.MethodGet, "scorch/run-0/out.bin", "Range", "bytes=4-")
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("GET %s from byte 4 = %d, want %d", path, resp.StatusCode, http.StatusPartialContent)
	}

	if got := resp.Header.Get("Repr-Digest"); got != want {
		t.Errorf("GET %s from byte 4 Repr-Digest = %q, want %q", path, got, want)
	}

	if got := resp.Header.Get("ETag"); got != body.ETag {
		t.Errorf("GET %s ETag = %q, want the checksum's %q", path, got, body.ETag)
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
			useCapturingExperiment(t)
			useExperimentFilesDir(t, map[string]string{"live.pcap": "pcap", "a.log": "a"})
			useTestFiles(t, "live.pcap", "a.log")

			events := captureAudit(t)

			resp := requestFiles(t, http.MethodGet, "/x/checksum?path="+url.QueryEscape(tc.path), "", tc.verb)
			if resp.StatusCode != tc.wantCode {
				t.Errorf("GET %s/x/checksum?path=%s as a files %s role = %d, want %d: %s",
					filesPath, tc.path, tc.verb, resp.StatusCode, tc.wantCode, readBody(t, resp))
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

	resp := do(t, http.MethodPost, server+filesPath+"/archive", body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s/archive %s = %d, want %d: %s", filesPath, body, resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("POST %s/archive %s did not answer with a job: %v", filesPath, body, err)
	}

	if loc := resp.Header.Get("Location"); loc != "/api/v1/jobs/"+started.ID {
		t.Errorf("POST %s/archive Location = %q, want %q", filesPath, loc, "/api/v1/jobs/"+started.ID)
	}

	return started
}

// zipEntries reads a zip archive's entries.
func zipEntries(t *testing.T, body string) map[string]string {
	t.Helper()

	archive, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}

	got := map[string]string{}

	for _, f := range archive.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening zip entry %s: %v", f.Name, err)
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

	want := job.Job{
		Kind:       "files.archive",
		Owner:      "alice",
		Experiment: "test-experiment",
		Cancelable: true,
		Progress:   job.Progress{BytesTotal: int64(len("alpha{}"))},
		Items:      []job.Item{{Name: "a.log"}, {Name: "scorch/run-0/b.json"}},
	}
	if diff := cmp.Diff(want, started, startedJobOpts); diff != "" {
		t.Errorf("POST %s/archive job mismatch (-want +got):\n%s", filesPath, diff)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusSucceeded {
		t.Fatalf("archive job status = %s, want %s", j.Status, job.StatusSucceeded)
	}

	artifact := &job.Artifact{Name: "test-experiment-files.zip"}
	if diff := cmp.Diff(artifact, j.Artifact, cmpopts.IgnoreFields(job.Artifact{}, "Size", "Path")); diff != "" {
		t.Fatalf("archive job artifact mismatch (-want +got):\n%s", diff)
	}

	artifactPath := "/api/v1/jobs/" + j.ID + "/artifact"

	resp := do(t, http.MethodGet, server+artifactPath, "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", artifactPath, resp.StatusCode, http.StatusOK)
	}

	if got := resp.Header.Get("Content-Type"); got != "application/zip" {
		t.Errorf("GET %s Content-Type = %q, want application/zip", artifactPath, got)
	}

	wantEntries := map[string]string{"a.log": "alpha", "scorch/run-0/b.json": "{}"}
	if diff := cmp.Diff(wantEntries, zipEntries(t, body)); diff != "" {
		t.Errorf("GET %s zip entries mismatch (-want +got):\n%s", artifactPath, diff)
	}

	// each file is audited as downloaded when the archive is, once however
	// many requests resuming it take
	resume := do(t, http.MethodGet, server+artifactPath, "", "Range", "bytes=5-")
	if resume.StatusCode != http.StatusPartialContent {
		t.Errorf("GET %s from byte 5 = %d, want %d", artifactPath, resume.StatusCode, http.StatusPartialContent)
	}

	got := events()
	for _, path := range []string{"a.log", "scorch/run-0/b.json"} {
		if n := countAudit(got, "file-download", "success", map[string]any{
			"user.name": "alice", "phenix.experiment.name": "test-experiment", "file.path": path,
			"phenix.job.id": j.ID, "url.path": artifactPath,
		}); n != 1 {
			t.Errorf("GET %s twice audited %s %d times, want 1: %v", artifactPath, path, n, got)
		}
	}

	if n := len(got); n != 2 {
		t.Errorf("GET %s twice logged %d audit events, want 2: %v", artifactPath, n, got)
	}
}

// An archive downloads only for a role that may still get the files; refusals are audited: the owner after losing the
// permission, a user who may get the job but not the files, and one who may not see the job.
func TestArchiveDownloadRechecksPermission(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha"})
	useTestFiles(t)

	registry := useJobs(t)
	owner := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL

	j := waitJob(t, registry, startArchive(t, owner, `{"paths":["a.log"]}`).ID)
	if j.Artifact == nil {
		t.Fatalf("archive job of a.log ended %s with no artifact, want one", j.Status)
	}

	artifactPath := "/api/v1/jobs/" + j.ID + "/artifact"

	for name, tc := range map[string]struct {
		user string
		role rbac.Role
		want int
	}{
		"owner without files get": {"alice", testRole("experiments", "get", "test-experiment"), http.StatusForbidden},
		"jobs viewer without it":  {"bob", testRole("jobs", "get", "test-experiment"), http.StatusForbidden},
		"user who may not see it": {"carol", testRole("experiments", "get", "test-experiment"), http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			server := serveAs(t, tc.user, tc.role).URL
			events := captureAudit(t)

			resp := do(t, http.MethodGet, server+artifactPath, "")
			if body := readBody(t, resp); resp.StatusCode != tc.want {
				t.Errorf("GET %s as %s = %d, want %d: %s", artifactPath, tc.user, resp.StatusCode, tc.want, body)
			}

			wantDenied(t, events(), "file-download")
		})
	}

	both := combinedRole(testRole("jobs", "get", "test-experiment"), testRole("experiments/files", "get", "test-experiment"))
	if resp := do(t, http.MethodGet, serveAs(t, "bob", both).URL+artifactPath, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s as bob, who may get the job and the files, = %d, want %d", artifactPath, resp.StatusCode, http.StatusOK)
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

	if j.Status != job.StatusPartial {
		t.Errorf("archive job of a.log and remote.log status = %s, want %s", j.Status, job.StatusPartial)
	}

	artifact := &job.Artifact{Name: "test-experiment-files-incomplete.zip", Incomplete: true}
	if diff := cmp.Diff(artifact, j.Artifact, cmpopts.IgnoreFields(job.Artifact{}, "Size", "Path")); diff != "" {
		t.Fatalf("archive job of a.log and remote.log artifact mismatch (-want +got):\n%s", diff)
	}

	wantItems := []job.Item{{Name: "a.log", Status: job.ItemSucceeded}, {Name: "remote.log", Status: job.ItemFailed}}
	if diff := cmp.Diff(wantItems, j.Items, cmpopts.IgnoreFields(job.Item{}, "Error", "Retryable", "Result")); diff != "" {
		t.Errorf("archive job of a.log and remote.log items mismatch (-want +got):\n%s", diff)
	}

	if len(j.Items) == 2 && !strings.Contains(j.Items[1].Error, "remote.log") {
		t.Errorf("archive job remote.log item error = %q, want it to name remote.log", j.Items[1].Error)
	}

	artifactPath := "/api/v1/jobs/" + j.ID + "/artifact"

	got := zipEntries(t, readBody(t, do(t, http.MethodGet, server+artifactPath, "")))
	if missing := got["MISSING.txt"]; !strings.Contains(missing, "remote.log: ") {
		t.Errorf("GET %s MISSING.txt = %q, want it to list remote.log", artifactPath, missing)
	}

	delete(got, "MISSING.txt")

	if diff := cmp.Diff(map[string]string{"a.log": "alpha"}, got); diff != "" {
		t.Errorf("GET %s zip entries besides MISSING.txt mismatch (-want +got):\n%s", artifactPath, diff)
	}

	// only the file archived reached the user
	downloaded := events()
	wantAudit(t, downloaded, "file-download", "success", map[string]any{"file.path": "a.log", "phenix.job.id": j.ID})

	if n := countAudit(downloaded, "file-download", "success", nil); n != 1 {
		t.Errorf("GET %s audited %d files as downloaded, want 1: %v", artifactPath, n, downloaded)
	}

	// with nothing archived, the job fails without an artifact, audited as a
	// failed download
	j = waitJob(t, registry, startArchive(t, server, `{"paths":["remote.log"]}`).ID)
	if j.Status != job.StatusFailed {
		t.Errorf("archive job of only remote.log status = %s, want %s", j.Status, job.StatusFailed)
	}

	if j.Artifact != nil {
		t.Errorf("archive job of only remote.log artifact = %+v, want none", j.Artifact)
	}

	waitAudit(t, events, "file-download", "failure", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "test-experiment", "phenix.job.id": j.ID,
		"phenix.file.count": 1,
	})
}

// A file that grew since the plan keeps its planned size in the archive and
// counts as archived, but the archive is incomplete and MISSING.txt says so.
func TestArchiveFilesCutsShortAGrownFile(t *testing.T) {
	useExperimentFilesDir(t, map[string]string{"a.log": "alpha", "b.log": "beta"})
	useTestFiles(t)

	registry := useJobs(t)
	server := serveAs(t, "alice", testRole("experiments/files", "get", "test-experiment")).URL
	events := captureAudit(t)

	plan, err := experiment.PlanZip("test-experiment", []string{"a.log", "b.log"}, experiment.ZipLimits{
		MaxFiles: 2, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("experiment.PlanZip(test-experiment, [a.log b.log]) error = %v", err)
	}

	local := filepath.Join(common.PhenixBase, "images", "test-experiment", "files", "a.log")
	if err := os.WriteFile(local, []byte("alpha and more"), 0o600); err != nil {
		t.Fatalf("growing a.log: %v", err)
	}

	started, err := registry.Start(job.Spec{
		Kind: archiveKind, Owner: "alice", Experiment: "test-experiment", Items: plan.Paths,
		Run: func(ctx context.Context, run *job.Run) error { return archiveFiles(ctx, run, plan) },
	})
	if err != nil {
		t.Fatalf("Registry.Start(archive of a.log, b.log) error = %v", err)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusSucceeded {
		t.Errorf("archive job of a grown a.log status = %s, want %s", j.Status, job.StatusSucceeded)
	}

	artifact := &job.Artifact{Name: "test-experiment-files-incomplete.zip", Incomplete: true}
	if diff := cmp.Diff(artifact, j.Artifact, cmpopts.IgnoreFields(job.Artifact{}, "Size", "Path")); diff != "" {
		t.Fatalf("archive job of a grown a.log artifact mismatch (-want +got):\n%s", diff)
	}

	artifactPath := "/api/v1/jobs/" + j.ID + "/artifact"

	got := zipEntries(t, readBody(t, do(t, http.MethodGet, server+artifactPath, "")))

	const cutShort = "a.log: changed while archiving; only the first 5 bytes were included"
	if missing := got["MISSING.txt"]; !strings.Contains(missing, cutShort) {
		t.Errorf("GET %s MISSING.txt = %q, want it to contain %q", artifactPath, missing, cutShort)
	}

	delete(got, "MISSING.txt")

	if diff := cmp.Diff(map[string]string{"a.log": "alpha", "b.log": "beta"}, got); diff != "" {
		t.Errorf("GET %s zip entries besides MISSING.txt mismatch (-want +got):\n%s", artifactPath, diff)
	}

	// its bytes reached the user, so it is audited as downloaded
	if n := countAudit(events(), "file-download", "success", nil); n != 2 {
		t.Errorf("GET %s audited %d files as downloaded, want 2", artifactPath, n)
	}
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

	cancel := "/api/v1/jobs/" + started.ID + "/cancel"
	if resp := do(t, http.MethodPost, server+cancel, ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s = %d, want %d", cancel, resp.StatusCode, http.StatusAccepted)
	}

	close(release)

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusCanceled {
		t.Errorf("canceled archive job status = %s, want %s", j.Status, job.StatusCanceled)
	}

	if j.Artifact != nil {
		t.Errorf("canceled archive job artifact = %+v, want none", j.Artifact)
	}

	entries, err := os.ReadDir(filepath.Join(dir, started.ID))
	if err != nil {
		t.Fatalf("reading the canceled job's artifact folder: %v", err)
	}

	for _, e := range entries {
		t.Errorf("canceled archive job left %s, want nothing", e.Name())
	}

	waitAudit(t, events, "file-download", "failure", map[string]any{
		"user.name": "alice", "phenix.job.id": j.ID, "error.message": "canceled",
	})
}
