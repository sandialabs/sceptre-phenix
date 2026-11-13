package minimega_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"
	"pgregory.net/rapid"

	"phenix/api/minimega"
)

// fakeMinimega serves minimega's command socket protocol from a temporary
// base directory, and returns it. It answers each request with the responses
// from handle, and closes the connection when handle returns none.
func fakeMinimega(t *testing.T, handle func(miniclient.Request) []miniclient.Response) string {
	t.Helper()

	base, err := os.MkdirTemp("", "mm") //nolint:usetesting // t.TempDir paths are too long for a socket
	if err != nil {
		t.Fatalf("creating minimega base directory: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(base) })

	listener, err := net.Listen("unix", filepath.Join(base, "minimega"))
	if err != nil {
		t.Fatalf("listening on minimega socket: %v", err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func() {
				defer func() { _ = conn.Close() }()

				dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)

				for {
					var req miniclient.Request
					if err := dec.Decode(&req); err != nil {
						return
					}

					responses := handle(req)
					if len(responses) == 0 {
						return
					}

					for i, resp := range responses {
						resp.More = i < len(responses)-1

						if err := enc.Encode(resp); err != nil {
							return
						}
					}
				}
			}()
		}
	}()

	return base
}

// RunAt collects each host's answer, its rendered output and its errors, and
// tells a minimega error and a lost connection apart.
func TestRunAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		responses []miniclient.Response
		want      minimega.Result
		wantErr   error
	}{
		{
			name: "one host",
			responses: []miniclient.Response{{
				Resp:     minicli.Responses{{Host: "mm1", Header: []string{"name"}, Tabular: [][]string{{"vm1"}}}},
				Rendered: "name\nvm1",
			}},
			want: minimega.Result{
				Output:    "name\nvm1",
				Responses: []minimega.HostResponse{{Host: "mm1", Header: []string{"name"}, Tabular: [][]string{{"vm1"}}}},
				Errors:    []string{},
			},
		},
		{
			name: "an error on one of two hosts",
			responses: []miniclient.Response{
				{Resp: minicli.Responses{{Host: "mm1", Response: "ok"}}, Rendered: "ok"},
				{Resp: minicli.Responses{{Host: "mm2", Error: "vm not found"}}},
			},
			want: minimega.Result{
				Output: "ok",
				Responses: []minimega.HostResponse{
					{Host: "mm1", Response: "ok"}, {Host: "mm2", Error: "vm not found"},
				},
				Errors: []string{"mm2: vm not found"},
			},
			wantErr: minimega.ErrCommandFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sent []string

			base := fakeMinimega(t, func(req miniclient.Request) []miniclient.Response {
				sent = append(sent, req.Command)

				return tt.responses
			})

			got, err := minimega.RunAt(t.Context(), base, "vm info")
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil) != (err == nil) {
				t.Errorf("RunAt(vm info) error = %v, want %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("RunAt(vm info) mismatch (-want +got):\n%s", diff)
			}

			if !slices.Equal(sent, []string{"vm info"}) {
				t.Errorf("RunAt(vm info) sent %q, want only vm info", sent)
			}
		})
	}
}

// RunAt reports a minimega that is not there, or that closes the connection
// without an answer, as a lost connection.
func TestRunAtLostConnection(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "none")
	if _, err := minimega.RunAt(t.Context(), missing, "vm info"); !errors.Is(err, minimega.ErrConnectionLost) {
		t.Errorf("RunAt(no minimega) error = %v, want %v", err, minimega.ErrConnectionLost)
	}

	silent := fakeMinimega(t, func(miniclient.Request) []miniclient.Response { return nil })

	got, err := minimega.RunAt(t.Context(), silent, "vm info")
	if !errors.Is(err, minimega.ErrConnectionLost) {
		t.Errorf("RunAt(minimega that hangs up) error = %v, want %v", err, minimega.ErrConnectionLost)
	}

	if len(got.Errors) != 1 {
		t.Errorf("RunAt(minimega that hangs up) errors = %q, want the lost connection", got.Errors)
	}
}

// RunAt stops waiting once its context is done, with its error.
func TestRunAtCanceled(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	base := fakeMinimega(t, func(miniclient.Request) []miniclient.Response {
		<-release

		return nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := minimega.RunAt(ctx, base, "vm info"); !errors.Is(err, context.Canceled) {
		t.Errorf("RunAt(canceled) error = %v, want %v", err, context.Canceled)
	}
}

// CommandLine sends a single argument as it is, and quotes each of several
// arguments so that minimega's parser reads them back unchanged.
func TestCommandLineParsesBack(t *testing.T) {
	minicli.Reset()
	t.Cleanup(minicli.Reset)

	noop := func(*minicli.Command, chan<- minicli.Responses) {}
	minicli.MustRegister(&minicli.Handler{Patterns: []string{"echo [args]..."}, Call: noop})

	if got := minimega.CommandLine("exp", []string{"vm info"}); got != "namespace exp vm info" {
		t.Errorf(`CommandLine("exp", ["vm info"]) = %q, want "namespace exp vm info"`, got)
	}

	rapid.Check(t, func(t *rapid.T) {
		args := rapid.SliceOfN(rapid.StringMatching(`[^\x00]*`), 1, 4).Draw(t, "args")

		command := minimega.CommandLine("", append([]string{"echo"}, args...))

		c, err := minicli.Compile(command)
		if err != nil {
			t.Fatalf("minicli.Compile(%q) error = %v", command, err)
		}

		if got := c.ListArgs["args"]; !slices.Equal(got, args) {
			t.Fatalf("CommandLine sent %q as %q, minimega read %q", args, command, got)
		}
	})
}
