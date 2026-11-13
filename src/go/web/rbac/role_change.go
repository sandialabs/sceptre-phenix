package rbac

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/activeshadow/structs"

	"phenix/api/config"
	"phenix/store"
	v1 "phenix/types/version/v1"
)

// ErrPolicyInvalid is wrapped by every error that [ValidatePolicies] returns.
var ErrPolicyInvalid = errors.New("invalid policy")

// knownVerbs is the set of verbs of the known permissions.
var knownVerbs = sync.OnceValue(func() map[string]bool { //nolint:gochecknoglobals // computed once
	verbs := make(map[string]bool, len(Permissions))
	for _, p := range Permissions {
		verbs[p.Verb] = true
	}

	return verbs
})

// ValidatePolicies returns an error that wraps [ErrPolicyInvalid] for the
// first policy with no resources or verbs, a resource pattern that matches no
// known resource, a verb that is not "*" or a known verb, or an empty or
// invalid resource name pattern (a pattern after an optional "!").
func ValidatePolicies(policies []*v1.PolicySpec) error {
	for i, p := range policies {
		if err := validatePolicy(p); err != nil {
			return fmt.Errorf("%w %d: %s", ErrPolicyInvalid, i, err.Error())
		}
	}

	return nil
}

func validatePolicy(p *v1.PolicySpec) error {
	if p == nil {
		return errors.New("the policy is missing")
	}

	if len(p.Resources) == 0 {
		return errors.New("it has no resources")
	}

	for _, resource := range p.Resources {
		if resource == "" || !validPattern(resource) {
			return fmt.Errorf("resource %q is not a valid pattern", resource)
		}

		known := func(r string) bool {
			matched, _ := filepath.Match(resource, r)

			return matched
		}

		if !slices.ContainsFunc(Resources, known) {
			return fmt.Errorf("resource %q matches no known resource", resource)
		}
	}

	if len(p.Verbs) == 0 {
		return errors.New("it has no verbs")
	}

	for _, verb := range p.Verbs {
		if verb != "*" && !knownVerbs()[verb] {
			return fmt.Errorf("verb %q is not a known verb or \"*\"", verb)
		}
	}

	for _, name := range p.ResourceNames {
		pattern := strings.TrimPrefix(name, "!")
		if pattern == "" || !validPattern(pattern) {
			return fmt.Errorf("resource name %q is not a valid pattern", name)
		}
	}

	return nil
}

// Ungranted returns, sorted, each permission that the policies grant and
// neither the role nor a held role grants, as "verb on resource" or "verb on
// resource for name". A held role is one that the target already has, such
// as a user's current role. A policy without resource names counts as one for
// "*", as a user gets it when it is assigned without names. Negated names only
// limit a policy, so they need nothing.
func (r Role) Ungranted(policies []*v1.PolicySpec, held ...Role) []string {
	holders := append([]Role{r}, held...)
	granted := func(resource, verb, name string) bool {
		return slices.ContainsFunc(holders, func(h Role) bool { return h.grants(resource, verb, name) })
	}

	var missing []string

	for _, p := range policies {
		names := []string{"*"}
		if len(p.ResourceNames) > 0 {
			names = slices.DeleteFunc(slices.Clone(p.ResourceNames), func(n string) bool { return strings.HasPrefix(n, "!") })
		}

		for _, resource := range p.Resources {
			for _, verb := range p.Verbs {
				if !granted(resource, verb, "") {
					missing = append(missing, verb+" on "+resource)
				}

				for _, name := range names {
					if !granted(resource, verb, name) {
						missing = append(missing, verb+" on "+resource+" for "+name)
					}
				}
			}
		}
	}

	slices.Sort(missing)

	return slices.Compact(missing)
}

