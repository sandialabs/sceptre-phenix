package mm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/common"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// c2Cluster answers ExecC2Command's commands for VMs vm1 and vm2, giving each posted cc command
// the next ID and the cc filter then set, and reporting a response to every command.
type c2Cluster struct {
	mu      sync.Mutex
	nextID  int
	filter  string        // the cc filter set, as `cc commands` shows it
	filters []string      // the filter of each command, by ID - 1
	hold    chan struct{} // if not nil, posted cc commands are answered once closed
}

func (c *c2Cluster) reply(cmd mmtest.Command) []*minicli.Response {
	switch {
	case cmd.Base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
	case cmd.Base == vmInfoCmd:
		name := filteredName.FindStringSubmatch(cmd.Raw)[1]

		return []*minicli.Response{mmtest.Tabular("head", []string{"name", "uuid"}, []string{name, "uuid-" + name})}
	case cmd.Base == ccClientCmd:
		return []*minicli.Response{mmtest.Tabular("head", []string{"uuid", "hostname"}, []string{"uuid-vm1", "vm1"})}
	case cmd.Base == ccCommandsCmd:
		c.mu.Lock()
		defer c.mu.Unlock()

		rows := make([][]string, 0, c.nextID)
		for id := 1; id <= c.nextID; id++ {
			rows = append(rows, []string{strconv.Itoa(id), "1", c.filters[id-1]})
		}

		return []*minicli.Response{mmtest.Tabular("head", []string{"id", "responses", "filter"}, rows...)}
	case strings.HasPrefix(cmd.Base, "cc filter "), cmd.Base == clearCCFilterCmd:
		c.mu.Lock()
		defer c.mu.Unlock()

		c.filter = strings.TrimPrefix(cmd.Base, "cc filter ")
		if cmd.Base == clearCCFilterCmd {
			c.filter = ""
		}
	case strings.HasPrefix(cmd.Base, "cc exec "), strings.HasPrefix(cmd.Base, "cc send "),
		strings.HasPrefix(cmd.Base, "cc test-conn "):
		if c.hold != nil {
			<-c.hold
		}

		c.mu.Lock()
		defer c.mu.Unlock()

		c.nextID++
		c.filters = append(c.filters, c.filter)

		return []*minicli.Response{{Host: "head", Data: c.nextID}}
	}

	return nil
}

// ccSequence lists the received cc commands that set, use or clear the
// filter, or delete a command, in order.
func ccSequence(cmds []mmtest.Command) []string {
	var out []string

	for _, cmd := range cmds {
		switch {
		case strings.HasPrefix(cmd.Base, "cc filter "), cmd.Base == clearCCFilterCmd,
			strings.HasPrefix(cmd.Base, "cc exec "), strings.HasPrefix(cmd.Base, "cc send "),
			strings.HasPrefix(cmd.Base, "cc test-conn "), strings.HasPrefix(cmd.Base, "cc delete "),
			strings.HasPrefix(cmd.Base, "mesh send ") && strings.Contains(cmd.Base, " cc delete "),
			strings.HasPrefix(cmd.Base, "cc mount"),
			strings.HasPrefix(cmd.Base, "clear cc mount"):
			out = append(out, cmd.Base)
		}
	}

	return out
}

