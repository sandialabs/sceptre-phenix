package util

import (
	"fmt"
	"os"

	"golang.org/x/exp/slices"
	"inet.af/netaddr"
)

func MustHostname() string {
	name, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	return name
}

func UnusedSubnet(start netaddr.IPPrefix, used []netaddr.IPPrefix) (netaddr.IPPrefix, error) {
	var (
		subnet = start
		err    error
	)

	for {
		if slices.Contains(used, subnet) {
			subnet, err = subnet.Range().To().Next().Prefix(start.Bits())
			if err != nil {
				return netaddr.IPPrefix{}, fmt.Errorf("getting next subnet: %w", err)
			}

			continue
		}

		return subnet, nil
	}
}
