package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"phenix/util/common"
	"phenix/util/mm"
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
