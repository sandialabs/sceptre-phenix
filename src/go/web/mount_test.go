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
//
//nolint:unparam // tests can read the users count
func mountStateOf(key string) (mountState, int, bool) {
	activeMountsMu.RLock()
	defer activeMountsMu.RUnlock()

	info, ok := activeMounts[key]
	if !ok {
		return 0, 0, false
	}

	return info.state, info.users, true
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
