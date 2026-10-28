package builder_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/adler32"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

// A 1 by 1 PNG of 70 bytes, in base64, its icon id, and the name the tests
// give it. validate.contract.test.js and icons.contract.test.js use the same icon.
const (
	iconFixtureData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	iconFixtureID   = "sha256:497790947d4666760ce38f3c00e852c71fdb66cae849bae8e9ede352719e1581"
	iconFixtureName = "plc-icon"
)

// iconFixture returns the bytes of the fixture icon.
func iconFixture(t *testing.T) []byte {
	t.Helper()

	data, err := base64.StdEncoding.DecodeString(iconFixtureData)
	if err != nil {
		t.Fatalf("decoding the fixture icon: %v", err)
	}

	return data
}

// pngChunk is one chunk of a PNG file.
type pngChunk struct {
	kind string
	data []byte
}

// pngSignature starts every PNG file.
const pngSignature = "\x89PNG\r\n\x1a\n"

// splitPNG returns the chunks of a well-formed PNG.
func splitPNG(t *testing.T, data []byte) []pngChunk {
	t.Helper()

	if !bytes.HasPrefix(data, []byte(pngSignature)) {
		t.Fatal("not a PNG")
	}

	var chunks []pngChunk

	for rest := data[len(pngSignature):]; len(rest) > 0; {
		length := int(binary.BigEndian.Uint32(rest))
		chunks = append(chunks, pngChunk{kind: string(rest[4:8]), data: rest[8 : 8+length]})
		rest = rest[12+length:]
	}

	return chunks
}

// joinPNG writes chunks as a PNG, each with the checksum of its content.
func joinPNG(chunks ...pngChunk) []byte {
	out := []byte(pngSignature)

	for _, chunk := range chunks {
		out = binary.BigEndian.AppendUint32(out, uint32(len(chunk.data)))
		body := append([]byte(chunk.kind), chunk.data...)
		out = append(out, body...)
		out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(body))
	}

	return out
}

// pngKinds returns the chunk types of a PNG, in order.
func pngKinds(t *testing.T, data []byte) string {
	t.Helper()

	chunks := splitPNG(t, data)
	kinds := make([]string, len(chunks))

	for i, chunk := range chunks {
		kinds[i] = chunk.kind
	}

	return strings.Join(kinds, " ")
}

// withChunk returns the fixture icon with a chunk put before its pixels.
func withChunk(t *testing.T, kind string, data []byte) []byte {
	t.Helper()

	chunks := splitPNG(t, iconFixture(t))

	return joinPNG(chunks[0], pngChunk{kind: kind, data: data}, chunks[1], chunks[2])
}

// The color types of a PNG header, and the chunk types the tests build.
const (
	colorGray = 0
	colorRGB  = 2
	colorRGBA = 6

	chunkIHDR = "IHDR"
	chunkTRNS = "tRNS"
	chunkIDAT = "IDAT"
	chunkIEND = "IEND"
)

// headerChunk returns the header chunk of an image of the given size, bit depth and color type.
func headerChunk(width, height uint32, depth, colorType byte) pngChunk {
	header := binary.BigEndian.AppendUint32(nil, width)
	header = binary.BigEndian.AppendUint32(header, height)

	return pngChunk{kind: chunkIHDR, data: append(header, depth, colorType, 0, 0, 0)}
}

// headerOnly returns a PNG that is a header for the given size and the end,
// with no pixels.
func headerOnly(width, height uint32) []byte {
	return joinPNG(headerChunk(width, height, 8, colorRGBA), pngChunk{kind: chunkIEND, data: nil})
}

// storedPixels returns raw pixel bytes as uncompressed pixel chunk data: an empty block, then one
// block of the bytes, 15 bytes more than they are.
func storedPixels(raw []byte) []byte {
	size := uint16(len(raw)) //nolint:gosec // callers pass fewer bytes than a block holds

	// The stream's header, a block of no bytes that is not the last, and
	// the header of the last block: its length and the complement of it.
	data := []byte{0x78, 0x01, 0, 0, 0, 0xff, 0xff, 1}
	data = binary.LittleEndian.AppendUint16(data, size)
	data = binary.LittleEndian.AppendUint16(data, ^size)
	data = append(data, raw...)

	return binary.BigEndian.AppendUint32(data, adler32.Checksum(raw))
}

