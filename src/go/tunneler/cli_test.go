package main

import (
	"bytes"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	ft "phenix/web/forward/forwardtypes"
)

// The list, activate, move and deactivate commands send their request through
// the unix socket and report the errors of the server.
func TestCLIManagesListeners(t *testing.T) {
	startTestSocket(t)

	var (
		manager  = useListenerManager(t)
		local, _ = manager.add(ft.Listener{Exp: "exp", VM: "vm", DstHost: "10.0.0.1", DstPort: 22})
		freePort = strconv.Itoa(freeTCPPort(t))
	)

	steps := []struct {
		args          []string
		wantErr       bool
		wantIs        error
		wantListening bool
		wantPort      string
	}{
		{args: []string{"list"}, wantPort: "0"},
		{args: []string{"activate", "1"}, wantListening: true, wantPort: "0"},
		{args: []string{"activate", "1"}, wantErr: true, wantListening: true, wantPort: "0"},
		{args: []string{"move", "1", freePort}, wantListening: true, wantPort: freePort},
		{args: []string{"deactivate", "1"}, wantPort: freePort},
		{args: []string{"deactivate", "1"}, wantErr: true, wantPort: freePort},
		{args: []string{"activate", "2"}, wantErr: true, wantPort: freePort},
		{args: []string{"move", "2", "2222"}, wantErr: true, wantPort: freePort},
		{args: []string{"move", "1", "0"}, wantErr: true, wantPort: freePort},
		{args: []string{"move", "x", "2222"}, wantErr: true, wantIs: strconv.ErrSyntax, wantPort: freePort},
		{args: []string{"move", "1", "x"}, wantErr: true, wantIs: strconv.ErrSyntax, wantPort: freePort},
		{args: []string{"move", "1"}, wantErr: true, wantPort: freePort},
		{args: []string{"activate", "x"}, wantErr: true, wantIs: strconv.ErrSyntax, wantPort: freePort},
		{args: []string{"deactivate", "x"}, wantErr: true, wantIs: strconv.ErrSyntax, wantPort: freePort},
		{args: []string{"deactivate", "0"}, wantErr: true, wantPort: freePort},
		{args: []string{"list", "extra"}, wantErr: true, wantPort: freePort},
	}

	for _, step := range steps {
		ok := t.Run(strings.Join(step.args, " "), func(t *testing.T) {
			_, err := runCLI(t, step.args...)
			if (err != nil) != step.wantErr {
				t.Fatalf("running %q error = %v, want error %t", step.args, err, step.wantErr)
			}

			if step.wantIs != nil && !errors.Is(err, step.wantIs) {
				t.Errorf("running %q error = %v, want %v", step.args, err, step.wantIs)
			}

			_ = manager.withListener(local.ID, func(ll *LocalListener) error {
				if ll.Listening != step.wantListening {
					t.Errorf("listener listening = %t, want %t", ll.Listening, step.wantListening)
				}

				if got := strconv.Itoa(ll.SrcPort); got != step.wantPort {
					t.Errorf("listener port = %s, want %s", got, step.wantPort)
				}

				return nil
			})
		})

		if !ok {
			break
		}
	}
}

func TestCLIListWithoutListeners(t *testing.T) {
	startTestSocket(t)
	useListenerManager(t)

	if _, err := runCLI(t, "list"); err != nil {
		t.Errorf("running list error = %v, want nil", err)
	}
}

// Each client command fails when no server listens on the socket.
func TestCLIWithoutServer(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "missing.sock")
	swapValue(t, &socketPath, func() string { return path })

	for _, args := range [][]string{{"list"}, {"move", "1", "2222"}, {"activate", "1"}, {"deactivate", "1"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := runCLI(t, args...); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("running %q error = %v, want %v", args, err, fs.ErrNotExist)
			}
		})
	}
}

func TestCLIWithoutArgumentsShowsHelp(t *testing.T) {
	out, err := runCLI(t)
	if err != nil {
		t.Fatalf("running with no arguments error = %v, want nil", err)
	}

	for _, want := range []string{"phenix-tunneler", "Available Commands", "activate", "deactivate", "list", "move"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output does not contain %q:\n%s", want, out)
		}
	}
}

// startUnixSocket removes a stale file at the socket path and fails when it
// cannot make the directory or the socket.
func TestStartUnixSocket(t *testing.T) {
	dir := shortTempDir(t)

	t.Run("stale file", func(t *testing.T) {
		path := filepath.Join(dir, "stale.sock")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("writing stale file: %v", err)
		}

		swapValue(t, &socketPath, func() string { return path })

		listener, err := startUnixSocket()
		if err != nil {
			t.Fatalf("startUnixSocket() error = %v", err)
		}

		defer func() { _ = listener.Close() }()

		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatalf("connecting to socket: %v", err)
		}

		_ = conn.Close()
	})

	t.Run("directory is a file", func(t *testing.T) {
		file := filepath.Join(dir, "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatalf("writing file: %v", err)
		}

		swapValue(t, &socketPath, func() string { return filepath.Join(file, "phenix", "tunneler.sock") })

		if _, err := startUnixSocket(); !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("startUnixSocket() error = %v, want %v", err, syscall.ENOTDIR)
		}
	})

	t.Run("path too long", func(t *testing.T) {
		path := filepath.Join(dir, strings.Repeat("s", 200)+".sock")
		swapValue(t, &socketPath, func() string { return path })

		if listener, err := startUnixSocket(); err == nil {
			_ = listener.Close()

			t.Error("startUnixSocket() with a long path error = nil, want error")
		}
	})
}

// startTestSocket serves the unix socket at a short path until cleanup.
func startTestSocket(t *testing.T) {
	t.Helper()

	path := filepath.Join(shortTempDir(t), "phenix", "tunneler.sock")
	swapValue(t, &socketPath, func() string { return path })

	listener, err := startUnixSocket()
	if err != nil {
		t.Fatalf("starting unix socket: %v", err)
	}

	t.Cleanup(func() { _ = listener.Close() })
}

// runCLI runs the root command with args and gives the text that cobra writes.
// Command output that goes to [os.Stdout] is not in the text.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	if !listCmd.HasParent() {
		rootCmd.AddCommand(listCmd, activateCmd, deactivateCmd, moveCmd)
	}

	var out bytes.Buffer

	rootCmd.SetArgs(append([]string{}, args...))
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)

	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	err := rootCmd.Execute()

	return out.String(), err
}
