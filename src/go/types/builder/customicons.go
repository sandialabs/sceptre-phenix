package builder

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Bounds on custom icons. At 96 pixels a side, an icon stays sharp at the
// Small size at the largest zoom on the densest screens, and an image that
// does not compress still fits in [MaxIconBytes].
const (
	// MaxIconPixels is the most pixels an icon has on a side.
	MaxIconPixels = 96

	// MaxIconBytes bounds the PNG of an icon.
	MaxIconBytes = 40960

	// MaxIconNameBytes bounds the name of an icon (see [IconNameProblem]).
	MaxIconNameBytes = 64

	// MaxDocumentIcons is the most custom icons a document may carry (see
	// [Document.Icons]).
	MaxDocumentIcons = 50
)

// pngSignature starts every PNG file. It is what makes bytes a PNG here: no
// file name, extension or declared type is ever read.
const pngSignature = "\x89PNG\r\n\x1a\n"

// The layout of a PNG chunk: the length of its data, its type, the data, and
// a checksum of the type and the data.
const (
	pngChunkLength = 4
	pngChunkType   = 4
	pngChunkCRC    = 4
	// pngChunkOverhead is what a chunk takes besides its data.
	pngChunkOverhead = pngChunkLength + pngChunkType + pngChunkCRC
	// pngHeaderBytes is the length of the data of the IHDR chunk, which
	// starts with the width and the height, pngSideBytes each. The color
	// type is its byte at pngColorTypeAt.
	pngHeaderBytes = 13
	pngSideBytes   = 4
	pngColorTypeAt = 9
)

// The color types that take a palette or a transparent color: grayscale
// and color without alpha name one transparent color, and only an image of
// palette indexes has a palette.
const (
	pngGrayscale = 0
	pngTrueColor = 2
	pngIndexed   = 3
)

// The chunks an icon may hold: the header, a palette and its transparency,
// the pixels and the end. Text, metadata, color profiles and animation
// frames have no use in an icon.
const (
	pngIHDR = "IHDR"
	pngPLTE = "PLTE"
	pngTRNS = "tRNS"
	pngIDAT = "IDAT"
	pngIEND = "IEND"
)

// ErrInvalidIcon is what every refusal of [ValidateIconPNG] and
// [NormalizeIconPNG] wraps. The refusal's text says why after it.
var ErrInvalidIcon = errors.New("icon is not an accepted PNG")

// Icon is a custom icon a document carries, under its name (see
// [Document.Icons]). A document carries one only when it is to stand on its
// own, as a downloaded file does: on a phenix server, a node names an icon
// of the server's icon library and the document carries nothing of it.
type Icon struct {
	// Data is the PNG, in standard base64 with padding.
	Data string `json:"data"`
}

// iconNamePattern is the form of an icon name: the characters a phenix
// config name may hold.
var iconNamePattern = regexp.MustCompile(`^[A-Za-z0-9_@.-]+$`)

// IconNameProblem says why name is no icon name, or returns "". An icon
// name is 1 to [MaxIconNameBytes] letters, digits, "_", "@", "." and "-",
// the characters of a phenix config name, and is neither "." nor "..". Two
// names that differ only in case name the same icon of a server's library.
func IconNameProblem(name string) string {
	switch {
	case name == "" || len(name) > MaxIconNameBytes || !iconNamePattern.MatchString(name):
		return fmt.Sprintf(
			`icon name %q must be 1 to %d letters, digits, "_", "@", "." or "-"`, truncate(name), MaxIconNameBytes,
		)
	case name == "." || name == "..":
		return fmt.Sprintf(`icon name %q must not be "." or ".."`, name)
	}

	return ""
}

// IconID returns the id of an icon's image: "sha256:" and the lowercase hex
// SHA-256 of its PNG bytes. The same image has the same id everywhere, so
// two icons are the same image exactly when their ids are equal.
func IconID(png []byte) string {
	sum := sha256.Sum256(png)

	return digestPrefix + hex.EncodeToString(sum[:])
}

// ValidateIconPNG reports whether data is a PNG this package accepts as an
// icon, and returns its width and height. An accepted icon is small, holds
// only the chunks IHDR, PLTE, tRNS, IDAT and IEND, and nothing after its
// pixels. It reads the size before it decodes, so a huge claimed size costs nothing.
func ValidateIconPNG(data []byte) (int, int, error) {
	width, height, pixels, err := iconChunks(data)
	if err != nil {
		return 0, 0, err
	}

	// It checks the checksum of every chunk and inflates the pixels.
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return 0, 0, errUndecodable(err)
	}

	if err := checkPixelData(pixels); err != nil {
		return 0, 0, err
	}

	return width, height, nil
}

