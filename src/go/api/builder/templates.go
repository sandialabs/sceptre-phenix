package builder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// library and applies the change again after another write landed first.
const maxLibraryAttempts = 5

// Kinds named in the typed errors of a template library.
const (
	kindLibrary    = "template library"
	kindTemplate   = "template"
	kindCollection = "collection"
)

// libraryKeyPrefix starts the record key of every template library.
const libraryKeyPrefix = "lib/"

// maxShownIDBytes bounds an identifier a refusal repeats. An icon ID, the
// longest one in use, is 71 bytes and is shown whole.
const maxShownIDBytes = 80

// TemplateLibrary is the device templates one user keeps, and the value of
// that user's library record. It is one record, so a change of a template,
// of a collection and of the icons they use is always one write.
//
// A user who never changed the library has no record: [Service.GetLibrary]
// then returns the built-in templates, and the first change stores them with
// the record. Nothing adds a built-in template to a record afterwards, so
// one that was deleted stays deleted.
type TemplateLibrary struct {
	// Owner is the user whose library this is. The record key holds the
	// owner's scope, so a record cannot claim to be another user's library.
	Owner string `json:"owner"`
	// Templates and Collections are in the order they were added.
	Templates   []LibraryTemplate    `json:"templates"`
	Collections []TemplateCollection `json:"collections"`
	// Icons holds the custom icons the templates name, by icon ID, as a
	// document carries them. A stored library holds no other icon.
	Icons     map[string]builder.Icon `json:"icons,omitempty"`
	Updated   time.Time               `json:"updated"`
	UpdatedBy string                  `json:"updatedBy"`
	// Revision is the record revision, 0 for a library that has no record
	// yet.
	Revision int64 `json:"-"`
}

// LibraryTemplate is one template of a library. Its ID is unique in that
// library only: the built-in templates keep their short names in every
// library, and any other template gets an ID from the service's ID source.
type LibraryTemplate struct {
	builder.Template

	// Version starts at 1 and grows by one with every change of the name,
	// the description or the device. Who the template is shared with and
	// whether it is published are not part of it.
	Version int64 `json:"version"`
	// Created and Updated are zero for a built-in template that was never
	// changed.
	Created time.Time `json:"created,omitzero"`
	Updated time.Time `json:"updated,omitzero"`
	// Shares are the users the template is shared with, sorted by user.
	Shares []TemplateShare `json:"shares,omitempty"`
	// Public is set while the template is published to every user.
	Public *TemplatePublished `json:"public,omitempty"`
}

// TemplateCollection is a named group of templates of the same library. A
// template may be in several collections, and deleting a collection leaves
// its templates.
type TemplateCollection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// TemplateIDs names templates of this library, in the order given.
	TemplateIDs []string `json:"templateIds"`
	// Version starts at 1 and grows by one with every change of the name,
	// the description or the templates.
	Version int64              `json:"version"`
	Created time.Time          `json:"created"`
	Updated time.Time          `json:"updated"`
	Shares  []TemplateShare    `json:"shares,omitempty"`
	Public  *TemplatePublished `json:"public,omitempty"`
}

// TemplateShare shares a template or a collection, read only, with one user.
type TemplateShare struct {
	User string `json:"user"`
	// UserCreated is metadata.created of the recipient's User config when
	// granted. The share applies only while it still matches, so it never
	// passes to a new account created under the same name.
	UserCreated string    `json:"userCreated"`
	GrantedAt   time.Time `json:"grantedAt"`
}

// TemplatePublished records that a template or a collection is published to
// every user, since when and by whom.
type TemplatePublished struct {
	At time.Time `json:"at"`
	By string    `json:"by"`
}

// CollectionContent is what a collection is made of or replaced with.
type CollectionContent struct {
	Name        string
	Description string
	TemplateIDs []string
}

// LibraryError says why a change of a template library was refused, in
// words for the person who asked for it. It unwraps to [ErrTooLarge] when
// the change would pass one of the library's limits, and otherwise to
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

