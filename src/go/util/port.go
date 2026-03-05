package util

import (
	"net"
)

// Taken (pretty much) straight from the following (with a few minor edits):
// https://github.com/phayes/freeport/blob/74d24b5ae9f58fbe4057614465b11352f71cdbea/freeport.go

// GetFreePort asks the kernel for a free open port that is ready to use.
func GetFreePort(ip string) (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", ip+":0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}

	defer func() { _ = l.Close() }()

	tcpAddr, _ := l.Addr().(*net.TCPAddr)
	return tcpAddr.Port, nil
}
