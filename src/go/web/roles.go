package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/mux"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

const (
	resourceRoles = "roles"

	// maxRoleName is the most characters of a role name and role config name.
	maxRoleName = 128

	// The largest role request body, and the most policies a role has and
	// resources, verbs or resource names a policy has.
	maxRoleBody     = 1 << 20
	maxRolePolicies = 256
	maxPolicyItems  = 64

	// reasonEscalation is the event.reason of a role change refused because
	// the role would grant more than the requester holds.
	reasonEscalation = "privilege escalation"
)

// roleChanges serializes the changes of roles, and of the users' copies of
// them, in one phenix process.
var roleChanges sync.Mutex //nolint:gochecknoglobals // one lock for the process

// errRoleRequest is wrapped by every error of a role request body.
var errRoleRequest = errors.New("invalid role request")

// CreateRoleRequest is the body of POST /roles.
type CreateRoleRequest struct {
	Name         string    `json:"name"`
	MetadataName string    `json:"metadataName,omitempty"`
	Policies     *[]Policy `json:"policies"`
}

// UpdateRoleRequest is the body of PATCH /roles/{name}. Name, when set, must
// be the role's name: a role is not renamed.
type UpdateRoleRequest struct {
	Name     string    `json:"name,omitempty"`
	Policies *[]Policy `json:"policies"`
}

// RoleUpdate is the answer to PATCH /roles/{name}: the role, how many users'
// copies of it changed, and those of the users that the requester may list.
type RoleUpdate struct {
	Role  Role     `json:"role"`
	Users []string `json:"users"`
	Count int      `json:"count"`
}

// decodeStrict decodes the JSON object of body into v, refusing unknown fields
// and anything after the object.
func decodeStrict(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", errRoleRequest, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: data after the JSON object", errRoleRequest)
	}

	return nil
}

// parseCreateRole returns the config name and the spec of the role that body,
// a POST /roles body, creates. The config name defaults to roleSlug of the
// name. The error wraps errRoleRequest or rbac.ErrPolicyInvalid.
func parseCreateRole(body []byte) (string, *v1.RoleSpec, error) {
	var req CreateRoleRequest
	if err := decodeStrict(body, &req); err != nil {
		return "", nil, err
	}

	if err := checkRoleName(req.Name); err != nil {
		return "", nil, err
	}

	configName := req.MetadataName
	if configName == "" {
		configName = roleSlug(req.Name)
	}

	if !validRoleConfigName(configName) {
		return "", nil, fmt.Errorf(
			"%w: metadataName %q must be 1 to %d of the characters A-Z, a-z, 0-9, '_', '@', '.' and '-', other than \".\" and \"..\"",
			errRoleRequest, configName, maxRoleName,
		)
	}

	policies, err := checkPolicies(req.Policies)
	if err != nil {
		return "", nil, err
	}

	return configName, &v1.RoleSpec{Name: req.Name, Policies: policies}, nil
}

// checkPolicies returns the policies of a request as a role spec holds them.
// The error wraps errRoleRequest for no policies key, too many policies or
// items, or a policy that grants roles create, patch or delete, which only the
// built-in Global Admin role holds, or wraps rbac.ErrPolicyInvalid.
func checkPolicies(list *[]Policy) ([]*v1.PolicySpec, error) {
	if list == nil {
		return nil, fmt.Errorf("%w: policies is required", errRoleRequest)
	}

	if len(*list) > maxRolePolicies {
		return nil, fmt.Errorf("%w: a role has at most %d policies", errRoleRequest, maxRolePolicies)
	}

	for i, p := range *list {
		if len(p.Resources) > maxPolicyItems || len(p.Verbs) > maxPolicyItems || len(p.ResourceNames) > maxPolicyItems {
			return nil, fmt.Errorf(
				"%w: policy %d: a policy has at most %d resources, verbs and resource names each", errRoleRequest, i, maxPolicyItems,
			)
		}
	}

	policies := policySpecs(*list)
	if err := rbac.ValidatePolicies(policies); err != nil {
		return nil, err //nolint:wrapcheck // the error says which policy is wrong
	}

	granting := rbac.Role{Spec: &v1.RoleSpec{Name: "", Policies: policies}}

	for _, verb := range []string{"create", verbPatch, "delete"} {
		if granting.Allowed(resourceRoles, verb) {
			return nil, fmt.Errorf(
				"%w: a custom role cannot grant roles %s; only the built-in Global Admin role changes roles", errRoleRequest, verb,
			)
		}
	}

	return policies, nil
}

