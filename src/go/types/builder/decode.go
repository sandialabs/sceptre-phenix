package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"phenix/util"
)

var (
	// ErrUnsupportedSchema is returned when a document does not carry
	// [SchemaURI].
	ErrUnsupportedSchema = errors.New("unsupported builder document schema")

	// ErrUnsupportedRevision is returned when a document carries a revision this
	// package cannot process.
	ErrUnsupportedRevision = errors.New("unsupported builder document revision")

	// ErrInvalidDocument wraps every validation failure so callers (for example
	// HTTP handlers mapping errors to status codes) can use [errors.Is].
	ErrInvalidDocument = errors.New("invalid builder document")
)

// Decode strictly decodes a builder document from JSON. It rejects unknown
// fields, trailing content, and documents with an unexpected schema or
// revision. Decode does not do semantic validation. Use [Parse] to decode
// and validate.
//
// The decoder refuses keys that their objects do not have, and values of the
// wrong type. When the editor also checks the type of each refused value,
// the error is a *[ValidationError]. It has an issue for each problem, in
// the words of the editor and with its codes. The editor checks the type of
// the metadata, its users, times and notes, the lists of nodes, networks and
// edges, the notes of a switch, the arrows of a line, the layout, the
// templates, the custom icons, and the included topologies and annotations
// of the source. For all other refusals, the error is the decoder error.
func Decode(data []byte) (*Document, error) {
	doc, err := DecodeReader(bytes.NewReader(data))
	if err == nil {
		return doc, nil
	}

	if issues := decodeIssues(data); issues != nil {
		return nil, &ValidationError{Issues: issues}
	}

	return nil, err
}

// DecodeReader behaves like [Decode], reading the document from r, except
// that its error for a key or a value the decoder refuses is always the
// decoder's own.
func DecodeReader(reader io.Reader) (*Document, error) {
	var doc Document

	if err := util.DecodeJSONStrict(reader, &doc); err != nil {
		return nil, fmt.Errorf("decoding builder document: %w", err)
	}

	if doc.Schema != SchemaURI {
		return nil, fmt.Errorf("%w: %q (expected %q)", ErrUnsupportedSchema, doc.Schema, SchemaURI)
	}

	if doc.Revision != SchemaRevision {
		return nil, fmt.Errorf(
			"%w: %d (expected %d)",
			ErrUnsupportedRevision,
			doc.Revision,
			SchemaRevision,
		)
	}

	if err := normalizeDocument(&doc); err != nil {
		return nil, fmt.Errorf("decoding builder document: %w", err)
	}

	return &doc, nil
}

// normalizeDocument makes free-form content (the specs of devices and
// templates) canonical, so decoded documents compare equal to generated
// ones. JSON decodes every number as a float. This function changes integral
// values back to int.
func normalizeDocument(doc *Document) error {
	for i := range doc.Nodes {
		device := doc.Nodes[i].Device
		if device == nil || device.Spec == nil {
			continue
		}

		spec, err := normalizeSpecMap(device.Spec)
		if err != nil {
			return fmt.Errorf("nodes[%d].device.spec: %w", i, err)
		}

		device.Spec = spec
	}

	for i := range doc.Templates {
		if err := doc.Templates[i].normalize(); err != nil {
			return fmt.Errorf("%s[%d].device.spec: %w", keyTemplates, i, err)
		}
	}

	return nil
}

// Parse strictly decodes and fully validates a builder document.
func Parse(data []byte) (*Document, error) {
	doc, err := Decode(data)
	if err != nil {
		return nil, err
	}

	if err := doc.Validate(); err != nil {
		return nil, err
	}

	return doc, nil
}

// Encode marshals a document to indented JSON.
func Encode(doc *Document) ([]byte, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding builder document: %w", err)
	}

	return data, nil
}

// truncate shortens a value for inclusion in an error message: to at most
// 64 bytes of whole characters, so a cut never leaves part of one.
func truncate(value string) string {
	const limit = 64

	if len(value) <= limit {
		return value
	}

	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}

	return value[:cut] + "..."
}
