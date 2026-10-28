package builder

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"log/slog"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
	"phenix/util/plog/plogtest"
)

// iconPixel returns a strict PNG of one pixel whose color depends on seed,
// so every seed below 65536 gives another icon.
func iconPixel(t *testing.T, seed int) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: uint8(seed), G: uint8(seed >> 8), B: 7, A: 255})

	return encodeIcon(t, img)
}

// iconNoise returns a strict PNG of the most pixels an icon has, which does
// not compress, so it is close to the most bytes one has.
func iconNoise(t *testing.T, seed uint64) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, builder.MaxIconPixels, builder.MaxIconPixels))
	random := rand.New(rand.NewPCG(seed, 2))

	for i := range img.Pix {
		img.Pix[i] = byte(random.UintN(256))
	}

	return encodeIcon(t, img)
}

// encodeIcon encodes an image as the standard library does: a header, the
// pixels and the end.
func encodeIcon(t *testing.T, img image.Image) []byte {
	t.Helper()

	var out bytes.Buffer

	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("png.Encode returned error: %v", err)
	}

	return out.Bytes()
}

// The lengths of the PNG parts [encodeIcon] writes: the signature and header
// chunk first, the empty end chunk last.
const (
	iconHeaderBytes = 8 + 4 + 4 + 13 + 4
	iconEndBytes    = 4 + 4 + 4
)

// iconChunk returns one chunk of a PNG: the length of its data, its type,
// the data, and the checksum of the type and the data.
func iconChunk(kind string, data []byte) []byte {
	body := append([]byte(kind), data...)
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, body...)

	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(body))
}

// iconWithChunk returns a PNG with a chunk put into it, after so many of
// its bytes.
func iconWithChunk(data []byte, after int, chunk []byte) []byte {
	out := bytes.Clone(data[:after])
	out = append(out, chunk...)

	return append(out, data[after:]...)
}

// iconWithText returns a PNG with a text chunk put after its header, which
// a document does not accept.
func iconWithText(t *testing.T, data []byte, text string) []byte {
	t.Helper()

	return iconWithChunk(data, iconHeaderBytes, iconChunk("tEXt", []byte("Comment\x00"+text)))
}

// iconHeader returns a PNG that is only a header claiming the given size,
// and the end: it has no pixels.
func iconHeader(width, height uint32) []byte {
	header := binary.BigEndian.AppendUint32(nil, width)
	header = binary.BigEndian.AppendUint32(header, height)
	header = append(header, 8, 6, 0, 0, 0)

	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, iconChunk("IHDR", header)...)

	return append(out, iconChunk("IEND", nil)...)
}

// iconBytes returns the PNG a library icon holds.
func iconBytes(t *testing.T, icon *LibraryIcon) []byte {
	t.Helper()

	data, err := base64.StdEncoding.Strict().DecodeString(icon.Data)
	if err != nil {
		t.Fatalf("decoding the base64 data of icon %s returned error: %v", icon.Name, err)
	}

	return data
}

// iconDiff returns how two icons, or two lists of icons, differ, as cmp.Diff
// does. No aliases and an empty list of them are equal.
func iconDiff(want, got any, opts ...cmp.Option) string {
	return cmp.Diff(want, got, append([]cmp.Option{cmpopts.EquateEmpty()}, opts...)...)
}

// mustAddIcon adds an icon whose name the library does not have yet.
func mustAddIcon(t *testing.T, h *testHarness, owner, name string, data []byte) *LibraryIcon {
	t.Helper()

	icon, created, err := h.service.AddIcon(context.Background(), owner, name, data)
	if err != nil {
		t.Fatalf("AddIcon(%q) returned error: %v", name, err)
	}

	if !created {
		t.Fatalf("AddIcon(%q) created = false, want a new icon", name)
	}

	return icon
}

// mustGetIcon returns the icon a name names.
func mustGetIcon(t *testing.T, h *testHarness, name string) *LibraryIcon {
	t.Helper()

	icon, err := h.service.GetIcon(context.Background(), name)
	if err != nil {
		t.Fatalf("GetIcon(%q) returned error: %v", name, err)
	}

	return icon
}

// iconTag returns the entity tag of the icon that name names.
func iconTag(t *testing.T, h *testHarness, name string) string {
	t.Helper()

	return mustGetIcon(t, h, name).ETag()
}

// recordTag returns the entity tag of the revision of a record of the icon
// library, which a record that cannot be read has.
func recordTag(t *testing.T, h *testHarness, key string) string {
	t.Helper()

	record, err := h.store.GetRecord(NamespaceIcons, key)
	if err != nil {
		t.Fatalf("GetRecord(%s) returned error: %v", key, err)
	}

	return RevisionETag(record.Revision)
}

// iconNames returns the names of the library's icons, in the order they are
// listed.
func iconNames(t *testing.T, h *testHarness) []string {
	t.Helper()

	icons, err := h.service.ListIcons(context.Background())
	if err != nil {
		t.Fatalf("ListIcons returned error: %v", err)
	}

	names := make([]string, 0, len(icons))

	for i := range icons {
		names = append(names, icons[i].Name)
	}

	return names
}

// storedValue returns the decoded value of a record of the icon library.
func storedValue(t *testing.T, h *testHarness, key string) map[string]any {
	t.Helper()

	record, err := h.store.GetRecord(NamespaceIcons, key)
	if err != nil {
		t.Fatalf("GetRecord(%s) returned error: %v", key, err)
	}

	var value map[string]any

	if err := json.Unmarshal(record.Value, &value); err != nil {
		t.Fatalf("decoding icon record %s returned error: %v", key, err)
	}

	return value
}

// iconRecordValue returns the value of an icon record holding the icon.
func iconRecordValue(t *testing.T, icon LibraryIcon) []byte {
	t.Helper()

	value, err := json.Marshal(icon)
	if err != nil {
		t.Fatalf("json.Marshal(icon %s) returned error: %v", icon.Name, err)
	}

	return value
}

// iconAuthors is who uploaded an icon and who renamed it last.
type iconAuthors struct {
	CreatedBy, UpdatedBy string
}

func authorsOf(icon *LibraryIcon) iconAuthors {
	return iconAuthors{CreatedBy: icon.CreatedBy, UpdatedBy: icon.UpdatedBy}
}

// TestIconCreatorAndUpdater asserts an icon names who uploaded it and who
// renamed it last.
func TestIconCreatorAndUpdater(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	icon := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))
	if diff := cmp.Diff(iconAuthors{CreatedBy: testActor, UpdatedBy: testActor}, authorsOf(icon)); diff != "" {
		t.Errorf("AddIcon(plc) authors mismatch (-want +got):\n%s", diff)
	}

	// Another user renames the icon, and is its updater.
	renamed, err := h.service.RenameIcon(ctx, testPeer, "plc", "plc-2")
	if err != nil {
		t.Fatalf("RenameIcon(plc, plc-2) by %s returned error: %v", testPeer, err)
	}

	want := iconAuthors{CreatedBy: testActor, UpdatedBy: testPeer}

	if diff := cmp.Diff(want, authorsOf(renamed)); diff != "" {
		t.Errorf("RenameIcon(plc, plc-2) by %s authors mismatch (-want +got):\n%s", testPeer, diff)
	}

	if diff := cmp.Diff(want, authorsOf(mustGetIcon(t, h, "plc"))); diff != "" {
		t.Errorf("GetIcon(plc) after the rename authors mismatch (-want +got):\n%s", diff)
	}
}

func TestIconLibraryRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if names := iconNames(t, h); len(names) != 0 {
		t.Errorf("ListIcons of a new library = %q, want nothing", names)
	}

	upload := iconPixel(t, 1)
	before := bytes.Clone(upload)

	icon := mustAddIcon(t, h, testActor, "PLC-1", upload)

	// A PNG a document accepts is stored as it is.
	want := LibraryIcon{
		Kind:      "icon",
		Name:      "PLC-1",
		ID:        builder.IconID(upload),
		Owner:     testActor,
		Width:     1,
		Height:    1,
		Bytes:     len(upload),
		Created:   icon.Created,
		Updated:   icon.Created,
		CreatedBy: testActor,
		UpdatedBy: testActor,
		Aliases:   []string{},
		Data:      base64.StdEncoding.EncodeToString(upload),
		Revision:  icon.Revision,
	}

	if diff := iconDiff(want, *icon); diff != "" {
		t.Errorf("AddIcon(PLC-1) mismatch (-want +got):\n%s", diff)
	}

	if icon.Created.IsZero() || icon.Created.Location().String() != "UTC" {
		t.Errorf("AddIcon(PLC-1) created = %v, want a time in UTC", icon.Created)
	}

	if icon.Revision == 0 {
		t.Errorf("AddIcon(PLC-1) revision = 0, want a revision")
	}

	if !bytes.Equal(upload, before) {
		t.Error("AddIcon(PLC-1) changed the bytes it was given")
	}

	// One record, in the library's own namespace, under the name in lower
	// case.
	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/plc-1"}) {
		t.Errorf("icon records = %q, want [name/plc-1]", keys)
	}

	for _, namespace := range []string{NamespaceDrafts, NamespaceContent, NamespacePublished, NamespaceTemplates} {
		if got := h.store.Count(namespace); got != 0 {
			t.Errorf("%s holds %d records, want 0", namespace, got)
		}
	}

	stored := storedValue(t, h, "name/plc-1")

	fields := []string{
		"aliases", "bytes", "created", "createdBy", "data", "height", "id", "kind", "name", "owner", "updated",
		"updatedBy", "width",
	}

	if diff := cmp.Diff(fields, slices.Sorted(maps.Keys(stored))); diff != "" {
		t.Errorf("icon record name/plc-1 fields mismatch (-want +got):\n%s", diff)
	}

	wantRecord := map[string]any{"kind": "icon", "name": "PLC-1"}
	if diff := cmp.Diff(wantRecord, map[string]any{"kind": stored["kind"], "name": stored["name"]}); diff != "" {
		t.Errorf("icon record name/plc-1 kind and name mismatch (-want +got):\n%s", diff)
	}

	if icons, err := h.service.ListIcons(ctx); err != nil {
		t.Errorf("ListIcons returned error: %v", err)
	} else if diff := iconDiff([]LibraryIcon{want}, icons); diff != "" {
		t.Errorf("ListIcons mismatch (-want +got):\n%s", diff)
	}

	// The name is found ignoring case.
	if diff := iconDiff(want, *mustGetIcon(t, h, "plc-1")); diff != "" {
		t.Errorf("GetIcon(plc-1) mismatch (-want +got):\n%s", diff)
	}

	// The same bytes under the same name, in any case, are the icon the
	// library has: nothing is written.
	revision := icon.Revision

	again, created, err := h.service.AddIcon(ctx, testPeer, "Plc-1", upload)
	if err != nil {
		t.Errorf("AddIcon(Plc-1) with the same bytes returned error: %v", err)
	} else if diff := iconDiff(want, *again); diff != "" {
		t.Errorf("AddIcon(Plc-1) with the same bytes mismatch (-want +got):\n%s", diff)
	}

	if created {
		t.Error("AddIcon(Plc-1) with the same bytes created = true, want false")
	}

	if got := mustGetIcon(t, h, "PLC-1").Revision; got != revision {
		t.Errorf("GetIcon(PLC-1) revision after adding the same bytes = %d, want %d", got, revision)
	}

	revisionTag := iconTag(t, h, "plc-1")

	if err := h.service.DeleteIcon(ctx, testActor, "plc-1", iconTag(t, h, "plc-1")); err != nil {
		t.Fatalf("DeleteIcon(plc-1) returned error: %v", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 0 {
		t.Errorf("%d icon records after DeleteIcon(plc-1), want 0", got)
	}

	if err := h.service.DeleteIcon(ctx, testActor, "PLC-1", revisionTag); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteIcon(PLC-1) a second time = %v, want ErrNotFound", err)
	}

	if _, err := h.service.GetIcon(ctx, "PLC-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIcon(PLC-1) after the delete = %v, want ErrNotFound", err)
	}
}

// TestIconNamesAreUnique asserts a name, ignoring case, names one icon, and the
// same image may have two names.
func TestIconNamesAreUnique(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	plc := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))

	icon, created, err := h.service.AddIcon(ctx, testPeer, "PLC", iconPixel(t, 2))
	if icon != nil || created {
		t.Errorf("AddIcon(PLC) with another image = %+v, created %t; want no icon", icon, created)
	}

	if !errors.Is(err, ErrConflict) {
		t.Errorf("AddIcon(PLC) with another image error = %v, want ErrConflict", err)
	}

	var taken *IconNameTakenError

	if !errors.As(err, &taken) {
		t.Fatalf("AddIcon(PLC) with another image error = %v, want an IconNameTakenError", err)
	}

	if diff := cmp.Diff(IconNameTakenError{Name: "PLC", Icon: "plc", Owner: testActor}, *taken); diff != "" {
		t.Errorf("AddIcon(PLC) with another image refusal mismatch (-want +got):\n%s", diff)
	}

	if got, want := taken.Sentence(), `icon name "PLC" belongs to an icon alice uploaded. Choose another name`; got != want {
		t.Errorf("IconNameTakenError.Sentence() = %q, want %q", got, want)
	}

	again, created, err := h.service.AddIcon(ctx, testPeer, "Plc", iconPixel(t, 1))
	if err != nil {
		t.Errorf("AddIcon(Plc) with the same image returned error: %v", err)
	} else if diff := iconDiff(*plc, *again); diff != "" {
		t.Errorf("AddIcon(Plc) with the same image mismatch (-want +got):\n%s", diff)
	}

	if created {
		t.Error("AddIcon(Plc) with the same image created = true, want false")
	}

	twin := mustAddIcon(t, h, testPeer, "plc-twin", iconPixel(t, 1))

	wantTwin := *plc
	wantTwin.Name, wantTwin.Owner, wantTwin.CreatedBy, wantTwin.UpdatedBy = "plc-twin", testPeer, testPeer, testPeer

	if diff := iconDiff(wantTwin, *twin, cmpopts.IgnoreFields(LibraryIcon{}, "Created", "Updated", "Revision")); diff != "" {
		t.Errorf("AddIcon(plc-twin) with the same image mismatch (-want +got):\n%s", diff)
	}

	// An alias is a name too.
	if _, err := h.service.RenameIcon(ctx, testActor, "plc", "plc-2"); err != nil {
		t.Fatalf("RenameIcon(plc, plc-2) returned error: %v", err)
	}

	_, _, err = h.service.AddIcon(ctx, testPeer, "PLC", iconPixel(t, 3))
	if !errors.As(err, &taken) {
		t.Errorf("AddIcon(PLC) under an alias error = %v, want an IconNameTakenError", err)
	} else {
		if diff := cmp.Diff(IconNameTakenError{Name: "PLC", Icon: "plc-2", Owner: testActor}, *taken); diff != "" {
			t.Errorf("AddIcon(PLC) under an alias refusal mismatch (-want +got):\n%s", diff)
		}

		want := `icon name "PLC" is already another name of icon "plc-2", which alice uploaded. Choose another name`
		if got := taken.Sentence(); got != want {
			t.Errorf("IconNameTakenError.Sentence() = %q, want %q", got, want)
		}
	}

	if names := iconNames(t, h); !slices.Equal(names, []string{"plc-2", "plc-twin"}) {
		t.Errorf("ListIcons names = %q, want [plc-2 plc-twin]", names)
	}
}

func TestAddIconNormalizesLooseImages(t *testing.T) {
	const secret = "<script>alert(1)</script> taken at 51.5,-0.1"

	plain := iconNoise(t, 1)
	padding := strings.Repeat("p", builder.MaxIconBytes-len(plain)+1)
	behind, colored := iconPixel(t, 4), iconPixel(t, 5)

	tests := []struct {
		name   string
		upload []byte
		// from is the image whose pixels the stored icon must have.
		from []byte
	}{
		{name: "a text chunk", upload: iconWithText(t, iconPixel(t, 2), secret), from: iconPixel(t, 2)},
		{name: "bytes after the end", upload: append(iconPixel(t, 3), secret...), from: iconPixel(t, 3)},
		{
			// Within the upload limit, above what a document accepts.
			name:   "more bytes than an icon has",
			upload: iconWithText(t, plain, secret+padding),
			from:   plain,
		},
		{
			// A decoder reads the pixels and skips what follows them.
			name:   "a pixel chunk after the pixels",
			upload: iconWithChunk(behind, len(behind)-iconEndBytes, iconChunk("IDAT", []byte(secret))),
			from:   behind,
		},
		{
			// A palette of 15 colors, which pixels that are colors
			// themselves do not use.
			name:   "a palette beside pixels that are colors",
			upload: iconWithChunk(colored, iconHeaderBytes, iconChunk("PLTE", []byte(secret+"!"))),
			from:   colored,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)

			if _, _, err := builder.ValidateIconPNG(test.upload); err == nil {
				t.Fatal("ValidateIconPNG(upload) accepts the upload: the test proves nothing")
			}

			if len(test.upload) > MaxIconUploadBytes {
				t.Fatalf("the upload is %d bytes, above the upload limit", len(test.upload))
			}

			icon := mustAddIcon(t, h, testActor, "loose", test.upload)
			stored := iconBytes(t, icon)

			width, height, err := builder.ValidateIconPNG(stored)
			if err != nil {
				t.Fatalf("ValidateIconPNG(stored icon) returned error: %v", err)
			}

			if icon.ID != builder.IconID(stored) {
				t.Errorf("AddIcon(loose) id = %q, want the ID of the stored bytes %q", icon.ID, builder.IconID(stored))
			}

			if icon.ID == builder.IconID(test.upload) {
				t.Errorf("AddIcon(loose) id = %q, the ID of the upload; want the ID of the stored bytes", icon.ID)
			}

			if got, want := [3]int{icon.Width, icon.Height, icon.Bytes}, [3]int{width, height, len(stored)}; got != want {
				t.Errorf("AddIcon(loose) width, height and bytes = %v, want the stored PNG's %v", got, want)
			}

			if bytes.Contains(stored, []byte("script")) || bytes.Contains(stored, []byte("51.5")) {
				t.Error("AddIcon(loose) stored an icon that holds the text of the upload")
			}

			if diff := cmp.Diff(iconPixels(t, test.from), iconPixels(t, stored)); diff != "" {
				t.Errorf("AddIcon(loose) pixels mismatch (-want +got):\n%s", diff)
			}

			// The same upload under the same name again is the same icon.
			again, created, err := h.service.AddIcon(context.Background(), testActor, "loose", test.upload)
			if err != nil {
				t.Errorf("AddIcon(loose) a second time returned error: %v", err)
			} else if again.ID != icon.ID {
				t.Errorf("AddIcon(loose) a second time id = %s, want %s", again.ID, icon.ID)
			}

			if created {
				t.Error("AddIcon(loose) a second time created = true, want false")
			}

			// Nothing of the upload but its pixels is in the store.
			record, err := h.store.GetRecord(NamespaceIcons, "name/loose")
			if err != nil {
				t.Fatalf("GetRecord(name/loose) returned error: %v", err)
			}

			if bytes.Contains(record.Value, []byte("script")) {
				t.Error("icon record name/loose holds the text of the upload")
			}
		})
	}
}

