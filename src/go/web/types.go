package web

import (
	"phenix/web/rbac"
	"sort"
)

type SignupRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type CreateUserRequest struct {
	Username      string   `json:"username"`
	Password      string   `json:"password"`
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
	Password string `json:"pass"`
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
}

type Policy struct {
	Resources     []string `json:"resources"`
	ResourceNames []string `json:"resourceNames"`
	Verbs         []string `json:"verbs"`
}

type Role struct {
	Name     string   `json:"name"`
	Policies []Policy `json:"policies"`
}

func userFromRBAC(u rbac.User) User {
	role, _ := u.Role()
	user := User{
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

	role := Role{
		Name:     r.Spec.Name,
		Policies: policies,
	}

	return role
}

func resourceNamesFromRBAC(r rbac.Role) []string {
	rnamemap := make(map[string]struct{})
	for _, p := range r.Spec.Policies {
		var skip bool

		for _, pn := range p.Resources {
			if pn == "disks" || pn == "hosts" || pn == "users" {
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

	var rnames []string

	for n := range rnamemap {
		rnames = append(rnames, n)
	}

	sort.Strings(rnames)
	return rnames
}
