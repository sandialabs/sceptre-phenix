package web

import (
	"sort"

	"phenix/web/rbac"
)

type SignupRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"` //nolint:gosec // G117 the request carries the password
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type CreateUserRequest struct {
	Username      string   `json:"username"`
	Password      string   `json:"password"` //nolint:gosec // G117 the request carries the password
	FirstName     string   `json:"first_name"`
	LastName      string   `json:"last_name"`
	RoleName      string   `json:"role_name"`
	ResourceNames []string `json:"resource_names"`
}

type UpdateUserRequest struct {
	CreateUserRequest

	NewPassword string `json:"new_password"`
}

type CreateTokenRequest struct {
	Lifetime    string `json:"lifetime"`
	Description string `json:"desc"`
}

type CreateTokenResponse struct {
	Token       string `json:"token"`
	Description string `json:"desc"`
	Expiration  string `json:"exp"`
}

type LoginRequest struct {
	Username string `json:"user"`
	Password string `json:"pass"` //nolint:gosec // G117 the request carries the password
}

type LoginResponse struct {
	User  User   `json:"user"`
	Token string `json:"token"`
}

type User struct {
	Username      string   `json:"username"`
	FirstName     string   `json:"first_name"`
	LastName      string   `json:"last_name"`
	ResourceNames []string `json:"resource_names"`
	Role          Role     `json:"role"`
	ProxyToken    string   `json:"proxy_token,omitempty"`
}

type Policy struct {
	Resources     []string `json:"resources"`
	ResourceNames []string `json:"resourceNames"`
	Verbs         []string `json:"verbs"`
}

type Role struct {
	Name     string   `json:"name"`
	Policies []Policy `json:"policies"`
	// Permissions is set only when a request asks for it with ?permissions=true.
	Permissions []rbac.Permission `json:"permissions,omitzero"`
}

func userFromRBAC(u rbac.User) User {
	role, _ := u.Role()

	user := User{ //nolint:exhaustruct // partial initialization
		Username:      u.Username(),
		FirstName:     u.FirstName(),
		LastName:      u.LastName(),
		ResourceNames: resourceNamesFromRBAC(role),
		Role:          roleFromRBAC(role),
	}

	return user
}

func roleFromRBAC(r rbac.Role) Role {
	policies := make([]Policy, len(r.Spec.Policies))
	for i, p := range r.Spec.Policies {
		policies[i] = Policy{
			Resources:     p.Resources,
			ResourceNames: p.ResourceNames,
			Verbs:         p.Verbs,
		}
	}

	role := Role{ //nolint:exhaustruct // roleView sets the permissions
		Name:     r.Spec.Name,
		Policies: policies,
	}

	return role
}

// roleView is roleFromRBAC, with the known permissions that r grants when
// withPermissions is true.
func roleView(r rbac.Role, withPermissions bool) Role {
	role := roleFromRBAC(r)
	if withPermissions {
		role.Permissions = r.AllowedPermissions()
	}

	return role
}

func resourceNamesFromRBAC(r rbac.Role) []string {
	rnamemap := make(map[string]struct{})

	for _, p := range r.Spec.Policies {
		var skip bool

		for _, pn := range p.Resources {
			if pn == "disks" || pn == "hosts" || pn == resourceUsers {
				skip = true

				break
			}
		}

		if skip {
			continue
		}

		for _, n := range p.ResourceNames {
			rnamemap[n] = struct{}{}
		}
	}

	rnames := make([]string, 0, len(rnamemap))

	for n := range rnamemap {
		rnames = append(rnames, n)
	}

	sort.Strings(rnames)

	return rnames
}