// LibraryKey returns the record key of a user's template library. The user
// name enters it only as [OwnerScope].
func LibraryKey(user string) string {
	return libraryKeyPrefix + OwnerScope(user)
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

// AddTemplates adds templates to the library, each under a new ID from
// newID, whatever ID it came with, and returns those IDs in order. A
// template that names a custom icon needs that icon in the library already
// or in icons, the icons the request carries; the ones it needs are added
// with it.
//
// A template that is not valid (see [builder.Template.Issues]) and a custom
// icon nothing carries are refused with a [LibraryError] that names the
// template by its index, and a library that would hold more than
// [MaxLibraryTemplates] with one that is a limit. A refusal leaves the
// library as it was.
func (l *TemplateLibrary) AddTemplates(
	templates []builder.Template,
	icons map[string]builder.Icon,
	newID IDSource,
) ([]string, error) {
	if len(l.Templates)+len(templates) > MaxLibraryTemplates {
		return nil, newLibraryLimitErrorf("a library holds at most %d templates", MaxLibraryTemplates)
	}

	added := make([]LibraryTemplate, 0, len(templates))
	ids := make([]string, 0, len(templates))

	for i := range templates {
		template := templates[i]

		if err := l.checkTemplate(&template, fmt.Sprintf("templates[%d]", i), i, icons); err != nil {
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

		ids = append(ids, id)
		added = append(added, LibraryTemplate{
			Template: template,
			Version:  0,
			Created:  time.Time{},
			Updated:  time.Time{},
			Shares:   nil,
			Public:   nil,
		})
	}

	for i := range added {
		l.keepIcon(added[i].Device.Icon, icons)
	}

	l.Templates = append(l.Templates, added...)

	return ids, nil
}

// ReplaceTemplate replaces the name, the description and the device of the
// template with the given ID, which keeps its ID, who it is shared with and
// whether it is published. The rules of [TemplateLibrary.AddTemplates] hold
// for the content and for icons. An ID the library does not hold is an
// error matching [ErrNotFound].
func (l *TemplateLibrary) ReplaceTemplate(id string, content builder.Template, icons map[string]builder.Icon) error {
	template := l.Template(id)
	if template == nil {
		return newNotFoundError(kindTemplate, id)
	}

	if err := l.checkTemplate(&content, kindTemplate, 0, icons); err != nil {
		return err
	}

	l.keepIcon(content.Device.Icon, icons)

	template.Name = content.Name
	template.Description = content.Description
	template.Device = content.Device

	return nil
}

// Delete removes the templates and the collections with the given IDs, and
// returns how many of each it removed. An ID the library does not hold is
// ignored, so deleting twice is harmless. A deleted template also leaves
// every collection that named it; a deleted collection leaves its templates
// in the library.
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

// AddCollection adds a collection under a new ID from newID, and returns
// that ID. Every template it names must be one of this library, named once.
// A name or a description a collection may not have, and a template the
// library does not hold, are refused with a [LibraryError], and more than
// [MaxLibraryCollections] collections or [MaxCollectionTemplates] templates
// in one with one that is a limit.
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
		Shares:      nil,
		Public:      nil,
	})

	return id, nil
}

// ReplaceCollection replaces the name, the description and the templates of
// the collection with the given ID, which keeps its ID, who it is shared
// with and whether it is published. The rules of
// [TemplateLibrary.AddCollection] hold for the content. An ID the library
// does not hold is an error matching [ErrNotFound].
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
// Issues are located under path; a custom icon nothing carries names the
// template by index, its place in the request.
func (l *TemplateLibrary) checkTemplate(
	template *builder.Template,
	path string,
	index int,
	icons map[string]builder.Icon,
) error {
	if issues := template.Issues(path); len(issues) != 0 {
		return newLibraryErrorf("%s", issues[0].String())
	}

	icon := template.Device.Icon
	if icon == "" {
		return nil
	}

	if _, held := l.Icons[icon]; held {
		return nil
	}

	if _, carried := icons[icon]; carried {
		return nil
	}

	return newLibraryErrorf(
		"template %d names custom icon %q, which the request does not carry", index, shownID(icon),
	)
}

// keepIcon adds the custom icon a template names to the library, from the
// icons a request carries, unless the library holds it already.
func (l *TemplateLibrary) keepIcon(id string, icons map[string]builder.Icon) {
	if id == "" {
		return
	}

	if _, held := l.Icons[id]; held {
		return
	}

	if l.Icons == nil {
		l.Icons = map[string]builder.Icon{}
	}

	l.Icons[id] = icons[id]
}

