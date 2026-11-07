package image_test

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/mitchellh/mapstructure"
	"pgregory.net/rapid"

	"phenix/api/image"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
)

const oldCreated = "2020-01-01T00:00:00Z"

// storeImage puts img in the store as the image config name, with spec as its
// spec when spec is not nil, and created as its created time.
func storeImage(t *testing.T, name string, img v1.Image, spec map[string]any, created string) {
	t.Helper()

	if spec == nil {
		spec = structs.MapDefaultCase(img, structs.CASESNAKE)
	}

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Image",
		Metadata: store.ConfigMetadata{Name: name, Created: created},
		Spec:     spec,
	}

	if err := store.Create(&c); err != nil {
		t.Fatalf("store.Create(image/%s) error = %v", name, err)
	}
}

// storedImage returns the image config name from the store and its decoded spec.
func storedImage(t *testing.T, name string) (*store.Config, v1.Image) {
	t.Helper()

	c, err := store.NewConfig("image/" + name)
	if err != nil {
		t.Fatalf("store.NewConfig(image/%s) error = %v", name, err)
	}

	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(image/%s) error = %v", name, err)
	}

	var img v1.Image
	if err := mapstructure.Decode(c.Spec, &img); err != nil {
		t.Fatalf("mapstructure.Decode(image/%s spec) error = %v", name, err)
	}

	return c, img
}

// writeScript writes contents to a new script file and returns its path.
func writeScript(t *testing.T, name, contents string) string {
	t.Helper()

	p := t.TempDir() + "/" + name
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", p, err)
	}

	return p
}

// Create stores the image that SetupImage completes, as an Image config with
// the image name.
//
//nolint:paralleltest // replaces the phenix store
func TestCreate(t *testing.T) {
	storetest.UseBolt(t)

	in := v1.Image{
		Name: "web", Variant: "minbase", Release: "bookworm", Format: v1.FormatQcow2,
		Size: "5G", Mirror: "http://us.archive.ubuntu.com/ubuntu", Packages: []string{"git"},
	}

	img := in
	if err := image.Create(&img); err != nil {
		t.Fatalf("image.Create(%+v) error = %v", in, err)
	}

	want := in
	if err := image.SetupImage(&want); err != nil {
		t.Fatalf("image.SetupImage(%+v) error = %v", in, err)
	}

	c, got := storedImage(t, "web")
	if c.Version != "phenix.sandia.gov/v1" || c.Kind != "Image" {
		t.Errorf("image.Create(web) stored %s %s, want phenix.sandia.gov/v1 Image", c.Version, c.Kind)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("image.Create(web) stored spec mismatch (-want +got):\n%s", diff)
	}

	again := in
	if err := image.Create(&again); !errors.Is(err, store.ErrExist) {
		t.Errorf("image.Create(web) again error = %v, want %v", err, store.ErrExist)
	}
}

// Create refuses an image with no name, a size that is not correct or a
// variant it does not know, and stores nothing.
//
//nolint:paralleltest // replaces the phenix store
func TestCreateErrors(t *testing.T) {
	storetest.UseBolt(t)

	for _, img := range []v1.Image{
		{Variant: "minbase", Release: "noble", Size: "10G"},
		{Name: "bad", Variant: "maxgui", Release: "noble", Size: "10G"},
	} {
		in := img
		if err := image.Create(&img); err == nil {
			t.Errorf("image.Create(%+v) error = nil, want an error", in)
		}
	}

	for _, size := range []string{"", "M", "10", "10g", "-5G"} {
		img := v1.Image{Name: "bad", Variant: "minbase", Release: "noble", Size: size}
		if err := image.Create(&img); !errors.Is(err, image.ErrInvalidSize) {
			t.Errorf("image.Create(size %q) error = %v, want %v", size, err, image.ErrInvalidSize)
		}
	}

	if got, err := image.List(); err != nil || len(got) != 0 {
		t.Errorf("image.List() after failed creates = %v, %v, want no images", got, err)
	}
}

