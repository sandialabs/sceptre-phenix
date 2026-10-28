package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/web/audit"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	netflowTestID    = "20261028T153012.345Z"
	netflowTestLines = "2026-10-28T15:30:12Z\t1s\t6\t10.0.0.1:1234\t<->\t10.0.0.2:443\t2\t2048\n" +
		"garbage\n"
)

// useNetflowCaptures stores test-experiment with one interrupted netflow
// capture, and returns the URL of its captures as a role that may get them.
func useNetflowCaptures(t *testing.T) string {
	t.Helper()

	useTestExperiment(t)
	useExperimentFilesDir(t, map[string]string{"netflow/" + netflowTestID + ".txt": netflowTestLines})

	role := testRole("experiments/netflow", "get", "test-experiment")

	return serveAs(t, "alice", role).URL + "/api/v1/experiments/test-experiment/netflow/captures"
}

// The capture list names each capture with its state, size and path; an
// interrupted capture's flows and stop time are null.
func TestGetNetflowCaptures(t *testing.T) {
	url := useNetflowCaptures(t)

	resp := do(t, http.MethodGet, url, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", url, resp.StatusCode, readBody(t, resp))
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(readBody(t, resp)), &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{"captures": []any{map[string]any{
		"id": netflowTestID, "started": "2026-10-28T15:30:12.345Z", "stopped": nil, "state": "interrupted",
		"size": float64(len(netflowTestLines)), "flows": nil, "truncated": false, "path": "netflow/" + netflowTestID + ".txt",
	}}}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET %s mismatch (-want +got):\n%s", url, diff)
	}

	other := serveAs(t, "bob", testRole("experiments/netflow", "get", "other")).URL +
		"/api/v1/experiments/test-experiment/netflow/captures"
	if resp := do(t, http.MethodGet, other, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET %s without experiments/netflow get = %d, want 403", other, resp.StatusCode)
	}

	unknown := serveAs(t, "carol", testRole("experiments/netflow", "get", "*")).URL +
		"/api/v1/experiments/none/netflow/captures"
	if resp := do(t, http.MethodGet, unknown, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s = %d, want 404", unknown, resp.StatusCode)
	}
}

// Text sends the recorded lines, resumably; CSV and JSONL convert the flows,
// skipping the lines that do not parse, and cannot be resumed.
func TestDownloadNetflowCapture(t *testing.T) {
	url := useNetflowCaptures(t) + "/" + netflowTestID

	tests := map[string]struct {
		method, query string
		header        []string
		status        int
		body          string
		headers       map[string]string
	}{
		"text": {
			method: http.MethodGet, status: http.StatusOK, body: netflowTestLines,
			headers: map[string]string{
				"Content-Type":        "text/plain; charset=utf-8",
				"Content-Disposition": `attachment; filename=test-experiment-netflow-20261028T153012.345Z.txt`,
				"Accept-Ranges":       "bytes",
			},
		},
		"text range": {
			method: http.MethodGet, query: "?format=text", header: []string{"Range", "bytes=0-9"},
			status: http.StatusPartialContent, body: netflowTestLines[:10],
		},
		"text head": {
			method: http.MethodHead, status: http.StatusOK,
			headers: map[string]string{"Content-Type": "text/plain; charset=utf-8", "Accept-Ranges": "bytes"},
		},
		"csv": {
			method: http.MethodGet, query: "?format=csv", status: http.StatusOK,
			body: "event.start,event.end,event.duration,source.ip,source.port,destination.ip,destination.port," +
				"network.transport,network.iana_number,network.packets,network.bytes\n" +
				"2026-10-28T15:30:12Z,2026-10-28T15:30:13Z,1000000000,10.0.0.1,1234,10.0.0.2,443,tcp,6,2,2048\n",
			headers: map[string]string{
				"Content-Type":           "text/csv; charset=utf-8; header=present",
				"Content-Disposition":    `attachment; filename=test-experiment-netflow-20261028T153012.345Z.csv`,
				"Accept-Ranges":          "none",
				"Cache-Control":          "no-store",
				"X-Content-Type-Options": "nosniff",
			},
		},
		"jsonl ignores a range": {
			method: http.MethodGet, query: "?format=jsonl", header: []string{"Range", "bytes=0-9"}, status: http.StatusOK,
			body: `{"event.start":"2026-10-28T15:30:12Z","event.end":"2026-10-28T15:30:13Z",` +
				`"event.duration":1000000000,"source.ip":"10.0.0.1","source.port":1234,"destination.ip":"10.0.0.2",` +
				`"destination.port":443,"network.transport":"tcp","network.iana_number":"6","network.packets":2,` +
				`"network.bytes":2048}` + "\n",
			headers: map[string]string{"Content-Type": "application/x-ndjson", "Accept-Ranges": "none"},
		},
		"jsonl head": {
			method: http.MethodHead, query: "?format=jsonl", status: http.StatusOK,
			headers: map[string]string{"Content-Type": "application/x-ndjson", "Accept-Ranges": "none"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp := do(t, tt.method, url+tt.query, "", tt.header...)
			body := readBody(t, resp)

			if resp.StatusCode != tt.status {
				t.Fatalf("%s %s = %d, want %d: %s", tt.method, tt.query, resp.StatusCode, tt.status, body)
			}

			if body != tt.body {
				t.Errorf("%s %s body = %q, want %q", tt.method, tt.query, body, tt.body)
			}

			for k, v := range tt.headers {
				if got := resp.Header.Get(k); got != v {
					t.Errorf("%s %s header %s = %q, want %q", tt.method, tt.query, k, got, v)
				}
			}
		})
	}
}