// iconPixels returns the image a PNG file decodes to, at the origin.
func iconPixels(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode returned error: %v", err)
	}

	bounds := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))

	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			out.Set(x, y, img.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}

	return out
}

func TestAddIconRefuses(t *testing.T) {
	var photo bytes.Buffer

	if err := jpeg.Encode(&photo, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatalf("jpeg.Encode returned error: %v", err)
	}

	pixel := iconPixel(t, 4)
	tooLarge := append(bytes.Clone(pixel), make([]byte, MaxIconUploadBytes+1-len(pixel))...)

	const (
		notPNG  = "is not a PNG image"
		charset = `must be 1 to 64 letters, digits, "_", "@", "." or "-"`
	)

	tests := []struct {
		name   string
		owner  string
		icon   string
		upload []byte
		is     error
		field  string
		reason string
	}{
		{name: "no owner", owner: "", icon: "plc", upload: pixel, is: ErrInvalid, field: "owner", reason: "must not be empty"},
		{name: "no name", owner: testActor, icon: "", upload: pixel, is: ErrInvalid, field: "icon name", reason: `"" ` + charset},
		{
			name: "a name of 65 bytes", owner: testActor, icon: strings.Repeat("n", 65), upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `"` + strings.Repeat("n", 64) + `..." ` + charset,
		},
		{
			name: "a name with a space", owner: testActor, icon: "plc icon", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `"plc icon" ` + charset,
		},
		{
			name: "a name around spaces", owner: testActor, icon: " plc ", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `" plc " ` + charset,
		},
		{
			name: "a name with a slash", owner: testActor, icon: "a/b", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `"a/b" ` + charset,
		},
		{
			name: "a name that is an icon id", owner: testActor, icon: builder.IconID(pixel), upload: pixel,
			is: ErrInvalid, field: "icon name", reason: charset,
		},
		{
			name: "a name that is not text", owner: testActor, icon: "\xff\xfe", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: charset,
		},
		{
			name: "a name of two dots", owner: testActor, icon: "..", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `".." must not be "." or ".."`,
		},
		{name: "nothing", owner: testActor, icon: "plc", upload: nil, is: ErrInvalid, field: kindIcon, reason: notPNG},
		{name: "a JPEG", owner: testActor, icon: "plc", upload: photo.Bytes(), is: ErrInvalid, field: kindIcon, reason: notPNG},
		{
			name: "an SVG", owner: testActor, icon: "plc",
			upload: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
			is:     ErrInvalid, field: kindIcon, reason: notPNG,
		},
		{
			name:   "a PNG cut short",
			owner:  testActor,
			icon:   "plc",
			upload: pixel[:len(pixel)-20],
			is:     ErrInvalid,
			field:  kindIcon,
			reason: notPNG,
		},
		{
			name:   "a header and no pixels",
			owner:  testActor,
			icon:   "plc",
			upload: iconHeader(1, 1),
			is:     ErrInvalid,
			field:  kindIcon,
			reason: notPNG,
		},
		{
			name: "97 pixels wide", owner: testActor, icon: "plc", upload: encodeIcon(t, image.NewNRGBA(image.Rect(0, 0, 97, 1))),
			is: ErrInvalid, field: kindIcon, reason: "is 97 x 1 pixels: the limit is 96 x 96",
		},
		{
			name: "97 pixels high", owner: testActor, icon: "plc", upload: encodeIcon(t, image.NewNRGBA(image.Rect(0, 0, 1, 97))),
			is: ErrInvalid, field: kindIcon, reason: "is 1 x 97 pixels: the limit is 96 x 96",
		},
		{
			// Refused on its header alone: nothing is decoded.
			name: "a header that claims 50000 by 50000 pixels", owner: testActor, icon: "plc", upload: iconHeader(50000, 50000),
			is: ErrInvalid, field: kindIcon, reason: "is 50000 x 50000 pixels: the limit is 96 x 96",
		},
		{name: "one byte above the upload limit", owner: testActor, icon: "plc", upload: tooLarge, is: ErrTooLarge},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)

			icon, created, err := h.service.AddIcon(context.Background(), test.owner, test.icon, test.upload)
			if !errors.Is(err, test.is) {
				t.Errorf("AddIcon(%q) error = %v, want %v", test.icon, err, test.is)
			}

			if icon != nil || created {
				t.Errorf("AddIcon(%q) = %+v, created %t; want no icon", test.icon, icon, created)
			}

			var invalid *ValidationError

			if test.field != "" {
				if !errors.As(err, &invalid) {
					t.Errorf("AddIcon(%q) error = %v, want a ValidationError", test.icon, err)
				} else if invalid.Field != test.field || !strings.HasSuffix(invalid.Reason, test.reason) {
					t.Errorf("AddIcon(%q) refused field %q because it %q, want field %q because it ends %q",
						test.icon, invalid.Field, invalid.Reason, test.field, test.reason)
				}
			}

			var limit *TooLargeError

			if errors.Is(test.is, ErrTooLarge) {
				want := TooLargeError{What: kindIcon, Size: MaxIconUploadBytes + 1, Limit: MaxIconUploadBytes}

				if !errors.As(err, &limit) {
					t.Errorf("AddIcon(%q) error = %v, want a TooLargeError", test.icon, err)
				} else if diff := cmp.Diff(want, *limit); diff != "" {
					t.Errorf("AddIcon(%q) limit mismatch (-want +got):\n%s", test.icon, diff)
				}
			}

			if got := h.store.Count(NamespaceIcons); got != 0 {
				t.Errorf("a refused AddIcon(%q) left %d records, want 0", test.icon, got)
			}
		})
	}
}

func TestAddIconAcceptsTheLimits(t *testing.T) {
	h := newHarness(t)

	// The longest name, and the largest upload: the most pixels, padded with
	// text to the upload limit.
	name := strings.Repeat("n", builder.MaxIconNameBytes)
	plain := iconNoise(t, 7)
	text := strings.Repeat("t", MaxIconUploadBytes-len(plain)-len("Comment\x00")-12)
	upload := iconWithText(t, plain, text)

	if len(upload) != MaxIconUploadBytes {
		t.Fatalf("the upload is %d bytes, want the limit %d", len(upload), MaxIconUploadBytes)
	}

	icon := mustAddIcon(t, h, testActor, name, upload)

	type shape struct {
		Name          string
		Width, Height int
	}

	want := shape{Name: name, Width: builder.MaxIconPixels, Height: builder.MaxIconPixels}

	if diff := cmp.Diff(want, shape{Name: icon.Name, Width: icon.Width, Height: icon.Height}); diff != "" {
		t.Errorf("AddIcon of the largest upload mismatch (-want +got):\n%s", diff)
	}

	if icon.Bytes > builder.MaxIconBytes {
		t.Errorf("AddIcon of the largest upload stored %d bytes, want at most %d", icon.Bytes, builder.MaxIconBytes)
	}

	// Every character a name may hold.
	mustAddIcon(t, h, testActor, "Az09_@.-", iconPixel(t, 1))
}

