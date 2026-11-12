package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"
	"pgregory.net/rapid"

	"phenix/api/livecapture"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
	"phenix/web/capability"
	"phenix/web/rbac"
)

// useWebShark turns the webshark feature on, with test-experiment stored
// and minimega listing its captures.
func useWebShark(t *testing.T, captures ...mm.Capture) *testMM {
	t.Helper()

	useTestExperiment(t)

	fake := useTestMM(t, &testMM{captures: captures})

	original := o
	t.Cleanup(func() { o = original })

	o.features = map[string]bool{"webshark": true}
	o.websharkDir, o.basePath = t.TempDir(), "/"

	useCaptureGrants(t)

	return fake
}

// useCaptureGrants gives the test a registry of capture grants of its own,
// and ends its grants once the test ends.
func useCaptureGrants(t *testing.T) {
	t.Helper()

	original := captureGrants
	grants := capability.New(maxCaptureGrants, maxCaptureGrantsPerUser, nil)
	captureGrants = grants

	t.Cleanup(func() {
		captureGrants = original

		grants.RevokeWhere(func(capability.Grant) bool { return true })
	})
}

// liveCapture is a running capture of test-vm's interface 0, written to a
// file on the headnode.
type liveCapture struct {
	path string
}

// useLiveCapture has fake minimega capture test-vm's interface 0, to live.pcap
// in the experiment's files.
func useLiveCapture(t *testing.T, fake *testMM) *liveCapture {
	t.Helper()

	// where minimega writes it: the experiment's files directory
	c := &liveCapture{
		path: filepath.Join(t.TempDir(), "images", "test-experiment", "files", "live.pcap"),
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0o750); err != nil {
		t.Fatalf("making the folder of live.pcap: %v", err)
	}

	if err := os.WriteFile(c.path, nil, 0o600); err != nil {
		t.Fatalf("writing live.pcap: %v", err)
	}

	fake.captures = append(fake.captures, mm.Capture{VM: "test-vm", Interface: 0, Filepath: c.path})

	// stop following before the fakes it reads are put back
	t.Cleanup(livecapture.CloseAll)

	return c
}

func (c *liveCapture) write(t *testing.T, b ...[]byte) {
	t.Helper()

	f, err := os.OpenFile(c.path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening live.pcap: %v", err)
	}

	defer f.Close()

	if _, err := f.Write(bytes.Join(b, nil)); err != nil {
		t.Fatalf("appending to live.pcap: %v", err)
	}
}

// testPcapHeader is a pcap file's 24-byte global header.
func testPcapHeader() []byte {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b, 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(b[4:], 2)
	binary.LittleEndian.PutUint16(b[6:], 4)
	binary.LittleEndian.PutUint32(b[16:], 1600)
	binary.LittleEndian.PutUint32(b[20:], 1)

	return b
}

func testPcapRecord(n int) []byte {
	b := make([]byte, 16+n)
	binary.LittleEndian.PutUint32(b[8:], uint32(n))
	binary.LittleEndian.PutUint32(b[12:], uint32(n))

	return b
}

// sharkRole may read test-vm's captures.
func sharkRole() rbac.Role {
	return combinedRole(
		testRole("experiments/files", "get", "test-experiment"),
		testRole("vms/captures", "list", "test-experiment/test-vm"),
	)
}

// createSharkCapture requests a WebShark capture of test-experiment, returning the response
// and, on 200, the capture.
func createSharkCapture(t *testing.T, server, body string) (*http.Response, sharkCaptureResponse) {
	t.Helper()

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/webshark", body)

	var capture sharkCaptureResponse

	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&capture); err != nil {
			t.Fatalf("decoding POST webshark %s: %v", body, err)
		}
	}

	return resp, capture
}

// openInWebShark asks the server for a capture WebShark can open, failing the
// test if it gives none.
func openInWebShark(t *testing.T, server, body string) sharkCaptureResponse {
	t.Helper()

	resp, capture := createSharkCapture(t, server, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST webshark %s = %d, want %d: %s", body, resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	return capture
}

// WebShark gets the capture it is given the name of, and only that, with
// requests that carry no credentials.
func TestCreateWebSharkCaptureOfSavedFile(t *testing.T) {
	contents := string(append(testPcapHeader(), testPcapRecord(60)...))

	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"captures/router.pcap": contents, "notes.txt": "hi"})

	server := serveAs(t, "alice", sharkRole()).URL
	events := captureAudit(t)

	const body = `{"path":"captures/router.pcap"}`

	capture := openInWebShark(t, server, body)
	if capture.Live {
		t.Errorf("POST webshark %s live = true, want false", body)
	}

	if got, want := capture.Title, "captures/router.pcap"; got != want {
		t.Errorf("POST webshark %s title = %q, want %q", body, got, want)
	}

	if !strings.HasPrefix(capture.Capture, "router-") || !strings.HasSuffix(capture.Capture, ".pcap") {
		t.Fatalf("POST webshark %s capture = %q, want router-<random>.pcap", body, capture.Capture)
	}

	path := "/webshark/captures/" + capture.Capture

	resp := do(t, http.MethodGet, server+path, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, http.StatusOK)
	}

	if got := readBody(t, resp); got != contents {
		t.Errorf("GET %s = %d bytes, want the %d of captures/router.pcap", path, len(got), len(contents))
	}

	// a saved file does not change: resumable against its ETag
	if etag := resp.Header.Get("ETag"); etag == "" || strings.HasPrefix(etag, "W/") {
		t.Errorf("GET %s ETag = %q, want a strong one", path, etag)
	}

	download := "/webshark/json?method=download&capture=" + capture.Capture

	resp = do(t, http.MethodGet, server+download, "")
	if got, want := resp.Header.Get("Content-Disposition"), `attachment; filename=router.pcap`; got != want {
		t.Errorf("GET %s Content-Disposition = %q, want %q", download, got, want)
	}

	// another name, however close, is not the capture
	resp = do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture[1:], "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /webshark/captures/%s = %d, want %d", capture.Capture[1:], resp.StatusCode, http.StatusNotFound)
	}

	// WebShark reads it again as it likes: the view is audited once
	_ = readBody(t, do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture, ""))

	got := events()
	file := map[string]any{"user.name": "alice", "file.path": "captures/router.pcap", "phenix.capability.id": capture.ID}

	wantAudit(t, got, "capture-webshark-open", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "test-experiment", "file.path": "captures/router.pcap",
		"phenix.webshark.live": false,
	})

	for op, want := range map[string]int{"view": 1, "download": 1} {
		file["phenix.capability.op"] = op
		if n := countAudit(got, "capture-webshark-read", "success", file); n != want {
			t.Errorf("capture-webshark-read %s audit events = %d, want %d", op, n, want)
		}
	}
}

