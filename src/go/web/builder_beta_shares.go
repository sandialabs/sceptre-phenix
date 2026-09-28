package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	bapi "phenix/api/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// builderBetaSharesMaxBytes bounds the body of a request replacing who a
// draft is shared with (64 KiB).
const builderBetaSharesMaxBytes = 64 << 10

// Reasons a request replacing who a draft is shared with refuses one entry.
const (
	// builderShareOwner: the owner is in the list.
	builderShareOwner = "owner"
	// builderShareDuplicate: the user appears more than once.
	builderShareDuplicate = "duplicate"
	// builderShareInvalidAccess: the access is not "view" or "edit".
	builderShareInvalidAccess = "invalid-access"
	// builderShareInvalidUser: the name cannot name a user.
	builderShareInvalidUser = "invalid-user"
	// builderShareTooMany: the list is longer than [bapi.MaxShares].
	builderShareTooMany = "too-many"
	// builderShareUnknownUser: no such account.
	builderShareUnknownUser = "unknown-user"
	// builderShareAccountRemoved: the account the user was shared with was
	// removed, or replaced by a new one under the same name. The share is
	// never moved to the new account: the owner removes it, saves, and adds
	// the user again.
	builderShareAccountRemoved = "account-removed"
)

// builderSharesRequest replaces who a draft is shared with.
type builderSharesRequest struct {
	Shares *[]builderShareRequestEntry `json:"shares"`
}

// builderShareRequestEntry shares a draft with one user.
type builderShareRequestEntry struct {
	User   string `json:"user"`
	Access string `json:"access"`
}

// builderShareResponse is one user a draft is shared with. Stale is set when
// the account it was shared with no longer exists, so the share grants
// nothing. Who granted it and the account it is bound to are never exposed.
type builderShareResponse struct {
	User      string    `json:"user"`
	Access    string    `json:"access"`
	GrantedAt time.Time `json:"grantedAt"`
	Stale     bool      `json:"stale"`
}

// builderSharesResponse is who a draft is shared with, and the entity tag of
// that list. A successful change also carries the draft, whose own entity tag
// changed with it: there is deliberately no top-level etag.
type builderSharesResponse struct {
	Shares     []builderShareResponse `json:"shares"`
	SharesETag string                 `json:"sharesEtag"`
	MaxShares  int                    `json:"maxShares"`
	Draft      *builderDraftResponse  `json:"draft,omitempty"`
}

// builderShareError is why one entry of a share list was refused.
type builderShareError struct {
	User   string `json:"user"`
	Reason string `json:"reason"`
}

// builderShareErrorsResponse is the 422 a refused share list is answered
// with: a [weberror.WebError] with the refused entries added.
type builderShareErrorsResponse struct {
	Message string              `json:"message"`
	Cause   string              `json:"cause"`
	Errors  []builderShareError `json:"errors"`
}

// getShares - GET /builder/drafts/{owner}/{draft}/shares.
//
// Only the owner learns who else has access to a draft.
func (b *builderBetaAPI) getShares(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaGetShares")

	const action = "getting who a builder draft is shared with"

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, action)
	}

	meta, access, err := b.draftAccessFor(r, actor, builderBetaVerbGet, action)
	if err != nil {
		return err
	}

	if !access.owner() {
		builderBetaWarnDenied(actor, meta.Owner, action, "not-owner")

		return builderBetaNotAllowed(actor, action)
	}

	shares, err := b.shareResponses(meta, true)
	if err != nil {
		return weberror.NewWebError(err, "unable to get who builder draft %s is shared with", meta.ID).
			SetStatus(http.StatusInternalServerError)
	}

	return builderBetaWriteJSON(w, http.StatusOK, meta.SharesETag(), builderSharesResponse{
		Shares:     shares,
		SharesETag: meta.SharesETag(),
		MaxShares:  bapi.MaxShares,
		Draft:      nil,
	})
}

