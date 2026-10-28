package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"phenix/store"
	"phenix/types/builder"
)

// maxLibraryAttempts bounds how often [Service.UpdateLibrary] reads the
// library and applies the change again after another write was first.
const maxLibraryAttempts = 5

// Kinds named in the typed errors of the template library.
const (
	kindLibrary    = "template library"
	kindTemplate   = "template"
	kindCollection = "collection"
)

// LibraryKey is the key of the one record in [NamespaceTemplates] that
// holds the template library of the server.
const LibraryKey = "library"

// ReadOnlyTemplateVersion is the version of every built-in template (see
// [builder.BuiltinTemplates]), and of every template and collection of a
// template file of the server (see [Service.ServerCollections]). These are
// read only, so their version never changes while the server runs.
const ReadOnlyTemplateVersion = 1

// maxShownIDBytes bounds an identifier a refusal repeats.
const maxShownIDBytes = 80

// TemplateLibrary is the shared device templates of the server, stored as one
// record so a change of templates and collections is one write. It holds only
// the templates users added. The built-in templates and those of the template
// files are read only and never in it.
type TemplateLibrary struct {
	// Templates and Collections are in the order they were added.
	Templates   []LibraryTemplate    `json:"templates"`
	Collections []TemplateCollection `json:"collections"`
	Updated     time.Time            `json:"updated"`
	UpdatedBy   string               `json:"updatedBy"`
	// Revision is the record revision, 0 for a library that has no record
	// yet.
	Revision int64 `json:"-"`
}

// LibraryTemplate is one template of the library. Its ID comes from the ID
// source of the service. It never has a source (see [builder.Template]): only
// a copy in a document names where it came from.
type LibraryTemplate struct {
	builder.Template

	// Version starts at 1 and increases by one with every change of the name,
	// the description or the device. A document's copy of the template names
	// the version it copied (see [builder.TemplateSource]).
	Version int64     `json:"version"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// CreatedBy is the user who added the template, and UpdatedBy the user
	// who last changed its content.
	CreatedBy string `json:"createdBy"`
	UpdatedBy string `json:"updatedBy"`
}

// TemplateCollection is a named group of templates of the library. A template
// may be in several collections. Deleting a collection keeps its templates.
type TemplateCollection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// TemplateIDs names templates of this library, in the order given.
	TemplateIDs []string `json:"templateIds"`
	// Version starts at 1 and increases by one with every change of the name,
	// the description or the templates.
	Version int64     `json:"version"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// CreatedBy is the user who added the collection, and UpdatedBy the user
	// who last changed its content.
	CreatedBy string `json:"createdBy"`
	UpdatedBy string `json:"updatedBy"`
}

// CollectionContent is what a collection is made of or replaced with.
type CollectionContent struct {
	Name        string
	Description string
	TemplateIDs []string
}

// LibraryError says why a change of the template library was refused, in words
// for the person who asked for it. It unwraps to [ErrTooLarge] when the change
// would go past one of the limits of the library. Otherwise it unwraps to
// [ErrInvalid].
type LibraryError struct {
	Reason string
	// Limit is set when the library would hold more than it may.
	Limit bool
}

func (e *LibraryError) Error() string {
	return fmt.Sprintf("%v: %s: %s", e.Unwrap(), kindLibrary, e.Reason)
}

// Unwrap allows [errors.Is](err, ErrInvalid), or [errors.Is](err,
// ErrTooLarge) for a limit, to succeed.
func (e *LibraryError) Unwrap() error {
	if e.Limit {
		return ErrTooLarge
	}

	return ErrInvalid
}

func newLibraryErrorf(format string, args ...any) *LibraryError {
	return &LibraryError{Reason: fmt.Sprintf(format, args...), Limit: false}
}

func newLibraryLimitErrorf(format string, args ...any) *LibraryError {
	return &LibraryError{Reason: fmt.Sprintf(format, args...), Limit: true}
}

