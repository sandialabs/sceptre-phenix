package mm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
)

// holdShared answers commands with handle (nil for an empty success), except
// a "hold" command it sends itself, which holds the shared minimega connection
// until the returned function is called or the test ends. It returns the
// listing of the commands received, as useFakeMinimega does.
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
			t.Fatalf("GetVMInfoObserved reported %v, want %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetVMInfoObserved kept waiting after its context was canceled")
	}

	if got := mmtest.Bases(received()); !slices.Equal(got, []string{"hold"}) {
		t.Fatalf("minimega received %q, want only the hold", got)
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
				t.Fatalf("the call failed: %v", err)
			}

			var got []string

			for _, base := range mmtest.Bases(received()) {
				if base != vmInfoCmd && base != ccClientCmd {
					got = append(got, base)
				}
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("minimega received %q, want %q", got, tc.want)
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
		"state": func() error {
			_, err := GetVMState(opts...)

			return err
		},
		"host": func() error {
			_, err := GetVMHost(opts...)

			return err
		},
		"screenshot": func() error {
			_, err := GetVMScreenshot(opts...)

			return err
		},
		"capture stop": func() error { return StopVMCapture(opts...) },
		"redeploy":     func() error { return RedeployVM(opts...) },
	}

	for name, read := range reads {
		if err := read(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: got %v, want %v", name, err, context.Canceled)
		}
	}

	if got := received(); len(got) != 0 {
		t.Fatalf("minimega received %q, want nothing", mmtest.Bases(got))
	}
}
