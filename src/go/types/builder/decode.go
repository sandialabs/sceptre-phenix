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

// Decode strictly decodes a builder document from JSON. Unknown fields, trailing
// content, and documents carrying an unexpected schema or revision are rejected.
// Decode does not perform semantic validation; use [Parse] for decode plus
// validation.
//
// When what the decoder refuses is keys their objects do not have and values
// of the wrong type where the editor checks the type too (the metadata, its
// users, times and notes, the lists of nodes, networks and edges, a switch's
// notes, a line's arrows, the layout, the templates, the custom icons, and
// the source's included topologies and annotations), the error is a
// *[ValidationError] with an issue for each, in the editor's words and with
// its codes. Otherwise it is the decoder's own.
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

// normalizeDocument canonicalizes free-form content (the specs of devices and
// templates) so decoded documents compare equal to generated ones. JSON
// decodes every number as a float; integral values are restored to int.
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