// ETag returns the entity tag of the template's content, `"<version>"`.
func (t *LibraryTemplate) ETag() string {
	return RevisionETag(t.Version)
}

// ETag returns the entity tag of the collection's content, `"<version>"`.
func (c *TemplateCollection) ETag() string {
	return RevisionETag(c.Version)
}

// Template returns the template of the library with the given ID, or nil.
func (l *TemplateLibrary) Template(id string) *LibraryTemplate {
	for i := range l.Templates {
		if l.Templates[i].ID == id {
			return &l.Templates[i]
		}
	}

	return nil
}

// Collection returns the collection of the library with the given ID, or
// nil.
func (l *TemplateLibrary) Collection(id string) *TemplateCollection {
	for i := range l.Collections {
		if l.Collections[i].ID == id {
			return &l.Collections[i]
		}
	}

	return nil
}

// AddTemplates adds templates under new IDs from newID, without a source, and
// returns the IDs in order. A refusal is a [LibraryError] and leaves the
// library as it was.
func (l *TemplateLibrary) AddTemplates(templates []builder.Template, newID IDSource) ([]string, error) {
	if len(l.Templates)+len(templates) > MaxLibraryTemplates {
		return nil, newLibraryLimitErrorf("a library holds at most %d templates", MaxLibraryTemplates)
	}

	added := make([]LibraryTemplate, 0, len(templates))
	ids := make([]string, 0, len(templates))

	for i := range templates {
		template := templates[i]

		if err := checkTemplate(&template, fmt.Sprintf("templates[%d]", i)); err != nil {
			return nil, err
		}

		id, err := newID()
		if err != nil {
			return nil, err
		}

		if !ValidID(id) || l.Template(id) != nil || slices.Contains(ids, id) {
			return nil, fmt.Errorf("adding a template: the new identifier %q is not usable", id)
		}

		template.ID = id
		template.Source = nil

		ids = append(ids, id)
		added = append(added, LibraryTemplate{
			Template:  template,
			Version:   0,
			Created:   time.Time{},
			Updated:   time.Time{},
			CreatedBy: "",
			UpdatedBy: "",
		})
	}

	l.Templates = append(l.Templates, added...)

	return ids, nil
}

// ReplaceTemplate replaces the name, the description and the device of the
// template with the given ID. The template keeps its ID. The rules of
// [TemplateLibrary.AddTemplates] apply to the content. An ID the library does
// not hold gives an error that matches [ErrNotFound].
func (l *TemplateLibrary) ReplaceTemplate(id string, content builder.Template) error {
	template := l.Template(id)
	if template == nil {
		return newNotFoundError(kindTemplate, id)
	}

	if err := checkTemplate(&content, kindTemplate); err != nil {
		return err
	}

	template.Name = content.Name
	template.Description = content.Description
	template.Device = content.Device

	return nil
}

// Delete removes the templates and the collections with the given IDs, and
// returns how many of each it removed. It ignores an ID the library does not
// hold, so deleting twice is harmless. A deleted template is also removed from
// every collection that named it. A deleted collection leaves its templates in
// the library.
func (l *TemplateLibrary) Delete(templateIDs, collectionIDs []string) (int, int) {
	templates := len(l.Templates)
	collections := len(l.Collections)

	l.Templates = slices.DeleteFunc(l.Templates, func(template LibraryTemplate) bool {
		return slices.Contains(templateIDs, template.ID)
	})

	l.Collections = slices.DeleteFunc(l.Collections, func(collection TemplateCollection) bool {
		return slices.Contains(collectionIDs, collection.ID)
	})

	for i := range l.Collections {
		l.Collections[i].TemplateIDs = slices.DeleteFunc(l.Collections[i].TemplateIDs, func(id string) bool {
			return slices.Contains(templateIDs, id)
		})
	}

	return templates - len(l.Templates), collections - len(l.Collections)
}