// A bad format or ID is refused, and a missing capture is not found, on GET
// and HEAD alike.
func TestDownloadNetflowCaptureErrors(t *testing.T) {
	base := useNetflowCaptures(t)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for path, want := range map[string]int{
			"/" + netflowTestID + "?format=pcap":     http.StatusBadRequest,
			"/latest":                                http.StatusBadRequest,
			"/" + netflowTestID + "-2":               http.StatusBadRequest,
			"/20200101T000000.000Z":                  http.StatusNotFound,
			"/20200101T000000.000Z?format=csv":       http.StatusNotFound,
			"/" + netflowTestID + "?format=CSV":      http.StatusBadRequest,
			"/" + netflowTestID + "?format=jsonl&x=": http.StatusOK,
		} {
			if resp := do(t, method, base+path, ""); resp.StatusCode != want {
				t.Errorf("%s %s = %d, want %d", method, path, resp.StatusCode, want)
			}
		}
	}
}

// An export that fails before sending anything answers 500; one that fails
// after its headers aborts the response, so the client sees it incomplete.
// Both are audited as failures with the flows and skipped lines so far.
func TestNetflowExportFailure(t *testing.T) {
	const rows = 1000 // enough CSV to flush the export's buffer

	errRead := errors.New("reading the capture failed")

	tests := map[string]struct {
		src            io.Reader
		abort          bool
		flows, skipped int
	}{
		"before the headers": {src: iotest.ErrReader(errRead)},
		"after the headers": {
			src:   io.MultiReader(strings.NewReader(strings.Repeat(netflowTestLines, rows)), iotest.ErrReader(errRead)),
			abort: true, flows: rows, skipped: rows,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var (
				events = captureAudit(t)
				w      = httptest.NewRecorder()
				r      = httptest.NewRequest(http.MethodGet, "/", nil)
				d      = util.Download{Path: "", Name: "x.csv", ContentType: "text/csv", MaxSize: 0}
				fields = audit.Fields{Experiment: "exp", File: "netflow/" + netflowTestID + ".txt"}
				err    error
				panicV any
			)

			func() {
				defer func() { panicV = recover() }()

				err = sendNetflowExport(w, r, tt.src, d, experiment.NetflowFormatCSV, fields)
			}()

			if tt.abort {
				if panicV != http.ErrAbortHandler { //nolint:errorlint // the exact sentinel net/http checks for
					t.Errorf("sendNetflowExport panicked with %v, want http.ErrAbortHandler", panicV)
				}

				if w.Code != http.StatusOK || w.Body.Len() == 0 {
					t.Errorf("sendNetflowExport sent %d with %d bytes, want 200 and part of the export", w.Code, w.Body.Len())
				}
			} else {
				var werr *weberror.WebError
				if panicV != nil || !errors.As(err, &werr) || werr.Status != http.StatusInternalServerError {
					t.Errorf("sendNetflowExport = %v, panic %v; want a 500 error and no panic", err, panicV)
				}

				if w.Body.Len() != 0 {
					t.Errorf("sendNetflowExport sent %q before failing, want nothing", w.Body.String())
				}
			}

			event := wantAudit(t, events(), "netflow-download", "failure", map[string]any{
				"phenix.netflow.flows": tt.flows, "phenix.netflow.skipped": tt.skipped,
			})
			wantErrorMessage(t, event, errRead.Error())
		})
	}
}
