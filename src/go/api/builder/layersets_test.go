package builder

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/types/builder"
)

// testLayerSet returns a valid layer set of the given name with two layers,
// one with a color and node matchers.
func testLayerSet(name string) builder.LayerSet {
	high, low := 2, 1

	return builder.LayerSet{
		Name: name, Description: "Levels of the plant.", IconKey: "plc", Icon: "",
		MatchConflict: builder.MatchConflictHighest, LayerOutlines: true,
		Layers: []builder.Layer{
			{
				ID: &high, Label: "Supervisory control", IconKey: "desktop", Icon: "", Color: "#15803d",
				Matchers: map[string]string{"general.hostname": "^hmi-"},
			},
			{ID: &low, Label: "Field devices", IconKey: "", Icon: "", Color: "", Matchers: nil},
		},
	}
}

// layerSetIDs returns the ID of each set, in order.
func layerSetIDs(sets []LibraryLayerSet) []string {
	ids := make([]string, len(sets))
	for i := range sets {
		ids[i] = sets[i].ID
	}

	return ids
}

func TestLayerSetLibraryRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant levels"))
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant levels) returned error: %v", err)
	}

	want := LibraryLayerSet{
		LayerSet: testLayerSet("Plant levels"), ID: created.ID, Version: 1,
		Created: created.Created, Updated: created.Created, CreatedBy: testActor, UpdatedBy: testActor,
		Revision: created.Revision,
	}

	if diff := cmp.Diff(want, *created, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("CreateLayerSet(Plant levels) mismatch (-want +got):\n%s", diff)
	}

	if created.ID == "" || created.Created.IsZero() {
		t.Errorf("CreateLayerSet(Plant levels) id = %q, created %v; want an ID and a time", created.ID, created.Created)
	}

	if got := created.ETag(); got != `"1"` {
		t.Errorf("CreateLayerSet(Plant levels) ETag() = %s, want \"1\"", got)
	}

	if created.Builtin() {
		t.Error("CreateLayerSet(Plant levels) Builtin() = true, want false")
	}

	if got, err := h.service.GetLayerSet(ctx, created.ID); err != nil {
		t.Errorf("GetLayerSet(%s) returned error: %v", created.ID, err)
	} else if diff := cmp.Diff(*created, *got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("GetLayerSet(%s) mismatch (-want +got):\n%s", created.ID, diff)
	}

	change := testLayerSet("Plant levels, changed")

	updated, err := h.service.UpdateLayerSet(ctx, testPeer, created.ID, created.ETag(), change)
	if err != nil {
		t.Fatalf("UpdateLayerSet(%s) returned error: %v", created.ID, err)
	}

	want = LibraryLayerSet{
		LayerSet: change, ID: created.ID, Version: 2,
		Created: created.Created, CreatedBy: testActor, UpdatedBy: testPeer,
	}

	if diff := cmp.Diff(want, *updated, cmpopts.EquateEmpty(),
		cmpopts.IgnoreFields(LibraryLayerSet{}, "Updated", "Revision")); diff != "" {
		t.Errorf("UpdateLayerSet(%s) mismatch (-want +got):\n%s", created.ID, diff)
	}

	if !updated.Updated.After(created.Updated) {
		t.Errorf("UpdateLayerSet(%s) updated = %v, want after %v", created.ID, updated.Updated, created.Updated)
	}

	// The same content again writes nothing.
	again, err := h.service.UpdateLayerSet(ctx, testActor, created.ID, updated.ETag(), change)
	if err != nil {
		t.Fatalf("UpdateLayerSet(%s) with the same content returned error: %v", created.ID, err)
	}

	if diff := cmp.Diff(*updated, *again, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("UpdateLayerSet(%s) with the same content mismatch (-want +got):\n%s", created.ID, diff)
	}
}