// onePixel returns a 1 by 1 PNG of 8 bits a channel whose pixel is the given
// bytes, with chunks put between its header and its pixels.
func onePixel(colorType byte, pixel []byte, before ...pngChunk) []byte {
	chunks := append([]pngChunk{headerChunk(1, 1, 8, colorType)}, before...)
	// A row starts with its filter: none.
	chunks = append(chunks,
		pngChunk{kind: chunkIDAT, data: storedPixels(append([]byte{0}, pixel...))},
		pngChunk{kind: chunkIEND, data: nil},
	)

	return joinPNG(chunks...)
}

// The largest icon there is, to the byte: 81 by 63 transparent 16-bit pixels, uncompressed.
// icons.contract.test.js and the validation corpus hold the same icon.
const (
	largestIconWidth  = 81
	largestIconHeight = 63
	largestIconID     = "sha256:6620402302a9ebd8e78490436e065649504fa7d02922d0432905f04debb0c5a2"
)

// largestIcon returns an icon of exactly [builder.MaxIconBytes].
func largestIcon() []byte {
	// Each row is its filter and eight bytes a pixel.
	raw := make([]byte, largestIconHeight*(1+8*largestIconWidth))

	return joinPNG(
		headerChunk(largestIconWidth, largestIconHeight, 16, colorRGBA),
		pngChunk{kind: chunkIDAT, data: storedPixels(raw)},
		pngChunk{kind: chunkIEND, data: nil},
	)
}

// encodePNG encodes an image as the standard library does.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()

	var out bytes.Buffer

	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encoding a PNG: %v", err)
	}

	return out.Bytes()
}

// noise returns an image of random pixels, which does not compress.
func noise(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	random := rand.New(rand.NewPCG(1, 2))

	for i := range img.Pix {
		img.Pix[i] = byte(random.UintN(256))
	}

	return img
}

// pixels returns the pixels of a PNG, 8 bits a channel without premultiplied
// alpha.
func pixels(t *testing.T, data []byte) []color.NRGBA {
	t.Helper()

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding a PNG: %v", err)
	}

	bounds := img.Bounds()
	out := make([]color.NRGBA, 0, bounds.Dx()*bounds.Dy())

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			out = append(out, color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)) //nolint:forcetypeassert // the model's own type
		}
	}

	return out
}

func TestIconID(t *testing.T) {
	if got := builder.IconID(iconFixture(t)); got != iconFixtureID {
		t.Errorf("IconID(fixture) = %q, want %q", got, iconFixtureID)
	}

	if id := builder.IconID(nil); !builder.IsDigest(id) {
		t.Errorf("IsDigest(IconID(nil)) = false for %q, want true", id)
	}
}

