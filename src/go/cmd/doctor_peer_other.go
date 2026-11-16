//go:build !linux

package cmd

import (
	"context"
	"errors"
	"time"
)

var errNoSocketPeer = errors.New("only Linux gives the peer of a unix socket")

// socketPeer is the pid of the process that listens on a unix socket, which doctor reads only on Linux.
func socketPeer(context.Context, string, time.Duration) (int, error) {
	return 0, errNoSocketPeer
}
