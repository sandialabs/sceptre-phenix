package rbac

import (
	"path/filepath"
	"strings"

	v1 "phenix/types/version/v1"
)

type Policy struct {
	Spec *v1.PolicySpec
}

func (p Policy) resourceNameAllowed(name string) bool {
	var allowed bool

	for _, n := range p.Spec.ResourceNames {
		negate := strings.HasPrefix(n, "!")
		n = strings.Replace(n, "!", "", 1)

		if matched, _ := filepath.Match(n, name); matched {
			if negate {
				return false
			}

			allowed = true
		}
	}

	return allowed
}

func (p Policy) verbAllowed(verb string) bool {
	for _, v := range p.Spec.Verbs {
		if v == "*" || v == verb {
			return true
		}
	}

	return false
}
