package vm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/vm"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

const execUUID = "0a1b2c3d-0000-4000-8000-000000000001"

// execMinimega fakes minimega for a running VM vm1 on the headnode, whose
// miniccc agent answers every cc command at once.
type execMinimega struct {
	mu       sync.Mutex
	files    string            // the minimega files directory
	commands []string          // the cc commands posted, by ID - 1
	stdout   string            // the stdout of each command
	stderr   string            // the stderr of each command
	exitcode string            // the exit code of each command, "" for none
	inVM     map[string]string // the files in the VM, by path
	sent     map[string]string // the content of each file posted with cc send, by name
	active   bool              // the VM's miniccc agent is active
}

func (m *execMinimega) handle(cmd mmtest.Command) []*minicli.Response {
	m.mu.Lock()
	defer m.mu.Unlock()

	base := cmd.Base

	switch {
	case base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
	case base == "vm info":
		return []*minicli.Response{mmtest.Tabular("head", []string{"host", "name", "uuid", "state"},
			[]string{"head", "vm1", execUUID, "RUNNING"})}
	case base == "cc client":
		if !m.active {
			return []*minicli.Response{mmtest.Tabular("head", []string{"host", "uuid", "hostname"})}
		}

		return []*minicli.Response{mmtest.Tabular("head", []string{"host", "uuid", "hostname"},
			[]string{"head", execUUID, "vm1"})}
	case base == "cc commands":
		rows := make([][]string, 0, len(m.commands))
		for i := range m.commands {
			rows = append(rows, []string{"head", strconv.Itoa(i + 1), "1", "uuid=" + execUUID})
		}

		return []*minicli.Response{mmtest.Tabular("head", []string{"host", "id", "responses", "filter"}, rows...)}
	case strings.HasPrefix(base, "cc exec "), strings.HasPrefix(base, "cc send "), strings.HasPrefix(base, "cc recv "):
		return m.post(base)
	case strings.HasPrefix(base, "cc response "):
		id := strings.Fields(base)[2]

		return []*minicli.Response{mmtest.Text("head", id+"/"+execUUID+"/stdout:\n"+m.stdout+"\n"+
			id+"/"+execUUID+"/stderr:\n"+m.stderr+"\n")}
	case strings.HasPrefix(base, "cc exitcode "):
		if m.exitcode == "" {
			return []*minicli.Response{mmtest.Error("head", "no exit code")}
		}

		return []*minicli.Response{mmtest.Text("head", m.exitcode)}
	}

	return nil
}

// post posts the cc command and does what miniccc does for it.
func (m *execMinimega) post(base string) []*minicli.Response {
	m.commands = append(m.commands, base)
	id := strconv.Itoa(len(m.commands))

	switch {
	case strings.HasPrefix(base, "cc send "):
		name := strings.TrimPrefix(base, "cc send ")

		content, err := os.ReadFile(filepath.Join(m.files, name))
		if err != nil {
			return []*minicli.Response{mmtest.Error("head", "no such file: "+name)}
		}

		m.sent[name] = string(content)
		m.inVM["/tmp/miniccc/files/"+name] = string(content)
	case strings.HasPrefix(base, "cc exec mv -f -- "):
		args := strings.Fields(strings.TrimPrefix(base, "cc exec mv -f -- "))
		m.inVM[args[1]] = m.inVM[args[0]]
		delete(m.inVM, args[0])
	case strings.HasPrefix(base, "cc recv "):
		name := strings.TrimPrefix(base, "cc recv ")
		if content, ok := m.inVM[name]; ok {
			dst := filepath.Join(m.files, "test-experiment", "miniccc_responses", id, execUUID, name)
			_ = os.MkdirAll(filepath.Dir(dst), 0o750)
			_ = os.WriteFile(dst, []byte(content), 0o600)
		}
	}

	return []*minicli.Response{{Host: "head", Data: len(m.commands)}}
}

// useExecMinimega makes the store hold testExp running with the VM vm1, of
// osType, and fakes minimega for it with a files directory of its own.
func useExecMinimega(t *testing.T, osType string) (*execMinimega, func() []mmtest.Command) {
	t.Helper()

	node := vmNode("vm1")
	node["hardware"].(map[string]any)["os_type"] = osType //nolint:forcetypeassert // vmNode's hardware

	useExperiment(t, startTime, node)

	base := t.TempDir()

	saved := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the minimega files directory is a temporary one

	t.Cleanup(func() { common.PhenixBase = saved }) //nolint:reassign // restore

	fake := &execMinimega{
		files: mm.GetMMFullPath(""), inVM: map[string]string{}, sent: map[string]string{}, active: true,
	}

	received := useMinimega(t, fake.handle)
	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	return fake, received
}

