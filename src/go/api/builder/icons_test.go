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
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"

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
		t.Fatalf("encoding a PNG: %v", err)
	}

	return out.Bytes()
}

// The lengths of the parts of a PNG that [encodeIcon] writes: the signature
// and the header chunk (its length, its type, 13 bytes and its checksum)
// come first, and the end chunk, which has no data, last.
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
		t.Fatalf("the icon's data is not base64: %v", err)
	}

	return data
}

// sameIcon reports whether two icons are equal in every field.
func sameIcon(got, want LibraryIcon) bool {
	if !got.Created.Equal(want.Created) || !got.Updated.Equal(want.Updated) || !slices.Equal(got.Aliases, want.Aliases) {
		return false
	}

	got.Created, got.Updated, got.Aliases = want.Created, want.Updated, want.Aliases

	return fmt.Sprint(got) == fmt.Sprint(want)
}

// mustAddIcon adds an icon whose name the library does not have yet.
func mustAddIcon(t *testing.T, h *testHarness, owner, name string, data []byte) *LibraryIcon {
	t.Helper()

	icon, created, err := h.service.AddIcon(context.Background(), owner, name, data)
	if err != nil {
		t.Fatalf("AddIcon(%q) returned error: %v", name, err)
	}

	if !created {
		t.Fatalf("AddIcon(%q) found the icon in the library, want a new one", name)
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
		t.Fatalf("reading the icon record %s: %v", key, err)
	}

	var value map[string]any

	if err := json.Unmarshal(record.Value, &value); err != nil {
		t.Fatalf("the icon record %s is not JSON: %v", key, err)
	}

	return value
}

// iconRecordValue returns the value of an icon record holding the icon.
func iconRecordValue(t *testing.T, icon LibraryIcon) []byte {
	t.Helper()

	value, err := json.Marshal(icon)
	if err != nil {
		t.Fatalf("encoding an icon record: %v", err)
	}

	return value
}

func TestOwnerScope(t *testing.T) {
	scope := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := map[string]string{}

	for _, user := range []string{"", "a", "a/b", "A", "..", ".", "a/../b", "alice", "alice ", "ålice", strings.Repeat("u", 4096)} {
		got := OwnerScope(user)

		if !scope.MatchString(got) {
			t.Errorf("OwnerScope(%q) = %q, want 64 lowercase hex digits", user, got)
		}

		if got != OwnerScope(user) {
			t.Errorf("OwnerScope(%q) is not the same every time", user)
		}

		if other, ok := seen[got]; ok {
			t.Errorf("OwnerScope(%q) and OwnerScope(%q) are both %q", user, other, got)
		}

		seen[got] = user

		if err := store.ValidateRecordKey(got + "/x"); err != nil {
			t.Errorf("OwnerScope(%q) cannot start a record key: %v", user, err)
		}
	}

	// The SHA-256 of "alice", so the scope of a user is the same on every
	// server and in every version.
	if got, want := OwnerScope("alice"), "2bd806c97f0e00af1a1fc3328fa763a9269723c8db8fac4f93af71db186d6e90"; got != want {
		t.Errorf("OwnerScope(alice) = %q, want %q", got, want)
	}
}

func TestIconLibraryRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if names := iconNames(t, h); len(names) != 0 {
		t.Fatalf("a new library lists %q, want nothing", names)
	}

	upload := iconPixel(t, 1)
	before := bytes.Clone(upload)

	icon := mustAddIcon(t, h, testOwner, "PLC-1", upload)

	// A PNG a document accepts is stored as it is.
	want := LibraryIcon{
		Kind:     "icon",
		Name:     "PLC-1",
		ID:       builder.IconID(upload),
		Owner:    testOwner,
		Width:    1,
		Height:   1,
		Bytes:    len(upload),
		Created:  icon.Created,
		Updated:  icon.Created,
		Aliases:  []string{},
		Data:     base64.StdEncoding.EncodeToString(upload),
		Revision: icon.Revision,
	}

	if !sameIcon(*icon, want) {
		t.Fatalf("AddIcon returned %+v, want %+v", *icon, want)
	}

	if icon.Created.IsZero() || icon.Created.Location().String() != "UTC" || icon.Revision == 0 {
		t.Fatalf("created = %v, revision %d, want a time in UTC and a revision", icon.Created, icon.Revision)
	}

	if !bytes.Equal(upload, before) {
		t.Fatal("AddIcon changed the bytes it was given")
	}

	// One record, in the library's own namespace, under the name in lower
	// case.
	if keys := h.store.Keys(NamespaceIcons); len(keys) != 1 || keys[0] != "name/plc-1" {
		t.Fatalf("icon records = %q, want only name/plc-1", keys)
	}

	for _, namespace := range []string{NamespaceDrafts, NamespaceChunks, NamespacePublished, NamespaceTemplates} {
		if got := h.store.Count(namespace); got != 0 {
			t.Errorf("%s holds %d records, want 0", namespace, got)
		}
	}

	stored := storedValue(t, h, "name/plc-1")

	for _, field := range []string{"kind", "name", "id", "owner", "width", "height", "bytes", "created", "updated", "aliases", "data"} {
		if _, ok := stored[field]; !ok {
			t.Errorf("the icon record has no %q", field)
		}
	}

	if len(stored) != 11 || stored["kind"] != "icon" || stored["name"] != "PLC-1" {
		t.Errorf("the icon record = %v, want 11 fields of an icon named PLC-1", stored)
	}

	icons, err := h.service.ListIcons(ctx)
	if err != nil || len(icons) != 1 || !sameIcon(icons[0], want) {
		t.Fatalf("ListIcons = %+v, %v; want only %+v", icons, err, want)
	}

	// The name is found ignoring case.
	if got := mustGetIcon(t, h, "plc-1"); !sameIcon(*got, want) {
		t.Fatalf("GetIcon(plc-1) = %+v, want %+v", got, want)
	}

	// The same bytes under the same name, in any case, are the icon the
	// library has: nothing is written.
	revision := icon.Revision

	again, created, err := h.service.AddIcon(ctx, testPeer, "Plc-1", upload)
	if err != nil || created || !sameIcon(*again, want) || mustGetIcon(t, h, "PLC-1").Revision != revision {
		t.Fatalf("adding the same bytes again: %+v, created = %v, err = %v; want the stored icon", again, created, err)
	}

	if err := h.service.DeleteIcon(ctx, testOwner, "plc-1", false); err != nil {
		t.Fatalf("DeleteIcon returned error: %v", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 0 {
		t.Fatalf("%d icon records after the delete, want 0", got)
	}

	if err := h.service.DeleteIcon(ctx, testOwner, "PLC-1", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting it again: %v, want ErrNotFound", err)
	}

	if _, err := h.service.GetIcon(ctx, "PLC-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reading it after the delete: %v, want ErrNotFound", err)
	}
}

