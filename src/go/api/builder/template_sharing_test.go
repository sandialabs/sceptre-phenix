package builder

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"phenix/store"
)

// Accounts of the sharing tests: when each was created.
const (
	createdBob   = "2026-09-26T15:04:05Z"
	createdCarol = "2026-09-26T16:00:00Z"
	recreatedBob = "2026-09-27T08:00:00Z"
)

// grant returns a share with user, bound to the account created at created,
// as the web layer resolves it.
func grant(user, created string) TemplateShare {
	return TemplateShare{User: user, UserCreated: created, GrantedAt: time.Time{}}
}

// sharedUsers returns who a share list names, in order.
func sharedUsers(shares []TemplateShare) []string {
	users := make([]string, 0, len(shares))

	for _, share := range shares {
		users = append(users, share.User+"@"+share.UserCreated)
	}

	return users
}

// TestLibraryShare asserts sharing adds and removes users on every named
// item, is safe to repeat, renews a share whose account was replaced, keeps
// an item that would pass the limit as it was, and leaves the versions of
// the content alone.
func TestLibraryShare(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"), testTemplate("HMI"))

	var floor string

	mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		var err error

		floor, err = library.AddCollection(CollectionContent{Name: "Floor", Description: "", TemplateIDs: ids[1:]}, h.service.NewID)

		return err
	})

	var failed []LibraryFailure

	// Carol is added before bob, and the list is kept sorted.
	shared := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		add := []TemplateShare{grant("carol", createdCarol), grant("bob", createdBob)}
		failed = library.Share([]string{ids[0]}, []string{floor}, add, nil)

		return nil
	})

	if len(failed) != 0 {
		t.Fatalf("sharing failed for %+v", failed)
	}

	want := []string{"bob@" + createdBob, "carol@" + createdCarol}

	plc, collection := shared.Template(ids[0]), shared.Collection(floor)

	if got := sharedUsers(plc.Shares); !slices.Equal(got, want) || !slices.Equal(sharedUsers(collection.Shares), want) {
		t.Fatalf("the shares are %q and %q, want %q", got, sharedUsers(collection.Shares), want)
	}

	// Each share gets the time of the change; sharing is not content.
	granted := plc.Shares[0].GrantedAt

	if granted.IsZero() || !granted.Equal(shared.Updated) || plc.Version != 1 || collection.Version != 1 ||
		shared.Template(ids[1]).Shares != nil {
		t.Fatalf("after sharing: %+v and %+v", plc, collection)
	}

	revision := libraryRecord(t, h).Revision

	// Adding a user again, and removing one that is not there, writes nothing.
	again := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		failed = library.Share([]string{ids[0], ids[0]}, nil, []TemplateShare{grant("bob", createdBob)}, []string{"dave"})

		return nil
	})

	if len(failed) != 0 || again.Revision != revision || !again.Template(ids[0]).Shares[0].GrantedAt.Equal(granted) {
		t.Fatalf("a repeated share changed the library: %+v, failed %+v", again.Template(ids[0]), failed)
	}

	// Bob's account was made again: adding him binds the share to the new
	// account, at a new time. Carol is removed in the same change.
	renewed := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		failed = library.Share([]string{ids[0]}, nil, []TemplateShare{grant("bob", recreatedBob)}, []string{"carol"})

		return nil
	})

	if got := renewed.Template(ids[0]).Shares; len(failed) != 0 || len(got) != 1 || got[0].UserCreated != recreatedBob ||
		!got[0].GrantedAt.After(granted) {
		t.Fatalf("after renewing bob's share: %+v", got)
	}

	// Removal goes first: a user both removed and added is shared with.
	both := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		failed = library.Share(nil, []string{floor}, []TemplateShare{grant("carol", createdCarol)}, []string{"carol"})

		return nil
	})

	if got := sharedUsers(both.Collection(floor).Shares); len(failed) != 0 || !slices.Equal(got, want) {
		t.Fatalf("after removing and adding carol the collection is shared with %q, want %q", got, want)
	}

	// Removing everyone leaves no list.
	emptied := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		failed = library.Share([]string{ids[0]}, []string{floor}, nil, []string{"bob", "carol"})

		return nil
	})

	if emptied.Template(ids[0]).Shares != nil || emptied.Collection(floor).Shares != nil || len(failed) != 0 {
		t.Fatalf("after removing everyone: %+v and %+v", emptied.Template(ids[0]), emptied.Collection(floor))
	}
}

