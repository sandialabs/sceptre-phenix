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
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"strings"
	"testing"

	"phenix/types/builder"
)

// A 1 by 1 PNG of 70 bytes, in base64, and its icon id. validate.test.js and
// icons.test.js use the same icon.
const (
	iconFixtureData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	iconFixtureID   = "sha256:497790947d4666760ce38f3c00e852c71fdb66cae849bae8e9ede352719e1581"
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
	colorGray      = 0
	colorRGB       = 2
	colorGrayAlpha = 4
	colorRGBA      = 6

	chunkIHDR = "IHDR"
	chunkPLTE = "PLTE"
	chunkTRNS = "tRNS"
	chunkIDAT = "IDAT"
	chunkIEND = "IEND"
)

// headerChunk returns the header chunk of an image of the given size, bits
// a channel and color type, with the only compression, filter and interlace
// methods there are.
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

// storedPixels returns raw pixel bytes as the data of a pixel chunk, stored
// without compression: an empty block and then the bytes in one block, so
// the data is 15 bytes longer than they are.
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

// The largest icon there is, to the byte: 81 by 63 transparent pixels of 16
// bits a channel, stored without compression. icons.test.js and the
// validation corpus hold the same icon.
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
		t.Fatalf("IconID of the fixture = %q, want %q", got, iconFixtureID)
	}

	if !builder.IsDigest(builder.IconID(nil)) {
		t.Fatalf("IconID of nothing %q does not have the form of a digest", builder.IconID(nil))
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
				t.Fatalf("refused (%s): %v", pngKinds(t, test.data), err)
			}

			if width != test.width || height != test.height {
				t.Fatalf("size = %d x %d, want %d x %d", width, height, test.width, test.height)
			}

			if !bytes.Equal(test.data, before) {
				t.Fatal("the image was changed")
			}
		})
	}

	if got := len(largest); got != builder.MaxIconBytes {
		t.Fatalf("the largest icon is %d bytes, want %d", got, builder.MaxIconBytes)
	}

	if got := builder.IconID(largest); got != largestIconID {
		t.Fatalf("the largest icon is %s, want the icon the corpus holds, %s", got, largestIconID)
	}

	// Refused for its length, before anything of it is read.
	if _, _, err := builder.ValidateIconPNG(append(largest, 0)); err == nil ||
		!strings.Contains(err.Error(), "the image is 40961 bytes; the limit is 40960") {
		t.Fatalf("an icon of one byte more: %v", err)
	}
}

