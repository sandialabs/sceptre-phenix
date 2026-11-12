package mm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// holdShared answers commands with handle (nil for an empty success) while its own "hold" command
// holds the shared connection until the test's end; received is as useFakeMinimega's.
func holdShared(t *testing.T, handle mmtest.Handler) func() []mmtest.Command {
	t.Helper()

	release := make(chan struct{})
	unhold := sync.OnceFunc(func() { close(release) })

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "hold" {
			<-release

			return nil
		}

		if handle == nil {
			return nil
		}

		return handle(cmd)
	})

	held := make(chan error, 1)

	go func() {
		cmd := mmcli.NewCommand()
		cmd.Command = "hold"

		held <- mmcli.ErrorResponse(mmcli.Run(cmd))
	}()

	// LIFO: the hold is let go before it is waited for
	t.Cleanup(func() { <-held })
	t.Cleanup(unhold)

	mmtest.Await(t, "hold")

	return received
}

// poolHold is the command that holds a read-only connection in these tests; phenix itself never sends it.
const poolHold = "mesh status"

// holdReadOnly is holdShared for the connections of mmcli.RunReadOnly: a poolHold command holds
// each of them until the test's end.
func holdReadOnly(t *testing.T) func() []mmtest.Command {
	t.Helper()

	release := make(chan struct{})

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == poolHold {
			<-release
		}

		return nil
	})

	held := make(chan error, mmcli.ReadOnlyLimit())

	for range mmcli.ReadOnlyLimit() {
		go func() {
			cmd := mmcli.NewCommand()
			cmd.Command = poolHold

			held <- mmcli.ErrorResponse(mmcli.RunReadOnly(context.Background(), mmcli.HeadRead, cmd))
		}()
	}

	// LIFO: the hold is let go before it is waited for
	t.Cleanup(func() {
		for range mmcli.ReadOnlyLimit() {
			<-held
		}
	})
	t.Cleanup(func() { close(release) })

	polltest.Until(t, "every read-only connection is held", func() bool {
		return mmtest.Count(received(), poolHold) == mmcli.ReadOnlyLimit()
	})

	return received
}

// MeshShellRead sends on the read-only connections and MeshShellResponse on the shared one, so each
// still runs while all connections of the other are held.
func TestMeshShellReadAndResponseUseTheirOwnConnections(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	t.Run("MeshShellRead while the shared connection is held", func(t *testing.T) {
		received := holdShared(t, func(mmtest.Command) []*minicli.Response {
			return []*minicli.Response{mmtest.Text("head", "head\n")}
		})

		if got, err := MeshShellRead(t.Context(), "", "hostname"); err != nil || got != "head" {
			t.Errorf("MeshShellRead(hostname) = %q, %v; want %q, nil", got, err, "head")
		}

		if got := mmtest.Count(received(), "shell hostname"); got != 1 {
			t.Errorf("MeshShellRead(hostname) sent it %d times, want 1", got)
		}
	})

	t.Run("MeshShellResponse while the read-only connections are held", func(t *testing.T) {
		received := holdReadOnly(t)

		// the fake gives no answer, so only what minimega received counts
		_, _ = DefaultMM.MeshShellResponse(t.Context(), "", "hostname")

		if got := mmtest.Count(received(), "shell hostname"); got != 1 {
			t.Errorf("MeshShellResponse(hostname) sent it %d times, want 1", got)
		}
	})
}

// A VM listing for a caller whose context ends while its commands wait for
// a read-only connection gives up, having sent nothing.
func TestGetVMInfoGivesUpWithItsContext(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	received := holdReadOnly(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, obs := GetVMInfoObserved(NS("exp"), Context(ctx))
		done <- obs.Err
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("GetVMInfoObserved(canceled ctx) error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetVMInfoObserved(canceled ctx) kept waiting after its context was canceled")
	}

	want := slices.Repeat([]string{poolHold}, mmcli.ReadOnlyLimit())
	if diff := cmp.Diff(want, mmtest.Bases(received())); diff != "" {
		t.Errorf("GetVMInfoObserved(canceled ctx) sent commands mismatch (-want +got):\n%s", diff)
	}
}

