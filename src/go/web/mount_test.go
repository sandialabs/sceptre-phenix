package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/polltest"
	"phenix/web/rbac"
)

// useTestMount turns vm-mount on and mounts test-vm of test-experiment with files,
// beside an unmounted sibling test-vm2 that test-vm's name prefixes.
func useTestMount(t *testing.T) string {
	t.Helper()

	useExperimentFilesDir(t, map[string]string{"a.txt": "alpha", "live.pcap": "partial"})

	original, originalOpts := common.MountBase, o
	common.MountBase = "" //nolint:reassign // mounts go under the temporary PhenixBase
	o.features = map[string]bool{"vm-mount": true}

	t.Cleanup(func() { common.MountBase, o = original, originalOpts }) //nolint:reassign // restore

	base := mm.GetLocalMountPath("test-experiment", "test-vm")

	for p, contents := range map[string]string{
		filepath.Join(base, "etc", "hostname"):          "test-vm",
		filepath.Join(base, "..foo", "readme"):          "a directory whose name only starts with ..",
		filepath.Join(base, "..", "test-vm2", "secret"): "not yours",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	activeMountsMu.Lock()
	activeMounts[base] = &MountInfo{users: 1, lock: &sync.RWMutex{}}
	activeMountsMu.Unlock()

	t.Cleanup(func() {
		activeMountsMu.Lock()
		delete(activeMounts, base)
		activeMountsMu.Unlock()
	})

	return base
}

func mountRole() rbac.Role {
	return combinedRole(
		testRole("vms/mount", "*", "test-experiment/test-vm"),
		testRole("experiments/files", "get", "test-experiment"),
	)
}

// requestMount sends a request for test-vm's mounted files, at route with
// query, as a user with mountRole.
func requestMount(t *testing.T, method, route string, query url.Values) *http.Response {
	t.Helper()

	server := serveAs(t, "alice", mountRole()).URL

	return do(t, method, server+"/api/v1/experiments/test-experiment/vms/test-vm/files"+route+"?"+query.Encode(), "")
}

// A sibling mount whose path starts with this one's is outside it; a name
// that only starts with ".." is inside.
func TestMountFileRequestsStayWithinTheMount(t *testing.T) {
	useTestMount(t)

	tests := map[string]struct {
		route, path string
		want        int
	}{
		"download a file":                      {"/download", "etc/hostname", http.StatusOK},
		"download from a .. named directory":   {"/download", "..foo/readme", http.StatusOK},
		"download from a sibling mount":        {"/download", "../test-vm2/secret", http.StatusBadRequest},
		"download from outside the mounts":     {"/download", "../../../images", http.StatusBadRequest},
		"list a directory":                     {"", "etc", http.StatusOK},
		"list a .. named directory":            {"", "..foo", http.StatusOK},
		"list the directory holding the mount": {"", "..", http.StatusBadRequest},
		"list a sibling mount":                 {"", "../test-vm2", http.StatusBadRequest},
		"list a missing sibling":               {"", "../no-such-vm", http.StatusBadRequest},
		"list outside the mounts":              {"", "../../..", http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			resp := requestMount(t, http.MethodGet, tc.route, url.Values{"path": {tc.path}})
			if resp.StatusCode != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, resp.StatusCode, readBody(t, resp))
			}

			if tc.route != "/download" {
				return
			}

			if _ = readBody(t, resp); tc.want == http.StatusOK {
				wantAudit(t, events(), "file-download", "success", map[string]any{
					"user.name": "alice", "phenix.vm.name": "test-vm", "file.name": filepath.Base(tc.path),
				})
			} else {
				wantAudit(t, events(), "file-download", "failure", map[string]any{"event.reason": "outside mount"})
			}
		})
	}
}

