//go:build linux

package cmd

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// socketPeer gives the pid of the process that listens on a unix socket, from SO_PEERCRED.
func TestDoctorSocketPeer(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "peer.sock")

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatalf("Listen(%s) error = %v", path, err)
	}

	defer func() { _ = ln.Close() }()

	pid, err := socketPeer(t.Context(), path, time.Second)
	if err != nil {
		t.Fatalf("socketPeer(%s) error = %v", path, err)
	}

	if pid != os.Getpid() {
		t.Errorf("socketPeer(%s) = %d, want %d", path, pid, os.Getpid())
	}

	if _, err := socketPeer(t.Context(), filepath.Join(t.TempDir(), "none.sock"), time.Second); err == nil {
		t.Errorf("socketPeer() of a missing socket error = nil, want an error")
	}
}
