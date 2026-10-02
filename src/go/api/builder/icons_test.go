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
	"strings"
	"testing"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
	"phenix/util/plog/plogtest"
)

// iconPixel returns a strict PNG of one pixel whose color depends on seed,
// so every seed gives another icon.
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
	if !got.Created.Equal(want.Created) {
		return false
	}

	got.Created = want.Created

	return got == want
}

// mustAddIcon adds an icon that the library must not have yet.
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

// iconNames returns the names of the owner's icons, in the order they are
// listed.
func iconNames(t *testing.T, h *testHarness, owner string) []string {
	t.Helper()

	icons, err := h.service.ListIcons(context.Background(), owner)
	if err != nil {
		t.Fatalf("ListIcons(%q) returned error: %v", owner, err)
	}

	names := make([]string, 0, len(icons))

	for i := range icons {
		names = append(names, icons[i].Name)
	}

	return names
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

	if names := iconNames(t, h, testOwner); len(names) != 0 {
		t.Fatalf("a new library lists %q, want nothing", names)
	}

	upload := iconPixel(t, 1)
	before := bytes.Clone(upload)

	icon := mustAddIcon(t, h, testOwner, "  plc  ", upload)

	// A PNG a document accepts is stored as it is, so it keeps its ID.
	want := LibraryIcon{
		ID:      builder.IconID(upload),
		Owner:   testOwner,
		Name:    "plc",
		Width:   1,
		Height:  1,
		Bytes:   len(upload),
		Created: icon.Created,
		Data:    base64.StdEncoding.EncodeToString(upload),
	}

	if !sameIcon(*icon, want) {
		t.Fatalf("AddIcon returned %+v, want %+v", *icon, want)
	}

	if icon.Created.IsZero() || icon.Created.Location().String() != "UTC" {
		t.Fatalf("created = %v, want a time in UTC", icon.Created)
	}

	if !bytes.Equal(upload, before) {
		t.Fatal("AddIcon changed the bytes it was given")
	}

	// One record, in the library's own namespace, under the owner's scope.
	_, digits, _ := strings.Cut(icon.ID, ":")
	key := OwnerScope(testOwner) + "/" + digits

	if keys := h.store.Keys(NamespaceIcons); len(keys) != 1 || keys[0] != key {
		t.Fatalf("icon records = %q, want only %q", keys, key)
	}

	for _, namespace := range []string{NamespaceDrafts, NamespaceChunks, NamespacePublished} {
		if got := h.store.Count(namespace); got != 0 {
			t.Errorf("%s holds %d records, want 0", namespace, got)
		}
	}

	record, err := h.store.GetRecord(NamespaceIcons, key)
	if err != nil {
		t.Fatalf("reading the icon record: %v", err)
	}

	var stored map[string]any

	if err := json.Unmarshal(record.Value, &stored); err != nil {
		t.Fatalf("the icon record is not JSON: %v", err)
	}

	for _, field := range []string{"id", "owner", "name", "width", "height", "bytes", "created", "data"} {
		if _, ok := stored[field]; !ok {
			t.Errorf("the icon record has no %q", field)
		}
	}

	if len(stored) != 8 {
		t.Errorf("the icon record has %d fields, want 8: %v", len(stored), stored)
	}

	icons, err := h.service.ListIcons(ctx, testOwner)
	if err != nil {
		t.Fatalf("ListIcons returned error: %v", err)
	}

	if len(icons) != 1 || !sameIcon(icons[0], want) {
		t.Fatalf("ListIcons = %+v, want only %+v", icons, want)
	}

	// The same bytes again are the icon the library has, under the name it
	// has: nothing is written.
	again, created, err := h.service.AddIcon(ctx, testOwner, "another name", upload)
	if err != nil || created {
		t.Fatalf("adding the same bytes: created = %v, err = %v, want the stored icon", created, err)
	}

	if !sameIcon(*again, want) || record.Revision != mustIconRevision(t, h, key) {
		t.Fatalf("adding the same bytes returned %+v, want %+v unchanged", *again, want)
	}

	if err := h.service.DeleteIcon(ctx, testOwner, icon.ID); err != nil {
		t.Fatalf("DeleteIcon returned error: %v", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 0 {
		t.Fatalf("%d icon records after the delete, want 0", got)
	}

	if err := h.service.DeleteIcon(ctx, testOwner, icon.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting it again: %v, want ErrNotFound", err)
	}
}

// mustIconRevision returns the revision of an icon record.
func mustIconRevision(t *testing.T, h *testHarness, key string) int64 {
	t.Helper()

	record, err := h.store.GetRecord(NamespaceIcons, key)
	if err != nil {
		t.Fatalf("reading the icon record: %v", err)
	}

	return record.Revision
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

			// The same upload again is the same icon.
			again, created, err := h.service.AddIcon(context.Background(), testOwner, "", test.upload)
			if err != nil || created || again.ID != icon.ID {
				t.Fatalf("the same upload again: created = %v, err = %v, want icon %s", created, err, icon.ID)
			}

			// Nothing of the upload but its pixels is in the store.
			record, err := h.store.GetRecord(NamespaceIcons, h.store.Keys(NamespaceIcons)[0])
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
	notPNG := "is not a PNG image"

	tests := []struct {
		name   string
		owner  string
		icon   string
		upload []byte
		is     error
		field  string
		reason string
	}{
		{name: "no owner", owner: "", upload: pixel, is: ErrInvalid, field: "owner", reason: "must not be empty"},
		{
			name: "a name of 65 bytes", owner: testOwner, icon: strings.Repeat("n", builder.MaxIconNameBytes+1), upload: pixel,
			is: ErrInvalid, field: "icon name", reason: "must be at most 64 bytes and contain no control characters",
		},
		{
			name: "a name with a line break", owner: testOwner, icon: "two\nlines", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: "must be at most 64 bytes and contain no control characters",
		},
		{
			name: "a name that is not text", owner: testOwner, icon: "\xff\xfe", upload: pixel,
			is: ErrInvalid, field: "icon name", reason: "must be at most 64 bytes and contain no control characters",
		},
		{name: "nothing", owner: testOwner, upload: nil, is: ErrInvalid, field: kindIcon, reason: notPNG},
		{name: "a JPEG", owner: testOwner, upload: photo.Bytes(), is: ErrInvalid, field: kindIcon, reason: notPNG},
		{
			name: "an SVG", owner: testOwner, upload: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
			is: ErrInvalid, field: kindIcon, reason: notPNG,
		},
		{name: "a PNG cut short", owner: testOwner, upload: pixel[:len(pixel)-20], is: ErrInvalid, field: kindIcon, reason: notPNG},
		{name: "a header and no pixels", owner: testOwner, upload: iconHeader(1, 1), is: ErrInvalid, field: kindIcon, reason: notPNG},
		{
			name: "97 pixels wide", owner: testOwner, upload: encodeIcon(t, image.NewNRGBA(image.Rect(0, 0, 97, 1))),
			is: ErrInvalid, field: kindIcon, reason: "is 97 x 1 pixels; the limit is 96 x 96",
		},
		{
			name: "97 pixels high", owner: testOwner, upload: encodeIcon(t, image.NewNRGBA(image.Rect(0, 0, 1, 97))),
			is: ErrInvalid, field: kindIcon, reason: "is 1 x 97 pixels; the limit is 96 x 96",
		},
		{
			// Refused on its header alone: nothing is decoded.
			name: "a header that claims 50000 by 50000 pixels", owner: testOwner, upload: iconHeader(50000, 50000),
			is: ErrInvalid, field: kindIcon, reason: "is 50000 x 50000 pixels; the limit is 96 x 96",
		},
		{name: "one byte above the upload limit", owner: testOwner, upload: tooLarge, is: ErrTooLarge},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)

			icon, created, err := h.service.AddIcon(context.Background(), test.owner, test.icon, test.upload)
			if !errors.Is(err, test.is) || icon != nil || created {
				t.Fatalf("AddIcon = %+v, %v, %v; want an error matching %v", icon, created, err, test.is)
			}

			var invalid *ValidationError

			if test.field != "" && (!errors.As(err, &invalid) || invalid.Field != test.field || invalid.Reason != test.reason) {
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
	name := strings.Repeat("ñ", builder.MaxIconNameBytes/2)
	plain := iconNoise(t, 7)
	text := strings.Repeat("t", MaxIconUploadBytes-len(plain)-len("Comment\x00")-12)
	upload := iconWithText(t, plain, text)

	if len(name) != builder.MaxIconNameBytes || len(upload) != MaxIconUploadBytes {
		t.Fatalf("the name is %d bytes and the upload %d, want the limits", len(name), len(upload))
	}

	icon := mustAddIcon(t, h, testOwner, name, upload)

	if icon.Name != name || icon.Width != builder.MaxIconPixels || icon.Height != builder.MaxIconPixels ||
		icon.Bytes > builder.MaxIconBytes {
		t.Fatalf("icon = %q, %d x %d, %d bytes", icon.Name, icon.Width, icon.Height, icon.Bytes)
	}
}

func TestIconLibraryHoldsAtMost64Icons(t *testing.T) {
	h := newHarness(t)

	for i := range MaxLibraryIcons {
		mustAddIcon(t, h, testOwner, fmt.Sprintf("icon %02d", i), iconPixel(t, i))
	}

	icon, created, err := h.service.AddIcon(context.Background(), testOwner, "one too many", iconPixel(t, MaxLibraryIcons))

	var invalid *ValidationError

	if !errors.As(err, &invalid) || icon != nil || created ||
		invalid.Field != "icon library" || invalid.Reason != "is full: at most 64 icons" {
		t.Fatalf("the 65th icon: %+v, %v, %v; want the library refused as full", icon, created, err)
	}

	if got := h.store.Count(NamespaceIcons); got != MaxLibraryIcons {
		t.Fatalf("the library holds %d records, want %d", got, MaxLibraryIcons)
	}

	// An icon the library has is still found, and another user is not held
	// back by this library.
	if _, created, err := h.service.AddIcon(context.Background(), testOwner, "", iconPixel(t, 3)); err != nil || created {
		t.Fatalf("an icon of the full library: created = %v, err = %v", created, err)
	}

	mustAddIcon(t, h, testPeer, "theirs", iconPixel(t, MaxLibraryIcons))

	// Deleting one makes room for one.
	if err := h.service.DeleteIcon(context.Background(), testOwner, builder.IconID(iconPixel(t, 0))); err != nil {
		t.Fatalf("DeleteIcon returned error: %v", err)
	}

	mustAddIcon(t, h, testOwner, "now it fits", iconPixel(t, MaxLibraryIcons))
}

func TestIconLibraryHoldsAtMostOneMebibyte(t *testing.T) {
	h := newHarness(t)

	var (
		used    int
		count   int
		refused bool
	)

	// Icons of the most bytes fill the library well before it holds the
	// most icons.
	for seed := uint64(1); seed <= MaxLibraryIcons && !refused; seed++ {
		upload := iconNoise(t, seed)

		icon, created, err := h.service.AddIcon(context.Background(), testOwner, "noise", upload)
		if err == nil {
			if !created || icon.Bytes != len(upload) {
				t.Fatalf("icon %d: created = %v, %d bytes, want the %d of the upload", seed, created, icon.Bytes, len(upload))
			}

			used += icon.Bytes
			count++

			continue
		}

		var invalid *ValidationError

		if !errors.As(err, &invalid) || invalid.Field != "icon library" || invalid.Reason != "is full: at most 1048576 bytes" {
			t.Fatalf("icon %d: %v, want the library refused as full of bytes", seed, err)
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

	// A small icon still fits.
	mustAddIcon(t, h, testOwner, "small", iconPixel(t, 1))
}

func TestIconLibrariesAreSeparate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// "a" is a prefix of "a/b", and a draft owner may be named either.
	owners := []string{"a", "a/b", "a/", testOwner, testPeer}
	shared := iconPixel(t, 99)

	for i, owner := range owners {
		mustAddIcon(t, h, owner, "own of "+owner, iconPixel(t, i))
		mustAddIcon(t, h, owner, "shared of "+owner, shared)
	}

	for _, owner := range owners {
		want := []string{"own of " + owner, "shared of " + owner}

		if got := iconNames(t, h, owner); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("icons of %q = %q, want %q", owner, got, want)
		}
	}

	// An icon of another user is not there to delete, and stays.
	theirs := builder.IconID(iconPixel(t, 1))

	if err := h.service.DeleteIcon(ctx, "a", theirs); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting another user's icon: %v, want ErrNotFound", err)
	}

	// Deleting the image two users both have removes it for one of them.
	if err := h.service.DeleteIcon(ctx, "a", builder.IconID(shared)); err != nil {
		t.Fatalf("DeleteIcon returned error: %v", err)
	}

	if got := iconNames(t, h, "a"); len(got) != 1 {
		t.Errorf("icons of a = %q, want one", got)
	}

	if got := iconNames(t, h, "a/b"); len(got) != 2 {
		t.Errorf("icons of a/b = %q, want both", got)
	}

	if got := h.store.Count(NamespaceIcons); got != 2*len(owners)-1 {
		t.Errorf("%d icon records, want %d", got, 2*len(owners)-1)
	}
}

func TestListIconsOrder(t *testing.T) {
	h := newHarness(t)

	for i, name := range []string{"beta", "Alpha", "", "alpha", "Beta", "alpha"} {
		mustAddIcon(t, h, testOwner, name, iconPixel(t, i))
	}

	icons, err := h.service.ListIcons(context.Background(), testOwner)
	if err != nil {
		t.Fatalf("ListIcons returned error: %v", err)
	}

	for i := 1; i < len(icons); i++ {
		a, b := icons[i-1], icons[i]
		left, right := strings.ToLower(a.Name), strings.ToLower(b.Name)

		if left > right || (left == right && a.ID >= b.ID) {
			t.Errorf("%q (%s) is listed before %q (%s)", a.Name, a.ID, b.Name, b.ID)
		}
	}

	if len(icons) != 6 || icons[0].Name != "" || strings.ToLower(icons[5].Name) != "beta" {
		t.Fatalf("ListIcons = %q", iconNames(t, h, testOwner))
	}
}

func TestDeleteIconRefuses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	icon := mustAddIcon(t, h, testOwner, "kept", iconPixel(t, 5))
	_, digits, _ := strings.Cut(icon.ID, ":")

	for _, id := range []string{
		"",
		digits,
		strings.ToUpper(icon.ID),
		icon.ID + "0",
		icon.ID[:len(icon.ID)-1],
		"md5:" + digits,
		"../" + digits,
		OwnerScope(testOwner) + "/" + digits,
	} {
		if err := h.service.DeleteIcon(ctx, testOwner, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("DeleteIcon(%q) = %v, want ErrNotFound", id, err)
		}
	}

	if err := h.service.DeleteIcon(ctx, "", icon.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteIcon without an owner = %v, want ErrInvalid", err)
	}

	if got := h.store.Count(NamespaceIcons); got != 1 {
		t.Fatalf("%d icon records, want the one that was kept", got)
	}
}

// TestListIconsSkipsDamagedRecords asserts a record that is not the owner's
// icon in every respect is never returned, is logged without its content,
// and hides no other icon.
func TestListIconsSkipsDamagedRecords(t *testing.T) {
	good := iconPixel(t, 6)
	other := iconPixel(t, 7)

	record := func(change func(*LibraryIcon)) []byte {
		icon := LibraryIcon{
			ID:      builder.IconID(other),
			Owner:   testOwner,
			Name:    "planted",
			Width:   1,
			Height:  1,
			Bytes:   len(other),
			Created: memrecord.Time(1),
			Data:    base64.StdEncoding.EncodeToString(other),
		}

		change(&icon)

		value, err := json.Marshal(icon)
		if err != nil {
			t.Fatalf("encoding a record: %v", err)
		}

		return value
	}

	_, digits, _ := strings.Cut(builder.IconID(other), ":")
	key := OwnerScope(testOwner) + "/" + digits

	tests := []struct {
		name   string
		key    string
		value  []byte
		reason string
	}{
		{name: "another owner", key: key, value: record(func(i *LibraryIcon) { i.Owner = testPeer }), reason: "names another owner"},
		{name: "no owner", key: key, value: record(func(i *LibraryIcon) { i.Owner = "" }), reason: "names another owner"},
		{
			name: "the ID of another icon", key: key,
			value:  record(func(i *LibraryIcon) { i.ID = builder.IconID(good) }),
			reason: "names another icon",
		},
		{name: "an ID that is not one", key: key, value: record(func(i *LibraryIcon) { i.ID = "plc" }), reason: "names another icon"},
		{
			name: "a key that is not the ID", key: OwnerScope(testOwner) + "/" + strings.Repeat("0", 64),
			value: record(func(*LibraryIcon) {}), reason: "names another icon",
		},
		{
			name: "the bytes of another image", key: key,
			value:  record(func(i *LibraryIcon) { i.Data = base64.StdEncoding.EncodeToString(good) }),
			reason: "does not match its ID",
		},
		{
			name: "bytes that are not a PNG", key: key,
			value:  record(func(i *LibraryIcon) { i.Data = base64.StdEncoding.EncodeToString([]byte("<svg onload=alert(1)>")) }),
			reason: "not an accepted PNG",
		},
		{
			name: "a PNG a document does not accept", key: key,
			value: record(func(i *LibraryIcon) {
				i.Data = base64.StdEncoding.EncodeToString(iconWithText(t, other, "hidden"))
			}),
			reason: "not an accepted PNG",
		},
		{name: "data that is not base64", key: key, value: record(func(i *LibraryIcon) { i.Data = "<svg/>" }), reason: "not base64"},
		{
			name: "a name with a control character", key: key,
			value:  record(func(i *LibraryIcon) { i.Name = "a\x00b" }),
			reason: "the name is not one an icon may have",
		},
		{
			name: "an unknown field", key: key,
			value:  []byte(strings.Replace(string(record(func(*LibraryIcon) {})), "{", `{"html":"x",`, 1)),
			reason: "not valid JSON",
		},
		{name: "not JSON", key: key, value: []byte("\x89PNG"), reason: "not valid JSON"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			kept := mustAddIcon(t, h, testOwner, "kept", good)

			if _, err := h.store.CreateRecord(NamespaceIcons, test.key, test.value); err != nil {
				t.Fatalf("planting the record: %v", err)
			}

			logs := plogtest.Capture(t)

			icons, err := h.service.ListIcons(context.Background(), testOwner)
			if err != nil {
				t.Fatalf("ListIcons returned error: %v", err)
			}

			if len(icons) != 1 || !sameIcon(icons[0], *kept) {
				t.Fatalf("ListIcons = %+v, want only the icon that was added", icons)
			}

			logged := logs.Records(t, plogtest.Level(slog.LevelError))
			if len(logged) != 1 || logged[0]["msg"] != "skipping unreadable builder icon" || logged[0]["icon"] != test.key {
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

			// The record does not count toward the library either, and the
			// owner can remove it by its key.
			if test.key != key {
				return
			}

			icon, created, err := h.service.AddIcon(context.Background(), testOwner, "again", other)
			if !errors.Is(err, ErrCorrupt) || icon != nil || created {
				t.Fatalf("adding the icon the record stands for: %+v, %v, %v; want ErrCorrupt", icon, created, err)
			}

			if err := h.service.DeleteIcon(context.Background(), testOwner, builder.IconID(other)); err != nil {
				t.Fatalf("deleting the damaged record: %v", err)
			}

			mustAddIcon(t, h, testOwner, "again", other)
		})
	}
}

// TestListIconsTrustsOnlyTheImage asserts the size an icon is listed with,
// and its base64, come from its PNG and not from the record.
func TestListIconsTrustsOnlyTheImage(t *testing.T) {
	h := newHarness(t)
	icon := mustAddIcon(t, h, testOwner, "plc", iconPixel(t, 8))
	key := h.store.Keys(NamespaceIcons)[0]

	lied := *icon
	lied.Width, lied.Height, lied.Bytes = 5000, 5000, 1
	// The decoder skips line breaks, which a document refuses.
	lied.Data = icon.Data[:8] + "\r\n" + icon.Data[8:]

	value, err := json.Marshal(lied)
	if err != nil {
		t.Fatalf("encoding the record: %v", err)
	}

	h.store.SetValue(NamespaceIcons, key, value)

	icons, err := h.service.ListIcons(context.Background(), testOwner)
	if err != nil {
		t.Fatalf("ListIcons returned error: %v", err)
	}

	if len(icons) != 1 || !sameIcon(icons[0], *icon) {
		t.Fatalf("ListIcons = %+v, want %+v", icons, *icon)
	}
}

// TestAddIconRacingUpload asserts two requests that add the same image at
// once both succeed, and one icon is stored.
func TestAddIconRacingUpload(t *testing.T) {
	h := newHarness(t)
	upload := iconPixel(t, 9)

	var first *LibraryIcon

	// Another request stores the image between this one's listing and its
	// write.
	h.store.BeforeCreate = func(string, string) error {
		h.store.BeforeCreate = nil
		first = mustAddIcon(t, h, testOwner, "first", upload)

		return nil
	}

	second, created, err := h.service.AddIcon(context.Background(), testOwner, "second", upload)
	if err != nil || created {
		t.Fatalf("the second upload: created = %v, err = %v, want the stored icon", created, err)
	}

	if first == nil || !sameIcon(*second, *first) || second.Name != "first" {
		t.Fatalf("the second upload returned %+v, want %+v", second, first)
	}

	if got := h.store.Count(NamespaceIcons); got != 1 {
		t.Fatalf("%d icon records, want 1", got)
	}
}

func TestIconLibraryStoreFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// What the Etcd store returns once etcd is out of space.
	h.store.BeforeCreate = func(namespace, key string) error {
		return fmt.Errorf("creating record %s/%s in Etcd: %w", namespace, key, store.ErrNoSpace)
	}

	if _, _, err := h.service.AddIcon(ctx, testOwner, "", iconPixel(t, 10)); !errors.Is(err, store.ErrNoSpace) {
		t.Fatalf("AddIcon = %v, want the store's out of space error", err)
	}

	h.store.BeforeCreate = nil
	icon := mustAddIcon(t, h, testOwner, "", iconPixel(t, 10))

	refused := errors.New("injected delete failure")
	h.store.FailDelete = func(string, string) error { return refused }

	if err := h.service.DeleteIcon(ctx, testOwner, icon.ID); !errors.Is(err, refused) {
		t.Fatalf("DeleteIcon = %v, want the store's error", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := h.service.ListIcons(canceled, testOwner); !errors.Is(err, context.Canceled) {
		t.Errorf("ListIcons with a canceled context = %v", err)
	}

	if _, _, err := h.service.AddIcon(canceled, testOwner, "", iconPixel(t, 11)); !errors.Is(err, context.Canceled) {
		t.Errorf("AddIcon with a canceled context = %v", err)
	}

	if err := h.service.DeleteIcon(canceled, testOwner, icon.ID); !errors.Is(err, context.Canceled) {
		t.Errorf("DeleteIcon with a canceled context = %v", err)
	}

	if _, err := h.service.ListIcons(ctx, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("ListIcons without an owner = %v, want ErrInvalid", err)
	}
}

// TestIconLibrarySurvivesCleanup asserts the cleanups a server start runs
// leave the icon libraries alone.
func TestIconLibrarySurvivesCleanup(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	icon := mustAddIcon(t, h, testOwner, "kept", iconPixel(t, 12))

	if _, err := h.service.CleanupOrphanedChunks(ctx); err != nil {
		t.Fatalf("CleanupOrphanedChunks returned error: %v", err)
	}

	if _, err := h.service.CleanupOrphanedDocuments(ctx, nil); err != nil {
		t.Fatalf("CleanupOrphanedDocuments returned error: %v", err)
	}

	icons, err := h.service.ListIcons(ctx, testOwner)
	if err != nil || len(icons) != 1 || !sameIcon(icons[0], *icon) {
		t.Fatalf("ListIcons after the cleanups = %+v, %v; want the icon", icons, err)
	}
}