// TestIconNamesAreUnique asserts a name, ignoring case, names one icon: a
// taken name is refused with who has it, the same image under it is the
// icon that has it, and the same image may have two names.
func TestIconNamesAreUnique(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	plc := mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 1))

	icon, created, err := h.service.AddIcon(ctx, testPeer, "PLC", iconPixel(t, 2))

	var taken *IconNameTakenError

	if !errors.As(err, &taken) || !errors.Is(err, ErrConflict) || icon != nil || created ||
		*taken != (IconNameTakenError{Name: "PLC", Icon: "plc", Owner: testOwner}) {
		t.Fatalf("another image under a taken name: %+v, %v, %v; want the name refused as taken by alice", icon, created, err)
	}

	if got := taken.Sentence(); got != `icon name "PLC" is taken by an icon alice uploaded; choose another name` {
		t.Fatalf("the refusal says %q", got)
	}

	again, created, err := h.service.AddIcon(ctx, testPeer, "Plc", iconPixel(t, 1))
	if err != nil || created || again.Owner != testOwner || again.Revision != plc.Revision {
		t.Fatalf("the same image under the name: %+v, %v, %v; want alice's icon", again, created, err)
	}

	twin := mustAddIcon(t, h, testPeer, "plc-twin", iconPixel(t, 1))

	if twin.ID != plc.ID || twin.Owner != testPeer {
		t.Fatalf("the same image under another name = %+v, want bob's icon of the same image", twin)
	}

	// An alias is a name too.
	if _, err := h.service.RenameIcon(ctx, testOwner, "plc", "plc-2", false); err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	_, _, err = h.service.AddIcon(ctx, testPeer, "PLC", iconPixel(t, 3))
	if !errors.As(err, &taken) || taken.Icon != "plc-2" || taken.Owner != testOwner ||
		taken.Sentence() != `icon name "PLC" is taken: it is another name of icon "plc-2", which alice uploaded; choose another name` {
		t.Fatalf("an alias's name: %v, want it refused as another name of plc-2", err)
	}

	if names := iconNames(t, h); !slices.Equal(names, []string{"plc-2", "plc-twin"}) {
		t.Fatalf("the library lists %q", names)
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
				t.Fatal("the upload is a PNG a document accepts: the test proves nothing")
			}

			if len(test.upload) > MaxIconUploadBytes {
				t.Fatalf("the upload is %d bytes, above the upload limit", len(test.upload))
			}

			icon := mustAddIcon(t, h, testOwner, "loose", test.upload)
			stored := iconBytes(t, icon)

			width, height, err := builder.ValidateIconPNG(stored)
			if err != nil {
				t.Fatalf("the stored icon is not one a document accepts: %v", err)
			}

			if icon.ID != builder.IconID(stored) || icon.ID == builder.IconID(test.upload) {
				t.Fatalf("id = %q, want the ID of the stored bytes, not of the upload", icon.ID)
			}

			if icon.Width != width || icon.Height != height || icon.Bytes != len(stored) {
				t.Fatalf("icon = %d x %d, %d bytes; the stored PNG is %d x %d, %d bytes",
					icon.Width, icon.Height, icon.Bytes, width, height, len(stored))
			}

			if bytes.Contains(stored, []byte("script")) || bytes.Contains(stored, []byte("51.5")) {
				t.Fatal("the stored icon still holds the text of the upload")
			}

			if !samePixels(t, stored, test.from) {
				t.Fatal("the stored icon does not have the pixels of the upload")
			}

			// The same upload under the same name again is the same icon.
			again, created, err := h.service.AddIcon(context.Background(), testOwner, "loose", test.upload)
			if err != nil || created || again.ID != icon.ID {
				t.Fatalf("the same upload again: created = %v, err = %v, want icon %s", created, err, icon.ID)
			}

			// Nothing of the upload but its pixels is in the store.
			record, err := h.store.GetRecord(NamespaceIcons, "name/loose")
			if err != nil {
				t.Fatalf("reading the icon record: %v", err)
			}

			if bytes.Contains(record.Value, []byte("script")) {
				t.Fatal("the record holds the text of the upload")
			}
		})
	}
}