// checkCollection checks the content a collection is made of or replaced
// with against the library's templates.
func (l *TemplateLibrary) checkCollection(content CollectionContent) error {
	if strings.TrimSpace(content.Name) == "" {
		return newLibraryErrorf("collection name is required")
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

// textProblem says why value is no text of at most limit bytes, or returns
// "". It reads after the name of what the text is.
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

// normalize makes the lists of the library lists rather than nothing, so a
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

	if len(l.Icons) == 0 {
		l.Icons = nil
	}
}

// dropUnusedIcons removes the custom icons no template of the library names.
func (l *TemplateLibrary) dropUnusedIcons() {
	used := make(map[string]bool, len(l.Icons))

	for i := range l.Templates {
		if icon := l.Templates[i].Device.Icon; icon != "" {
			used[icon] = true
		}
	}

	for id := range l.Icons {
		if !used[id] {
			delete(l.Icons, id)
		}
	}
}

// stamp sets the version and the times of every template and collection
// from what the library held before a change: a new one starts at version 1,
// one whose content changed gains a version and the time of the change, and
// any other keeps what it had. So a change can never leave an entity tag
// that names other content.
func (l *TemplateLibrary) stamp(before *TemplateLibrary, now time.Time) {
	for i := range l.Templates {
		template := &l.Templates[i]
		previous := before.Template(template.ID)

		if previous == nil {
			template.Version, template.Created, template.Updated = 1, now, now

			continue
		}

		template.Version, template.Created, template.Updated = previous.Version, previous.Created, previous.Updated

		if !sameTemplate(&template.Template, &previous.Template) {
			template.Version++
			template.Updated = now
		}
	}

	for i := range l.Collections {
		collection := &l.Collections[i]
		previous := before.Collection(collection.ID)

		if previous == nil {
			collection.Version, collection.Created, collection.Updated = 1, now, now

			continue
		}

		collection.Version, collection.Created, collection.Updated = previous.Version, previous.Created, previous.Updated

		if collection.Name != previous.Name || collection.Description != previous.Description ||
			!slices.Equal(collection.TemplateIDs, previous.TemplateIDs) {
			collection.Version++
			collection.Updated = now
		}
	}
}

// sameTemplate reports whether two templates have the same content. Devices
// are compared as they encode, so a spec read from a record equals the same
// spec built in memory.
func sameTemplate(a, b *builder.Template) bool {
	if a.Name != b.Name || a.Description != b.Description {
		return false
	}

	first, firstErr := json.Marshal(a.Device)
	second, secondErr := json.Marshal(b.Device)

	return firstErr == nil && secondErr == nil && bytes.Equal(first, second)
}

// builtinLibrary returns the library of a user who has no record: the
// built-in templates, at version 1, with no times.
func builtinLibrary(owner string) *TemplateLibrary {
	builtins := builder.BuiltinTemplates()
	templates := make([]LibraryTemplate, 0, len(builtins))

	for _, template := range builtins {
		templates = append(templates, LibraryTemplate{
			Template: template,
			Version:  1,
			Created:  time.Time{},
			Updated:  time.Time{},
			Shares:   nil,
			Public:   nil,
		})
	}

	return &TemplateLibrary{
		Owner:       owner,
		Templates:   templates,
		Collections: []TemplateCollection{},
		Icons:       nil,
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

// GetLibrary returns the template library of owner. A user with no library
// record gets the built-in templates; nothing is written by a read.
//
// A record that does not decode strictly or does not validate is an error
// matching [ErrCorrupt]: a library is never read leniently.
func (s *Service) GetLibrary(ctx context.Context, owner string) (*TemplateLibrary, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("getting a template library: %w", err)
	}

	if err := validateText("owner", owner, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	library, err := s.storedLibrary(LibraryKey(owner))
	if errors.Is(err, ErrNotFound) {
		return builtinLibrary(owner), nil
	}

	return library, err
}

// GetLibraryByKey returns the stored template library whose owner has the
// given scope (see [OwnerScope]), for a caller that knows a library by its
// record key only. A scope with no library record, and anything that is not
// a scope, is an error matching [ErrNotFound].
func (s *Service) GetLibraryByKey(ctx context.Context, ownerScope string) (*TemplateLibrary, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("getting a template library: %w", err)
	}

	if !isOwnerScope(ownerScope) {
		return nil, newNotFoundError(kindLibrary, ownerScope)
	}

	return s.storedLibrary(libraryKeyPrefix + ownerScope)
}

// isOwnerScope reports whether value has the form [OwnerScope] returns: 64
// lowercase hex digits.
func isOwnerScope(value string) bool {
	decoded, err := hex.DecodeString(value)

	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

// storedLibrary reads and validates the library record with the given key.
func (s *Service) storedLibrary(key string) (*TemplateLibrary, error) {
	record, err := s.store.GetRecord(NamespaceTemplates, key)
	if err != nil {
		return nil, storeError(kindLibrary, key, store.AnyRevision, err)
	}

	return decodeLibrary(record)
}

// UpdateLibrary applies change to the template library of owner and stores
// the result, as one record write. It returns the library as stored.
//
// The library is read, change runs on it, and the result is written against
// the revision that was read: a first change creates the record, from the
// built-in templates. When another write landed first, the library is read
// and change runs again, up to [maxLibraryAttempts] times before [ErrBusy]
// is returned. So change must depend only on the library it is given, and
// may run more than once. An error it returns is returned as it is.
//
// After change ran, the icons no template names are dropped, every template
// and collection gets its version and times (see [TemplateLibrary.stamp]),
// and the library is validated. One that would hold more than
// [MaxLibraryTemplateIcons] icons or take more than [MaxMetadataBytes] is
// refused with a [LibraryError] that is a limit, and an icon that is not one
// a document accepts with one that is not. When change left the library as
// it was, nothing is written and the library read is returned.
func (s *Service) UpdateLibrary(
	ctx context.Context,
	owner, actor string,
	change func(*TemplateLibrary) error,
) (*TemplateLibrary, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("updating a template library: %w", err)
	}

	if err := validateText("owner", owner, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	if err := validateText("actor", actor, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	key := LibraryKey(owner)

	for range maxLibraryAttempts {
		before, library, err := s.libraryForUpdate(owner, key)
		if err != nil {
			return nil, err
		}

		if err := change(library); err != nil {
			return nil, err
		}

		value, changed, err := s.settleLibrary(key, before, library, actor)
		if err != nil {
			return nil, err
		}

		if !changed {
			return before, nil
		}

		revision, err := s.writeLibrary(key, value, before.Revision)

		switch {
		case err == nil:
			library.Revision = revision

			return library, nil
		case errors.Is(err, ErrConflict), errors.Is(err, ErrNotFound):
			continue
		}

		return nil, err
	}

	return nil, fmt.Errorf("updating the template library of %s: %w", owner, ErrBusy)
}

// libraryForUpdate reads the library of owner twice over: as it is, and as a
// copy of its own for a change to work on.
func (s *Service) libraryForUpdate(owner, key string) (*TemplateLibrary, *TemplateLibrary, error) {
	record, err := s.store.GetRecord(NamespaceTemplates, key)
	if errors.Is(err, store.ErrRecordNotExist) {
		return builtinLibrary(owner), builtinLibrary(owner), nil
	}

	if err != nil {
		return nil, nil, storeError(kindLibrary, key, store.AnyRevision, err)
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
func (s *Service) settleLibrary(key string, before, library *TemplateLibrary, actor string) ([]byte, bool, error) {
	now := s.clock().UTC()

	library.dropUnusedIcons()
	library.normalize()

	if len(library.Icons) > MaxLibraryTemplateIcons {
		return nil, false, newLibraryLimitErrorf("a library holds at most %d custom icons", MaxLibraryTemplateIcons)
	}

	if issues := builder.ValidateIcons(library.Icons, "icons"); len(issues) != 0 {
		return nil, false, newLibraryErrorf("%s", issues[0].String())
	}

	library.stamp(before, now)

	if problem := libraryProblem(key, library); problem != "" {
		return nil, false, newLibraryErrorf("%s", problem)
	}

	// Compared without who changed it last and when, which a change that
	// changes nothing must not touch.
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
		return nil, fmt.Errorf("encoding the template library of %s: %w", library.Owner, err)
	}

	return value, nil
}

// writeLibrary writes a library record against the revision that was read,
// 0 for a library with no record yet, and returns the new revision.
//
// A write the store neither applied nor refused for certain (an etcd request
// can time out after its proposal was applied) is settled by reading the
// record back: when it holds exactly the value of this write, the write was
// applied.
func (s *Service) writeLibrary(key string, value []byte, revision int64) (int64, error) {
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

	err = storeError(kindLibrary, key, revision, err)

	if !writeRejected(err) {
		if stored, getErr := s.store.GetRecord(NamespaceTemplates, key); getErr == nil && bytes.Equal(stored.Value, value) {
			return stored.Revision, nil
		}
	}

	return 0, err
}

// decodeLibrary strictly decodes and fully validates a library record.
func decodeLibrary(record store.Record) (*TemplateLibrary, error) {
	var library TemplateLibrary

	if err := decodeMetadata(kindLibrary, record.Value, &library); err != nil {
		return nil, newCorruptError(kindLibrary, record.Key, err.Error())
	}

	library.normalize()

	if problem := libraryProblem(record.Key, &library); problem != "" {
		return nil, newCorruptError(kindLibrary, record.Key, problem)
	}

	library.Revision = record.Revision

	return &library, nil
}

// libraryProblem says why a library is not one this package stores under
// key, or returns "". It is checked on every read and before every write,
// and it never repeats what the library holds.
//
// The owner must be the user the key stands for, so a record cannot claim to
// be another user's library. The library holds at most
// [MaxLibraryTemplates] templates and [MaxLibraryCollections] collections,
// each with an ID of its own; every template is valid (see
// [builder.Template.Issues]) and names no custom icon but one the library
// carries; every collection names only templates of the library; the icons
// are ones a document accepts; and who an item is shared with or published
// by is well formed. Shares are never read leniently: a tampered list
// grants nobody anything.
func libraryProblem(key string, library *TemplateLibrary) string {
	switch {
	case validateText("owner", library.Owner, MaxOwnerLength, true) != nil:
		return "the library has no usable owner"
	case LibraryKey(library.Owner) != key:
		return "the library names another owner"
	case validateText("updatedBy", library.UpdatedBy, MaxOwnerLength, false) != nil:
		return "the library has an unusable actor"
	case len(library.Templates) > MaxLibraryTemplates:
		return fmt.Sprintf("the library holds %d templates, more than %d", len(library.Templates), MaxLibraryTemplates)
	case len(library.Collections) > MaxLibraryCollections:
		return fmt.Sprintf(
			"the library holds %d collections, more than %d", len(library.Collections), MaxLibraryCollections,
		)
	case len(library.Icons) > MaxLibraryTemplateIcons:
		return fmt.Sprintf("the library holds %d custom icons, more than %d", len(library.Icons), MaxLibraryTemplateIcons)
	case len(builder.ValidateIcons(library.Icons, "icons")) != 0:
		return "a custom icon is not one a document accepts"
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
		}

		seen[template.ID] = true

		if icon := template.Device.Icon; icon != "" {
			if _, held := library.Icons[icon]; !held {
				return fmt.Sprintf("template %d names a custom icon the library does not carry", i)
			}
		}

		if problem := librarySharingProblem(library.Owner, template.Shares, template.Public); problem != "" {
			return fmt.Sprintf("template %d %s", i, problem)
		}
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
		}

		seen[collection.ID] = true

		for j, id := range collection.TemplateIDs {
			if library.Template(id) == nil || slices.Contains(collection.TemplateIDs[:j], id) {
				return fmt.Sprintf("collection %d names a template the library does not hold, or one twice", i)
			}
		}

		if problem := librarySharingProblem(library.Owner, collection.Shares, collection.Public); problem != "" {
			return fmt.Sprintf("collection %d %s", i, problem)
		}
	}

	return ""
}

// librarySharingProblem says what is wrong with who a template or a
// collection of owner is shared with and published by, or returns "". It
// reads after the name of the item.
func librarySharingProblem(owner string, shares []TemplateShare, public *TemplatePublished) string {
	if len(shares) > MaxShares {
		return fmt.Sprintf("is shared with %d users, more than %d", len(shares), MaxShares)
	}

	for i := range shares {
		share := shares[i]

		switch {
		case ValidateShareUser(share.User) != nil:
			return fmt.Sprintf("has a share %d that names an invalid user", i)
		case share.User == owner:
			return fmt.Sprintf("has a share %d that names the owner", i)
		case i > 0 && share.User <= shares[i-1].User:
			return fmt.Sprintf("has a share %d that is out of order or repeats a user", i)
		case validateText("userCreated", share.UserCreated, maxUserCreatedLength, true) != nil:
			return fmt.Sprintf("has a share %d with no usable account binding", i)
		}
	}

	if public != nil && validateText("by", public.By, MaxOwnerLength, true) != nil {
		return "is published by no usable user"
	}

	return ""
}