// parseUpdateRole returns the name and the policies that body, a PATCH
// /roles/{name} body, gives. The error wraps errRoleRequest or
// rbac.ErrPolicyInvalid.
func parseUpdateRole(body []byte) (string, []*v1.PolicySpec, error) {
	var req UpdateRoleRequest
	if err := decodeStrict(body, &req); err != nil {
		return "", nil, err
	}

	policies, err := checkPolicies(req.Policies)
	if err != nil {
		return "", nil, err
	}

	return req.Name, policies, nil
}

// checkRoleName returns an error wrapping errRoleRequest unless name is 1 to
// maxRoleName characters of UTF-8 without "/", control or format characters
// (such as a zero-width space), or spaces other than " " and none at either
// end, and is not "." or "..", so it is one URL path segment.
func checkRoleName(name string) error {
	bad := func(r rune) bool {
		return r == '/' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || (unicode.IsSpace(r) && r != ' ')
	}

	ok := name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= maxRoleName &&
		strings.TrimSpace(name) == name && name != "." && name != ".." && !strings.ContainsFunc(name, bad)
	if !ok {
		return fmt.Errorf(
			"%w: name %q must be 1 to %d characters, without '/', control or format characters, or space at either end",
			errRoleRequest, name, maxRoleName,
		)
	}

	return nil
}

// validRoleConfigName reports whether name is a config name that is also one
// URL path segment.
func validRoleConfigName(name string) bool {
	return name != "" && len(name) <= maxRoleName && name != "." && name != ".." && config.NameRegex.MatchString(name)
}

// roleSlug returns name in lower case with each run of "-" and characters
// that a config name cannot hold changed to one "-", and no "-" at either
// end, so that "Lab Admin" becomes "lab-admin".
func roleSlug(name string) string {
	var (
		b    strings.Builder
		dash bool
	)

	for _, r := range strings.ToLower(name) {
		if r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_@.", r)) {
			b.WriteRune(r)

			dash = false
		} else if !dash {
			b.WriteByte('-')

			dash = true
		}
	}

	return strings.Trim(b.String(), "-")
}

// policySpecs returns the policies as a role spec holds them, with no list of
// resource names for an empty one.
func policySpecs(policies []Policy) []*v1.PolicySpec {
	specs := make([]*v1.PolicySpec, len(policies))

	for i, p := range policies {
		specs[i] = &v1.PolicySpec{Resources: p.Resources, ResourceNames: p.ResourceNames, Verbs: p.Verbs}
		if len(p.ResourceNames) == 0 {
			specs[i].ResourceNames = nil
		}
	}

	return specs
}

// roleConflict returns why a new role may not have the config name or the
// name, or "" if it may: a built-in role has it, or a role has it as either
// of its names, since GET /roles/{name} finds a role by both. Names compare
// without case, so "GLOBAL ADMIN" is a built-in role's name.
func roleConflict(roles []*rbac.Role, builtin []string, configName, name string) string {
	same := func(a string) func(string) bool { return func(b string) bool { return strings.EqualFold(a, b) } }

	for _, n := range []string{configName, name} {
		if slices.ContainsFunc(builtin, same(n)) {
			return fmt.Sprintf("the name %q belongs to a built-in role", n)
		}
	}

	for _, role := range roles {
		for _, n := range []string{configName, name} {
			if strings.EqualFold(role.ConfigName(), n) || strings.EqualFold(role.Spec.Name, n) {
				return fmt.Sprintf("the role %q already has the name %q", role.ConfigName(), n)
			}
		}
	}

	return ""
}