// TestLibraryShareFailures asserts an item that would be shared with more
// than MaxShares users, and an ID the library does not hold, are reported
// and left as they were, while the other items are changed.
func TestLibraryShareFailures(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"), testTemplate("HMI"))

	full := make([]TemplateShare, 0, MaxShares)
	for i := range MaxShares {
		full = append(full, grant(fmt.Sprintf("user-%02d", i), createdBob))
	}

	mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		library.Share([]string{ids[0]}, nil, full, nil)

		return nil
	})

	var failed []LibraryFailure

	library := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		failed = library.Share(
			[]string{ids[0], "missing", ids[1]}, []string{"no-collection"}, []TemplateShare{grant("zed", createdBob)}, nil,
		)

		return nil
	})

	want := []LibraryFailure{
		{Kind: kindTemplate, ID: ids[0], Reason: FailureTooMany},
		{Kind: kindTemplate, ID: "missing", Reason: FailureNotFound},
		{Kind: kindCollection, ID: "no-collection", Reason: FailureNotFound},
	}

	if !slices.Equal(failed, want) {
		t.Fatalf("failed = %+v, want %+v", failed, want)
	}

	if got := library.Template(ids[0]).Shares; len(got) != MaxShares || slices.ContainsFunc(got, func(s TemplateShare) bool {
		return s.User == "zed"
	}) {
		t.Fatalf("the full template is shared with %q", sharedUsers(got))
	}

	if got := sharedUsers(library.Template(ids[1]).Shares); !slices.Equal(got, []string{"zed@" + createdBob}) {
		t.Fatalf("the other template is shared with %q, want zed", got)
	}

	// Removing one makes room, in the same change.
	library = mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		failed = library.Share([]string{ids[0]}, nil, []TemplateShare{grant("zed", createdBob)}, []string{"user-00"})

		return nil
	})

	if len(failed) != 0 || len(library.Template(ids[0]).Shares) != MaxShares {
		t.Fatalf("swapping one user failed: %+v", failed)
	}
}

// TestLibrarySetPublic asserts publishing marks every named item with who
// published it, at the time of the change, keeps that on a repeat, and that
// taking an item back clears it; IDs the library does not hold are reported.
func TestLibrarySetPublic(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"))

	var (
		floor  string
		failed []LibraryFailure
	)

	published := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		var err error

		floor, err = library.AddCollection(CollectionContent{Name: "Floor", Description: "", TemplateIDs: ids}, h.service.NewID)
		if err != nil {
			return err
		}

		failed = library.SetPublic([]string{ids[0], "missing"}, []string{floor}, true, "admin")

		return nil
	})

	if want := []LibraryFailure{{Kind: kindTemplate, ID: "missing", Reason: FailureNotFound}}; !slices.Equal(failed, want) {
		t.Fatalf("failed = %+v, want %+v", failed, want)
	}

	public := published.Template(ids[0]).Public

	if public == nil || public.By != "admin" || !public.At.Equal(published.Updated) || published.Collection(floor).Public == nil ||
		published.Template(ids[0]).Version != 1 || published.Template(builtinIDs[0]).Public != nil {
		t.Fatalf("after publishing: %+v", published.Template(ids[0]))
	}

	// Publishing again, by someone else, changes nothing.
	revision := libraryRecord(t, h).Revision

	again := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		library.SetPublic([]string{ids[0]}, []string{floor}, true, testOwner)

		return nil
	})

	if again.Revision != revision || *again.Template(ids[0]).Public != *public {
		t.Fatalf("publishing again changed the library: %+v", again.Template(ids[0]).Public)
	}

	taken := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		failed = library.SetPublic([]string{ids[0]}, []string{floor, "gone"}, false, "admin")

		return nil
	})

	if taken.Template(ids[0]).Public != nil || taken.Collection(floor).Public != nil ||
		!slices.Equal(failed, []LibraryFailure{{Kind: kindCollection, ID: "gone", Reason: FailureNotFound}}) {
		t.Fatalf("after taking back: %+v, failed %+v", taken.Template(ids[0]), failed)
	}
}

