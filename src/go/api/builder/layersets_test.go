package builder

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

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

func TestLayerSetLibraryRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant levels"))
	if err != nil {
		t.Fatalf("CreateLayerSet returned error: %v", err)
	}

	if created.ID == "" || created.Version != 1 || created.ETag() != `"1"` || created.CreatedBy != testActor ||
		created.UpdatedBy != testActor || created.Created.IsZero() || !created.Created.Equal(created.Updated) ||
		created.Builtin() {
		t.Fatalf("created set %+v", created)
	}

	got, err := h.service.GetLayerSet(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetLayerSet returned error: %v", err)
	}

	if !sameLayerSet(&got.LayerSet, &created.LayerSet) || got.Version != 1 || got.Revision != created.Revision {
		t.Fatalf("GetLayerSet returned %+v, want %+v", got, created)
	}

	change := testLayerSet("Plant levels, changed")

	updated, err := h.service.UpdateLayerSet(ctx, testPeer, created.ID, created.ETag(), change)
	if err != nil {
		t.Fatalf("UpdateLayerSet returned error: %v", err)
	}

	if updated.Version != 2 || updated.Name != change.Name || updated.UpdatedBy != testPeer ||
		updated.CreatedBy != testActor || !updated.Created.Equal(created.Created) || !updated.Updated.After(created.Updated) {
		t.Fatalf("updated set %+v", updated)
	}

	// The same content again writes nothing.
	again, err := h.service.UpdateLayerSet(ctx, testActor, created.ID, updated.ETag(), change)
	if err != nil || again.Version != 2 || again.Revision != updated.Revision || again.UpdatedBy != testPeer {
		t.Fatalf("an update with the same content returned %+v, %v", again, err)
	}
}

func TestLayerSetLibraryRefusesStaleAndDeletedSets(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant levels"))
	if err != nil {
		t.Fatalf("CreateLayerSet returned error: %v", err)
	}

	change := testLayerSet("Plant levels, changed")

	if _, err := h.service.UpdateLayerSet(ctx, testPeer, created.ID, created.ETag(), change); err != nil {
		t.Fatalf("UpdateLayerSet returned error: %v", err)
	}

	// A stale entity tag is refused with the current one.
	_, err = h.service.UpdateLayerSet(ctx, testActor, created.ID, created.ETag(), testLayerSet("Other"))

	var stale *LayerSetStaleError
	if !errors.As(err, &stale) || stale.ETag != `"2"` || !errors.Is(err, ErrConflict) {
		t.Fatalf("a stale update returned %v, want a LayerSetStaleError naming \"2\"", err)
	}

	sets, damaged, err := h.service.ListLayerSets(ctx)
	if err != nil || damaged != 0 {
		t.Fatalf("ListLayerSets returned %d damaged, %v", damaged, err)
	}

	if len(sets) != 2 || sets[0].ID != builder.BuiltinPurdueLayerSetID || sets[1].ID != created.ID {
		t.Fatalf("ListLayerSets returned %+v, want the built-in set, then the new set", sets)
	}

	if err := h.service.DeleteLayerSet(ctx, created.ID); err != nil {
		t.Fatalf("DeleteLayerSet returned error: %v", err)
	}

	if _, err := h.service.GetLayerSet(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLayerSet after delete returned %v, want ErrNotFound", err)
	}

	if err := h.service.DeleteLayerSet(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a second delete returned %v, want ErrNotFound", err)
	}

	if _, err := h.service.UpdateLayerSet(ctx, testActor, created.ID, `"2"`, change); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an update after delete returned %v, want ErrNotFound", err)
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

	if got, want := strings.Join(names, ","), "Purdue Model,Alpha,alpha,beta,Gamma"; got != want {
		t.Fatalf("names %s, want %s", got, want)
	}
}

func TestBuiltinLayerSetIsReadOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	purdue, err := h.service.GetLayerSet(ctx, builder.BuiltinPurdueLayerSetID)
	if err != nil {
		t.Fatalf("GetLayerSet(built-in) returned error: %v", err)
	}

	if !purdue.Builtin() || purdue.Version != 1 || purdue.Name != "Purdue Model" || len(purdue.Layers) != 7 ||
		!purdue.Created.IsZero() || purdue.CreatedBy != "" {
		t.Fatalf("the built-in set is %+v", purdue)
	}

	if _, err := h.service.UpdateLayerSet(ctx, testActor, purdue.ID, purdue.ETag(), testLayerSet("Mine")); !errors.Is(
		err, ErrForbidden,
	) {
		t.Fatalf("UpdateLayerSet(built-in) returned %v, want ErrForbidden", err)
	}

	if err := h.service.DeleteLayerSet(ctx, purdue.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("DeleteLayerSet(built-in) returned %v, want ErrForbidden", err)
	}

	if h.store.Count(NamespaceLayerSets) != 0 {
		t.Fatalf("reading the built-in set stored %v", h.store.Keys(NamespaceLayerSets))
	}
}

