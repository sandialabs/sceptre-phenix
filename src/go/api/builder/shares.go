package builder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ShareAccess is what a share lets its recipient do with a draft.
type ShareAccess string

const (
	// ShareView lets the recipient open the draft and read its history.
	ShareView ShareAccess = "view"
	// ShareEdit also lets the recipient save, undo, redo and publish it.
	ShareEdit ShareAccess = "edit"
)

// maxShareAttempts bounds how often [Service.UpdateShares] builds and writes
// the share list again after a save wrote first.
const maxShareAttempts = 5

// Valid reports whether the access is one this package understands.
func (a ShareAccess) Valid() bool {
	switch a {
	case ShareView, ShareEdit:
		return true
	}

	return false
}

// ShareEntry shares a draft with one user.
type ShareEntry struct {
	User   string      `json:"user"`
	Access ShareAccess `json:"access"`
	// UserCreated is the metadata.created of the User config of the recipient
	// at grant time. The share applies only while it still matches, so it
	// never passes to a new account created under the same name.
	UserCreated string    `json:"userCreated"`
	GrantedAt   time.Time `json:"grantedAt"`
	GrantedBy   string    `json:"grantedBy"`
}

// SharingState is who a draft is shared with.
type SharingState struct {
	// Version increases with every change, and nothing resets it. Thus a list
	// that was emptied still differs from a list that was never set.
	Version int64 `json:"version"`
	// Entries are sorted by user.
	Entries   []ShareEntry `json:"entries"`
	UpdatedAt time.Time    `json:"updatedAt"`
	UpdatedBy string       `json:"updatedBy"`
}

// ShareGrant is one entry of the share list [Service.UpdateShares] stores.
type ShareGrant struct {
	User   string
	Access ShareAccess
	// UserCreated is metadata.created of the recipient's User config. The
	// caller resolves it, and so decides that the account exists.
	UserCreated string
}

// UpdateSharesRequest replaces who a draft is shared with.
type UpdateSharesRequest struct {
	DraftID string
	// Actor must be the draft owner: only the owner changes who has access.
	Actor string
	// ExpectedVersion is the [DraftMetadata.SharingVersion] the caller
	// authorized the change against: 0 for a draft never shared.
	ExpectedVersion int64
	// Grants is the complete new list.
	Grants []ShareGrant
}

// SharingVersion returns the version of the draft's share list, 0 when it
// was never shared.
func (d *DraftMetadata) SharingVersion() int64 {
	if d.Sharing == nil {
		return 0
	}

	return d.Sharing.Version
}

// ShareFor returns the entry that shares the draft with user, or nil. It does
// not check the entry against the account of the user. The caller does that.
func (d *DraftMetadata) ShareFor(user string) *ShareEntry {
	if d.Sharing == nil {
		return nil
	}

	for i := range d.Sharing.Entries {
		if d.Sharing.Entries[i].User == user {
			return &d.Sharing.Entries[i]
		}
	}

	return nil
}

// SharesETag returns the entity tag of the share list of the draft,
// `"shares-<version>"`. It is separate from [DraftMetadata.ETag]. Thus a
// content save never causes the refusal of a change of who has access, and the
// reverse.
func (d *DraftMetadata) SharesETag() string {
	return `"shares-` + strconv.FormatInt(d.SharingVersion(), 10) + `"`
}

// ValidateShareUser reports whether user can name a share recipient: a
// bounded, printable user name that is also a usable config name. It also
// refuses invisible characters, such as bidirectional overrides and C1
// controls. Thus a recipient never reorders or hides text where it is shown or
// logged.
func ValidateShareUser(user string) error {
	if err := validateText("user", user, MaxOwnerLength, true); err != nil {
		return err
	}

	if strings.Contains(user, "/") {
		return newValidationError("user", "must not contain '/'")
	}

	for _, r := range user {
		if !unicode.IsPrint(r) {
			return newValidationError("user", "must contain only printable characters")
		}
	}

	return nil
}

// UpdateShares replaces who a draft is shared with, and returns the draft.
//
// The share list is in the draft record, so UpdateShares writes it with the
// same compare-and-swap as the content. Thus every save that a share removed
// here authorized fails after UpdateShares returns. A save that writes first
// makes the write fail. The new list depends only on the old list and the
// request, never on the content. Thus UpdateShares builds and writes it again,
// up to [maxShareAttempts] times, and then returns [ErrBusy]. A share list
// that changed after ExpectedVersion is a conflict.
//
// An entry that names the same user, access and account as before keeps when
// and by whom it was granted. When nothing changes, nothing is written.
// Content timestamps (Updated, LastModifiedBy) never change here.
func (s *Service) UpdateShares(ctx context.Context, req UpdateSharesRequest) (*DraftMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("updating draft shares: %w", err)
	}

	if err := validateSharesRequest(req); err != nil {
		return nil, err
	}

	for range maxShareAttempts {
		meta, err := s.GetDraft(ctx, req.DraftID)
		if err != nil {
			return nil, err
		}

		if err := checkSharesRequest(meta, req); err != nil {
			return nil, err
		}

		entries := s.shareEntries(meta, req)

		var current []ShareEntry
		if meta.Sharing != nil {
			current = meta.Sharing.Entries
		}

		if sameShareEntries(entries, current) {
			return meta, nil
		}

		updated := meta.Clone()
		updated.Sharing = &SharingState{
			Version:   meta.SharingVersion() + 1,
			Entries:   entries,
			UpdatedAt: s.clock().UTC(),
			UpdatedBy: req.Actor,
		}

		value, err := encodeDraft(updated)
		if err != nil {
			return nil, err
		}

		err = s.saveDraft(updated, value, meta.Revision)

		switch {
		case err == nil:
			return updated, nil
		case errors.Is(err, ErrConflict):
			continue
		case !writeRejected(err):
			return s.settleSharesWrite(ctx, req.DraftID, updated.Sharing, err)
		}

		return nil, err
	}

	return nil, fmt.Errorf("updating shares of draft %s: %w", req.DraftID, ErrBusy)
}