// List returns every stored image with its decoded spec.
//
//nolint:paralleltest // replaces the phenix store
func TestList(t *testing.T) {
	storetest.UseBolt(t)

	if got, err := image.List(); err != nil || len(got) != 0 {
		t.Errorf("image.List() of an empty store = %v, %v, want no images and no error", got, err)
	}

	want := map[string]v1.Image{
		"a": {Name: "a", Release: "noble", Packages: []string{"git"}},
		"b": {Name: "b", Release: "bookworm", Overlays: []string{"/overlay"}},
	}

	for name, img := range want {
		storeImage(t, name, img, nil, "")
	}

	images, err := image.List()
	if err != nil {
		t.Fatalf("image.List() error = %v", err)
	}

	got := make(map[string]v1.Image)
	for _, img := range images {
		got[img.Metadata.Name] = *img.Spec
	}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.List() mismatch (-want +got):\n%s", diff)
	}
}

// List returns an error for a stored spec that does not decode as an image.
//
//nolint:paralleltest // replaces the phenix store
func TestListBadSpec(t *testing.T) {
	storetest.UseBolt(t)
	storeImage(t, "bad", v1.Image{}, map[string]any{"packages": 5}, "")

	if got, err := image.List(); err == nil {
		t.Errorf("image.List() = %v, nil, want a decode error", got)
	}
}

// CreateFromConfig stores a new image that has the source spec, the given
// additions, the new name and a new created time. The source does not change.
//
//nolint:paralleltest // replaces the phenix store
func TestCreateFromConfig(t *testing.T) {
	storetest.UseBolt(t)

	script := writeScript(t, "extra.sh", "echo extra")
	src := v1.Image{
		Name: "base", Release: "noble", Packages: []string{"vim"}, Overlays: []string{"/o1"},
		Scripts: map[string]string{"FIRST": "echo first"}, ScriptOrder: []string{"FIRST"},
	}
	storeImage(t, "base", src, nil, oldCreated)

	if err := image.CreateFromConfig("base", "copy", []string{"/o2"}, []string{"git"}, []string{script}); err != nil {
		t.Fatalf("image.CreateFromConfig(base, copy) error = %v", err)
	}

	c, got := storedImage(t, "copy")
	want := v1.Image{
		Name: "copy", Release: "noble", Packages: []string{"vim", "git"}, Overlays: []string{"/o1", "/o2"},
		Scripts:     map[string]string{"FIRST": "echo first", script: "echo extra"},
		ScriptOrder: []string{"FIRST", script},
	}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.CreateFromConfig(base, copy) spec mismatch (-want +got):\n%s", diff)
	}

	if c.Metadata.Created == oldCreated || c.Metadata.Created == "" {
		t.Errorf("image.CreateFromConfig(base, copy) created = %q, want a new time", c.Metadata.Created)
	}

	srcConfig, gotSrc := storedImage(t, "base")
	if diff := cmp.Diff(src, gotSrc, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.CreateFromConfig(base, copy) changed the source (-want +got):\n%s", diff)
	}

	if srcConfig.Metadata.Created != oldCreated {
		t.Errorf("source created = %q, want %q", srcConfig.Metadata.Created, oldCreated)
	}
}

// CreateFromConfig returns an error for a missing source, a name that is in
// use or a script that it cannot read, and stores no new image.
//
//nolint:paralleltest // replaces the phenix store
func TestCreateFromConfigErrors(t *testing.T) {
	storetest.UseBolt(t)
	storeImage(t, "base", v1.Image{Release: "noble"}, nil, "")
	storeImage(t, "taken", v1.Image{Release: "jammy"}, nil, "")

	missing := t.TempDir() + "/missing.sh"

	tests := []struct {
		name, src, saveas string
		scripts           []string
		wantErr           error
	}{
		{name: "missing source", src: "nope", saveas: "new1", wantErr: store.ErrNotExist},
		{name: "name in use", src: "base", saveas: "taken", wantErr: store.ErrExist},
		{name: "missing script", src: "base", saveas: "new2", scripts: []string{missing}, wantErr: fs.ErrNotExist},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := image.CreateFromConfig(tt.src, tt.saveas, nil, nil, tt.scripts)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("image.CreateFromConfig(%s, %s) error = %v, want %v", tt.src, tt.saveas, err, tt.wantErr)
			}
		})
	}

	images, err := image.List()
	if err != nil {
		t.Fatalf("image.List() error = %v", err)
	}

	if len(images) != 2 {
		t.Errorf("image.List() after failed copies has %d images, want 2", len(images))
	}

	if _, img := storedImage(t, "taken"); img.Release != "jammy" {
		t.Errorf("image taken release = %q after a failed copy over it, want jammy", img.Release)
	}
}