func TestValidateIconPNGRefuses(t *testing.T) {
	fixture := iconFixture(t)
	chunks := splitPNG(t, fixture)

	var photo bytes.Buffer

	if err := jpeg.Encode(&photo, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatalf("encoding a JPEG: %v", err)
	}

	// The last byte of the pixels' checksum, and a byte of the pixels with
	// the checksum made right again.
	badChecksum := bytes.Clone(fixture)
	badChecksum[len(badChecksum)-13] ^= 0xff

	corrupt := bytes.Clone(chunks[1].data)
	corrupt[len(corrupt)/2] ^= 0xff

	// Bytes that are no part of any image, where a decoder does not look.
	const hidden = "<script>alert(1)</script>"

	const (
		paletteNotUsed      = `the PNG holds a "PLTE" chunk, which its color type does not use`
		transparencyNotUsed = `the PNG holds a "tRNS" chunk, which its color type does not use`
		afterPixels         = "the PNG holds data after its pixels"
	)

	tests := []struct {
		name    string
		data    []byte
		wantMsg string
	}{
		{name: "empty", data: nil, wantMsg: "the image is empty"},
		{name: "a GIF", data: []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), wantMsg: "the image is not a PNG"},
		{name: "a JPEG", data: photo.Bytes(), wantMsg: "the image is not a PNG"},
		{name: "an SVG", data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), wantMsg: "the image is not a PNG"},
		{name: "only the signature", data: []byte(pngSignature), wantMsg: "the PNG is cut short"},
		{
			name:    "97 pixels wide",
			data:    encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 97, 1))),
			wantMsg: "icon is 97 x 1 pixels; the limit is 96 x 96",
		},
		{
			name:    "97 pixels high",
			data:    encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 1, 97))),
			wantMsg: "icon is 1 x 97 pixels; the limit is 96 x 96",
		},
		{name: "no pixels wide", data: headerOnly(0, 1), wantMsg: "icon is 0 x 1 pixels"},
		{
			// Refused on its header alone: nothing is decoded.
			name:    "a header that claims 50000 by 50000 pixels",
			data:    headerOnly(50000, 50000),
			wantMsg: "icon is 50000 x 50000 pixels; the limit is 96 x 96",
		},
		{
			name:    "a header that claims the most a PNG can",
			data:    headerOnly(0xffffffff, 0xffffffff),
			wantMsg: "icon is 4294967295 x 4294967295 pixels",
		},
		{name: "a header and no pixels", data: headerOnly(1, 1), wantMsg: "the PNG cannot be decoded"},
		{
			name:    "a text chunk",
			data:    withChunk(t, "tEXt", []byte("Comment\x00<script>alert(1)</script>")),
			wantMsg: `the PNG holds a "tEXt" chunk; only IHDR, PLTE, tRNS, IDAT and IEND are allowed`,
		},
		{
			name:    "an animation chunk",
			data:    withChunk(t, "acTL", []byte{0, 0, 0, 1, 0, 0, 0, 0}),
			wantMsg: `the PNG holds a "acTL" chunk`,
		},
		{
			name:    "a color profile chunk",
			data:    withChunk(t, "iCCP", []byte("x\x00\x00")),
			wantMsg: `the PNG holds a "iCCP" chunk`,
		},
		{
			name:    "bytes after the end",
			data:    append(bytes.Clone(fixture), "<html>"...),
			wantMsg: "the PNG has data after its end",
		},
		{
			name:    "a chunk after the end",
			data:    joinPNG(chunks[0], chunks[1], chunks[2], pngChunk{kind: chunkIDAT, data: nil}),
			wantMsg: "the PNG has data after its end",
		},
		{name: "no end", data: joinPNG(chunks[0], chunks[1]), wantMsg: "the PNG is cut short"},
		{name: "a chunk cut short", data: fixture[:len(fixture)-17], wantMsg: "the PNG is cut short"},
		{
			name:    "an end with data",
			data:    joinPNG(chunks[0], chunks[1], pngChunk{kind: chunkIEND, data: []byte{0}}),
			wantMsg: "the PNG does not end as a PNG does",
		},
		{
			name:    "pixels before the header",
			data:    joinPNG(chunks[1], chunks[0], chunks[2]),
			wantMsg: "the PNG does not start with its header",
		},
		{
			name:    "a header of the wrong length",
			data:    joinPNG(pngChunk{kind: chunkIHDR, data: chunks[0].data[:12]}, chunks[1], chunks[2]),
			wantMsg: "the PNG does not start with its header",
		},
		{
			name:    "two headers",
			data:    joinPNG(chunks[0], chunks[0], chunks[1], chunks[2]),
			wantMsg: "the PNG has a second header",
		},
		{
			name:    "a palette beside pixels that are colors",
			data:    withChunk(t, chunkPLTE, []byte(strings.Repeat(hidden, 30))),
			wantMsg: paletteNotUsed,
		},
		{
			name:    "a palette beside color pixels without alpha",
			data:    onePixel(colorRGB, []byte{1, 2, 3}, pngChunk{kind: chunkPLTE, data: []byte("abc")}),
			wantMsg: paletteNotUsed,
		},
		{
			name:    "a palette beside grayscale pixels",
			data:    onePixel(colorGray, []byte{1}, pngChunk{kind: chunkPLTE, data: []byte("abc")}),
			wantMsg: paletteNotUsed,
		},
		{
			name:    "a transparent color beside pixels with alpha",
			data:    withChunk(t, chunkTRNS, []byte("abcdef")),
			wantMsg: transparencyNotUsed,
		},
		{
			name:    "a transparent shade beside grayscale pixels with alpha",
			data:    onePixel(colorGrayAlpha, []byte{1, 2}, pngChunk{kind: chunkTRNS, data: []byte("ab")}),
			wantMsg: transparencyNotUsed,
		},
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
		{name: "a wrong checksum", data: badChecksum, wantMsg: "the PNG cannot be decoded"},
		{
			name:    "corrupt pixels",
			data:    joinPNG(chunks[0], pngChunk{kind: chunkIDAT, data: corrupt}, chunks[2]),
			wantMsg: "the PNG cannot be decoded",
		},
		{
			name:    "one byte too many",
			data:    append([]byte(pngSignature), make([]byte, builder.MaxIconBytes+1-len(pngSignature))...),
			wantMsg: "the image is 40961 bytes; the limit is 40960",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			width, height, err := builder.ValidateIconPNG(test.data)
			if err == nil {
				t.Fatalf("accepted, as %d x %d", width, height)
			}

			if !errors.Is(err, builder.ErrInvalidIcon) {
				t.Fatalf("error %v does not wrap ErrInvalidIcon", err)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.wantMsg)
			}

			if width != 0 || height != 0 {
				t.Fatalf("a refused image has the size %d x %d", width, height)
			}
		})
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

	if _, _, err := builder.ValidateIconPNG(loose); err == nil {
		t.Fatal("the loose PNG is accepted as it is")
	}

	normalized, err := builder.NormalizeIconPNG(loose)
	if err != nil {
		t.Fatalf("NormalizeIconPNG: %v", err)
	}

	if width, height, err := builder.ValidateIconPNG(normalized); err != nil || width != 3 || height != 2 {
		t.Fatalf("the normalized PNG is %d x %d, %v", width, height, err)
	}

	if got := pngKinds(t, normalized); got != "IHDR IDAT IEND" {
		t.Fatalf("the normalized PNG holds the chunks %s", got)
	}

	if got, want := pixels(t, normalized), pixels(t, strict); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the pixels changed:\nwant %v\ngot  %v", want, got)
	}

	// The same pixels give the same bytes, so the same icon id.
	again, err := builder.NormalizeIconPNG(normalized)
	if err != nil || !bytes.Equal(again, normalized) {
		t.Fatalf("normalizing twice changed the bytes (%v)", err)
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
				t.Fatalf("NormalizeIconPNG: %v", err)
			}

			bounds := test.img.Bounds()

			width, height, err := builder.ValidateIconPNG(normalized)
			if err != nil || width != bounds.Dx() || height != bounds.Dy() {
				t.Fatalf("the normalized PNG of %d bytes is %d x %d, %v", len(normalized), width, height, err)
			}

			if got := pixels(t, normalized)[test.at.Y*width+test.at.X]; got != test.want {
				t.Fatalf("pixel %v = %v, want %v", test.at, got, test.want)
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
			wantMsg: "icon is 97 x 96 pixels; the limit is 96 x 96",
		},
		{
			name:    "97 pixels high",
			data:    encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 1, 97))),
			wantMsg: "icon is 1 x 97 pixels; the limit is 96 x 96",
		},
		{
			name:    "a header that claims 50000 by 50000 pixels",
			data:    headerOnly(50000, 50000),
			wantMsg: "icon is 50000 x 50000 pixels; the limit is 96 x 96",
		},
		{name: "a header and no pixels", data: headerOnly(1, 1), wantMsg: "the PNG cannot be decoded"},
		{name: "only the signature", data: []byte(pngSignature), wantMsg: "the PNG cannot be decoded"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := builder.NormalizeIconPNG(test.data)
			if err == nil {
				t.Fatalf("normalized to %d bytes", len(normalized))
			}

			if !errors.Is(err, builder.ErrInvalidIcon) {
				t.Fatalf("error %v does not wrap ErrInvalidIcon", err)
			}

			if !strings.Contains(err.Error(), test.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.wantMsg)
			}
		})
	}
}

