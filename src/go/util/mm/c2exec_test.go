package mm

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// c2Cluster answers the commands ExecC2Command sends for VMs vm1 and vm2,
// giving each posted cc command the next ID, and reporting a response to
// every command.
type c2Cluster struct {
	mu     sync.Mutex
	nextID int
	hold   chan struct{} // if not nil, posted cc commands are answered once closed
}

func (c *c2Cluster) reply(cmd mmtest.Command) []*minicli.Response {
	switch {
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
			rows = append(rows, []string{strconv.Itoa(id), "1"})
		}

		return []*minicli.Response{mmtest.Tabular("head", []string{"id", "responses"}, rows...)}
	case strings.HasPrefix(cmd.Base, "cc exec "), strings.HasPrefix(cmd.Base, "cc send "),
		strings.HasPrefix(cmd.Base, "cc test-conn "):
		if c.hold != nil {
			<-c.hold
		}

		c.mu.Lock()
		defer c.mu.Unlock()

		c.nextID++

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
				t.Fatalf("ExecC2Command: %v", err)
			}

			if tc.wantID != "" && id != tc.wantID {
				t.Fatalf("ExecC2Command = %q, want the command's ID %s", id, tc.wantID)
			}

			if got := ccSequence(received()); !slices.Equal(got, tc.want) {
				t.Fatalf("sent %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWaitForC2ResponseDeletesTheCommand(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, new(c2Cluster).reply)
	useFastPolling(t, time.Millisecond)

	id, err := (Minimega{}).ExecC2Command(C2NS("exp"), C2VM("vm1"), C2Command("hostname"))
	if err != nil {
		t.Fatalf("ExecC2Command: %v", err)
	}

	if _, err := (Minimega{}).WaitForC2Response(C2NS("exp"), C2CommandID(id)); err != nil {
		t.Fatalf("WaitForC2Response: %v", err)
	}

	cmds := ccSequence(received())
	if last := cmds[len(cmds)-1]; last != "cc delete command "+id {
		t.Fatalf("last cc command %q, want the waited command deleted", last)
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
				t.Errorf("ExecC2Command %s: %v", vm, err)
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
		t.Fatalf("sent %d filter/exec/clear commands, want %d: %q", len(seq), 3*callers, seq)
	}

	for i := 0; i < len(seq); i += 3 {
		if !strings.HasPrefix(seq[i], "cc filter uuid=") || seq[i+1] != "cc exec hostname" ||
			seq[i+2] != clearCCFilterCmd {
			t.Fatalf("filter, exec and clear interleaved at %d: %q", i, seq)
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
		t.Fatalf("ExecC2Command error = %v, want a timeout", err)
	}

	// The unanswered exec may still be posted; clearing the filter now could
	// let it reach every VM.
	if got, want := ccSequence(received()), []string{"cc filter uuid=uuid-vm1", "cc exec hostname"}; !slices.Equal(got, want) {
		t.Fatalf("sent %q after an unanswered exec, want %q", got, want)
	}

	if got := ccLocks.refs("exp"); got != 0 {
		t.Fatalf("namespace's cc lock has %d holders after the timeout, want 0", got)
	}
}
