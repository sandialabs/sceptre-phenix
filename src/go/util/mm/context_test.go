package mm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
)

// holdShared answers commands with handle (nil for an empty success) while its own "hold" command
// holds the shared connection until unhold or the test's end; received is as useFakeMinimega's.
func holdShared(t *testing.T, handle mmtest.Handler) (func(), func() []mmtest.Command) {
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

	return unhold, received
}

// A VM listing for a caller whose context ends while its commands wait for
// the shared connection gives up, having sent nothing.
func TestGetVMInfoGivesUpWithItsContext(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	_, received := holdShared(t, nil)

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

	if diff := cmp.Diff([]string{"hold"}, mmtest.Bases(received())); diff != "" {
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
	received := useFakeMinimega(t, nil)

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
		"StopVMCapture": func() error { return StopVMCapture(opts...) },
		"RedeployVM":    func() error { return RedeployVM(opts...) },
	}

	for name, read := range reads {
		if err := read(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s(vm1, canceled ctx) error = %v, want %v", name, err, context.Canceled)
		}
	}

	if got := received(); len(got) != 0 {
		t.Errorf("reads with a canceled ctx sent %q, want nothing", mmtest.Bases(got))
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

// A cluster host list whose context ends partway gives up with its context's error, neither taking
// the mesh check that gave up for a changed mesh nor keeping the partial list for later callers.
func TestClusterHostsGiveUpWithTheirContext(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mesh := fakeMesh{head: "head", peers: "compute1", members: "compute1", compute: []string{"compute1"}}

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "mesh list peers" {
			cancel()
		}

		return mesh.reply(cmd)
	})

	if _, _, err := (Minimega{}).GetClusterHostsObserved(ctx, false); !errors.Is(err, context.Canceled) {
		t.Errorf("GetClusterHostsObserved(ctx canceled at mesh list peers) error = %v, want %v", err, context.Canceled)
	}

	want := []string{"minimega: host", "mesh list peers"}
	if diff := cmp.Diff(want, hostCommands(received())); diff != "" {
		t.Errorf("GetClusterHostsObserved(ctx canceled at mesh list peers) sent commands mismatch (-want +got):\n%s", diff)
	}

	if _, _, ok := recentHosts.get(); ok {
		t.Error("GetClusterHostsObserved(ctx canceled at mesh list peers) kept its partial list for later callers")
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
