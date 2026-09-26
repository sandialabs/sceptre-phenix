package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
func Decode(data []byte) (*Document, error) {
	return DecodeReader(bytes.NewReader(data))
}

// DecodeReader behaves like [Decode], reading the document from r.
func DecodeReader(reader io.Reader) (*Document, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var doc Document

	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decoding builder document: %w", err)
	}

	// decoder.More reports whether more values exist in a *stream*; it is false
	// for a trailing scalar such as "null" and unreliable at the top level. A
	// second decode that does not hit io.EOF therefore means the payload carried
	// more than one JSON value.
	var trailing json.RawMessage

	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf(
				"decoding builder document: unexpected trailing content: %s",
				truncate(string(trailing)),
			)
		}

		return nil, fmt.Errorf("decoding builder document: unexpected trailing content: %w", err)
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

// normalizeDocument canonicalizes free-form content (device specs and scenario
// content) so decoded documents compare equal to generated ones. JSON decodes
// every number as a float; integral values are restored to int.
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

	if doc.Scenario != nil && doc.Scenario.Content != nil {
		content, err := normalizeSpecMap(doc.Scenario.Content)
		if err != nil {
			return fmt.Errorf("scenario.content: %w", err)
		}

		doc.Scenario.Content = content
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

// truncate shortens a value for inclusion in an error message.
func truncate(value string) string {
	const limit = 64

	if len(value) <= limit {
		return value
	}

	return value[:limit] + "..."
}