func TestValidateIconPNGAccepts(t *testing.T) {
	fixture := iconFixture(t)
	chunks := splitPNG(t, fixture)
	pixelData := chunks[1].data
	largest := largestIcon()

	paletted := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{
		color.NRGBA{R: 255, A: 128}, color.NRGBA{B: 255, A: 255},
	})
	paletted.SetColorIndex(1, 1, 1)

	tests := []struct {
		name          string
		data          []byte
		width, height int
	}{
		{name: "the fixture", data: fixture, width: 1, height: 1},
		{name: "the most pixels", data: encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 96, 96))), width: 96, height: 96},
		{name: "not square", data: encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 96, 3))), width: 96, height: 3},
		{name: "a palette with transparency", data: encodePNG(t, paletted), width: 4, height: 4},
		{name: "grayscale", data: encodePNG(t, image.NewGray(image.Rect(0, 0, 5, 7))), width: 5, height: 7},
		{name: "16 bits a channel", data: encodePNG(t, image.NewNRGBA64(image.Rect(0, 0, 2, 2))), width: 2, height: 2},
		{
			name:  "grayscale with a transparent shade",
			data:  onePixel(colorGray, []byte{0x80}, pngChunk{kind: chunkTRNS, data: []byte{0, 0x80}}),
			width: 1, height: 1,
		},
		{
			name:  "color with a transparent color",
			data:  onePixel(colorRGB, []byte{1, 2, 3}, pngChunk{kind: chunkTRNS, data: []byte{0, 1, 0, 2, 0, 3}}),
			width: 1, height: 1,
		},
		{
			name: "pixels in two chunks",
			data: joinPNG(
				chunks[0],
				pngChunk{kind: chunkIDAT, data: pixelData[:6]},
				pngChunk{kind: chunkIDAT, data: pixelData[6:]},
				chunks[2],
			),
			width: 1, height: 1,
		},
		{name: "the most bytes", data: largest, width: largestIconWidth, height: largestIconHeight},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := bytes.Clone(test.data)

			width, height, err := builder.ValidateIconPNG(test.data)
			if err != nil {
				t.Fatalf("ValidateIconPNG(%s) error = %v, want nil", pngKinds(t, test.data), err)
			}

			if width != test.width || height != test.height {
				t.Errorf("ValidateIconPNG() = %d x %d, want %d x %d", width, height, test.width, test.height)
			}

			if !bytes.Equal(test.data, before) {
				t.Error("ValidateIconPNG() changed the image, want it unchanged")
			}
		})
	}

	if got := len(largest); got != builder.MaxIconBytes {
		t.Errorf("len(largestIcon()) = %d, want %d", got, builder.MaxIconBytes)
	}

	if got := builder.IconID(largest); got != largestIconID {
		t.Errorf("IconID(largestIcon()) = %s, want the icon the corpus holds, %s", got, largestIconID)
	}

	// Refused for its length, before anything of it is read.
	const tooLong = "the image is 40961 bytes: the limit is 40960"
	if _, _, err := builder.ValidateIconPNG(append(largest, 0)); err == nil || !strings.Contains(err.Error(), tooLong) {
		t.Errorf("ValidateIconPNG(largest icon and one byte) error = %v, want one containing %q", err, tooLong)
	}
}

func TestValidateIconPNGRefuses(t *testing.T) {
	fixture := iconFixture(t)
	chunks := splitPNG(t, fixture)

	// The last byte of the pixels' checksum, and a byte of the pixels with
	// the checksum made right again.
	badChecksum := bytes.Clone(fixture)
	badChecksum[len(badChecksum)-13] ^= 0xff

	corrupt := bytes.Clone(chunks[1].data)
	corrupt[len(corrupt)/2] ^= 0xff

	// Bytes that are no part of any image, where a decoder does not look.
	const hidden = "<script>alert(1)</script>"

	const afterPixels = "the PNG holds data after its pixels"

	tests := []struct {
		name    string
		data    []byte
		wantMsg string
	}{
		{name: "empty", data: nil, wantMsg: "the image is empty"},
		{
			name:    "a header that claims the most a PNG can",
			data:    headerOnly(0xffffffff, 0xffffffff),
			wantMsg: "icon is 4294967295 x 4294967295 pixels",
		},
		{name: "a header and no pixels", data: headerOnly(1, 1), wantMsg: "the Builder cannot decode the PNG"},
		{
			// The decoder skips a pixel chunk that follows the last pixel.
			name:    "a pixel chunk after the pixels",
			data:    joinPNG(chunks[0], chunks[1], pngChunk{kind: chunkIDAT, data: []byte(hidden)}, chunks[2]),
			wantMsg: afterPixels,
		},
		{
			name: "bytes after the pixels in their chunk",
			data: joinPNG(
				chunks[0],
				pngChunk{kind: chunkIDAT, data: append(bytes.Clone(chunks[1].data), hidden...)},
				chunks[2],
			),
			wantMsg: afterPixels,
		},
		{
			// More of them than the decoder reads ahead, which it refuses
			// itself.
			name: "many bytes after the pixels in their chunk",
			data: joinPNG(
				chunks[0],
				pngChunk{kind: chunkIDAT, data: append(bytes.Clone(chunks[1].data), strings.Repeat(hidden, 400)...)},
				chunks[2],
			),
			wantMsg: "pixel",
		},
		{
			name:    "one byte after the pixels",
			data:    joinPNG(chunks[0], chunks[1], pngChunk{kind: chunkIDAT, data: []byte{0}}, chunks[2]),
			wantMsg: afterPixels,
		},
		{name: "a wrong checksum", data: badChecksum, wantMsg: "the Builder cannot decode the PNG"},
		{
			name:    "corrupt pixels",
			data:    joinPNG(chunks[0], pngChunk{kind: chunkIDAT, data: corrupt}, chunks[2]),
			wantMsg: "the Builder cannot decode the PNG",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			width, height, err := builder.ValidateIconPNG(test.data)
			if err == nil {
				t.Fatalf("ValidateIconPNG() = %d x %d, nil, want an error", width, height)
			}

			if !errors.Is(err, builder.ErrInvalidIcon) {
				t.Errorf("ValidateIconPNG() error = %v, want one wrapping ErrInvalidIcon", err)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Errorf("ValidateIconPNG() error = %q, want one containing %q", err.Error(), test.wantMsg)
			}

			if width != 0 || height != 0 {
				t.Errorf("ValidateIconPNG() = %d x %d with an error, want 0 x 0", width, height)
			}
		})
	}
}

