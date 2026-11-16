package vlan

import (
	"fmt"
	"slices"
	"strings"
)

// ProposalError is why CheckProposal refuses a proposal. It wraps ErrInvalid.
type ProposalError struct {
	Reason string
}

// Error returns the reason.
func (e *ProposalError) Error() string { return e.Reason }

// Unwrap returns ErrInvalid.
func (e *ProposalError) Unwrap() error { return ErrInvalid }

// proposalError returns a *ProposalError with the reason that format and args give.
func proposalError(format string, args ...any) error {
	return &ProposalError{Reason: fmt.Sprintf(format, args...)}
}

// CheckProposal returns a *ProposalError unless p names an experiment, pins each alias to a VLAN
// ID or to 0 (no pin), and gives a range within MinID–MaxID, lowest first, where 0 leaves an end open. With more than
// one bad alias, it names the first in sort order.
func CheckProposal(p Proposal) error {
	if strings.TrimSpace(p.Experiment) == "" {
		return proposalError("the VLAN check request names no experiment")
	}

	aliases := make([]string, 0, len(p.Aliases))
	for alias := range p.Aliases {
		aliases = append(aliases, alias)
	}

	slices.Sort(aliases)

	for _, alias := range aliases {
		if id := p.Aliases[alias]; id != 0 && !ValidID(id) {
			return proposalError("VLAN alias %s pins %d, which is not in %d–%d", alias, id, MinID, MaxID)
		}
	}

	return CheckRange(p.Min, p.Max)
}

// CheckRange returns a *ProposalError unless the VLAN range minID–maxID is within MinID–MaxID, lowest first,
// where 0 leaves an end open.
func CheckRange(minID, maxID int) error {
	if (minID != 0 && !ValidID(minID)) || (maxID != 0 && !ValidID(maxID)) || (minID != 0 && maxID != 0 && minID > maxID) {
		return proposalError("the range %d–%d is not within %d–%d, lowest first", minID, maxID, MinID, MaxID)
	}

	return nil
}