func TestLayerSetLibraryRefusesStaleAndDeletedSets(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant levels"))
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant levels) returned error: %v", err)
	}

	change := testLayerSet("Plant levels, changed")

	if _, err := h.service.UpdateLayerSet(ctx, testPeer, created.ID, created.ETag(), change); err != nil {
		t.Fatalf("UpdateLayerSet(%s) returned error: %v", created.ID, err)
	}

	// A stale entity tag is refused with the current one.
	_, err = h.service.UpdateLayerSet(ctx, testActor, created.ID, created.ETag(), testLayerSet("Other"))
	if !errors.Is(err, ErrConflict) {
		t.Errorf("UpdateLayerSet(%s) with a stale ETag error = %v, want ErrConflict", created.ID, err)
	}

	var stale *LayerSetStaleError
	if !errors.As(err, &stale) {
		t.Errorf("UpdateLayerSet(%s) with a stale ETag error = %v, want a LayerSetStaleError", created.ID, err)
	} else if diff := cmp.Diff(LayerSetStaleError{ID: created.ID, ETag: `"2"`}, *stale); diff != "" {
		t.Errorf("UpdateLayerSet(%s) with a stale ETag refusal mismatch (-want +got):\n%s", created.ID, diff)
	}

	sets, damaged, err := h.service.ListLayerSets(ctx)
	if err != nil {
		t.Fatalf("ListLayerSets returned error: %v", err)
	}

	if damaged != 0 {
		t.Errorf("ListLayerSets damaged = %d, want 0", damaged)
	}

	// The built-in set comes first, then the new set.
	if diff := cmp.Diff([]string{builder.BuiltinPurdueLayerSetID, created.ID}, layerSetIDs(sets)); diff != "" {
		t.Errorf("ListLayerSets IDs mismatch (-want +got):\n%s", diff)
	}

	// A delete with a stale entity tag is refused with the current one, and
	// deletes nothing.
	err = h.service.DeleteLayerSet(ctx, created.ID, created.ETag())
	if !errors.As(err, &stale) {
		t.Errorf("DeleteLayerSet(%s) with a stale ETag error = %v, want a LayerSetStaleError", created.ID, err)
	} else if diff := cmp.Diff(LayerSetStaleError{ID: created.ID, ETag: `"2"`}, *stale); diff != "" {
		t.Errorf("DeleteLayerSet(%s) with a stale ETag refusal mismatch (-want +got):\n%s", created.ID, diff)
	}

	if _, err := h.service.GetLayerSet(ctx, created.ID); err != nil {
		t.Errorf("GetLayerSet(%s) after a stale delete returned error: %v", created.ID, err)
	}

	if err := h.service.DeleteLayerSet(ctx, created.ID, `"2"`); err != nil {
		t.Fatalf("DeleteLayerSet(%s) returned error: %v", created.ID, err)
	}

	if _, err := h.service.GetLayerSet(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLayerSet(%s) after the delete = %v, want ErrNotFound", created.ID, err)
	}

	if err := h.service.DeleteLayerSet(ctx, created.ID, `"2"`); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteLayerSet(%s) a second time = %v, want ErrNotFound", created.ID, err)
	}

	if _, err := h.service.UpdateLayerSet(ctx, testActor, created.ID, `"2"`, change); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateLayerSet(%s) after the delete = %v, want ErrNotFound", created.ID, err)
	}
}

func TestLayerSetLibrarySortsByName(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	for _, name := range []string{"beta", "Alpha", "alpha", "Gamma"} {
		if _, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet(name)); err != nil {
			t.Fatalf("CreateLayerSet(%q) returned error: %v", name, err)
		}
	}

	sets, _, err := h.service.ListLayerSets(ctx)
	if err != nil {
		t.Fatalf("ListLayerSets returned error: %v", err)
	}

	names := make([]string, 0, len(sets))
	for i := range sets {
		names = append(names, sets[i].Name)
	}

	if want := []string{"Purdue Model", "Alpha", "alpha", "beta", "Gamma"}; !slices.Equal(names, want) {
		t.Errorf("ListLayerSets names = %q, want %q", names, want)
	}
}

func TestBuiltinLayerSetIsReadOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	purdue, err := h.service.GetLayerSet(ctx, builder.BuiltinPurdueLayerSetID)
	if err != nil {
		t.Fatalf("GetLayerSet(%s) returned error: %v", builder.BuiltinPurdueLayerSetID, err)
	}

	type summary struct {
		Builtin   bool
		Version   int64
		Name      string
		Layers    int
		Created   time.Time
		CreatedBy string
	}

	want := summary{Builtin: true, Version: 1, Name: "Purdue Model", Layers: 7}
	got := summary{
		Builtin: purdue.Builtin(), Version: purdue.Version, Name: purdue.Name, Layers: len(purdue.Layers),
		Created: purdue.Created, CreatedBy: purdue.CreatedBy,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetLayerSet(%s) mismatch (-want +got):\n%s", builder.BuiltinPurdueLayerSetID, diff)
	}

	if _, err := h.service.UpdateLayerSet(ctx, testActor, purdue.ID, purdue.ETag(), testLayerSet("Mine")); !errors.Is(
		err, ErrForbidden,
	) {
		t.Errorf("UpdateLayerSet(%s) = %v, want ErrForbidden", purdue.ID, err)
	}

	if err := h.service.DeleteLayerSet(ctx, purdue.ID, purdue.ETag()); !errors.Is(err, ErrForbidden) {
		t.Errorf("DeleteLayerSet(%s) = %v, want ErrForbidden", purdue.ID, err)
	}

	if h.store.Count(NamespaceLayerSets) != 0 {
		t.Errorf("layer set records after reading the built-in set = %v, want none", h.store.Keys(NamespaceLayerSets))
	}
}

func TestLayerSetLibraryRefuses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	_, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet(""))
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("CreateLayerSet(no name) error = %v, want ErrInvalid", err)
	}

	var refused *builder.LayerSetError
	if !errors.As(err, &refused) {
		t.Errorf("CreateLayerSet(no name) error = %v, want a LayerSetError", err)
	} else if diff := cmp.Diff([]builder.Code{builder.CodeLayerSetNameRequired}, issueCodes(refused.Issues)); diff != "" {
		t.Errorf("CreateLayerSet(no name) issue codes mismatch (-want +got):\n%s", diff)
	}

	if _, err := h.service.CreateLayerSet(ctx, "", testLayerSet("Plant")); !errors.Is(err, ErrInvalid) {
		t.Errorf("CreateLayerSet(Plant) with no actor = %v, want ErrInvalid", err)
	}

	for _, id := range []string{"", "missing", "a/b", "builtin:other", strings.Repeat("a", MaxIDLength+1)} {
		if _, err := h.service.GetLayerSet(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetLayerSet(%q) = %v, want ErrNotFound", id, err)
		}
	}

	if h.store.Count(NamespaceLayerSets) != 0 {
		t.Errorf("layer set records after the refusals = %v, want none", h.store.Keys(NamespaceLayerSets))
	}

	// An empty list of matchers is stored as none.
	set := testLayerSet("Plant")
	set.Layers[1].Matchers = map[string]string{}

	created, err := h.service.CreateLayerSet(ctx, testActor, set)
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
	}

	if got := created.Layers[1].Matchers; got != nil {
		t.Errorf("CreateLayerSet(Plant) second layer matchers = %#v, want nil", got)
	}
}

func TestLayerSetLibraryHoldsAtMost200Sets(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	for i := range MaxLayerSets {
		if _, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Set "+strconv.Itoa(i))); err != nil {
			t.Fatalf("CreateLayerSet(Set %d) returned error: %v", i, err)
		}
	}

	_, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("One more"))
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("CreateLayerSet(One more) past the limit error = %v, want ErrTooLarge", err)
	}

	var limit *LayerSetLimitError
	if !errors.As(err, &limit) {
		t.Errorf("CreateLayerSet(One more) past the limit error = %v, want a LayerSetLimitError", err)
	} else if diff := cmp.Diff(LayerSetLimitError{Limit: MaxLayerSets}, *limit); diff != "" {
		t.Errorf("CreateLayerSet(One more) past the limit refusal mismatch (-want +got):\n%s", diff)
	}
}