func TestLayerSetLibraryRefuses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	invalid := testLayerSet("")

	_, err := h.service.CreateLayerSet(ctx, testActor, invalid)

	var refused *builder.LayerSetError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &refused) || refused.Issues[0].Code != builder.CodeLayerSetNameRequired {
		t.Fatalf("CreateLayerSet(no name) returned %v, want a LayerSetError that matches ErrInvalid", err)
	}

	if _, err := h.service.CreateLayerSet(ctx, "", testLayerSet("Plant")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateLayerSet(no actor) returned %v, want ErrInvalid", err)
	}

	for _, id := range []string{"", "missing", "a/b", "builtin:other", strings.Repeat("a", MaxIDLength+1)} {
		if _, err := h.service.GetLayerSet(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetLayerSet(%q) returned %v, want ErrNotFound", id, err)
		}
	}

	if h.store.Count(NamespaceLayerSets) != 0 {
		t.Fatalf("a refusal stored %v", h.store.Keys(NamespaceLayerSets))
	}

	// An empty list of matchers is stored as none.
	set := testLayerSet("Plant")
	set.Layers[1].Matchers = map[string]string{}

	created, err := h.service.CreateLayerSet(ctx, testActor, set)
	if err != nil || created.Layers[1].Matchers != nil {
		t.Fatalf("CreateLayerSet returned %+v, %v, want no matchers on the second layer", created, err)
	}
}

func TestLayerSetLibraryHoldsAtMost200Sets(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	for i := range MaxLayerSets {
		if _, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Set "+strconv.Itoa(i))); err != nil {
			t.Fatalf("CreateLayerSet %d returned error: %v", i, err)
		}
	}

	_, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("One more"))

	var limit *LayerSetLimitError
	if !errors.As(err, &limit) || !errors.Is(err, ErrTooLarge) || limit.Limit != MaxLayerSets {
		t.Fatalf("CreateLayerSet past the limit returned %v, want a LayerSetLimitError", err)
	}
}

func TestLayerSetLibrarySkipsDamagedRecords(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet returned error: %v", err)
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
	if err != nil || damaged != 5 || len(sets) != 2 || sets[1].ID != created.ID {
		t.Fatalf("ListLayerSets returned %+v, %d damaged, %v", sets, damaged, err)
	}

	if _, err := h.service.GetLayerSet(ctx, "broken"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("GetLayerSet(damaged) returned %v, want ErrCorrupt", err)
	}
}

func TestDefaultLayerSet(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if id, err := h.service.DefaultLayerSet(ctx); err != nil || id != "" {
		t.Fatalf("DefaultLayerSet with no choice returned %q, %v", id, err)
	}

	if err := h.service.SetDefaultLayerSet(ctx, builder.BuiltinPurdueLayerSetID); err != nil {
		t.Fatalf("SetDefaultLayerSet(built-in) returned error: %v", err)
	}

	if id, err := h.service.DefaultLayerSet(ctx); err != nil || id != builder.BuiltinPurdueLayerSetID {
		t.Fatalf("DefaultLayerSet returned %q, %v", id, err)
	}

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet returned error: %v", err)
	}

	// A second choice replaces the first.
	if err := h.service.SetDefaultLayerSet(ctx, created.ID); err != nil {
		t.Fatalf("SetDefaultLayerSet again returned error: %v", err)
	}

	if id, _ := h.service.DefaultLayerSet(ctx); id != created.ID {
		t.Fatalf("DefaultLayerSet after a second choice returned %q", id)
	}

	if err := h.service.SetDefaultLayerSet(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDefaultLayerSet(missing) returned %v, want ErrNotFound", err)
	}

	// A deleted set is no choice.
	if err := h.service.DeleteLayerSet(ctx, created.ID); err != nil {
		t.Fatalf("DeleteLayerSet returned error: %v", err)
	}

	if id, err := h.service.DefaultLayerSet(ctx); err != nil || id != "" {
		t.Fatalf("DefaultLayerSet after the delete returned %q, %v", id, err)
	}

	if keys := h.store.Keys(NamespaceLayerSets); len(keys) != 1 || keys[0] != layerSetDefaultKey {
		t.Fatalf("the namespace holds %v, want only the default record", keys)
	}

	// Clearing removes the record, and clearing twice is harmless.
	for range 2 {
		if err := h.service.SetDefaultLayerSet(ctx, ""); err != nil {
			t.Fatalf("SetDefaultLayerSet(none) returned error: %v", err)
		}
	}

	if keys := h.store.Keys(NamespaceLayerSets); len(keys) != 0 {
		t.Fatalf("the namespace holds %v after clearing, want nothing", keys)
	}

	// A record that does not decode is no choice.
	if _, err := h.store.CreateRecord(NamespaceLayerSets, layerSetDefaultKey, []byte(`{"layerSet":`)); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	if id, err := h.service.DefaultLayerSet(ctx); err != nil || id != "" {
		t.Fatalf("DefaultLayerSet of an unreadable record returned %q, %v", id, err)
	}
}

func TestUpdateLayerSetRetriesAfterAnotherWrite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	created, err := h.service.CreateLayerSet(ctx, testActor, testLayerSet("Plant"))
	if err != nil {
		t.Fatalf("CreateLayerSet returned error: %v", err)
	}

	key := layerSetKey(created.ID)
	interleaved := false

	// Another writer stores the same record between the read and the write; the
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
	if err != nil || !interleaved || updated.Version != 2 || updated.Name != "Changed" {
		t.Fatalf("UpdateLayerSet returned %+v, %v (interleaved %v)", updated, err, interleaved)
	}
}