// samePixels reports whether two PNG files decode to the same image.
func samePixels(t *testing.T, got, want []byte) bool {
	t.Helper()

	decode := func(data []byte) *image.NRGBA {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decoding a PNG: %v", err)
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

	a, b := decode(got), decode(want)

	return a.Bounds() == b.Bounds() && bytes.Equal(a.Pix, b.Pix)
}

func TestAddIconRefuses(t *testing.T) {
	var photo bytes.Buffer

	if err := jpeg.Encode(&photo, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatalf("encoding a JPEG: %v", err)
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
		{name: "no name", owner: testOwner, icon: "", upload: pixel, is: ErrInvalid, field: "icon name", reason: `"" ` + charset},
		{
			name: "a name of 65 bytes", owner: testOwner, icon: strings.Repeat("n", 65), upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `"` + strings.Repeat("n", 64) + `..." ` + charset,
		},
		{
			name: "a name with a space", owner: testOwner, icon: "plc icon", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `"plc icon" ` + charset,
		},
		{
			name: "a name around spaces", owner: testOwner, icon: " plc ", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `" plc " ` + charset,
		},
		{
			name: "a name with a slash", owner: testOwner, icon: "a/b", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `"a/b" ` + charset,
		},
		{
			name: "a name that is an icon id", owner: testOwner, icon: builder.IconID(pixel), upload: pixel,
			is: ErrInvalid, field: "icon name", reason: charset,
		},
		{
			name: "a name that is not text", owner: testOwner, icon: "\xff\xfe", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: charset,
		},
		{
			name: "a name of two dots", owner: testOwner, icon: "..", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: `".." must not be "." or ".."`,
		},
		{name: "nothing", owner: testOwner, icon: "plc", upload: nil, is: ErrInvalid, field: kindIcon, reason: notPNG},
		{name: "a JPEG", owner: testOwner, icon: "plc", upload: photo.Bytes(), is: ErrInvalid, field: kindIcon, reason: notPNG},
		{
			name: "an SVG", owner: testOwner, icon: "plc",
			upload: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
			is:     ErrInvalid, field: kindIcon, reason: notPNG,
		},
		{
			name:   "a PNG cut short",
			owner:  testOwner,
			icon:   "plc",
			upload: pixel[:len(pixel)-20],
			is:     ErrInvalid,
			field:  kindIcon,
			reason: notPNG,
		},
		{
			name:   "a header and no pixels",
			owner:  testOwner,
			icon:   "plc",
			upload: iconHeader(1, 1),
			is:     ErrInvalid,
			field:  kindIcon,
			reason: notPNG,
		},
		{
			name: "97 pixels wide", owner: testOwner, icon: "plc", upload: encodeIcon(t, image.NewNRGBA(image.Rect(0, 0, 97, 1))),
			is: ErrInvalid, field: kindIcon, reason: "is 97 x 1 pixels; the limit is 96 x 96",
		},
		{
			name: "97 pixels high", owner: testOwner, icon: "plc", upload: encodeIcon(t, image.NewNRGBA(image.Rect(0, 0, 1, 97))),
			is: ErrInvalid, field: kindIcon, reason: "is 1 x 97 pixels; the limit is 96 x 96",
		},
		{
			// Refused on its header alone: nothing is decoded.
			name: "a header that claims 50000 by 50000 pixels", owner: testOwner, icon: "plc", upload: iconHeader(50000, 50000),
			is: ErrInvalid, field: kindIcon, reason: "is 50000 x 50000 pixels; the limit is 96 x 96",
		},
		{name: "one byte above the upload limit", owner: testOwner, icon: "plc", upload: tooLarge, is: ErrTooLarge},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)

			icon, created, err := h.service.AddIcon(context.Background(), test.owner, test.icon, test.upload)
			if !errors.Is(err, test.is) || icon != nil || created {
				t.Fatalf("AddIcon = %+v, %v, %v; want an error matching %v", icon, created, err, test.is)
			}

			var invalid *ValidationError

			if test.field != "" && (!errors.As(err, &invalid) || invalid.Field != test.field ||
				!strings.HasSuffix(invalid.Reason, test.reason)) {
				t.Fatalf("AddIcon error = %v, want %q %q", err, test.field, test.reason)
			}

			var limit *TooLargeError

			if errors.Is(test.is, ErrTooLarge) &&
				(!errors.As(err, &limit) || limit.Limit != MaxIconUploadBytes || limit.What != kindIcon || limit.Size != MaxIconUploadBytes+1) {
				t.Fatalf("AddIcon error = %v, want the upload limit of an icon", err)
			}

			if got := h.store.Count(NamespaceIcons); got != 0 {
				t.Fatalf("a refused icon left %d records", got)
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
		t.Fatalf("the upload is %d bytes, want the limit", len(upload))
	}

	icon := mustAddIcon(t, h, testOwner, name, upload)

	if icon.Name != name || icon.Width != builder.MaxIconPixels || icon.Height != builder.MaxIconPixels ||
		icon.Bytes > builder.MaxIconBytes {
		t.Fatalf("icon = %q, %d x %d, %d bytes", icon.Name, icon.Width, icon.Height, icon.Bytes)
	}

	// Every character a name may hold.
	mustAddIcon(t, h, testOwner, "Az09_@.-", iconPixel(t, 1))
}

// TestIconLibraryHoldsAtMost64IconsAUser asserts each user may upload at
// most 64 icons, which other users' uploads do not count toward.
func TestIconLibraryHoldsAtMost64IconsAUser(t *testing.T) {
	h := newHarness(t)

	for i := range MaxLibraryIcons {
		mustAddIcon(t, h, testOwner, fmt.Sprintf("icon-%02d", i), iconPixel(t, i))
	}

	icon, created, err := h.service.AddIcon(context.Background(), testOwner, "one-too-many", iconPixel(t, MaxLibraryIcons))

	var invalid *ValidationError

	if !errors.As(err, &invalid) || icon != nil || created || invalid.Field != "icon library" ||
		invalid.Reason != "is full for you: each user may upload at most 64 icons" {
		t.Fatalf("the 65th icon: %+v, %v, %v; want the library refused as full for alice", icon, created, err)
	}

	if got := h.store.Count(NamespaceIcons); got != MaxLibraryIcons {
		t.Fatalf("the library holds %d records, want %d", got, MaxLibraryIcons)
	}

	// An icon the library has is still found, and another user is not held
	// back by alice's uploads.
	if _, created, err := h.service.AddIcon(context.Background(), testOwner, "icon-03", iconPixel(t, 3)); err != nil || created {
		t.Fatalf("an icon of the full library: created = %v, err = %v", created, err)
	}

	mustAddIcon(t, h, testPeer, "theirs", iconPixel(t, MaxLibraryIcons))

	// Deleting one makes room for one.
	if err := h.service.DeleteIcon(context.Background(), testOwner, "icon-00", false); err != nil {
		t.Fatalf("DeleteIcon returned error: %v", err)
	}

	mustAddIcon(t, h, testOwner, "now-it-fits", iconPixel(t, MaxLibraryIcons+1))
}

// TestIconLibraryHoldsAtMostOneMebibyteAUser asserts the icons each user
// uploads take at most 1 MiB together.
func TestIconLibraryHoldsAtMostOneMebibyteAUser(t *testing.T) {
	h := newHarness(t)

	var (
		used    int
		count   int
		refused bool
	)

	// Icons of the most bytes fill a user's share well before it holds the
	// most icons.
	for seed := uint64(1); seed <= MaxLibraryIcons && !refused; seed++ {
		upload := iconNoise(t, seed)

		icon, created, err := h.service.AddIcon(context.Background(), testOwner, fmt.Sprintf("noise-%d", seed), upload)
		if err == nil {
			if !created || icon.Bytes != len(upload) {
				t.Fatalf("icon %d: created = %v, %d bytes, want the %d of the upload", seed, created, icon.Bytes, len(upload))
			}

			used += icon.Bytes
			count++

			continue
		}

		var invalid *ValidationError

		if !errors.As(err, &invalid) || invalid.Field != "icon library" ||
			invalid.Reason != "is full for you: the icons each user uploads may take at most 1048576 bytes" {
			t.Fatalf("icon %d: %v, want the library refused as full of alice's bytes", seed, err)
		}

		if used > MaxLibraryIconBytes || used+len(upload) <= MaxLibraryIconBytes {
			t.Fatalf("refused at %d bytes with %d more to add; the limit is %d", used, len(upload), MaxLibraryIconBytes)
		}

		refused = true
	}

	if !refused || count >= MaxLibraryIcons || count != h.store.Count(NamespaceIcons) {
		t.Fatalf("refused = %v with %d icons in %d records, want a refusal before %d icons",
			refused, count, h.store.Count(NamespaceIcons), MaxLibraryIcons)
	}

	// A small icon still fits, and another user's large one too.
	mustAddIcon(t, h, testOwner, "small", iconPixel(t, 1))
	mustAddIcon(t, h, testPeer, "large", iconNoise(t, 999))
}

// TestIconLibraryHoldsAtMost2000Icons asserts the library holds at most
// MaxIcons icons, of all users together.
func TestIconLibraryHoldsAtMost2000Icons(t *testing.T) {
	h := newHarness(t)

	for i := range MaxIcons {
		data := iconPixel(t, i)
		name := fmt.Sprintf("icon-%04d", i)
		value := iconRecordValue(t, LibraryIcon{
			Kind: "icon", Name: name, ID: builder.IconID(data), Owner: fmt.Sprintf("user-%03d", i/50),
			Width: 1, Height: 1, Bytes: len(data), Created: memrecord.Time(1), Updated: memrecord.Time(1),
			Aliases: []string{}, Data: base64.StdEncoding.EncodeToString(data), Revision: 0,
		})

		if _, err := h.store.CreateRecord(NamespaceIcons, "name/"+name, value); err != nil {
			t.Fatalf("planting icon %d: %v", i, err)
		}
	}

	icon, created, err := h.service.AddIcon(context.Background(), testOwner, "one-too-many", iconPixel(t, MaxIcons))

	var invalid *ValidationError

	if !errors.As(err, &invalid) || icon != nil || created || invalid.Reason != "is full: it holds at most 2000 icons" {
		t.Fatalf("the 2001st icon: %+v, %v, %v; want the library refused as full", icon, created, err)
	}

	var used int

	for i := range 50 {
		used += len(iconPixel(t, i))
	}

	if usage := IconUsage(mustListIcons(t, h), "user-000"); usage != (IconOwnerUsage{Icons: 50, Bytes: used, TotalIcons: MaxIcons}) {
		t.Fatalf("IconUsage = %+v, want 50 icons of %d bytes of %d", usage, used, MaxIcons)
	}
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
		mustAddIcon(t, h, []string{testOwner, testPeer}[i%2], name, iconPixel(t, i))
	}

	if names := iconNames(t, h); !slices.Equal(names, []string{"alpha", "Alpha-2", "beta", "Beta-1", "gamma"}) {
		t.Fatalf("ListIcons = %q, want them by name ignoring case", names)
	}
}