// Exec runs the command in the VM alone and returns its output and exit code.
func TestExec(t *testing.T) { //nolint:paralleltest // shares the fake minimega and the store
	for _, test := range []struct {
		name     string
		exitcode string
		want     vm.ExecResult
	}{
		{name: "exit code", exitcode: "3", want: vm.ExecResult{Stdout: "hello", Stderr: "oops", ExitCode: 3, ExitCodeKnown: true}},
		{name: "no exit code", exitcode: "", want: vm.ExecResult{Stdout: "hello", Stderr: "oops"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, received := useExecMinimega(t, "linux")
			fake.stdout, fake.stderr, fake.exitcode = "hello", "oops", test.exitcode

			got, err := vm.Exec(context.Background(), testExp, "vm1", []string{"cat", "/tmp/my file"}, time.Minute)
			if err != nil {
				t.Fatalf("Exec() error = %v", err)
			}

			if diff := cmp.Diff(&test.want, got); diff != "" {
				t.Errorf("Exec() mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff([]string{"cc exec cat '/tmp/my file'"}, fake.commands); diff != "" {
				t.Errorf("Exec() posted cc commands mismatch (-want +got):\n%s", diff)
			}

			if got := mmtest.Count(received(), "cc filter uuid="+execUUID); got != 1 {
				t.Errorf("Exec() set the VM's cc filter %d times, want 1", got)
			}
		})
	}
}

// Exec refuses a command minimega would change, a stopped experiment, a VM
// the topology does not have and a VM without an active miniccc agent, and
// posts no cc command for them.
func TestExecRefuses(t *testing.T) { //nolint:paralleltest // shares the fake minimega and the store
	for _, test := range []struct {
		name    string
		vm      string
		argv    []string
		stopped bool
		active  bool
		wantErr error
	}{
		{name: "a variable", vm: "vm1", argv: []string{"echo", "$HOME"}, active: true, wantErr: vm.ErrExecCommand},
		{name: "a redirect", vm: "vm1", argv: []string{"stdout=x", "y"}, active: true, wantErr: vm.ErrExecCommand},
		{name: "no command", vm: "vm1", argv: nil, active: true, wantErr: vm.ErrExecCommand},
		{name: "a stopped experiment", vm: "vm1", argv: []string{"ls"}, stopped: true, active: true, wantErr: vm.ErrExperimentNotRunning},
		{name: "an unknown VM", vm: "vm9", argv: []string{"ls"}, active: true, wantErr: mm.ErrVMNotFound},
		{name: "no miniccc", vm: "vm1", argv: []string{"ls"}, active: false, wantErr: vm.ErrC2NotActive},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, _ := useExecMinimega(t, "linux")
			fake.active = test.active

			if test.stopped {
				useExperiment(t, "", vmNode("vm1"))
			}

			_, err := vm.Exec(context.Background(), testExp, test.vm, test.argv, time.Minute)
			if !errors.Is(err, test.wantErr) {
				t.Errorf("Exec(%s, %q) error = %v, want %v", test.vm, test.argv, err, test.wantErr)
			}

			if len(fake.commands) != 0 {
				t.Errorf("Exec(%s, %q) posted %q, want no cc commands", test.vm, test.argv, fake.commands)
			}
		})
	}
}