func TestExecC2Command(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		opts   []C2Option
		wantID string
		want   []string
	}{
		"runs a command with the VM's filter and deletes it once waited on": {
			opts:   []C2Option{C2VM("vm1"), C2IDClientsByUUID(), C2Command("hostname"), C2Wait()},
			wantID: "1",
			want:   []string{"cc filter uuid=uuid-vm1", "cc exec hostname", clearCCFilterCmd, "cc delete command 1"},
		},
		"leaves a command not waited on": {
			opts:   []C2Option{C2VM("vm1"), C2Command("hostname")},
			wantID: "1",
			want:   []string{"cc filter uuid=uuid-vm1", "cc exec hostname", clearCCFilterCmd},
		},
		"sends a file and waits for it before running a command": {
			opts:   []C2Option{C2VM("vm1"), C2SendFile("script.sh"), C2Command("bash script.sh")},
			wantID: "2",
			want: []string{
				"cc filter uuid=uuid-vm1", "cc send script.sh", clearCCFilterCmd, "cc delete command 1",
				"cc filter uuid=uuid-vm1", "cc exec bash script.sh", clearCCFilterCmd,
			},
		},
		"tests a connection": {
			opts:   []C2Option{C2VM("vm1"), C2TestConn("tcp 10.0.0.1 22"), C2Wait()},
			wantID: "1",
			want:   []string{"cc filter uuid=uuid-vm1", "cc test-conn tcp 10.0.0.1 22", clearCCFilterCmd, "cc delete command 1"},
		},
		"unmounts without a filter": {
			opts:   []C2Option{C2VM("vm1"), C2Unmount(), C2SkipActiveClientCheck(true)},
			wantID: "",
			want:   []string{"clear cc mount vm1"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, new(c2Cluster).reply)
			useFastPolling(t, time.Millisecond)

			id, err := (Minimega{}).ExecC2Command(append([]C2Option{C2NS("exp")}, tc.opts...)...)
			if err != nil {
				t.Fatalf("ExecC2Command() error = %v", err)
			}

			if tc.wantID != "" && id != tc.wantID {
				t.Errorf("ExecC2Command() = %q, want %q", id, tc.wantID)
			}

			if diff := cmp.Diff(tc.want, ccSequence(received())); diff != "" {
				t.Errorf("ExecC2Command() sent cc commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWaitForC2ResponseDeletesTheCommand(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, new(c2Cluster).reply)
	useFastPolling(t, time.Millisecond)

	id, err := (Minimega{}).ExecC2Command(C2NS("exp"), C2VM("vm1"), C2Command("hostname"))
	if err != nil {
		t.Fatalf("ExecC2Command(hostname) error = %v", err)
	}

	if _, err := (Minimega{}).WaitForC2Response(C2NS("exp"), C2CommandID(id)); err != nil {
		t.Errorf("WaitForC2Response(%s) error = %v", id, err)
	}

	cmds := ccSequence(received())
	if len(cmds) == 0 {
		t.Fatal("ExecC2Command and WaitForC2Response sent no cc commands")
	}

	if last, want := cmds[len(cmds)-1], "cc delete command "+id; last != want {
		t.Errorf("WaitForC2Response(%s) sent %q last, want %q", id, last, want)
	}
}

func TestExecC2CommandSerializesFilterPerNamespace(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	const callers = 6

	cluster := &c2Cluster{hold: make(chan struct{})}
	received := useFakeMinimega(t, cluster.reply)

	var wg sync.WaitGroup

	// Answer the held command even if the test fails before it would, so every
	// caller finishes and releases the namespace's cc lock.
	unhold := sync.OnceFunc(func() { close(cluster.hold) })

	t.Cleanup(func() {
		unhold()
		wg.Wait()
	})

	for i := range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			vm := []string{"vm1", "vm2"}[i%2]

			_, err := (Minimega{}).ExecC2Command(
				C2NS("exp"), C2VM(vm), C2IDClientsByUUID(), C2Command("hostname"),
			)
			if err != nil {
				t.Errorf("ExecC2Command(%s) error = %v", vm, err)
			}
		}()
	}

	// One caller's command is posted and held; the others wait for the
	// namespace's cc lock rather than set their own filter meanwhile.
	polltest.Until(t, "every caller holds or waits for the cc lock", func() bool { return ccLocks.refs("exp") == callers })

	unhold()
	wg.Wait()

	seq := ccSequence(received())
	if len(seq) != 3*callers {
		t.Fatalf("%d concurrent ExecC2Command calls sent %d filter/exec/clear commands, want %d: %q",
			callers, len(seq), 3*callers, seq)
	}

	for i := 0; i < len(seq); i += 3 {
		if !strings.HasPrefix(seq[i], "cc filter uuid=") || seq[i+1] != "cc exec hostname" ||
			seq[i+2] != clearCCFilterCmd {
			t.Errorf("%d concurrent ExecC2Command calls interleaved filter, exec and clear: %q", callers, seq)

			break
		}
	}
}

func TestExecC2CommandLeavesFilterAfterTimeout(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	cluster := &c2Cluster{hold: make(chan struct{})}
	received := useFakeMinimega(t, cluster.reply)

	// Answer the abandoned exec once the test is done.
	t.Cleanup(func() { close(cluster.hold) })

	_, err := (Minimega{}).ExecC2Command(
		C2NS("exp"), C2VM("vm1"), C2Command("hostname"), C2Timeout(50*time.Millisecond),
	)
	if err == nil || !strings.Contains(err.Error(), "timeout running 'cc exec hostname'") {
		t.Errorf("ExecC2Command(hostname, 50ms timeout) error = %v, want a timeout", err)
	}

	// The unanswered exec may still be posted; clearing the filter now could
	// let it reach every VM.
	want := []string{"cc filter uuid=uuid-vm1", "cc exec hostname"}
	if diff := cmp.Diff(want, ccSequence(received())); diff != "" {
		t.Errorf("ExecC2Command(hostname, 50ms timeout) sent cc commands mismatch (-want +got):\n%s", diff)
	}

	if got := ccLocks.refs("exp"); got != 0 {
		t.Errorf("ccLocks.refs(exp) after ExecC2Command timed out = %d, want 0", got)
	}
}

func TestExecC2CommandForMissingVMSendsNoCC(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	cluster := new(c2Cluster)

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == vmInfoCmd {
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "uuid"})}
		}

		return cluster.reply(cmd)
	})

	_, err := (Minimega{}).ExecC2Command(C2NS("exp"), C2VM("vm3"), C2Command("hostname"), C2Wait())
	if want := "cannot execute command: vm vm3 does not exist"; err == nil || err.Error() != want {
		t.Errorf("ExecC2Command(vm3, hostname) error = %v, want %q", err, want)
	}

	if got := ccSequence(received()); len(got) != 0 {
		t.Errorf("ExecC2Command(vm3, hostname) sent cc commands %q, want none", got)
	}

	if got := ccLocks.refs("exp"); got != 0 {
		t.Errorf("ccLocks.refs(exp) after ExecC2Command(vm3) failed = %d, want 0", got)
	}
}