func TestCreateWebSharkCaptureRefuses(t *testing.T) {
	fake := useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"notes.txt": "hi"})
	useLiveCapture(t, fake)

	tests := map[string]struct {
		role rbac.Role
		body string
		want int
		// the audit event's outcome, "" for none
		wantOutcome string
	}{
		"no file permission": {
			testRole("experiments/files", "list", "test-experiment"), `{"path":"a.pcap"}`, http.StatusForbidden, "failure",
		},
		"no capture permission": {
			testRole("experiments/files", "get", "test-experiment"), `{"vm":"test-vm","interface":0}`,
			http.StatusForbidden, "failure",
		},
		"not a capture": {sharkRole(), `{"path":"notes.txt"}`, http.StatusBadRequest, ""},
		// the role may read files, wherever this one is
		"escaping path":      {sharkRole(), `{"path":"../other/a.pcap"}`, http.StatusBadRequest, "failure"},
		"missing file":       {sharkRole(), `{"path":"gone.pcap"}`, http.StatusNotFound, "failure"},
		"both":               {sharkRole(), `{"path":"a.pcap","vm":"test-vm","interface":0}`, http.StatusBadRequest, ""},
		"neither":            {sharkRole(), `{}`, http.StatusBadRequest, ""},
		"no interface":       {sharkRole(), `{"vm":"test-vm"}`, http.StatusBadRequest, ""},
		"not being captured": {sharkRole(), `{"vm":"test-vm","interface":1}`, http.StatusNotFound, "failure"},
	}

	useTestFiles(t) // lists no files

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server := serveAs(t, "alice", tt.role).URL
			events := captureAudit(t)

			resp, _ := createSharkCapture(t, server, tt.body)
			if resp.StatusCode != tt.want {
				t.Fatalf("POST webshark %s = %d, want %d: %s", tt.body, resp.StatusCode, tt.want, readBody(t, resp))
			}

			got := events()

			switch {
			case tt.want == http.StatusForbidden:
				wantDenied(t, got, "capture-webshark-open")
			case tt.wantOutcome != "":
				wantAudit(t, got, "capture-webshark-open", tt.wantOutcome, map[string]any{"user.name": "alice"})
			case len(got) != 0:
				t.Errorf("POST webshark %s audit events = %v, want none before the RBAC check", tt.body, got)
			}
		})
	}

	o.features = map[string]bool{}

	resp, _ := createSharkCapture(t, serveAs(t, "alice", sharkRole()).URL, `{"path":"a.pcap"}`)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("POST webshark without the webshark feature = %d, want %d", resp.StatusCode, http.StatusNotImplemented)
	}
}

// A file whose name only resembles the one a capture writes is a saved file.
func TestCreateWebSharkCaptureOfFileLikeACapture(t *testing.T) {
	useWebShark(t, mm.Capture{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/xa.pcap"})
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	if capture := openInWebShark(t, serveAs(t, "alice", sharkRole()).URL, `{"path":"a.pcap"}`); capture.Live {
		t.Errorf("POST webshark {\"path\":\"a.pcap\"} = %+v, want the saved file", capture)
	}
}

func TestCreateWebSharkCaptureOfRunningCapture(t *testing.T) {
	fake := useWebShark(t)
	live := useLiveCapture(t, fake)
	header, one, two := testPcapHeader(), testPcapRecord(60), testPcapRecord(1500)
	live.write(t, header, one, two[:100])

	server := serveAs(t, "alice", sharkRole()).URL

	// the file being captured opens live, found with one listing of captures
	audited := captureAudit(t)

	capture := openInWebShark(t, server, `{"path":"live.pcap"}`)
	if !capture.Live {
		t.Fatalf("POST webshark {\"path\":\"live.pcap\"} = %+v, want a live capture", capture)
	}

	if !strings.HasPrefix(capture.Capture, "live-") {
		t.Errorf("POST webshark {\"path\":\"live.pcap\"} capture = %q, want live-<random>", capture.Capture)
	}

	wantAudit(t, audited(), "capture-webshark-open", "success", map[string]any{
		"user.name": "alice", "phenix.vm.name": "test-vm", "phenix.vm.interface": 0, "phenix.webshark.live": true,
	})

	if n := fake.captureListings.Load(); n != 1 {
		t.Errorf("POST webshark of a live capture listed captures %d times, want 1", n)
	}

	followLiveCapture(t)

	// only whole records are served
	whole := len(header) + len(one)

	path := "/webshark/captures/" + capture.Capture

	resp := do(t, http.MethodGet, server+path, "")
	if body := readBody(t, resp); len(body) != whole {
		t.Errorf("GET %s = %d bytes, want the %d of whole records", path, len(body), whole)
	}

	// still growing: no validator a resumed download could splice against
	for _, k := range []string{"ETag", "Last-Modified"} {
		if got := resp.Header.Get(k); got != "" {
			t.Errorf("GET %s of a running capture %s = %q, want none", path, k, got)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel() // before closing the server, which waits for the watch to end

	events := sharkEvents(ctx, t, server+"/webshark/watch?capture="+capture.Capture)

	noMtime := cmpopts.IgnoreMapEntries(func(k string, _ any) bool { return k == "mtime" })

	select {
	case ev := <-events:
		if diff := cmp.Diff(map[string]any{"kind": "init", "size": float64(whole)}, ev, noMtime); diff != "" {
			t.Errorf("first /webshark/watch event mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("/webshark/watch sent no event within 5s, want an init")
	}

	live.write(t, two[100:])

	select {
	case ev := <-events:
		if diff := cmp.Diff(map[string]any{"kind": "append", "size": float64(whole + len(two))}, ev, noMtime); diff != "" {
			t.Errorf("/webshark/watch event after the capture grew mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Error("/webshark/watch sent no event within 5s of the capture growing, want an append")
	}
}

// sharkEvents reads the capture-changed events of a watch.
func sharkEvents(ctx context.Context, t *testing.T, url string) <-chan map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext(GET, %s) error = %v", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("GET %s = %d with Content-Type %q, want text/event-stream", url, resp.StatusCode, got)
	}

	events := make(chan map[string]any, 8)

	go func() {
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		event := ""

		for scanner.Scan() {
			line := scanner.Text()

			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && event == "capture-changed":
				var data map[string]any
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data) == nil {
					events <- data
				}
			}
		}
	}()

	return events
}

// writeSharkBuild writes a WebShark UI build of the given files.
func writeSharkBuild(t *testing.T, files map[string][]byte) string {
	t.Helper()

	dir := t.TempDir()

	for name, b := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o750); err != nil {
			t.Fatalf("making the folder of WebShark build file %s: %v", name, err)
		}

		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatalf("writing WebShark build file %s: %v", name, err)
		}
	}

	return dir
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()

	gz, err := gzipBytes(b)
	if err != nil {
		t.Fatalf("gzipBytes() error = %v", err)
	}

	return gz
}

func TestWebSharkHandlerServesBuild(t *testing.T) {
	wasm := bytes.Repeat([]byte("wasm"), 1000)
	gz := gzipped(t, wasm)

	handler := WebSharkHandler(writeSharkBuild(t, map[string][]byte{
		"index.html":                       []byte(`<base href="/webshark/">`),
		"main.js":                          []byte("main"),
		"assets/wiregasm/wiregasm.wasm.gz": gz,
		"enable-threads.js":                []byte("navigator.serviceWorker.register(src)"),
	}), "/")

	for _, target := range []string{"/webshark/", "/webshark/embed?capture=a&embed=1", "/webshark/embed/packets"} {
		resp := get(t, handler, target, "")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want %d", target, resp.StatusCode, http.StatusOK)
		}

		if body := readBody(t, resp); !strings.Contains(body, "<base") {
			t.Errorf("GET %s = %q, want the app's index.html", target, body)
		}
	}

	resp := get(t, handler, "/webshark/main.js", "")
	if got, want := readBody(t, resp), "main"; got != want {
		t.Errorf("GET /webshark/main.js = %q, want %q", got, want)
	}

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("GET /webshark/main.js Content-Type = %q, want text/javascript", got)
	}

	const wasmPath = "/webshark/assets/wiregasm/wiregasm.wasm"

	// the precompressed copy, sent as it is to a client that takes gzip
	resp = get(t, handler, wasmPath, "gzip, br")
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("GET %s with gzip Content-Encoding = %q, want gzip", wasmPath, got)
	}

	if got, want := resp.Header.Get("Content-Type"), "application/wasm"; got != want {
		t.Errorf("GET %s with gzip Content-Type = %q, want %q", wasmPath, got, want)
	}

	if body, _ := io.ReadAll(resp.Body); !bytes.Equal(body, gz) {
		t.Errorf("GET %s with gzip = %d bytes, want the %d of wiregasm.wasm.gz as it is", wasmPath, len(body), len(gz))
	}

	resp = get(t, handler, wasmPath, "")
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("GET %s without gzip Content-Encoding = %q, want none", wasmPath, got)
	}

	if body, _ := io.ReadAll(resp.Body); !bytes.Equal(body, wasm) {
		t.Errorf("GET %s without gzip = %d bytes, want the %d of the decompressed file", wasmPath, len(body), len(wasm))
	}

	// WebShark's service worker is replaced by one that removes itself
	resp = get(t, handler, "/webshark/enable-threads.js", "gzip, br")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /webshark/enable-threads.js = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if body := readBody(t, resp); !strings.Contains(body, "unregister()") || strings.Contains(body, "register(src)") {
		t.Errorf("GET /webshark/enable-threads.js = %q, want a service worker that unregisters itself", body)
	}

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("GET /webshark/enable-threads.js Content-Type = %q, want text/javascript", got)
	}

	for _, target := range []string{"/webshark/missing.js", "/webshark/../main.js", "/other/main.js"} {
		if resp := get(t, handler, target, "gzip, br"); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d", target, resp.StatusCode, http.StatusNotFound)
		}
	}
}

