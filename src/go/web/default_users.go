package web

import (
	"fmt"
	"strings"
)

// DefaultAdminName is the user phenix ui creates when ui.users is empty.
const DefaultAdminName = "admin@foo.com"

// Where a ui.users entry's password and role are, after its name.
const (
	defaultUserPasswordField = 1
	defaultUserRoleField     = 2
)

// DefaultUser is one ui.users entry, name:password:role[:resource...].
type DefaultUser struct {
	Name      string
	Password  string //nolint:gosec // G117 the entry's password, which no caller logs
	Role      string
	Resources []string
}

// ParseDefaultUser parses the ui.users entry at position, counted from 1, as
// ConfigureUsers does. An entry without a role fails, with only its name set.
func ParseDefaultUser(position int, entry string) (DefaultUser, error) {
	creds := strings.Split(entry, ":")

	if len(creds) <= defaultUserRoleField {
		// Name the entry by position and by its name, never by a field that
		// may be a password.
		var name string
		if len(creds) > defaultUserPasswordField {
			name = creds[0]
		}

		return DefaultUser{Name: name, Password: "", Role: "", Resources: nil}, fmt.Errorf(
			"default user %d (%q) is not name:password:role[:resource...]", position, name,
		)
	}

	return DefaultUser{
		Name:      creds[0],
		Password:  creds[defaultUserPasswordField],
		Role:      creds[defaultUserRoleField],
		Resources: creds[defaultUserRoleField+1:],
	}, nil
}