// TestIconPNGSize reads the size of an icon from its header. It does not
// decode the pixels, so it accepts a PNG whose checksum is wrong.
func TestIconPNGSize(t *testing.T) {
	fixture := iconFixture(t)

	badChecksum := bytes.Clone(fixture)
	badChecksum[len(badChecksum)-13] ^= 0xff

	for name, data := range map[string][]byte{"the fixture": fixture, "a wrong checksum": badChecksum} {
		width, height, err := builder.IconPNGSize(data)
		if err != nil || width != 1 || height != 1 {
			t.Errorf("IconPNGSize(%s) = %d x %d, %v, want 1 x 1, nil", name, width, height, err)
		}
	}

	for name, data := range map[string][]byte{"empty": nil, "not a PNG": []byte("<svg/>")} {
		if _, _, err := builder.IconPNGSize(data); !errors.Is(err, builder.ErrInvalidIcon) {
			t.Errorf("IconPNGSize(%s) error = %v, want ErrInvalidIcon", name, err)
		}
	}
}

func TestNormalizeIconPNG(t *testing.T) {
	// Semi-transparent pixels, which a conversion through premultiplied
	// alpha would change.
	source := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for i := range source.Pix {
		source.Pix[i] = byte(1 + i*37)
	}

	strict := encodePNG(t, source)
	chunks := splitPNG(t, strict)

	loose := joinPNG(
		chunks[0],
		pngChunk{kind: "tEXt", data: []byte("Comment\x00hello")},
		pngChunk{kind: "gAMA", data: []byte{0, 0, 0xb1, 0x8f}},
		chunks[1],
		chunks[2],
	)
	loose = append(loose, "trailing bytes"...)

	if width, height, err := builder.ValidateIconPNG(loose); err == nil {
		t.Errorf("ValidateIconPNG(loose PNG) = %d x %d, nil, want an error", width, height)
	}

	normalized, err := builder.NormalizeIconPNG(loose)
	if err != nil {
		t.Fatalf("NormalizeIconPNG(loose PNG): %v", err)
	}

	if width, height, err := builder.ValidateIconPNG(normalized); err != nil || width != 3 || height != 2 {
		t.Errorf("ValidateIconPNG(normalized) = %d x %d, %v, want 3 x 2, nil", width, height, err)
	}

	if got := pngKinds(t, normalized); got != "IHDR IDAT IEND" {
		t.Errorf("chunks of NormalizeIconPNG(loose PNG) = %s, want IHDR IDAT IEND", got)
	}

	if diff := cmp.Diff(pixels(t, strict), pixels(t, normalized)); diff != "" {
		t.Errorf("pixels of NormalizeIconPNG(loose PNG) mismatch (-want +got):\n%s", diff)
	}

	// The same pixels give the same bytes, so the same icon id.
	again, err := builder.NormalizeIconPNG(normalized)
	if err != nil {
		t.Fatalf("NormalizeIconPNG(normalized): %v", err)
	}

	if diff := cmp.Diff(normalized, again); diff != "" {
		t.Errorf("NormalizeIconPNG(normalized) mismatch (-want +got):\n%s", diff)
	}
}