func TestWebSharkHandlerRebasesUnderBasePath(t *testing.T) {
	script := "const api={apiUrl:\"/webshark/\"},w=`/webshark/watch?capture=${c}`," +
		"s=`${origin}/webshark/${state}`,f='/webshark/captures/'+n," +
		"home=\"https://github.com/QXIP/webshark/\";"
	want := "const api={apiUrl:\"/proxy/phenix/webshark/\"},w=`/proxy/phenix/webshark/watch?capture=${c}`," +
		"s=`${origin}/proxy/phenix/webshark/${state}`,f='/proxy/phenix/webshark/captures/'+n," +
		"home=\"https://github.com/QXIP/webshark/\";"

	dir := writeSharkBuild(t, map[string][]byte{
		"index.html":     []byte(`<base href="/webshark/">`),
		"main.js":        []byte(script),
		"worker.js.gz":   gzipped(t, []byte(script)),
		"plain.js":       []byte("nothing to move"),
		"data.wasm":      []byte(`"/webshark/"`),
		"styles.css":     []byte(`a{background:url("/webshark/assets/a.png")}`),
		"assets/f.woff2": []byte(`'/webshark/'`),
	})

	handler := WebSharkHandler(dir, "/proxy/phenix/")

	for _, encoding := range []string{"", "gzip"} {
		for target, expected := range map[string]string{
			"/webshark/embed?capture=a": `<base href="/proxy/phenix/webshark/">`,
			"/webshark/main.js":         want,
			"/webshark/worker.js":       want,
			"/webshark/plain.js":        "nothing to move",
			"/webshark/data.wasm":       `"/webshark/"`,
			"/webshark/styles.css":      `a{background:url("/proxy/phenix/webshark/assets/a.png")}`,
			"/webshark/assets/f.woff2":  `'/webshark/'`,
		} {
			resp := get(t, handler, target, encoding)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s with Accept-Encoding %q = %d, want %d", target, encoding, resp.StatusCode, http.StatusOK)
			}

			if got := readBody(t, resp); got != expected {
				t.Errorf("GET %s with Accept-Encoding %q = %q, want %q", target, encoding, got, expected)
			}
		}

		// a rewritten file has a content-hash ETag, so revalidating it costs a 304
		etag := get(t, handler, "/webshark/main.js", encoding).Header.Get("ETag")
		if etag == "" {
			t.Fatalf("GET /webshark/main.js with Accept-Encoding %q has no ETag, want one", encoding)
		}

		if resp := getIfNoneMatch(t, handler, "/webshark/main.js", encoding, etag); resp.StatusCode != http.StatusNotModified {
			t.Errorf("GET /webshark/main.js with Accept-Encoding %q and If-None-Match %s = %d, want %d",
				encoding, etag, resp.StatusCode, http.StatusNotModified)
		}
	}

	if resp := get(t, handler, "/webshark/missing.js", "gzip"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /webshark/missing.js = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	// a base path its scripts cannot take is left alone
	if got := readBody(t, get(t, WebSharkHandler(dir, `/a"b/`), "/webshark/main.js", "")); got != script {
		t.Errorf("GET /webshark/main.js under base path /a\"b/ = %q, want %q", got, script)
	}
}

func TestSharkCaptureNamesKeepTheirExtension(t *testing.T) {
	for base, want := range map[string][2]string{
		"router.pcap":      {"router-", ".pcap"},
		"router.PCAPNG":    {"router-", ".PCAPNG"},
		"old.pcap.gz":      {"old-", ".pcap.gz"},
		"vm-0.cap":         {"vm-0-", ".cap"},
		"no-extension":     {"no-extension-", ""},
		"dir.pcap/file.gz": {"dir.pcap/file.gz-", ""},
		".pcap":            {"-", ".pcap"},
		".pcap.gz":         {"-", ".pcap.gz"},
	} {
		name := newCaptureSecret(false, base)

		if !strings.HasPrefix(name, want[0]) || !strings.HasSuffix(name, want[1]) ||
			len(name) < len(want[0])+len(want[1])+20 {
			t.Errorf("newCaptureSecret(false, %q) = %q, want %s<random>%s", base, name, want[0], want[1])
		}
	}

	if token := newCaptureSecret(true, "router.pcap"); !strings.HasPrefix(token, "pcs_") || len(token) < 24 ||
		strings.Contains(token, "router") {
		t.Errorf("newCaptureSecret(true, router.pcap) = %q, want pcs_<random>", token)
	}
}

func TestTrustworthyOrigin(t *testing.T) {
	for _, tc := range []struct {
		host    string
		headers map[string]string
		want    bool
	}{
		{"localhost:3000", nil, true},
		{"127.0.0.1:3000", nil, true},
		{"phenix.example.com", nil, false},
		{"phenix.example.com", map[string]string{"X-Forwarded-Proto": "https"}, true},
		{"phenix.example.com", map[string]string{"X-Forwarded-Proto": "http"}, false},
		{"phenix.example.com", map[string]string{"Forwarded": `for=192.0.2.1;proto=https;host=a`}, true},
		{"phenix.example.com", map[string]string{"Forwarded": `For="[2001:db8::1]"; Proto="HTTPS"`}, true},
		{"phenix.example.com", map[string]string{"Forwarded": "proto=http, proto=https"}, false},
		{"phenix.example.com", map[string]string{"Forwarded": "for=192.0.2.1"}, false},
		{"phenix.example.com", map[string]string{"Forwarded": `for=192.0.2.1;host="a;proto=https"`}, false},
		{"phenix.example.com", map[string]string{"Forwarded": `host="a,b";proto=https`}, true},
	} {
		req := httptest.NewRequest(http.MethodGet, "/webshark/", nil)
		req.Host = tc.host

		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}

		if got := trustworthyOrigin(req); got != tc.want {
			t.Errorf("trustworthyOrigin(Host %s, headers %v) = %t, want %t", tc.host, tc.headers, got, tc.want)
		}
	}
}

// websharkSecret is the random part newCaptureSecret puts in a capture name.
var websharkSecret = regexp.MustCompile(`^-[a-z2-7]{26}$`)