func TestLayerSetLibrarySkipsDamagedRecords(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
	}

	for key, value := range map[string]string{
		"set/broken":  "{",
		"set/unknown": `{"id":"unknown","name":"x","layers":[{"id":1,"label":"a"}],"version":1,"extra":1}`,
		"set/other":   `{"id":"another","name":"x","layers":[{"id":1,"label":"a"}],"version":1}`,
		"set/version": `{"id":"version","name":"x","layers":[{"id":1,"label":"a"}],"version":0}`,
		"set/invalid": `{"id":"invalid","name":"x","layers":[],"version":1}`,
	} {
		if _, err := h.store.CreateRecord(NamespaceLayerSets, key, []byte(value)); err != nil {
			t.Fatalf("CreateRecord(%s) returned error: %v", key, err)
		}
	}

	sets, damaged, err := h.service.ListLayerSets(ctx)
	if err != nil {
		t.Fatalf("ListLayerSets returned error: %v", err)
	}

	if damaged != 5 {
		t.Errorf("ListLayerSets damaged = %d, want 5", damaged)
	}

	if diff := cmp.Diff([]string{builder.BuiltinPurdueLayerSetID, created.ID}, layerSetIDs(sets)); diff != "" {
		t.Errorf("ListLayerSets IDs mismatch (-want +got):\n%s", diff)
	}

	if _, err := h.service.GetLayerSet(ctx, "broken"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("GetLayerSet(broken) = %v, want ErrCorrupt", err)
	}
}

