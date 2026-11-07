package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"

	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/polltest"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
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
			t.Fatalf("making the folder of mounted file %s: %v", p, err)
		}

		if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
			t.Fatalf("writing mounted file %s: %v", p, err)
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
				t.Fatalf("GET files%s?path=%s = %d, want %d: %s", tc.route, tc.path, resp.StatusCode, tc.want, readBody(t, resp))
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
				t.Fatalf("POST files/copy?%s = %d, want %d: %s", tc.query.Encode(), resp.StatusCode, tc.want, readBody(t, resp))
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
		t.Errorf("POST files/copy without experiments/files get = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	wantDenied(t, events(), "vm-file-copy")

	if b, err := os.ReadFile(filepath.Join(base, "etc", "a.txt")); err != nil {
		t.Errorf("reading the copied etc/a.txt: %v", err)
	} else if got, want := string(b), "alpha"; got != want {
		t.Errorf("copied etc/a.txt = %q, want %q", got, want)
	}

	if _, err := os.Stat(filepath.Join(base, "..", "test-vm2", "a.txt")); !os.IsNotExist(err) {
		t.Errorf("os.Stat(test-vm2/a.txt) error = %v, want it not to exist", err)
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

// mountServerRole may use every test-experiment VM's mount, as mountServer's user may.
func mountServerRole() rbac.Role {
	return combinedRole(
		testRole("vms/mount", "*", "test-experiment/*"),
		testRole("experiments/files", "get", "test-experiment"),
	)
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
			t.Errorf("GET test-vm/files?path=etc while test-vm2 mounts = %d, want %d", code, http.StatusOK)
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
		for name, ch := range map[string]chan int{"first": first, "second": second} {
			if code := <-ch; code != http.StatusOK {
				t.Errorf("%s POST test-vm2/mount = %d, want %d", name, code, http.StatusOK)
			}
		}
	})

	// each user's mount, the one that waited for it included
	if n := countAudit(events(), "vm-mount", "success", map[string]any{"phenix.vm.name": "test-vm2"}); n != 2 {
		t.Errorf("vm-mount success audit events of test-vm2 = %d, want 2", n)
	}

	if n := len(g.entered); n != 0 {
		t.Errorf("test-vm2 mounted %d more times, want once", n)
	}

	state, users, _ := mountStateOf(key)
	if state != mountMounted {
		t.Errorf("test-vm2 mount state = %d, want %d (mounted)", state, mountMounted)
	}

	if users != 2 {
		t.Errorf("test-vm2 mount users = %d, want 2", users)
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
			t.Errorf("POST test-vm2/mount while test-vm unmounts = %d, want %d", code, http.StatusOK)
		}

		if code := mountStatus(http.MethodPost, api+"test-vm/mount"); code != http.StatusConflict {
			t.Errorf("POST test-vm/mount while it unmounts = %d, want %d", code, http.StatusConflict)
		}

		if code := mountStatus(http.MethodGet, api+"test-vm/files?path=etc"); code != http.StatusBadRequest {
			t.Errorf("GET test-vm/files?path=etc while it unmounts = %d, want %d", code, http.StatusBadRequest)
		}
	})

	download.RUnlock()

	polltest.Within(t, "the unmount answering", func() {
		if code := <-unmounted; code != http.StatusOK {
			t.Errorf("DELETE test-vm/unmount = %d, want %d", code, http.StatusOK)
		}
	})

	if _, _, ok := mountStateOf(base); ok {
		t.Error("test-vm mount is still listed after DELETE test-vm/unmount, want it gone")
	}

	if n := g.unmounts.Load(); n != 1 {
		t.Errorf("DELETE test-vm/unmount unmounted %d times, want 1", n)
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
			t.Errorf("DELETE test-vm/unmount of a busy mount = %d, want %d", code, http.StatusConflict)
		}
	})

	wantAudit(t, events(), "vm-unmount", "failure", map[string]any{"error.message": "mount busy"})

	state, users, ok := mountStateOf(base)
	if !ok {
		t.Fatal("test-vm mount is not listed after a refused unmount, want it kept")
	}

	if state != mountMounted {
		t.Errorf("test-vm mount state after a refused unmount = %d, want %d (mounted)", state, mountMounted)
	}

	if users != 1 {
		t.Errorf("test-vm mount users after a refused unmount = %d, want 1", users)
	}

	if n := g.unmounts.Load(); n != 0 {
		t.Errorf("DELETE test-vm/unmount of a busy mount unmounted %d times, want 0", n)
	}
}