// grants reports whether one policy of the role covers the resource pattern,
// the verb, and the name pattern (any name when name is empty). Unlike
// [Role.Allowed], it reads resource and name as patterns, not as names.
func (r Role) grants(resource, verb, name string) bool {
	if r.Spec == nil {
		return false
	}

	for _, spec := range r.Spec.Policies {
		policy := Policy{Spec: spec}

		if !policy.verbAllowed(verb) || !slices.ContainsFunc(spec.Resources, func(p string) bool { return covers(p, resource) }) {
			continue
		}

		switch {
		case name == "":
			return true
		case !hasMeta(name):
			if policy.resourceNameAllowed(name) {
				return true
			}
		case !slices.ContainsFunc(spec.ResourceNames, func(n string) bool { return strings.HasPrefix(n, "!") }) &&
			slices.ContainsFunc(spec.ResourceNames, func(n string) bool { return covers(n, name) }):
			return true
		}
	}

	return false
}

// hasMeta reports whether s has a character that [filepath.Match] reads as
// syntax.
func hasMeta(s string) bool {
	return strings.ContainsAny(s, `*?[\`)
}

// covers reports, never wrongly true, whether every name that pattern q
// matches also matches pattern p. A q without syntax is a name. Otherwise
// both split at "/", and each segment of p must cover the same segment of q.
func covers(p, q string) bool {
	if !hasMeta(q) {
		matched, _ := filepath.Match(p, q)

		return matched
	}

	if p == q {
		return true
	}

	if !validPattern(q) {
		return false
	}

	segments, ok := patternSegments(q)
	if !ok || strings.Contains(p, `\`) {
		return false
	}

	parts := strings.Split(p, "/")
	if len(parts) != len(segments) {
		return false
	}

	for i, part := range parts {
		if !segments[i].coveredBy(part) {
			return false
		}
	}

	return true
}

// patternSegment is one "/"-separated part of a pattern: its text, and the
// name it is when it has no unescaped syntax.
type patternSegment struct {
	text, name string
	plain      bool
}

// coveredBy reports whether every string that the segment matches also
// matches the segment p: p is the same, p is "*" (no segment matches "/"), or
// the segment is one name that p matches.
func (s patternSegment) coveredBy(p string) bool {
	if p == s.text || p == "*" {
		return true
	}

	if !s.plain {
		return false
	}

	matched, _ := filepath.Match(p, s.name)

	return matched
}

// patternSegments splits the valid pattern q at each "/", escaped or not,
// since both match a "/" in a name. It returns false when a class of q can
// match "/", as "[^a]" can, since a segment then does not end at a "/".
func patternSegments(q string) ([]patternSegment, bool) {
	var (
		segments []patternSegment
		current  = patternSegment{text: "", name: "", plain: true}
	)

	for i := 0; i < len(q); i++ {
		switch c := q[i]; c {
		case '/':
			segments = append(segments, current)
			current = patternSegment{text: "", name: "", plain: true}
		case '\\':
			i++

			if q[i] == '/' {
				segments = append(segments, current)
				current = patternSegment{text: "", name: "", plain: true}

				continue
			}

			current.text += q[i-1 : i+1]
			current.name += q[i : i+1]
		case '[':
			n := classLen(q[i+1:])
			class := q[i : i+1+n]

			if slash, _ := filepath.Match(class, "/"); slash {
				return nil, false
			}

			current.text += class
			current.plain = false
			i += n
		case '*', '?':
			current.text += string(c)
			current.plain = false
		default:
			current.text += string(c)
			current.name += string(c)
		}
	}

	return append(segments, current), true
}

// ConfigName returns the name of the role's config, such as "global-admin".
func (r Role) ConfigName() string {
	if r.config == nil {
		return ""
	}

	return r.config.Metadata.Name
}

// CreateRole stores a new role config of the name with the spec, checked
// against the Role schema. For a name in use the error wraps [store.ErrExist].
func CreateRole(name string, spec *v1.RoleSpec) (*Role, error) {
	c := &store.Config{ //nolint:exhaustruct // partial initialization
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Role",
		Metadata: store.ConfigMetadata{Name: name}, //nolint:exhaustruct // partial initialization
		Spec:     roleConfigSpec(spec),
	}

	if _, err := config.Create(config.CreateFromConfig(c), config.CreateWithValidation()); err != nil {
		return nil, fmt.Errorf("creating role %s: %w", name, err)
	}

	return newRole(spec, c), nil
}

// SetPolicies replaces the role's policies and stores it, checked against the
// Role schema, if the stored role is still at the revision it was read at.
func (r *Role) SetPolicies(policies []*v1.PolicySpec) error {
	spec := &v1.RoleSpec{Name: r.Spec.Name, Policies: policies}

	c := *r.config
	c.Spec = roleConfigSpec(spec)
	c.IfRevision = r.config.Metadata.Revision

	if err := config.Update("role/"+c.Metadata.Name, &c); err != nil {
		return fmt.Errorf("updating role %s: %w", c.Metadata.Name, err)
	}

	r.Spec = spec
	r.config = &c
	r.mappedPolicies = mapPolicies(spec)

	return nil
}

// Delete deletes the role's config, if the stored role is still at the
// revision it was read at.
func (r Role) Delete() error {
	if err := config.DeleteAtRevision("role/"+r.ConfigName(), r.config.Metadata.Revision); err != nil {
		return fmt.Errorf("deleting role %s: %w", r.ConfigName(), err)
	}

	return nil
}

// roleConfigSpec is spec as a stored role config holds it, with an empty list
// rather than null for no policies.
func roleConfigSpec(spec *v1.RoleSpec) map[string]any {
	if spec.Policies == nil {
		spec.Policies = []*v1.PolicySpec{}
	}

	return structs.MapDefaultCase(spec, structs.CASESNAKE)
}

// UsersWithRole returns the users whose copy of their role has the role name.
func UsersWithRole(name string) ([]*User, error) {
	users, err := GetUsers()
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(users, func(u *User) bool { return u.Spec.Role == nil || u.Spec.Role.Name != name }), nil
}

// ReplaceRole gives the user, if its copy of a role is still of old's role
// name, a copy of updated scoped as [UserRoleCopy] does, and reports whether
// it did. It changes the stored user under the user's lock.
func (u *User) ReplaceRole(old, updated *v1.RoleSpec) (bool, error) {
	var replaced bool

	err := u.updateIf(func(spec *v1.UserSpec) bool {
		if spec.Role == nil || spec.Role.Name != old.Name {
			return false
		}

		spec.Role = UserRoleCopy(updated, old, spec.Role, spec.Username)
		replaced = true

		return true
	})
	if err != nil {
		return false, fmt.Errorf("replacing the role of user %s: %w", u.Username(), err)
	}

	return replaced, nil
}

// UserRoleCopy returns the copy of the role updated to store for the user
// username, whose current copy holds the role. When the current copy is a copy
// of old, the policies of updated without names get the names that the copy
// gives the policies of old without names, or stay without names when old has
// none. Otherwise, as after an update stored for the role but not for the
// user, they keep the names of the copy's policy on the same resources, or get
// the copy's narrowest set of names, never wider ones. The user's own users
// policy, which assignment adds, is kept.
func UserRoleCopy(updated, old, current *v1.RoleSpec, username string) *v1.RoleSpec {
	var own []*v1.PolicySpec

	assigned := make([]*v1.PolicySpec, 0, len(current.Policies))

	for _, p := range current.Policies {
		if ownUserPolicy(p, username) {
			own = append(own, clonePolicy(p))
		} else {
			assigned = append(assigned, p)
		}
	}

	var namesFor func(*v1.PolicySpec) []string

	if names, ok := copyNames(old, assigned); ok {
		namesFor = func(*v1.PolicySpec) []string { return names }
	} else {
		narrowest := narrowestNames(assigned)
		namesFor = func(p *v1.PolicySpec) []string {
			if same := samePolicy(p, assigned); same != nil {
				return same.ResourceNames
			}

			return narrowest
		}
	}

	policies := make([]*v1.PolicySpec, 0, len(updated.Policies)+len(own))

	for _, p := range updated.Policies {
		c := clonePolicy(p)
		if len(c.ResourceNames) == 0 {
			c.ResourceNames = slices.Clone(namesFor(p))
		}

		policies = append(policies, c)
	}

	return &v1.RoleSpec{Name: updated.Name, Policies: append(policies, own...)}
}

// copyNames reports whether the assigned policies are a copy of old: the same
// resources and verbs in the same order, the names of old where old names
// them, and one set of names elsewhere. It then returns the names the copy gives the first policy of old
// without names, or nil when old names every policy.
func copyNames(old *v1.RoleSpec, assigned []*v1.PolicySpec) ([]string, bool) {
	if len(assigned) != len(old.Policies) {
		return nil, false
	}

	var (
		names []string
		found bool
	)

	for i, o := range old.Policies {
		a := assigned[i]
		if !slices.Equal(a.Resources, o.Resources) || !slices.Equal(a.Verbs, o.Verbs) {
			return nil, false
		}

		switch {
		case len(o.ResourceNames) > 0 && !slices.Equal(a.ResourceNames, o.ResourceNames):
			return nil, false
		case len(o.ResourceNames) > 0:
		case !found:
			names, found = a.ResourceNames, true
		case !slices.Equal(a.ResourceNames, names):
			// assignment gives each of these policies the same names
			return nil, false
		}
	}

	return names, true
}

// samePolicy returns the first assigned policy with the resources and verbs
// of p, else the first with its resources, or nil.
func samePolicy(p *v1.PolicySpec, assigned []*v1.PolicySpec) *v1.PolicySpec {
	for _, sameVerbs := range []bool{true, false} {
		for _, a := range assigned {
			if slices.Equal(a.Resources, p.Resources) && (!sameVerbs || slices.Equal(a.Verbs, p.Verbs)) {
				return a
			}
		}
	}

	return nil
}

// narrowestNames returns the names of an assigned policy that every other
// policy's names cover, or nil when no set is narrower than all the others.
func narrowestNames(assigned []*v1.PolicySpec) []string {
	var sets [][]string

	for _, a := range assigned {
		if len(a.ResourceNames) > 0 {
			sets = append(sets, a.ResourceNames)
		}
	}

	for _, set := range sets {
		if !slices.ContainsFunc(sets, func(other []string) bool { return !namesCover(other, set) }) {
			return set
		}
	}

	return nil
}

// namesCover reports, never wrongly true, whether every name that the names
// set allows, the names wide also allow.
func namesCover(wide, set []string) bool {
	if slices.Equal(wide, set) {
		return true
	}

	negated := func(n string) bool { return strings.HasPrefix(n, "!") }
	if slices.ContainsFunc(wide, negated) {
		return false
	}

	return !slices.ContainsFunc(set, func(n string) bool {
		return !negated(n) && !slices.ContainsFunc(wide, func(w string) bool { return covers(w, n) })
	})
}

// ownUserPolicy reports whether p is the policy that assignment adds to let a
// user get, and maybe patch, only its own user.
func ownUserPolicy(p *v1.PolicySpec, username string) bool {
	return slices.Equal(p.Resources, []string{"users"}) && slices.Equal(p.ResourceNames, []string{username}) &&
		len(p.Verbs) > 0 && !slices.ContainsFunc(p.Verbs, func(v string) bool { return v != "get" && v != "patch" })
}

func clonePolicy(p *v1.PolicySpec) *v1.PolicySpec {
	return &v1.PolicySpec{
		Resources:     slices.Clone(p.Resources),
		ResourceNames: slices.Clone(p.ResourceNames),
		Verbs:         slices.Clone(p.Verbs),
	}
}