// A capture name is the file's base with a random part before its capture
// extension, so WebShark opens the name exactly when it opens the file.
func TestSharkCaptureNamesKeepTheirExtensionProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		// a stem, which may be empty or an extension itself, as path.Base gives
		stem := rapid.StringMatching(`[a-zA-Z0-9._-]{0,8}`).Draw(t, "stem")
		ext := rapid.SampledFrom([]string{
			"", ".pcap", ".PCAPNG", ".cap", ".pcap.gz", ".Cap.GZ", ".pcapng.gz", ".gz", ".txt", ".pcap.txt", ".gz.pcap",
		}).Draw(t, "ext")
		base := stem + ext

		name := newCaptureSecret(false, base)

		if got, want := isCaptureFile(name), isCaptureFile(base); got != want {
			t.Fatalf("isCaptureFile(newCaptureSecret(false, %q) = %q) = %t, want %t", base, name, got, want)
		}

		const secretLen = 27 // "-" and 26 base32 characters

		found := false

		for k := 0; k+secretLen <= len(name) && !found; k++ {
			found = name[:k]+name[k+secretLen:] == base && websharkSecret.MatchString(name[k:k+secretLen])
		}

		if !found {
			t.Fatalf("newCaptureSecret(false, %q) = %q, want %q with -<random> inserted", base, name, base)
		}

		if again := newCaptureSecret(false, base); again == name {
			t.Fatalf("newCaptureSecret(false, %q) = %q twice, want a new name each time", base, name)
		}
	})
}

// websharkForwarded is a Forwarded header and whether its first element says
// proto=https.
type websharkForwarded struct {
	header string
	https  bool
}

// genWebsharkForwarded draws Forwarded headers of up to three elements.
func genWebsharkForwarded() *rapid.Generator[websharkForwarded] {
	keyCase := func(t *rapid.T, key string) string {
		return rapid.SampledFrom([]string{key, strings.ToUpper(key), strings.ToUpper(key[:1]) + key[1:]}).Draw(t, "case")
	}

	element := func(t *rapid.T) (string, bool) {
		params := rapid.SliceOfDistinct(rapid.SampledFrom([]string{"for", "by", "host", "proto", ""}), rapid.ID).Draw(t, "params")
		pairs := make([]string, 0, len(params))
		https := false

		for _, param := range params {
			value := rapid.SampledFrom([]string{
				"192.0.2.1", `"[2001:db8::1]:80"`, "_hidden", "a.example", "unknown",
				`"a;proto=https"`, `"a,proto=https"`, `"a\";proto=https"`,
			}).Draw(t, "value")

			if param == "proto" {
				value = rapid.SampledFrom([]string{"http", "https", "HTTPS", "Https", "wss", "httpss"}).Draw(t, "proto")
				https = strings.EqualFold(value, "https")

				if rapid.Bool().Draw(t, "quoted") {
					value = `"` + value + `"`
				}
			}

			pair := ""
			if param != "" {
				pair = keyCase(t, param) + "=" + value
			}

			pairs = append(pairs, pair)
		}

		return strings.Join(pairs, rapid.SampledFrom([]string{";", "; "}).Draw(t, "sep")), https
	}

	return rapid.Custom(func(t *rapid.T) websharkForwarded {
		more := rapid.IntRange(0, 2).Draw(t, "more")
		first, https := element(t)
		elements := make([]string, 1, 1+more)
		elements[0] = first

		for range more {
			e, _ := element(t)
			elements = append(elements, e)
		}

		return websharkForwarded{header: strings.Join(elements, rapid.SampledFrom([]string{",", ", "}).Draw(t, "comma")), https: https}
	})
}

// Only the first proxy's proto counts, whatever the case, quoting, order or
// spacing of its parameters, and a quoted value never splits an element.
func TestForwardedHTTPSProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		f := genWebsharkForwarded().Draw(t, "forwarded")

		if got := forwardedHTTPS(f.header); got != f.https {
			t.Fatalf("forwardedHTTPS(%q) = %t, want %t", f.header, got, f.https)
		}
	})
}

func TestFindWebShark(t *testing.T) {
	dir := t.TempDir()
	file, folder := filepath.Join(dir, "index.html"), filepath.Join(dir, "folder")

	if err := os.WriteFile(file, []byte("<html>"), 0o600); err != nil {
		t.Fatalf("writing index.html: %v", err)
	}

	if err := os.Mkdir(folder, 0o750); err != nil {
		t.Fatalf("making folder: %v", err)
	}

	// stat answers for each build directory's index.html as one of these
	statAs := func(kinds map[string]string) func(string) (os.FileInfo, error) {
		return func(name string) (os.FileInfo, error) {
			switch kinds[filepath.Dir(name)] {
			case "file":
				return os.Stat(file)
			case "folder":
				return os.Stat(folder)
			default:
				return nil, fs.ErrNotExist
			}
		}
	}

	tests := map[string]struct {
		kinds  map[string]string
		want   string
		wantOK bool
	}{
		"none":             {map[string]string{}, "", false},
		"relative":         {map[string]string{"webshark": "file", "/opt/phenix/webshark": "file"}, "webshark", true},
		"installed":        {map[string]string{"/opt/phenix/webshark": "file"}, "/opt/phenix/webshark", true},
		"index.html a dir": {map[string]string{"webshark": "folder"}, "", false},
		"index.html a dir, then installed": {
			map[string]string{"webshark": "folder", "/opt/phenix/webshark": "file"}, "/opt/phenix/webshark", true,
		},
	}

	for name, tc := range tests {
		if got, ok := FindWebShark(statAs(tc.kinds)); got != tc.want || ok != tc.wantOK {
			t.Errorf("FindWebShark(%s) = %q, %t, want %q, %t", name, got, ok, tc.want, tc.wantOK)
		}
	}
}

// WebSharkDirs lists the directories in order, as a copy.
func TestWebSharkDirs(t *testing.T) {
	t.Parallel()

	dirs := WebSharkDirs()
	if diff := cmp.Diff([]string{"webshark", "/opt/phenix/webshark"}, dirs); diff != "" {
		t.Errorf("WebSharkDirs() mismatch (-want +got):\n%s", diff)
	}

	dirs[0] = "changed"

	if got := WebSharkDirs()[0]; got != "webshark" {
		t.Errorf("WebSharkDirs()[0] after changing a returned copy = %q, want webshark", got)
	}
}

// A request that is not JSON, or too large, is refused before any lookup.
func TestCreateWebSharkCaptureBadBodies(t *testing.T) {
	useWebShark(t)

	server := serveAs(t, "alice", sharkRole()).URL

	for name, body := range map[string]string{
		"not JSON":   "path=a.pcap",
		"too large":  `{"path":"` + strings.Repeat("a", maxSharkRequestBytes) + `.pcap"}`,
		"wrong type": `{"interface":"0","vm":"test-vm"}`,
	} {
		resp, _ := createSharkCapture(t, server, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST webshark with a body %s = %d, want %d", name, resp.StatusCode, http.StatusBadRequest)
		}

		if got := errorMessage(t, resp.StatusCode, readBody(t, resp)); !strings.HasPrefix(got, "invalid request body: ") {
			t.Errorf("POST webshark with a body %s = %q, want invalid request body", name, got)
		}
	}
}

// websharkFailingFiles fails to list experiment files. Other file.ClusterFiles
// methods panic.
type websharkFailingFiles struct {
	file.ClusterFiles
}

func (websharkFailingFiles) GetExperimentFiles(string, string) (file.Files, error) {
	return nil, errors.New("mesh unreachable")
}