// Append adds overlays, packages and scripts to a stored image. A script that
// is added again gets its new contents and keeps its one place in the order.
//
//nolint:paralleltest // replaces the phenix store
func TestAppend(t *testing.T) {
	storetest.UseBolt(t)

	script := writeScript(t, "s.sh", "echo one")
	storeImage(t, "img", v1.Image{
		Packages: []string{"vim"}, Scripts: map[string]string{"FIRST": "first"}, ScriptOrder: []string{"FIRST"},
	}, nil, "")

	if err := image.Append("img", []string{"/o"}, []string{"git", "curl"}, []string{script}); err != nil {
		t.Fatalf("image.Append(img) error = %v", err)
	}

	if err := os.WriteFile(script, []byte("echo two"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", script, err)
	}

	if err := image.Append("img", nil, nil, []string{script}); err != nil {
		t.Fatalf("image.Append(img) again error = %v", err)
	}

	_, got := storedImage(t, "img")
	want := v1.Image{
		Packages: []string{"vim", "git", "curl"}, Overlays: []string{"/o"},
		Scripts:     map[string]string{"FIRST": "first", script: "echo two"},
		ScriptOrder: []string{"FIRST", script},
	}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.Append(img) twice spec mismatch (-want +got):\n%s", diff)
	}
}

// Append adds a script to a stored spec that has no scripts at all, as a
// config file without a scripts key gives.
//
//nolint:paralleltest // replaces the phenix store
func TestAppendToSpecWithoutScripts(t *testing.T) {
	storetest.UseBolt(t)

	script := writeScript(t, "s.sh", "echo hi")
	storeImage(t, "img", v1.Image{}, map[string]any{"release": "noble"}, "")

	if err := image.Append("img", nil, nil, []string{script}); err != nil {
		t.Fatalf("image.Append(img, %s) error = %v", script, err)
	}

	_, got := storedImage(t, "img")
	want := v1.Image{Release: "noble", Scripts: map[string]string{script: "echo hi"}, ScriptOrder: []string{script}}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.Append(img, %s) spec mismatch (-want +got):\n%s", script, diff)
	}
}

// Append returns an error for a missing image or a script that it cannot read,
// and keeps the stored image.
//
//nolint:paralleltest // replaces the phenix store
func TestAppendErrors(t *testing.T) {
	storetest.UseBolt(t)

	before := v1.Image{Packages: []string{"vim"}}
	storeImage(t, "img", before, nil, "")

	if err := image.Append("nope", nil, []string{"git"}, nil); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("image.Append(nope) error = %v, want %v", err, store.ErrNotExist)
	}

	missing := t.TempDir() + "/missing.sh"
	if err := image.Append("img", nil, []string{"git"}, []string{missing}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("image.Append(img, %s) error = %v, want %v", missing, err, fs.ErrNotExist)
	}

	if _, got := storedImage(t, "img"); !cmp.Equal(before, got, cmpopts.EquateEmpty()) {
		t.Errorf("image after failed appends = %+v, want %+v", got, before)
	}
}

// Remove takes every copy of the given overlays and packages and the given
// scripts out of a stored image, and takes the scripts out of the order too.
//
//nolint:paralleltest // replaces the phenix store
func TestRemove(t *testing.T) {
	storetest.UseBolt(t)

	storeImage(t, "img", v1.Image{
		Overlays: []string{"/a", "/b", "/a", "/c"}, Packages: []string{"x", "y", "x"},
		Scripts:     map[string]string{"S1": "one", "S2": "two"},
		ScriptOrder: []string{"S1", "S2"},
	}, nil, "")

	if err := image.Remove("img", []string{"/a"}, []string{"x"}, []string{"S1", "NOT_THERE"}); err != nil {
		t.Fatalf("image.Remove(img) error = %v", err)
	}

	_, got := storedImage(t, "img")
	want := v1.Image{
		Overlays: []string{"/b", "/c"}, Packages: []string{"y"},
		Scripts: map[string]string{"S2": "two"}, ScriptOrder: []string{"S2"},
	}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.Remove(img) spec mismatch (-want +got):\n%s", diff)
	}

	if err := image.Remove("nope", nil, []string{"x"}, nil); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("image.Remove(nope) error = %v, want %v", err, store.ErrNotExist)
	}
}

