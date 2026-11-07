package rbac

// ResourceVerbs is one resource of the known role checks and the verbs they
// check on it, sorted. Verbs is empty for a resource whose checks all take
// their verb from the caller.
type ResourceVerbs struct {
	Resource string   `json:"resource"`
	Verbs    []string `json:"verbs"`
}

// KnownPermissions returns each resource of the known role checks, in the
// order of Resources, with the verbs Permissions holds for it.
func KnownPermissions() []ResourceVerbs {
	verbs := make(map[string][]string, len(Resources))

	for _, p := range Permissions {
		verbs[p.Resource] = append(verbs[p.Resource], p.Verb)
	}

	known := make([]ResourceVerbs, 0, len(Resources))

	for _, resource := range Resources {
		v := verbs[resource]
		if v == nil {
			v = []string{}
		}

		known = append(known, ResourceVerbs{Resource: resource, Verbs: v})
	}

	return known
}
