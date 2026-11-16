package web

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// NormalizeFileServerEndpoint converts the ui.file-server-endpoint setting into a
// listen endpoint, or "" when the file server is off.
func NormalizeFileServerEndpoint(value string) (string, error) {
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

// parseFileServerPort validates a TCP port of plain digits in 1-65535.
func parseFileServerPort(value string) error {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return fmt.Errorf("invalid port %q", value)
	}

	if port, err := strconv.Atoi(value); err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("port %q out of range 1-65535", value)
	}

	return nil
}