// IconPNGSize returns the width and height of an icon PNG. It does the
// checks of [ValidateIconPNG] that decode no pixels. Use it only for a PNG
// that was validated when it was stored.
func IconPNGSize(data []byte) (int, int, error) {
	width, height, _, err := iconChunks(data)

	return width, height, err
}

// iconChunks checks the size and the signature of an icon PNG, and walks its
// chunks (see walkIconChunks).
func iconChunks(data []byte) (int, int, []byte, error) {
	switch {
	case len(data) == 0:
		return 0, 0, nil, iconErrorf("the image is empty")
	case len(data) > MaxIconBytes:
		return 0, 0, nil, iconErrorf("the image is %d bytes: the limit is %d", len(data), MaxIconBytes)
	case !bytes.HasPrefix(data, []byte(pngSignature)):
		return 0, 0, nil, errNotPNG()
	}

	return walkIconChunks(data)
}

// checkPixelData refuses data after the pixels in the joined IDAT stream of a
// PNG that the decoder accepted. The decoder skips such bytes, so they would
// be kept without being part of the image.
func checkPixelData(joined []byte) error {
	// The decompressor reads a [bytes.Reader] byte by byte, so it takes
	// nothing past the end of its stream.
	source := bytes.NewReader(joined)

	stream, err := zlib.NewReader(source)
	if err != nil {
		return errUndecodable(err)
	}

	defer func() { _ = stream.Close() }()

	if _, err := io.Copy(io.Discard, stream); err != nil {
		return errUndecodable(err)
	}

	if source.Len() > 0 {
		return iconErrorf("the PNG holds data after its pixels")
	}

	return nil
}

// NormalizeIconPNG decodes any PNG of at most [MaxIconPixels] a side and
// encodes it again, 8 bits a channel with alpha. Only the pixels survive:
// text, metadata, color profiles, further frames and anything after the end
// of the image are gone, so the result passes [ValidateIconPNG]. An image
// larger than that is refused, never scaled.
func NormalizeIconPNG(input []byte) ([]byte, error) {
	if !bytes.HasPrefix(input, []byte(pngSignature)) {
		return nil, errNotPNG()
	}

	config, err := png.DecodeConfig(bytes.NewReader(input))
	if err != nil {
		return nil, errUndecodable(err)
	}

	if !iconSide(config.Width) || !iconSide(config.Height) {
		return nil, iconSizeError(int64(config.Width), int64(config.Height))
	}

	decoded, err := png.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, errUndecodable(err)
	}

	bounds := decoded.Bounds()
	pixels := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(pixels, pixels.Bounds(), decoded, bounds.Min, draw.Src)

	var output bytes.Buffer

	// The encoder writes IHDR, IDAT and IEND, and nothing else.
	encoder := png.Encoder{CompressionLevel: png.BestCompression, BufferPool: nil}
	if err := encoder.Encode(&output, pixels); err != nil {
		return nil, iconErrorf("the Builder cannot encode the PNG: %v", err)
	}

	return output.Bytes(), nil
}

// ValidateIcons checks the custom icons of a document at path: the count, each
// name, and each data as standard base64 of a PNG that [ValidateIconPNG]
// accepts. It checks keys in order, so the issues are stable.
func ValidateIcons(icons map[string]Icon, path string) []Issue {
	var issues []Issue

	addf := func(code Code, format string, args ...any) {
		issues = append(issues, NewIssue(code, path, fmt.Sprintf(format, args...)))
	}

	if len(icons) > MaxDocumentIcons {
		addf(CodeIconListTooMany, "a document can carry at most %d custom icons, not %d", MaxDocumentIcons, len(icons))
	}

	for _, key := range slices.Sorted(maps.Keys(icons)) {
		icon := icons[key]
		shown := truncate(key)

		if problem := IconNameProblem(key); problem != "" {
			addf(CodeIconNameInvalid, "%s", problem)
		}

		data, ok := decodeIconData(icon.Data)
		if !ok {
			addf(CodeIconDataInvalid, "icon %q data must be base64 of at most %d bytes", shown, MaxIconBytes)

			continue
		}

		if _, _, err := ValidateIconPNG(data); err != nil {
			addf(CodeIconPNGInvalid, "icon %q is not an accepted PNG: %s", shown, iconReason(err))
		}
	}

	return issues
}

// decodeIconData returns the bytes of an icon's data: strict standard
// base64, with padding and without line breaks, of 1 to [MaxIconBytes]
// bytes. The text is measured before it is decoded, so data far past the
// limit costs nothing.
func decodeIconData(text string) ([]byte, bool) {
	// The decoder skips line breaks, which the schema's pattern refuses.
	if text == "" || len(text) > base64.StdEncoding.EncodedLen(MaxIconBytes) || strings.ContainsAny(text, "\r\n") {
		return nil, false
	}

	data, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil || len(data) == 0 || len(data) > MaxIconBytes {
		return nil, false
	}

	return data, true
}