func TestNormalizeIconPNGFormats(t *testing.T) {
	deep := image.NewNRGBA64(image.Rect(0, 0, 4, 4))
	deep.SetNRGBA64(1, 2, color.NRGBA64{R: 0xffff, G: 0x8000, B: 0x1234, A: 0xffff})

	paletted := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{
		color.NRGBA{R: 255, A: 128}, color.NRGBA{B: 255, A: 255},
	})
	paletted.SetColorIndex(3, 0, 1)

	gray := image.NewGray(image.Rect(0, 0, 96, 96))
	gray.SetGray(95, 95, color.Gray{Y: 200})

	tests := []struct {
		name string
		img  image.Image
		at   image.Point
		want color.NRGBA
	}{
		{name: "16 bits a channel", img: deep, at: image.Pt(1, 2), want: color.NRGBA{R: 0xff, G: 0x80, B: 0x12, A: 0xff}},
		{name: "a palette with transparency", img: paletted, at: image.Pt(0, 0), want: color.NRGBA{R: 255, A: 128}},
		{name: "a palette, opaque entry", img: paletted, at: image.Pt(3, 0), want: color.NRGBA{B: 255, A: 255}},
		{name: "grayscale at the most pixels", img: gray, at: image.Pt(95, 95), want: color.NRGBA{R: 200, G: 200, B: 200, A: 255}},
		// The largest image there is, which does not compress, still fits.
		{name: "noise at the most pixels", img: noise(96, 96), at: image.Pt(0, 0), want: noise(96, 96).NRGBAAt(0, 0)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := builder.NormalizeIconPNG(encodePNG(t, test.img))
			if err != nil {
				t.Fatalf("NormalizeIconPNG(): %v", err)
			}

			bounds := test.img.Bounds()

			// The pixel is found by the width, so a wrong size stops the test.
			width, height, err := builder.ValidateIconPNG(normalized)
			if err != nil || width != bounds.Dx() || height != bounds.Dy() {
				t.Fatalf("ValidateIconPNG(normalized, %d bytes) = %d x %d, %v, want %d x %d, nil",
					len(normalized), width, height, err, bounds.Dx(), bounds.Dy())
			}

			if got := pixels(t, normalized)[test.at.Y*width+test.at.X]; got != test.want {
				t.Errorf("pixel %v of NormalizeIconPNG() = %v, want %v", test.at, got, test.want)
			}
		})
	}
}

func TestNormalizeIconPNGRefuses(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantMsg string
	}{
		{name: "empty", data: nil, wantMsg: "the image is not a PNG"},
		{name: "an SVG", data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), wantMsg: "the image is not a PNG"},
		{
			name:    "97 pixels wide",
			data:    encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 97, 96))),
			wantMsg: "icon is 97 x 96 pixels: the limit is 96 x 96",
		},
		{
			name:    "97 pixels high",
			data:    encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 1, 97))),
			wantMsg: "icon is 1 x 97 pixels: the limit is 96 x 96",
		},
		{
			name:    "a header that claims 50000 by 50000 pixels",
			data:    headerOnly(50000, 50000),
			wantMsg: "icon is 50000 x 50000 pixels: the limit is 96 x 96",
		},
		{name: "a header and no pixels", data: headerOnly(1, 1), wantMsg: "the Builder cannot decode the PNG"},
		{name: "only the signature", data: []byte(pngSignature), wantMsg: "the Builder cannot decode the PNG"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := builder.NormalizeIconPNG(test.data)
			if err == nil {
				t.Fatalf("NormalizeIconPNG() = %d bytes, nil, want an error", len(normalized))
			}

			if !errors.Is(err, builder.ErrInvalidIcon) {
				t.Errorf("NormalizeIconPNG() error = %v, want one wrapping ErrInvalidIcon", err)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Errorf("NormalizeIconPNG() error = %q, want one containing %q", err.Error(), test.wantMsg)
			}
		})
	}
}