// TestRenameIcon asserts a rename moves the icon to its new name and keeps
// the old one as an alias, also across renames, that a change of case
// renames it in place, and that renaming back to an old name takes it back.
func TestRenameIcon(t *testing.T) {
	h := newHarness(t)
	original := mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 1))

	h.now.Add(10)

	checkFirstRename(t, h, original)
	third := checkSecondRename(t, h)
	cased := checkRenameChangingCase(t, h, third)

	// The same name again changes nothing.
	same, err := h.service.RenameIcon(context.Background(), testOwner, "plc", "plc-3", false)
	if err != nil || same.Revision != cased.Revision {
		t.Fatalf("renaming to its own name = %+v, %v; want nothing written", same, err)
	}

	checkRenameBack(t, h)
}

// checkFirstRename renames the icon plc to plc-v2 and asserts the icon moved
// to its new name, keeping its ID, owner and creation time, and that the old
// name is an alias that names the icon and the library does not list.
func checkFirstRename(t *testing.T, h *testHarness, original *LibraryIcon) {
	t.Helper()

	renamed, err := h.service.RenameIcon(context.Background(), testOwner, "PLC", "plc-v2", false)
	if err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	if renamed.Name != "plc-v2" || !slices.Equal(renamed.Aliases, []string{"plc"}) || renamed.ID != original.ID ||
		renamed.Owner != testOwner || !renamed.Created.Equal(original.Created) || !renamed.Updated.After(original.Updated) {
		t.Fatalf("the renamed icon = %+v, was %+v", renamed, original)
	}

	if alias := storedValue(t, h, "name/plc"); fmt.Sprint(alias) != fmt.Sprint(map[string]any{
		"kind": "alias", "name": "plc", "target": "plc-v2",
	}) {
		t.Fatalf("the old name's record = %v, want an alias of plc-v2", alias)
	}

	if got := mustGetIcon(t, h, "plc"); got.Name != "plc-v2" || got.Revision != renamed.Revision {
		t.Fatalf("the old name names %+v, want the renamed icon", got)
	}

	if names := iconNames(t, h); !slices.Equal(names, []string{"plc-v2"}) {
		t.Fatalf("the library lists %q, want the icon once", names)
	}
}