// refuseBuiltinRole returns the 409 for a change of role when it is a built-in role,
// found by either of its names, or the 500 when those cannot be read.
func refuseBuiltinRole(role *rbac.Role) error {
	builtin, err := config.BuiltinRoles()
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "error reading the built-in roles")
	}

	if slices.Contains(builtin, role.ConfigName()) || slices.Contains(builtin, role.Spec.Name) {
		return weberror.New(http.StatusConflict, "the role "+role.ConfigName()+" is a built-in role and cannot be changed")
	}

	return nil
}

// roleWriteError is the answer to err, from storing or deleting a role.
func roleWriteError(err error) *weberror.WebError {
	switch {
	case errors.Is(err, store.ErrExist):
		return weberror.New(http.StatusConflict, "a role config of that name already exists")
	case errors.Is(err, store.ErrConflict):
		return weberror.New(http.StatusConflict, "the role changed while the request ran; send it again")
	case errors.Is(err, store.ErrNotExist):
		return weberror.New(http.StatusNotFound, "role not found")
	case errors.Is(err, types.ErrValidationFailed):
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	return weberror.New(http.StatusInternalServerError, "error storing the role")
}

// roleLookup returns the role of name, its config name or role name, or the
// 404 or 500 to answer.
func roleLookup(name string) (*rbac.Role, error) {
	found, err := rbac.RoleFromConfig(name)
	if errors.Is(err, rbac.ErrRoleNotFound) {
		return nil, weberror.New(http.StatusNotFound, "role not found")
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "retrieving role", "role", name, "err", err)

		return nil, weberror.New(http.StatusInternalServerError, "error retrieving role")
	}

	return found, nil
}

// readRoleBody reads the request's body, or returns the 413 for one larger
// than maxRoleBody or the 500 when it cannot be read.
func readRoleBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRoleBody))

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, weberror.New(http.StatusRequestEntityTooLarge, "the request body is larger than 1 MiB")
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return nil, weberror.New(http.StatusInternalServerError, "unable to read request body")
	}

	return body, nil
}

// visibleUsers returns those of the usernames that the role may list.
func visibleUsers(role rbac.Role, usernames []string) []string {
	visible := []string{}

	for _, name := range usernames {
		if role.Allowed(resourceUsers, "list", name) {
			visible = append(visible, name)
		}
	}

	return visible
}

// roleInSettings returns which setting of phenix ui names the role, or "": a
// default user of ui.users, or the dev|<user>|<role> ui.jwt-signing-key.
func roleInSettings(role *rbac.Role) string {
	names := func(n string) bool { return n == role.ConfigName() || n == role.Spec.Name }

	for i, entry := range o.users {
		if user, err := ParseDefaultUser(i+1, entry); err == nil && names(user.Role) {
			return "a default user in ui.users"
		}
	}

	if _, devRole, ok := middleware.DevAuthCredentials(o.jwtKey); ok && names(devRole) {
		return "the development key in ui.jwt-signing-key"
	}

	return ""
}

// refuseEscalation returns the 403 for a request whose role would grant what
// neither the requester's role nor a held role grants, after it audits the
// refusal, or nil.
func refuseEscalation(
	r *http.Request, a audit.Action, role rbac.Role, policies []*v1.PolicySpec, f audit.Fields, held ...rbac.Role,
) error {
	missing := role.Ungranted(policies, held...)
	if len(missing) == 0 {
		return nil
	}

	f.Reason = reasonEscalation
	audit.Denied(r, a, "role grants permissions the requester does not hold", f)

	return weberror.New(
		http.StatusForbidden, "the role would grant permissions that you do not hold: "+strings.Join(missing, ", "),
	)
}

// roleFields are the audit fields of a change of the role name.
func roleFields(name string) audit.Fields {
	return audit.Fields{Extra: []any{"phenix.role.name", name}}
}

