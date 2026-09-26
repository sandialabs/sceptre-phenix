package builder

import (
	"crypto/sha1" //nolint:gosec // RFC 4122 mandates SHA-1 for version 5 UUIDs
	"encoding/hex"
	"strings"
)

// uuidNamespaceURL is the RFC 4122 URL namespace UUID. It seeds the builder
// namespace, so builder identifiers cannot collide with identifiers minted by
// other name based UUID schemes.
const uuidNamespaceURL = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

// uuidLength is the length of the canonical 8-4-4-4-12 UUID text form.
const uuidLength = 36

// Bit masks and shifts of the RFC 4122 layout.
const (
	uuidVersionMask  = 0x0f
	uuidVersion5     = 0x50
	uuidVersionShift = 4
	uuidVariantMask  = 0x3f
	uuidVariantRFC   = 0x80
	uuidVariantCheck = 0xc0
	uuidMinVersion   = 1
	uuidMaxVersion   = 8
)

// NamespaceUUID returns the RFC 4122 namespace UUID of builder identifiers. It
// is derived from [SchemaURI] in the standard URL namespace, so it is stable
// for the lifetime of the schema and changes if the schema URI ever changes.
func NamespaceUUID() string {
	return UUIDv5(uuidNamespaceURL, SchemaURI)
}

// UUIDv5 returns the RFC 4122 version 5 (name based, SHA-1) UUID of name inside
// the given namespace UUID. It returns the empty string when namespace is not a
// valid UUID, which cannot happen for the namespaces this package uses.
//
// The returned value carries the correct version (5) and variant (RFC 4122)
// bits, so it is a valid UUID for consumers such as JSON Schema "format":
// "uuid" and the browser's crypto.randomUUID contract.
func UUIDv5(namespace, name string) string {
	raw, ok := parseUUID(namespace)
	if !ok {
		return ""
	}

	hash := sha1.New() //nolint:gosec // RFC 4122 mandates SHA-1 for version 5
	hash.Write(raw[:])
	hash.Write([]byte(name))

	var id [16]byte

	copy(id[:], hash.Sum(nil))

	id[6] = (id[6] & uuidVersionMask) | uuidVersion5
	id[8] = (id[8] & uuidVariantMask) | uuidVariantRFC

	return formatUUID(id)
}

// IsUUID reports whether value is a canonical RFC 4122 UUID string with a known
// version (1-8) and the RFC 4122 variant. Comparison is case-insensitive, which
// matches the case-insensitive identifier comparison performed elsewhere in
// this package.
func IsUUID(value string) bool {
	_, ok := parseUUID(value)

	return ok
}

// formatUUID renders 16 bytes in the canonical 8-4-4-4-12 text form.
func formatUUID(id [16]byte) string {
	encoded := hex.EncodeToString(id[:])

	return strings.Join([]string{
		encoded[0:8],
		encoded[8:12],
		encoded[12:16],
		encoded[16:20],
		encoded[20:32],
	}, "-")
}

// parseUUID decodes a canonical UUID string, rejecting malformed values,
// unknown versions, and non RFC 4122 variants.
func parseUUID(value string) ([16]byte, bool) {
	var id [16]byte

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