// CopyToVM sends a staging copy, moves it to the path in the VM, and removes
// the staging copy.
func TestCopyToVM(t *testing.T) { //nolint:paralleltest // shares the fake minimega and the store
	for _, test := range []struct {
		name, dst, want string
	}{
		{name: "a file", dst: "/root/setup.sh", want: "/root/setup.sh"},
		{name: "a folder", dst: "/root/", want: "/root/local.sh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, _ := useExecMinimega(t, "linux")
			fake.exitcode = "0"

			src := filepath.Join(t.TempDir(), "local.sh")
			if err := os.WriteFile(src, []byte("echo hi\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := vm.CopyToVM(context.Background(), testExp, "vm1", src, test.dst, time.Minute)
			if err != nil || got != test.want {
				t.Fatalf("CopyToVM(%s) = %q, %v, want %q, nil", test.dst, got, err, test.want)
			}

			if content := fake.inVM[test.want]; content != "echo hi\n" {
				t.Errorf("the VM's %s after CopyToVM = %q, want %q", test.want, content, "echo hi\n")
			}

			if len(fake.sent) != 1 {
				t.Fatalf("CopyToVM sent %d files, want 1", len(fake.sent))
			}

			for name := range fake.sent {
				if !strings.HasPrefix(name, testExp+"/phenix-cp-") {
					t.Errorf("CopyToVM sent %q, want a staging copy in %s/", name, testExp)
				}

				if _, err := os.Stat(filepath.Join(fake.files, name)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("staging copy %s after CopyToVM: stat error = %v, want it removed", name, err)
				}
			}
		})
	}
}

// CopyToVM reports the error of a move that fails in the VM, and refuses a
// relative path in the VM.
func TestCopyToVMFails(t *testing.T) { //nolint:paralleltest // shares the fake minimega and the store
	fake, _ := useExecMinimega(t, "linux")
	fake.exitcode, fake.stderr = "1", "mv: cannot move"

	src := filepath.Join(t.TempDir(), "local.sh")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := vm.CopyToVM(context.Background(), testExp, "vm1", src, "/root/x", time.Minute); !errors.Is(err, vm.ErrCopyFailed) {
		t.Errorf("CopyToVM() with a failed move error = %v, want %v", err, vm.ErrCopyFailed)
	}

	if _, err := vm.CopyToVM(context.Background(), testExp, "vm1", src, "root/x", time.Minute); !errors.Is(err, vm.ErrGuestPath) {
		t.Errorf("CopyToVM(root/x) error = %v, want %v", err, vm.ErrGuestPath)
	}
}

// CopyToVM moves the file with PowerShell in a Windows VM.
func TestCopyToVMWindows(t *testing.T) { //nolint:paralleltest // shares the fake minimega and the store
	fake, _ := useExecMinimega(t, "windows")
	fake.exitcode = "0"

	src := filepath.Join(t.TempDir(), "local.ps1")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := vm.CopyToVM(context.Background(), testExp, "vm1", src, `C:\Users\it's\x.ps1`, time.Minute); err != nil {
		t.Fatalf("CopyToVM() error = %v", err)
	}

	if len(fake.commands) != 2 {
		t.Fatalf("CopyToVM() posted %q, want a send and an exec", fake.commands)
	}

	staged := "/tmp/miniccc/files/" + strings.TrimPrefix(fake.commands[0], "cc send ")
	want := `cc exec powershell -NoProfile -NonInteractive -Command "Move-Item -LiteralPath '` + staged +
		`' -Destination 'C:\\Users\\it''s\\x.ps1' -Force"`

	if fake.commands[1] != want {
		t.Errorf("CopyToVM() move = %q, want %q", fake.commands[1], want)
	}
}

// CopyFromVM receives the file, writes it to the local path, and removes the
// received copy.
func TestCopyFromVM(t *testing.T) { //nolint:paralleltest // shares the fake minimega and the store
	fake, _ := useExecMinimega(t, "linux")
	fake.inVM["/etc/hosts"] = "127.0.0.1 localhost\n"

	dir := t.TempDir()

	got, err := vm.CopyFromVM(context.Background(), testExp, "vm1", "/etc/hosts", dir, time.Minute)
	if want := filepath.Join(dir, "hosts"); err != nil || got != want {
		t.Fatalf("CopyFromVM(/etc/hosts, %s) = %q, %v, want %q, nil", dir, got, err, want)
	}

	if content, err := os.ReadFile(got); err != nil || string(content) != "127.0.0.1 localhost\n" {
		t.Errorf("%s after CopyFromVM = %q, %v, want the VM's file", got, content, err)
	}

	if diff := cmp.Diff([]string{"cc recv /etc/hosts"}, fake.commands); diff != "" {
		t.Errorf("CopyFromVM() posted cc commands mismatch (-want +got):\n%s", diff)
	}

	responses := filepath.Join(fake.files, testExp, "miniccc_responses", "1")
	if _, err := os.Stat(responses); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("received copy %s after CopyFromVM: stat error = %v, want it removed", responses, err)
	}
}

// CopyFromVM reports a file the VM did not send, and refuses a path that is
// relative, has a ".." part or a glob character, before it posts a command.
func TestCopyFromVMFails(t *testing.T) { //nolint:paralleltest // shares the fake minimega and the store
	fake, _ := useExecMinimega(t, "linux")
	dst := filepath.Join(t.TempDir(), "out")

	if _, err := vm.CopyFromVM(context.Background(), testExp, "vm1", "/nope", dst, time.Minute); !errors.Is(err, vm.ErrCopyFailed) {
		t.Errorf("CopyFromVM(/nope) error = %v, want %v", err, vm.ErrCopyFailed)
	}

	for _, src := range []string{"etc/hosts", "/../etc/shadow", "/etc/*", "/tmp/$HOME"} {
		if _, err := vm.CopyFromVM(context.Background(), testExp, "vm1", src, dst, time.Minute); !errors.Is(err, vm.ErrGuestPath) {
			t.Errorf("CopyFromVM(%s) error = %v, want %v", src, err, vm.ErrGuestPath)
		}
	}

	if len(fake.commands) != 1 {
		t.Errorf("CopyFromVM posted %q, want only the receive of /nope", fake.commands)
	}
}