// CreateRole - POST /roles.
func CreateRole(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CreateRole")

	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed(resourceRoles, "create") {
		audit.Denied(r, audit.RoleCreate, "creating roles not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden to create roles")
	}

	body, err := readRoleBody(w, r)
	if err != nil {
		return err
	}

	configName, spec, err := parseCreateRole(body)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	fields := roleFields(spec.Name)
	fields.Config = "Role/" + configName

	if err := refuseEscalation(r, audit.RoleCreate, role, spec.Policies, fields); err != nil {
		return err
	}

	roleChanges.Lock()
	defer roleChanges.Unlock()

	builtin, err := config.BuiltinRoles()
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "error reading the built-in roles")
	}

	roles, err := rbac.GetRoles()
	if err != nil {
		plog.Error(plog.TypeSystem, "retrieving roles", "err", err)

		return weberror.New(http.StatusInternalServerError, "error retrieving roles")
	}

	if conflict := roleConflict(roles, builtin, configName, spec.Name); conflict != "" {
		audit.Failure(r, audit.RoleCreate, "creating role failed", fields, errors.New(conflict))

		return weberror.New(http.StatusConflict, conflict)
	}

	created, err := rbac.CreateRole(configName, spec)
	if err != nil {
		audit.Failure(r, audit.RoleCreate, "creating role failed", fields, err)

		return roleWriteError(err)
	}

	body, err = json.Marshal(roleView(*created, false))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.RoleCreate, "role created", fields)

	w.Header().Set("Location", apiLocation("roles/"+configName))
	w.Header().Set("Content-Type", mimeJSON)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)

	return nil
}

// UpdateRole - PATCH /roles/{name}. It replaces the role's policies, and those
// of each user's copy of the role, keeping the names the user was given.
func UpdateRole(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "UpdateRole")

	var (
		role   = middleware.RoleFromContext(r.Context())
		name   = mux.Vars(r)["name"]
		fields = roleFields(name)
	)

	if !role.Allowed(resourceRoles, verbPatch, name) {
		audit.Denied(r, audit.RoleUpdate, "updating role not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden to update role")
	}

	body, err := readRoleBody(w, r)
	if err != nil {
		return err
	}

	newName, policies, err := parseUpdateRole(body)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if err := refuseEscalation(r, audit.RoleUpdate, role, policies, fields); err != nil {
		return err
	}

	roleChanges.Lock()
	defer roleChanges.Unlock()

	found, err := roleLookup(name)
	if err != nil {
		return err
	}

	fields.Config = "Role/" + found.ConfigName()

	if newName != "" && newName != found.Spec.Name {
		return weberror.New(http.StatusBadRequest, "a role cannot be renamed; name must be "+found.Spec.Name)
	}

	if err := refuseBuiltinRole(found); err != nil {
		return err
	}

	users, err := rbac.UsersWithRole(found.Spec.Name)
	if err != nil {
		plog.Error(plog.TypeSystem, "retrieving users", "err", err)

		return weberror.New(http.StatusInternalServerError, "error retrieving users")
	}

	old := found.Spec

	if err := found.SetPolicies(policies); err != nil {
		audit.Failure(r, audit.RoleUpdate, "updating role failed", fields, err)

		return roleWriteError(err)
	}

	updated, err := replaceUserRoles(users, old, found.Spec)
	if err != nil {
		return roleUsersError(r, role, found, fields, err)
	}

	fields.Extra = append(fields.Extra, "phenix.role.users", updated)
	audit.Success(r, audit.RoleUpdate, "role updated", fields)

	body, err = json.Marshal(RoleUpdate{Role: roleView(*found, false), Users: visibleUsers(role, updated), Count: len(updated)})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// userRolesError is the error of the users whose copy of a role could not
// be replaced.
type userRolesError struct {
	users []string
	err   error
}

func (e *userRolesError) Error() string {
	return "updating users " + strings.Join(e.users, ", ") + ": " + e.err.Error()
}

func (e *userRolesError) Unwrap() error { return e.err }

