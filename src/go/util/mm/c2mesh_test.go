package mm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/common"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
)

// meshCommand is one cc command on one host of a meshC2 cluster.
type meshCommand struct {
	id     int
	filter string
}

// meshC2 answers C2 commands as a cluster of two hosts does. vm1 runs on
// compute1, which has its client. Each host numbers its cc commands itself: the
// headnode already has commands 1 to 5 for another VM, so its IDs differ from
// compute1's and its command 1 is not vm1's. Each headnode command has a
// response, so only compute1's rows tell when vm1 answered.
type meshC2 struct {
	mu       sync.Mutex
	filter   string
	cmds     map[string][]meshCommand // by host
	last     map[string]int           // the last ID each host gave
	answered bool                     // compute1's commands for vm1 have a response
	polls    int                      // `cc commands` polls while not answered
	// overwrite, if set, is the filter compute1 gives new commands, as if
	// another cc filter had been set there meanwhile.
	overwrite string
}

func newMeshC2() *meshC2 {
	m := &meshC2{cmds: map[string][]meshCommand{}, last: map[string]int{"head": 5}}

	for id := 1; id <= 5; id++ {
		m.cmds["head"] = append(m.cmds["head"], meshCommand{id: id, filter: "uuid=uuid-other"})
	}

	return m
}

// meshDelete is the delete of compute1's command id, sent to compute1 alone.
func meshDelete(id string) string {
	return "mesh send compute1 namespace exp cc delete command " + id
}

// remove deletes command id on each of hosts, as `cc delete command` does.
func (m *meshC2) remove(id string, hosts ...string) {
	for _, host := range hosts {
		m.cmds[host] = slices.DeleteFunc(m.cmds[host], func(c meshCommand) bool { return strconv.Itoa(c.id) == id })
	}
}

// ids lists the IDs of host's commands.
func (m *meshC2) ids(host string) []int {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]int, 0, len(m.cmds[host]))
	for _, c := range m.cmds[host] {
		ids = append(ids, c.id)
	}

	return ids
}

// responses is how many clients on host have answered c.
func (m *meshC2) responses(host string, c meshCommand) string {
	if host == "head" || (host == "compute1" && m.answered && strings.EqualFold(c.filter, "uuid=uuid-vm1")) {
		return "1"
	}

	return "0"
}

func (m *meshC2) reply(cmd mmtest.Command) []*minicli.Response {
	m.mu.Lock()
	defer m.mu.Unlock()

	hosts := []string{"head", "compute1"}

	switch {
	case cmd.Base == "host" && cmd.Namespace == "minimega":
		return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
	case strings.HasPrefix(cmd.Base, "cc delete command "):
		m.remove(strings.TrimPrefix(cmd.Base, "cc delete command "), hosts...)
	case strings.HasPrefix(cmd.Base, meshDelete("")):
		m.remove(strings.TrimPrefix(cmd.Base, meshDelete("")), "compute1")
	case cmd.Base == vmInfoCmd:
		return []*minicli.Response{mmtest.Tabular("compute1", []string{"name", "uuid"}, []string{"vm1", "uuid-vm1"})}
	case cmd.Base == ccClientCmd:
		return []*minicli.Response{mmtest.Tabular("compute1", []string{"uuid", "hostname"}, []string{"uuid-vm1", "vm1"})}
	case strings.HasPrefix(cmd.Base, "cc filter "):
		m.filter = strings.TrimPrefix(cmd.Base, "cc filter ")
	case cmd.Base == clearCCFilterCmd:
		m.filter = ""
	case strings.HasPrefix(cmd.Base, "cc exec "):
		var out []*minicli.Response

		for _, host := range hosts {
			m.last[host] = max(m.last[host], len(m.cmds[host])) + 1
			c := meshCommand{id: m.last[host], filter: m.filter}
			if host == "compute1" && m.overwrite != "" {
				c.filter = m.overwrite
			}
			m.cmds[host] = append(m.cmds[host], c)
			out = append(out, &minicli.Response{Host: host, Data: c.id})
		}

		return out
	case cmd.Base == ccCommandsCmd:
		if !m.answered {
			m.polls++
		}

		var out []*minicli.Response

		for _, host := range hosts {
			rows := make([][]string, 0, len(m.cmds[host]))

			for _, c := range m.cmds[host] {
				rows = append(rows, []string{strconv.Itoa(c.id), m.responses(host, c), c.filter})
			}

			out = append(out, mmtest.Tabular(host, []string{"id", "responses", "filter"}, rows...))
		}

		return out
	case strings.HasPrefix(cmd.Base, "cc response "):
		id := strings.Fields(cmd.Base)[2]

		return []*minicli.Response{
			mmtest.Text("head", "head's command "+id),
			mmtest.Text("compute1", "compute1's command "+id),
		}
	}

	return nil
}