// TestIconLibraryHoldsAtMost2000Icons asserts the library holds at most
// MaxIcons icons, whoever uploaded them.
func TestIconLibraryHoldsAtMost2000Icons(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	plantLibraryIcons(t, h, MaxIcons)

	icon, created, err := h.service.AddIcon(context.Background(), testActor, "one-too-many", iconPixel(t, MaxIcons))
	if icon != nil || created {
		t.Errorf("AddIcon(one-too-many) = %+v, created %t; want no icon", icon, created)
	}

	var invalid *ValidationError

	if !errors.As(err, &invalid) {
		t.Errorf("AddIcon(one-too-many) error = %v, want a ValidationError", err)
	} else if want := "is full: it holds at most 2000 icons"; invalid.Reason != want {
		t.Errorf("AddIcon(one-too-many) refused because it %q, want %q", invalid.Reason, want)
	}

	_, created, err = h.service.AddIcon(context.Background(), testActor, "icon-0000", iconPixel(t, 0))
	if err != nil {
		t.Errorf("AddIcon(icon-0000) of an icon the full library has returned error: %v", err)
	}

	if created {
		t.Error("AddIcon(icon-0000) of an icon the full library has created = true, want false")
	}

	if err := h.service.DeleteIcon(context.Background(), testActor, "icon-0000", iconTag(t, h, "icon-0000")); err != nil {
		t.Fatalf("DeleteIcon(icon-0000) returned error: %v", err)
	}

	mustAddIcon(t, h, testPeer, "now-it-fits", iconPixel(t, MaxIcons))
}

// mustListIcons returns the icons of the library.
func mustListIcons(t *testing.T, h *testHarness) []LibraryIcon {
	t.Helper()

	icons, err := h.service.ListIcons(context.Background())
	if err != nil {
		t.Fatalf("ListIcons returned error: %v", err)
	}

	return icons
}

func TestListIconsOrder(t *testing.T) {
	h := newHarness(t)

	for i, name := range []string{"beta", "Alpha-2", "gamma", "alpha", "Beta-1"} {
		mustAddIcon(t, h, []string{testActor, testPeer}[i%2], name, iconPixel(t, i))
	}

	want := []string{"alpha", "Alpha-2", "beta", "Beta-1", "gamma"}
	if names := iconNames(t, h); !slices.Equal(names, want) {
		t.Errorf("ListIcons names = %q, want them by name ignoring case %q", names, want)
	}
}

// aliasRecord is the decoded record of an old name of an icon.
func aliasRecord(name, target string) map[string]any {
	return map[string]any{"kind": "alias", "name": name, "target": target}
}

// TestRenameIcon renames an icon twice, changes the case of its name, and
// renames it back to its first name.
func TestRenameIcon(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	original := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))
	volatile := cmpopts.IgnoreFields(LibraryIcon{}, "Updated", "Revision")

	h.now.Add(10)

	// The icon moves, and its old name is an unlisted alias of it.
	renamed, err := h.service.RenameIcon(ctx, testActor, "PLC", "plc-v2")
	if err != nil {
		t.Fatalf("RenameIcon(PLC, plc-v2) returned error: %v", err)
	}

	want := *original
	want.Name, want.Aliases = "plc-v2", []string{"plc"}

	if diff := iconDiff(want, *renamed, volatile); diff != "" {
		t.Errorf("RenameIcon(PLC, plc-v2) mismatch (-want +got):\n%s", diff)
	}

	if !renamed.Updated.After(original.Updated) {
		t.Errorf("RenameIcon(PLC, plc-v2) updated = %v, want after %v", renamed.Updated, original.Updated)
	}

	if diff := cmp.Diff(aliasRecord("plc", "plc-v2"), storedValue(t, h, "name/plc")); diff != "" {
		t.Errorf("icon record name/plc after the first rename mismatch (-want +got):\n%s", diff)
	}

	if diff := iconDiff(*renamed, *mustGetIcon(t, h, "plc")); diff != "" {
		t.Errorf("GetIcon(plc) after the first rename mismatch (-want +got):\n%s", diff)
	}

	if names := iconNames(t, h); !slices.Equal(names, []string{"plc-v2"}) {
		t.Errorf("ListIcons names after the first rename = %q, want [plc-v2]", names)
	}

	// A second rename points both old names at the new one.
	third, err := h.service.RenameIcon(ctx, testActor, "plc-v2", "PLC-3")
	if err != nil {
		t.Fatalf("RenameIcon(plc-v2, PLC-3) returned error: %v", err)
	}

	if want := []string{"plc", "plc-v2"}; !slices.Equal(third.Aliases, want) {
		t.Errorf("RenameIcon(plc-v2, PLC-3) aliases = %q, want %q", third.Aliases, want)
	}

	for _, name := range []string{"plc", "plc-v2"} {
		if diff := cmp.Diff(aliasRecord(name, "plc-3"), storedValue(t, h, iconKey(name))); diff != "" {
			t.Errorf("icon record of %s after two renames mismatch (-want +got):\n%s", name, diff)
		}
	}

	if got := mustGetIcon(t, h, "plc"); got.Name != "PLC-3" {
		t.Errorf("GetIcon(plc) after two renames name = %q, want PLC-3", got.Name)
	}

	// A change of case renames the icon in place, with a new revision.
	keys := h.store.Keys(NamespaceIcons)

	cased, err := h.service.RenameIcon(ctx, testActor, "plc-3", "plc-3")
	if err != nil {
		t.Fatalf("RenameIcon(plc-3, plc-3) returned error: %v", err)
	}

	want = *third
	want.Name = "plc-3"

	if diff := iconDiff(want, *cased, volatile); diff != "" {
		t.Errorf("RenameIcon(plc-3, plc-3) mismatch (-want +got):\n%s", diff)
	}

	if cased.Revision == third.Revision {
		t.Errorf("RenameIcon(plc-3, plc-3) revision = %d, want a new one", cased.Revision)
	}

	if after := h.store.Keys(NamespaceIcons); !slices.Equal(after, keys) {
		t.Errorf("icon records after RenameIcon(plc-3, plc-3) = %q, want them unchanged %q", after, keys)
	}

	// The same name again changes nothing.
	if same, err := h.service.RenameIcon(ctx, testActor, "plc", "plc-3"); err != nil {
		t.Errorf("RenameIcon(plc, plc-3) to its own name returned error: %v", err)
	} else if diff := iconDiff(*cased, *same); diff != "" {
		t.Errorf("RenameIcon(plc, plc-3) to its own name mismatch (-want +got):\n%s", diff)
	}

	// Renaming back takes the first name back from the alias. The other old
	// names become aliases of it.
	back, err := h.service.RenameIcon(ctx, testActor, "plc-3", "plc")
	if err != nil {
		t.Fatalf("RenameIcon(plc-3, plc) returned error: %v", err)
	}

	want = *cased
	want.Name, want.Aliases = "plc", []string{"plc-v2", "plc-3"}

	if diff := iconDiff(want, *back, volatile); diff != "" {
		t.Errorf("RenameIcon(plc-3, plc) mismatch (-want +got):\n%s", diff)
	}

	if stored := storedValue(t, h, "name/plc"); stored["kind"] != "icon" {
		t.Errorf("icon record name/plc after renaming back = %v, want the icon", stored)
	}

	for _, name := range []string{"plc-v2", "PLC-3", "plc"} {
		if got := mustGetIcon(t, h, name); got.Name != "plc" {
			t.Errorf("GetIcon(%s) after renaming back name = %q, want plc", name, got.Name)
		}
	}

	if got := h.store.Count(NamespaceIcons); got != 3 {
		t.Errorf("%d icon records after renaming back, want the icon and two aliases", got)
	}

	for _, name := range []string{"plc-v2", "plc-3"} {
		if diff := cmp.Diff(aliasRecord(name, "plc"), storedValue(t, h, iconKey(name))); diff != "" {
			t.Errorf("icon record of %s after renaming back mismatch (-want +got):\n%s", name, diff)
		}
	}
}

// TestRenameIconManyTimes renames an icon until it has maxIconAliases old
// names: every old name still names the icon and stays reserved.
func TestRenameIconManyTimes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	icon := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))

	const renames = maxIconAliases

	names := []string{"plc"}

	for i := 1; i <= renames; i++ {
		name := fmt.Sprintf("plc-%d", i)
		from := names[len(names)-1]

		// Each rename starts from the name the one before it gave.
		renamed, err := h.service.RenameIcon(ctx, testActor, from, name)
		if err != nil {
			t.Fatalf("RenameIcon(%s, %s) returned error: %v", from, name, err)
		}

		if !slices.Equal(renamed.Aliases, names) {
			t.Errorf("RenameIcon(%s, %s) aliases = %q, want %q", from, name, renamed.Aliases, names)
		}

		names = append(names, name)
	}

	last := names[len(names)-1]

	for _, name := range names[:renames] {
		if diff := cmp.Diff(aliasRecord(name, last), storedValue(t, h, iconKey(name))); diff != "" {
			t.Errorf("icon record of %s mismatch (-want +got):\n%s", name, diff)
		}
	}

	final := mustGetIcon(t, h, last)
	if final.Name != last {
		t.Errorf("GetIcon(%s) name = %q, want %q", last, final.Name, last)
	}

	if final.ID != icon.ID {
		t.Errorf("GetIcon(%s) id = %s, want the icon's %s", last, final.ID, icon.ID)
	}

	for _, name := range names {
		if diff := iconDiff(*final, *mustGetIcon(t, h, name)); diff != "" {
			t.Errorf("GetIcon(%s) mismatch (-want +got):\n%s", name, diff)
		}
	}

	if got := h.store.Count(NamespaceIcons); got != renames+1 {
		t.Errorf("%d icon records, want the icon and %d aliases", got, renames)
	}

	// The first name is still taken, by the icon it names.
	var taken *IconNameTakenError

	if _, _, err := h.service.AddIcon(ctx, testPeer, "PLC", iconPixel(t, 2)); !errors.As(err, &taken) {
		t.Errorf("AddIcon(PLC) under the first name error = %v, want an IconNameTakenError", err)
	} else if taken.Icon != last {
		t.Errorf("AddIcon(PLC) under the first name is taken by %s, want %s", taken.Icon, last)
	}
}