// AddCollection adds a collection under a new ID from newID and returns the
// ID. Every template it names must be in this library, once. A refusal, also
// of a limit, is a [LibraryError].
func (l *TemplateLibrary) AddCollection(content CollectionContent, newID IDSource) (string, error) {
	if len(l.Collections) >= MaxLibraryCollections {
		return "", newLibraryLimitErrorf("a library holds at most %d collections", MaxLibraryCollections)
	}

	if err := l.checkCollection(content); err != nil {
		return "", err
	}

	id, err := newID()
	if err != nil {
		return "", err
	}

	if !ValidID(id) || l.Collection(id) != nil {
		return "", fmt.Errorf("adding a collection: the new identifier %q is not usable", id)
	}

	l.Collections = append(l.Collections, TemplateCollection{
		ID:          id,
		Name:        content.Name,
		Description: content.Description,
		TemplateIDs: slices.Clone(content.TemplateIDs),
		Version:     0,
		Created:     time.Time{},
		Updated:     time.Time{},
		CreatedBy:   "",
		UpdatedBy:   "",
	})

	return id, nil
}

// ReplaceCollection replaces the name, the description and the templates of
// the collection with the given ID. The collection keeps its ID. The rules of
// [TemplateLibrary.AddCollection] apply to the content. An ID the library does
// not hold gives an error that matches [ErrNotFound].
func (l *TemplateLibrary) ReplaceCollection(id string, content CollectionContent) error {
	collection := l.Collection(id)
	if collection == nil {
		return newNotFoundError(kindCollection, id)
	}

	if err := l.checkCollection(content); err != nil {
		return err
	}

	collection.Name = content.Name
	collection.Description = content.Description
	collection.TemplateIDs = slices.Clone(content.TemplateIDs)

	return nil
}

// checkTemplate checks the content a template is made of or replaced with.
// Issues are located under path.
func checkTemplate(template *builder.Template, path string) error {
	if issues := template.Issues(path); len(issues) != 0 {
		return newLibraryErrorf("%s", issues[0].String())
	}

	return nil
}

// checkCollection checks the content a collection is made of or replaced
// with against the library's templates.
func (l *TemplateLibrary) checkCollection(content CollectionContent) error {
	if strings.TrimSpace(content.Name) == "" {
		return newLibraryErrorf("a collection needs a name")
	}

	if reason := textProblem(content.Name, builder.MaxTemplateNameBytes); reason != "" {
		return newLibraryErrorf("collection name %s", reason)
	}

	if reason := textProblem(content.Description, builder.MaxTemplateDescriptionBytes); reason != "" {
		return newLibraryErrorf("collection description %s", reason)
	}

	if len(content.TemplateIDs) > MaxCollectionTemplates {
		return newLibraryLimitErrorf("a collection holds at most %d templates", MaxCollectionTemplates)
	}

	for i, id := range content.TemplateIDs {
		switch {
		case l.Template(id) == nil:
			return newLibraryErrorf("collection names template %q, which this library does not hold", shownID(id))
		case slices.Contains(content.TemplateIDs[:i], id):
			return newLibraryErrorf("collection names template %q more than once", shownID(id))
		}
	}

	return nil
}

// textProblem says why value is not text of at most limit bytes, or returns
// "". The result follows the name of what the text is.
func textProblem(value string, limit int) string {
	var invalid *ValidationError

	if err := validateText("text", value, limit, false); errors.As(err, &invalid) {
		return invalid.Reason
	}

	return ""
}

// shownID shortens an identifier a caller sent for a refusal that repeats
// it, to whole characters.
func shownID(id string) string {
	if len(id) <= maxShownIDBytes {
		return id
	}

	cut := maxShownIDBytes
	for cut > 0 && !utf8.RuneStart(id[cut]) {
		cut--
	}

	return id[:cut] + "..."
}

// normalize makes the lists of the library empty lists instead of nil, so a
// library encodes the same however it was built.
func (l *TemplateLibrary) normalize() {
	if l.Templates == nil {
		l.Templates = []LibraryTemplate{}
	}

	if l.Collections == nil {
		l.Collections = []TemplateCollection{}
	}

	for i := range l.Collections {
		if l.Collections[i].TemplateIDs == nil {
			l.Collections[i].TemplateIDs = []string{}
		}
	}
}