// A mount download answers HEAD and resumes with a range against its ETag.
func TestDownloadMountFileResumes(t *testing.T) {
	useTestMount(t)

	query := url.Values{"path": {"etc/hostname"}}

	head := requestMount(t, http.MethodHead, "/download", query)
	etag := head.Header.Get("ETag")

	if head.StatusCode != http.StatusOK {
		t.Fatalf("HEAD files/download?%s = %d, want %d", query.Encode(), head.StatusCode, http.StatusOK)
	}

	if etag == "" {
		t.Fatalf("HEAD files/download?%s has no ETag, want one", query.Encode())
	}

	if got, want := head.Header.Get("Content-Length"), "7"; got != want {
		t.Errorf("HEAD files/download?%s Content-Length = %q, want %q", query.Encode(), got, want)
	}

	server := serveAs(t, "alice", mountRole()).URL
	resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/vms/test-vm/files/download?"+query.Encode(), "",
		"Range", "bytes=5-", "If-Range", etag)

	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("GET files/download?%s with Range bytes=5- = %d, want %d", query.Encode(), resp.StatusCode, http.StatusPartialContent)
	}

	if got, want := readBody(t, resp), "vm"; got != want {
		t.Errorf("GET files/download?%s with Range bytes=5- = %q, want %q", query.Encode(), got, want)
	}

	for path, want := range map[string]int{"etc": http.StatusBadRequest, "etc/missing": http.StatusNotFound} {
		if resp := requestMount(t, http.MethodGet, "/download", url.Values{"path": {path}}); resp.StatusCode != want {
			t.Errorf("GET files/download?path=%s = %d, want %d", path, resp.StatusCode, want)
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
			t.Errorf("%s %s = %d, want %d", req.method, req.route, code, http.StatusOK)
		}
	}

	if n := len(g.entered); n != 1 {
		t.Errorf("test-vm2 mounted %d times, want 1", n)
	}

	if n := g.unmounts.Load(); n != 1 {
		t.Errorf("test-vm2 unmounted %d times, want 1", n)
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
			t.Errorf("%s %s without vms/mount %s = %d, want %d", req.method, req.route, req.method, code, http.StatusForbidden)
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
			body := readBody(t, resp)

			if resp.StatusCode != http.StatusInternalServerError {
				t.Errorf("PUT files/upload?path=%s with field %s = %d, want %d",
					tc.path, tc.field, resp.StatusCode, http.StatusInternalServerError)
			}

			if !strings.HasPrefix(errorMessage(t, resp.StatusCode, body), "Error") {
				t.Errorf("PUT files/upload?path=%s with field %s = %q, want the error", tc.path, tc.field, body)
			}

			wantAudit(t, events(), "vm-file-upload", "failure", map[string]any{"user.name": "alice"})
		})
	}

	// an upload replaces a longer file of the same name whole
	if err := os.WriteFile(filepath.Join(base, "etc", "notes.txt"), []byte("an older, longer file"), 0o600); err != nil {
		t.Fatalf("writing etc/notes.txt: %v", err)
	}

	events := captureAudit(t)

	if resp := upload(t, "etc", "file"); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT files/upload?path=etc = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	if b, err := os.ReadFile(filepath.Join(base, "etc", "notes.txt")); err != nil {
		t.Errorf("reading the uploaded etc/notes.txt: %v", err)
	} else if got, want := string(b), "uploaded"; got != want {
		t.Errorf("uploaded etc/notes.txt = %q, want %q", got, want)
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
			t.Fatalf("os.Symlink(%s, %s) error = %v", target, link, err)
		}
	}

	for _, path := range []string{"abs-secret", "rel-secret"} {
		resp := requestMount(t, http.MethodGet, "/download", url.Values{"path": {path}})
		body := readBody(t, resp)

		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET files/download?path=%s = %d, want an error", path, resp.StatusCode)
		}

		if strings.Contains(body, "not yours") {
			t.Errorf("GET files/download?path=%s = %q, want no sibling file contents", path, body)
		}
	}

	inside := requestMount(t, http.MethodGet, "/download", url.Values{"path": {"inside"}})
	if inside.StatusCode != http.StatusOK {
		t.Errorf("GET files/download?path=inside = %d, want %d", inside.StatusCode, http.StatusOK)
	}

	if got, want := readBody(t, inside), "test-vm"; got != want {
		t.Errorf("GET files/download?path=inside = %q, want %q", got, want)
	}

	for _, path := range []string{"abs-dir", "rel-dir"} {
		resp := requestMount(t, http.MethodGet, "", url.Values{"path": {path}})
		body := readBody(t, resp)

		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET files?path=%s = %d, want an error", path, resp.StatusCode)
		}

		if strings.Contains(body, "secret") {
			t.Errorf("GET files?path=%s = %q, want no sibling listing", path, body)
		}

		resp = requestMount(t, http.MethodPost, "/copy", url.Values{"source": {"a.txt"}, "path": {path}})
		if resp.StatusCode == http.StatusOK {
			t.Errorf("POST files/copy?path=%s = %d, want an error", path, resp.StatusCode)
		}

		form, contentType := formUpload(t, "file", "notes.txt", "uploaded")

		server := serveAs(t, "alice", mountRole()).URL
		resp = do(t, http.MethodPut,
			server+"/api/v1/experiments/test-experiment/vms/test-vm/files/upload?path="+path,
			string(form), "Content-Type", contentType)

		if resp.StatusCode == http.StatusOK {
			t.Errorf("PUT files/upload?path=%s = %d, want an error", path, resp.StatusCode)
		}
	}

	entries, err := os.ReadDir(sibling)
	if err != nil {
		t.Fatalf("os.ReadDir(test-vm2) error = %v", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if diff := cmp.Diff([]string{"secret"}, names); diff != "" {
		t.Errorf("test-vm2 mount entries mismatch (-want +got):\n%s", diff)
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
			t.Errorf("PUT files/upload?path=%s = %d, want %d: %s", path, resp.StatusCode, http.StatusBadRequest, body)
		}
	}

	if _, err := os.Stat(filepath.Join(base, "..", "test-vm2")); err != nil {
		t.Errorf("os.Stat(test-vm2) after refused uploads error = %v, want the mount kept", err)
	}
}

