package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/polltest"
	"phenix/web/rbac"
)

// useTestMount turns the vm-mount feature on and mounts test-vm of
// test-experiment, with files in it, beside an unmounted sibling test-vm2
// whose name test-vm's is a prefix of.
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
		"list outside the mounts":              {"", "../../..", http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resp := requestMount(t, http.MethodGet, tc.route, url.Values{"path": {tc.path}})
			if resp.StatusCode != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, resp.StatusCode, readBody(t, resp))
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
			resp := requestMount(t, http.MethodPost, "/copy", tc.query)
			if resp.StatusCode != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, resp.StatusCode, readBody(t, resp))
			}
		})
	}

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

// within fails the test if fn does not return within 2 seconds.
func within(t *testing.T, what string, fn func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		fn()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s took longer than 2s", what)
	}
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

	within(t, "the mount starting", func() { <-g.entered })

	go func() { second <- mountStatus(http.MethodPost, api+"test-vm2/mount") }()

	within(t, "listing another VM's mounted files", func() {
		if code := mountStatus(http.MethodGet, api+"test-vm/files?path=etc"); code != http.StatusOK {
			t.Errorf("listing files: status %d, want 200", code)
		}
	})

	key := mm.GetLocalMountPath("test-experiment", "test-vm2")

	polltest.Until(t, "the second request joining the mount", func() bool {
		_, users, _ := mountStateOf(key)

		return users == 2
	})

	g.release()

	within(t, "the mounts answering", func() {
		if a, b := <-first, <-second; a != http.StatusOK || b != http.StatusOK {
			t.Errorf("mount statuses = %d, %d; want 200, 200", a, b)
		}
	})

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

	within(t, "requests for another VM and this one", func() {
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

	within(t, "the unmount answering", func() {
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

	within(t, "the unmount giving up", func() {
		if code := mountStatus(http.MethodDelete, mountServer(t)+"test-vm/unmount"); code != http.StatusConflict {
			t.Errorf("unmount status %d, want 409", code)
		}
	})

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