// ExecC2Command filters by name where it has no UUID, waits for a file sent
// alone only when asked, and mounts in the local mount directory.
func TestExecC2CommandMoreOptions(t *testing.T) { //nolint:paralleltest // shares the fake minimega and common.MountBase
	mounts := t.TempDir()

	saved := common.MountBase
	common.MountBase = mounts //nolint:reassign // mounts go to a temporary directory

	t.Cleanup(func() { common.MountBase = saved }) //nolint:reassign // restore

	for name, tc := range map[string]struct {
		opts   []C2Option
		wantID string
		want   []string
	}{
		"filters by name without a UUID": {
			opts:   []C2Option{C2VM("vm1"), C2SkipActiveClientCheck(true), C2Command("hostname")},
			wantID: "1",
			want:   []string{"cc filter name=vm1", "cc exec hostname", clearCCFilterCmd},
		},
		"sends a file without waiting": {
			opts:   []C2Option{C2VM("vm1"), C2SendFile("a.sh")},
			wantID: "1",
			want:   []string{"cc filter uuid=uuid-vm1", "cc send a.sh", clearCCFilterCmd},
		},
		"sends a file and waits": {
			opts:   []C2Option{C2VM("vm1"), C2SendFile("a.sh"), C2Wait()},
			wantID: "1",
			want:   []string{"cc filter uuid=uuid-vm1", "cc send a.sh", clearCCFilterCmd, "cc delete command 1"},
		},
		"tests a connection without waiting": {
			opts:   []C2Option{C2VM("vm1"), C2TestConn("tcp 10.0.0.1 22")},
			wantID: "1",
			want:   []string{"cc filter uuid=uuid-vm1", "cc test-conn tcp 10.0.0.1 22", clearCCFilterCmd},
		},
		// minimega does not expand the packet: the fake fails a command it would expand
		"tests a connection with a $ in its packet": {
			opts:   []C2Option{C2VM("vm1"), C2TestConn("udp 10.0.0.1 53 wait 5s $HOME")},
			wantID: "1",
			want:   []string{"cc filter uuid=uuid-vm1", "cc test-conn udp 10.0.0.1 53 wait 5s $HOME", clearCCFilterCmd},
		},
		"mounts in the local mount directory": {
			opts:   []C2Option{C2VM("vm1"), C2Mount()},
			wantID: "",
			want:   []string{"cc mount vm1 " + filepath.Join(mounts, "exp", "vm1")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, new(c2Cluster).reply)
			useFastPolling(t, time.Millisecond)

			id, err := ExecC2Command(append([]C2Option{C2NS("exp")}, tc.opts...)...)
			if err != nil {
				t.Fatalf("ExecC2Command() error = %v", err)
			}

			if id != tc.wantID {
				t.Errorf("ExecC2Command() = %q, want %q", id, tc.wantID)
			}

			if diff := cmp.Diff(tc.want, ccSequence(received())); diff != "" {
				t.Errorf("ExecC2Command() sent cc commands mismatch (-want +got):\n%s", diff)
			}
		})
	}

	if info, err := os.Stat(filepath.Join(mounts, "exp", "vm1")); err != nil || !info.IsDir() {
		t.Errorf("ExecC2Command(mount) left no mount directory: %v", err)
	}
}