// pollsUnanswered is how many times `cc commands` was polled before vm1's
// command had a response.
func (m *meshC2) pollsUnanswered() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.polls
}

func (m *meshC2) answer() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.answered = true
}

// On a cluster, ExecC2Command takes the command ID of the VM's host and waits
// for the response there, not for the headnode's command of the same ID.
func TestExecC2CommandOnAnotherHost(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	mesh := newMeshC2()

	received := useFakeMinimega(t, mesh.reply)
	useFastPolling(t, time.Millisecond)

	type result struct {
		id  string
		err error
	}

	done := make(chan result, 1)

	go func() {
		// A wait on a wrong ID ends with an error rather than hanging the test.
		id, err := (Minimega{}).ExecC2Command(
			C2NS("exp"), C2VM("vm1"), C2Command("hostname"), C2Wait(), C2Timeout(10*time.Second),
		)
		done <- result{id: id, err: err}
	}()

	// The headnode's command 1 has a response, but it is another VM's.
	polltest.Until(t, "vm1's command is polled twice without a response", func() bool {
		return mesh.pollsUnanswered() >= 2
	})

	select {
	case got := <-done:
		t.Fatalf("ExecC2Command(vm1, wait) = %q, %v before vm1 answered", got.id, got.err)
	default:
	}

	mesh.answer()

	got := <-done
	if got.err != nil || got.id != "1" {
		t.Errorf("ExecC2Command(vm1, wait) = %q, %v, want compute1's ID 1", got.id, got.err)
	}

	want := []string{"cc filter uuid=uuid-vm1", "cc exec hostname", clearCCFilterCmd, meshDelete("1")}
	if diff := cmp.Diff(want, ccSequence(received())); diff != "" {
		t.Errorf("ExecC2Command(vm1, wait) sent cc commands mismatch (-want +got):\n%s", diff)
	}
}

