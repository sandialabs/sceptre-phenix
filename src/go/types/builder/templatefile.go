package builder

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"phenix/util"
)

// TemplateFileSchemaURI identifies the format of a template file: one
// collection of device templates, as the Builder exports it and as phenix
// reads it from its template directory.
const TemplateFileSchemaURI = "https://phenix.sandia.gov/schemas/builder/templates/v1"

// Bounds on a template file.
const (
	// MaxTemplateFileBytes bounds the text of a template file (8 MiB). It
	// holds the most templates a collection holds, each at its largest, with
	// the most custom icons a file carries.
	MaxTemplateFileBytes = 8 << 20

	// MaxTemplateFileTemplates is the most templates a template file holds:
	// the most a collection of a template library holds.
	MaxTemplateFileTemplates = 200
)

// ErrInvalidTemplateFile is what a [TemplateFileError] unwraps to, and what
// [ParseTemplateFile] wraps every refusal of a file's text in.
var ErrInvalidTemplateFile = errors.New("invalid template file")

// TemplateFile is a collection of device templates as a file holds it, YAML
// or JSON. The Builder writes one when it exports templates, imports one
// into a user's library, and phenix reads every one in its template
// directory at start as a read-only collection of the server's.
type TemplateFile struct {
	// Schema is [TemplateFileSchemaURI].
	Schema string `json:"$schema"`
	// Name is the name of the collection the file holds.
	Name string `json:"name"`
	// Description is the collection's description.
	Description string `json:"description,omitempty"`
	// Templates are the collection's templates, in order.
	Templates []TemplateFileTemplate `json:"templates"`
	// Icons are copies of the custom icons the templates name, by icon name,
	// as a downloaded Builder document carries them. A template may also
	// name an icon the file does not carry, which the server's icon library
	// is expected to hold.
	Icons map[string]Icon `json:"icons,omitempty"`
}

// TemplateFileTemplate is one template of a template file. It has no
// identifier: where the template is kept gives it one.
type TemplateFileTemplate struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Device      TemplateDevice `json:"device"`
}

// TemplateFileError lists everything that makes a template file unusable.
// It unwraps to [ErrInvalidTemplateFile].
type TemplateFileError struct {
	Issues []Issue
}

func (e *TemplateFileError) Error() string {
	messages := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		messages[i] = issue.String()
	}

	return fmt.Sprintf("%s: %s", ErrInvalidTemplateFile.Error(), strings.Join(messages, "; "))
}

// Unwrap allows [errors.Is](err, ErrInvalidTemplateFile).
func (e *TemplateFileError) Unwrap() error {
	return ErrInvalidTemplateFile
}

// ParseTemplateFile decodes and validates the text of a template file:
// JSON, or YAML read as [JSONFromYAML] reads a Builder document, so anchors,
// aliases, merge keys and a second document are refused. The content
// decides, never the name of the file. Text that is empty or longer than
// [MaxTemplateFileBytes], that does not decode strictly into a
// [TemplateFile], or that [TemplateFile.Validate] refuses is an error
// matching [ErrInvalidTemplateFile], which says why; a file that decodes but
// does not validate gives a [TemplateFileError].
func ParseTemplateFile(text []byte) (*TemplateFile, error) {
	switch {
	case len(bytes.TrimSpace(text)) == 0:
		return nil, fmt.Errorf("%w: the file is empty", ErrInvalidTemplateFile)
	case len(text) > MaxTemplateFileBytes:
		return nil, fmt.Errorf(
			"%w: the file is %d bytes; the limit is %d", ErrInvalidTemplateFile, len(text), MaxTemplateFileBytes,
		)
	}

	data, err := JSONFromText(text)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTemplateFile, err)
	}

	file, err := DecodeTemplateFile(data)
	if err != nil {
		return nil, err
	}

	if err := file.Validate(); err != nil {
		return nil, err
	}

	return file, nil
}