// putShares - PUT /builder/drafts/{owner}/{draft}/shares.
//
// The request replaces the whole list, and needs an If-Match naming the
// list's own entity tag, `"shares-N"`. Only the owner may make it, and only
// with a user account of its own. Every user in the list must have an
// account; a share is bound to it.
func (b *builderBetaAPI) putShares(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaPutShares")

	const action = "changing who a builder draft is shared with"

	actor, ok := builderBetaRequestActor(r)
	if !ok || !builderBetaBaseAllowed(actor.role, builderBetaVerbShare) {
		return builderBetaForbidden(actor, action)
	}

	ifMatch, err := builderBetaIfMatch(r)
	if err != nil {
		return err
	}

	meta, access, err := b.draftAccessFor(r, actor, builderBetaVerbShare, action)
	if err != nil {
		return err
	}

	canShare, err := b.canShare(actor, b.accountOnce(actor.user))
	if err != nil {
		return weberror.NewWebError(err, "unable to change who builder draft %s is shared with", meta.ID).
			SetStatus(http.StatusInternalServerError)
	}

	// The UI never offers sharing then: see [builderDraftResponse.CanShare].
	if !canShare {
		return builderBetaForbidden(actor, action+" without a user account")
	}

	if ifMatch != meta.SharesETag() {
		w.Header().Set("ETag", meta.SharesETag())

		return weberror.NewWebError(nil, "who draft %s is shared with has changed since it was last read", meta.ID).
			SetStatus(http.StatusPreconditionFailed)
	}

	var request builderSharesRequest

	if err := builderBetaDecodeLimit(w, r, &request, builderBetaSharesMaxBytes); err != nil {
		return err
	}

	if request.Shares == nil {
		return weberror.NewWebError(nil, "a list of shares is required").SetStatus(http.StatusBadRequest)
	}

	if problems := builderShareShapeErrors(meta.Owner, *request.Shares); len(problems) != 0 {
		return builderWriteShareErrors(w, problems)
	}

	grants, problems, err := b.resolveShareGrants(actor, meta, *request.Shares)
	if err != nil {
		return weberror.NewWebError(err, "unable to change who builder draft %s is shared with", meta.ID).
			SetStatus(http.StatusInternalServerError)
	}

	if len(problems) != 0 {
		return builderWriteShareErrors(w, problems)
	}

	updated, err := b.drafts.UpdateShares(r.Context(), bapi.UpdateSharesRequest{
		DraftID:         meta.ID,
		Actor:           actor.user,
		ExpectedVersion: meta.SharingVersion(),
		Grants:          grants,
	})
	if err != nil {
		return b.sharesUpdateError(w, r, meta, err)
	}

	if updated.SharingVersion() != meta.SharingVersion() {
		builderLogSharesChanged(actor, meta, updated)
	}

	// Every entry was just resolved to the account it names, so none is stale.
	shares, err := b.shareResponses(updated, false)
	if err != nil {
		return weberror.NewWebError(err, "unable to get who builder draft %s is shared with", meta.ID).
			SetStatus(http.StatusInternalServerError)
	}

	draft := newBuilderDraftResponse(updated)
	builderDraftAccessFields(&draft, actor, access, updated, true)

	return builderBetaWriteJSON(w, http.StatusOK, updated.SharesETag(), builderSharesResponse{
		Shares:     shares,
		SharesETag: updated.SharesETag(),
		MaxShares:  bapi.MaxShares,
		Draft:      &draft,
	})
}

// shareResponses returns who a draft is shared with. When check is set, each
// entry is checked against the account it was shared with, one read each.
func (b *builderBetaAPI) shareResponses(meta *bapi.DraftMetadata, check bool) ([]builderShareResponse, error) {
	shares := []builderShareResponse{}

	if meta.Sharing == nil {
		return shares, nil
	}

	for _, entry := range meta.Sharing.Entries {
		stale := false

		if check {
			created, exists, err := b.accountCreated(entry.User)
			if err != nil {
				return nil, err
			}

			stale = !exists || created != entry.UserCreated
		}

		shares = append(shares, builderShareResponse{
			User:      entry.User,
			Access:    string(entry.Access),
			GrantedAt: entry.GrantedAt,
			Stale:     stale,
		})
	}

	return shares, nil
}

// builderShareShapeErrors returns what is wrong with the entries of a share
// list on their own, before any account is read.
func builderShareShapeErrors(owner string, entries []builderShareRequestEntry) []builderShareError {
	var problems []builderShareError

	if len(entries) > bapi.MaxShares {
		problems = append(problems, builderShareError{User: "", Reason: builderShareTooMany})
	}

	var (
		seen       = make(map[string]bool, len(entries))
		duplicated = make(map[string]bool)
	)

	for _, entry := range entries {
		reason := ""

		switch {
		case bapi.ValidateShareUser(entry.User) != nil:
			reason = builderShareInvalidUser
		case entry.User == owner:
			reason = builderShareOwner
		case duplicated[entry.User]:
			continue
		case seen[entry.User]:
			reason = builderShareDuplicate
			duplicated[entry.User] = true
		case !bapi.ShareAccess(entry.Access).Valid():
			reason = builderShareInvalidAccess
		}

		seen[entry.User] = true

		if reason != "" {
			problems = append(problems, builderShareError{User: entry.User, Reason: reason})
		}
	}

	return problems
}

