package vm_test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/store"
	"phenix/util/common"
	"phenix/util/file"
)

const (
	testExp  = "test-experiment"
	fakeHost = "fake"
	fakeUUID = "11111111-2222-3333-4444-555555555555"
)

// fakeDir is the directory of the fake minimega command socket that TestMain
// starts for every test in the package.
var fakeDir string //nolint:gochecknoglobals // test fixture

// commandPrefix matches what mmcli puts in front of a command: no history,
// the namespace, columns, and filters.
var commandPrefix = regexp.MustCompile(`^(\.record false |namespace "[^"]*" |\.columns ("[^"]*",?)+ |\.filter \S+ )+`)

// queries are the commands that only read state. Tests compare the other
// commands, which change the VM.
var queries = map[string]bool{ //nolint:gochecknoglobals // test fixture
	"host":            true,
	"capture":         true,
	"cc client":       true,
	"vm info":         true,
	"vm info summary": true,
}

// fakeMinimega answers minimega commands for one VM and records them.
type fakeMinimega struct {
	mu sync.Mutex

	vm     string
	vmType string
	vmHost string
	state  string

	commands []string
	polled   map[string]bool
}

var current struct { //nolint:gochecknoglobals // test fixture
	mu   sync.Mutex
	fake *fakeMinimega
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mmfake")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating fake minimega directory:", err)
		os.Exit(1)
	}

	listener, err := net.Listen("unix", filepath.Join(dir, "minimega"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "listening on fake minimega socket:", err)
		os.Exit(1)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go serveFake(conn)
		}
	}()

	fakeDir = dir

	code := m.Run()

	_ = listener.Close()
	_ = os.RemoveAll(dir)

	os.Exit(code)
}

func serveFake(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	var (
		dec = json.NewDecoder(conn)
		enc = json.NewEncoder(conn)
	)

	for {
		var req miniclient.Request

		if err := dec.Decode(&req); err != nil {
			return
		}

		current.mu.Lock()
		fake := current.fake
		current.mu.Unlock()

		// mmcli keeps its connection after a test, so without a test the fake
		// fails every command, as a missing minimega would.
		resp := &minicli.Response{Host: fakeHost, Error: "no minimega in this test"}
		if fake != nil {
			resp = fake.handle(req.Command)
		}

		out := &miniclient.Response{Resp: minicli.Responses{resp}}
		if err := enc.Encode(out); err != nil {
			return
		}
	}
}

// useFakeMinimega points mmcli at the fake minimega, which answers for the VM
// vmName of type vmType, in the RUNNING state, on host vmHost.
func useFakeMinimega(t *testing.T, vmName, vmType, vmHost string) *fakeMinimega {
	t.Helper()

	fake := &fakeMinimega{vm: vmName, vmType: vmType, vmHost: vmHost, state: "RUNNING"}

	prev := common.MinimegaBase
	common.MinimegaBase = fakeDir //nolint:reassign // test fixture

	current.mu.Lock()
	current.fake = fake
	current.mu.Unlock()

	t.Cleanup(func() {
		current.mu.Lock()
		current.fake = nil
		current.mu.Unlock()

		common.MinimegaBase = prev //nolint:reassign // test fixture
	})

	return fake
}

func (f *fakeMinimega) handle(raw string) *minicli.Response {
	cmd := strings.TrimSpace(commandPrefix.ReplaceAllString(raw, ""))

	f.mu.Lock()
	defer f.mu.Unlock()

	f.commands = append(f.commands, cmd)

	resp := &minicli.Response{Host: fakeHost}

	switch {
	case cmd == "host":
		resp.Header = []string{"name"}
		resp.Tabular = [][]string{{fakeHost}}
	case cmd == "vm info" || cmd == "vm info summary":
		resp.Host = f.vmHost
		resp.Header = []string{"id", "name", "state", "type", "uuid", "snapshot", "disks"}
		resp.Tabular = [][]string{{"0", f.vm, f.state, f.vmType, fakeUUID, "true", ""}}
	case cmd == "vm save" || cmd == "vm export-disk" || cmd == "vm memory-dump":
		// The first poll finds the task half done, and the next one finds it completed.
		resp.Header = []string{"name", "status", "complete (%)"}
		resp.Tabular = [][]string{{f.vm, "completed", "100"}}

		if f.polled == nil {
			f.polled = make(map[string]bool)
		}

		if !f.polled[cmd] {
			f.polled[cmd] = true
			resp.Tabular = [][]string{{f.vm, "running", "50"}}
		}
	case strings.HasPrefix(cmd, "vm kill "), strings.HasPrefix(cmd, "vm shutdown "),
		strings.Contains(cmd, "system_powerdown"):
		f.state = "QUIT"
	case strings.HasPrefix(cmd, "vm start "):
		f.state = "RUNNING"
	}

	return resp
}

// changes returns the commands that change the VM, in the order sent.
func (f *fakeMinimega) changes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var changes []string

	for _, cmd := range f.commands {
		if !queries[cmd] {
			changes = append(changes, cmd)
		}
	}

	return changes
}

// fakeFiles stands in for the cluster file listing and transfers.
type fakeFiles struct {
	file.ClusterFiles

	files     file.Files
	snapshots []string
	copied    []string
	synced    []string
}

func (f *fakeFiles) GetExperimentFiles(string, string) (file.Files, error) {
	return f.files, nil
}

func (f *fakeFiles) GetExperimentSnapshots(string) ([]string, error) {
	return f.snapshots, nil
}

func (f *fakeFiles) CopyFile(path, dest string, _ file.CopyStatus) error {
	f.copied = append(f.copied, path+" to "+dest)

	return nil
}

func (f *fakeFiles) SyncFile(path string, _ file.CopyStatus) error {
	f.synced = append(f.synced, path)

	return nil
}

func useFakeFiles(t *testing.T, files *fakeFiles) {
	t.Helper()

	prev := file.DefaultClusterFiles
	file.DefaultClusterFiles = files //nolint:reassign // test fixture

	t.Cleanup(func() { file.DefaultClusterFiles = prev }) //nolint:reassign // test fixture
}

// useExperiment stores an experiment with one node, vmName of type vmType,
// and makes it the phenix default store for the duration of the test.
func useExperiment(t *testing.T, vmName, vmType string, running bool) {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: testExp},
		Spec: map[string]any{
			"experimentName": testExp,
			"defaultBridge":  "phenix",
			"topology": map[string]any{
				"nodes": []map[string]any{
					{
						"type": "VirtualMachine",
						"general": map[string]any{
							"hostname":    vmName,
							"vm_type":     vmType,
							"do_not_boot": false,
							"snapshot":    true,
						},
						"hardware": map[string]any{
							"vcpus":   1,
							"memory":  512,
							"os_type": "linux",
							"drives": []map[string]any{
								{"image": "test.raw", "inject_partition": 1},
							},
						},
						"injections": []map[string]any{
							{"src": "/tmp/motd", "dst": "/etc/motd"},
						},
					},
				},
			},
		},
	}

	if running {
		c.Status = map[string]any{"startTime": "2026-11-05T00:00:00Z"}
	}

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		*cfg = c

		return nil
	}).AnyTimes()

	prev := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = prev }) //nolint:reassign // restore store
}
