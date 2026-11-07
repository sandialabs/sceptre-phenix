package vlan

import (
	"errors"
	"testing"

	"pgregory.net/rapid"
)

func TestCheckProposal(t *testing.T) {
	tests := []struct {
		name    string
		p       Proposal
		wantErr bool
	}{
		{name: "pins and a range", p: Proposal{Experiment: "e", Aliases: map[string]int{"A": 100, "B": 0}, Min: 100, Max: 199}},
		{name: "no pins and no range", p: Proposal{Experiment: "e"}},
		{name: "an open end", p: Proposal{Experiment: "e", Min: 100}},
		{name: "no experiment", p: Proposal{Experiment: " "}, wantErr: true},
		{name: "a pin past the last ID", p: Proposal{Experiment: "e", Aliases: map[string]int{"A": MaxID + 1}}, wantErr: true},
		{name: "a negative pin", p: Proposal{Experiment: "e", Aliases: map[string]int{"A": -1}}, wantErr: true},
		{name: "a range highest first", p: Proposal{Experiment: "e", Min: 200, Max: 100}, wantErr: true},
		{name: "a range past the last ID", p: Proposal{Experiment: "e", Min: 1, Max: MaxID + 1}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckProposal(tc.p)

			var refused *ProposalError

			switch {
			case tc.wantErr && (!errors.As(err, &refused) || !errors.Is(err, ErrInvalid)):
				t.Errorf("CheckProposal(%+v) = %v, want a *ProposalError that wraps %v", tc.p, err, ErrInvalid)
			case !tc.wantErr && err != nil:
				t.Errorf("CheckProposal(%+v) = %v, want nil", tc.p, err)
			}
		})
	}
}

// CheckProposal takes a proposal exactly when each pin is 0 or a VLAN ID and
// each set end of the range is a VLAN ID, lowest first.
func TestCheckProposalProperty(t *testing.T) {
	id := rapid.IntRange(-3, MaxID+3)

	rapid.Check(t, func(t *rapid.T) {
		p := Proposal{
			Experiment: "e",
			Aliases:    rapid.MapOf(rapid.StringMatching(`[A-Z]{1,3}`), id).Draw(t, "aliases"),
			Min:        rapid.OneOf(rapid.Just(0), id).Draw(t, "min"),
			Max:        rapid.OneOf(rapid.Just(0), id).Draw(t, "max"),
		}

		valid := (p.Min == 0 || ValidID(p.Min)) && (p.Max == 0 || ValidID(p.Max)) &&
			(p.Min == 0 || p.Max == 0 || p.Min <= p.Max)

		for _, pin := range p.Aliases {
			valid = valid && (pin == 0 || ValidID(pin))
		}

		if err := CheckProposal(p); (err == nil) != valid {
			t.Fatalf("CheckProposal(%+v) = %v, want valid %t", p, err, valid)
		}
	})
}