// mountServe serves a request for VM name of test-experiment, with query, to
// the handler directly, as alice with role, in ctx.
func mountServe(
	ctx context.Context, h func(http.ResponseWriter, *http.Request) error, role rbac.Role, name, query string,
) *httptest.ResponseRecorder {
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "alice")

	values, _ := url.ParseQuery(query)

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/experiments/test-experiment/vms/"+name+"?"+query, nil)
	req = mux.SetURLVars(req, map[string]string{"exp": "test-experiment", "name": name, "path": values.Get("path")})

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(h).ServeHTTP(rec, req)

	return rec
}

// useFailingMount makes mounting and unmounting fail with err for one test,
// mounting only once gate is closed (nil: at once).
func useFailingMount(t *testing.T, err error, gate chan struct{}) {
	t.Helper()

	originalMount, originalUnmount := mountFilesystem, unmountFilesystem

	mountFilesystem = func(string, string) error {
		if gate != nil {
			<-gate
		}

		return err
	}

	unmountFilesystem = func(string, string) error { return err }

	t.Cleanup(func() { mountFilesystem, unmountFilesystem = originalMount, originalUnmount })
}

// A mount that fails answers 500 to its request and to one waiting for it,
// is audited as failed, and leaves no mount behind.
func TestMountVMFails(t *testing.T) {
	useTestMount(t)

	gate := make(chan struct{})
	useFailingMount(t, errors.New("guestmount: no such disk"), gate)

	key := mm.GetLocalMountPath("test-experiment", "test-vm2")
	role := testRole("vms/mount", "*", "test-experiment/*")
	events := captureAudit(t)

	first, second := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)

	go func() { first <- mountServe(context.Background(), MountVM, role, "test-vm2", "") }()

	polltest.Until(t, "the first mount starting", func() bool {
		state, _, ok := mountStateOf(key)

		return ok && state == mountMounting
	})

	go func() { second <- mountServe(context.Background(), MountVM, role, "test-vm2", "") }()

	polltest.Until(t, "the second request joining the mount", func() bool {
		_, users, _ := mountStateOf(key)

		return users == 2
	})

	close(gate)

	for name, ch := range map[string]chan *httptest.ResponseRecorder{"first": first, "second": second} {
		var rec *httptest.ResponseRecorder

		polltest.Within(t, name+" mount answering", func() { rec = <-ch })

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s POST test-vm2/mount of a failing mount = %d, want %d", name, rec.Code, http.StatusInternalServerError)
		}

		if got, want := errorMessage(t, rec.Code, rec.Body.String()), "Error mounting: guestmount: no such disk"; got != want {
			t.Errorf("%s POST test-vm2/mount of a failing mount = %q, want %q", name, got, want)
		}
	}

	if n := countAudit(events(), "vm-mount", "failure", map[string]any{"phenix.vm.name": "test-vm2"}); n != 2 {
		t.Errorf("vm-mount failure audit events of test-vm2 = %d, want 2", n)
	}

	if _, _, ok := mountStateOf(key); ok {
		t.Error("test-vm2 mount is listed after it failed, want it gone")
	}
}