// distinctIcons returns count icons of one pixel each, no two alike, by
// name.
func distinctIcons(t *testing.T, count int) map[string]builder.Icon {
	t.Helper()

	icons := make(map[string]builder.Icon, count)

	for i := range count {
		img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		img.SetNRGBA(0, 0, color.NRGBA{R: byte(i), G: byte(i >> 8), B: 7, A: 255})

		data := encodePNG(t, img)
		icons[fmt.Sprintf("icon-%02d", i)] = builder.Icon{Data: base64.StdEncoding.EncodeToString(data)}
	}

	if len(icons) != count {
		t.Fatalf("made %d distinct icons, want %d", len(icons), count)
	}

	return icons
}

func TestIconNameProblem(t *testing.T) {
	const charset = `must be 1 to 64 letters, digits, "_", "@", "." or "-"`

	valid := []struct{ desc, name string }{
		{desc: "one letter", name: "a"},
		{desc: "lower case", name: "plc"},
		{desc: "upper case", name: "PLC"},
		{desc: "a hyphen", name: "plc-2"},
		{desc: "an underscore", name: "plc_2"},
		{desc: "an at sign", name: "alice@site"},
		{desc: "a dot", name: "v1.2"},
		{desc: "three dots", name: "..."},
		{desc: "a leading dot", name: ".hidden"},
		{desc: "only a hyphen", name: "-"},
		{desc: "only an underscore", name: "_"},
		{desc: "only an at sign", name: "@"},
		{desc: "the longest name", name: strings.Repeat("n", builder.MaxIconNameBytes)},
	}

	for _, test := range valid {
		t.Run(test.desc, func(t *testing.T) {
			if problem := builder.IconNameProblem(test.name); problem != "" {
				t.Errorf("IconNameProblem(%q) = %q, want none", test.name, problem)
			}
		})
	}

	invalid := []struct{ desc, name, want string }{
		{desc: "empty", name: "", want: `icon name "" ` + charset},
		{desc: "one byte too long", name: strings.Repeat("n", builder.MaxIconNameBytes+1), want: charset},
		{desc: "a space", name: "plc icon", want: `icon name "plc icon" ` + charset},
		{desc: "a slash", name: "plc/icon", want: charset},
		{desc: "an icon id", name: iconFixtureID, want: charset},
		{desc: "not ASCII", name: "ñandú", want: charset},
		{desc: "a tab", name: "tab\there", want: `icon name "tab\there" ` + charset},
		{desc: "one dot", name: ".", want: `icon name "." must not be "." or ".."`},
		{desc: "two dots", name: "..", want: `icon name ".." must not be "." or ".."`},
	}

	for _, test := range invalid {
		t.Run(test.desc, func(t *testing.T) {
			if problem := builder.IconNameProblem(test.name); !strings.Contains(problem, test.want) {
				t.Errorf("IconNameProblem(%q) = %q, want one containing %q", test.name, problem, test.want)
			}
		})
	}
}

func TestValidateIconsAccepts(t *testing.T) {
	if issues := builder.ValidateIcons(nil, "icons"); len(issues) != 0 {
		t.Errorf("ValidateIcons(nil, icons) = %v, want no issues", issues)
	}

	icons := distinctIcons(t, builder.MaxDocumentIcons-2)

	// The longest name, and a name that is not the image's digest: two names may carry one image.
	icons[strings.Repeat("n", builder.MaxIconNameBytes)] = builder.Icon{Data: iconFixtureData}
	icons[iconFixtureName] = builder.Icon{Data: iconFixtureData}

	if issues := builder.ValidateIcons(icons, "icons"); len(issues) != 0 {
		t.Errorf("ValidateIcons(%d icons, icons) = %v, want no issues", len(icons), issues)
	}
}

