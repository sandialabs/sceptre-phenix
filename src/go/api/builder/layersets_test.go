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

	"phenix/store"
	"phenix/store/recordtest/memrecord"
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

	// Each "<" of a matcher takes 6 bytes in the record, which then holds
	// more than MaxMetadataBytes.
	large := testLayerSet("Large")
	large.Layers = nil

	for id := range builder.MaxLayers {
		matchers := make(map[string]string, builder.MaxLayerMatchers)
		for i := range builder.MaxLayerMatchers {
			matchers["spec.field"+strconv.Itoa(i)] = strings.Repeat("<", builder.MaxMatcherPatternBytes)
		}

		large.Layers = append(large.Layers, builder.Layer{
			ID: &id, Label: "Layer " + strconv.Itoa(id), IconKey: "", Icon: "", Color: "", Matchers: matchers,
		})
	}

	sets := h.store.Count(NamespaceLayerSets)

	var tooLarge *TooLargeError
	if _, err := h.service.CreateLayerSet(ctx, testActor, large); !errors.As(err, &tooLarge) {
		t.Errorf("CreateLayerSet(Large) error = %v, want a *TooLargeError", err)
	} else if tooLarge.What != kindLayerSet || tooLarge.Limit != MaxMetadataBytes {
		t.Errorf("CreateLayerSet(Large) refusal = %+v, want the %s record past %d bytes", tooLarge, kindLayerSet, MaxMetadataBytes)
	}

	if got := h.store.Count(NamespaceLayerSets); got != sets {
		t.Errorf("layer set records after CreateLayerSet(Large) = %d, want %d", got, sets)
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
		"set/user":    `{"id":"user","name":"x","layers":[{"id":1,"label":"a"}],"version":1,"createdBy":"a\nb"}`,
	} {
		if _, err := h.store.CreateRecord(NamespaceLayerSets, key, []byte(value)); err != nil {
			t.Fatalf("CreateRecord(%s) returned error: %v", key, err)
		}
	}

	sets, damaged, err := h.service.ListLayerSets(ctx)
	if err != nil {
		t.Fatalf("ListLayerSets returned error: %v", err)
	}

	if damaged != 6 {
		t.Errorf("ListLayerSets damaged = %d, want 6", damaged)
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

func TestLayerSetErrorMessages(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "stale",
			err:  &LayerSetStaleError{ID: "set-1", ETag: `"3"`},
			want: "builder: revision conflict: layer set set-1 changed after you last read it",
		},
		{
			name: "limit",
			err:  &LayerSetLimitError{Limit: MaxLayerSets},
			want: "builder: payload too large: the layer set library holds at most 200 sets",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLayerSetLibraryRefusesACanceledContext(t *testing.T) {
	h := newHarness(t)

	created, err := h.service.CreateLayerSet(context.Background(), testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	commits := h.store.Commits()

	for _, tt := range []struct {
		name string
		call func() error
	}{
		{name: "ListLayerSets", call: func() error { _, _, err := h.service.ListLayerSets(ctx); return err }},
		{name: "GetLayerSet", call: func() error { _, err := h.service.GetLayerSet(ctx, created.ID); return err }},
		{name: "CreateLayerSet", call: func() error {
			_, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Other"))

			return err
		}},
		{name: "UpdateLayerSet", call: func() error {
			_, err := h.service.UpdateLayerSet(ctx, testActor, created.ID, created.ETag(), testLayerSet("Other"))

			return err
		}},
		{name: "DeleteLayerSet", call: func() error { return h.service.DeleteLayerSet(ctx, created.ID, created.ETag()) }},
		{name: "DefaultLayerSet", call: func() error { _, err := h.service.DefaultLayerSet(ctx); return err }},
		{name: "SetDefaultLayerSet", call: func() error {
			_, err := h.service.SetDefaultLayerSet(ctx, created.ID, `"0"`)

			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, context.Canceled) {
				t.Errorf("%s with a canceled context = %v, want context.Canceled", tt.name, err)
			}
		})
	}

	if got := h.store.Commits(); got != commits {
		t.Errorf("store commits after the canceled calls = %d, want %d", got, commits)
	}
}

// TestLayerSetNotFoundShowsAShortID asserts a refusal repeats at most 80 bytes
// of an ID, cut at a whole character.
func TestLayerSetNotFoundShowsAShortID(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	for _, tt := range []struct {
		name, id, want string
	}{
		{name: "short", id: "a/b", want: "a/b"},
		{name: "long", id: strings.Repeat("a", MaxIDLength+1), want: strings.Repeat("a", 80) + "..."},
		{name: "three-byte characters", id: strings.Repeat("€", 40), want: strings.Repeat("€", 26) + "..."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, getErr := h.service.GetLayerSet(ctx, tt.id)

			for name, err := range map[string]error{
				"GetLayerSet":    getErr,
				"DeleteLayerSet": h.service.DeleteLayerSet(ctx, tt.id, `"1"`),
			} {
				var notFound *NotFoundError
				if !errors.As(err, &notFound) {
					t.Errorf("%s(%q) = %v, want a NotFoundError", name, tt.id, err)
				} else if diff := cmp.Diff(NotFoundError{Kind: kindLayerSet, ID: tt.want}, *notFound); diff != "" {
					t.Errorf("%s(%q) refusal mismatch (-want +got):\n%s", name, tt.id, diff)
				}
			}
		})
	}
}

func TestUpdateLayerSetRefusesBadRequests(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
	}

	for _, tt := range []struct {
		name, actor string
		content     builder.LayerSet
	}{
		{name: "no actor", actor: "", content: testLayerSet("Changed")},
		{name: "an actor with a control character", actor: "a\nb", content: testLayerSet("Changed")},
		{name: "no name", actor: testActor, content: testLayerSet("")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := h.service.UpdateLayerSet(ctx, tt.actor, created.ID, created.ETag(), tt.content); !errors.Is(
				err, ErrInvalid,
			) {
				t.Errorf("UpdateLayerSet(%s) = %v, want ErrInvalid", created.ID, err)
			}
		})
	}

	if got, err := h.service.GetLayerSet(ctx, created.ID); err != nil || got.Version != 1 {
		t.Errorf("GetLayerSet(%s) after the refusals = %+v, %v; want version 1", created.ID, got, err)
	}
}

