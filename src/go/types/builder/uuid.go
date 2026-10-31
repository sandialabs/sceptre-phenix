package builder

import (
	"encoding/hex"
	"strings"

	"github.com/gofrs/uuid/v5"
)

// uuidLength is the length of the canonical 8-4-4-4-12 UUID text form.
const uuidLength = 36

// Bit masks and shifts of the RFC 4122 layout.
const (
	uuidVersionShift = 4
	uuidVariantRFC   = 0x80
	uuidVariantCheck = 0xc0
	uuidMinVersion   = 1
	uuidMaxVersion   = 8
)

// NamespaceUUID returns the RFC 4122 namespace UUID of builder identifiers. It
// is derived from [SchemaURI] in the standard URL namespace, so it is stable
// for the lifetime of the schema and changes if the schema URI ever changes.
// The URL namespace seeds it, so builder identifiers cannot collide with
// identifiers minted by other name based UUID schemes.
func NamespaceUUID() string {
	return uuid.NewV5(uuid.NamespaceURL, SchemaURI).String()
}

// UUIDv5 returns the RFC 4122 version 5 UUID of name inside the namespace UUID, or "" for a bad namespace.
// It sets the version and variant bits, so JSON Schema "format": "uuid" accepts it.
func UUIDv5(namespace, name string) string {
	raw, ok := parseUUID(namespace)
	if !ok {
		return ""
	}

	return uuid.NewV5(raw, name).String()
}

// IsUUID reports whether value is a canonical RFC 4122 UUID string with a known
// version (1-8) and the RFC 4122 variant, in either case. [Document.Validate]
// finds duplicate identifiers ignoring case, but a reference must repeat an
// identifier exactly.
func IsUUID(value string) bool {
	_, ok := parseUUID(value)

	return ok
}

// parseUUID decodes a canonical UUID string, rejecting malformed values,
// unknown versions, and non RFC 4122 variants.
func parseUUID(value string) (uuid.UUID, bool) {
	var id uuid.UUID

	if len(value) != uuidLength {
		return id, false
	}

	for _, position := range []int{8, 13, 18, 23} {
		if value[position] != '-' {
			return id, false
		}
	}

	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(decoded) != len(id) {
		return id, false
	}

	copy(id[:], decoded)

	if version := id[6] >> uuidVersionShift; version < uuidMinVersion || version > uuidMaxVersion {
		return id, false
	}

	if id[8]&uuidVariantCheck != uuidVariantRFC {
		return id, false
	}

	return id, true
}