// The delete of a command that was waited on goes to the VM's host alone, so
// the headnode's command of the same ID, for another VM, is kept.
func TestC2CommandDeletedOnTheVMHost(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, wait := range map[string]func(id string) error{
		"ExecC2Command": func(string) error { return nil },
		"WaitForC2Response": func(id string) error {
			_, err := (Minimega{}).WaitForC2Response(C2NS("exp"), C2VM("vm1"), C2CommandID(id))

			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			mesh := newMeshC2()
			mesh.answer()

			useFakeMinimega(t, mesh.reply)
			useFastPolling(t, time.Millisecond)

			opts := []C2Option{C2NS("exp"), C2VM("vm1"), C2Command("hostname")}
			if name == "ExecC2Command" {
				opts = append(opts, C2Wait())
			}

			id, err := (Minimega{}).ExecC2Command(opts...)
			if err != nil {
				t.Fatalf("ExecC2Command(vm1) error = %v", err)
			}

			if err := wait(id); err != nil {
				t.Fatalf("%s(vm1, %s) error = %v", name, id, err)
			}

			got := map[string][]int{"head": mesh.ids("head"), "compute1": mesh.ids("compute1")}
			want := map[string][]int{"head": {1, 2, 3, 4, 5, 6}, "compute1": {}}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("%s(vm1) left cc commands mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// deleteC2Command sends the delete to the command's host alone, and to every
// host when the host is not known or is the headnode. It logs a failed delete.
func TestDeleteC2Command(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		host    string
		fail    bool
		want    string
		wantLog []string // host of each failed delete logged
	}{
		"host not known":  {host: "", want: "cc delete command 3"},
		"the headnode":    {host: "head", want: "cc delete command 3"},
		"another host":    {host: "compute1", want: meshDelete("3")},
		"a failed delete": {host: "compute1", fail: true, want: meshDelete("3"), wantLog: []string{"compute1"}},
	} {
		t.Run(name, func(t *testing.T) {
			logs := plogtest.Capture(t)
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch {
				case cmd.Base == "host" && cmd.Namespace == "minimega":
					return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"head"})}
				case tc.fail && strings.Contains(cmd.Base, "cc delete command "):
					return []*minicli.Response{{Host: tc.host, Error: "invalid command"}}
				}

				return nil
			})

			deleteC2Command("exp", tc.host, "3")

			var sent []string

			for _, cmd := range received() {
				if strings.Contains(cmd.Base, "cc delete command ") {
					sent = append(sent, cmd.Base)
				}
			}

			if diff := cmp.Diff([]string{tc.want}, sent); diff != "" {
				t.Errorf("deleteC2Command(exp, %q, 3) sent deletes mismatch (-want +got):\n%s", tc.host, diff)
			}

			records := logs.Records(t, plogtest.Message("deleting C2 command"))
			logged := make([]string, 0, len(records))

			for _, record := range records {
				logged = append(logged, fmt.Sprint(record["host"]))
			}

			if diff := cmp.Diff(tc.wantLog, logged, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("deleteC2Command(exp, %q, 3) logged failures mismatch (-want +got):\n%s", tc.host, diff)
			}
		})
	}
}

// With the VM given, WaitForC2Response and GetC2Response read the command on
// the VM's host.
func TestC2ResponseOnTheVMHost(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, call := range map[string]func(...C2Option) (string, error){
		"WaitForC2Response": (Minimega{}).WaitForC2Response,
		"GetC2Response":     (Minimega{}).GetC2Response,
	} {
		t.Run(name, func(t *testing.T) {
			mesh := newMeshC2()

			useFakeMinimega(t, mesh.reply)
			useFastPolling(t, time.Millisecond)

			id, err := (Minimega{}).ExecC2Command(C2NS("exp"), C2VM("vm1"), C2Command("hostname"))
			if err != nil {
				t.Fatalf("ExecC2Command(vm1) error = %v", err)
			}

			mesh.answer()

			got, err := call(C2NS("exp"), C2VM("vm1"), C2CommandID(id))
			if want := "compute1's command " + id; err != nil || got != want {
				t.Errorf("%s(vm1, %s) = %q, %v, want %q", name, id, got, err, want)
			}

			// Without the VM, any host's response is taken, as before.
			if got, err := call(C2NS("exp"), C2CommandID(id)); err != nil || got == "" {
				t.Errorf("%s(%s) = %q, %v, want a response", name, id, got, err)
			}
		})
	}
}

// A command whose ID on the VM's host names a command for another VM ends the
// wait at once.
func TestWaitForC2ResponseCommandMismatch(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	mesh := newMeshC2()
	mesh.cmds["compute1"] = []meshCommand{{id: 1, filter: "uuid=uuid-other && hostname=db"}}

	received := useFakeMinimega(t, mesh.reply)
	useFastPolling(t, time.Millisecond)

	_, err := (Minimega{}).WaitForC2Response(C2NS("exp"), C2VM("vm1"), C2CommandID("1"), C2Timeout(10*time.Second))
	if !errors.Is(err, ErrC2CommandMismatch) {
		t.Errorf("WaitForC2Response(vm1, 1) error = %v, want %v", err, ErrC2CommandMismatch)
	}

	if got := mmtest.Count(received(), meshDelete("1")); got != 1 {
		t.Errorf("WaitForC2Response(vm1, 1) sent %d %q, want 1", got, meshDelete("1"))
	}
}