// shareEntries builds the sorted share list a request asks for.
func (s *Service) shareEntries(meta *DraftMetadata, req UpdateSharesRequest) []ShareEntry {
	now := s.clock().UTC()
	entries := make([]ShareEntry, 0, len(req.Grants))

	for _, grant := range req.Grants {
		current := meta.ShareFor(grant.User)
		if current != nil && current.Access == grant.Access && current.UserCreated == grant.UserCreated {
			entries = append(entries, *current)

			continue
		}

		entries = append(entries, ShareEntry{
			User:        grant.User,
			Access:      grant.Access,
			UserCreated: grant.UserCreated,
			GrantedAt:   now,
			GrantedBy:   req.Actor,
		})
	}

	slices.SortFunc(entries, func(a, b ShareEntry) int { return strings.Compare(a.User, b.User) })

	return entries
}

// settleSharesWrite settles a share list write that failed with err, an error
// that does not prove the write was not applied. It reads the draft back. When
// the draft holds exactly the list the write stored, the write was applied.
// When it holds another list at the same version, another change of the list
// won, which is a conflict. Otherwise it returns err.
func (s *Service) settleSharesWrite(
	ctx context.Context,
	draftID string,
	want *SharingState,
	err error,
) (*DraftMetadata, error) {
	stored, getErr := s.GetDraft(ctx, draftID)
	if getErr != nil || stored.SharingVersion() != want.Version {
		return nil, err
	}

	if !sameShareEntries(stored.Sharing.Entries, want.Entries) {
		return nil, &ConflictError{
			Kind:     kindDraft,
			ID:       stored.ID,
			Expected: 0,
			Actual:   stored.Revision,
			Reason:   "sharing changed",
		}
	}

	return stored, nil
}

// checkSharesRequest checks a share list request against the draft it
// changes.
func checkSharesRequest(meta *DraftMetadata, req UpdateSharesRequest) error {
	if meta.Owner != req.Actor {
		return newValidationError("actor", "only the draft owner can change who has access")
	}

	if version := meta.SharingVersion(); version != req.ExpectedVersion {
		return &ConflictError{
			Kind:     kindDraft,
			ID:       meta.ID,
			Expected: 0,
			Actual:   meta.Revision,
			Reason:   fmt.Sprintf("sharing changed from version %d to %d", req.ExpectedVersion, version),
		}
	}

	for _, grant := range req.Grants {
		if grant.User == meta.Owner {
			return newValidationError("grants", "the owner cannot be shared with")
		}
	}

	return nil
}

func validateSharesRequest(req UpdateSharesRequest) error {
	if err := validateID("draftID", req.DraftID); err != nil {
		return err
	}

	if err := validateText("actor", req.Actor, MaxOwnerLength, true); err != nil {
		return err
	}

	if req.ExpectedVersion < 0 {
		return newValidationError("expectedVersion", "must not be negative")
	}

	if len(req.Grants) > MaxShares {
		return newValidationError("grants", fmt.Sprintf("must name at most %d users", MaxShares))
	}

	seen := make(map[string]bool, len(req.Grants))

	for _, grant := range req.Grants {
		if err := ValidateShareUser(grant.User); err != nil {
			return err
		}

		if seen[grant.User] {
			return newValidationError("grants", fmt.Sprintf("user %q appears more than once", grant.User))
		}

		seen[grant.User] = true

		if !grant.Access.Valid() {
			return newValidationError("access", fmt.Sprintf("unknown access %q", grant.Access))
		}

		if err := validateText("userCreated", grant.UserCreated, maxUserCreatedLength, true); err != nil {
			return err
		}
	}

	return nil
}

// sameShareEntries reports whether two share lists are identical.
func sameShareEntries(a, b []ShareEntry) bool {
	return slices.EqualFunc(a, b, func(x, y ShareEntry) bool {
		return x.User == y.User &&
			x.Access == y.Access &&
			x.UserCreated == y.UserCreated &&
			x.GrantedAt.Equal(y.GrantedAt) &&
			x.GrantedBy == y.GrantedBy
	})
}
