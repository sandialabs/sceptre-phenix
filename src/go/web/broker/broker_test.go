package broker

import (
	"testing"

	v1 "phenix/types/version/v1"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/rbac"
)

func TestRequestPoliciesAllowedRequiresEveryPolicy(t *testing.T) {
	t.Parallel()

	role := rbac.Role{Spec: &v1.RoleSpec{
		Policies: []*v1.PolicySpec{
			{Resources: []string{"scorch"}, Verbs: []string{"get"}},
			{
				Resources:     []string{"experiments"},
				ResourceNames: []string{"allowed"},
				Verbs:         []string{"get"},
			},
		},
	}}

	tests := []struct {
		name     string
		policies []*bt.RequestPolicy
		want     bool
	}{
		{
			name: "service and experiment allowed",
			policies: []*bt.RequestPolicy{
				bt.NewRequestPolicy("scorch", "get", ""),
				bt.NewRequestPolicy("experiments", "get", "allowed"),
			},
			want: true,
		},
		{
			name: "experiment denied",
			policies: []*bt.RequestPolicy{
				bt.NewRequestPolicy("scorch", "get", ""),
				bt.NewRequestPolicy("experiments", "get", "denied"),
			},
			want: false,
		},
		{
			name: "service denied",
			policies: []*bt.RequestPolicy{
				bt.NewRequestPolicy("builder", "get", ""),
				bt.NewRequestPolicy("experiments", "get", "allowed"),
			},
			want: false,
		},
		{name: "no policies", policies: nil, want: true},
		{name: "nil policy", policies: []*bt.RequestPolicy{nil}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := requestPoliciesAllowed(role, test.policies); got != test.want {
				t.Fatalf("unexpected result: got %t, want %t", got, test.want)
			}
		})
	}
}