// websharkHostMM is the test minimega, failing to find VMs' hosts.
type websharkHostMM struct {
	*testMM
}

func (websharkHostMM) GetVMHost(...mm.Option) (string, error) {
	return "", errors.New("mesh unreachable")
}

// A file or capture that cannot be looked up for a reason other than a bad
// request is answered 500 and audited as failed.
func TestCreateWebSharkCaptureServerErrors(t *testing.T) {
	fake := useWebShark(t, mm.Capture{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/live.pcap"})
	useExperimentFilesDir(t, nil)

	original := file.DefaultClusterFiles
	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore test double

	file.DefaultClusterFiles = websharkFailingFiles{} //nolint:reassign // test double

	originalMM := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double

	mm.DefaultMM = websharkHostMM{testMM: fake} //nolint:reassign // test double

	server := serveAs(t, "alice", sharkRole()).URL

	for _, body := range []string{`{"path":"captures/a.pcap"}`, `{"vm":"test-vm","interface":0}`} {
		events := captureAudit(t)

		resp, _ := createSharkCapture(t, server, body)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("POST webshark %s with the mesh unreachable = %d, want %d", body, resp.StatusCode, http.StatusInternalServerError)
		}

		if got := errorMessage(t, resp.StatusCode, readBody(t, resp)); !strings.Contains(got, "mesh unreachable") {
			t.Errorf("POST webshark %s with the mesh unreachable = %q, want the error", body, got)
		}

		wantAudit(t, events(), "capture-webshark-open", "failure", map[string]any{"user.name": "alice"})
	}
}

// websharkGrant gives alice a grant of ops on target, returning its name.
func websharkGrant(t *testing.T, target capability.Target, ops ...capability.Op) string {
	t.Helper()

	name := newCaptureSecret(false, sharkBaseName(target))
	captureGrants.Mint(name, capability.Grant{
		User: "alice", Target: target, Ops: ops, Expires: time.Now().Add(time.Hour),
	})

	return name
}

// websharkServe serves req to h, as WebShark's requests reach it, with the
// capture name in the route.
func websharkServe(h http.HandlerFunc, method, target, name string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req = mux.SetURLVars(req, map[string]string{"name": name})

	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}

	rec := httptest.NewRecorder()
	h(rec, req)

	return rec
}

