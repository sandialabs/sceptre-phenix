package types

import (
	"strings"

	"phenix/store"
)

type Upgrader interface {
	Upgrade(oldVersion string, spec map[string]any, md store.ConfigMetadata) (any, error)
}

// Key should be in the form of `kind/version` -- ie. Topology/v1.
var upgraders = make(map[string]Upgrader) //nolint:gochecknoglobals // global registry

func RegisterUpgrader(v string, u Upgrader) {
	v = strings.ToLower(v)
	upgraders[v] = u
}

func GetUpgrader(v string) Upgrader { //nolint:ireturn // interface
	v = strings.ToLower(v)

	return upgraders[v]
}