// ExecC2Command reports a command whose ID on the VM's host does not target the
// VM, and deletes it.
func TestExecC2CommandMismatch(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	mesh := newMeshC2()
	mesh.overwrite = "uuid=uuid-other"

	received := useFakeMinimega(t, mesh.reply)
	useFastPolling(t, time.Millisecond)

	_, err := (Minimega{}).ExecC2Command(C2NS("exp"), C2VM("vm1"), C2Command("hostname"), C2Wait())
	if !errors.Is(err, ErrC2CommandMismatch) {
		t.Errorf("ExecC2Command(vm1, wait) error = %v, want %v", err, ErrC2CommandMismatch)
	}

	want := []string{"cc filter uuid=uuid-vm1", "cc exec hostname", clearCCFilterCmd, meshDelete("1")}
	if diff := cmp.Diff(want, ccSequence(received())); diff != "" {
		t.Errorf("ExecC2Command(vm1, wait) sent cc commands mismatch (-want +got):\n%s", diff)
	}
}

// The C2 calls give up with their context, or at their timeout, while they
// look for the VM and its client.
func TestC2CallsEndWithTheirContext(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	for name, call := range map[string]func() error{
		"IsC2ClientActive": func() error {
			return (Minimega{}).IsC2ClientActive(C2NS("exp"), C2VM("vm1"), C2Context(canceled))
		},
		"GetC2Response": func() error {
			_, err := (Minimega{}).GetC2Response(C2NS("exp"), C2VM("vm1"), C2CommandID("1"), C2Context(canceled))

			return err
		},
		"WaitForC2Response": func() error {
			_, err := (Minimega{}).WaitForC2Response(C2NS("exp"), C2VM("vm1"), C2CommandID("1"), C2Context(canceled))

			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, newMeshC2().reply)

			if err := call(); !errors.Is(err, context.Canceled) {
				t.Errorf("%s(vm1, canceled ctx) error = %v, want %v", name, err, context.Canceled)
			}

			// Only the delete, which runs whatever the context says, is sent.
			for _, cmd := range received() {
				if !strings.HasPrefix(cmd.Base, "cc delete command ") {
					t.Errorf("%s(vm1, canceled ctx) sent %q", name, cmd.Base)
				}
			}
		})
	}

	t.Run("canceled once the VM is found", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		mesh := newMeshC2()
		received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
			if cmd.Base == vmInfoCmd {
				cancel()
			}

			return mesh.reply(cmd)
		})

		if err := (Minimega{}).IsC2ClientActive(C2NS("exp"), C2VM("vm1"), C2Context(ctx)); !errors.Is(err, context.Canceled) {
			t.Errorf("IsC2ClientActive(vm1, canceled at vm info) error = %v, want %v", err, context.Canceled)
		}

		if got := mmtest.Count(received(), ccClientCmd); got != 0 {
			t.Errorf("IsC2ClientActive(vm1, canceled at vm info) sent %d cc client, want 0", got)
		}
	})

	t.Run("out of time once the VM is found", func(t *testing.T) {
		received := useFakeMinimega(t, newMeshC2().reply)

		if err := (Minimega{}).IsC2ClientActive(C2NS("exp"), C2VM("vm1"), C2Timeout(-time.Second)); !errors.Is(err, ErrC2ClientNotActive) {
			t.Errorf("IsC2ClientActive(vm1, no time) error = %v, want %v", err, ErrC2ClientNotActive)
		}

		if got := mmtest.Count(received(), ccClientCmd); got != 0 {
			t.Errorf("IsC2ClientActive(vm1, no time) sent %d cc client, want 0", got)
		}
	})
}