// walkIconChunks checks the chunks of a PNG, without decoding anything: the
// first is the header, each is one an icon may hold and its color type
// uses, each lies inside data, and the last is the end, with nothing after
// it. It returns the width and height the header gives, each 1 to
// [MaxIconPixels], and the data of the IDAT chunks, joined.
func walkIconChunks(data []byte) (int, int, []byte, error) {
	var (
		width, height int
		colorType     byte
		pixels        []byte
		ended         bool
	)

	rest := data[len(pngSignature):]

	for first := true; len(rest) > 0; first = false {
		if ended {
			return 0, 0, nil, iconErrorf("the PNG has data after its end")
		}

		if len(rest) < pngChunkOverhead {
			return 0, 0, nil, errCutShort()
		}

		length := binary.BigEndian.Uint32(rest)
		kind := string(rest[pngChunkLength : pngChunkLength+pngChunkType])

		if uint64(length) > uint64(len(rest)-pngChunkOverhead) {
			return 0, 0, nil, errCutShort()
		}

		body := rest[pngChunkLength+pngChunkType : pngChunkLength+pngChunkType+int(length)]
		rest = rest[pngChunkOverhead+int(length):]

		switch {
		case first && (kind != pngIHDR || len(body) != pngHeaderBytes):
			return 0, 0, nil, iconErrorf("the PNG does not start with its header")
		case first:
			wide, high := binary.BigEndian.Uint32(body), binary.BigEndian.Uint32(body[pngSideBytes:])
			if wide < 1 || wide > MaxIconPixels || high < 1 || high > MaxIconPixels {
				return 0, 0, nil, iconSizeError(int64(wide), int64(high))
			}

			width, height = int(wide), int(high)
			colorType = body[pngColorTypeAt]
		case kind == pngIHDR:
			return 0, 0, nil, iconErrorf("the PNG has a second header")
		case kind == pngIEND:
			if len(body) != 0 {
				return 0, 0, nil, iconErrorf("the PNG does not end as a PNG does")
			}

			ended = true
		case kind == pngIDAT:
			pixels = append(pixels, body...)
		case kind != pngPLTE && kind != pngTRNS:
			return 0, 0, nil, iconErrorf("the PNG holds a %q chunk: an icon can hold only IHDR, PLTE, tRNS, IDAT and IEND chunks", kind)
		case !colorTypeUses(colorType, kind):
			return 0, 0, nil, iconErrorf("the PNG holds a %q chunk, which its color type does not use", kind)
		}
	}

	if !ended {
		return 0, 0, nil, errCutShort()
	}

	return width, height, pixels, nil
}

// colorTypeUses reports whether an image of the color type uses a PLTE or a
// tRNS chunk. Only pixels that are palette indexes use a palette. For pixels
// that are colors, a palette is a suggestion that no decoder needs.
// Transparency goes with a palette, or names the one transparent color of
// an image without alpha. An image with alpha has no transparency.
func colorTypeUses(colorType byte, kind string) bool {
	switch colorType {
	case pngIndexed:
		return true
	case pngGrayscale, pngTrueColor:
		return kind == pngTRNS
	default:
		return false
	}
}

// iconSide reports whether pixels is a width or a height an icon may have.
func iconSide(pixels int) bool {
	return pixels >= 1 && pixels <= MaxIconPixels
}

// iconSizeError is the refusal of an image of the wrong size.
func iconSizeError(width, height int64) error {
	return iconErrorf("icon is %d x %d pixels: the limit is %d x %d", width, height, MaxIconPixels, MaxIconPixels)
}

// errNotPNG is the refusal of bytes that do not start as a PNG does.
func errNotPNG() error {
	return iconErrorf("the image is not a PNG")
}

// errCutShort is the refusal of a PNG that ends inside a chunk, or before
// its end chunk.
func errCutShort() error {
	return iconErrorf("the PNG is not complete")
}

// errUndecodable is the refusal of a PNG the decoder refuses, for the reason
// it gives.
func errUndecodable(err error) error {
	return iconErrorf("the Builder cannot decode the PNG: %v", err)
}

// iconErrorf returns a refusal of an icon, which says why after "icon is
// not an accepted PNG: ".
func iconErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidIcon, fmt.Sprintf(format, args...))
}

// iconReason is the reason of a refusal, without what every one starts
// with.
func iconReason(err error) string {
	return strings.TrimPrefix(err.Error(), ErrInvalidIcon.Error()+": ")
}
