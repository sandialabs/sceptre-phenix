package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/livecapture"
	"phenix/util/mm"
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

// useCaptureGrants gives the test a registry of capture grants of its own.
func useCaptureGrants(t *testing.T) {
	t.Helper()

	original := captureGrants
	captureGrants = capability.New(maxCaptureGrants, maxCaptureGrantsPerUser, nil)

	t.Cleanup(func() { captureGrants = original })
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

	if diff := cmp.Diff(map[string]any{"kind": "init", "size": float64(whole)}, <-events, noMtime); diff != "" {
		t.Errorf("first /webshark/watch event mismatch (-want +got):\n%s", diff)
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
