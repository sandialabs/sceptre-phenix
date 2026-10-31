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

	"github.com/google/go-cmp/cmp"

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