// websharkErrorBody is the error body WebShark reads.
func websharkErrorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Err    int    `json:"err"`
		Errstr string `json:"errstr"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Err != 1 {
		t.Fatalf("WebShark error body %q: %v, want {\"err\":1,...}", rec.Body.String(), err)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("WebShark error Content-Type = %q, want application/json", got)
	}

	return body.Errstr
}

// A capture is refused under a name no grant has, for an operation its grant
// does not allow, and when its file is gone or is not a file.
func TestServeSharkCaptureRefuses(t *testing.T) {
	useWebShark(t)

	dir := t.TempDir()
	saved := filepath.Join(dir, "router.pcap")

	if err := os.WriteFile(saved, testPcapHeader(), 0o600); err != nil {
		t.Fatalf("writing router.pcap: %v", err)
	}

	var (
		c        = grantChecker{}
		viewOnly = websharkGrant(t, capability.Target{Exp: "test-experiment", File: "router.pcap", Local: saved},
			capability.OpView)
		gone = websharkGrant(t, capability.Target{Exp: "test-experiment", File: "gone.pcap", Local: filepath.Join(dir, "gone.pcap")},
			viewOps...)
		folder = websharkGrant(t, capability.Target{Exp: "test-experiment", File: "dir.pcap", Local: dir}, capability.OpView)
		live   = websharkGrant(t, capability.Target{Exp: "test-experiment", VM: "test-vm", Interface: 3, Live: true},
			capability.OpView)
	)

	tests := map[string]struct {
		h          http.HandlerFunc
		target     string
		name       string
		wantStatus int
		wantErr    string
		// the audit event's outcome, "" for none
		wantAudit string
	}{
		"unknown name": {c.GetWebSharkCaptureFile, "/webshark/captures/x.pcap", "x.pcap", http.StatusNotFound, "Not Found", ""},
		"download not granted": {
			c.GetWebSharkJSON, "/webshark/json?method=download&capture=" + viewOnly, "", http.StatusForbidden, "Forbidden", "failure",
		},
		"file gone":   {c.GetWebSharkCaptureFile, "/webshark/captures/" + gone, gone, http.StatusNotFound, "not found", "failure"},
		"not a file":  {c.GetWebSharkCaptureFile, "/webshark/captures/" + folder, folder, http.StatusNotFound, "not found", ""},
		"not running": {c.GetWebSharkCaptureFile, "/webshark/captures/" + live, live, http.StatusNotFound, "not found", "failure"},
		"watch, unknown name": {
			c.GetWebSharkWatch, "/webshark/watch?capture=x.pcap", "", http.StatusNotFound, "Not Found", "",
		},
		"watch, file gone": {c.GetWebSharkWatch, "/webshark/watch?capture=" + gone, "", http.StatusNotFound, "not found", ""},
		"watch not granted": {
			c.GetWebSharkWatch, "/webshark/watch?capture=" + viewOnly, "", http.StatusForbidden, "Forbidden", "",
		},
		"unknown method": {
			c.GetWebSharkJSON, "/webshark/json?method=status", "", http.StatusBadRequest,
			`method must be one of files, download, not "status"`, "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			rec := websharkServe(tc.h, http.MethodGet, tc.target, tc.name)
			if rec.Code != tc.wantStatus {
				t.Errorf("GET %s = %d, want %d", tc.target, rec.Code, tc.wantStatus)
			}

			if got := websharkErrorBody(t, rec); got != tc.wantErr {
				t.Errorf("GET %s error = %q, want %q", tc.target, got, tc.wantErr)
			}

			got := events()

			switch tc.wantAudit {
			case "":
				if n := countAudit(got, "capture-webshark-read", "success", nil); n != 0 {
					t.Errorf("GET %s capture-webshark-read success audit events = %d, want 0", tc.target, n)
				}
			default:
				if countAudit(got, "capture-webshark-read", tc.wantAudit, nil) == 0 &&
					countAudit(got, "capture-webshark-download", tc.wantAudit, nil) == 0 {
					t.Errorf("GET %s audit events = %v, want a %s", tc.target, got, tc.wantAudit)
				}
			}
		})
	}
}

// WebShark lists no files, and a HEAD of a capture, or a range resuming it,
// is not audited as a read.
func TestGetWebSharkJSONAndPartialReads(t *testing.T) {
	useWebShark(t)

	saved := filepath.Join(t.TempDir(), "router.pcap")
	contents := append(testPcapHeader(), testPcapRecord(60)...)

	if err := os.WriteFile(saved, contents, 0o600); err != nil {
		t.Fatalf("writing router.pcap: %v", err)
	}

	c := grantChecker{}

	rec := websharkServe(c.GetWebSharkJSON, http.MethodGet, "/webshark/json?method=files", "")
	if got, want := rec.Body.String(), `{"files":[],"pwd":"."}`; got != want {
		t.Errorf("GET /webshark/json?method=files = %q, want %q", got, want)
	}

	name := websharkGrant(t, capability.Target{Exp: "test-experiment", File: "router.pcap", Local: saved}, viewOps...)
	events := captureAudit(t)

	head := websharkServe(c.GetWebSharkCaptureFile, http.MethodHead, "/webshark/captures/"+name, name)
	if head.Code != http.StatusOK || head.Header().Get("Content-Length") != strconv.Itoa(len(contents)) {
		t.Errorf("HEAD /webshark/captures/%s = %d with Content-Length %q, want %d with %d",
			name, head.Code, head.Header().Get("Content-Length"), http.StatusOK, len(contents))
	}

	resumed := websharkServe(c.GetWebSharkCaptureFile, http.MethodGet, "/webshark/captures/"+name, name, "Range", "bytes=24-")
	if resumed.Code != http.StatusPartialContent || !bytes.Equal(resumed.Body.Bytes(), contents[24:]) {
		t.Errorf("GET /webshark/captures/%s from byte 24 = %d with %d bytes, want %d with %d",
			name, resumed.Code, resumed.Body.Len(), http.StatusPartialContent, len(contents)-24)
	}

	if n := countAudit(events(), "capture-webshark-read", "success", nil); n != 0 {
		t.Errorf("capture-webshark-read audit events of a HEAD and a resumed range = %d, want 0", n)
	}

	full := websharkServe(c.GetWebSharkCaptureFile, http.MethodGet, "/webshark/captures/"+name, name)
	if !bytes.Equal(full.Body.Bytes(), contents) {
		t.Errorf("GET /webshark/captures/%s = %d bytes, want %d", name, full.Body.Len(), len(contents))
	}

	if n := countAudit(events(), "capture-webshark-read", "success", nil); n != 1 {
		t.Errorf("capture-webshark-read audit events after a whole read = %d, want 1", n)
	}
}

// A live capture that has stopped is read from its file from then on, and
// its grant says so.
func TestSharkSourceFallsBackToTheFile(t *testing.T) {
	useWebShark(t)
	useExperimentFilesDir(t, map[string]string{"live.pcap": "finished capture"})

	target := capability.Target{Exp: "test-experiment", File: "live.pcap", VM: "test-vm", Interface: 0, Live: true}
	name := websharkGrant(t, target, viewOps...)

	rec := websharkServe(grantChecker{}.GetWebSharkCaptureFile, http.MethodGet, "/webshark/captures/"+name, name)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /webshark/captures/%s of a stopped capture = %d, want %d", name, rec.Code, http.StatusOK)
	}

	if got, want := rec.Body.String(), "finished capture"; got != want {
		t.Errorf("GET /webshark/captures/%s of a stopped capture = %q, want %q", name, got, want)
	}

	// a saved file is resumable, which a running capture is not
	if etag := rec.Header().Get("ETag"); etag == "" {
		t.Errorf("GET /webshark/captures/%s of a stopped capture has no ETag, want the file's", name)
	}

	g, err := captureGrants.Use(name, capability.OpView)
	if err != nil {
		t.Fatalf("captureGrants.Use(%s) error = %v", name, err)
	}

	if g.Target.Live || g.Target.Local == "" {
		t.Errorf("grant target after its capture stopped = %+v, want the saved file", g.Target)
	}

	// a stopped capture whose file is not there either is not found
	missing := websharkGrant(t, capability.Target{Exp: "test-experiment", File: "gone.pcap", VM: "test-vm", Live: true}, viewOps...)

	rec = websharkServe(grantChecker{}.GetWebSharkCaptureFile, http.MethodGet, "/webshark/captures/"+missing, missing)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /webshark/captures/%s of a stopped capture without its file = %d, want %d",
			missing, rec.Code, http.StatusNotFound)
	}
}

// A saved capture's watch tells its size once, then only sends heartbeats
// until the watcher leaves.
func TestGetWebSharkWatchOfSavedFile(t *testing.T) {
	useWebShark(t)

	saved := filepath.Join(t.TempDir(), "router.pcap")
	if err := os.WriteFile(saved, testPcapHeader(), 0o600); err != nil {
		t.Fatalf("writing router.pcap: %v", err)
	}

	name := websharkGrant(t, capability.Target{Exp: "test-experiment", File: "router.pcap", Local: saved}, viewOps...)

	server := httptest.NewServer(http.HandlerFunc(grantChecker{}.GetWebSharkWatch))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	events := sharkEvents(ctx, t, server.URL+"/webshark/watch?capture="+name)

	noMtime := cmpopts.IgnoreMapEntries(func(k string, _ any) bool { return k == "mtime" })

	select {
	case ev := <-events:
		if diff := cmp.Diff(map[string]any{"kind": "init", "size": float64(len(testPcapHeader()))}, ev, noMtime); diff != "" {
			t.Errorf("first /webshark/watch event of a saved file mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("/webshark/watch of a saved file sent no event within 5s, want init")
	}

	select {
	case ev := <-events:
		t.Errorf("/webshark/watch of a saved file sent %v after init, want nothing more", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// websharkWriter is a response writer that flushes and fails every write
// after the first ok ones.
type websharkWriter struct {
	header http.Header
	ok     int

	mu   sync.Mutex
	body bytes.Buffer
}

func (w *websharkWriter) Header() http.Header { return w.header }

func (w *websharkWriter) WriteHeader(int) {}

func (w *websharkWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.ok == 0 {
		return 0, errors.New("watcher went away")
	}

	w.ok--

	return w.body.Write(b)
}

// written is the body written so far.
func (w *websharkWriter) written() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.body.String()
}

// events counts the capture-changed events written so far.
func (w *websharkWriter) events() int {
	return strings.Count(w.written(), "event: capture-changed")
}

func (w *websharkWriter) Flush() {}

// websharkNoFlush is a response writer that cannot flush.
type websharkNoFlush struct {
	http.ResponseWriter
}

// websharkNewWatch is a watch writing to w, with heartbeats every beat.
func websharkNewWatch(t *testing.T, w http.ResponseWriter, beat time.Duration) *sharkWatch {
	t.Helper()

	watch := &sharkWatch{w: w, rc: http.NewResponseController(w), heartbeat: time.NewTicker(beat)}
	t.Cleanup(watch.heartbeat.Stop)

	return watch
}

// An event is sent and flushed, and a watcher that cannot take it ends the
// watch.
func TestSharkWatchSend(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	watch := websharkNewWatch(t, rec, time.Hour)

	mtime := time.UnixMilli(1_700_000_000_123)
	if !watch.changed(42, mtime, "append") {
		t.Fatal("changed() to a recorder = false, want true")
	}

	want := `event: capture-changed` + "\n" + `data: {"kind":"append","mtime":1700000000123,"size":42}` + "\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("changed(42, %v, append) wrote %q, want %q", mtime, got, want)
	}

	if !rec.Flushed {
		t.Error("changed() did not flush, want the event sent at once")
	}

	if websharkNewWatch(t, &websharkWriter{header: http.Header{}}, time.Hour).send("heartbeat", nil) {
		t.Error("send() to a watcher that went away = true, want false")
	}

	if websharkNewWatch(t, websharkNoFlush{httptest.NewRecorder()}, time.Hour).send("heartbeat", nil) {
		t.Error("send() to a writer that cannot flush = true, want false")
	}
}

// wait sends heartbeats until a channel fires or the watch ends.
func TestSharkWatchWait(t *testing.T) {
	t.Parallel()

	changed := make(chan struct{})
	close(changed)

	tests := map[string]struct {
		due     bool
		changed <-chan struct{}
		// ended ends the watch before wait; heartbeat ends it after a heartbeat
		ended     bool
		w         func() http.ResponseWriter
		want      bool
		heartbeat bool
	}{
		"due":         {due: true, w: websharkRecorder, want: true},
		"changed":     {changed: changed, w: websharkRecorder, want: true},
		"watch ended": {ended: true, w: websharkRecorder},
		"heartbeat, then ended": {
			w: func() http.ResponseWriter { return &websharkWriter{header: http.Header{}, ok: 1 << 20} }, heartbeat: true,
		},
		"heartbeat to no watcher": {w: func() http.ResponseWriter { return &websharkWriter{header: http.Header{}} }},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			if tc.ended {
				cancel()
			}

			var due chan time.Time
			if tc.due {
				due = make(chan time.Time, 1)
				due <- time.Now()
			}

			w := tc.w()
			watch := websharkNewWatch(t, w, 10*time.Millisecond)

			var got bool

			done := make(chan struct{})

			go func() {
				defer close(done)

				got = watch.wait(ctx, due, tc.changed)
			}()

			if tc.heartbeat {
				ww, _ := w.(*websharkWriter)
				polltest.Until(t, "a heartbeat", func() bool {
					return strings.HasPrefix(ww.written(), "event: heartbeat\ndata: {\"t\":")
				})
				cancel()
			}

			polltest.Within(t, "wait returning", func() { <-done })

			if got != tc.want {
				t.Errorf("wait(%s) = %t, want %t", name, got, tc.want)
			}
		})
	}
}

// websharkRecorder is a new response recorder, as a writer.
func websharkRecorder() http.ResponseWriter { return httptest.NewRecorder() }

// A live watch ends as soon as the watcher cannot take an event: the first,
// or the one telling that the capture grew.
func TestWatchSharkTapEndsWhenTheWatcherLeaves(t *testing.T) {
	fake := useWebShark(t)
	live := useLiveCapture(t, fake)
	live.write(t, testPcapHeader(), testPcapRecord(60))

	followLiveCapture(t)

	for name, ok := range map[string]int{"init": 0, "append": 1} {
		t.Run(name, func(t *testing.T) {
			tap, err := livecapture.Open("test-experiment", "test-vm", 0)
			if err != nil {
				t.Fatalf("livecapture.Open(test-experiment, test-vm, 0) = %v", err)
			}

			defer tap.Release()

			w := &websharkWriter{header: http.Header{}, ok: ok}
			done := make(chan struct{})

			go func() {
				defer close(done)

				watchSharkTap(t.Context(), websharkNewWatch(t, w, time.Hour), tap)
			}()

			if ok > 0 {
				// the init event must read the size before the capture grows
				polltest.Until(t, "the init event", func() bool { return w.events() == 1 })
				live.write(t, testPcapRecord(60))
			}

			polltest.Within(t, "the watch ending", func() { <-done })

			if got := w.events(); got != ok {
				t.Errorf("events the watcher took = %d, want %d", got, ok)
			}
		})
	}
}

// A capture that grows again soon after it was told of is told of once
// sharkUpdateInterval has passed, not at once.
func TestWatchSharkTapWaitsBetweenUpdates(t *testing.T) {
	fake := useWebShark(t)
	live := useLiveCapture(t, fake)
	live.write(t, testPcapHeader(), testPcapRecord(60))

	followLiveCapture(t)

	tap, err := livecapture.Open("test-experiment", "test-vm", 0)
	if err != nil {
		t.Fatalf("livecapture.Open(test-experiment, test-vm, 0) = %v", err)
	}

	defer tap.Release()

	w := &websharkWriter{header: http.Header{}, ok: 100}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		watchSharkTap(ctx, websharkNewWatch(t, w, time.Hour), tap)
	}()

	defer func() {
		cancel()
		<-done
	}()

	polltest.Until(t, "the init event", func() bool { return w.events() == 1 })

	live.write(t, testPcapRecord(60))
	polltest.Until(t, "the first append", func() bool { return w.events() == 2 })

	told := time.Now()

	live.write(t, testPcapRecord(60))
	polltest.Until(t, "the second append", func() bool { return w.events() == 3 })

	if waited := time.Since(told); waited < sharkMinUpdate-100*time.Millisecond {
		t.Errorf("second append told %s after the first, want about %s", waited, sharkMinUpdate)
	}
}

func TestSharkUpdateInterval(t *testing.T) {
	t.Parallel()

	for size, want := range map[int64]time.Duration{
		0:                        time.Second,
		sharkBytesPerSecond - 1:  time.Second,
		sharkBytesPerSecond:      2 * time.Second,
		5 * sharkBytesPerSecond:  6 * time.Second,
		9 * sharkBytesPerSecond:  10 * time.Second,
		10 * sharkBytesPerSecond: 10 * time.Second,
		1 << 40:                  10 * time.Second,
	} {
		if got := sharkUpdateInterval(size); got != want {
			t.Errorf("sharkUpdateInterval(%d) = %s, want %s", size, got, want)
		}
	}
}

func TestSharkBaseNameAndTitle(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		target          capability.Target
		base, wantTitle string
	}{
		"saved file": {capability.Target{File: "captures/router.pcap"}, "router.pcap", "captures/router.pcap"},
		"live with its file": {
			capability.Target{File: "live.pcap", VM: "vm", Interface: 2, Live: true}, "live.pcap", "vm interface 2 (live)",
		},
		"live without a file": {capability.Target{VM: "vm", Interface: 0, Live: true}, "vm-0.pcap", "vm interface 0 (live)"},
		"live experiment capture": {
			capability.Target{Exp: "exp1", File: "exp1-1.pcapng", Capture: "3", Live: true}, "exp1-1.pcapng",
			"exp1 capture 3 (live)",
		},
		"experiment capture without a file": {
			capability.Target{Exp: "exp1", Capture: "3", Live: true}, "exp1-capture-3.pcapng", "exp1 capture 3 (live)",
		},
		"stopped experiment capture": {
			capability.Target{Exp: "exp1", File: "exp1-1.pcapng", Capture: "3"}, "exp1-1.pcapng", "exp1-1.pcapng",
		},
	}

	for name, tc := range tests {
		if got := sharkBaseName(tc.target); got != tc.base {
			t.Errorf("sharkBaseName(%s) = %q, want %q", name, got, tc.base)
		}

		if got := sharkTitle(tc.target); got != tc.wantTitle {
			t.Errorf("sharkTitle(%s) = %q, want %q", name, got, tc.wantTitle)
		}
	}
}

func TestReadsFromStart(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		method, rng string
		want        bool
	}{
		{http.MethodGet, "", true},
		{http.MethodGet, "bytes=0-", true},
		{http.MethodGet, "bytes=0-99", true},
		{http.MethodGet, "bytes=5-", false},
		{http.MethodGet, "bytes=-100", false},
		{http.MethodHead, "", false},
	} {
		req := httptest.NewRequest(tc.method, "/webshark/captures/a.pcap", nil)
		if tc.rng != "" {
			req.Header.Set("Range", tc.rng)
		}

		if got := readsFromStart(req); got != tc.want {
			t.Errorf("readsFromStart(%s Range %q) = %t, want %t", tc.method, tc.rng, got, tc.want)
		}
	}
}

func TestSharkContentType(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"wiregasm.wasm":  "application/wasm",
		"main.js":        "text/javascript; charset=utf-8",
		"worker.mjs":     "text/javascript; charset=utf-8",
		"wiregasm.data":  "application/octet-stream",
		"styles.css":     "text/css; charset=utf-8",
		"index.html":     "text/html; charset=utf-8",
		"no-extension":   "application/octet-stream",
		"file.unknownxx": "application/octet-stream",
	} {
		if got := sharkContentType(name); got != want {
			t.Errorf("sharkContentType(%q) = %q, want %q", name, got, want)
		}
	}
}

// websharkNoSeek hides its files' Seek, as a file system that cannot seek.
type websharkNoSeek struct {
	fs.FS
}

func (s websharkNoSeek) Open(name string) (fs.File, error) {
	f, err := s.FS.Open(name)
	if err != nil {
		return nil, err //nolint:wrapcheck // as the file system answers
	}

	return struct{ fs.File }{f}, nil
}

// Only a regular file that can seek is served, gzipped when it is the
// compressed copy.
func TestServeSharkFile(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"main.js": {Data: []byte("main")}, "assets/a.png": {Data: []byte("png")}}

	tests := map[string]struct {
		fsys     fs.FS
		name     string
		gzipped  bool
		want     bool
		wantBody string
	}{
		"file":        {fsys, "main.js", false, true, "main"},
		"gzipped":     {fsys, "main.js", true, true, "main"},
		"missing":     {fsys, "gone.js", false, false, ""},
		"folder":      {fsys, "assets", false, false, ""},
		"cannot seek": {websharkNoSeek{fsys}, "main.js", false, false, ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()

			req := httptest.NewRequest(http.MethodGet, "/webshark/"+tc.name, nil)

			if got := serveSharkFile(rec, req, tc.fsys, tc.name, tc.gzipped); got != tc.want {
				t.Fatalf("serveSharkFile(%s) = %t, want %t", name, got, tc.want)
			}

			if rec.Body.String() != tc.wantBody {
				t.Errorf("serveSharkFile(%s) body = %q, want %q", name, rec.Body.String(), tc.wantBody)
			}

			if got := rec.Header().Get("Content-Encoding") == "gzip"; got != (tc.want && tc.gzipped) {
				t.Errorf("serveSharkFile(%s) gzip Content-Encoding = %t, want %t", name, got, tc.want && tc.gzipped)
			}
		})
	}
}

// The build's file is read as it is, or from its compressed copy; a copy that
// is not gzip, or is cut short, is an error.
func TestReadSharkFile(t *testing.T) {
	t.Parallel()

	gz := gzipped(t, []byte("from the copy"))

	fsys := fstest.MapFS{
		"plain.js":      {Data: []byte("plain")},
		"copy.js.gz":    {Data: gz},
		"corrupt.js.gz": {Data: []byte("not gzip")},
		"cut.js.gz":     {Data: gz[:len(gz)-8]},
		"folder/x":      {Data: nil},
	}

	tests := map[string]struct {
		want    string
		wantErr bool
	}{
		"plain.js":   {"plain", false},
		"copy.js":    {"from the copy", false},
		"corrupt.js": {"", true},
		"cut.js":     {"", true},
		"gone.js":    {"", true},
		"folder":     {"", true},
	}

	for name, tc := range tests {
		b, _, err := readSharkFile(fsys, name)
		if (err != nil) != tc.wantErr {
			t.Errorf("readSharkFile(%s) error = %v, want error: %t", name, err, tc.wantErr)
		}

		if err == nil && string(b) != tc.want {
			t.Errorf("readSharkFile(%s) = %q, want %q", name, b, tc.want)
		}
	}
}

// The rebaser is off at the root and for a base path the build's scripts
// cannot take, and rewrites each file once however many requests ask.
func TestNewSharkRebaser(t *testing.T) {
	logs := plogtest.Capture(t)

	fsys := fstest.MapFS{"main.js": {Data: []byte(`fetch("/webshark/json")`)}, "corrupt.js.gz": {Data: []byte("not gzip")}}

	for _, base := range []string{"/", `/a"b/`, "/no-trailing-slash", "relative/"} {
		if r := newSharkRebaser(fsys, base); r != nil {
			t.Errorf("newSharkRebaser(%q) = %v, want nil", base, r)
		}

		if (*sharkRebaser)(nil).serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/webshark/main.js", nil), "main.js") {
			t.Error("nil sharkRebaser.serve() = true, want false")
		}
	}

	const refused = "WebShark works only at /webshark/: the base path has characters its scripts cannot accept"

	if got := len(logs.Take(t, plogtest.Message(refused))); got != 3 {
		t.Errorf("warnings of base paths the scripts cannot take = %d, want 3", got)
	}

	r := newSharkRebaser(fsys, "/proxy/")

	if f := r.file("gone.js"); f != nil {
		t.Errorf("file(gone.js) = %v, want nil for a file the build does not have", f)
	}

	if f := r.file("corrupt.js"); f == nil || f.err == nil {
		t.Errorf("file(corrupt.js) = %v, want its read error", f)
	}

	if r.serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/webshark/corrupt.js", nil), "corrupt.js") {
		t.Error("serve(corrupt.js) = true, want false for a file that cannot be read")
	}

	files := make(chan *rebasedSharkFile, 16)

	var wg sync.WaitGroup

	for range cap(files) {
		wg.Add(1)

		go func() {
			defer wg.Done()

			files <- r.file("main.js")
		}()
	}

	wg.Wait()
	close(files)

	first := <-files
	for f := range files {
		if f != first {
			t.Fatal("concurrent file(main.js) calls returned different entries, want one rewritten once")
		}
	}

	rec := httptest.NewRecorder()
	if !r.serve(rec, httptest.NewRequest(http.MethodGet, "/webshark/main.js", nil), "main.js") {
		t.Fatal("serve(main.js) = false, want the rewritten file")
	}

	if got, want := rec.Body.String(), `fetch("/proxy/webshark/json")`; got != want {
		t.Errorf("serve(main.js) = %q, want %q", got, want)
	}
}

