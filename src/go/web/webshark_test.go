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
	"strings"
	"testing"
	"time"

	"phenix/api/livecapture"
	"phenix/util/mm"
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

	return fake
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
		t.Fatal(err)
	}

	if err := os.WriteFile(c.path, nil, 0o600); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}

	defer f.Close()

	if _, err := f.Write(bytes.Join(b, nil)); err != nil {
		t.Fatal(err)
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
	binary.LittleEndian.PutUint32(b[8:], uint32(n))  //nolint:gosec // small
	binary.LittleEndian.PutUint32(b[12:], uint32(n)) //nolint:gosec // small

	return b
}

// sharkRole may read test-vm's captures.
func sharkRole() rbac.Role {
	return combinedRole(
		testRole("experiments/files", "get", "test-experiment"),
		testRole("vms/captures", "list", "test-experiment/test-vm"),
	)
}

// createSharkCapture asks the server for a capture of test-experiment
// WebShark can open, and returns the response and, if it is a 200, the
// capture it gives.
func createSharkCapture(t *testing.T, server, body string) (*http.Response, sharkCaptureResponse) {
	t.Helper()

	resp := do(t, http.MethodPost, server+"/api/v1/experiments/test-experiment/webshark", body)

	var capture sharkCaptureResponse

	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&capture); err != nil {
			t.Fatalf("decoding capture: %v", err)
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
		t.Fatalf("got %d: %s", resp.StatusCode, readBody(t, resp))
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

	capture := openInWebShark(t, server, `{"path":"captures/router.pcap"}`)
	if capture.Live || capture.Title != "captures/router.pcap" || !strings.HasPrefix(capture.Capture, "router-") ||
		!strings.HasSuffix(capture.Capture, ".pcap") {
		t.Fatalf("unexpected response %+v", capture)
	}

	resp := do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture, "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || body != contents {
		t.Fatalf("serving the capture: %d, %d bytes", resp.StatusCode, len(body))
	}

	resp = do(t, http.MethodGet, server+"/webshark/json?method=download&capture="+capture.Capture, "")
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename=router.pcap` {
		t.Fatalf("download named %q", got)
	}

	// another name, however close, is not the capture
	resp = do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture[1:], "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown name: expected 404, got %d", resp.StatusCode)
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
	}{
		"no file permission": {
			testRole("experiments/files", "list", "test-experiment"), `{"path":"a.pcap"}`, http.StatusForbidden,
		},
		"no capture permission": {
			testRole("experiments/files", "get", "test-experiment"), `{"vm":"test-vm","interface":0}`, http.StatusForbidden,
		},
		"not a capture":      {sharkRole(), `{"path":"notes.txt"}`, http.StatusBadRequest},
		"escaping path":      {sharkRole(), `{"path":"../other/a.pcap"}`, http.StatusBadRequest},
		"missing file":       {sharkRole(), `{"path":"gone.pcap"}`, http.StatusNotFound},
		"both":               {sharkRole(), `{"path":"a.pcap","vm":"test-vm","interface":0}`, http.StatusBadRequest},
		"neither":            {sharkRole(), `{}`, http.StatusBadRequest},
		"no interface":       {sharkRole(), `{"vm":"test-vm"}`, http.StatusBadRequest},
		"not being captured": {sharkRole(), `{"vm":"test-vm","interface":1}`, http.StatusNotFound},
	}

	useTestFiles(t) // lists no files

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resp, _ := createSharkCapture(t, serveAs(t, "alice", tt.role).URL, tt.body)
			if resp.StatusCode != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, resp.StatusCode, readBody(t, resp))
			}
		})
	}

	o.features = map[string]bool{}

	resp, _ := createSharkCapture(t, serveAs(t, "alice", sharkRole()).URL, `{"path":"a.pcap"}`)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("without WebShark: expected 501, got %d", resp.StatusCode)
	}
}

// A file whose name only resembles the one a capture writes is a saved file.
func TestCreateWebSharkCaptureOfFileLikeACapture(t *testing.T) {
	useWebShark(t, mm.Capture{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/xa.pcap"})
	useExperimentFilesDir(t, map[string]string{"a.pcap": string(testPcapHeader())})

	if capture := openInWebShark(t, serveAs(t, "alice", sharkRole()).URL, `{"path":"a.pcap"}`); capture.Live {
		t.Fatalf("got %+v, want the saved file", capture)
	}
}

func TestCreateWebSharkCaptureOfRunningCapture(t *testing.T) {
	fake := useWebShark(t)
	live := useLiveCapture(t, fake)
	header, one, two := testPcapHeader(), testPcapRecord(60), testPcapRecord(1500)
	live.write(t, header, one, two[:100])

	server := serveAs(t, "alice", sharkRole()).URL

	// the file being captured opens live, found with one listing of captures
	capture := openInWebShark(t, server, `{"path":"live.pcap"}`)
	if !capture.Live || !strings.HasPrefix(capture.Capture, "live-") {
		t.Fatalf("got %+v, want a live capture", capture)
	}

	if n := fake.captureListings.Load(); n != 1 {
		t.Fatalf("captures listed %d times, want once", n)
	}

	followLiveCapture(t)

	// only whole records are served
	whole := len(header) + len(one)

	resp := do(t, http.MethodGet, server+"/webshark/captures/"+capture.Capture, "")
	if body := readBody(t, resp); len(body) != whole {
		t.Fatalf("served %d bytes, want the %d of whole records", len(body), whole)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel() // before closing the server, which waits for the watch to end

	events := sharkEvents(ctx, t, server+"/webshark/watch?capture="+capture.Capture)

	if init := <-events; init["kind"] != "init" || init["size"] != float64(whole) {
		t.Fatalf("first event %v, want init with the size of the whole records", init)
	}

	live.write(t, two[100:])

	select {
	case ev := <-events:
		if ev["kind"] != "append" || ev["size"] != float64(whole+len(two)) {
			t.Fatalf("got event %v, want an append of the second record", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event when the capture grew")
	}
}

// sharkEvents reads the capture-changed events of a watch.
func sharkEvents(ctx context.Context, t *testing.T, url string) <-chan map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("watch answered %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
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
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()

	gz, err := gzipBytes(b)
	if err != nil {
		t.Fatal(err)
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
		if body := readBody(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(body, "<base") {
			t.Errorf("%s: got %d %q, want the app", target, resp.StatusCode, body)
		}
	}

	resp := get(t, handler, "/webshark/main.js", "")
	if body := readBody(t, resp); body != "main" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("main.js: got %q as %q", body, resp.Header.Get("Content-Type"))
	}

	// the precompressed copy, sent as it is to a client that takes gzip
	resp = get(t, handler, "/webshark/assets/wiregasm/wiregasm.wasm", "gzip, br")
	if body, _ := io.ReadAll(resp.Body); resp.Header.Get("Content-Encoding") != "gzip" ||
		resp.Header.Get("Content-Type") != "application/wasm" || !bytes.Equal(body, gz) {
		t.Errorf("wasm with gzip: %q %q, %d bytes", resp.Header.Get("Content-Encoding"),
			resp.Header.Get("Content-Type"), len(body))
	}

	resp = get(t, handler, "/webshark/assets/wiregasm/wiregasm.wasm", "")
	if body, _ := io.ReadAll(resp.Body); resp.Header.Get("Content-Encoding") != "" || !bytes.Equal(body, wasm) {
		t.Errorf("wasm without gzip: %q, %d bytes", resp.Header.Get("Content-Encoding"), len(body))
	}

	// WebShark's service worker is replaced by one that removes itself
	resp = get(t, handler, "/webshark/enable-threads.js", "gzip, br")
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(body, "unregister()") ||
		strings.Contains(body, "register(src)") ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("enable-threads.js: got %d %q as %q", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}

	for _, target := range []string{"/webshark/missing.js", "/webshark/../main.js", "/other/main.js"} {
		if resp := get(t, handler, target, "gzip, br"); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", target, resp.StatusCode)
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
			if body := readBody(t, resp); resp.StatusCode != http.StatusOK || body != expected {
				t.Errorf("%s (%q): got %d %q, want %q", target, encoding, resp.StatusCode, body, expected)
			}
		}

		// a rewritten file has a content-hash ETag, so revalidating it costs a 304
		etag := get(t, handler, "/webshark/main.js", encoding).Header.Get("ETag")
		if etag == "" {
			t.Fatalf("main.js (%q): no ETag", encoding)
		}

		if resp := getIfNoneMatch(t, handler, "/webshark/main.js", encoding, etag); resp.StatusCode != http.StatusNotModified {
			t.Errorf("main.js (%q): revalidating %s got %d, want 304", encoding, etag, resp.StatusCode)
		}
	}

	if resp := get(t, handler, "/webshark/missing.js", "gzip"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing.js: expected 404, got %d", resp.StatusCode)
	}

	// a base path its scripts cannot take is left alone
	if body := readBody(t, get(t, WebSharkHandler(dir, `/a"b/`), "/webshark/main.js", "")); body != script {
		t.Errorf("unusable base path: got %q", body)
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
	} {
		name := addSharkCapture(&sharkCapture{}, base)

		if !strings.HasPrefix(name, want[0]) || !strings.HasSuffix(name, want[1]) ||
			len(name) < len(want[0])+len(want[1])+20 {
			t.Errorf("%s named %s, want %s<random>%s", base, name, want[0], want[1])
		}

		if _, ok := getSharkCapture(name); !ok {
			t.Errorf("%s: not found by its name", base)
		}
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
	} {
		req := httptest.NewRequest(http.MethodGet, "/webshark/", nil)
		req.Host = tc.host

		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}

		if got := trustworthyOrigin(req); got != tc.want {
			t.Errorf("%s %v: got %t, want %t", tc.host, tc.headers, got, tc.want)
		}
	}
}