func TestCopyExperimentFileToMount(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{captures: []mm.Capture{
		{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/live.pcap"},
	}})

	base := useTestMount(t)

	tests := map[string]struct {
		query url.Values
		want  int
	}{
		"experiment file":       {url.Values{"source": {"a.txt"}, "path": {"etc"}}, http.StatusOK},
		"unclean source":        {url.Values{"source": {"./a.txt"}, "path": {"etc"}}, http.StatusBadRequest},
		"source outside files":  {url.Values{"source": {"../files/a.txt"}, "path": {"etc"}}, http.StatusBadRequest},
		"file still captured":   {url.Values{"source": {"live.pcap"}, "path": {"etc"}}, http.StatusBadRequest},
		"sibling mount":         {url.Values{"source": {"a.txt"}, "path": {"../test-vm2"}}, http.StatusBadRequest},
		"above the mount":       {url.Values{"source": {"a.txt"}, "path": {".."}}, http.StatusBadRequest},
		".. named directory":    {url.Values{"source": {"a.txt"}, "path": {"..foo"}}, http.StatusOK},
		"no source":             {url.Values{"path": {"etc"}}, http.StatusBadRequest},
		"missing file":          {url.Values{"source": {"gone.txt"}, "path": {"etc"}}, http.StatusBadRequest},
		"destination not a dir": {url.Values{"source": {"a.txt"}, "path": {"etc/hostname"}}, http.StatusBadRequest},
	}

	useTestFiles(t, "a.txt", "live.pcap")

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			resp := requestMount(t, http.MethodPost, "/copy", tc.query)
			if resp.StatusCode != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, resp.StatusCode, readBody(t, resp))
			}

			outcome := "failure"
			if tc.want == http.StatusOK {
				outcome = "success"
			}

			wantAudit(t, events(), "vm-file-copy", outcome, map[string]any{
				"user.name": "alice", "phenix.vm.name": "test-vm", "phenix.experiment.name": "test-experiment",
			})
		})
	}

	refused := serveAs(t, "alice", testRole("vms/mount", "*", "test-experiment/test-vm")).URL
	events := captureAudit(t)

	resp := do(t, http.MethodPost, refused+"/api/v1/experiments/test-experiment/vms/test-vm/files/copy?source=a.txt&path=etc", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("copying without reading experiment files: got %d, want 403", resp.StatusCode)
	}

	wantDenied(t, events(), "vm-file-copy")

	if b, err := os.ReadFile(filepath.Join(base, "etc", "a.txt")); err != nil || string(b) != "alpha" {
		t.Fatalf("copied file: %q, %v", b, err)
	}

	if _, err := os.Stat(filepath.Join(base, "..", "test-vm2", "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("copied into the sibling mount: %v", err)
	}
}

// gatedMount makes mounting wait on a gate, telling entered when one starts,
// and unmounting count its calls, for one test.
type gatedMount struct {
	entered  chan string
	gate     chan struct{}
	release  func()
	unmounts atomic.Int32
}

func useGatedMount(t *testing.T) *gatedMount {
	t.Helper()

	g := &gatedMount{entered: make(chan string, 8), gate: make(chan struct{})}
	g.release = sync.OnceFunc(func() { close(g.gate) })

	originalMount, originalUnmount := mountFilesystem, unmountFilesystem

	mountFilesystem = func(_, name string) error {
		g.entered <- name
		<-g.gate

		return nil
	}

	unmountFilesystem = func(string, string) error {
		g.unmounts.Add(1)

		return nil
	}

	t.Cleanup(func() {
		g.release()

		mountFilesystem, unmountFilesystem = originalMount, originalUnmount

		activeMountsMu.Lock()
		delete(activeMounts, mm.GetLocalMountPath("test-experiment", "test-vm2"))
		activeMountsMu.Unlock()
	})

	return g
}

// mountServer serves the API as a user who may use every test-experiment
// VM's mount.
func mountServer(t *testing.T) string {
	t.Helper()

	role := combinedRole(
		testRole("vms/mount", "*", "test-experiment/*"),
		testRole("experiments/files", "get", "test-experiment"),
	)

	return serveAs(t, "alice", role).URL + "/api/v1/experiments/test-experiment/vms/"
}

// mountStatus sends a request without failing the test, so it can run on
// its own goroutine, and returns its status code, or 0 if it failed.
func mountStatus(method, url string) int {
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		return 0
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	return resp.StatusCode
}

// mountStateOf returns the state of the mount at key, and whether there is one.
func mountStateOf(key string) (mountState, int, bool) {
	activeMountsMu.RLock()
	defer activeMountsMu.RUnlock()

	info, ok := activeMounts[key]
	if !ok {
		return 0, 0, false
	}

	return info.state, info.users, true
}