// TestRenameIconAliasLimit renames an icon that has maxIconAliases old names.
// A new name is refused and writes nothing. An old name is taken back.
func TestRenameIconAliasLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mustAddIcon(t, h, testActor, "plc-0", iconPixel(t, 1))

	for i := 1; i <= maxIconAliases; i++ {
		if _, err := h.service.RenameIcon(ctx, testActor, fmt.Sprintf("plc-%d", i-1), fmt.Sprintf("plc-%d", i)); err != nil {
			t.Fatalf("RenameIcon(plc-%d, plc-%d) returned error: %v", i-1, i, err)
		}
	}

	last := fmt.Sprintf("plc-%d", maxIconAliases)
	before := h.store.Commits()

	var invalid *ValidationError

	if _, err := h.service.RenameIcon(ctx, testActor, last, "plc-new"); !errors.As(err, &invalid) {
		t.Errorf("RenameIcon(%s, plc-new) error = %v, want a ValidationError", last, err)
	}

	if got := h.store.Commits(); got != before {
		t.Errorf("RenameIcon(%s, plc-new) made %d commits, want none", last, got-before)
	}

	if _, err := h.service.GetIcon(ctx, "plc-new"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIcon(plc-new) after the refused rename error = %v, want ErrNotFound", err)
	}

	renamed, err := h.service.RenameIcon(ctx, testActor, last, "plc-0")
	if err != nil {
		t.Fatalf("RenameIcon(%s, plc-0) returned error: %v", last, err)
	}

	if len(renamed.Aliases) != maxIconAliases {
		t.Errorf("RenameIcon(%s, plc-0) kept %d old names, want %d", last, len(renamed.Aliases), maxIconAliases)
	}
}

// TestEncodeIconRecordSizeLimit encodes an icon record larger than
// MaxMetadataBytes.
func TestEncodeIconRecordSizeLimit(t *testing.T) {
	aliases := make([]string, MaxMetadataBytes/8)
	for i := range aliases {
		aliases[i] = fmt.Sprintf("name-%d", i)
	}

	if _, err := encodeIconRecord(LibraryIcon{Name: "plc", Aliases: aliases}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("encodeIconRecord(%d aliases) error = %v, want ErrTooLarge", len(aliases), err)
	}
}

// iconNaming is the name of an icon and its old names.
type iconNaming struct {
	Name    string
	Aliases []string
}

func namingOf(icon *LibraryIcon) iconNaming {
	return iconNaming{Name: icon.Name, Aliases: icon.Aliases}
}

// TestRenameIconRetargetFailure asserts a rename that cannot repoint an older
// alias still renames the icon, and the next rename repoints it.
func TestRenameIconRetargetFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))

	if _, err := h.service.RenameIcon(ctx, testActor, "plc", "plc-2"); err != nil {
		t.Fatalf("RenameIcon(plc, plc-2) returned error: %v", err)
	}

	h.store.BeforeUpdate = func(_, key string) error {
		if key == "name/plc" {
			return fmt.Errorf("injected: %w", store.ErrRecordConflict)
		}

		return nil
	}

	renamed, err := h.service.RenameIcon(ctx, testActor, "plc-2", "plc-3")
	if err != nil {
		t.Fatalf("RenameIcon(plc-2, plc-3) whose retargeting failed returned error: %v", err)
	}

	if diff := cmp.Diff(iconNaming{Name: "plc-3", Aliases: []string{"plc", "plc-2"}}, namingOf(renamed)); diff != "" {
		t.Errorf("RenameIcon(plc-2, plc-3) whose retargeting failed mismatch (-want +got):\n%s", diff)
	}

	h.store.BeforeUpdate = nil

	if diff := cmp.Diff(aliasRecord("plc", "plc-2"), storedValue(t, h, "name/plc")); diff != "" {
		t.Errorf("icon record name/plc that could not be retargeted mismatch (-want +got):\n%s", diff)
	}

	if got := mustGetIcon(t, h, "plc"); got.Name != "plc-3" {
		t.Errorf("GetIcon(plc) name = %q, want plc-3", got.Name)
	}

	if _, err := h.service.RenameIcon(ctx, testActor, "plc-3", "plc-4"); err != nil {
		t.Fatalf("RenameIcon(plc-3, plc-4) returned error: %v", err)
	}

	for _, name := range []string{"plc", "plc-2", "plc-3"} {
		if diff := cmp.Diff(aliasRecord(name, "plc-4"), storedValue(t, h, iconKey(name))); diff != "" {
			t.Errorf("icon record of %s after the next rename mismatch (-want +got):\n%s", name, diff)
		}
	}
}

func TestRenameIconRefuses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	plc := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))
	mustAddIcon(t, h, testPeer, "hmi", iconPixel(t, 2))

	if _, err := h.service.RenameIcon(ctx, testPeer, "hmi", "hmi-old"); err != nil {
		t.Fatalf("RenameIcon(hmi, hmi-old) returned error: %v", err)
	}

	before := h.store.Keys(NamespaceIcons)

	for _, test := range []struct {
		name, from, to string
		taken          *IconNameTakenError
		invalidField   string
		wantErr        error
	}{
		{
			name: "the name of another icon", from: "plc", to: "HMI-OLD",
			taken: &IconNameTakenError{Name: "HMI-OLD", Icon: "hmi-old", Owner: testPeer},
		},
		{
			name: "an alias of another icon", from: "plc", to: "Hmi",
			taken: &IconNameTakenError{Name: "Hmi", Icon: "hmi-old", Owner: testPeer},
		},
		{name: "a name that is none", from: "plc", to: "plc v2", invalidField: "icon name"},
		{name: "no name", from: "plc", to: "", wantErr: ErrInvalid},
		{name: "an icon nobody has", from: "scada", to: "scada-2", wantErr: ErrNotFound},
		{name: "a path", from: "../plc", to: "scada-2", wantErr: ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			icon, err := h.service.RenameIcon(ctx, testActor, test.from, test.to)
			if icon != nil {
				t.Errorf("RenameIcon(%q, %q) = %+v, want no icon", test.from, test.to, icon)
			}

			switch {
			case test.taken != nil:
				var taken *IconNameTakenError
				if !errors.As(err, &taken) {
					t.Errorf("RenameIcon(%q, %q) error = %v, want an IconNameTakenError", test.from, test.to, err)
				} else if diff := cmp.Diff(*test.taken, *taken); diff != "" {
					t.Errorf("RenameIcon(%q, %q) IconNameTakenError mismatch (-want +got):\n%s", test.from, test.to, diff)
				}
			case test.invalidField != "":
				var invalid *ValidationError
				if !errors.As(err, &invalid) {
					t.Errorf("RenameIcon(%q, %q) error = %v, want a ValidationError", test.from, test.to, err)
				} else if invalid.Field != test.invalidField {
					t.Errorf("RenameIcon(%q, %q) ValidationError field = %q, want %q", test.from, test.to, invalid.Field, test.invalidField)
				}
			case !errors.Is(err, test.wantErr):
				t.Errorf("RenameIcon(%q, %q) error = %v, want %v", test.from, test.to, err, test.wantErr)
			}
		})
	}

	if after := h.store.Keys(NamespaceIcons); !slices.Equal(after, before) {
		t.Errorf("icon records after the refused renames = %q, want them unchanged %q", after, before)
	}

	// Any user renames the icon of another user, which keeps its uploader.
	icon, err := h.service.RenameIcon(ctx, testPeer, "plc", "plc-2")
	if err != nil {
		t.Fatalf("RenameIcon(plc, plc-2) by %s returned error: %v", testPeer, err)
	}

	want := *plc
	want.Name, want.Aliases, want.UpdatedBy = "plc-2", []string{"plc"}, testPeer

	if diff := iconDiff(want, *icon, cmpopts.IgnoreFields(LibraryIcon{}, "Updated", "Revision")); diff != "" {
		t.Errorf("RenameIcon(plc, plc-2) by %s mismatch (-want +got):\n%s", testPeer, diff)
	}
}

