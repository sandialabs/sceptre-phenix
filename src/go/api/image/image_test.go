package image_test

import (
	"os"
	"slices"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
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
