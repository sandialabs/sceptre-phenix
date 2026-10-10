package builder

import (
	"slices"
	"strings"
)

// IconServer is the generic icon key assigned to devices whose operating
// system has no dedicated icon.
const IconServer = "server"

// The icon keys that generation assigns by VM type and operating system, and
// by node type.
const (
	iconContainer = "container"
	iconDesktop   = "desktop"
	iconExternal  = "external"
	iconFirewall  = "firewall"
	iconRouter    = "router"
)

// iconKeys is the bounded registry of builder icon keys. It covers the icon
// assets shipped with the web UI (linux, windows, redhat, centos, router,
// firewall, printer, external, vlan) plus the semantic keys the builder itself
// assigns (server, desktop, switch, container).
//
// Keys are short, opaque identifiers. The front end resolves them to bundled
// assets, so validation rejects remote URLs and file paths.
var iconKeys = []string{ //nolint:gochecknoglobals // immutable registry
	"centos",
	iconContainer,
	iconDesktop,
	iconExternal,
	iconFirewall,
	"linux",
	"printer",
	"redhat",
	iconRouter,
	IconServer,
	"switch",
	"vlan",
	"windows",
}

// IsIconKey reports whether key is a member of the icon key registry. The empty
// string is not a registry member. It means "use the default icon", and
// [Document.Validate] accepts it.
func IsIconKey(key string) bool {
	return slices.Contains(iconKeys, key)
}

// iconKeyLooksExternal reports whether a key looks like a URL, file path, or
// other external reference rather than a registry key.
func iconKeyLooksExternal(key string) bool {
	if strings.ContainsAny(key, "/\\:?#") {
		return true
	}

	return strings.HasPrefix(key, ".") || strings.Contains(key, "..")
}