// TestRenameIconRace asserts a rename whose old record changed between its
// two writes undoes the first and is a conflict.
func TestRenameIconRace(t *testing.T) {
	h := newHarness(t)
	icon := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))

	h.store.BeforeUpdate = func(_, key string) error {
		if key == "name/plc" {
			return fmt.Errorf("injected: %w", store.ErrRecordConflict)
		}

		return nil
	}

	renamed, err := h.service.RenameIcon(context.Background(), testActor, "plc", "plc-2")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("RenameIcon(plc, plc-2) that lost a race error = %v, want ErrConflict", err)
	}

	if renamed != nil {
		t.Errorf("RenameIcon(plc, plc-2) that lost a race = %+v, want no icon", renamed)
	}

	h.store.BeforeUpdate = nil

	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/plc"}) {
		t.Errorf("icon records after the lost race = %q, want [name/plc]", keys)
	}

	if diff := iconDiff(*icon, *mustGetIcon(t, h, "plc")); diff != "" {
		t.Errorf("GetIcon(plc) after the lost race mismatch (-want +got):\n%s", diff)
	}
}

// TestDeleteIconRemovesAliases asserts deleting an icon by any name removes its
// record and its aliases, and nothing else.
func TestDeleteIconRemovesAliases(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))
	mustAddIcon(t, h, testPeer, "hmi", iconPixel(t, 2))

	for _, rename := range [][2]string{{"plc", "plc-2"}, {"plc-2", "plc-3"}, {"hmi", "hmi-2"}} {
		if _, err := h.service.RenameIcon(ctx, testActor, rename[0], rename[1]); err != nil {
			t.Fatalf("RenameIcon(%s, %s) returned error: %v", rename[0], rename[1], err)
		}
	}

	// An alias the icon no longer lists, whose chain still ends at it.
	h.store.SetValue(NamespaceIcons, "name/plc-3", iconRecordValue(t, func() LibraryIcon {
		icon := *mustGetIcon(t, h, "plc-3")
		icon.Aliases = []string{"plc-2"}

		return icon
	}()))

	// A user other than the uploader deletes it.
	if err := h.service.DeleteIcon(ctx, testPeer, "PLC", iconTag(t, h, "PLC")); err != nil {
		t.Fatalf("DeleteIcon(PLC) through its first name returned error: %v", err)
	}

	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/hmi", "name/hmi-2"}) {
		t.Errorf("icon records after DeleteIcon(PLC) = %q, want only the other icon's [name/hmi name/hmi-2]", keys)
	}

	for _, name := range []string{"plc", "plc-2", "plc-3"} {
		if _, err := h.service.GetIcon(ctx, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetIcon(%s) after the delete = %v, want ErrNotFound", name, err)
		}
	}

	if err := h.service.DeleteIcon(ctx, testActor, "hmi", iconTag(t, h, "hmi")); err != nil {
		t.Errorf("DeleteIcon(hmi) of another user's icon returned error: %v", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 0 {
		t.Errorf("%d icon records after DeleteIcon(hmi), want 0", got)
	}
}

// TestDeleteIconChecksETag asserts a delete with the tag of an older version
// of the icon is refused with the current tag, and deletes nothing.
func TestDeleteIconChecksETag(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	old := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 1))

	renamed, err := h.service.RenameIcon(ctx, testPeer, "plc", "plc-2")
	if err != nil {
		t.Fatalf("RenameIcon(plc, plc-2) returned error: %v", err)
	}

	if renamed.ETag() == old.ETag() {
		t.Fatalf("RenameIcon(plc, plc-2) kept the tag %s, want a new one", old.ETag())
	}

	for _, name := range []string{"plc", "plc-2"} {
		err := h.service.DeleteIcon(ctx, testActor, name, old.ETag())

		var stale *IconStaleError
		if !errors.As(err, &stale) || !errors.Is(err, ErrConflict) {
			t.Errorf("DeleteIcon(%s, %s) = %v, want an IconStaleError that matches ErrConflict", name, old.ETag(), err)
		} else if diff := cmp.Diff(IconStaleError{Name: "plc-2", ETag: renamed.ETag()}, *stale); diff != "" {
			t.Errorf("DeleteIcon(%s, %s) refusal mismatch (-want +got):\n%s", name, old.ETag(), diff)
		}
	}

	if got := h.store.Count(NamespaceIcons); got != 2 {
		t.Errorf("%d icon records after stale deletes, want the icon and its alias", got)
	}

	// A record that cannot be read has the tag of its revision.
	if _, err := h.store.CreateRecord(NamespaceIcons, "name/broken", []byte(`{"kind":"icon"}`)); err != nil {
		t.Fatalf("CreateRecord(name/broken) returned error: %v", err)
	}

	if err := h.service.DeleteIcon(ctx, testActor, "broken", `"1"`); !errors.Is(err, ErrConflict) {
		t.Errorf("DeleteIcon(broken) with another tag = %v, want ErrConflict", err)
	}

	if err := h.service.DeleteIcon(ctx, testActor, "broken", recordTag(t, h, "name/broken")); err != nil {
		t.Errorf("DeleteIcon(broken) with the tag of its revision returned error: %v", err)
	}

	if err := h.service.DeleteIcon(ctx, testActor, "plc", renamed.ETag()); err != nil {
		t.Fatalf("DeleteIcon(plc) with the current tag returned error: %v", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 0 {
		t.Errorf("%d icon records after the delete, want 0", got)
	}
}

func TestDeleteIconRefuses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	icon := mustAddIcon(t, h, testActor, "kept", iconPixel(t, 5))

	for _, name := range []string{"", "missing", icon.ID, "name/kept", "../kept", "kept/", "kept "} {
		if err := h.service.DeleteIcon(ctx, testActor, name, icon.ETag()); !errors.Is(err, ErrNotFound) {
			t.Errorf("DeleteIcon(%q) = %v, want ErrNotFound", name, err)
		}
	}

	if err := h.service.DeleteIcon(ctx, "", "kept", icon.ETag()); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteIcon(kept) without a caller = %v, want ErrInvalid", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 1 {
		t.Errorf("%d icon records, want the one that was kept", got)
	}
}

// TestDeleteIconLosesRace changes the icon after DeleteIcon checked its tag
// and before its transaction removes it. The delete then removes nothing. It
// names the icon as it is now, with the tag that a retry sends.
func TestDeleteIconLosesRace(t *testing.T) {
	ctx := context.Background()

	for _, test := range []struct {
		name string
		// change runs in the transaction of the delete, before the delete of
		// the record name/plc.
		change func(t *testing.T, h *testHarness)
		// staleName is the name the IconStaleError gives, or "" when the
		// delete fails with wantErr.
		staleName string
		wantErr   error
		wantKeys  []string
	}{
		{
			name: "renamed",
			change: func(t *testing.T, h *testHarness) {
				t.Helper()

				if _, err := h.service.RenameIcon(ctx, testPeer, "plc", "plc-2"); err != nil {
					t.Fatalf("RenameIcon(plc, plc-2) returned error: %v", err)
				}
			},
			staleName: "plc-2",
			wantKeys:  []string{"name/plc", "name/plc-1", "name/plc-2"},
		},
		{
			name: "renamed in case",
			change: func(t *testing.T, h *testHarness) {
				t.Helper()

				if _, err := h.service.RenameIcon(ctx, testPeer, "plc", "PLC"); err != nil {
					t.Fatalf("RenameIcon(plc, PLC) returned error: %v", err)
				}
			},
			staleName: "PLC",
			wantKeys:  []string{"name/plc", "name/plc-1"},
		},
		{
			name: "deleted and uploaded again",
			change: func(t *testing.T, h *testHarness) {
				t.Helper()

				if err := h.service.DeleteIcon(ctx, testPeer, "plc", iconTag(t, h, "plc")); err != nil {
					t.Fatalf("DeleteIcon(plc) returned error: %v", err)
				}

				mustAddIcon(t, h, testPeer, "plc", iconPixel(t, 2))
			},
			staleName: "plc",
			wantKeys:  []string{"name/plc"},
		},
		{
			name: "deleted",
			change: func(t *testing.T, h *testHarness) {
				t.Helper()

				if err := h.service.DeleteIcon(ctx, testPeer, "plc", iconTag(t, h, "plc")); err != nil {
					t.Fatalf("DeleteIcon(plc) returned error: %v", err)
				}
			},
			wantErr:  ErrNotFound,
			wantKeys: []string{},
		},
		{
			// Only a damaged store loses the record of the new name.
			name: "renamed, and the record of the new name is lost",
			change: func(t *testing.T, h *testHarness) {
				t.Helper()

				if _, err := h.service.RenameIcon(ctx, testPeer, "plc", "plc-2"); err != nil {
					t.Fatalf("RenameIcon(plc, plc-2) returned error: %v", err)
				}

				h.store.Drop(NamespaceIcons, "name/plc-2")
			},
			wantErr:  ErrNotFound,
			wantKeys: []string{"name/plc", "name/plc-1"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			mustAddIcon(t, h, testActor, "plc-1", iconPixel(t, 1))

			if _, err := h.service.RenameIcon(ctx, testActor, "plc-1", "plc"); err != nil {
				t.Fatalf("RenameIcon(plc-1, plc) returned error: %v", err)
			}

			tag := iconTag(t, h, "plc")
			changed := false

			h.store.FailDelete = func(_, key string) error {
				if key == "name/plc" && !changed {
					changed = true
					test.change(t, h)
				}

				return nil
			}

			err := h.service.DeleteIcon(ctx, testActor, "plc", tag)

			h.store.FailDelete = nil

			if !changed {
				t.Fatalf("DeleteIcon(plc, %s) did not delete the record name/plc, want the change to run", tag)
			}

			if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, test.wantKeys) {
				t.Errorf("icon records after DeleteIcon(plc, %s) lost the race = %q, want %q", tag, keys, test.wantKeys)
			}

			if test.staleName == "" {
				if !errors.Is(err, test.wantErr) {
					t.Errorf("DeleteIcon(plc, %s) that lost the race = %v, want %v", tag, err, test.wantErr)
				}

				return
			}

			want := IconStaleError{Name: test.staleName, ETag: iconTag(t, h, test.staleName)}

			var stale *IconStaleError
			if !errors.As(err, &stale) || !errors.Is(err, ErrConflict) {
				t.Fatalf("DeleteIcon(plc, %s) that lost the race = %v, want an IconStaleError that matches ErrConflict", tag, err)
			}

			if diff := cmp.Diff(want, *stale); diff != "" {
				t.Errorf("DeleteIcon(plc, %s) that lost the race refusal mismatch (-want +got):\n%s", tag, diff)
			}

			// A retry with the tag of the refusal deletes the icon.
			if err := h.service.DeleteIcon(ctx, testActor, "plc", stale.ETag); err != nil {
				t.Errorf("DeleteIcon(plc, %s) with the tag of the refusal returned error: %v", stale.ETag, err)
			}

			if keys := h.store.Keys(NamespaceIcons); len(keys) != 0 {
				t.Errorf("icon records after the retry = %q, want none", keys)
			}
		})
	}
}