// checkSecondRename renames the icon plc-v2 to PLC-3 and asserts the rename
// points both old names at the new one, so the oldest name names the icon
// through one alias. It returns the renamed icon.
func checkSecondRename(t *testing.T, h *testHarness) *LibraryIcon {
	t.Helper()

	third, err := h.service.RenameIcon(context.Background(), testOwner, "plc-v2", "PLC-3", false)
	if err != nil || !slices.Equal(third.Aliases, []string{"plc", "plc-v2"}) {
		t.Fatalf("the second rename = %+v, %v", third, err)
	}

	for _, name := range []string{"plc", "plc-v2"} {
		if alias := storedValue(t, h, iconKey(name)); fmt.Sprint(alias) != fmt.Sprint(map[string]any{
			"kind": "alias", "name": name, "target": "plc-3",
		}) {
			t.Fatalf("the record of %s after two renames = %v, want an alias of plc-3", name, alias)
		}
	}

	if got := mustGetIcon(t, h, "plc"); got.Name != "PLC-3" {
		t.Fatalf("the first name names %q after two renames, want PLC-3", got.Name)
	}

	return third
}

// checkRenameChangingCase renames the icon PLC-3 to plc-3 and asserts a change
// of case renames the icon in place: the aliases and the records stay as they
// were, and the icon gets a new revision. It returns the renamed icon.
func checkRenameChangingCase(t *testing.T, h *testHarness, third *LibraryIcon) *LibraryIcon {
	t.Helper()

	keys := h.store.Keys(NamespaceIcons)

	cased, err := h.service.RenameIcon(context.Background(), testOwner, "plc-3", "plc-3", false)
	if err != nil || cased.Name != "plc-3" || !slices.Equal(cased.Aliases, third.Aliases) || cased.Revision == third.Revision {
		t.Fatalf("the change of case = %+v, %v", cased, err)
	}

	if after := h.store.Keys(NamespaceIcons); !slices.Equal(after, keys) {
		t.Fatalf("a change of case changed the records from %q to %q", keys, after)
	}

	return cased
}

// checkRenameBack renames the icon plc-3 back to its first name, plc, and
// asserts the rename takes the name back from the alias: plc is the icon's
// record again, every old name names the icon, and the other two old names
// are aliases of plc.
func checkRenameBack(t *testing.T, h *testHarness) {
	t.Helper()

	back, err := h.service.RenameIcon(context.Background(), testOwner, "plc-3", "plc", false)
	if err != nil || back.Name != "plc" || !slices.Equal(back.Aliases, []string{"plc-v2", "plc-3"}) {
		t.Fatalf("renaming back = %+v, %v", back, err)
	}

	if stored := storedValue(t, h, "name/plc"); stored["kind"] != "icon" {
		t.Fatalf("the first name's record = %v, want the icon", stored)
	}

	for _, name := range []string{"plc-v2", "PLC-3", "plc"} {
		if got := mustGetIcon(t, h, name); got.Name != "plc" {
			t.Errorf("%s names %q, want plc", name, got.Name)
		}
	}

	if got := h.store.Count(NamespaceIcons); got != 3 {
		t.Fatalf("%d records, want the icon and two aliases", got)
	}

	for _, name := range []string{"plc-v2", "plc-3"} {
		if alias := storedValue(t, h, iconKey(name)); alias["target"] != "plc" {
			t.Errorf("the record of %s after renaming back = %v, want an alias of plc", name, alias)
		}
	}
}

// TestRenameIconManyTimes asserts every rename points each alias of the icon
// at its new name: after 20 renames, more than the aliases a name is
// followed through (maxAliasHops), every old name, the first included, is an
// alias of the icon's name now, names the icon, and stays reserved.
func TestRenameIconManyTimes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	icon := mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 1))

	const renames = 20

	names := []string{"plc"}

	for i := 1; i <= renames; i++ {
		name := fmt.Sprintf("plc-%d", i)

		renamed, err := h.service.RenameIcon(ctx, testOwner, names[len(names)-1], name, false)
		if err != nil {
			t.Fatalf("rename %d returned error: %v", i, err)
		}

		if !slices.Equal(renamed.Aliases, names) {
			t.Fatalf("after rename %d the aliases are %q, want %q", i, renamed.Aliases, names)
		}

		names = append(names, name)
	}

	last := names[len(names)-1]

	for _, name := range names[:renames] {
		if alias := storedValue(t, h, iconKey(name)); alias["kind"] != "alias" || alias["target"] != last {
			t.Errorf("the record of %s = %v, want an alias of %s", name, alias, last)
		}
	}

	for _, name := range names {
		if got := mustGetIcon(t, h, name); got.Name != last || got.ID != icon.ID {
			t.Errorf("%s names %+v, want the icon as %s", name, got, last)
		}
	}

	if got := h.store.Count(NamespaceIcons); got != renames+1 {
		t.Fatalf("%d records, want the icon and %d aliases", got, renames)
	}

	// The first name is still taken, by the icon it names.
	var taken *IconNameTakenError

	if _, _, err := h.service.AddIcon(ctx, testPeer, "PLC", iconPixel(t, 2)); !errors.As(err, &taken) || taken.Icon != last {
		t.Fatalf("adding an icon under the first name = %v, want it taken by %s", err, last)
	}
}

