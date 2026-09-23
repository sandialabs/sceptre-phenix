package v1

import (
	"fmt"
	"strings"
)

const (
	// minimegaWildcardVM is the VM target minimega expands to every VM in a
	// namespace (`Wildcard` in minimega's cmd/minimega/main.go). minimega's
	// reserved-name check compares names case-sensitively.
	minimegaWildcardVM = "all"

	// phenixHostname prefixes the Windows startup wrapper, phenix-startup.ps1,
	// which the startup app stages next to each Windows node's
	// <hostname>-startup.ps1. It is also the hostname `phenix image` bakes into
	// the images it builds.
	phenixHostname = "phenix"
)

// checkHostnameKeywords reports hostnames that minimega or phenix read as
// something other than a node name. It returns an error for a hostname that
// cannot work for the node, and a warning for a hostname that works but is one
// step away from one that does not.
func checkHostnameKeywords(hostname, osType string) (string, error) {
	switch {
	case hostname == minimegaWildcardVM:
		return "", fmt.Errorf(
			"hostname '%s' is reserved: minimega uses 'all' as its wildcard VM target, so it refuses to launch "+
				"a VM named 'all', and commands that target a VM by name (such as 'vm kill all') act on every VM "+
				"in the experiment",
			hostname,
		)
	// Schema validation rejects a hostname that YAML parses as a number
	// (`hostname: 42`), but a quoted string of digits (`hostname: '42'`)
	// passes it, so check for one here.
	case isDigits(hostname):
		return "", fmt.Errorf(
			"hostname '%s' is all digits: minimega reads 'vm launch kvm %s' as a number of VMs to launch rather "+
				"than a VM name, and reads a numeric VM target as a VM ID",
			hostname, hostname,
		)
	case strings.EqualFold(hostname, minimegaWildcardVM):
		return fmt.Sprintf(
			"hostname '%s' differs from the reserved name 'all' only by case: minimega accepts it because its "+
				"reserved-name check is case-sensitive, but any tool or script that lowercases VM names would "+
				"treat it as minimega's wildcard for every VM",
			hostname,
		), nil
	case strings.EqualFold(hostname, phenixHostname) && strings.EqualFold(osType, "windows"):
		return "", fmt.Errorf(
			"hostname '%s' can't be used for a Windows node: the startup app writes each Windows node's startup "+
				"script to '<hostname>-startup.ps1' in the experiment's startup directory, where it also stages "+
				"'phenix-startup.ps1', the startup wrapper every Windows node runs; the file names collide (in any "+
				"casing on a case-insensitive filesystem), so one file overwrites the other",
			hostname,
		)
	case strings.EqualFold(hostname, phenixHostname):
		return fmt.Sprintf(
			"hostname '%s' matches 'phenix', the hostname 'phenix image' bakes into the images it builds: until "+
				"the startup app renames them, other VMs built from those images also report 'phenix' to miniccc, "+
				"so hostname-based C2 checks for this node (such as delay.c2 without useUUID) can match the wrong "+
				"VM; this hostname also fails validation if the node's os_type is changed to windows",
			hostname,
		), nil
	}

	return "", nil
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}
