package builder

import (
	"slices"
	"strings"
)

// IconServer is the generic icon key assigned to devices whose operating
// system has no dedicated icon.
const IconServer = "server"

// iconKeys is the bounded registry of builder icon keys. It covers the icon
// assets shipped with the web UI (linux, windows, redhat, centos, router,
// firewall, printer, external, vlan) plus the semantic keys the builder itself
// assigns (server, desktop, switch, container).
//
// Keys are deliberately opaque, short identifiers: the front end resolves them
// to bundled assets, so remote URLs and file paths are rejected.
var iconKeys = []string{ //nolint:gochecknoglobals // immutable registry
	"centos",
	"container",
	"desktop",
	"external",
	"firewall",
	"linux",
	"printer",
	"redhat",
	"router",
	IconServer,
	"switch",
	"vlan",
	"windows",
}

// IconKeys returns the sorted registry of icon keys a device may declare. The
// returned slice is a copy and safe to modify.
func IconKeys() []string {
	return slices.Clone(iconKeys)
}

// IsIconKey reports whether key is a member of the icon key registry. The empty
// string is not a registry member; it means "use the default icon" and is
// accepted by [Document.Validate].
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