// TestLibraryVisibleTo asserts who sees which items of a library, and how:
// a share of the item or of a collection holding it, publication of either,
// a share before publication, and nothing for an account the share was not
// made for, a user without one, or the owner.
func TestLibraryVisibleTo(t *testing.T) {
	share := func(user, created string) []TemplateShare {
		return []TemplateShare{{User: user, UserCreated: created, GrantedAt: time.Time{}}}
	}

	public := &TemplatePublished{At: time.Time{}, By: testOwner}

	library := &TemplateLibrary{
		Owner: testOwner,
		Templates: []LibraryTemplate{
			{Template: testTemplate("direct"), Version: 1, Shares: share("bob", createdBob)},
			{Template: testTemplate("in shared collection"), Version: 1},
			{Template: testTemplate("published"), Version: 1, Public: public},
			{Template: testTemplate("in published collection"), Version: 1},
			{Template: testTemplate("both"), Version: 1, Shares: share("bob", createdBob), Public: public},
			{Template: testTemplate("private"), Version: 1, Shares: share("carol", createdCarol)},
		},
		Collections: []TemplateCollection{
			{ID: "shared", Name: "Shared", TemplateIDs: []string{"t1"}, Version: 1, Shares: share("bob", createdBob)},
			{ID: "public", Name: "Public", TemplateIDs: []string{"t3", "t4"}, Version: 1, Public: public},
			{ID: "none", Name: "None", TemplateIDs: []string{"t5"}, Version: 1},
		},
	}

	for i := range library.Templates {
		library.Templates[i].ID = fmt.Sprintf("t%d", i)
	}

	tests := []struct {
		name                  string
		user, created         string
		exists                bool
		templates, collection map[string]Visibility
	}{
		{
			name: "recipient", user: "bob", created: createdBob, exists: true,
			templates: map[string]Visibility{
				"t0": VisibleShared, "t1": VisibleShared, "t2": VisibleServer, "t3": VisibleServer, "t4": VisibleShared,
			},
			collection: map[string]Visibility{"shared": VisibleShared, "public": VisibleServer},
		},
		{
			name: "recreated account", user: "bob", created: recreatedBob, exists: true,
			templates:  map[string]Visibility{"t2": VisibleServer, "t3": VisibleServer, "t4": VisibleServer},
			collection: map[string]Visibility{"public": VisibleServer},
		},
		{
			name: "no account", user: "bob", created: createdBob, exists: false,
			templates:  map[string]Visibility{"t2": VisibleServer, "t3": VisibleServer, "t4": VisibleServer},
			collection: map[string]Visibility{"public": VisibleServer},
		},
		{
			name: "another user", user: "dave", created: createdBob, exists: true,
			templates:  map[string]Visibility{"t2": VisibleServer, "t3": VisibleServer, "t4": VisibleServer},
			collection: map[string]Visibility{"public": VisibleServer},
		},
		{
			name: "owner", user: testOwner, created: createdBob, exists: true,
			templates: map[string]Visibility{}, collection: map[string]Visibility{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates, collections := library.VisibleTo(tt.user, tt.created, tt.exists)

			if !maps.Equal(templates, tt.templates) || !maps.Equal(collections, tt.collection) {
				t.Fatalf("VisibleTo = %v and %v, want %v and %v", templates, collections, tt.templates, tt.collection)
			}
		})
	}
}

