//go:build linux

package cmd

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// socketPeer gives the pid of the process that listens on a unix socket, from
// SO_PEERCRED, and an error that names the path when it cannot connect.
func TestDoctorSocketPeer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		setup   func(t *testing.T, path string) // makes what is at path
		ctx     func(t *testing.T) context.Context
		wantErr string // "" for the pid of this process
		wantIs  error  // an error that the returned error wraps
	}{
		"listener": {
			setup: func(t *testing.T, path string) {
				t.Helper()

				var lc net.ListenConfig

				ln, err := lc.Listen(t.Context(), "unix", path)
				if err != nil {
					t.Fatalf("Listen(%s) error = %v", path, err)
				}

				t.Cleanup(func() { _ = ln.Close() })
			},
		},
		"missing socket": {wantErr: "connecting to "},
		"not a socket": {
			setup: func(t *testing.T, path string) {
				t.Helper()
				writeTestFile(t, filepath.Dir(path), filepath.Base(path), "")
			},
			wantErr: "connecting to ",
		},
		"context ended": {
			setup: func(t *testing.T, path string) {
				t.Helper()

				var lc net.ListenConfig

				ln, err := lc.Listen(t.Context(), "unix", path)
				if err != nil {
					t.Fatalf("Listen(%s) error = %v", path, err)
				}

				t.Cleanup(func() { _ = ln.Close() })
			},
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				ctx, cancel := context.WithCancel(t.Context())
				cancel()

				return ctx
			},
			wantErr: "operation was canceled",
			wantIs:  context.Canceled,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := shortSocketPath(t)
			if tc.setup != nil {
				tc.setup(t, path)
			}

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(t)
			}

			pid, err := socketPeer(ctx, path, time.Second)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), path) {
					t.Errorf("socketPeer(%s) = %d, %v; want an error naming the path and %q", path, pid, err, tc.wantErr)
				}

				if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
					t.Errorf("socketPeer(%s) error = %v, want one that wraps %v", path, err, tc.wantIs)
				}

				return
			}

			if err != nil || pid != os.Getpid() {
				t.Errorf("socketPeer(%s) = %d, %v; want %d", path, pid, err, os.Getpid())
			}
		})
	}
}