// TestCreateLayerSetStoresNothingWhenRefused gives CreateLayerSet an ID it
// cannot use, or a store that refuses the write.
func TestCreateLayerSetStoresNothingWhenRefused(t *testing.T) {
	failure := errors.New("no entropy")

	for _, tt := range []struct {
		name     string
		id       func() (string, error)
		refuse   error
		count    error
		wantErr  error
		anyError bool
	}{
		{name: "the ID source fails", id: func() (string, error) { return "", failure }, wantErr: failure},
		{name: "an invalid ID", id: func() (string, error) { return "a/b", nil }, anyError: true},
		{name: "the ID of a built-in set", id: func() (string, error) { return builder.BuiltinPurdueLayerSetID, nil }, anyError: true},
		{name: "the store refuses", id: func() (string, error) { return "set-1", nil }, refuse: failure, wantErr: failure},
		{name: "the store cannot count the sets", id: func() (string, error) { return "set-1", nil }, count: failure, wantErr: failure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := memrecord.New()
			fake.BeforeCreate = func(string, string) error { return tt.refuse }

			faults := &cleanupFaultStore{
				Store: fake, failList: nil, failKeys: func(string) error { return tt.count }, failGet: nil, failTxGet: nil,
			}

			service, err := New(WithStore(faults), WithIDSource(tt.id))
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}

			_, err = service.CreateLayerSet(context.Background(), testActor, testLayerSet("Plant"))

			switch {
			case tt.anyError && err == nil:
				t.Error("CreateLayerSet(Plant) error = nil, want an error")
			case !tt.anyError && !errors.Is(err, tt.wantErr):
				t.Errorf("CreateLayerSet(Plant) error = %v, want %v", err, tt.wantErr)
			}

			if keys := fake.Keys(NamespaceLayerSets); len(keys) != 0 {
				t.Errorf("layer set records after a refused CreateLayerSet = %q, want none", keys)
			}
		})
	}
}