// TestResolveIconHopLimit asserts that a name whose aliases lead through more
// than maxIconAliases aliases, or around a loop, names no icon. The icon library
// keeps each alias one hop from its icon. So only a damaged store, or a long
// run of renames that cannot retarget their aliases, holds such a chain.
func TestResolveIconHopLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	icon := mustAddIcon(t, h, testActor, "end", iconPixel(t, 1))

	// hop-i is maxIconAliases+1-i aliases from the icon.
	hop := func(i int) string { return fmt.Sprintf("hop-%d", i) }

	for i := range maxIconAliases + 1 {
		target := "end"
		if i < maxIconAliases {
			target = hop(i + 1)
		}

		value, err := json.Marshal(iconAlias{Kind: aliasRecordKind, Name: hop(i), Target: target})
		if err != nil {
			t.Fatalf("json.Marshal(alias %s) returned error: %v", hop(i), err)
		}

		if _, err := h.store.CreateRecord(NamespaceIcons, iconKey(hop(i)), value); err != nil {
			t.Fatalf("CreateRecord(%s) returned error: %v", iconKey(hop(i)), err)
		}
	}

	for _, loop := range [][2]string{{"loop-a", "loop-b"}, {"loop-b", "loop-a"}} {
		value := fmt.Appendf(nil, `{"kind":"alias","name":%q,"target":%q}`, loop[0], loop[1])
		if _, err := h.store.CreateRecord(NamespaceIcons, iconKey(loop[0]), value); err != nil {
			t.Fatalf("CreateRecord(%s) returned error: %v", iconKey(loop[0]), err)
		}
	}

	if diff := iconDiff(*icon, *mustGetIcon(t, h, hop(1))); diff != "" {
		t.Errorf("GetIcon(%s) %d aliases from the icon mismatch (-want +got):\n%s", hop(1), maxIconAliases, diff)
	}

	for _, name := range []string{hop(0), "loop-a", "loop-b"} {
		if got, err := h.service.GetIcon(ctx, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetIcon(%s) = %v, %v, want ErrNotFound", name, got, err)
		}
	}

	if err := h.service.DeleteIcon(ctx, testActor, "loop-a", icon.ETag()); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteIcon(loop-a) = %v, want ErrNotFound", err)
	}

	// The delete removes every chain that ends at the icon, and no loop.
	if err := h.service.DeleteIcon(ctx, testActor, "end", icon.ETag()); err != nil {
		t.Fatalf("DeleteIcon(end) returned error: %v", err)
	}

	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/loop-a", "name/loop-b"}) {
		t.Errorf("icon records after DeleteIcon(end) = %q, want only the loop [name/loop-a name/loop-b]", keys)
	}
}

// TestAddIconOverDanglingAlias asserts an alias whose icon is gone does not
// keep its name from a new icon.
func TestAddIconOverDanglingAlias(t *testing.T) {
	h := newHarness(t)

	if _, err := h.store.CreateRecord(NamespaceIcons, "name/old", []byte(`{"kind":"alias","name":"old","target":"gone"}`)); err != nil {
		t.Fatalf("CreateRecord(name/old) returned error: %v", err)
	}

	if _, err := h.service.GetIcon(context.Background(), "old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIcon(old) of a dangling alias = %v, want ErrNotFound", err)
	}

	icon := mustAddIcon(t, h, testPeer, "OLD", iconPixel(t, 3))

	if stored := storedValue(t, h, "name/old"); stored["kind"] != "icon" {
		t.Errorf("icon record name/old = %v, want the new icon", stored)
	}

	if diff := iconDiff(*icon, *mustGetIcon(t, h, "old")); diff != "" {
		t.Errorf("GetIcon(old) mismatch (-want +got):\n%s", diff)
	}
}

// TestListIconsSkipsDamagedRecords asserts a damaged record is skipped and logged
// without its content, and only a holder of the delete permission removes it.
func TestListIconsSkipsDamagedRecords(t *testing.T) {
	good := iconPixel(t, 6)
	other := iconPixel(t, 7)

	record := func(change func(*LibraryIcon)) []byte {
		icon := LibraryIcon{
			Kind:     "icon",
			Name:     "planted",
			ID:       builder.IconID(other),
			Owner:    testActor,
			Width:    1,
			Height:   1,
			Bytes:    len(other),
			Created:  memrecord.Time(1),
			Updated:  memrecord.Time(1),
			Aliases:  []string{},
			Data:     base64.StdEncoding.EncodeToString(other),
			Revision: 0,
		}

		change(&icon)

		return iconRecordValue(t, icon)
	}

	tests := []struct {
		name   string
		value  []byte
		reason string
	}{
		{name: "another name", value: record(func(i *LibraryIcon) { i.Name = "other" }), reason: "names another icon"},
		{name: "a name that is none", value: record(func(i *LibraryIcon) { i.Name = "plan ted" }), reason: "names another icon"},
		// No owner at all is the server's (ServerIconOwner). An owner no
		// user name can be is damage.
		{
			name:   "an owner on two lines",
			value:  record(func(i *LibraryIcon) { i.Owner = "alice\nbob" }),
			reason: "names no usable owner",
		},
		{name: "an ID that is not one", value: record(func(i *LibraryIcon) { i.ID = "plc" }), reason: "has no image ID"},
		{
			name:   "the ID of another image",
			value:  record(func(i *LibraryIcon) { i.ID = builder.IconID(good) }),
			reason: "does not match its ID",
		},
		{
			name:   "bytes that are not a PNG",
			value:  record(func(i *LibraryIcon) { i.Data = base64.StdEncoding.EncodeToString([]byte("<svg onload=alert(1)>")) }),
			reason: "not an accepted PNG",
		},
		{
			name: "a PNG a document does not accept",
			value: record(func(i *LibraryIcon) {
				i.Data = base64.StdEncoding.EncodeToString(iconWithText(t, other, "hidden"))
			}),
			reason: "not an accepted PNG",
		},
		{name: "data that is not base64", value: record(func(i *LibraryIcon) { i.Data = "<svg/>" }), reason: "not base64"},
		{
			name:   "an alias that is none",
			value:  record(func(i *LibraryIcon) { i.Aliases = []string{"a b"} }),
			reason: "aliases are not names",
		},
		{
			name:   "its own name as an alias",
			value:  record(func(i *LibraryIcon) { i.Aliases = []string{"PLANTED"} }),
			reason: "aliases are not names",
		},
		{
			name:   "an unknown field",
			value:  []byte(strings.Replace(string(record(func(*LibraryIcon) {})), "{", `{"html":"x",`, 1)),
			reason: "not valid JSON",
		},
		{name: "not JSON", value: []byte("\x89PNG"), reason: "not valid JSON"},
		{name: "no kind", value: []byte(`{"name":"planted"}`), reason: "neither an icon nor an alias"},
		{
			name:   "an alias of itself",
			value:  []byte(`{"kind":"alias","name":"planted","target":"planted"}`),
			reason: "points at no other name",
		},
		{name: "an alias of a path", value: []byte(`{"kind":"alias","name":"planted","target":"../x"}`), reason: "points at no other name"},
		{name: "an alias of another name", value: []byte(`{"kind":"alias","name":"other","target":"x"}`), reason: "names another icon"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			kept := mustAddIcon(t, h, testActor, "kept", good)

			if _, err := h.store.CreateRecord(NamespaceIcons, "name/planted", test.value); err != nil {
				t.Fatalf("CreateRecord(name/planted) returned error: %v", err)
			}

			logs := plogtest.Capture(t)

			icons, err := h.service.ListIcons(ctx)
			if err != nil {
				t.Fatalf("ListIcons returned error: %v", err)
			}

			if diff := iconDiff([]LibraryIcon{*kept}, icons); diff != "" {
				t.Errorf("ListIcons mismatch (-want +got):\n%s", diff)
			}

			logged := logs.Records(t, func(r map[string]any) bool { return r["level"] == slog.LevelError.String() })
			if len(logged) != 1 {
				t.Fatalf("ListIcons logged %d errors %v, want 1 naming the record", len(logged), logged)
			}

			want := map[string]any{"msg": "skipping unreadable builder icon", "icon": "name/planted"}
			if diff := cmp.Diff(want, map[string]any{"msg": logged[0]["msg"], "icon": logged[0]["icon"]}); diff != "" {
				t.Errorf("ListIcons logged error mismatch (-want +got):\n%s", diff)
			}

			if reason, _ := logged[0]["err"].(string); !strings.Contains(reason, test.reason) {
				t.Errorf("ListIcons logged reason %q, want it to contain %q", reason, test.reason)
			}

			// What the record holds is never logged.
			for _, content := range []string{"svg", "alert", "hidden", "iVBOR"} {
				if strings.Contains(logs.String(), content) {
					t.Errorf("ListIcons log holds %q of the record: %s", content, logs.String())
				}
			}

			// The name is taken by what cannot be read, which any user
			// deletes.
			icon, created, err := h.service.AddIcon(ctx, testActor, "planted", other)
			if !errors.Is(err, ErrCorrupt) {
				t.Errorf("AddIcon(planted) over the damaged record error = %v, want ErrCorrupt", err)
			}

			if icon != nil || created {
				t.Errorf("AddIcon(planted) over the damaged record = %+v, created %t; want no icon", icon, created)
			}

			if err := h.service.DeleteIcon(ctx, testPeer, "planted", recordTag(t, h, "name/planted")); err != nil {
				t.Fatalf("DeleteIcon(planted) of the damaged record returned error: %v", err)
			}

			mustAddIcon(t, h, testActor, "planted", other)
		})
	}
}