// A script that is removed and then added again runs once.
//
//nolint:paralleltest // replaces the phenix store
func TestRemoveThenAppendScriptRunsOnce(t *testing.T) {
	storetest.UseBolt(t)

	script := writeScript(t, "s.sh", "echo once")
	storeImage(t, "img", v1.Image{
		Scripts: map[string]string{script: "echo once", "LAST": "last"}, ScriptOrder: []string{script, "LAST"},
	}, nil, "")

	if err := image.Remove("img", nil, nil, []string{script}); err != nil {
		t.Fatalf("image.Remove(img, %s) error = %v", script, err)
	}

	if err := image.Append("img", nil, nil, []string{script}); err != nil {
		t.Fatalf("image.Append(img, %s) error = %v", script, err)
	}

	_, got := storedImage(t, "img")
	if want := []string{"LAST", script}; !slices.Equal(got.ScriptOrder, want) {
		t.Errorf("script order after remove and append = %q, want %q", got.ScriptOrder, want)
	}

	if want := "      last\n      echo once"; got.PostBuild() != want {
		t.Errorf("PostBuild() = %q, want %q", got.PostBuild(), want)
	}
}

// For any overlays and packages, Remove keeps, in order, only those that are
// not named, and keeps the others as they were.
//
//nolint:paralleltest // replaces the phenix store
func TestRemoveProperty(t *testing.T) {
	storetest.UseBolt(t)

	var n int

	rapid.Check(t, func(rt *rapid.T) {
		names := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "b", "c", "d"}), 0, 8)
		overlays, packages := names.Draw(rt, "overlays"), names.Draw(rt, "packages")
		drop := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "b", "e"}), 0, 3)
		dropOverlays, dropPackages := drop.Draw(rt, "dropOverlays"), drop.Draw(rt, "dropPackages")

		n++
		name := fmt.Sprintf("img%d", n)
		storeImage(t, name, v1.Image{Overlays: overlays, Packages: packages, Release: "noble"}, nil, "")

		if err := image.Remove(name, dropOverlays, dropPackages, nil); err != nil {
			rt.Fatalf("image.Remove(%s) error = %v", name, err)
		}

		keep := func(all, drop []string) []string {
			var out []string

			for _, s := range all {
				if !slices.Contains(drop, s) {
					out = append(out, s)
				}
			}

			return out
		}

		_, got := storedImage(t, name)
		want := v1.Image{Release: "noble", Overlays: keep(overlays, dropOverlays), Packages: keep(packages, dropPackages)}

		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			rt.Errorf("image.Remove(%s, %q, %q) mismatch (-want +got):\n%s", name, dropOverlays, dropPackages, diff)
		}
	})
}

// Append reads an HTTP(S) script from its URL. It refuses a script URL with
// another scheme, a server that it cannot reach, or an HTTP error status.
//
//nolint:paralleltest // replaces the phenix store
func TestAppendScriptFromURL(t *testing.T) {
	storetest.UseBolt(t)
	storeImage(t, "img", v1.Image{Release: "noble"}, nil, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.sh" {
			http.NotFound(w, r)

			return
		}

		_, _ = io.WriteString(w, "echo from "+r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	script := srv.URL + "/post.sh"
	if err := image.Append("img", nil, nil, []string{script}); err != nil {
		t.Fatalf("image.Append(img, %s) error = %v", script, err)
	}

	_, got := storedImage(t, "img")
	want := v1.Image{Release: "noble", Scripts: map[string]string{script: "echo from /post.sh"}, ScriptOrder: []string{script}}

	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("image.Append(img, %s) spec mismatch (-want +got):\n%s", script, diff)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	for _, bad := range []string{"ftp://host/s.sh", closed.URL + "/s.sh", "http://[::1", srv.URL + "/missing.sh"} {
		if err := image.Append("img", nil, nil, []string{bad}); err == nil {
			t.Errorf("image.Append(img, %s) error = nil, want an error", bad)
		}
	}

	if _, after := storedImage(t, "img"); !cmp.Equal(want, after, cmpopts.EquateEmpty()) {
		t.Errorf("image after failed appends = %+v, want %+v", after, want)
	}
}