// TestLayerSetWritesThatFail makes every write of a set lose to another
// writer, or fail in the store. A lost write is tried again up to five times.
func TestLayerSetWritesThatFail(t *testing.T) {
	failure := errors.New("disk full")

	for _, tt := range []struct {
		name         string
		lose         bool
		wantErr      error
		wantAttempts int
	}{
		{name: "every write loses", lose: true, wantErr: ErrBusy, wantAttempts: maxLibraryAttempts},
		{name: "the store fails", lose: false, wantErr: failure, wantAttempts: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
			if err != nil {
				t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
			}

			key := layerSetKey(created.ID)
			attempts, writing := 0, false

			// interleave writes the record again unchanged, so its revision
			// changes and its entity tag does not.
			interleave := func(namespace, at string) error {
				if namespace != NamespaceLayerSets || at != key || writing {
					return nil
				}

				attempts++

				if !tt.lose {
					return failure
				}

				writing = true
				defer func() { writing = false }()

				record, err := h.store.GetRecord(namespace, at)
				if err != nil {
					return err
				}

				_, err = h.store.UpdateRecord(namespace, at, record.Value, record.Revision)

				return err
			}

			h.store.BeforeUpdate = interleave

			_, err = h.service.UpdateLayerSet(ctx, testPeer, created.ID, created.ETag(), testLayerSet("Changed"))
			if !errors.Is(err, tt.wantErr) || attempts != tt.wantAttempts {
				t.Errorf("UpdateLayerSet(%s) = %v after %d attempts, want %v after %d",
					created.ID, err, attempts, tt.wantErr, tt.wantAttempts)
			}

			h.store.BeforeUpdate = nil
			attempts = 0
			h.store.FailDelete = interleave

			err = h.service.DeleteLayerSet(ctx, created.ID, created.ETag())
			if !errors.Is(err, tt.wantErr) || attempts != tt.wantAttempts {
				t.Errorf("DeleteLayerSet(%s) = %v after %d attempts, want %v after %d",
					created.ID, err, attempts, tt.wantErr, tt.wantAttempts)
			}

			h.store.FailDelete = nil

			if got, err := h.service.GetLayerSet(ctx, created.ID); err != nil {
				t.Errorf("GetLayerSet(%s) after the failed writes returned error: %v", created.ID, err)
			} else if got.Name != "Plant" || got.Version != 1 {
				t.Errorf("GetLayerSet(%s) after the failed writes = %q at version %d, want Plant at version 1",
					created.ID, got.Name, got.Version)
			}
		})
	}
}

// TestSetDefaultLayerSetLosesToAnotherChoice makes another writer choose a
// default between the read and the write. The choice is refused with the tag
// of the other one, which stays.
func TestSetDefaultLayerSetLosesToAnotherChoice(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
	}

	var other store.Record

	writing := false
	h.store.BeforeCreate = func(namespace, at string) error {
		if namespace != NamespaceLayerSets || at != layerSetDefaultKey || writing {
			return nil
		}

		writing = true

		record, err := h.store.CreateRecord(namespace, at, []byte(`{"layerSet":"`+builder.BuiltinPurdueLayerSetID+`"}`))
		other = record

		return err
	}

	_, err = h.service.SetDefaultLayerSet(ctx, created.ID, `"0"`)

	var stale *LayerSetStaleError
	if !errors.As(err, &stale) {
		t.Fatalf("SetDefaultLayerSet(%s) racing another choice = %v, want a LayerSetStaleError", created.ID, err)
	}

	if diff := cmp.Diff(LayerSetStaleError{ID: layerSetDefaultKey, ETag: RevisionETag(other.Revision)}, *stale); diff != "" {
		t.Errorf("SetDefaultLayerSet(%s) racing another choice refusal mismatch (-want +got):\n%s", created.ID, diff)
	}

	want := LayerSetDefault{ID: builder.BuiltinPurdueLayerSetID, ETag: RevisionETag(other.Revision)}
	if got, err := h.service.DefaultLayerSet(ctx); err != nil || got != want {
		t.Errorf("DefaultLayerSet() after the lost choice = %+v, %v; want %+v, nil", got, err, want)
	}
}