// TestListIconsTrustsOnlyTheImage asserts the size an icon is listed with,
// and its base64, come from its PNG and not from the record.
func TestListIconsTrustsOnlyTheImage(t *testing.T) {
	h := newHarness(t)
	icon := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 8))

	lied := *icon
	lied.Width, lied.Height, lied.Bytes = 5000, 5000, 1
	// The decoder skips line breaks, which a document refuses.
	lied.Data = icon.Data[:8] + "\r\n" + icon.Data[8:]

	h.store.SetValue(NamespaceIcons, "name/plc", iconRecordValue(t, lied))

	icons := mustListIcons(t, h)

	if diff := iconDiff([]LibraryIcon{*icon}, icons, cmpopts.IgnoreFields(LibraryIcon{}, "Revision")); diff != "" {
		t.Errorf("ListIcons of a record that lies about its image mismatch (-want +got):\n%s", diff)
	}
}

// TestListIconsDoesNotDecodePixels stores an icon whose pixels do not
// decode. The listing shows it, and GetIcon, which gives the image, refuses
// it.
func TestListIconsDoesNotDecodePixels(t *testing.T) {
	h := newHarness(t)
	icon := mustAddIcon(t, h, testActor, "plc", iconPixel(t, 8))

	data, err := base64.StdEncoding.DecodeString(icon.Data)
	if err != nil {
		t.Fatalf("decoding the image of plc: %v", err)
	}

	// The last byte of the checksum of the pixels.
	data[len(data)-13] ^= 0xff

	damaged := *icon
	damaged.ID, damaged.Data = builder.IconID(data), base64.StdEncoding.EncodeToString(data)
	h.store.SetValue(NamespaceIcons, "name/plc", iconRecordValue(t, damaged))

	icons := mustListIcons(t, h)
	if len(icons) != 1 || icons[0].ID != damaged.ID {
		t.Errorf("ListIcons = %+v, want plc with the image ID %s", icons, damaged.ID)
	}

	if _, err := h.service.GetIcon(context.Background(), "plc"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("GetIcon(plc) error = %v, want ErrCorrupt", err)
	}
}

// TestAddIconRacingUpload adds two icons of one name at once: the same image
// stores one icon, another image is refused as taken.
func TestAddIconRacingUpload(t *testing.T) {
	h := newHarness(t)
	upload := iconPixel(t, 9)

	var first *LibraryIcon

	// Another request stores the icon between this one's check and its
	// write.
	h.store.BeforeCreate = func(string, string) error {
		h.store.BeforeCreate = nil
		first = mustAddIcon(t, h, testActor, "plc", upload)

		return nil
	}

	second, created, err := h.service.AddIcon(context.Background(), testPeer, "PLC", upload)
	if first == nil {
		t.Fatal("AddIcon(PLC) did not create a record: the test proves nothing")
	}

	if err != nil {
		t.Errorf("AddIcon(PLC) of the same image racing another returned error: %v", err)
	} else if diff := iconDiff(*first, *second); diff != "" {
		t.Errorf("AddIcon(PLC) of the same image racing another mismatch (-want +got):\n%s", diff)
	}

	if created {
		t.Error("AddIcon(PLC) of the same image racing another created = true, want false")
	}

	h.store.BeforeCreate = func(string, string) error {
		h.store.BeforeCreate = nil
		mustAddIcon(t, h, testActor, "hmi", iconPixel(t, 10))

		return nil
	}

	var taken *IconNameTakenError

	if _, _, err := h.service.AddIcon(context.Background(), testPeer, "hmi", iconPixel(t, 11)); !errors.As(err, &taken) {
		t.Errorf("AddIcon(hmi) of another image under a name just taken error = %v, want an IconNameTakenError", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 2 {
		t.Errorf("%d icon records, want 2", got)
	}
}

func TestIconLibraryStoreFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	full := errors.New("injected create failure")
	h.store.BeforeCreate = func(string, string) error { return full }

	if _, _, err := h.service.AddIcon(ctx, testActor, "plc", iconPixel(t, 10)); !errors.Is(err, full) {
		t.Errorf("AddIcon(plc) = %v, want the store's error %v", err, full)
	}

	h.store.BeforeCreate = nil
	mustAddIcon(t, h, testActor, "plc", iconPixel(t, 10))

	refused := errors.New("injected delete failure")
	h.store.FailDelete = func(string, string) error { return refused }

	if err := h.service.DeleteIcon(ctx, testActor, "plc", iconTag(t, h, "plc")); !errors.Is(err, refused) {
		t.Errorf("DeleteIcon(plc) = %v, want the store's error %v", err, refused)
	}

	h.store.FailDelete = nil

	// The icon and its aliases go together, or none of them goes.
	if _, err := h.service.RenameIcon(ctx, testActor, "plc", "plc-2"); err != nil {
		t.Fatalf("RenameIcon(plc, plc-2) returned error: %v", err)
	}

	h.store.FailDelete = func(_, key string) error {
		if key == "name/plc" {
			return refused
		}

		return nil
	}

	if err := h.service.DeleteIcon(ctx, testActor, "plc-2", iconTag(t, h, "plc-2")); !errors.Is(err, refused) {
		t.Errorf("DeleteIcon(plc-2) with an alias that cannot be removed = %v, want %v", err, refused)
	}

	h.store.FailDelete = nil

	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/plc", "name/plc-2"}) {
		t.Errorf("icon records after the failed delete = %q, want the icon and its alias [name/plc name/plc-2]", keys)
	}

	if err := h.service.DeleteIcon(ctx, testActor, "plc", iconTag(t, h, "plc")); err != nil {
		t.Errorf("DeleteIcon(plc) through its alias returned error: %v", err)
	}

	if keys := h.store.Keys(NamespaceIcons); len(keys) != 0 {
		t.Errorf("icon records after the delete = %q, want none", keys)
	}

	mustAddIcon(t, h, testPeer, "plc", iconPixel(t, 12))

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := h.service.ListIcons(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("ListIcons with a canceled context = %v, want context.Canceled", err)
	}

	if _, err := h.service.GetIcon(canceled, "plc"); !errors.Is(err, context.Canceled) {
		t.Errorf("GetIcon(plc) with a canceled context = %v, want context.Canceled", err)
	}

	if _, _, err := h.service.AddIcon(canceled, testActor, "x", iconPixel(t, 11)); !errors.Is(err, context.Canceled) {
		t.Errorf("AddIcon(x) with a canceled context = %v, want context.Canceled", err)
	}

	if _, err := h.service.RenameIcon(canceled, testActor, "plc", "y"); !errors.Is(err, context.Canceled) {
		t.Errorf("RenameIcon(plc, y) with a canceled context = %v, want context.Canceled", err)
	}

	if err := h.service.DeleteIcon(canceled, testActor, "plc", `"1"`); !errors.Is(err, context.Canceled) {
		t.Errorf("DeleteIcon(plc) with a canceled context = %v, want context.Canceled", err)
	}
}