func TestDefaultLayerSet(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// mustChoose sets the default with the tag of the current choice, and
	// returns the choice as stored.
	mustChoose := func(id string) LayerSetDefault {
		t.Helper()

		current, err := h.service.DefaultLayerSet(ctx)
		if err != nil {
			t.Fatalf("DefaultLayerSet() returned error: %v", err)
		}

		chosen, err := h.service.SetDefaultLayerSet(ctx, id, current.ETag)
		if err != nil {
			t.Fatalf("SetDefaultLayerSet(%q, %s) returned error: %v", id, current.ETag, err)
		}

		if chosen.ID != id || chosen.ETag == current.ETag && id != "" {
			t.Errorf("SetDefaultLayerSet(%q, %s) = %+v, want that ID and a new tag", id, current.ETag, chosen)
		}

		return chosen
	}

	none := LayerSetDefault{ID: "", ETag: `"0"`}

	if got, err := h.service.DefaultLayerSet(ctx); err != nil || got != none {
		t.Errorf("DefaultLayerSet() with no choice = %+v, %v; want %+v, nil", got, err, none)
	}

	first := mustChoose(builder.BuiltinPurdueLayerSetID)

	if got, err := h.service.DefaultLayerSet(ctx); err != nil || got != first {
		t.Errorf("DefaultLayerSet() = %+v, %v; want %+v, nil", got, err, first)
	}

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
	}

	// A second choice replaces the first.
	second := mustChoose(created.ID)

	if got, _ := h.service.DefaultLayerSet(ctx); got != second {
		t.Errorf("DefaultLayerSet() after a second choice = %+v, want %+v", got, second)
	}

	// A choice made with the tag of an older one is refused with the current
	// tag, and changes nothing.
	for _, id := range []string{builder.BuiltinPurdueLayerSetID, ""} {
		_, err := h.service.SetDefaultLayerSet(ctx, id, first.ETag)

		var stale *LayerSetStaleError
		if !errors.As(err, &stale) {
			t.Errorf("SetDefaultLayerSet(%q) with a stale ETag error = %v, want a LayerSetStaleError", id, err)
		} else if diff := cmp.Diff(LayerSetStaleError{ID: layerSetDefaultKey, ETag: second.ETag}, *stale); diff != "" {
			t.Errorf("SetDefaultLayerSet(%q) with a stale ETag refusal mismatch (-want +got):\n%s", id, diff)
		}
	}

	if got, _ := h.service.DefaultLayerSet(ctx); got != second {
		t.Errorf("DefaultLayerSet() after stale choices = %+v, want %+v", got, second)
	}

	if _, err := h.service.SetDefaultLayerSet(ctx, "missing", second.ETag); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetDefaultLayerSet(missing) = %v, want ErrNotFound", err)
	}

	// A deleted set is no choice, with the tag of the record that stays.
	if err := h.service.DeleteLayerSet(ctx, created.ID, created.ETag()); err != nil {
		t.Fatalf("DeleteLayerSet(%s) returned error: %v", created.ID, err)
	}

	want := LayerSetDefault{ID: "", ETag: second.ETag}
	if got, err := h.service.DefaultLayerSet(ctx); err != nil || got != want {
		t.Errorf("DefaultLayerSet() after the delete = %+v, %v; want %+v, nil", got, err, want)
	}

	if keys := h.store.Keys(NamespaceLayerSets); !slices.Equal(keys, []string{layerSetDefaultKey}) {
		t.Errorf("layer set records = %q, want only the default record %q", keys, layerSetDefaultKey)
	}

	// Clearing removes the record, and clearing twice is harmless.
	for i := range 2 {
		if got := mustChoose(""); got != none {
			t.Errorf("SetDefaultLayerSet(\"\") call %d = %+v, want %+v", i+1, got, none)
		}
	}

	if keys := h.store.Keys(NamespaceLayerSets); len(keys) != 0 {
		t.Errorf("layer set records after clearing = %q, want none", keys)
	}

	// A record that does not decode is no choice, and can be cleared.
	record, err := h.store.CreateRecord(NamespaceLayerSets, layerSetDefaultKey, []byte(`{"layerSet":`))
	if err != nil {
		t.Fatalf("CreateRecord(%s) returned error: %v", layerSetDefaultKey, err)
	}

	want = LayerSetDefault{ID: "", ETag: RevisionETag(record.Revision)}
	if got, err := h.service.DefaultLayerSet(ctx); err != nil || got != want {
		t.Errorf("DefaultLayerSet() of an unreadable record = %+v, %v; want %+v, nil", got, err, want)
	}

	mustChoose("")
}

func TestUpdateLayerSetRetriesAfterAnotherWrite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
	}

	key := layerSetKey(created.ID)
	interleaved := false

	// Another writer stores the same record between the read and the write. The
	// update reads it again and goes through.
	h.store.BeforeUpdate = func(namespace, at string) error {
		if namespace != NamespaceLayerSets || at != key || interleaved {
			return nil
		}

		interleaved = true

		record, err := h.store.GetRecord(namespace, at)
		if err != nil {
			return err
		}

		_, err = h.store.UpdateRecord(namespace, at, record.Value, record.Revision)

		return err
	}

	updated, err := h.service.UpdateLayerSet(ctx, testPeer, created.ID, created.ETag(), testLayerSet("Changed"))
	if err != nil {
		t.Fatalf("UpdateLayerSet(%s) racing another write returned error: %v", created.ID, err)
	}

	if !interleaved {
		t.Errorf("UpdateLayerSet(%s) wrote no record another writer could interleave with", created.ID)
	}

	want := LibraryLayerSet{
		LayerSet: testLayerSet("Changed"), ID: created.ID, Version: 2,
		Created: created.Created, CreatedBy: testActor, UpdatedBy: testPeer,
	}

	if diff := cmp.Diff(want, *updated, cmpopts.EquateEmpty(),
		cmpopts.IgnoreFields(LibraryLayerSet{}, "Updated", "Revision")); diff != "" {
		t.Errorf("UpdateLayerSet(%s) racing another write mismatch (-want +got):\n%s", created.ID, diff)
	}
}