// A request waiting for another's mount whose client leaves stops counting as
// a user of the mount and writes nothing.
func TestMountVMWaiterLeaves(t *testing.T) {
	useTestMount(t)

	g := useGatedMount(t)
	key := mm.GetLocalMountPath("test-experiment", "test-vm2")
	role := testRole("vms/mount", "*", "test-experiment/*")

	first := make(chan *httptest.ResponseRecorder, 1)

	go func() { first <- mountServe(context.Background(), MountVM, role, "test-vm2", "") }()

	polltest.Within(t, "the mount starting", func() { <-g.entered })

	ctx, cancel := context.WithCancel(t.Context())
	left := make(chan *httptest.ResponseRecorder, 1)

	go func() { left <- mountServe(ctx, MountVM, role, "test-vm2", "") }()

	polltest.Until(t, "the second request joining the mount", func() bool {
		_, users, _ := mountStateOf(key)

		return users == 2
	})

	cancel()

	var rec *httptest.ResponseRecorder

	polltest.Within(t, "the request whose client left returning", func() { rec = <-left })

	if rec.Body.Len() != 0 {
		t.Errorf("POST test-vm2/mount whose client left wrote %q, want nothing", rec.Body.String())
	}

	if _, users, _ := mountStateOf(key); users != 1 {
		t.Errorf("test-vm2 mount users after a waiting client left = %d, want 1", users)
	}

	g.release()

	polltest.Within(t, "the first mount answering", func() { rec = <-first })

	if rec.Code != http.StatusOK {
		t.Errorf("POST test-vm2/mount = %d, want %d", rec.Code, http.StatusOK)
	}
}

