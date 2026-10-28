package cmd

import (
	"strings"
	"testing"
)

// The commands that files add to a parent from their init functions are in the command tree.
func TestSubcommandOf(t *testing.T) {
	for _, path := range []string{
		"vm capture list", "experiment captures", "vlan free", "vlan check", "vm qos get", "vm qos set", "vm qos clear",
		"vm cdrom insert", "vm cdrom eject", "vm forward list", "vm forward add", "vm forward remove", "vm screenshot",
		"vm snapshot delete", "admin user list", "admin user get", "admin user create", "admin user update",
		"admin user delete", "admin role list", "admin role get", "admin role create", "admin role update",
		"admin role delete", "admin token create",
	} {
		names := strings.Fields(path)

		if got := subcommandOf(names...); got.CommandPath() != "phenix "+path {
			t.Errorf("subcommandOf(%q).CommandPath() = %q, want %q", path, got.CommandPath(), "phenix "+path)
		}
	}

	defer func() {
		if recover() == nil {
			t.Error("subcommandOf(vm, nope) did not panic, want a panic for a command that does not exist")
		}
	}()

	subcommandOf("vm", "nope")
}
