package web

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// normalizeFileServerEndpoint converts the fileserver option into a listen endpoint.
func normalizeFileServerEndpoint(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return "", nil
	}

	if err := parseFileServerPort(value); err == nil {
		return net.JoinHostPort("127.0.0.1", value), nil
	}

	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", errors.New("expected port or host:port")
	}

	if err := parseFileServerPort(port); err != nil {
		return "", err
	}

	return net.JoinHostPort(host, port), nil
}

// parseFileServerPort validates a numeric TCP port.
func parseFileServerPort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid port %q", value)
	}

	if port < 0 || port > 65535 {
		return fmt.Errorf("port %d out of range", port)
	}

	return nil
}