func TestValidateIconsRefuses(t *testing.T) {
	fixture := iconFixture(t)
	encode := base64.StdEncoding.EncodeToString

	// The fixture's data ends "gg==": the last character holds four bits no
	// byte takes, which strict base64 wants zero.
	loosePadding := strings.TrimSuffix(iconFixtureData, "gg==") + "gh=="

	const charset = `must be 1 to 64 letters, digits, "_", "@", "." or "-"`

	tests := []struct {
		name    string
		icons   map[string]builder.Icon
		wantMsg string
	}{
		{
			name:    "one icon too many",
			icons:   distinctIcons(t, builder.MaxDocumentIcons+1),
			wantMsg: "a document can carry at most 50 custom icons, not 51",
		},
		{
			name:    "a key that is an icon id",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: iconFixtureData}},
			wantMsg: `icon name "` + iconFixtureID[:64] + `..." ` + charset,
		},
		{
			name:    "a key with a space",
			icons:   map[string]builder.Icon{"plc icon": {Data: iconFixtureData}},
			wantMsg: `icon name "plc icon" ` + charset,
		},
		{
			name:    "a key of 65 bytes, cut in the message",
			icons:   map[string]builder.Icon{strings.Repeat("k", 65): {Data: iconFixtureData}},
			wantMsg: `icon name "` + strings.Repeat("k", 64) + `..." ` + charset,
		},
		{
			name:    "a key that is two dots",
			icons:   map[string]builder.Icon{"..": {Data: iconFixtureData}},
			wantMsg: `icon name ".." must not be "." or ".."`,
		},
		{
			name:    "no data",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: ""}},
			wantMsg: `icon "plc-icon" data must be base64 of at most 40960 bytes`,
		},
		{
			name:    "data that is not base64",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: "<svg onload=alert(1)>"}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "URL-safe base64",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: strings.Replace(iconFixtureData, "A", "_", 1)}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "base64 without padding",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: strings.TrimRight(iconFixtureData, "=")}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "base64 with a line break",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: iconFixtureData[:40] + "\n" + iconFixtureData[40:]}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "base64 with bits set in its padding",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: loosePadding}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "a data URL",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: "data:image/png;base64," + iconFixtureData}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name: "one byte too many",
			icons: map[string]builder.Icon{iconFixtureName: {
				Data: encode(append(bytes.Clone(fixture), make([]byte, builder.MaxIconBytes+1-len(fixture))...)),
			}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "an SVG",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: encode([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))}},
			wantMsg: `icon "plc-icon" is not an accepted PNG: the image is not a PNG`,
		},
		{
			name:    "97 pixels wide",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: encode(headerOnly(97, 1))}},
			wantMsg: "is not an accepted PNG: icon is 97 x 1 pixels: the limit is 96 x 96",
		},
		{
			name:    "a text chunk",
			icons:   map[string]builder.Icon{iconFixtureName: {Data: encode(withChunk(t, "tEXt", []byte("a\x00b")))}},
			wantMsg: `is not an accepted PNG: the PNG holds a "tEXt" chunk`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := builder.ValidateIcons(test.icons, "library.icons")
			if len(issues) != 1 {
				t.Fatalf("ValidateIcons(library.icons) = %d issues %v, want 1", len(issues), issues)
			}

			if issues[0].Path != "library.icons" {
				t.Errorf("ValidateIcons(library.icons) issue path = %q, want library.icons", issues[0].Path)
			}

			if !strings.Contains(issues[0].Message, test.wantMsg) {
				t.Errorf("ValidateIcons(library.icons) issue message = %q, want one containing %q", issues[0].Message, test.wantMsg)
			}
		})
	}
}

// Keys are walked in order, so the issues of a set are the same every time.
func TestValidateIconsIsStable(t *testing.T) {
	icons := map[string]builder.Icon{
		"b":             {Data: "?"},
		"a b":           {Data: iconFixtureData},
		iconFixtureName: {Data: iconFixtureData},
	}

	issues := builder.ValidateIcons(icons, "icons")
	first := fmt.Sprint(issues)

	// A malformed key is reported once, and the icon of a valid one with
	// valid data not at all.
	if strings.Count(first, `"a b"`) != 1 || strings.Contains(first, iconFixtureName) {
		t.Errorf("ValidateIcons() = %s, want one issue naming \"a b\" and none naming %s", first, iconFixtureName)
	}

	if !strings.Contains(first, `icon name "a b"`) || strings.Index(first, `"a b"`) > strings.Index(first, `"b"`) {
		t.Errorf("ValidateIcons() = %s, want the issues in key order", first)
	}

	for range 20 {
		if diff := cmp.Diff(issues, builder.ValidateIcons(icons, "icons")); diff != "" {
			t.Errorf("ValidateIcons() changed between runs (-first +again):\n%s", diff)

			break
		}
	}
}