// TestRenameIconRetargetFailure asserts a rename that cannot point an older
// alias at the new name still renames the icon, the alias still names it
// through the alias it points at, and the next rename points it at the icon.
func TestRenameIconRetargetFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 1))

	if _, err := h.service.RenameIcon(ctx, testOwner, "plc", "plc-2", false); err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	h.store.BeforeUpdate = func(_, key string) error {
		if key == "name/plc" {
			return fmt.Errorf("injected: %w", store.ErrRecordConflict)
		}

		return nil
	}

	renamed, err := h.service.RenameIcon(ctx, testOwner, "plc-2", "plc-3", false)
	if err != nil || renamed.Name != "plc-3" || !slices.Equal(renamed.Aliases, []string{"plc", "plc-2"}) {
		t.Fatalf("a rename whose retargeting failed = %+v, %v; want the icon renamed", renamed, err)
	}

	h.store.BeforeUpdate = nil

	if alias := storedValue(t, h, "name/plc"); alias["target"] != "plc-2" {
		t.Fatalf("the alias that could not be retargeted = %v, want it as it was", alias)
	}

	if got := mustGetIcon(t, h, "plc"); got.Name != "plc-3" {
		t.Fatalf("the first name names %q, want plc-3", got.Name)
	}

	if _, err := h.service.RenameIcon(ctx, testOwner, "plc-3", "plc-4", false); err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	for _, name := range []string{"plc", "plc-2", "plc-3"} {
		if alias := storedValue(t, h, iconKey(name)); alias["target"] != "plc-4" {
			t.Errorf("the record of %s after the next rename = %v, want an alias of plc-4", name, alias)
		}
	}
}

func TestRenameIconRefuses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 1))
	mustAddIcon(t, h, testPeer, "hmi", iconPixel(t, 2))

	if _, err := h.service.RenameIcon(ctx, testPeer, "hmi", "hmi-old", false); err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	before := h.store.Keys(NamespaceIcons)

	var taken *IconNameTakenError

	for _, test := range []struct {
		name, from, to string
		check          func(error) bool
	}{
		{name: "another icon's name", from: "plc", to: "HMI-OLD", check: func(err error) bool {
			return errors.As(err, &taken) && taken.Icon == "hmi-old" && taken.Owner == testPeer
		}},
		{name: "another icon's alias", from: "plc", to: "Hmi", check: func(err error) bool {
			return errors.As(err, &taken) && taken.Icon == "hmi-old"
		}},
		{name: "a name that is none", from: "plc", to: "plc v2", check: func(err error) bool {
			var invalid *ValidationError

			return errors.As(err, &invalid) && invalid.Field == "icon name"
		}},
		{name: "no name", from: "plc", to: "", check: func(err error) bool { return errors.Is(err, ErrInvalid) }},
		{name: "an icon nobody has", from: "scada", to: "scada-2", check: func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{name: "a path", from: "../plc", to: "scada-2", check: func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{name: "another user's icon", from: "plc", to: "plc-2", check: func(err error) bool {
			return errors.Is(err, ErrForbidden)
		}},
	} {
		caller := testOwner
		if test.name == "another user's icon" {
			caller = testPeer
		}

		if icon, err := h.service.RenameIcon(ctx, caller, test.from, test.to, false); icon != nil || !test.check(err) {
			t.Errorf("%s: RenameIcon = %+v, %v", test.name, icon, err)
		}
	}

	if after := h.store.Keys(NamespaceIcons); !slices.Equal(after, before) {
		t.Fatalf("a refused rename changed the records from %q to %q", before, after)
	}

	// The builder-icons permission renames any user's icon.
	icon, err := h.service.RenameIcon(ctx, testPeer, "plc", "plc-2", true)
	if err != nil || icon.Owner != testOwner || icon.Name != "plc-2" {
		t.Fatalf("renaming another user's icon with the permission = %+v, %v", icon, err)
	}
}

// TestRenameIconRace asserts a rename whose old record changed between its
// two writes undoes the first and is a conflict.
func TestRenameIconRace(t *testing.T) {
	h := newHarness(t)
	icon := mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 1))

	h.store.BeforeUpdate = func(_, key string) error {
		if key == "name/plc" {
			return fmt.Errorf("injected: %w", store.ErrRecordConflict)
		}

		return nil
	}

	renamed, err := h.service.RenameIcon(context.Background(), testOwner, "plc", "plc-2", false)
	if !errors.Is(err, ErrConflict) || renamed != nil {
		t.Fatalf("a rename that lost a race = %+v, %v; want a conflict", renamed, err)
	}

	h.store.BeforeUpdate = nil

	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/plc"}) {
		t.Fatalf("after the lost race the records are %q, want the icon's alone", keys)
	}

	if got := mustGetIcon(t, h, "plc"); !sameIcon(*got, *icon) {
		t.Fatalf("after the lost race the icon is %+v, want %+v", got, icon)
	}
}

// TestDeleteIconRemovesAliases asserts deleting an icon, by any of its
// names, removes its record and every alias that names it, and nothing
// else.
func TestDeleteIconRemovesAliases(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 1))
	mustAddIcon(t, h, testPeer, "hmi", iconPixel(t, 2))

	for _, rename := range [][2]string{{"plc", "plc-2"}, {"plc-2", "plc-3"}, {"hmi", "hmi-2"}} {
		if _, err := h.service.RenameIcon(ctx, testOwner, rename[0], rename[1], true); err != nil {
			t.Fatalf("RenameIcon(%s) returned error: %v", rename[0], err)
		}
	}

	// An alias the icon no longer lists, whose chain still ends at it.
	h.store.SetValue(NamespaceIcons, "name/plc-3", iconRecordValue(t, func() LibraryIcon {
		icon := *mustGetIcon(t, h, "plc-3")
		icon.Aliases = []string{"plc-2"}

		return icon
	}()))

	// A user other than the uploader may not delete it.
	if err := h.service.DeleteIcon(ctx, testPeer, "plc", false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("deleting another user's icon = %v, want ErrForbidden", err)
	}

	if err := h.service.DeleteIcon(ctx, testOwner, "PLC", false); err != nil {
		t.Fatalf("deleting the icon through its first name: %v", err)
	}

	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/hmi", "name/hmi-2"}) {
		t.Fatalf("after the delete the records are %q, want only the other icon's", keys)
	}

	for _, name := range []string{"plc", "plc-2", "plc-3"} {
		if _, err := h.service.GetIcon(ctx, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetIcon(%s) after the delete = %v, want ErrNotFound", name, err)
		}
	}

	// The builder-icons permission deletes any user's icon.
	if err := h.service.DeleteIcon(ctx, testOwner, "hmi", true); err != nil || h.store.Count(NamespaceIcons) != 0 {
		t.Fatalf("deleting another user's icon with the permission = %v, %d records left", err, h.store.Count(NamespaceIcons))
	}
}