// A build file kept only as a compressed copy that is not gzip is answered
// 500; a trustworthy origin gets the isolation headers, another does not.
func TestWebSharkHandlerCorruptCopyAndIsolation(t *testing.T) {
	t.Parallel()

	handler := WebSharkHandler(writeSharkBuild(t, map[string][]byte{
		"index.html":       []byte("<html>"),
		"broken.wasm.gz":   []byte("not gzip"),
		"assets/style.css": []byte("a{}"),
	}), "/")

	if rec := get(t, handler, "/webshark/broken.wasm", ""); rec.StatusCode != http.StatusInternalServerError {
		t.Errorf("GET /webshark/broken.wasm of a corrupt copy = %d, want %d", rec.StatusCode, http.StatusInternalServerError)
	}

	for host, want := range map[string]bool{"localhost:3000": true, "[::1]:3000": true, "phenix.example.com": false} {
		req := httptest.NewRequest(http.MethodGet, "/webshark/", nil)
		req.Host = host

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		isolated := map[string]string{
			"Cross-Origin-Opener-Policy":   rec.Header().Get("Cross-Origin-Opener-Policy"),
			"Cross-Origin-Embedder-Policy": rec.Header().Get("Cross-Origin-Embedder-Policy"),
		}

		wantHeaders := map[string]string{"Cross-Origin-Opener-Policy": "", "Cross-Origin-Embedder-Policy": ""}
		if want {
			wantHeaders = map[string]string{
				"Cross-Origin-Opener-Policy": "same-origin", "Cross-Origin-Embedder-Policy": "credentialless",
			}
		}

		if diff := cmp.Diff(wantHeaders, isolated); diff != "" {
			t.Errorf("GET /webshark/ from host %s isolation headers mismatch (-want +got):\n%s", host, diff)
		}
	}
}

func TestSharkError(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sharkError(rec, `bad "method"`, http.StatusTeapot)

	if rec.Code != http.StatusTeapot {
		t.Errorf("sharkError() status = %d, want %d", rec.Code, http.StatusTeapot)
	}

	if got, want := rec.Body.String(), `{"err":1,"errstr":"bad \"method\""}`; got != want {
		t.Errorf("sharkError() body = %q, want %q", got, want)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("sharkError() Content-Type = %q, want application/json", got)
	}
}
