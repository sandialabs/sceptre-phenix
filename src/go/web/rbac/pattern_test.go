package rbac

import "testing"

// patternCases maps resource name patterns to whether they are valid.
func patternCases() map[string]bool {
	return map[string]bool{
		"*": true, "a*": true, "[a-z]*": true, `x\*`: true, "[^ab]c": true, `[\]]`: true, "vms/*": true,
		"x*[": false, "v*[": false, "[": false, "a[": false, "[]": false, "[a-": false, `x\`: false,
		"[-a]": false, "[a-]": false,
	}
}

func TestValidPattern(t *testing.T) {
	t.Parallel()

	for pattern, want := range patternCases() {
		if got := validPattern(pattern); got != want {
			t.Errorf("validPattern(%q) = %t, want %t", pattern, got, want)
		}
	}
}
