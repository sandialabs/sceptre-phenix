package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
)

// testAccountCreated is the creation time of every test recipient's account.
const testAccountCreated = "2026-09-26T15:04:05Z"

func testGrant(user string, access ShareAccess) ShareGrant {
	return ShareGrant{User: user, Access: access, UserCreated: testAccountCreated}
}

// shareTestDraft replaces who a draft is shared with and returns the draft.
func shareTestDraft(t *testing.T, h *testHarness, meta *DraftMetadata, grants ...ShareGrant) *DraftMetadata {
	t.Helper()

	shared, err := h.service.UpdateShares(context.Background(), UpdateSharesRequest{
		DraftID: meta.ID, Actor: testOwner, ExpectedVersion: meta.SharingVersion(), Grants: grants,
	})
	if err != nil {
		t.Fatalf("UpdateShares returned error: %v", err)
	}

	return shared
}

func TestUpdateSharesRejectsInvalidRequests(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	tooMany := make([]ShareGrant, 0, MaxShares+1)
	for i := range MaxShares + 1 {
		tooMany = append(tooMany, testGrant(fmt.Sprintf("user-%02d", i), ShareView))
	}

	tests := map[string]UpdateSharesRequest{
		"owner in the list": {Grants: []ShareGrant{testGrant(testOwner, ShareView)}},
		"duplicate":         {Grants: []ShareGrant{testGrant(testPeer, ShareView), testGrant(testPeer, ShareEdit)}},
		"too many users":    {Grants: tooMany},
		"unknown access":    {Grants: []ShareGrant{testGrant(testPeer, "admin")}},
		"slash in a name":   {Grants: []ShareGrant{testGrant("bob/carol", ShareView)}},
		"control character": {Grants: []ShareGrant{testGrant("bob\x00", ShareView)}},
		"C1 control":        {Grants: []ShareGrant{testGrant("bob\u009b", ShareView)}},
		"bidi override":     {Grants: []ShareGrant{testGrant("bob\u202e", ShareView)}},
		"line separator":    {Grants: []ShareGrant{testGrant("bob\u2028", ShareView)}},
		"empty name":        {Grants: []ShareGrant{testGrant("", ShareView)}},
		"no account binding": {
			Grants: []ShareGrant{{User: testPeer, Access: ShareView, UserCreated: ""}},
		},
		"non-owner actor":  {Actor: testPeer, Grants: []ShareGrant{testGrant("carol", ShareView)}},
		"negative version": {ExpectedVersion: -1},
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			req.DraftID = meta.ID
			if req.Actor == "" {
				req.Actor = testOwner
			}

			if _, err := h.service.UpdateShares(ctx, req); !errors.Is(err, ErrInvalid) {
				t.Fatalf("UpdateShares error = %s, want ErrInvalid", fmtErr(err))
			}
		})
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if stored.Revision != meta.Revision || stored.Sharing != nil {
		t.Fatalf("revision = %d, sharing = %+v; want the draft untouched", stored.Revision, stored.Sharing)
	}
}