// While one VM is being mounted, another VM's mounted files can be listed,
// and a second request to mount the first waits for the same mount.
func TestMountingOneVMDoesNotBlockOthers(t *testing.T) {
	useTestMount(t)

	g := useGatedMount(t)
	api := mountServer(t)

	first, second := make(chan int, 1), make(chan int, 1)

	go func() { first <- mountStatus(http.MethodPost, api+"test-vm2/mount") }()

	polltest.Within(t, "the mount starting", func() { <-g.entered })

	go func() { second <- mountStatus(http.MethodPost, api+"test-vm2/mount") }()

	polltest.Within(t, "listing another VM's mounted files", func() {
		if code := mountStatus(http.MethodGet, api+"test-vm/files?path=etc"); code != http.StatusOK {
			t.Errorf("listing files: status %d, want 200", code)
		}
	})

	key := mm.GetLocalMountPath("test-experiment", "test-vm2")

	polltest.Until(t, "the second request joining the mount", func() bool {
		_, users, _ := mountStateOf(key)

		return users == 2
	})

	events := captureAudit(t)

	g.release()

	polltest.Within(t, "the mounts answering", func() {
		if a, b := <-first, <-second; a != http.StatusOK || b != http.StatusOK {
			t.Errorf("mount statuses = %d, %d; want 200, 200", a, b)
		}
	})

	// each user's mount, the one that waited for it included
	if n := countAudit(events(), "vm-mount", "success", map[string]any{"phenix.vm.name": "test-vm2"}); n != 2 {
		t.Errorf("%d mounts audited, want 2", n)
	}

	if len(g.entered) != 0 {
		t.Error("the VM was mounted twice")
	}

	if state, users, _ := mountStateOf(key); state != mountMounted || users != 2 {
		t.Errorf("mount state = %d with %d users, want mounted with 2", state, users)
	}
}

// An unmount waits for its own mount's downloads, not other mounts' requests,
// and mounting the VM again meanwhile is refused.
func TestUnmountWaitsOnlyForItsOwnDownloads(t *testing.T) {
	base := useTestMount(t)

	g := useGatedMount(t)
	g.release()

	api := mountServer(t)

	activeMountsMu.RLock()
	download := activeMounts[base].lock
	activeMountsMu.RUnlock()

	// a download in progress
	download.RLock()

	unmounted := make(chan int, 1)

	go func() { unmounted <- mountStatus(http.MethodDelete, api+"test-vm/unmount") }()

	polltest.Until(t, "the unmount starting", func() bool {
		state, _, _ := mountStateOf(base)

		return state == mountUnmounting
	})

	polltest.Within(t, "requests for another VM and this one", func() {
		if code := mountStatus(http.MethodPost, api+"test-vm2/mount"); code != http.StatusOK {
			t.Errorf("mounting another VM: status %d, want 200", code)
		}

		if code := mountStatus(http.MethodPost, api+"test-vm/mount"); code != http.StatusConflict {
			t.Errorf("mounting the VM being unmounted: status %d, want 409", code)
		}

		if code := mountStatus(http.MethodGet, api+"test-vm/files?path=etc"); code != http.StatusBadRequest {
			t.Errorf("listing files being unmounted: status %d, want 400", code)
		}
	})

	download.RUnlock()

	polltest.Within(t, "the unmount answering", func() {
		if code := <-unmounted; code != http.StatusOK {
			t.Errorf("unmount status %d, want 200", code)
		}
	})

	if _, _, ok := mountStateOf(base); ok || g.unmounts.Load() != 1 {
		t.Errorf("mount still listed = %v, unmounts = %d; want gone after 1", ok, g.unmounts.Load())
	}
}

// An unmount whose mount stays busy gives up with 409 and leaves the mount
// as it was.
func TestUnmountGivesUpOnABusyMount(t *testing.T) {
	base := useTestMount(t)

	g := useGatedMount(t)

	original := unmountWaitTimeout
	unmountWaitTimeout = 100 * time.Millisecond

	t.Cleanup(func() { unmountWaitTimeout = original })

	activeMountsMu.RLock()
	download := activeMounts[base].lock
	activeMountsMu.RUnlock()

	download.RLock()
	defer download.RUnlock()

	api := mountServer(t)
	events := captureAudit(t)

	polltest.Within(t, "the unmount giving up", func() {
		if code := mountStatus(http.MethodDelete, api+"test-vm/unmount"); code != http.StatusConflict {
			t.Errorf("unmount status %d, want 409", code)
		}
	})

	wantAudit(t, events(), "vm-unmount", "failure", map[string]any{"error.message": "mount busy"})

	if state, users, ok := mountStateOf(base); !ok || state != mountMounted || users != 1 {
		t.Errorf("mount state = %d with %d users (listed %v), want mounted with 1", state, users, ok)
	}

	if g.unmounts.Load() != 0 {
		t.Error("unmounted a busy mount")
	}
}