// replaceUserRoles gives each user whose copy of the role old is still of its
// name a copy of updated, announces the change of each, and closes their
// websockets, which hold the old role. It returns the users it changed, and a
// *userRolesError for those it could not.
func replaceUserRoles(users []*rbac.User, old, updated *v1.RoleSpec) ([]string, error) {
	var (
		changed = []string{}
		failed  []string
		errs    []error
	)

	for _, u := range users {
		replaced, err := u.ReplaceRole(old, updated)
		if err != nil {
			failed = append(failed, u.Username())
			errs = append(errs, err)

			continue
		}

		if !replaced {
			continue
		}

		changed = append(changed, u.Username())

		broker.CloseUserClients(u.Username())

		body, err := json.Marshal(userFromRBAC(*u))
		if err == nil {
			broker.Broadcast(
				bt.NewRequestPolicy(resourceUsers, verbPatch, u.Username()),
				bt.NewResource("user", u.Username(), "update"),
				body,
			)
		}
	}

	if len(failed) > 0 {
		return changed, &userRolesError{users: failed, err: errors.Join(errs...)}
	}

	return changed, nil
}

// roleUsersError audits, and returns the 500 for, a role update stored for the
// role but not for each of its users, which err names. The answer names only
// the users that the requester may list.
func roleUsersError(r *http.Request, requester rbac.Role, role *rbac.Role, fields audit.Fields, err error) error {
	var failed *userRolesError

	errors.As(err, &failed)

	fields.Extra = append(fields.Extra, "phenix.role.users", failed.users)
	audit.Failure(r, audit.RoleUpdate, "updating users of role failed", fields, err)

	return weberror.New(http.StatusInternalServerError, fmt.Sprintf(
		"the role %s is updated, but the copy of the role of %d users is not; send the request again",
		role.ConfigName(), len(failed.users),
	)).WithDetail("users", visibleUsers(requester, failed.users))
}

// CheckRoleDelete checks a delete of the role name as DELETE /roles/{name}
// does, and deletes nothing: the role exists, is not built in, and no user
// has it. It does not check the users and the key that the settings of the
// running server name, as the server does.
func CheckRoleDelete(name string) error {
	found, err := roleLookup(name)
	if err != nil {
		return err
	}

	return roleDeleteRefusal(found, func(*rbac.Role) string { return "" }, func(names []string) []string { return names })
}

// roleDeleteRefusal returns why the role found cannot be deleted: it is built
// in, inSettings names the setting that names it, or users have it. The
// error of the last names the users that visible returns.
func roleDeleteRefusal(found *rbac.Role, inSettings func(*rbac.Role) string, visible func([]string) []string) error {
	if err := refuseBuiltinRole(found); err != nil {
		return err
	}

	users, err := rbac.UsersWithRole(found.Spec.Name)
	if err != nil {
		plog.Error(plog.TypeSystem, "retrieving users", "err", err)

		return weberror.New(http.StatusInternalServerError, "error retrieving users")
	}

	if setting := inSettings(found); setting != "" {
		return weberror.New(http.StatusConflict, "the role "+found.ConfigName()+" is named by "+setting+"; change the setting first")
	}

	if len(users) > 0 {
		names := make([]string, len(users))
		for i, u := range users {
			names[i] = u.Username()
		}

		slices.Sort(names)

		return weberror.New(http.StatusConflict, fmt.Sprintf(
			"the role %s is assigned to %d users; give them another role first", found.ConfigName(), len(names),
		)).WithDetail("users", visible(names))
	}

	return nil
}

// DeleteRole - DELETE /roles/{name}.
func DeleteRole(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "DeleteRole")

	var (
		role   = middleware.RoleFromContext(r.Context())
		name   = mux.Vars(r)["name"]
		fields = roleFields(name)
	)

	if !role.Allowed(resourceRoles, "delete", name) {
		audit.Denied(r, audit.RoleDelete, "deleting role not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden to delete role")
	}

	roleChanges.Lock()
	defer roleChanges.Unlock()

	found, err := roleLookup(name)
	if err != nil {
		return err
	}

	fields.Config = "Role/" + found.ConfigName()

	visible := func(names []string) []string { return visibleUsers(role, names) }
	if err := roleDeleteRefusal(found, roleInSettings, visible); err != nil {
		return err
	}

	if err := found.Delete(); err != nil {
		audit.Failure(r, audit.RoleDelete, "deleting role failed", fields, err)

		return roleWriteError(err)
	}

	audit.Success(r, audit.RoleDelete, "role deleted", fields)
	w.WriteHeader(http.StatusNoContent)

	return nil
}