// stamp sets the version, times and users of every new or changed template and
// collection, so an entity tag never names other content.
func (l *TemplateLibrary) stamp(before *TemplateLibrary, now time.Time, actor string) {
	for i := range l.Templates {
		template := &l.Templates[i]
		previous := before.Template(template.ID)

		if previous == nil {
			template.Version, template.Created, template.Updated = 1, now, now
			template.CreatedBy, template.UpdatedBy = actor, actor

			continue
		}

		template.Version, template.Created, template.Updated = previous.Version, previous.Created, previous.Updated
		template.CreatedBy, template.UpdatedBy = previous.CreatedBy, previous.UpdatedBy

		if !sameTemplate(&template.Template, &previous.Template) {
			template.Version++
			template.Updated, template.UpdatedBy = now, actor
		}
	}

	for i := range l.Collections {
		collection := &l.Collections[i]
		previous := before.Collection(collection.ID)

		if previous == nil {
			collection.Version, collection.Created, collection.Updated = 1, now, now
			collection.CreatedBy, collection.UpdatedBy = actor, actor

			continue
		}

		collection.Version, collection.Created, collection.Updated = previous.Version, previous.Created, previous.Updated
		collection.CreatedBy, collection.UpdatedBy = previous.CreatedBy, previous.UpdatedBy

		if collection.Name != previous.Name || collection.Description != previous.Description ||
			!slices.Equal(collection.TemplateIDs, previous.TemplateIDs) {
			collection.Version++
			collection.Updated, collection.UpdatedBy = now, actor
		}
	}
}

// sameTemplate reports whether two templates have the same content. It
// compares devices as they encode, so a spec read from a record equals the
// same spec built in memory.
func sameTemplate(a, b *builder.Template) bool {
	if a.Name != b.Name || a.Description != b.Description {
		return false
	}

	first, firstErr := json.Marshal(a.Device)
	second, secondErr := json.Marshal(b.Device)

	return firstErr == nil && secondErr == nil && bytes.Equal(first, second)
}

// emptyLibrary returns the library of a server that has no library record.
func emptyLibrary() *TemplateLibrary {
	return &TemplateLibrary{
		Templates:   []LibraryTemplate{},
		Collections: []TemplateCollection{},
		Updated:     time.Time{},
		UpdatedBy:   "",
		Revision:    0,
	}
}

// NewID returns a new identifier from the service's ID source, for the
// templates and collections a change adds to a library (see
// [TemplateLibrary.AddTemplates]).
func (s *Service) NewID() (string, error) {
	return s.newID()
}

// GetLibrary returns the template library of the server. A server with no
// library record has an empty library. A read writes nothing.
//
// A record that does not decode strictly or does not validate gives an error
// that matches [ErrCorrupt]. A library is never read leniently.
func (s *Service) GetLibrary(ctx context.Context) (*TemplateLibrary, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("getting the template library: %w", err)
	}

	library, err := s.storedLibrary()
	if errors.Is(err, ErrNotFound) {
		return emptyLibrary(), nil
	}

	return library, err
}

// storedLibrary reads and validates the library record.
func (s *Service) storedLibrary() (*TemplateLibrary, error) {
	record, err := s.store.GetRecord(NamespaceTemplates, LibraryKey)
	if err != nil {
		return nil, storeError(kindLibrary, LibraryKey, store.AnyRevision, err)
	}

	return decodeLibrary(record)
}