// TestLibraryHints asserts the hints say whose libraries a user may see
// items of, without naming the user's own, that writing one again is
// harmless, and that LibrarySources lists keys only.
func TestLibraryHints(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if shared, public, err := h.service.LibrarySources(ctx, "bob"); err != nil || len(shared) != 0 || len(public) != 0 {
		t.Fatalf("LibrarySources on an empty store = %q, %q, %v", shared, public, err)
	}

	for range 2 {
		if err := h.service.NoteShared(ctx, testOwner, []string{"bob", "carol"}); err != nil {
			t.Fatalf("NoteShared returned error: %v", err)
		}

		if err := h.service.NotePublic(ctx, "carol"); err != nil {
			t.Fatalf("NotePublic returned error: %v", err)
		}
	}

	if err := h.service.NoteShared(ctx, "dave", []string{"bob"}); err != nil {
		t.Fatalf("NoteShared returned error: %v", err)
	}

	if err := h.service.NotePublic(ctx, "bob"); err != nil {
		t.Fatalf("NotePublic returned error: %v", err)
	}

	// The keys stand for the users only through their scopes, and the
	// value says nothing.
	wantKeys := []string{
		"in/" + OwnerScope("bob") + "/" + OwnerScope(testOwner),
		"in/" + OwnerScope("bob") + "/" + OwnerScope("dave"),
		"in/" + OwnerScope("carol") + "/" + OwnerScope(testOwner),
		"pub/" + OwnerScope("bob"),
		"pub/" + OwnerScope("carol"),
	}
	slices.Sort(wantKeys)

	if got := h.store.Keys(NamespaceTemplates); !slices.Equal(got, wantKeys) {
		t.Fatalf("the hint keys are %q, want %q", got, wantKeys)
	}

	for _, key := range wantKeys {
		if record, err := h.store.GetRecord(NamespaceTemplates, key); err != nil || string(record.Value) != "{}" {
			t.Fatalf("hint %s = %q, %v", key, record.Value, err)
		}
	}

	sortedScopes := func(users ...string) []string {
		scopes := make([]string, 0, len(users))
		for _, user := range users {
			scopes = append(scopes, OwnerScope(user))
		}

		slices.Sort(scopes)

		return scopes
	}

	// Bob's own publication is not a source of his.
	shared, public, err := h.service.LibrarySources(ctx, "bob")
	if err != nil || !slices.Equal(shared, sortedScopes(testOwner, "dave")) || !slices.Equal(public, sortedScopes("carol")) {
		t.Fatalf("LibrarySources(bob) = %q, %q, %v", shared, public, err)
	}

	// A hint does not grant: alice's library is read to see what applies.
	if shared, public, err = h.service.LibrarySources(ctx, "erin"); err != nil || len(shared) != 0 ||
		!slices.Equal(public, sortedScopes("bob", "carol")) {
		t.Fatalf("LibrarySources(erin) = %q, %q, %v", shared, public, err)
	}

	// A library record is never a hint, and a hint never a library.
	mustAddTemplates(t, h, testOwner, testTemplate("PLC"))

	if shared, _, err = h.service.LibrarySources(ctx, "carol"); err != nil || !slices.Equal(shared, sortedScopes(testOwner)) {
		t.Fatalf("LibrarySources(carol) = %q, %v", shared, err)
	}

	if _, err := h.service.GetLibraryByKey(ctx, OwnerScope("dave")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLibraryByKey of a user with hints only = %v, want ErrNotFound", err)
	}
}

// listFailingStore is a record store whose key listings fail.
type listFailingStore struct {
	*unreadableStore
}

func (s *listFailingStore) ListRecordKeys(string, string) ([]string, error) {
	return nil, s.err
}

// TestLibraryHintFailures asserts a hint the store does not write, and a
// listing of hints the store does not answer, are errors, and that
// requests naming no user are refused.
func TestLibraryHintFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	full := fmt.Errorf("updating record: %w", store.ErrNoSpace)

	h.store.BeforeCreate = func(string, string) error { return full }

	if err := h.service.NoteShared(ctx, testOwner, []string{"bob"}); !errors.Is(err, store.ErrNoSpace) {
		t.Fatalf("NoteShared on a full store = %v, want the store's error", err)
	}

	if err := h.service.NotePublic(ctx, testOwner); !errors.Is(err, store.ErrNoSpace) {
		t.Fatalf("NotePublic on a full store = %v, want the store's error", err)
	}

	h.store.BeforeCreate = nil

	down := errors.New("the store is down")

	service, err := New(WithStore(&listFailingStore{unreadableStore: &unreadableStore{Store: h.store, err: down}}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if _, _, err := service.LibrarySources(ctx, "bob"); !errors.Is(err, down) {
		t.Fatalf("LibrarySources on a store that cannot list = %v, want its error", err)
	}

	for name, err := range map[string]error{
		"NoteShared without owner":     h.service.NoteShared(ctx, "", []string{"bob"}),
		"NoteShared without recipient": h.service.NoteShared(ctx, testOwner, []string{""}),
		"NotePublic without owner":     h.service.NotePublic(ctx, ""),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}

	if _, _, err := h.service.LibrarySources(ctx, strings.Repeat("u", MaxOwnerLength+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("LibrarySources of an unusable user = %v, want ErrInvalid", err)
	}

	if h.store.Count(NamespaceTemplates) != 0 {
		t.Fatalf("a refused hint was written: %q", h.store.Keys(NamespaceTemplates))
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if _, _, err := h.service.LibrarySources(canceled, "bob"); !errors.Is(err, context.Canceled) {
		t.Errorf("LibrarySources after cancel = %v", err)
	}
}
