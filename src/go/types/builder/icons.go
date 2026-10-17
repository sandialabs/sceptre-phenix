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

// iconKeys is the bounded registry of builder icon keys: one key for each
// built-in icon that the web UI draws (BuilderIcon.vue). It holds the
// semantic keys that the builder assigns (server, desktop, switch,
// container, external, router, firewall) and the keys a user can choose.
//
// Keys are short, opaque identifiers. The front end resolves them to bundled
// drawings, so validation rejects remote URLs and file paths. The list is
// sorted.
var iconKeys = []string{ //nolint:gochecknoglobals // immutable registry
	"alpine",
	"android",
	"apple",
	"blackhat",
	"bsd",
	"cell-tower",
	"centos",
	iconContainer,
	iconDesktop,
	"distribution",
	iconExternal,
	"fedora",
	iconFirewall,
	"fox",
	"hacker",
	"human",
	"kali",
	"laptop",
	"linux",
	"lizard",
	"minimega",
	"ntp",
	"opensuse",
	"pfsense",
	"phenix",
	"plc",
	"printer",
	"redhat",
	"relay",
	"robot",
	iconRouter,
	"rtac",
	"rtu",
	IconServer,
	"smartphone",
	"switch",
	"terminal",
	"transmission",
	"ubuntu",
	"vlan",
	"windows",
	"wireshark",
	"zeek",
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