func TestDeleteIconRefuses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	icon := mustAddIcon(t, h, testOwner, "kept", iconPixel(t, 5))

	for _, name := range []string{"", "missing", icon.ID, "name/kept", "../kept", "kept/", "kept ", OwnerScope(testOwner)} {
		if err := h.service.DeleteIcon(ctx, testOwner, name, true); !errors.Is(err, ErrNotFound) {
			t.Errorf("DeleteIcon(%q) = %v, want ErrNotFound", name, err)
		}
	}

	if err := h.service.DeleteIcon(ctx, "", "kept", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteIcon without a caller = %v, want ErrInvalid", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 1 {
		t.Fatalf("%d icon records, want the one that was kept", got)
	}
}

// TestAddIconOverDanglingAlias asserts an alias whose icon is gone does not
// keep its name from a new icon.
func TestAddIconOverDanglingAlias(t *testing.T) {
	h := newHarness(t)

	if _, err := h.store.CreateRecord(NamespaceIcons, "name/old", []byte(`{"kind":"alias","name":"old","target":"gone"}`)); err != nil {
		t.Fatalf("planting the alias: %v", err)
	}

	if _, err := h.service.GetIcon(context.Background(), "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetIcon of a dangling alias = %v, want ErrNotFound", err)
	}

	icon := mustAddIcon(t, h, testPeer, "OLD", iconPixel(t, 3))

	if stored := storedValue(t, h, "name/old"); stored["kind"] != "icon" || mustGetIcon(t, h, "old").Revision != icon.Revision {
		t.Fatalf("the name's record = %v, want the new icon", stored)
	}
}

// TestListIconsSkipsDamagedRecords asserts a record that is not an icon or
// an alias in every respect is never returned, is logged without its
// content, and hides no other icon; and that only a holder of the delete
// permission removes it.
func TestListIconsSkipsDamagedRecords(t *testing.T) {
	good := iconPixel(t, 6)
	other := iconPixel(t, 7)

	record := func(change func(*LibraryIcon)) []byte {
		icon := LibraryIcon{
			Kind:     "icon",
			Name:     "planted",
			ID:       builder.IconID(other),
			Owner:    testOwner,
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
		{name: "no owner", value: record(func(i *LibraryIcon) { i.Owner = "" }), reason: "names no usable owner"},
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
			kept := mustAddIcon(t, h, testOwner, "kept", good)

			if _, err := h.store.CreateRecord(NamespaceIcons, "name/planted", test.value); err != nil {
				t.Fatalf("planting the record: %v", err)
			}

			logs := plogtest.Capture(t)

			icons, err := h.service.ListIcons(ctx)
			if err != nil {
				t.Fatalf("ListIcons returned error: %v", err)
			}

			if len(icons) != 1 || !sameIcon(icons[0], *kept) {
				t.Fatalf("ListIcons = %+v, want only the icon that was added", icons)
			}

			logged := logs.Records(t, plogtest.Level(slog.LevelError))
			if len(logged) != 1 || logged[0]["msg"] != "skipping unreadable builder icon" || logged[0]["icon"] != "name/planted" {
				t.Fatalf("logged %v, want one error naming the record", logged)
			}

			if reason, _ := logged[0]["err"].(string); !strings.Contains(reason, test.reason) {
				t.Fatalf("logged reason %q, want %q", reason, test.reason)
			}

			// What the record holds is never logged.
			for _, content := range []string{"svg", "alert", "hidden", "iVBOR"} {
				if strings.Contains(logs.String(), content) {
					t.Fatalf("the log holds %q of the record: %s", content, logs.String())
				}
			}

			// The name is taken by what cannot be read, which only the
			// delete permission removes.
			if icon, created, err := h.service.AddIcon(ctx, testOwner, "planted", other); !errors.Is(err, ErrCorrupt) ||
				icon != nil || created {
				t.Fatalf("adding an icon of the record's name: %+v, %v, %v; want ErrCorrupt", icon, created, err)
			}

			if err := h.service.DeleteIcon(ctx, testOwner, "planted", false); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("deleting the damaged record without the permission = %v, want ErrCorrupt", err)
			}

			if err := h.service.DeleteIcon(ctx, testPeer, "planted", true); err != nil {
				t.Fatalf("deleting the damaged record: %v", err)
			}

			mustAddIcon(t, h, testOwner, "planted", other)
		})
	}
}

// TestListIconsTrustsOnlyTheImage asserts the size an icon is listed with,
// and its base64, come from its PNG and not from the record.
func TestListIconsTrustsOnlyTheImage(t *testing.T) {
	h := newHarness(t)
	icon := mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 8))

	lied := *icon
	lied.Width, lied.Height, lied.Bytes = 5000, 5000, 1
	// The decoder skips line breaks, which a document refuses.
	lied.Data = icon.Data[:8] + "\r\n" + icon.Data[8:]

	h.store.SetValue(NamespaceIcons, "name/plc", iconRecordValue(t, lied))

	icons := mustListIcons(t, h)

	if len(icons) != 1 || icons[0].Width != 1 || icons[0].Height != 1 || icons[0].Bytes != icon.Bytes ||
		icons[0].Data != icon.Data {
		t.Fatalf("ListIcons = %+v, want %+v", icons, *icon)
	}
}

