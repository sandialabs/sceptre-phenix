package forwardtypes

import (
	"errors"
	"fmt"
	"net"
	"regexp"
)

// maxHostnameSize is the longest DNS host name.
const maxHostnameSize = 253

var (
	// ErrBadHost is returned for a forward destination host that is not an IP address or a host name.
	ErrBadHost = errors.New("not a host")

	// hostRegex matches a DNS host name: labels of letters, digits and inner hyphens, joined by dots.
	hostRegex = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// CheckHost checks the destination host of a forward, which minimega gets in a
// command: an IP address or a DNS host name.
func CheckHost(host string) error {
	if net.ParseIP(host) != nil || (len(host) <= maxHostnameSize && hostRegex.MatchString(host)) {
		return nil
	}

	return fmt.Errorf("%w: %q is not an IP address or a host name", ErrBadHost, host)
}
