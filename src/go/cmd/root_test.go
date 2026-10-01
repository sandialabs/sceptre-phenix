package cmd

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSudoRanPhenix(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("unable to determine test executable: %v", err)
	}

	exeName := filepath.Base(exe)

	tests := []struct {
		name        string
		sudoCommand string
		want        bool
	}{
		{
			name:        "empty SUDO_COMMAND",
			sudoCommand: "",
			want:        false,
		},
		{
			name:        "sudo ran phenix directly",
			sudoCommand: "/usr/local/bin/phenix config list",
			want:        true,
		},
		{
			name:        "sudo ran current test binary",
			sudoCommand: exe + " -test.run TestSudoRanPhenix",
			want:        true,
		},
		{
			name:        "sudo ran su (root shell escalation)",
			sudoCommand: "/bin/su",
			want:        false,
		},
		{
			name:        "sudo ran su dash (root shell escalation)",
			sudoCommand: "/bin/su -",
			want:        false,
		},
		{
			name:        "sudo ran bash",
			sudoCommand: "/bin/bash",
			want:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUDO_COMMAND", tc.sudoCommand)

			if got := sudoRanPhenix(); got != tc.want {
				t.Errorf("sudoRanPhenix() with SUDO_COMMAND=%q (exe=%s) = %v, want %v", tc.sudoCommand, exeName, got, tc.want)
			}
		})
	}
}

// TestServerOptionsClient checks that the root command's request for the
// server's options has the preflight bound: against a socket whose listener
// accepts every connection and never answers, the request ends within the
// bound, lowered here, with a timeout. The root command ignores that error,
// as it does for a server that is not there.
func TestServerOptionsClient(t *testing.T) {
	lowerPreflightTimeout(t, 200*time.Millisecond)

	socket := shortSocketPath(t)

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on %s: %v", socket, err)
	}

	var (
		mu    sync.Mutex
		conns []net.Conn
	)

	t.Cleanup(func() {
		_ = listener.Close()

		mu.Lock()
		defer mu.Unlock()

		for _, conn := range conns {
			_ = conn.Close()
		}
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()

	done := make(chan error, 1)

	go func() {
		resp, err := serverOptionsClient(socket).Get("http://unix/api/v1/options")
		if err == nil {
			_ = resp.Body.Close()
		}

		done <- err
	}()

	select {
	case err := <-done:
		var urlErr *url.Error
		if !errors.As(err, &urlErr) || !urlErr.Timeout() {
			t.Fatalf("Get() error = %v, want a timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request for the server's options had no answer after 5s; want it to end within the preflight bound")
	}
}