// distinctIcons returns count icons of one pixel each, no two alike, by icon
// id.
func distinctIcons(t *testing.T, count int) map[string]builder.Icon {
	t.Helper()

	icons := make(map[string]builder.Icon, count)

	for i := range count {
		img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		img.SetNRGBA(0, 0, color.NRGBA{R: byte(i), G: byte(i >> 8), B: 7, A: 255})

		data := encodePNG(t, img)
		icons[builder.IconID(data)] = builder.Icon{Name: "", Data: base64.StdEncoding.EncodeToString(data)}
	}

	if len(icons) != count {
		t.Fatalf("made %d distinct icons, want %d", len(icons), count)
	}

	return icons
}

func TestValidateIconsAccepts(t *testing.T) {
	if issues := builder.ValidateIcons(nil, "icons"); len(issues) != 0 {
		t.Fatalf("no icons: %v", issues)
	}

	icons := distinctIcons(t, builder.MaxDocumentIcons-1)
	icons[iconFixtureID] = builder.Icon{Name: strings.Repeat("é", builder.MaxIconNameBytes/2), Data: iconFixtureData}

	if issues := builder.ValidateIcons(icons, "icons"); len(issues) != 0 {
		t.Fatalf("%d icons: %v", len(icons), issues)
	}
}

func TestValidateIconsRefuses(t *testing.T) {
	fixture := iconFixture(t)
	encode := base64.StdEncoding.EncodeToString
	otherID := "sha256:" + strings.Repeat("0", 64)

	// The fixture's data ends "gg==": the last character holds four bits no
	// byte takes, which strict base64 wants zero.
	loosePadding := strings.TrimSuffix(iconFixtureData, "gg==") + "gh=="

	tests := []struct {
		name    string
		icons   map[string]builder.Icon
		wantMsg string
	}{
		{
			name:    "one icon too many",
			icons:   distinctIcons(t, builder.MaxDocumentIcons+1),
			wantMsg: "at most 50 custom icons are allowed, not 51",
		},
		{
			name:    "a key that is no icon id",
			icons:   map[string]builder.Icon{"plc": {Data: iconFixtureData}},
			wantMsg: `icon key "plc" must be sha256: and 64 hex digits`,
		},
		{
			name:    "a key in upper case",
			icons:   map[string]builder.Icon{strings.ToUpper(iconFixtureID): {Data: iconFixtureData}},
			wantMsg: "must be sha256: and 64 hex digits",
		},
		{
			name:    "a long key, cut in the message",
			icons:   map[string]builder.Icon{strings.Repeat("k", 100): {Data: iconFixtureData}},
			wantMsg: `icon key "` + strings.Repeat("k", 64) + `..." must be`,
		},
		{
			name:    "a name of 65 bytes",
			icons:   map[string]builder.Icon{iconFixtureID: {Name: strings.Repeat("n", 65), Data: iconFixtureData}},
			wantMsg: `icon "` + iconFixtureID[:64] + `..." name must be at most 64 bytes and contain no control characters`,
		},
		{
			name:    "a name with a line break",
			icons:   map[string]builder.Icon{iconFixtureID: {Name: "two\nlines", Data: iconFixtureData}},
			wantMsg: "name must be at most 64 bytes and contain no control characters",
		},
		{
			name:    "no data",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: ""}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "data that is not base64",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: "<svg onload=alert(1)>"}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "URL-safe base64",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: strings.Replace(iconFixtureData, "A", "_", 1)}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "base64 without padding",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: strings.TrimRight(iconFixtureData, "=")}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "base64 with a line break",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: iconFixtureData[:40] + "\n" + iconFixtureData[40:]}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "base64 with bits set in its padding",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: loosePadding}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "a data URL",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: "data:image/png;base64," + iconFixtureData}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name: "one byte too many",
			icons: map[string]builder.Icon{iconFixtureID: {
				Data: encode(append(bytes.Clone(fixture), make([]byte, builder.MaxIconBytes+1-len(fixture))...)),
			}},
			wantMsg: "data must be base64 of at most 40960 bytes",
		},
		{
			name:    "an SVG",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: encode([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))}},
			wantMsg: "is not an accepted PNG: the image is not a PNG",
		},
		{
			name:    "97 pixels wide",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: encode(headerOnly(97, 1))}},
			wantMsg: "is not an accepted PNG: icon is 97 x 1 pixels; the limit is 96 x 96",
		},
		{
			name:    "a text chunk",
			icons:   map[string]builder.Icon{iconFixtureID: {Data: encode(withChunk(t, "tEXt", []byte("a\x00b")))}},
			wantMsg: `is not an accepted PNG: the PNG holds a "tEXt" chunk`,
		},
		{
			name:    "a key of other bytes",
			icons:   map[string]builder.Icon{otherID: {Data: iconFixtureData}},
			wantMsg: `icon "` + otherID[:64] + `..." does not match its data`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := builder.ValidateIcons(test.icons, "library.icons")
			if len(issues) != 1 {
				t.Fatalf("got %d issues, want 1: %v", len(issues), issues)
			}

			if issues[0].Path != "library.icons" {
				t.Fatalf("issue path = %q, want the path given", issues[0].Path)
			}

			if !strings.Contains(issues[0].Message, test.wantMsg) {
				t.Fatalf("issue %q does not contain %q", issues[0].Message, test.wantMsg)
			}
		})
	}
}

// Keys are walked in order, so the issues of a set are the same every time.
func TestValidateIconsIsStable(t *testing.T) {
	icons := map[string]builder.Icon{
		"b":           {Data: "?"},
		"a":           {Data: iconFixtureData},
		iconFixtureID: {Name: "ok", Data: iconFixtureData},
	}

	first := fmt.Sprint(builder.ValidateIcons(icons, "icons"))

	// A malformed key is reported once: its data is not compared with it.
	if strings.Count(first, `"a"`) != 1 || strings.Contains(first, "does not match") {
		t.Fatalf("unexpected issues: %s", first)
	}

	if !strings.Contains(first, `icon key "a"`) || strings.Index(first, `"a"`) > strings.Index(first, `"b"`) {
		t.Fatalf("issues are not in key order: %s", first)
	}

	for range 20 {
		if again := fmt.Sprint(builder.ValidateIcons(icons, "icons")); again != first {
			t.Fatalf("issues changed between runs:\n%s\n%s", first, again)
		}
	}
}