// UpdateLibrary runs change on the stored template library, stamps and
// validates the result, and writes it against the revision it read. It runs
// change again when another write was first, so change must depend only on
// the library it gets. Past [maxLibraryAttempts] it returns [ErrBusy]. An
// unchanged library writes nothing.
func (s *Service) UpdateLibrary(
	ctx context.Context,
	actor string,
	change func(*TemplateLibrary) error,
) (*TemplateLibrary, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("updating the template library: %w", err)
	}

	if err := validateText("actor", actor, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	for range maxLibraryAttempts {
		before, library, err := s.libraryForUpdate()
		if err != nil {
			return nil, err
		}

		if err := change(library); err != nil {
			return nil, err
		}

		value, changed, err := s.settleLibrary(before, library, actor)
		if err != nil {
			return nil, err
		}

		if !changed {
			return before, nil
		}

		revision, err := s.writeLibrary(value, before.Revision)

		switch {
		case err == nil:
			library.Revision = revision

			return library, nil
		case errors.Is(err, ErrConflict), errors.Is(err, ErrNotFound):
			continue
		}

		return nil, err
	}

	return nil, fmt.Errorf("updating the template library: %w", ErrBusy)
}

// libraryForUpdate reads the library two times: as it is, and as a separate
// copy for a change to work on.
func (s *Service) libraryForUpdate() (*TemplateLibrary, *TemplateLibrary, error) {
	record, err := s.store.GetRecord(NamespaceTemplates, LibraryKey)
	if errors.Is(err, store.ErrRecordNotExist) {
		return emptyLibrary(), emptyLibrary(), nil
	}

	if err != nil {
		return nil, nil, storeError(kindLibrary, LibraryKey, store.AnyRevision, err)
	}

	before, err := decodeLibrary(record)
	if err != nil {
		return nil, nil, err
	}

	library, err := decodeLibrary(record)
	if err != nil {
		return nil, nil, err
	}

	return before, library, nil
}

// settleLibrary finishes a changed library and returns the record value to
// write. The second result is false when the change left the library as it
// was before, and nothing is to be written.
func (s *Service) settleLibrary(before, library *TemplateLibrary, actor string) ([]byte, bool, error) {
	now := s.clock().UTC()

	library.normalize()
	library.stamp(before, now, actor)

	if problem := libraryProblem(library); problem != "" {
		return nil, false, newLibraryErrorf("%s", problem)
	}

	// Compare without who changed it last and when. A change that changes
	// nothing must not touch these fields.
	library.Updated, library.UpdatedBy = before.Updated, before.UpdatedBy

	was, err := encodeLibrary(before)
	if err != nil {
		return nil, false, err
	}

	value, err := encodeLibrary(library)
	if err != nil {
		return nil, false, err
	}

	if bytes.Equal(value, was) {
		return nil, false, nil
	}

	library.Updated, library.UpdatedBy = now, actor

	value, err = encodeLibrary(library)
	if err != nil {
		return nil, false, err
	}

	if len(value) > MaxMetadataBytes {
		return nil, false, newLibraryLimitErrorf(
			"a library takes at most %d bytes, and this one would take %d", MaxMetadataBytes, len(value),
		)
	}

	return value, true, nil
}

// encodeLibrary returns the record value of a library.
func encodeLibrary(library *TemplateLibrary) ([]byte, error) {
	value, err := json.Marshal(library)
	if err != nil {
		return nil, fmt.Errorf("encoding the template library: %w", err)
	}

	return value, nil
}

// writeLibrary writes a library record against the revision that was read (0
// for a library with no record yet), and returns the new revision.
func (s *Service) writeLibrary(value []byte, revision int64) (int64, error) {
	key := LibraryKey

	var (
		record store.Record
		err    error
	)

	if revision == 0 {
		record, err = s.store.CreateRecord(NamespaceTemplates, key, value)
	} else {
		record, err = s.store.UpdateRecord(NamespaceTemplates, key, value, revision)
	}

	if err == nil {
		return record.Revision, nil
	}

	return 0, storeError(kindLibrary, key, revision, err)
}

// decodeLibrary strictly decodes and fully validates a library record.
func decodeLibrary(record store.Record) (*TemplateLibrary, error) {
	var library TemplateLibrary

	if err := decodeMetadata(kindLibrary, record.Value, &library); err != nil {
		return nil, newCorruptError(kindLibrary, record.Key, err.Error())
	}

	library.normalize()

	if problem := libraryProblem(&library); problem != "" {
		return nil, newCorruptError(kindLibrary, record.Key, problem)
	}

	library.Revision = record.Revision

	return &library, nil
}