func TestUpdateSharesStoresTheList(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	if meta.SharesETag() != `"shares-0"` || meta.ShareFor(testPeer) != nil {
		t.Fatalf("a new draft: shares etag %s, share %+v", meta.SharesETag(), meta.ShareFor(testPeer))
	}

	shared := shareTestDraft(t, h, meta, testGrant("carol", ShareView), testGrant(testPeer, ShareEdit))

	switch entries := shared.Sharing.Entries; {
	case shared.SharingVersion() != 1 || shared.SharesETag() != `"shares-1"`:
		t.Fatalf("version = %d, etag %s; want 1", shared.SharingVersion(), shared.SharesETag())
	case len(entries) != 2 || entries[0].User != testPeer || entries[1].User != "carol":
		t.Fatalf("entries = %+v, want bob then carol", entries)
	case entries[0].GrantedBy != testOwner || entries[0].GrantedAt.IsZero() || shared.Sharing.UpdatedBy != testOwner:
		t.Fatalf("entries = %+v, want each granted by the owner", entries)
	case shared.ETag() == meta.ETag():
		t.Fatal("the draft ETag did not change with its share list")
	case !shared.Updated.Equal(meta.Updated) || shared.LastModifiedBy != meta.LastModifiedBy:
		t.Fatalf("updated %v by %s, want the content timestamps untouched", shared.Updated, shared.LastModifiedBy)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if stored.Revision != shared.Revision || !sameShareEntries(stored.Sharing.Entries, shared.Sharing.Entries) {
		t.Fatalf("stored sharing = %+v, want what UpdateShares returned", stored.Sharing)
	}

	// A request authorized against a list that has changed since is a
	// conflict.
	_, err = h.service.UpdateShares(ctx, UpdateSharesRequest{
		DraftID: meta.ID, Actor: testOwner, ExpectedVersion: 0, Grants: nil,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateShares at a stale version error = %s, want ErrConflict", fmtErr(err))
	}

	// The same list again writes nothing.
	same := shareTestDraft(t, h, shared, testGrant(testPeer, ShareEdit), testGrant("carol", ShareView))
	if same.Revision != shared.Revision || same.SharingVersion() != 1 {
		t.Fatalf("revision = %d, version = %d; want the no-op to write nothing", same.Revision, same.SharingVersion())
	}

	// An unchanged grant keeps when it was granted; a changed one does not.
	bob, carol := *shared.ShareFor(testPeer), *shared.ShareFor("carol")

	changed := shareTestDraft(t, h, same,
		testGrant(testPeer, ShareView), testGrant("carol", ShareView), testGrant("dave", ShareEdit))

	switch {
	case changed.SharingVersion() != 2:
		t.Fatalf("version = %d, want 2", changed.SharingVersion())
	case !changed.ShareFor("carol").GrantedAt.Equal(carol.GrantedAt):
		t.Fatal("an unchanged grant was granted again")
	case changed.ShareFor(testPeer).GrantedAt.Equal(bob.GrantedAt):
		t.Fatal("a grant whose access changed kept its old grant time")
	}

	rebound := shareTestDraft(t, h, changed,
		testGrant(testPeer, ShareView),
		ShareGrant{User: "carol", Access: ShareView, UserCreated: "2026-09-27T00:00:00Z"},
		testGrant("dave", ShareEdit))

	if rebound.ShareFor("carol").GrantedAt.Equal(carol.GrantedAt) {
		t.Fatal("a grant bound to another account kept its old grant time")
	}

	// Emptying the list still counts as a change, so no stale tag matches it.
	emptied := shareTestDraft(t, h, rebound)

	stored, err = h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft of the emptied list returned error: %v", err)
	}

	if emptied.SharingVersion() != 4 || stored.SharingVersion() != 4 || len(stored.Sharing.Entries) != 0 {
		t.Fatalf("version = %d, stored %+v; want an empty list at version 4", emptied.SharingVersion(), stored.Sharing)
	}
}

// TestUpdateSharesRetriesOverSaves lets an editor's save land between reading
// the draft and writing its share list: the list is written again on top of
// the save, which is kept.
func TestUpdateSharesRetriesOverSaves(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := shareTestDraft(t, h, createTestDraft(t, h, "topo"), testGrant(testPeer, ShareEdit))

	var (
		saved *DraftMetadata
		raced bool
	)

	h.store.BeforeUpdate = func(namespace, _ string) error {
		if namespace != NamespaceDrafts || raced {
			return nil
		}

		raced = true
		saved = appendTestSnapshot(t, h, meta, "topo-v2", testPeer)

		return nil
	}

	shared, err := h.service.UpdateShares(ctx, UpdateSharesRequest{
		DraftID: meta.ID, Actor: testOwner, ExpectedVersion: 1,
		Grants: []ShareGrant{testGrant(testPeer, ShareView)},
	})

	switch {
	case err != nil:
		t.Fatalf("UpdateShares returned error: %v", err)
	case !raced:
		t.Fatal("the test did not interleave the save")
	case shared.Current().ID != saved.Current().ID || shared.Revision <= saved.Revision:
		t.Fatalf("current snapshot %s at revision %d, want the editor's save kept", shared.Current().ID, shared.Revision)
	case shared.SharingVersion() != 2 || shared.ShareFor(testPeer).Access != ShareView:
		t.Fatalf("sharing = %+v, want bob at view", shared.Sharing)
	}

	// A draft that keeps changing is busy.
	attempts := 0
	h.store.BeforeUpdate = func(namespace, key string) error {
		if namespace != NamespaceDrafts {
			return nil
		}

		attempts++

		return store.NewRecordConflictError(namespace, key, 0, 0)
	}

	_, err = h.service.UpdateShares(ctx, UpdateSharesRequest{
		DraftID: meta.ID, Actor: testOwner, ExpectedVersion: 2, Grants: nil,
	})
	if !errors.Is(err, ErrBusy) || attempts != maxShareAttempts {
		t.Fatalf("UpdateShares error = %s after %d attempts, want ErrBusy after %d", fmtErr(err), attempts, maxShareAttempts)
	}
}

// TestUpdateSharesSettlesAmbiguousWrites asserts a write the store reports
// as failed, but may have applied, is read back.
func TestUpdateSharesSettlesAmbiguousWrites(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	// Applied, then reported as failed.
	h.store.AfterWrite = func(namespace, _ string) error {
		if namespace != NamespaceDrafts {
			return nil
		}

		return errAmbiguous
	}

	shared, err := h.service.UpdateShares(ctx, UpdateSharesRequest{
		DraftID: meta.ID, Actor: testOwner, ExpectedVersion: 0,
		Grants: []ShareGrant{testGrant(testPeer, ShareEdit)},
	})
	if err != nil || shared.SharingVersion() != 1 {
		t.Fatalf("UpdateShares = %+v, %s; want the applied list", shared, fmtErr(err))
	}

	// Applied, then replaced by another change of the list before it could be
	// read back: a conflict, not a success. The hook runs under the store
	// lock, so it rewrites the stored record through RewriteLocked.
	h.store.AfterWrite = func(namespace, key string) error {
		if namespace != NamespaceDrafts {
			return nil
		}

		err := h.store.RewriteLocked(namespace, key, func(value []byte) ([]byte, error) {
			var other DraftMetadata
			if err := json.Unmarshal(value, &other); err != nil {
				return nil, err
			}

			other.Sharing.Entries[0].Access = ShareEdit

			return json.Marshal(&other)
		})
		if err != nil {
			return err
		}

		return errAmbiguous
	}

	_, err = h.service.UpdateShares(ctx, UpdateSharesRequest{
		DraftID: meta.ID, Actor: testOwner, ExpectedVersion: 1,
		Grants: []ShareGrant{testGrant(testPeer, ShareView)},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateShares error = %s, want ErrConflict", fmtErr(err))
	}

	// Never applied: the store's error.
	h.store.AfterWrite = nil
	h.store.BeforeUpdate = func(namespace, _ string) error {
		if namespace != NamespaceDrafts {
			return nil
		}

		return errAmbiguous
	}

	_, err = h.service.UpdateShares(ctx, UpdateSharesRequest{
		DraftID: meta.ID, Actor: testOwner, ExpectedVersion: 2, Grants: nil,
	})
	if !errors.Is(err, errAmbiguous) {
		t.Fatalf("UpdateShares error = %s, want the store's error", fmtErr(err))
	}
}

// TestSharingSurvivesContentChanges asserts content changes keep who a draft
// is shared with, and that a clone never shares its list with the original.
func TestSharingSurvivesContentChanges(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := shareTestDraft(t, h, createTestDraft(t, h, "topo"), testGrant(testPeer, ShareEdit))

	clone := meta.Clone()
	clone.Sharing.Version++
	clone.Sharing.Entries[0].Access = ShareView

	if meta.SharingVersion() != 1 || meta.ShareFor(testPeer).Access != ShareEdit {
		t.Fatalf("sharing = %+v, want the original untouched by its clone", meta.Sharing)
	}

	appended := appendTestSnapshot(t, h, meta, "topo-v2", testPeer)

	moved, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testPeer, ExpectedRevision: appended.Revision, Index: 0, SnapshotID: "", UseIndex: true,
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	published, err := h.service.MarkPublished(ctx, markPublishedRequest(moved, moved.Revision))
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	for name, changed := range map[string]*DraftMetadata{
		"AppendSnapshot": appended, "MoveCursor": moved, "MarkPublished": published,
	} {
		if changed.SharingVersion() != 1 || !sameShareEntries(changed.Sharing.Entries, meta.Sharing.Entries) {
			t.Errorf("%s: sharing = %+v, want %+v", name, changed.Sharing, meta.Sharing)
		}
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil || !sameShareEntries(stored.Sharing.Entries, meta.Sharing.Entries) {
		t.Fatalf("stored sharing = %+v, %s; want it kept", stored, fmtErr(err))
	}
}

// TestUnsharedDraftRecordsRoundTrip asserts a draft never shared is stored
// without a sharing field, as drafts written before sharing existed are, and
// reads back.
func TestUnsharedDraftRecordsRoundTrip(t *testing.T) {
	h := newHarness(t)

	meta := createTestDraft(t, h, "topo")

	record, err := h.store.GetRecord(NamespaceDrafts, meta.ID)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(record.Value, &raw); err != nil {
		t.Fatalf("unmarshalling the stored draft returned error: %v", err)
	}

	if _, ok := raw["sharing"]; ok {
		t.Fatal("a draft never shared was stored with a sharing field")
	}

	stored, err := h.service.GetDraft(context.Background(), meta.ID)
	if err != nil || stored.Sharing != nil || stored.SharesETag() != `"shares-0"` {
		t.Fatalf("GetDraft = %+v, %s; want a draft never shared", stored, fmtErr(err))
	}
}

// TestTamperedSharingIsRejected asserts a share list that does not validate
// makes the draft damaged: it is never read leniently.
func TestTamperedSharingIsRejected(t *testing.T) {
	entry := func(user string) map[string]any {
		return map[string]any{
			"user": user, "access": "view", "userCreated": testAccountCreated,
			"grantedAt": memrecord.Time(1), "grantedBy": testOwner,
		}
	}

	sharing := func(entries ...map[string]any) map[string]any {
		list := make([]any, 0, len(entries))
		for _, e := range entries {
			list = append(list, e)
		}

		return map[string]any{"version": 1, "entries": list, "updatedAt": memrecord.Time(1), "updatedBy": testOwner}
	}

	with := func(user, key string, value any) map[string]any {
		e := entry(user)
		e[key] = value

		return e
	}

	tooMany := make([]map[string]any, 0, MaxShares+1)
	for i := range MaxShares + 1 {
		tooMany = append(tooMany, entry(fmt.Sprintf("user-%02d", i)))
	}

	tests := map[string]map[string]any{
		"version 0":                 {"version": 0, "entries": []any{}, "updatedAt": memrecord.Time(1), "updatedBy": testOwner},
		"no actor":                  {"version": 1, "entries": []any{}, "updatedAt": memrecord.Time(1), "updatedBy": ""},
		"unknown field":             {"version": 1, "entries": []any{}, "updatedAt": memrecord.Time(1), "updatedBy": testOwner, "x": 1},
		"unknown field in an entry": sharing(with(testPeer, "admin", true)),
		"too many users":            sharing(tooMany...),
		"slash in a user":           sharing(entry("bob/carol")),
		"control character":         sharing(entry("bob\x00")),
		"names the owner":           sharing(entry(testOwner)),
		"out of order":              sharing(entry("carol"), entry(testPeer)),
		"repeats a user":            sharing(entry(testPeer), entry(testPeer)),
		"unknown access":            sharing(with(testPeer, "access", "admin")),
		"no account binding":        sharing(with(testPeer, "userCreated", "")),
		"no granting actor":         sharing(with(testPeer, "grantedBy", "")),
	}

	for name, state := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			meta := createTestDraft(t, h, "topo")

			tamperDraft(t, h, meta, func(raw map[string]any) { raw["sharing"] = state })

			if _, err := h.service.GetDraft(context.Background(), meta.ID); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("GetDraft error = %s, want ErrCorrupt", fmtErr(err))
			}
		})
	}
}

// TestRevokeFailsSavesAuthorizedBefore asserts a save authorized by a share
// before it was removed cannot land after: it is written at the revision it
// was authorized at, which the removal changed.
func TestRevokeFailsSavesAuthorizedBefore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	authorized := shareTestDraft(t, h, createTestDraft(t, h, "topo"), testGrant(testPeer, ShareEdit))

	revoked := shareTestDraft(t, h, authorized)

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: authorized.ID, Actor: testPeer, ExpectedRevision: authorized.Revision,
		Document: testDocument(t, "topo-v2", 0), Summary: "topo-v2",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("AppendSnapshot error = %s, want ErrConflict", fmtErr(err))
	}

	stored, err := h.service.GetDraft(ctx, authorized.ID)
	if err != nil || stored.Revision != revoked.Revision || len(stored.History) != 1 {
		t.Fatalf("stored = %+v, %s; want the draft as the revoke left it", stored, fmtErr(err))
	}
}