// A call that changes something in several commands goes on once its first
// change is sent, whatever its context says, so it is never left half done.
func TestChangesFinishOnceStarted(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	tests := map[string]struct {
		// the command whose arrival cancels the context
		first string
		call  func(ctx context.Context) error
		want  []string
	}{
		"tags": {
			first: "clear vm tag",
			call: func(ctx context.Context) error {
				return SetVMTags(NS("exp"), VMName("vm1"), Tags(map[string]string{"a": "1"}), Context(ctx))
			},
			want: []string{"clear vm tag vm1 ", `vm tag vm1 "a" "1"`},
		},
		"cc command": {
			first: "cc filter",
			call: func(ctx context.Context) error {
				_, err := ExecC2Command(C2NS("exp"), C2VM("vm1"), C2Command("hostname"), C2Context(ctx))

				return err
			},
			want: []string{"cc filter uuid=uuid-vm1", "cc exec hostname", clearCCFilterCmd},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var (
				ctx, cancel = context.WithCancel(context.Background())
				cluster     = new(c2Cluster)
			)

			t.Cleanup(cancel)

			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if strings.HasPrefix(cmd.Base, tc.first) {
					cancel()
				}

				return cluster.reply(cmd)
			})

			if err := tc.call(ctx); err != nil {
				t.Errorf("call canceled at %q error = %v", tc.first, err)
			}

			var got []string

			for _, base := range mmtest.Bases(received()) {
				if base != vmInfoCmd && base != ccClientCmd {
					got = append(got, base)
				}
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("call canceled at %q sent commands mismatch (-want +got):\n%s", tc.first, diff)
			}
		})
	}
}

// A read whose commands gave up reports its context's error, not an empty
// answer such as a VM not found.
func TestReadsReportTheirContextEnding(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := []Option{NS("exp"), VMName("vm1"), Context(ctx)}

	reads := map[string]func() error{
		"GetVMState": func() error {
			_, err := GetVMState(opts...)

			return err
		},
		"GetVMHost": func() error {
			_, err := GetVMHost(opts...)

			return err
		},
		"GetVMScreenshot": func() error {
			_, err := GetVMScreenshot(opts...)

			return err
		},
		"GetVLANs": func() error {
			_, err := (Minimega{}).GetVLANs(opts...)

			return err
		},
		"StopVMCapture": func() error { return StopVMCapture(opts...) },
		"RedeployVM":    func() error { return RedeployVM(opts...) },
	}

	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, nil)

			if err := read(); !errors.Is(err, context.Canceled) {
				t.Errorf("%s(vm1, canceled ctx) error = %v, want %v", name, err, context.Canceled)
			}

			if got := received(); len(got) != 0 {
				t.Errorf("%s(vm1, canceled ctx) sent %q, want nothing", name, mmtest.Bases(got))
			}
		})
	}
}