// libraryProblem says why a library is not one this package stores, or returns
// "". It checks the limits, unique IDs, valid templates without a source, and
// collections that name only templates of the library.
func libraryProblem(library *TemplateLibrary) string {
	switch {
	case validateText("updatedBy", library.UpdatedBy, MaxOwnerLength, false) != nil:
		return "the library has an unusable actor"
	case len(library.Templates) > MaxLibraryTemplates:
		return fmt.Sprintf("the library holds %d templates, more than %d", len(library.Templates), MaxLibraryTemplates)
	case len(library.Collections) > MaxLibraryCollections:
		return fmt.Sprintf(
			"the library holds %d collections, more than %d", len(library.Collections), MaxLibraryCollections,
		)
	}

	if problem := libraryTemplatesProblem(library); problem != "" {
		return problem
	}

	return libraryCollectionsProblem(library)
}

// libraryTemplatesProblem is the part of [libraryProblem] that checks the
// templates.
func libraryTemplatesProblem(library *TemplateLibrary) string {
	seen := make(map[string]bool, len(library.Templates))

	for i := range library.Templates {
		template := &library.Templates[i]

		switch {
		case !ValidID(template.ID):
			return fmt.Sprintf("template %d has an invalid identifier", i)
		case seen[template.ID]:
			return fmt.Sprintf("template %d repeats an identifier", i)
		case template.Version < 1:
			return fmt.Sprintf("template %d has version %d", i, template.Version)
		case len(template.Issues(kindTemplate)) != 0:
			return fmt.Sprintf("template %d is not a valid template", i)
		case template.Source != nil:
			return fmt.Sprintf("template %d names a source", i)
		case validateText("createdBy", template.CreatedBy, MaxOwnerLength, false) != nil ||
			validateText("updatedBy", template.UpdatedBy, MaxOwnerLength, false) != nil:
			return fmt.Sprintf("template %d names an unusable user", i)
		}

		seen[template.ID] = true
	}

	return ""
}

// libraryCollectionsProblem is the part of [libraryProblem] that checks the
// collections.
func libraryCollectionsProblem(library *TemplateLibrary) string {
	seen := make(map[string]bool, len(library.Collections))

	for i := range library.Collections {
		collection := &library.Collections[i]

		switch {
		case !ValidID(collection.ID):
			return fmt.Sprintf("collection %d has an invalid identifier", i)
		case seen[collection.ID]:
			return fmt.Sprintf("collection %d repeats an identifier", i)
		case collection.Version < 1:
			return fmt.Sprintf("collection %d has version %d", i, collection.Version)
		case strings.TrimSpace(collection.Name) == "" ||
			textProblem(collection.Name, builder.MaxTemplateNameBytes) != "":
			return fmt.Sprintf("collection %d has no usable name", i)
		case textProblem(collection.Description, builder.MaxTemplateDescriptionBytes) != "":
			return fmt.Sprintf("collection %d has an unusable description", i)
		case len(collection.TemplateIDs) > MaxCollectionTemplates:
			return fmt.Sprintf(
				"collection %d names %d templates, more than %d", i, len(collection.TemplateIDs), MaxCollectionTemplates,
			)
		case validateText("createdBy", collection.CreatedBy, MaxOwnerLength, false) != nil ||
			validateText("updatedBy", collection.UpdatedBy, MaxOwnerLength, false) != nil:
			return fmt.Sprintf("collection %d names an unusable user", i)
		}

		seen[collection.ID] = true

		for j, id := range collection.TemplateIDs {
			if library.Template(id) == nil || slices.Contains(collection.TemplateIDs[:j], id) {
				return fmt.Sprintf("collection %d names a template the library does not hold, or one twice", i)
			}
		}
	}

	return ""
}
