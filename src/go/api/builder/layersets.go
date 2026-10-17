package builder

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"phenix/store"
	"phenix/types/builder"
	"phenix/util/plog"
)

// Kinds named in the typed errors of the layer set library.
const (
	kindLayerSet        = "layer set"
	kindLayerSetDefault = "default layer set"
)

// The layout of the records of the layer set library (see
// [NamespaceLayerSets]). Each set has the record "set/" plus its ID. The
// default set of the server, when one was chosen, has the record "default".
const (
	layerSetKeyPrefix  = "set/"
	layerSetDefaultKey = "default"
)

// LibraryLayerSet is one layer set of the library of the server, which every
// user of the Builder shares: its content, its ID, and who made it and last
// changed it, and when. A built-in set (see [builder.BuiltinLayerSets]) has
// no record. It is always at version 1, with no times and no users, and
// cannot be changed or deleted.
type LibraryLayerSet struct {
	builder.LayerSet

	// ID is the ID of a built-in set, or an ID from the ID source of the
	// service.
	ID string `json:"id"`
	// Version starts at 1 and increases by one with every change of the
	// content.
	Version int64 `json:"version"`
	// Created and Updated are zero for a built-in set.
	Created time.Time `json:"created,omitzero"`
	Updated time.Time `json:"updated,omitzero"`
	// CreatedBy is the user who added the set, and UpdatedBy the user who
	// last changed its content. Both are empty for a built-in set.
	CreatedBy string `json:"createdBy,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	// Revision is the record revision, 0 for a built-in set.
	Revision int64 `json:"-"`
}

// layerSetDefault is the value of the record of the default layer set of
// the server.
type layerSetDefault struct {
	// LayerSet is the ID of the set.
	LayerSet string `json:"layerSet"`
}

// LayerSetStaleError is returned by a change of a layer set whose entity
// tag does not name the current content of the set. ETag is the current
// one. It unwraps to [ErrConflict].
type LayerSetStaleError struct {
	ID   string
	ETag string
}

func (e *LayerSetStaleError) Error() string {
	return fmt.Sprintf("%v: %s %s changed after it was last read", ErrConflict, kindLayerSet, e.ID)
}

// Unwrap allows [errors.Is](err, ErrConflict) to succeed.
func (e *LayerSetStaleError) Unwrap() error { return ErrConflict }

// LayerSetLimitError reports that the library holds [MaxLayerSets] sets,
// and refuses one more. It unwraps to [ErrTooLarge].
type LayerSetLimitError struct {
	Limit int
}

func (e *LayerSetLimitError) Error() string {
	return fmt.Sprintf("%v: the layer set library holds at most %d sets", ErrTooLarge, e.Limit)
}

// Unwrap allows [errors.Is](err, ErrTooLarge) to succeed.
func (e *LayerSetLimitError) Unwrap() error { return ErrTooLarge }

// ETag returns the entity tag of the set's content, `"<version>"`.
func (s *LibraryLayerSet) ETag() string {
	return RevisionETag(s.Version)
}

// Builtin reports whether the set is a built-in set, which no user may
// change or delete.
func (s *LibraryLayerSet) Builtin() bool {
	return builtinLayerSet(s.ID) != nil
}

// builtinLayerSet returns the built-in set with the given ID, at version 1,
// or nil.
func builtinLayerSet(id string) *LibraryLayerSet {
	for _, set := range builder.BuiltinLayerSets() {
		if set.ID == id {
			return &LibraryLayerSet{
				LayerSet: set.LayerSet, ID: set.ID, Version: 1, Created: time.Time{}, Updated: time.Time{},
				CreatedBy: "", UpdatedBy: "", Revision: 0,
			}
		}
	}

	return nil
}

// layerSetKey returns the record key of the stored set with the given ID.
func layerSetKey(id string) string {
	return layerSetKeyPrefix + id
}

// ListLayerSets returns every layer set of the library: the built-in sets
// first, in their order, then the stored sets by name, ignoring case, then
// by ID. A stored record that does not decode or validate is left out and
// logged. The second result counts those records.
func (s *Service) ListLayerSets(ctx context.Context) ([]LibraryLayerSet, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("listing layer sets: %w", err)
	}

	records, err := s.store.ListRecords(NamespaceLayerSets, layerSetKeyPrefix)
	if err != nil {
		return nil, 0, fmt.Errorf("listing layer sets: %w", err)
	}

	builtins := builder.BuiltinLayerSets()
	sets := make([]LibraryLayerSet, 0, len(builtins)+len(records))
	stored := make([]LibraryLayerSet, 0, len(records))
	damaged := 0

	for _, set := range builtins {
		sets = append(sets, *builtinLayerSet(set.ID))
	}

	for _, record := range records {
		set, err := decodeLayerSet(record)
		if err != nil {
			damaged++

			plog.Error(plog.TypeSystem, "skipping unreadable builder layer set", "key", record.Key, "err", err)

			continue
		}

		stored = append(stored, *set)
	}

	slices.SortFunc(stored, func(a, b LibraryLayerSet) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.ID, b.ID),
		)
	})

	return append(sets, stored...), damaged, nil
}

// GetLayerSet returns the layer set with the given ID: a built-in set, or a
// stored one. An ID that names no set, whatever its form, gives an error that
// matches [ErrNotFound]. A record that does not decode or validate gives an
// error that matches [ErrCorrupt].
func (s *Service) GetLayerSet(ctx context.Context, id string) (*LibraryLayerSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("reading a layer set: %w", err)
	}

	if set := builtinLayerSet(id); set != nil {
		return set, nil
	}

	if !ValidID(id) {
		return nil, newNotFoundError(kindLayerSet, shownID(id))
	}

	record, err := s.store.GetRecord(NamespaceLayerSets, layerSetKey(id))
	if err != nil {
		return nil, storeError(kindLayerSet, id, store.AnyRevision, err)
	}

	return decodeLayerSet(record)
}

// CreateLayerSet adds a layer set to the library under a new ID from the ID
// source of the service, at version 1, made and changed now by actor, and
// returns it as stored.
//
// It refuses a set that [builder.LayerSet.Validate] refuses with that
// error, which also matches [ErrInvalid]. It refuses a set past
// [MaxLayerSets] with a [LayerSetLimitError]. The limit is a plain bound,
// not a quota: two requests at the same moment can each pass the check.
func (s *Service) CreateLayerSet(ctx context.Context, actor string, content builder.LayerSet) (*LibraryLayerSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("adding a layer set: %w", err)
	}

	if err := validateText("actor", actor, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	if err := checkLayerSet(&content); err != nil {
		return nil, err
	}

	keys, err := s.store.ListRecordKeys(NamespaceLayerSets, layerSetKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("counting layer sets: %w", err)
	}

	if len(keys) >= MaxLayerSets {
		return nil, &LayerSetLimitError{Limit: MaxLayerSets}
	}

	id, err := s.newID()
	if err != nil {
		return nil, fmt.Errorf("adding a layer set: %w", err)
	}

	if !ValidID(id) || builtinLayerSet(id) != nil {
		return nil, fmt.Errorf("adding a layer set: the new identifier %q is not usable", id)
	}

	now := s.clock().UTC()
	set := &LibraryLayerSet{
		LayerSet: content, ID: id, Version: 1, Created: now, Updated: now,
		CreatedBy: actor, UpdatedBy: actor, Revision: 0,
	}

	revision, err := s.writeLayerSet(set, 0)
	if err != nil {
		return nil, err
	}

	set.Revision = revision

	return set, nil
}

// UpdateLayerSet replaces the content of the stored layer set with the
// given ID, when etag is its entity tag (see [LibraryLayerSet.ETag]), and
// returns it as stored. A change of the content raises the version by one
// and records actor and now. Content equal to what the set holds writes
// nothing and returns the set as it is.
//
// A built-in set gives an error that matches [ErrForbidden]. An ID that
// names no set gives an error that matches [ErrNotFound]. An entity tag of
// other content gives a [LayerSetStaleError]. Content that
// [builder.LayerSet.Validate] refuses gives that error. When another write
// was first, it reads the set again, up to [maxLibraryAttempts] times, and
// then returns [ErrBusy].
func (s *Service) UpdateLayerSet(
	ctx context.Context,
	actor, id, etag string,
	content builder.LayerSet,
) (*LibraryLayerSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("changing a layer set: %w", err)
	}

	if builtinLayerSet(id) != nil {
		return nil, fmt.Errorf("changing built-in %s %s: %w", kindLayerSet, id, ErrForbidden)
	}

	if err := validateText("actor", actor, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	if err := checkLayerSet(&content); err != nil {
		return nil, err
	}

	for range maxLibraryAttempts {
		current, err := s.GetLayerSet(ctx, id)
		if err != nil {
			return nil, err
		}

		if current.ETag() != etag {
			return nil, &LayerSetStaleError{ID: id, ETag: current.ETag()}
		}

		if sameLayerSet(&current.LayerSet, &content) {
			return current, nil
		}

		set := *current
		set.LayerSet = content
		set.Version++
		set.Updated, set.UpdatedBy = s.clock().UTC(), actor

		revision, err := s.writeLayerSet(&set, current.Revision)

		switch {
		case err == nil:
			set.Revision = revision

			return &set, nil
		case errors.Is(err, ErrConflict):
			continue
		}

		return nil, err
	}

	return nil, fmt.Errorf("changing %s %s: %w", kindLayerSet, id, ErrBusy)
}

// DeleteLayerSet deletes the stored layer set with the given ID. A built-in
// set gives an error that matches [ErrForbidden], and an ID that names no
// set one that matches [ErrNotFound]. A default choice that names the set
// stays. It names no set from then on (see [Service.DefaultLayerSet]).
func (s *Service) DeleteLayerSet(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deleting a layer set: %w", err)
	}

	if builtinLayerSet(id) != nil {
		return fmt.Errorf("deleting built-in %s %s: %w", kindLayerSet, id, ErrForbidden)
	}

	if !ValidID(id) {
		return newNotFoundError(kindLayerSet, shownID(id))
	}

	err := s.store.DeleteRecord(NamespaceLayerSets, layerSetKey(id), store.AnyRevision)

	return storeError(kindLayerSet, id, store.AnyRevision, err)
}

// DefaultLayerSet returns the ID of the layer set that new diagrams of every
// user start with, or "" for none. A choice that names a set that no longer
// exists, or a record that does not decode, is none.
func (s *Service) DefaultLayerSet(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("reading the default layer set: %w", err)
	}

	record, err := s.store.GetRecord(NamespaceLayerSets, layerSetDefaultKey)
	if errors.Is(err, store.ErrRecordNotExist) {
		return "", nil
	}

	if err != nil {
		return "", storeError(kindLayerSetDefault, layerSetDefaultKey, store.AnyRevision, err)
	}

	var choice layerSetDefault

	if err := decodeMetadata(kindLayerSetDefault, record.Value, &choice); err != nil {
		plog.Error(plog.TypeSystem, "ignoring unreadable builder default layer set", "err", err)

		return "", nil
	}

	if _, err := s.GetLayerSet(ctx, choice.LayerSet); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrCorrupt) {
			return "", nil
		}

		return "", err
	}

	return choice.LayerSet, nil
}

// SetDefaultLayerSet records the layer set with the given ID as the default
// of the server for new diagrams, or with "" removes the choice. An ID that
// names no set gives an error that matches [ErrNotFound].
func (s *Service) SetDefaultLayerSet(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("choosing the default layer set: %w", err)
	}

	if id == "" {
		err := s.store.DeleteRecord(NamespaceLayerSets, layerSetDefaultKey, store.AnyRevision)
		if errors.Is(err, store.ErrRecordNotExist) {
			return nil
		}

		return storeError(kindLayerSetDefault, layerSetDefaultKey, store.AnyRevision, err)
	}

	if _, err := s.GetLayerSet(ctx, id); err != nil {
		return err
	}

	value, err := json.Marshal(layerSetDefault{LayerSet: id})
	if err != nil {
		return fmt.Errorf("encoding the default layer set: %w", err)
	}

	_, err = s.store.UpdateRecord(NamespaceLayerSets, layerSetDefaultKey, value, store.AnyRevision)
	if errors.Is(err, store.ErrRecordNotExist) {
		_, err = s.store.CreateRecord(NamespaceLayerSets, layerSetDefaultKey, value)
	}

	if errors.Is(err, store.ErrRecordExist) {
		_, err = s.store.UpdateRecord(NamespaceLayerSets, layerSetDefaultKey, value, store.AnyRevision)
	}

	return storeError(kindLayerSetDefault, layerSetDefaultKey, store.AnyRevision, err)
}

// checkLayerSet checks the content a set is made of or replaced with. It
// normalizes an empty list of matchers to none, so a set encodes the same
// however it was built.
func checkLayerSet(content *builder.LayerSet) error {
	for i := range content.Layers {
		if len(content.Layers[i].Matchers) == 0 {
			content.Layers[i].Matchers = nil
		}
	}

	if err := content.Validate(); err != nil {
		return newValidationCause(kindLayerSet, "is not a valid layer set", err)
	}

	return nil
}

// sameLayerSet reports whether two sets have the same content, as they
// encode.
func sameLayerSet(a, b *builder.LayerSet) bool {
	first, firstErr := json.Marshal(a)
	second, secondErr := json.Marshal(b)

	return firstErr == nil && secondErr == nil && bytes.Equal(first, second)
}

// writeLayerSet writes the record of a set against the revision that was
// read (0 for a new set), and returns the new revision. It refuses a record
// larger than [MaxMetadataBytes].
func (s *Service) writeLayerSet(set *LibraryLayerSet, revision int64) (int64, error) {
	value, err := json.Marshal(set)
	if err != nil {
		return 0, fmt.Errorf("encoding %s %s: %w", kindLayerSet, set.ID, err)
	}

	if len(value) > MaxMetadataBytes {
		return 0, newTooLargeError(kindLayerSet, int64(len(value)), MaxMetadataBytes)
	}

	key := layerSetKey(set.ID)

	var record store.Record

	if revision == 0 {
		record, err = s.store.CreateRecord(NamespaceLayerSets, key, value)
	} else {
		record, err = s.store.UpdateRecord(NamespaceLayerSets, key, value, revision)
	}

	if err == nil {
		return record.Revision, nil
	}

	return 0, storeError(kindLayerSet, set.ID, revision, err)
}

// decodeLayerSet strictly decodes and fully validates the record of a set.
// The record key must name the ID the set holds, so a record cannot claim to
// be another set.
func decodeLayerSet(record store.Record) (*LibraryLayerSet, error) {
	var set LibraryLayerSet

	if err := decodeMetadata(kindLayerSet, record.Value, &set); err != nil {
		return nil, newCorruptError(kindLayerSet, record.Key, err.Error())
	}

	switch {
	case layerSetKey(set.ID) != record.Key || !ValidID(set.ID):
		return nil, newCorruptError(kindLayerSet, record.Key, "the record names another layer set")
	case set.Version < 1:
		return nil, newCorruptError(kindLayerSet, record.Key, fmt.Sprintf("the layer set has version %d", set.Version))
	case validateText("createdBy", set.CreatedBy, MaxOwnerLength, false) != nil ||
		validateText("updatedBy", set.UpdatedBy, MaxOwnerLength, false) != nil:
		return nil, newCorruptError(kindLayerSet, record.Key, "the layer set names an unusable user")
	case set.LayerSet.Validate() != nil:
		return nil, newCorruptError(kindLayerSet, record.Key, "the layer set is not valid")
	}

	set.Revision = record.Revision

	return &set, nil
}