// DecodeTemplateFile strictly decodes a template file from JSON: unknown
// fields and trailing content are refused. The specs of its templates are
// canonicalized as a document's are. It does not validate the file: see
// [ParseTemplateFile].
func DecodeTemplateFile(data []byte) (*TemplateFile, error) {
	var file TemplateFile

	if err := util.DecodeJSONStrict(bytes.NewReader(data), &file); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTemplateFile, err)
	}

	for i := range file.Templates {
		template := file.Templates[i].Template("")

		if err := template.normalize(); err != nil {
			return nil, fmt.Errorf("%w: %s[%d].device.spec: %w", ErrInvalidTemplateFile, keyTemplates, i, err)
		}

		file.Templates[i].Device = template.Device
	}

	return &file, nil
}

// Template returns the template as a library or a document keeps it, under
// the given identifier.
func (t *TemplateFileTemplate) Template(id string) Template {
	return Template{ID: id, Name: t.Name, Description: t.Description, Device: t.Device}
}

// Validate checks a decoded template file, and returns nil or a
// *[TemplateFileError] listing every issue:
//
//   - a "$schema" other than [TemplateFileSchemaURI],
//   - a collection name that is blank, longer than [MaxTemplateNameBytes] or
//     holds control characters, and a description longer than
//     [MaxTemplateDescriptionBytes] or holding control characters, the
//     rules a collection of a library follows,
//   - no template, or more than [MaxTemplateFileTemplates],
//   - a template [Template.Issues] refuses, as a library refuses it,
//   - two templates whose names differ only in case,
//   - custom icons [ValidateIcons] refuses, as a document's are refused.
//
// A template's custom icon may name an icon the file carries or one it does
// not, which the server's icon library is expected to hold.
func (f *TemplateFile) Validate() error {
	if issues := f.Issues(); len(issues) > 0 {
		return &TemplateFileError{Issues: issues}
	}

	return nil
}

// Issues returns what [TemplateFile.Validate] finds, in the order of the
// file.
func (f *TemplateFile) Issues() []Issue {
	var issues []Issue

	addf := func(at, format string, args ...any) {
		issues = append(issues, Issue{Path: at, Message: fmt.Sprintf(format, args...)})
	}

	if f.Schema != TemplateFileSchemaURI {
		addf(schemaKey, "template file schema must be %q, not %q", TemplateFileSchemaURI, truncate(f.Schema))
	}

	switch {
	case strings.TrimSpace(f.Name) == "":
		addf(keyName, "collection name is required")
	case len(f.Name) > MaxTemplateNameBytes:
		addf(keyName, "collection name must be at most %d bytes", MaxTemplateNameBytes)
	case strings.ContainsFunc(f.Name, isControl):
		addf(keyName, "collection name must not contain control characters")
	}

	switch {
	case len(f.Description) > MaxTemplateDescriptionBytes:
		addf(keyDescription, "collection description must be at most %d bytes", MaxTemplateDescriptionBytes)
	case strings.ContainsFunc(f.Description, isControl):
		addf(keyDescription, "collection description must not contain control characters")
	}

	switch {
	case len(f.Templates) == 0:
		addf(keyTemplates, "a template file holds at least one template")
	case len(f.Templates) > MaxTemplateFileTemplates:
		addf(
			keyTemplates, "a template file holds at most %d templates, not %d",
			MaxTemplateFileTemplates, len(f.Templates),
		)
	}

	names := make(map[string]int, len(f.Templates))

	for i := range f.Templates {
		path := fmt.Sprintf("%s[%d]", keyTemplates, i)
		template := f.Templates[i].Template("")

		issues = append(issues, template.Issues(path)...)

		name := strings.ToLower(strings.TrimSpace(template.Name))
		if name == "" {
			continue
		}

		if first, seen := names[name]; seen {
			addf(
				path+"."+keyName, "template name %q is also the name of %s[%d], ignoring case",
				truncate(template.Name), keyTemplates, first,
			)

			continue
		}

		names[name] = i
	}

	return append(issues, ValidateIcons(f.Icons, keyIcons)...)
}
