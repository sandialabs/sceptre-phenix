//go:build linux

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

var errNotUnixConn = errors.New("not a unix socket connection")

// socketPeer is the pid of the process that listens on the unix socket at path, from SO_PEERCRED. It is 0 when that
// process is in another pid namespace, such as another container.
func socketPeer(ctx context.Context, path string, timeout time.Duration) (int, error) {
	dialer := net.Dialer{Timeout: timeout} //nolint:exhaustruct // partial initialization

	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return 0, fmt.Errorf("connecting to %s: %w", path, err)
	}

	defer func() { _ = conn.Close() }()

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errNotUnixConn
	}

	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("reading the socket of %s: %w", path, err)
	}

	var (
		cred    *unix.Ucred
		credErr error
	)

	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) //nolint:gosec // G115 a descriptor fits an int
	}); err != nil {
		return 0, fmt.Errorf("reading the socket of %s: %w", path, err)
	}

	if credErr != nil {
		return 0, fmt.Errorf("reading the peer of %s: %w", path, credErr)
	}

	return int(cred.Pid), nil
}
