package util

import (
	"fmt"
	"net"
)

// Taken (pretty much) straight from the following (with a few minor edits):
// https://github.com/phayes/freeport/blob/74d24b5ae9f58fbe4057614465b11352f71cdbea/freeport.go

// GetFreePort asks the kernel for a free open port that is ready to use.
func GetFreePort(ip string) (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:0", ip))
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}

	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port, nil
}