// WaitForC2Response for a VM that does not exist still deletes the command.
func TestWaitForC2ResponseMissingVM(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == vmInfoCmd {
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "uuid"})}
		}

		return nil
	})

	for name, call := range map[string]func(...C2Option) (string, error){
		"WaitForC2Response": (Minimega{}).WaitForC2Response,
		"GetC2Response":     (Minimega{}).GetC2Response,
	} {
		_, err := call(C2NS("exp"), C2VM("vm3"), C2CommandID("4"))
		if want := "vm vm3 does not exist"; err == nil || err.Error() != want {
			t.Errorf("%s(vm3, 4) error = %v, want %q", name, err, want)
		}
	}

	want := []string{vmInfoCmd, "cc delete command 4", vmInfoCmd}
	if diff := cmp.Diff(want, mmtest.Bases(received()), cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
		t.Errorf("WaitForC2Response and GetC2Response(vm3, 4) sent commands mismatch (-want +got):\n%s", diff)
	}
}

// ExecC2Command fails when the VM's host gives no command ID.
func TestExecC2CommandWithoutAnID(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	mesh := newMeshC2()

	for name, tc := range map[string]struct {
		answer []*minicli.Response
		want   string
	}{
		"only another host answers": {
			answer: []*minicli.Response{{Host: "head", Data: 6}},
			want:   "no answer from host compute1",
		},
		"the VM's host answers without an ID": {
			answer: []*minicli.Response{{Host: "compute1"}},
			want:   "no command ID in the answer",
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == "cc exec hostname" {
					return tc.answer
				}

				return mesh.reply(cmd)
			})

			_, err := (Minimega{}).ExecC2Command(C2NS("exp"), C2VM("vm1"), C2Command("hostname"), C2Wait())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ExecC2Command(vm1) error = %v, want one holding %q", err, tc.want)
			}

			// The filter is cleared and nothing is waited on or deleted.
			want := []string{"cc filter uuid=uuid-vm1", "cc exec hostname", clearCCFilterCmd}
			if diff := cmp.Diff(want, ccSequence(received())); diff != "" {
				t.Errorf("ExecC2Command(vm1) sent cc commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The headnode mounts and unmounts a VM on another host and answers alone (the
// fake's empty answer); that answer is enough.
func TestMountVMOnAnotherHost(t *testing.T) { //nolint:paralleltest // shares the fake minimega and common.MountBase
	mounts := t.TempDir()

	saved := common.MountBase
	common.MountBase = mounts //nolint:reassign // mounts go to a temporary directory

	t.Cleanup(func() { common.MountBase = saved }) //nolint:reassign // restore

	mesh := newMeshC2()
	received := useFakeMinimega(t, mesh.reply)

	for _, opt := range []C2Option{C2Mount(), C2Unmount()} {
		if _, err := (Minimega{}).ExecC2Command(C2NS("exp"), C2VM("vm1"), C2IDClientsByUUID(), opt); err != nil {
			t.Errorf("ExecC2Command(vm1, mount or unmount) error = %v", err)
		}
	}

	want := []string{"cc mount vm1 " + filepath.Join(mounts, "exp", "vm1"), "clear cc mount vm1"}
	if diff := cmp.Diff(want, ccSequence(received())); diff != "" {
		t.Errorf("ExecC2Command(vm1, mount, unmount) sent cc commands mismatch (-want +got):\n%s", diff)
	}
}

func TestCCCommandAnswer(t *testing.T) {
	t.Parallel()

	row := func(host, id, responses, filter string) map[string]string {
		return map[string]string{hostColumn: host, "id": id, "responses": responses, "filter": filter}
	}

	vm1 := ccCommand{ns: "exp", id: "7", host: "compute1", uuid: "uuid-vm1"}

	for name, tc := range map[string]struct {
		cmd      ccCommand
		rows     []map[string]string
		answered bool
		wantErr  error
	}{
		"a response on the VM's host": {
			cmd: vm1, rows: []map[string]string{row("compute1", "7", "1", "uuid=UUID-VM1")}, answered: true,
		},
		"no response on the VM's host yet": {
			cmd: vm1, rows: []map[string]string{row("compute1", "7", "0", "uuid=uuid-vm1")},
		},
		"another host's command of the same ID": {
			cmd: vm1, rows: []map[string]string{row("head", "7", "1", "uuid=uuid-db"), row("compute1", "7", "0", "uuid=uuid-vm1")},
		},
		"another command on the VM's host": {
			cmd: vm1, rows: []map[string]string{row("compute1", "8", "1", "uuid=uuid-vm1")},
		},
		"the ID names another VM's command on the VM's host": {
			cmd: vm1, rows: []map[string]string{row("compute1", "7", "1", "uuid=uuid-db")}, answered: true,
			wantErr: ErrC2CommandMismatch,
		},
		"a command without a filter is not the VM's": {
			cmd: vm1, rows: []map[string]string{row("compute1", "7", "1", "")}, answered: true,
			wantErr: ErrC2CommandMismatch,
		},
		"any host's response when the host is not known": {
			cmd:      ccCommand{ns: "exp", id: "7", host: "", uuid: "uuid-vm1"},
			rows:     []map[string]string{row("head", "7", "1", "uuid=uuid-db"), row("compute1", "7", "2", "uuid=uuid-vm1")},
			answered: true,
		},
		"another VM's command when the host is not known": {
			cmd:  ccCommand{ns: "exp", id: "7", host: "", uuid: "uuid-vm1"},
			rows: []map[string]string{row("head", "7", "1", "uuid=uuid-db")},
		},
		"any response when nothing is known": {
			cmd:      ccCommand{ns: "exp", id: "7", host: "", uuid: ""},
			rows:     []map[string]string{row("head", "7", "1", "")},
			answered: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			answered, err := tc.cmd.answer(tc.rows)
			if answered != tc.answered || !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Errorf("%+v.answer(%v) = %t, %v, want %t, %v", tc.cmd, tc.rows, answered, err, tc.answered, tc.wantErr)
			}
		})
	}
}