// ExecC2Command reports minimega's error at each step, and clears the
// filter it set whatever the command's outcome.
func TestExecC2CommandReportsErrors(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		opts    []C2Option
		failing string // the base minimega fails
		want    []string
		wantMsg string
	}{
		"a failed filter": {
			opts: []C2Option{C2Command("hostname")}, failing: "cc filter uuid=uuid-vm1",
			want: []string{"cc filter uuid=uuid-vm1"}, wantMsg: "setting host filter to vm1",
		},
		"a failed command": {
			opts: []C2Option{C2Command("hostname")}, failing: "cc exec hostname",
			want: []string{"cc filter uuid=uuid-vm1", "cc exec hostname", clearCCFilterCmd}, wantMsg: "boom",
		},
		"a failed clear": {
			opts: []C2Option{C2Command("hostname")}, failing: clearCCFilterCmd,
			want: []string{"cc filter uuid=uuid-vm1", "cc exec hostname", clearCCFilterCmd},
		},
		"a failed file": {
			opts: []C2Option{C2SendFile("a.sh"), C2Command("bash a.sh")}, failing: "cc send a.sh",
			want: []string{"cc filter uuid=uuid-vm1", "cc send a.sh", clearCCFilterCmd}, wantMsg: "sending file 'a.sh'",
		},
		"a failed connection test": {
			opts: []C2Option{C2TestConn("tcp 10.0.0.1 22")}, failing: "cc test-conn tcp 10.0.0.1 22",
			want: []string{"cc filter uuid=uuid-vm1", "cc test-conn tcp 10.0.0.1 22", clearCCFilterCmd}, wantMsg: "boom",
		},
		"a failed unmount": {
			opts: []C2Option{C2Unmount()}, failing: "clear cc mount vm1",
			want: []string{"clear cc mount vm1"}, wantMsg: "error clearing mount",
		},
		"no options": {
			opts: nil, want: nil, wantMsg: "no options to execute were provided",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cluster := new(c2Cluster)

			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == tc.failing {
					return []*minicli.Response{mmtest.Error("head", "boom")}
				}

				return cluster.reply(cmd)
			})

			_, err := ExecC2Command(append([]C2Option{C2NS("exp"), C2VM("vm1"), C2IDClientsByUUID()}, tc.opts...)...)

			switch {
			case tc.wantMsg == "" && err != nil:
				t.Errorf("ExecC2Command() error = %v, want nil", err)
			case tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)):
				t.Errorf("ExecC2Command() error = %v, want one holding %q", err, tc.wantMsg)
			}

			if diff := cmp.Diff(tc.want, ccSequence(received())); diff != "" {
				t.Errorf("ExecC2Command() sent cc commands mismatch (-want +got):\n%s", diff)
			}

			if got := ccLocks.refs("exp"); got != 0 {
				t.Errorf("ccLocks.refs(exp) after ExecC2Command = %d, want 0", got)
			}
		})
	}
}
