package jwt

import (
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
)

func TestUsernameFromClaims(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		claims jwt.MapClaims
		want   string // empty for an error
	}{
		"sub":                     {jwt.MapClaims{"sub": "alice"}, "alice"},
		"username":                {jwt.MapClaims{"username": "bob"}, "bob"},
		"user":                    {jwt.MapClaims{"user": "carol"}, "carol"},
		"sub before username":     {jwt.MapClaims{"sub": "alice", "username": "bob", "user": "carol"}, "alice"},
		"username before user":    {jwt.MapClaims{"username": "bob", "user": "carol"}, "bob"},
		"empty sub is skipped":    {jwt.MapClaims{"sub": "", "username": "bob"}, "bob"},
		"non-string sub skipped":  {jwt.MapClaims{"sub": 42.0, "user": "carol"}, "carol"},
		"no user claim":           {jwt.MapClaims{"exp": 1.0}, ""},
		"only empty user claims":  {jwt.MapClaims{"sub": "", "username": "", "user": ""}, ""},
		"only non-string claims":  {jwt.MapClaims{"sub": true, "username": []any{"x"}}, ""},
		"no claims":               {jwt.MapClaims{}, ""},
		"unrelated name claim":    {jwt.MapClaims{"name": "dave"}, ""},
		"user claim is not a sub": {jwt.MapClaims{"Sub": "eve"}, ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := UsernameFromClaims(tc.claims)

			switch {
			case tc.want == "" && err == nil:
				t.Errorf("UsernameFromClaims(%v) = %q, want an error", tc.claims, got)
			case tc.want != "" && err != nil:
				t.Errorf("UsernameFromClaims(%v) error = %v, want %q", tc.claims, err, tc.want)
			case got != tc.want:
				t.Errorf("UsernameFromClaims(%v) = %q, want %q", tc.claims, got, tc.want)
			}
		})
	}
}

func TestValidateExpirationClaim(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := map[string]struct {
		claims jwt.MapClaims
		valid  bool
	}{
		"future":            {jwt.MapClaims{"exp": float64(now.Add(time.Hour).Unix())}, true},
		"past":              {jwt.MapClaims{"exp": float64(now.Add(-time.Hour).Unix())}, false},
		"epoch":             {jwt.MapClaims{"exp": 0.0}, false},
		"missing":           {jwt.MapClaims{"sub": "alice"}, false},
		"string":            {jwt.MapClaims{"exp": "4102444800"}, false},
		"integer, not JSON": {jwt.MapClaims{"exp": int64(4102444800)}, false},
		"null":              {jwt.MapClaims{"exp": nil}, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := ValidateExpirationClaim(tc.claims)
			if tc.valid && err != nil {
				t.Errorf("ValidateExpirationClaim(%v) = %v, want nil", tc.claims, err)
			}

			if !tc.valid && err == nil {
				t.Errorf("ValidateExpirationClaim(%v) = nil, want an error", tc.claims)
			}
		})
	}
}