// TestDefaultLayerSetStoreFailures fails a read or the write of the default
// layer set, or makes another writer change the choice before each write.
func TestDefaultLayerSetStoreFailures(t *testing.T) {
	ctx := context.Background()
	failure := errors.New("disk full")

	for _, tt := range []struct {
		name string
		// fail sets the store hooks once the set is the default.
		fail func(t *testing.T, h *testHarness, faults *cleanupFaultStore, set string)
		// clear removes the choice before fail runs.
		clear   bool
		wantErr error
	}{
		{
			name: "the default cannot be read",
			fail: func(_ *testing.T, _ *testHarness, faults *cleanupFaultStore, _ string) {
				faults.failGet = func(_, key string) error {
					if key == layerSetDefaultKey {
						return failure
					}

					return nil
				}
			},
			wantErr: failure,
		},
		{
			name: "the default cannot be written",
			fail: func(_ *testing.T, h *testHarness, _ *cleanupFaultStore, _ string) {
				h.store.BeforeUpdate = func(_, key string) error {
					if key == layerSetDefaultKey {
						return failure
					}

					return nil
				}
			},
			wantErr: failure,
		},
		{
			// Another writer clears the choice before each read and makes it
			// again before each write.
			name:  "every write loses",
			clear: true,
			fail: func(t *testing.T, h *testHarness, faults *cleanupFaultStore, set string) {
				t.Helper()

				faults.failGet = func(namespace, key string) error {
					if key == layerSetDefaultKey {
						_ = h.store.DeleteRecord(namespace, key, store.AnyRevision)
					}

					return nil
				}

				writing := false
				h.store.BeforeCreate = func(namespace, key string) error {
					if key != layerSetDefaultKey || writing {
						return nil
					}

					writing = true
					defer func() { writing = false }()

					_, err := h.store.CreateRecord(namespace, key, []byte(`{"layerSet":"`+set+`"}`))

					return err
				}
			},
			wantErr: ErrBusy,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			service, faults := newCleanupFaultService(t, h)

			created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
			if err != nil {
				t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
			}

			chosen, err := h.service.SetDefaultLayerSet(ctx, created.ID, RevisionETag(0))
			if err != nil {
				t.Fatalf("SetDefaultLayerSet(%s) returned error: %v", created.ID, err)
			}

			if tt.clear {
				if chosen, err = h.service.SetDefaultLayerSet(ctx, "", chosen.ETag); err != nil {
					t.Fatalf("SetDefaultLayerSet(\"\") returned error: %v", err)
				}
			}

			tt.fail(t, h, faults, builder.BuiltinPurdueLayerSetID)

			if got, err := service.SetDefaultLayerSet(ctx, builder.BuiltinPurdueLayerSetID, chosen.ETag); !errors.Is(err, tt.wantErr) {
				t.Errorf("SetDefaultLayerSet(%s) = %+v, %v; want %v", builder.BuiltinPurdueLayerSetID, got, err, tt.wantErr)
			}
		})
	}

	t.Run("the default set cannot be read", func(t *testing.T) {
		h := newHarness(t)
		service, faults := newCleanupFaultService(t, h)

		created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
		if err != nil {
			t.Fatalf("CreateLayerSet(Plant) returned error: %v", err)
		}

		if _, err := h.service.SetDefaultLayerSet(ctx, created.ID, RevisionETag(0)); err != nil {
			t.Fatalf("SetDefaultLayerSet(%s) returned error: %v", created.ID, err)
		}

		faults.failGet = func(_, key string) error {
			if key == layerSetKey(created.ID) {
				return failure
			}

			return nil
		}

		if got, err := service.DefaultLayerSet(ctx); !errors.Is(err, failure) || got != (LayerSetDefault{}) {
			t.Errorf("DefaultLayerSet() = %+v, %v; want no choice and %v", got, err, failure)
		}
	})
}