// An unmount of a VM that is not mounted succeeds, one of a mount in
// progress is refused, one of a mount others still use only drops the user,
// and one that fails keeps the mount.
func TestUnmountVMStates(t *testing.T) {
	base := useTestMount(t)
	role := testRole("vms/mount", "*", "test-experiment/*")

	useFailingMount(t, errors.New("fusermount: device busy"), nil)

	setMount := func(state mountState, users int) {
		activeMountsMu.Lock()
		activeMounts[base].state, activeMounts[base].users = state, users
		activeMountsMu.Unlock()
	}

	tests := []struct {
		name      string
		vm        string
		state     mountState
		users     int
		wantCode  int
		wantState mountState
		wantUsers int
		wantAudit string
	}{
		{"not mounted", "test-vm2", mountMounted, 1, http.StatusOK, mountMounted, 1, ""},
		{"mounting", "test-vm", mountMounting, 1, http.StatusConflict, mountMounting, 1, ""},
		{"unmounting", "test-vm", mountUnmounting, 1, http.StatusConflict, mountUnmounting, 1, ""},
		{"other users", "test-vm", mountMounted, 3, http.StatusOK, mountMounted, 2, "success"},
		{"unmount fails", "test-vm", mountMounted, 1, http.StatusInternalServerError, mountMounted, 0, "failure"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setMount(tc.state, tc.users)

			events := captureAudit(t)

			rec := mountServe(t.Context(), UnmountVM, role, tc.vm, "")
			if rec.Code != tc.wantCode {
				t.Errorf("DELETE %s/unmount = %d, want %d: %s", tc.vm, rec.Code, tc.wantCode, rec.Body.String())
			}

			state, users, ok := mountStateOf(base)
			if !ok {
				t.Fatal("test-vm mount is not listed, want it kept")
			}

			if state != tc.wantState || users != tc.wantUsers {
				t.Errorf("test-vm mount after DELETE %s/unmount = state %d, %d users, want state %d, %d users",
					tc.vm, state, users, tc.wantState, tc.wantUsers)
			}

			want, outcomes := 1, []string{tc.wantAudit}
			if tc.wantAudit == "" {
				want, outcomes = 0, []string{"success", "failure"}
			}

			got := 0
			for _, outcome := range outcomes {
				got += countAudit(events(), "vm-unmount", outcome, map[string]any{"phenix.vm.name": tc.vm})
			}

			if got != want {
				t.Errorf("vm-unmount %v audit events = %d, want %d", outcomes, got, want)
			}
		})
	}

	activeMountsMu.RLock()
	lock := activeMounts[base].lock
	activeMountsMu.RUnlock()

	// a failed unmount let its lock go
	if !lock.TryLock() {
		t.Fatal("test-vm mount lock is held after a failed unmount, want it free")
	}

	lock.Unlock()
}