// CloseTunnel gives up with its context until a close succeeds: neither a close that gave up nor
// one minimega answered with an error has changed anything yet.
func TestCloseTunnelGivesUpWithItsContext(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for _, tc := range []struct {
		name     string
		cancelOn string
		want     []string
	}{
		{name: "cancel before first close", cancelOn: vmInfoCmd, want: nil},
		{name: "first close errors, then cancel", cancelOn: "cc tunnel close", want: []string{"cc tunnel close vm1 3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				base := strings.TrimSpace(cmd.Base)
				if strings.HasPrefix(base, tc.cancelOn) {
					cancel()
				}

				switch {
				case strings.HasPrefix(base, "cc tunnel list "):
					return []*minicli.Response{
						mmtest.Tabular("head", []string{"vm", "id", "src port", "dst", "dst port"},
							[]string{"vm1", "3", "50001", "127.0.0.1", "22"},
							[]string{"vm1", "4", "50002", "127.0.0.1", "22"}),
					}
				case strings.HasPrefix(base, "cc tunnel close"):
					return []*minicli.Response{{Host: "head", Error: "boom"}}
				}

				return vm1OnHeadnode(cmd)
			})

			if err := CloseTunnel(NS("exp"), VMName("vm1"), Context(ctx)); !errors.Is(err, context.Canceled) {
				t.Errorf("CloseTunnel(vm1) canceled at %q error = %v, want %v", tc.cancelOn, err, context.Canceled)
			}

			var closes []string

			for _, cmd := range received() {
				if base := strings.TrimSpace(cmd.Base); strings.HasPrefix(base, "cc tunnel close") {
					closes = append(closes, base)
				}
			}

			if diff := cmp.Diff(tc.want, closes); diff != "" {
				t.Errorf("CloseTunnel(vm1) canceled at %q sent closes mismatch (-want +got):\n%s", tc.cancelOn, diff)
			}
		})
	}
}

// A cluster host list whose context ends partway gives up with its context's error, not going on
// to the next command. The headnode's `host` runs beside the mesh reads, so it may or may not be
// sent first. TestGetClusterHostsErrorIsNotReused checks that an error is not kept.
func TestClusterHostsGiveUpWithTheirContext(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for _, tc := range []struct {
		cancelOn string
		allowed  []string
	}{
		{cancelOn: "mesh list peers", allowed: []string{"minimega: host", "mesh list peers"}},
		{cancelOn: meshHostCmd, allowed: []string{"minimega: host", "mesh list peers", meshHostCmd}},
	} {
		t.Run(tc.cancelOn, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			mesh := fakeMesh{head: "head", peers: "compute1", compute: []string{"compute1"}}

			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == tc.cancelOn {
					cancel()
				}

				return mesh.reply(cmd)
			})

			// The cancel reaches the shared flight's run from a goroutine, so the
			// run can send more commands first: check the commands without it.
			call := fmt.Sprintf("getClusterHosts(ctx canceled at %s)", tc.cancelOn)

			if _, err := getClusterHosts(ctx, func() {}, true); !errors.Is(err, context.Canceled) {
				t.Errorf("%s error = %v, want %v", call, err, context.Canceled)
			}

			cmds := received()
			sent := hostCommands(cmds)

			if !slices.Contains(sent, tc.cancelOn) {
				t.Errorf("%s sent %q, want %q among them", call, sent, tc.cancelOn)
			}

			for _, cmd := range sent {
				if !slices.Contains(tc.allowed, cmd) {
					t.Errorf("%s sent %q, want only some of %q", call, cmd, tc.allowed)
				}
			}

			for _, cmd := range cmds {
				if isDiskUsage(cmd) {
					t.Errorf("%s measured disk usage with %q, want no measurement", call, cmd.Base)
				}
			}
		})
	}
}

// A VM listing whose follow-up reads (captures, snapshot disks) gave up after `vm info` was
// answered reports ctx's error, not a complete listing.
func TestGetVMInfoReportsFollowUpsThatGaveUp(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base != vmInfoCmd {
			return nil
		}

		cancel()

		return []*minicli.Response{mmtest.Tabular("head",
			[]string{"name", stateColumn, "uptime", "disks", "snapshot"},
			[]string{"vm1", "RUNNING", "1h0m0s", "/snap/vm1.qcow2", "true"},
		)}
	})

	vms, err := (Minimega{}).getVMInfo(NewOptions(NS("exp"), Context(ctx)), nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("getVMInfo(ctx canceled at vm info) = %v, %v; want %v", vms, err, context.Canceled)
	}

	if n := mmtest.Count(received(), captureCmd); n != 0 {
		t.Errorf("getVMInfo(ctx canceled at vm info) sent %d capture listings, want 0", n)
	}
}
