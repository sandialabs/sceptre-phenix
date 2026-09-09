package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// idSeparator is a value separator that cannot appear in the semantic keys used
// to derive identifiers, so ("a", "bc") and ("ab", "c") never collide.
const idSeparator = "\x1f"

// DocumentID returns the deterministic document UUID for a document name.
func DocumentID(name string) string {
	return deterministicID("document", strings.ToLower(name))
}

// DeviceNodeID returns the deterministic node UUID of a device.
func DeviceNodeID(hostname string) string {
	return deterministicID("device", strings.ToLower(hostname))
}

// SwitchNodeID returns the deterministic node ID of the switch hub of a
// network.
func SwitchNodeID(network string) string {
	return deterministicID("switch", strings.ToLower(network))
}

// NetworkID returns the deterministic UUID of a network (VLAN) name.
func NetworkID(name string) string {
	return deterministicID("network", strings.ToLower(name))
}

// NoteNodeID returns the deterministic node ID of a note, keyed by an arbitrary
// caller-supplied key.
func NoteNodeID(key string) string {
	return deterministicID("note", key)
}

// GroupNodeID returns the deterministic node ID of a group, keyed by an
// arbitrary caller-supplied key.
func GroupNodeID(key string) string {
	return deterministicID("group", key)
}

// InterfaceHandleID returns the deterministic handle ID for an interface of a
// device. The index is included so unnamed interfaces still receive stable,
// unique handles.
func InterfaceHandleID(hostname, iface string, index int) string {
	return deterministicID(
		"handle",
		strings.ToLower(hostname),
		strings.ToLower(iface),
		strconv.Itoa(index),
	)
}

// EdgeID returns the deterministic ID of an edge between two endpoints.
func EdgeID(sourceNodeID, sourceHandleID, targetNodeID, targetHandleID string) string {
	return deterministicID(
		"edge",
		sourceNodeID,
		sourceHandleID,
		targetNodeID,
		targetHandleID,
	)
}

// ContentDigest returns a stable "sha256:<hex>" digest of arbitrary JSON
// encodable content. Map keys are sorted by encoding/json, so the digest is
// independent of map iteration order.
func ContentDigest(content any) (string, error) {
	if content == nil {
		return "", nil
	}

	data, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("marshaling content for digest: %w", err)
	}

	sum := sha256.Sum256(data)

	return digestPrefix + hex.EncodeToString(sum[:]), nil
}

// deterministicID derives a stable RFC 4122 name based UUID from a kind and the
// semantic key parts of an entity, inside the builder namespace.
func deterministicID(parts ...string) string {
	return UUIDv5(NamespaceUUID(), strings.Join(parts, idSeparator))
}

// digestPrefix is the algorithm prefix of digests produced by [ContentDigest].
const digestPrefix = "sha256:"

// digestHexLength is the number of hex characters in a SHA-256 digest.
const digestHexLength = 64

// isContentDigest reports whether value has the "sha256:<64 hex>" shape
// produced by [ContentDigest].
func isContentDigest(value string) bool {
	encoded, found := strings.CutPrefix(value, digestPrefix)
	if !found || len(encoded) != digestHexLength {
		return false
	}

	_, err := hex.DecodeString(encoded)

	return err == nil
}
