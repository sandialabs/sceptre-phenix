package image_test

import (
	"errors"
	"io/fs"
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

type setupImageTest struct {
	name           string
	img            v1.Image
	wantMirror     string
	wantComponents []string
	wantPackages   []string
	wantNoPackages []string
}

func TestSetupImage(t *testing.T) {
	const ubuntuMirror = "http://us.archive.ubuntu.com/ubuntu"

	tests := []setupImageTest{
		{
			name:           "ubuntu keeps default mirror and ubuntu components",
			img:            v1.Image{Variant: "minbase", Release: "noble", Mirror: ubuntuMirror},
			wantMirror:     ubuntuMirror,
			wantComponents: image.UbuntuComponents,
			wantPackages:   []string{"linux-image-generic", "curl"},
			wantNoPackages: []string{"linux-image-amd64"},
		},
		{
			name:           "debian trixie swaps mirror and uses debian components",
			img:            v1.Image{Variant: "minbase", Release: "trixie", Mirror: ubuntuMirror},
			wantMirror:     "http://ftp.us.debian.org/debian",
			wantComponents: image.DebianComponents,
			wantPackages:   []string{"linux-image-amd64"},
			wantNoPackages: []string{"linux-image-generic"},
		},
		{
			name:           "bare kali swaps mirror and uses kali components",
			img:            v1.Image{Variant: "minbase", Release: "kali", Mirror: ubuntuMirror},
			wantMirror:     "http://http.kali.org/kali",
			wantComponents: image.KaliComponents,
			wantPackages:   []string{"linux-image-amd64", "default-jdk"},
		},
		{
			name: "explicit mirror and components are preserved",
			img: v1.Image{
				Variant:    "minbase",
				Release:    "forky",
				Mirror:     "http://mirror/debian",
				Components: []string{"main"},
			},
			wantMirror:     "http://mirror/debian",
			wantComponents: []string{"main"},
			wantPackages:   []string{"linux-image-amd64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := tt.img

			if err := image.SetupImage(&img); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			checkSetupImage(t, tt, img)
		})
	}
}

func checkSetupImage(t *testing.T, tt setupImageTest, img v1.Image) {
	t.Helper()

	if tt.wantMirror != "" && img.Mirror != tt.wantMirror {
		t.Errorf("mirror = %q, want %q", img.Mirror, tt.wantMirror)
	}

	if tt.wantComponents != nil && !slices.Equal(img.Components, tt.wantComponents) {
		t.Errorf("components = %v, want %v", img.Components, tt.wantComponents)
	}

	for _, pkg := range tt.wantPackages {
		if !slices.Contains(img.Packages, pkg) {
			t.Errorf("missing package %q in %v", pkg, img.Packages)
		}
	}

	for _, pkg := range tt.wantNoPackages {
		if slices.Contains(img.Packages, pkg) {
			t.Errorf("unexpected package %q in %v", pkg, img.Packages)
		}
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
			// REMOVED_SCRIPT has no script, so the update drops it from the order
			ScriptOrder: []string{first, "DEFAULT_SCRIPT", "REMOVED_SCRIPT", last},
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

// Update returns a stat error other than "does not exist" and keeps the store unchanged.
func TestUpdateReturnsStatError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	locked := t.TempDir() + "/locked"
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", locked, err)
	}

	script := locked + "/script.sh"
	if err := os.WriteFile(script, []byte("new"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", script, err)
	}

	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("os.Chmod(%q) error = %v", locked, err)
	}

	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	config := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Image",
		Metadata: store.ConfigMetadata{Name: "test-image"},
		Spec: structs.MapDefaultCase(v1.Image{
			Scripts:     map[string]string{script: "old"},
			ScriptOrder: []string{script},
		}, structs.CASESNAKE),
	}

	ctrl := gomock.NewController(t)
	mockStore := store.NewMockStore(ctrl)
	mockStore.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = config

		return nil
	})

	originalStore := store.DefaultStore
	store.DefaultStore = mockStore //nolint:reassign // install test double
	t.Cleanup(func() {
		store.DefaultStore = originalStore //nolint:reassign // restore test double
	})

	err := image.Update("test-image")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("image.Update(test-image) error = %v, want %v", err, fs.ErrPermission)
	}
}

// A script name under a regular file, which stat reports as not a directory, is
// not a script file, so the update keeps its stored contents.
func TestUpdateKeepsAScriptUnderARegularFile(t *testing.T) {
	file := t.TempDir() + "/file"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", file, err)
	}

	script := file + "/script.sh"
	config := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Image",
		Metadata: store.ConfigMetadata{Name: "test-image"},
		Spec: structs.MapDefaultCase(v1.Image{
			Scripts:     map[string]string{script: "stored"},
			ScriptOrder: []string{script},
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

	want := v1.Image{Scripts: map[string]string{script: "stored"}, ScriptOrder: []string{script}}
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.Update(test-image) image mismatch (-want +got):\n%s", diff)
	}
}