func TestFilterTargets(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		filter string
		want   bool
	}{
		{filter: "uuid=u1", want: true},
		{filter: "UUID=U1", want: true},
		{filter: "hostname=web && uuid=u1", want: true},
		{filter: "uuid=u1 && os=linux", want: true},
		{filter: "uuid=u10", want: false},
		{filter: "hostname=u1", want: false},
		{filter: "name=uuid=u1x", want: false},
		{filter: "", want: false},
	} {
		if got := filterTargets(tc.filter, "u1"); got != tc.want {
			t.Errorf("filterTargets(%q, u1) = %t, want %t", tc.filter, got, tc.want)
		}
	}
}

// c2Client finds the client by the VM's UUID: a client of another VM that
// reports the VM's name as its hostname is not the VM's.
func TestC2ClientMatchesTheVMUUID(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		clients [][]string
		want    error
		target  c2Target
	}{
		"another VM's client with the VM's hostname": {
			clients: [][]string{{"uuid-other", "vm1"}},
			want:    ErrC2ClientNotActive,
		},
		"the VM's client with its hostname in another case": {
			clients: [][]string{{"uuid-other", "vm1"}, {"UUID-VM1", "VM1"}},
			target:  c2Target{name: "vm1", uuid: "uuid-vm1", host: "head"},
		},
		"the VM's client before the hostname is set": {
			clients: [][]string{{"uuid-vm1", "localhost"}},
			want:    ErrC2ClientNotActive,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch cmd.Base {
				case vmInfoCmd:
					return []*minicli.Response{mmtest.Tabular("head", []string{"name", "uuid"}, []string{"vm1", "uuid-vm1"})}
				case ccClientCmd:
					return []*minicli.Response{filtered(cmd, []string{"uuid", "hostname"}, tc.clients)}
				}

				return nil
			})

			target, err := (Minimega{}).c2Client(NewC2Options(C2NS("exp"), C2VM("vm1"), C2Timeout(20*time.Millisecond)))
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) || target != tc.target {
				t.Errorf("c2Client(vm1) = %+v, %v, want %+v, %v", target, err, tc.target, tc.want)
			}

			if got, want := received()[1].Raw, `.record false namespace "exp" .columns "uuid","hostname" .filter uuid=uuid-vm1 cc client`; got != want {
				t.Errorf("c2Client(vm1) sent %q, want %q", got, want)
			}
		})
	}
}
