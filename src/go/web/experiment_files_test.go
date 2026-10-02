package web

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"phenix/util/file"
	"phenix/util/mm"
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
	}{
		"delete one":       {method: http.MethodDelete, path: "/a.log?path=a.log", verb: "get"},
		"delete several":   {method: http.MethodPost, path: "/delete", body: `{"paths":["a.log"]}`, verb: "get"},
		"download several": {method: http.MethodPost, path: "/download", body: `{"paths":["a.log"]}`, verb: "list"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := useTestFiles(t, "a.log")

			resp := requestFiles(t, tc.method, tc.path, tc.body, tc.verb)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", resp.StatusCode, readBody(t, resp))
			}

			if len(fake.deleted) != 0 {
				t.Fatalf("deleted %v without permission", fake.deleted)
			}
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

			resp := requestFiles(t, http.MethodDelete, "/x?path="+url.QueryEscape(tc.path), "", "delete")
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, resp.StatusCode, readBody(t, resp))
			}

			if !slices.Equal(fake.deleted, tc.wantDeleted) {
				t.Fatalf("deleted %v, want %v", fake.deleted, tc.wantDeleted)
			}
		})
	}
}

func TestDeleteExperimentFilesReportsEachFailure(t *testing.T) {
	fake := useTestFiles(t, "a.log", "b.log")

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