// TestAddIconRacingUpload asserts two requests that add an icon under the
// same name at once: with the same image both succeed and one icon is
// stored; with another the second is refused as taken.
func TestAddIconRacingUpload(t *testing.T) {
	h := newHarness(t)
	upload := iconPixel(t, 9)

	var first *LibraryIcon

	// Another request stores the icon between this one's check and its
	// write.
	h.store.BeforeCreate = func(string, string) error {
		h.store.BeforeCreate = nil
		first = mustAddIcon(t, h, testOwner, "plc", upload)

		return nil
	}

	second, created, err := h.service.AddIcon(context.Background(), testPeer, "PLC", upload)
	if err != nil || created || first == nil || !sameIcon(*second, *first) {
		t.Fatalf("the second upload: %+v, created = %v, err = %v; want %+v", second, created, err, first)
	}

	h.store.BeforeCreate = func(string, string) error {
		h.store.BeforeCreate = nil
		mustAddIcon(t, h, testOwner, "hmi", iconPixel(t, 10))

		return nil
	}

	var taken *IconNameTakenError

	if _, _, err := h.service.AddIcon(context.Background(), testPeer, "hmi", iconPixel(t, 11)); !errors.As(err, &taken) {
		t.Fatalf("another image under a name just taken = %v, want the name refused as taken", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 2 {
		t.Fatalf("%d icon records, want 2", got)
	}
}

func TestIconLibraryStoreFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// What the Etcd store returns once etcd is out of space.
	h.store.BeforeCreate = func(namespace, key string) error {
		return fmt.Errorf("creating record %s/%s in Etcd: %w", namespace, key, store.ErrNoSpace)
	}

	if _, _, err := h.service.AddIcon(ctx, testOwner, "plc", iconPixel(t, 10)); !errors.Is(err, store.ErrNoSpace) {
		t.Fatalf("AddIcon = %v, want the store's out of space error", err)
	}

	h.store.BeforeCreate = nil
	mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 10))

	refused := errors.New("injected delete failure")
	h.store.FailDelete = func(string, string) error { return refused }

	if err := h.service.DeleteIcon(ctx, testOwner, "plc", false); !errors.Is(err, refused) {
		t.Fatalf("DeleteIcon = %v, want the store's error", err)
	}

	h.store.FailDelete = nil

	// The icon's record goes, and an alias that stays is reported.
	if _, err := h.service.RenameIcon(ctx, testOwner, "plc", "plc-2", false); err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	h.store.FailDelete = func(_, key string) error {
		if key == "name/plc" {
			return refused
		}

		return nil
	}

	if err := h.service.DeleteIcon(ctx, testOwner, "plc-2", false); !errors.Is(err, ErrCleanup) || !errors.Is(err, refused) {
		t.Fatalf("DeleteIcon with an alias that cannot be removed = %v, want a cleanup error", err)
	}

	h.store.FailDelete = nil

	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/plc"}) {
		t.Fatalf("after the failed cleanup the records are %q, want the alias alone", keys)
	}

	// The alias left behind names nothing, and a new icon may take its name.
	mustAddIcon(t, h, testPeer, "plc", iconPixel(t, 12))

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := h.service.ListIcons(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("ListIcons with a canceled context = %v", err)
	}

	if _, err := h.service.GetIcon(canceled, "plc"); !errors.Is(err, context.Canceled) {
		t.Errorf("GetIcon with a canceled context = %v", err)
	}

	if _, _, err := h.service.AddIcon(canceled, testOwner, "x", iconPixel(t, 11)); !errors.Is(err, context.Canceled) {
		t.Errorf("AddIcon with a canceled context = %v", err)
	}

	if _, err := h.service.RenameIcon(canceled, testOwner, "plc", "y", true); !errors.Is(err, context.Canceled) {
		t.Errorf("RenameIcon with a canceled context = %v", err)
	}

	if err := h.service.DeleteIcon(canceled, testOwner, "plc", true); !errors.Is(err, context.Canceled) {
		t.Errorf("DeleteIcon with a canceled context = %v", err)
	}

	if _, err := h.service.CleanupLegacyIcons(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("CleanupLegacyIcons with a canceled context = %v", err)
	}
}

// TestIconLibraryCleanups asserts the cleanups a server start runs leave the
// icon library alone, but for the records of the per-user layout of earlier
// builds, which CleanupLegacyIcons removes.
func TestIconLibraryCleanups(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	icon := mustAddIcon(t, h, testOwner, "kept", iconPixel(t, 12))

	if _, err := h.service.RenameIcon(ctx, testOwner, "kept", "kept-2", false); err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	legacy := OwnerScope(testOwner) + "/" + strings.Repeat("a", 64)

	if _, err := h.store.CreateRecord(NamespaceIcons, legacy, []byte(`{"id":"x"}`)); err != nil {
		t.Fatalf("planting a record of the per-user layout: %v", err)
	}

	if _, err := h.service.CleanupOrphanedChunks(ctx); err != nil {
		t.Fatalf("CleanupOrphanedChunks returned error: %v", err)
	}

	if _, err := h.service.CleanupOrphanedDocuments(ctx, nil); err != nil {
		t.Fatalf("CleanupOrphanedDocuments returned error: %v", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 3 {
		t.Fatalf("%d icon records after the other cleanups, want 3", got)
	}

	removed, err := h.service.CleanupLegacyIcons(ctx)
	if err != nil || removed != 1 {
		t.Fatalf("CleanupLegacyIcons = %d, %v; want the one record removed", removed, err)
	}

	if keys := h.store.Keys(NamespaceIcons); !slices.Equal(keys, []string{"name/kept", "name/kept-2"}) {
		t.Fatalf("after the cleanup the records are %q", keys)
	}

	if got := mustGetIcon(t, h, "kept"); got.ID != icon.ID {
		t.Fatalf("after the cleanup kept names %+v", got)
	}

	if removed, err := h.service.CleanupLegacyIcons(ctx); err != nil || removed != 0 {
		t.Fatalf("CleanupLegacyIcons again = %d, %v; want nothing removed", removed, err)
	}
}
