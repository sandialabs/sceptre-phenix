package image_test

import (
	"os"
	"slices"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/mitchellh/mapstructure"

	"phenix/api/image"
	"phenix/store"
	v1 "phenix/types/version/v1"
)

func TestUpdateDoesNotDuplicateScriptOrder(t *testing.T) {
	scriptPath := t.TempDir() + "/post-build.sh"

	if err := os.WriteFile(scriptPath, []byte("updated script"), 0o600); err != nil {
		t.Fatalf("writing script: %v", err)
	}

	config := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Image",
		Metadata: store.ConfigMetadata{Name: "test-image"},
		Spec: structs.MapDefaultCase(v1.Image{
			Scripts: map[string]string{
				"DEFAULT_SCRIPT": "default script",
				scriptPath:       "old script",
			},
			ScriptOrder: []string{"DEFAULT_SCRIPT", scriptPath, scriptPath},
		}, structs.CASESNAKE),
	}

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockStore := store.NewMockStore(ctrl)
	mockStore.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = config

		return nil
	}).Times(2)
	mockStore.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		config = *c

		return nil
	}).Times(2)

	originalStore := store.DefaultStore
	store.DefaultStore = mockStore //nolint:reassign // install test double
	t.Cleanup(func() {
		store.DefaultStore = originalStore //nolint:reassign // restore test double
	})

	for range 2 {
		if err := image.Update("test-image"); err != nil {
			t.Fatalf("updating image: %v", err)
		}
	}

	var updated v1.Image
	if err := mapstructure.Decode(config.Spec, &updated); err != nil {
		t.Fatalf("decoding updated image: %v", err)
	}

	expectedOrder := []string{"DEFAULT_SCRIPT", scriptPath}
	if !slices.Equal(updated.ScriptOrder, expectedOrder) {
		t.Fatalf("script order = %v, want %v", updated.ScriptOrder, expectedOrder)
	}

	if got := updated.Scripts[scriptPath]; got != "updated script" {
		t.Fatalf("script contents = %q, want %q", got, "updated script")
	}
}

// Update rereads the scripts found on disk and keeps the order they run in.
func TestUpdateKeepsScriptOrder(t *testing.T) {
	dir := t.TempDir()
	first, last, extra := dir+"/first.sh", dir+"/last.sh", dir+"/extra.sh"

	for _, script := range []string{first, last, extra} {
		if err := os.WriteFile(script, []byte("new "+script), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", script, err)
		}
	}

	config := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Image",
		Metadata: store.ConfigMetadata{Name: "test-image"},
		Spec: structs.MapDefaultCase(v1.Image{
			Scripts: map[string]string{
				first:            "old",
				"DEFAULT_SCRIPT": "default script",
				last:             "old",
				// on disk but not in the order: it never ran, and now runs last
				extra: "old",
			},
			ScriptOrder: []string{first, "DEFAULT_SCRIPT", last},
		}, structs.CASESNAKE),
	}

	ctrl := gomock.NewController(t)
	mockStore := store.NewMockStore(ctrl)
	mockStore.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = config

		return nil
	})
	mockStore.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		config = *c

		return nil
	})

	originalStore := store.DefaultStore
	store.DefaultStore = mockStore //nolint:reassign // install test double
	t.Cleanup(func() {
		store.DefaultStore = originalStore //nolint:reassign // restore test double
	})

	if err := image.Update("test-image"); err != nil {
		t.Fatalf("image.Update(test-image) error = %v", err)
	}

	var got v1.Image
	if err := mapstructure.Decode(config.Spec, &got); err != nil {
		t.Fatalf("mapstructure.Decode(updated spec) error = %v", err)
	}

	want := v1.Image{
		Scripts: map[string]string{
			first:            "new " + first,
			"DEFAULT_SCRIPT": "default script",
			last:             "new " + last,
			extra:            "new " + extra,
		},
		ScriptOrder: []string{first, "DEFAULT_SCRIPT", last, extra},
	}
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.Update(test-image) image mismatch (-want +got):\n%s", diff)
	}
}