// A mount download answers HEAD and resumes with a range against its ETag.
func TestDownloadMountFileResumes(t *testing.T) {
	useTestMount(t)

	query := url.Values{"path": {"etc/hostname"}}

	head := requestMount(t, http.MethodHead, "/download", query)
	etag := head.Header.Get("ETag")

	if head.StatusCode != http.StatusOK || etag == "" || head.Header.Get("Content-Length") != "7" {
		t.Fatalf("HEAD: status %d, ETag %q, Content-Length %q", head.StatusCode, etag, head.Header.Get("Content-Length"))
	}

	server := serveAs(t, "alice", mountRole()).URL
	resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/vms/test-vm/files/download?"+query.Encode(), "",
		"Range", "bytes=5-", "If-Range", etag)

	if body := readBody(t, resp); resp.StatusCode != http.StatusPartialContent || body != "vm" {
		t.Fatalf("resume: status %d, body %q; want 206 vm", resp.StatusCode, body)
	}

	for path, want := range map[string]int{"etc": http.StatusBadRequest, "etc/missing": http.StatusNotFound} {
		if resp := requestMount(t, http.MethodGet, "/download", url.Values{"path": {path}}); resp.StatusCode != want {
			t.Errorf("%s: status %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// Mounting and unmounting a VM are audited, refusals included.
func TestMountAndUnmountAreAudited(t *testing.T) {
	useTestMount(t)

	g := useGatedMount(t)
	g.release()

	api := mountServer(t)
	events := captureAudit(t)

	for _, req := range []struct{ method, route string }{
		{http.MethodPost, "test-vm2/mount"},
		{http.MethodDelete, "test-vm2/unmount"},
	} {
		if code := mountStatus(req.method, api+req.route); code != http.StatusOK {
			t.Fatalf("%s %s: status %d", req.method, req.route, code)
		}
	}

	if len(g.entered) != 1 || g.unmounts.Load() != 1 {
		t.Fatalf("mounted %d times, unmounted %d times", len(g.entered), g.unmounts.Load())
	}

	for _, action := range []string{"vm-mount", "vm-unmount"} {
		wantAudit(t, events(), action, "success", map[string]any{
			"user.name": "alice", "phenix.vm.name": "test-vm2", "phenix.experiment.name": "test-experiment",
		})
	}

	refused := serveAs(t, "alice", testRole("vms/mount", "get", "test-experiment/*")).URL +
		"/api/v1/experiments/test-experiment/vms/test-vm2/"

	for _, req := range []struct{ method, route, action string }{
		{http.MethodPost, "mount", "vm-mount"},
		{http.MethodDelete, "unmount", "vm-unmount"},
	} {
		if code := mountStatus(req.method, refused+req.route); code != http.StatusForbidden {
			t.Fatalf("%s %s without permission: status %d", req.method, req.route, code)
		}

		wantDenied(t, events(), req.action)
	}
}

// An upload without a file, or into a path that is not a folder, is answered
// with the error rather than crashing the request, and an upload is audited.
func TestUploadMountFile(t *testing.T) {
	base := useTestMount(t)
	server := serveAs(t, "alice", mountRole()).URL + "/api/v1/experiments/test-experiment/vms/test-vm/files/upload?path="

	upload := func(t *testing.T, path, field string) *http.Response {
		t.Helper()

		body, contentType := formUpload(t, field, "notes.txt", "uploaded")

		return do(t, http.MethodPut, server+url.QueryEscape(path), string(body), "Content-Type", contentType)
	}

	for name, tc := range map[string]struct{ path, field string }{
		"no file":      {path: "etc", field: "other"},
		"not a folder": {path: "etc/hostname", field: "file"},
		"missing":      {path: "gone", field: "file"},
	} {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			resp := upload(t, tc.path, tc.field)
			if body := readBody(t, resp); resp.StatusCode != http.StatusInternalServerError ||
				!strings.HasPrefix(body, "Error") {
				t.Fatalf("status %d: %s; want 500 with the error", resp.StatusCode, body)
			}

			wantAudit(t, events(), "vm-file-upload", "failure", map[string]any{"user.name": "alice"})
		})
	}

	// an upload replaces a longer file of the same name whole
	if err := os.WriteFile(filepath.Join(base, "etc", "notes.txt"), []byte("an older, longer file"), 0o600); err != nil {
		t.Fatal(err)
	}

	events := captureAudit(t)

	if resp := upload(t, "etc", "file"); resp.StatusCode != http.StatusOK {
		t.Fatalf("uploading: status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	if b, err := os.ReadFile(filepath.Join(base, "etc", "notes.txt")); err != nil || string(b) != "uploaded" {
		t.Fatalf("uploaded file: %q, %v", b, err)
	}

	wantAudit(t, events(), "vm-file-upload", "success", map[string]any{
		"user.name": "alice", "phenix.file.target_path": filepath.Join(base, "etc", "notes.txt"), "file.size": 8,
	})
}

// A guest symlink leading out of the mount is not followed by listing, download, upload or copy;
// one inside is.
func TestMountFileRequestsDoNotFollowSymlinksOut(t *testing.T) {
	useTestExperiment(t)
	useTestMM(t, &testMM{})
	useTestFiles(t, "a.txt")

	base := useTestMount(t)
	sibling := filepath.Join(base, "..", "test-vm2")

	for link, target := range map[string]string{
		"abs-secret": filepath.Join(sibling, "secret"),
		"rel-secret": "../test-vm2/secret",
		"abs-dir":    sibling,
		"rel-dir":    "../test-vm2",
		"inside":     "etc/hostname",
	} {
		if err := os.Symlink(target, filepath.Join(base, link)); err != nil {
			t.Fatal(err)
		}
	}

	for _, path := range []string{"abs-secret", "rel-secret"} {
		resp := requestMount(t, http.MethodGet, "/download", url.Values{"path": {path}})
		if body := readBody(t, resp); resp.StatusCode == http.StatusOK || strings.Contains(body, "not yours") {
			t.Errorf("downloading %s: status %d: %s", path, resp.StatusCode, body)
		}
	}

	if resp := requestMount(t, http.MethodGet, "/download", url.Values{"path": {"inside"}}); resp.StatusCode != http.StatusOK ||
		readBody(t, resp) != "test-vm" {
		t.Errorf("downloading a symlink inside the mount: status %d", resp.StatusCode)
	}

	for _, path := range []string{"abs-dir", "rel-dir"} {
		resp := requestMount(t, http.MethodGet, "", url.Values{"path": {path}})
		if body := readBody(t, resp); resp.StatusCode == http.StatusOK || strings.Contains(body, "secret") {
			t.Errorf("listing %s: status %d: %s", path, resp.StatusCode, body)
		}

		resp = requestMount(t, http.MethodPost, "/copy", url.Values{"source": {"a.txt"}, "path": {path}})
		if resp.StatusCode == http.StatusOK {
			t.Errorf("copying into %s: status %d", path, resp.StatusCode)
		}

		body, contentType := formUpload(t, "file", "notes.txt", "uploaded")

		server := serveAs(t, "alice", mountRole()).URL
		resp = do(t, http.MethodPut,
			server+"/api/v1/experiments/test-experiment/vms/test-vm/files/upload?path="+path,
			string(body), "Content-Type", contentType)

		if resp.StatusCode == http.StatusOK {
			t.Errorf("uploading into %s: status %d", path, resp.StatusCode)
		}
	}

	entries, err := os.ReadDir(sibling)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("wrote into the sibling mount: %v", entries)
	}
}

// An upload outside the mount is refused the same way whether or not the path
// exists, so it does not tell which server paths exist (as listing does not).
func TestMountPathsOutsideTellNothing(t *testing.T) {
	base := useTestMount(t)
	server := serveAs(t, "alice", mountRole()).URL + "/api/v1/experiments/test-experiment/vms/test-vm/files"

	for _, path := range []string{"../test-vm2", "../no-such-vm"} {
		resp := do(t, http.MethodPut, server+"/upload?path="+url.QueryEscape(path), "")
		if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("uploading to %s: status %d: %s, want 400", path, resp.StatusCode, body)
		}
	}

	if _, err := os.Stat(filepath.Join(base, "..", "test-vm2")); err != nil {
		t.Fatal(err)
	}
}