// resolveShareGrants binds each entry of a well-formed share list to the
// account it names. A user already shared with keeps the account it was
// shared with, and is refused when that account is gone; a new user must have
// an account. Refusals are logged: they may be probing for user names.
func (b *builderBetaAPI) resolveShareGrants(
	actor builderBetaActor,
	meta *bapi.DraftMetadata,
	entries []builderShareRequestEntry,
) ([]bapi.ShareGrant, []builderShareError, error) {
	var (
		grants   = make([]bapi.ShareGrant, 0, len(entries))
		problems []builderShareError
	)

	for _, entry := range entries {
		created, exists, err := b.accountCreated(entry.User)
		if err != nil {
			return nil, nil, err
		}

		stored := meta.ShareFor(entry.User)

		reason := ""

		switch {
		case stored != nil && (!exists || created != stored.UserCreated):
			reason = builderShareAccountRemoved
		case !exists:
			reason = builderShareUnknownUser
		}

		if reason == "" {
			grants = append(grants, bapi.ShareGrant{
				User:        entry.User,
				Access:      bapi.ShareAccess(entry.Access),
				UserCreated: created,
			})

			continue
		}

		plog.Warn(
			plog.TypeSecurity,
			"builder draft share names an unknown user",
			"user", actor.user,
			"recipient", entry.User,
		)

		problems = append(problems, builderShareError{User: entry.User, Reason: reason})
	}

	return grants, problems, nil
}

// sharesUpdateError maps a failed change of who a draft is shared with. A
// change that lost to another one is a failed precondition, answered with the
// list's current entity tag; a draft that kept changing under it is busy.
func (b *builderBetaAPI) sharesUpdateError(
	w http.ResponseWriter,
	r *http.Request,
	meta *bapi.DraftMetadata,
	err error,
) error {
	switch {
	case errors.Is(err, bapi.ErrConflict):
		if current, getErr := b.drafts.GetDraft(r.Context(), meta.ID); getErr == nil {
			w.Header().Set("ETag", current.SharesETag())
		}

		return weberror.NewWebError(nil, "who draft %s is shared with has changed since it was last read", meta.ID).
			SetStatus(http.StatusPreconditionFailed)
	case errors.Is(err, bapi.ErrBusy):
		w.Header().Set("Retry-After", "1")
	}

	return builderBetaWebError(err, "unable to change who builder draft %s is shared with", meta.ID)
}

// builderWriteShareErrors answers a refused share list with 422 and every
// refused entry.
func builderWriteShareErrors(w http.ResponseWriter, problems []builderShareError) error {
	return builderBetaWriteJSON(w, http.StatusUnprocessableEntity, "", builderShareErrorsResponse{
		Message: "Some people could not be added.",
		Cause:   "",
		Errors:  problems,
	})
}

// builderLogSharesChanged records who a draft is now shared with, as the
// users added ("bob:edit"), removed ("dave") and changed ("erin:view->edit").
func builderLogSharesChanged(actor builderBetaActor, before, after *bapi.DraftMetadata) {
	var added, removed, changed []string

	if after.Sharing != nil {
		for _, entry := range after.Sharing.Entries {
			previous := before.ShareFor(entry.User)

			switch {
			case previous == nil:
				added = append(added, entry.User+":"+string(entry.Access))
			case previous.Access != entry.Access:
				changed = append(changed, entry.User+":"+string(previous.Access)+"->"+string(entry.Access))
			}
		}
	}

	if before.Sharing != nil {
		for _, entry := range before.Sharing.Entries {
			if after.ShareFor(entry.User) == nil {
				removed = append(removed, entry.User)
			}
		}
	}

	plog.Info(
		plog.TypeSecurity,
		"builder draft sharing changed",
		"user", actor.user,
		"owner", after.Owner,
		"draft", after.ID,
		"version", after.SharingVersion(),
		"added", strings.Join(added, ","),
		"removed", strings.Join(removed, ","),
		"changed", strings.Join(changed, ","),
	)
}
