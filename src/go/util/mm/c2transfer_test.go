package mm

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

// transferCluster is c2Cluster that also takes `cc recv` and answers
// `cc exitcode` with exitcode, or with an error when exitcode is "".
func transferCluster(exitcode string) mmtest.Handler {
	cluster := new(c2Cluster)

	return func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case strings.HasPrefix(cmd.Base, "cc recv "):
			return cluster.reply(mmtest.Command{Raw: cmd.Raw, Namespace: cmd.Namespace, Base: "cc exec recv"})
		case strings.HasPrefix(cmd.Base, "cc exitcode "):
			if exitcode == "" {
				return []*minicli.Response{mmtest.Error("head", `invalid command: "cc exitcode"`)}
			}

			return []*minicli.Response{mmtest.Text("head", exitcode+"\n")}
		}

		return cluster.reply(cmd)
	}
}

// transferSequence lists the received cc commands that receive a file, read
// an exit code or delete a command, in order.
func transferSequence(cmds []mmtest.Command) []string {
	var out []string

	for _, cmd := range cmds {
		for _, prefix := range []string{"cc recv ", "cc exitcode ", "cc delete "} {
			if strings.HasPrefix(cmd.Base, prefix) {
				out = append(out, cmd.Base)
			}
		}
	}

	return out
}

// ExecC2Command posts `cc recv` for the VM alone and waits for its response.
func TestExecC2CommandReceivesAFile(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := useFakeMinimega(t, transferCluster("0"))
	useFastPolling(t, time.Millisecond)

	id, err := ExecC2Command(C2NS("exp"), C2VM("vm1"), C2IDClientsByUUID(), C2RecvFile("/etc/hosts"), C2Wait())
	if err != nil || id != "1" {
		t.Fatalf("ExecC2Command(recv /etc/hosts) = %q, %v, want 1, nil", id, err)
	}

	if diff := cmp.Diff([]string{"cc recv /etc/hosts", "cc delete command 1"}, transferSequence(received())); diff != "" {
		t.Errorf("ExecC2Command(recv) sent commands mismatch (-want +got):\n%s", diff)
	}

	if got := mmtest.Count(received(), "cc filter uuid=uuid-vm1"); got != 1 {
		t.Errorf("ExecC2Command(recv) set the VM's filter %d times, want 1", got)
	}
}

// GetC2ExitCode reads `cc exitcode` for the VM's UUID on the VM's host, and
// gives ErrNoC2ExitCode when minimega has no exit code.
func TestGetC2ExitCode(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	tests := []struct {
		name     string
		exitcode string
		opts     []C2Option
		want     int
		wantErr  error
		wantSent []string
	}{
		{
			name:     "zero",
			exitcode: "0",
			opts:     []C2Option{C2VM("vm1"), C2CommandID("3")},
			want:     0,
			wantSent: []string{"cc exitcode 3 uuid-vm1"},
		},
		{
			name:     "not zero",
			exitcode: "42",
			opts:     []C2Option{C2VM("vm1"), C2CommandID("3")},
			want:     42,
			wantSent: []string{"cc exitcode 3 uuid-vm1"},
		},
		{
			name:     "not reported",
			exitcode: "",
			opts:     []C2Option{C2VM("vm1"), C2CommandID("3")},
			wantErr:  ErrNoC2ExitCode,
			wantSent: []string{"cc exitcode 3 uuid-vm1"},
		},
		{
			name:     "not a number",
			exitcode: "x",
			opts:     []C2Option{C2VM("vm1"), C2CommandID("3")},
			wantErr:  ErrNoC2ExitCode,
			wantSent: []string{"cc exitcode 3 uuid-vm1"},
		},
		{name: "no VM", exitcode: "0", opts: []C2Option{C2CommandID("3")}, wantErr: errAnyExitCode},
		{name: "a bad ID", exitcode: "0", opts: []C2Option{C2VM("vm1"), C2CommandID("3 uuid")}, wantErr: errAnyExitCode},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := useFakeMinimega(t, transferCluster(test.exitcode))

			got, err := GetC2ExitCode(append([]C2Option{C2NS("exp")}, test.opts...)...)

			switch {
			case test.wantErr == nil && (err != nil || got != test.want):
				t.Errorf("GetC2ExitCode() = %d, %v, want %d, nil", got, err, test.want)
			case errors.Is(test.wantErr, errAnyExitCode) && err == nil:
				t.Errorf("GetC2ExitCode() = %d, nil, want an error", got)
			case test.wantErr != nil && !errors.Is(test.wantErr, errAnyExitCode) && !errors.Is(err, test.wantErr):
				t.Errorf("GetC2ExitCode() = %d, %v, want error %v", got, err, test.wantErr)
			}

			if diff := cmp.Diff(test.wantSent, transferSequence(received())); diff != "" {
				t.Errorf("GetC2ExitCode() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// errAnyExitCode is the wanted error of a case that expects any error.
var errAnyExitCode = errors.New("any error")

// An MM without exit codes gives ErrNoC2ExitCode.
func TestGetC2ExitCodeWithoutSupport(t *testing.T) { //nolint:paralleltest // replaces DefaultMM
	original := DefaultMM
	t.Cleanup(func() { DefaultMM = original })

	DefaultMM = struct{ MM }{}

	if _, err := GetC2ExitCode(C2NS("exp"), C2VM("vm1"), C2CommandID("1")); !errors.Is(err, ErrNoC2ExitCode) {
		t.Errorf("GetC2ExitCode() with an MM without exit codes error = %v, want %v", err, ErrNoC2ExitCode)
	}
}

// C2ResponsePath joins the namespace, the command ID and the UUID onto the
// minimega files directory, and refuses a part that could leave it.
func TestC2ResponsePath(t *testing.T) { //nolint:paralleltest // sets common.PhenixBase
	base := t.TempDir()

	saved := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the files directory is a temporary one

	t.Cleanup(func() { common.PhenixBase = saved }) //nolint:reassign // restore

	files := GetMMFullPath("")

	tests := []struct {
		ns, id, uuid string
		want         string
	}{
		{ns: "exp", id: "4", uuid: "0a1b-2c", want: filepath.Join(files, "exp", "miniccc_responses", "4", "0a1b-2c")},
		{ns: "exp", id: "4", want: filepath.Join(files, "exp", "miniccc_responses", "4")},
		{ns: "..", id: "4"},
		{ns: "a/b", id: "4"},
		{ns: "exp", id: "../4"},
		{ns: "exp", id: "4", uuid: "../x"},
		{ns: "", id: "4"},
	}

	for _, test := range tests {
		got, err := C2ResponsePath(test.ns, test.id, test.uuid)

		switch {
		case test.want == "" && err == nil:
			t.Errorf("C2ResponsePath(%q, %q, %q) = %q, nil, want an error", test.ns, test.id, test.uuid, got)
		case test.want != "" && (err != nil || got != test.want):
			t.Errorf("C2ResponsePath(%q, %q, %q) = %q, %v, want %q, nil", test.ns, test.id, test.uuid, got, err, test.want)
		}
	}
}