// Listing a mounted folder answers its entries, paths relative to the mount;
// other requests are refused.
func TestGetMountFiles(t *testing.T) {
	useTestMount(t)

	rec := mountServe(t.Context(), GetMountFiles, mountRole(), "test-vm", "path=etc")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET test-vm/files?path=etc = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var listing struct {
		Error string `json:"error"`
		Files []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			Size  int64  `json:"size"`
			IsDir bool   `json:"isDir"`
		} `json:"files"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("GET test-vm/files?path=etc = %s, not a listing: %v", rec.Body.String(), err)
	}

	want := listing
	want.Error = ""
	want.Files = append(want.Files[:0:0], struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		IsDir bool   `json:"isDir"`
	}{"hostname", "/etc/hostname", int64(len("test-vm")), false})

	if diff := cmp.Diff(want, listing); diff != "" {
		t.Errorf("GET test-vm/files?path=etc mismatch (-want +got):\n%s", diff)
	}

	for name, tc := range map[string]struct {
		role        rbac.Role
		vm, query   string
		wantCode    int
		wantMessage string
	}{
		"forbidden": {testRole("vms/mount", "get", "test-experiment/test-vm"), "test-vm", "path=etc",
			http.StatusForbidden, "forbidden"},
		"not mounted": {mountServerRole(), "test-vm2", "path=etc", http.StatusBadRequest, "No existing mount for "},
		"a file":      {mountRole(), "test-vm", "path=etc/hostname", http.StatusBadRequest, "Expected directory not file: "},
		"missing":     {mountRole(), "test-vm", "path=gone", http.StatusInternalServerError, "Error getting path "},
	} {
		t.Run(name, func(t *testing.T) {
			rec := mountServe(t.Context(), GetMountFiles, tc.role, tc.vm, tc.query)
			if rec.Code != tc.wantCode {
				t.Errorf("GET %s/files?%s = %d, want %d", tc.vm, tc.query, rec.Code, tc.wantCode)
			}

			if got := errorMessage(t, rec.Code, rec.Body.String()); !strings.HasPrefix(got, tc.wantMessage) {
				t.Errorf("GET %s/files?%s = %q, want it to start with %q", tc.vm, tc.query, got, tc.wantMessage)
			}
		})
	}
}

// Downloads, uploads and copies are refused without permission, audited, and
// refused for a VM that is not mounted.
func TestMountFileRequestsRefused(t *testing.T) {
	useTestMount(t)

	handlers := map[string]struct {
		h      func(http.ResponseWriter, *http.Request) error
		action string
	}{
		"download": {DownloadMountFile, "file-download"},
		"upload":   {UploadMountFile, "vm-file-upload"},
		"copy":     {CopyExperimentFileToMount, "vm-file-copy"},
	}

	for name, tc := range handlers {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			rec := mountServe(t.Context(), tc.h, testRole("vms/mount", "list", "test-experiment/test-vm"), "test-vm", "path=etc")
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s test-vm path=etc without permission = %d, want %d", name, rec.Code, http.StatusForbidden)
			}

			wantDenied(t, events(), tc.action)

			rec = mountServe(t.Context(), tc.h, mountServerRole(), "test-vm2", "path=etc")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s test-vm2 path=etc, not mounted = %d, want %d", name, rec.Code, http.StatusBadRequest)
			}

			if got := errorMessage(t, rec.Code, rec.Body.String()); !strings.HasPrefix(got, "No existing mount for ") {
				t.Errorf("%s test-vm2 path=etc, not mounted = %q, want No existing mount", name, got)
			}
		})
	}
}

// A copy into a folder the mount does not have, or onto a folder of the
// source's name, fails, audited.
func TestCopyExperimentFileToMountFails(t *testing.T) {
	base := useTestMount(t)

	if err := os.MkdirAll(filepath.Join(base, "etc", "a.txt"), 0o750); err != nil {
		t.Fatalf("making the folder etc/a.txt: %v", err)
	}

	for name, tc := range map[string]struct{ query, wantMessage string }{
		"missing folder":            {"source=a.txt&path=gone", "Error getting path "},
		"a folder of the same name": {"source=a.txt&path=etc", "Error creating destination file: "},
	} {
		t.Run(name, func(t *testing.T) {
			events := captureAudit(t)

			rec := mountServe(t.Context(), CopyExperimentFileToMount, mountRole(), "test-vm", tc.query)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("POST files/copy?%s = %d, want %d", tc.query, rec.Code, http.StatusInternalServerError)
			}

			if got := errorMessage(t, rec.Code, rec.Body.String()); !strings.HasPrefix(got, tc.wantMessage) {
				t.Errorf("POST files/copy?%s = %q, want it to start with %q", tc.query, got, tc.wantMessage)
			}

			wantAudit(t, events(), "vm-file-copy", "failure", map[string]any{"phenix.vm.name": "test-vm"})
		})
	}
}

// Mounting a VM that is mounted already adds a user and succeeds at once.
func TestMountVMJoinsAMountedVM(t *testing.T) {
	base := useTestMount(t)
	useFailingMount(t, errors.New("mounted again"), nil)

	events := captureAudit(t)

	if rec := mountServe(t.Context(), MountVM, mountRole(), "test-vm", ""); rec.Code != http.StatusOK {
		t.Errorf("POST test-vm/mount of a mounted VM = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if state, users, _ := mountStateOf(base); state != mountMounted || users != 2 {
		t.Errorf("test-vm mount after a second mount = state %d, %d users, want state %d, 2 users", state, users, mountMounted)
	}

	wantAudit(t, events(), "vm-mount", "success", map[string]any{"user.name": "alice", "phenix.vm.name": "test-vm"})
}

// A download from the first byte is audited with the range it asked for.
func TestDownloadMountFileAuditsRange(t *testing.T) {
	useTestMount(t)

	server := serveAs(t, "alice", mountRole()).URL
	events := captureAudit(t)

	resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/vms/test-vm/files/download?path=etc/hostname", "",
		"Range", "bytes=0-3")
	if got := readBody(t, resp); resp.StatusCode != http.StatusPartialContent || got != "test" {
		t.Errorf("GET files/download?path=etc/hostname Range bytes=0-3 = %d %q, want %d %q",
			resp.StatusCode, got, http.StatusPartialContent, "test")
	}

	wantAudit(t, events(), "file-download", "success", map[string]any{"http.request.range": "bytes=0-3"})
}

func TestLockMountForFiles(t *testing.T) {
	const key = "/lock-mount-for-files-test"

	for name, tc := range map[string]struct {
		info *MountInfo
		// whether an unmount holds the lock
		locked bool
		want   bool
	}{
		"no mount": {nil, false, false},
		"mounting": {&MountInfo{state: mountMounting, lock: &sync.RWMutex{}}, false, false},
		"unmounting": {
			&MountInfo{state: mountUnmounting, lock: &sync.RWMutex{}}, false, false,
		},
		"mounted":                {&MountInfo{state: mountMounted, lock: &sync.RWMutex{}}, false, true},
		"mounted, unmount holds": {&MountInfo{state: mountMounted, lock: &sync.RWMutex{}}, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.info != nil {
				if tc.locked {
					tc.info.lock.Lock()
					defer tc.info.lock.Unlock()
				}

				activeMountsMu.Lock()
				activeMounts[key] = tc.info
				activeMountsMu.Unlock()

				t.Cleanup(func() {
					activeMountsMu.Lock()
					delete(activeMounts, key)
					activeMountsMu.Unlock()
				})
			}

			info, ok := lockMountForFiles(key)
			if ok != tc.want || (ok && info != tc.info) {
				t.Fatalf("lockMountForFiles(%s mount) = %v, %t, want the mount: %t", name, info, ok, tc.want)
			}

			if !ok {
				return
			}

			// read-locked: an unmount cannot take it, another file request can
			if info.lock.TryLock() {
				t.Error("lockMountForFiles() left the mount unlocked, want it read-locked")
			}

			if !info.lock.TryRLock() {
				t.Error("lockMountForFiles() locked the mount for writing, want it read-locked")
			} else {
				info.lock.RUnlock()
			}

			info.lock.RUnlock()
		})
	}
}

func TestLockMountForUnmount(t *testing.T) {
	original := unmountWaitTimeout
	t.Cleanup(func() { unmountWaitTimeout = original })

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := map[string]struct {
		ctx     context.Context //nolint:containedctx // a table of contexts
		timeout time.Duration
		// how long a file request holds the mount, 0 for none, -1 for ever
		held time.Duration
		want bool
	}{
		"free":                    {t.Context(), time.Minute, 0, true},
		"freed while waiting":     {t.Context(), time.Minute, 120 * time.Millisecond, true},
		"busy past the timeout":   {t.Context(), 100 * time.Millisecond, -1, false},
		"busy, client left":       {canceled, time.Minute, -1, false},
		"free, client left first": {canceled, time.Minute, 0, true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			unmountWaitTimeout = tc.timeout

			info := &MountInfo{state: mountUnmounting, lock: &sync.RWMutex{}}

			if tc.held != 0 {
				info.lock.RLock()

				if tc.held > 0 {
					time.AfterFunc(tc.held, info.lock.RUnlock)
				} else {
					defer info.lock.RUnlock()
				}
			}

			var got bool

			polltest.Within(t, "lockMountForUnmount returning", func() { got = lockMountForUnmount(tc.ctx, info) })

			if got != tc.want {
				t.Fatalf("lockMountForUnmount(%s) = %t, want %t", name, got, tc.want)
			}

			if got {
				// it holds the lock: no file request can take it
				if info.lock.TryRLock() {
					t.Error("lockMountForUnmount() = true without holding the lock")
				}

				info.lock.Unlock()
			}
		})
	}
}

// mountTree is a mount holding etc/hostname, beside a folder outside it
// holding secret, which the mount's escape symlink names.
func mountTree(tb testing.TB) string {
	tb.Helper()

	dir := tb.TempDir()
	base := filepath.Join(dir, "mount")

	for p, contents := range map[string]string{
		filepath.Join(base, "etc", "hostname"):  "test-vm",
		filepath.Join(dir, "outside", "secret"): "not yours",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			tb.Fatalf("making the folder of %s: %v", p, err)
		}

		if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
			tb.Fatalf("writing %s: %v", p, err)
		}
	}

	if err := os.Symlink(filepath.Join(dir, "outside"), filepath.Join(base, "escape")); err != nil {
		tb.Fatalf("os.Symlink(outside, escape) error = %v", err)
	}

	return base
}

// openInMount and statInMount reach the mount's own files, and not those a
// symlink leads out to or a mount that is not there; a missing file is
// [fs.ErrNotExist], as the download handler needs.
func TestOpenAndStatInMount(t *testing.T) {
	t.Parallel()

	base := mountTree(t)

	tests := map[string]struct {
		base, path string
		wantErr    bool
		notExist   bool
		wantDir    bool
		contents   string
	}{
		"file":            {base: base, path: filepath.Join(base, "etc", "hostname"), contents: "test-vm"},
		"folder":          {base: base, path: filepath.Join(base, "etc"), wantDir: true},
		"missing":         {base: base, path: filepath.Join(base, "gone"), wantErr: true, notExist: true},
		"through symlink": {base: base, path: filepath.Join(base, "escape", "secret"), wantErr: true},
		"no mount": {
			base: filepath.Join(base, "none"), path: filepath.Join(base, "none", "etc"), wantErr: true, notExist: true,
		},
		"relative path": {base: base, path: "etc/hostname", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			info, err := statInMount(tc.base, tc.path)
			if (err != nil) != tc.wantErr || errors.Is(err, fs.ErrNotExist) != tc.notExist {
				t.Fatalf("statInMount(%s) error = %v, want error: %t, not exist: %t", name, err, tc.wantErr, tc.notExist)
			}

			if err == nil && info.IsDir() != tc.wantDir {
				t.Errorf("statInMount(%s) IsDir = %t, want %t", name, info.IsDir(), tc.wantDir)
			}

			f, err := openInMount(tc.base, tc.path, os.O_RDONLY, 0)
			if (err != nil) != tc.wantErr || errors.Is(err, fs.ErrNotExist) != tc.notExist {
				t.Fatalf("openInMount(%s) error = %v, want error: %t, not exist: %t", name, err, tc.wantErr, tc.notExist)
			}

			if err != nil {
				return
			}

			defer f.Close()

			if tc.wantDir {
				return
			}

			b, err := io.ReadAll(f)
			if err != nil || string(b) != tc.contents {
				t.Errorf("reading openInMount(%s) = %q, %v, want %q", name, b, err, tc.contents)
			}
		})
	}
}

// mountRoot opens the mount with the path relative to it.
func TestMountRoot(t *testing.T) {
	t.Parallel()

	base := mountTree(t)

	root, rel, err := mountRoot(base, filepath.Join(base, "etc", "hostname"))
	if err != nil {
		t.Fatalf("mountRoot(mount, etc/hostname) error = %v", err)
	}

	defer root.Close()

	if want := filepath.Join("etc", "hostname"); rel != want {
		t.Errorf("mountRoot(mount, etc/hostname) path = %q, want %q", rel, want)
	}

	if _, err := root.Stat(rel); err != nil {
		t.Errorf("Stat(%s) in the root mountRoot opened error = %v, want the file", rel, err)
	}
}

// Writing through openInMount creates a file inside the mount with the mode
// uploads use.
func TestOpenInMountCreates(t *testing.T) {
	t.Parallel()

	base := mountTree(t)
	path := filepath.Join(base, "etc", "notes.txt")

	f, err := openInMount(base, path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mountFileMode)
	if err != nil {
		t.Fatalf("openInMount(new etc/notes.txt) error = %v", err)
	}

	if _, err := io.WriteString(f, "notes"); err != nil {
		t.Errorf("writing etc/notes.txt: %v", err)
	}

	f.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(etc/notes.txt) error = %v", err)
	}

	if got := info.Mode().Perm(); got != mountFileMode {
		t.Errorf("etc/notes.txt mode = %v, want %v", got, os.FileMode(mountFileMode))
	}

	if _, err := openInMount(base, filepath.Join(base, "escape", "planted"), os.O_WRONLY|os.O_CREATE, mountFileMode); err == nil {
		t.Error("openInMount(escape/planted) error = nil, want a symlink out of the mount refused")
	}
}
