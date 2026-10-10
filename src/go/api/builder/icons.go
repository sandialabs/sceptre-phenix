package builder

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"slices"
	"strings"
	"time"

	"phenix/store"
	"phenix/types/builder"
	"phenix/util/plog"
)

// Limits of the icon library. They are plain bounds, not a quota: two
// uploads at the same moment can each pass the check and leave the library,
// or one user's share of it, one icon over, and the next upload is then
// refused.
const (
	// MaxIconUploadBytes bounds the image [Service.AddIcon] is given (64
	// KiB). It is above [builder.MaxIconBytes], the bound on what is stored,
	// because an image that carries text or compresses badly is encoded
	// again before it is stored.
	MaxIconUploadBytes = 64 << 10

	// MaxLibraryIcons is the most icons one user may have uploaded to the
	// icon library.
	MaxLibraryIcons = 64

	// MaxLibraryIconBytes bounds the PNG bytes of the icons one user
	// uploaded, together (1 MiB).
	MaxLibraryIconBytes = 1 << 20

	// MaxIcons is the most icons the icon library holds, of all users
	// together.
	MaxIcons = 2000
)

// kindIcon names an icon in typed errors.
const kindIcon = "icon"

// The layout of the icon library's records (see [NamespaceIcons]). Every
// icon name and every alias owns the record "name/" and its name in lower
// case, so creating either is one create-if-absent write, and names are
// unique across icons and aliases ignoring case.
const (
	// iconKeyPrefix starts the key of every record of the icon library.
	iconKeyPrefix = "name/"

	// iconRecordKind and aliasRecordKind are the kinds of record a key holds:
	// an icon, or another name of one.
	iconRecordKind  = "icon"
	aliasRecordKind = "alias"

	// maxAliasHops is the most aliases a name is followed through. A rename
	// points every alias of the icon at its new name (see
	// [Service.RenameIcon]), so an alias is one hop from its icon; longer
	// chains are left only by a rename whose retargeting failed, and the
	// bound keeps a damaged store from looping.
	maxAliasHops = 16
)

// LibraryIcon is one icon of the icon library, and the value of its record.
// Every user who may list the library sees it; only the user who uploaded it
// and the holders of the builder-icons permissions rename or delete it, and
// only the latter one the server added (see [ServerIconOwner]).
type LibraryIcon struct {
	// Kind is "icon": the record is an icon and not an alias.
	Kind string `json:"kind"`
	// Name is the icon's name, as its uploader typed it or as it was last
	// renamed to (see [builder.IconNameProblem]). Nodes name the icon by it,
	// or by one of its Aliases, ignoring case.
	Name string `json:"name"`
	// ID is "sha256:" and the SHA-256 of the PNG (see [builder.IconID]), so
	// two icons hold the same image exactly when their IDs are equal.
	ID string `json:"id"`
	// Owner is the user who uploaded the icon, or [ServerIconOwner] for one
	// the server added from its template files, which no user owns.
	Owner string `json:"owner"`
	// Width and Height are the size of the image in pixels, and Bytes the
	// length of its PNG.
	Width  int `json:"width"`
	Height int `json:"height"`
	Bytes  int `json:"bytes"`
	// Created is when the icon was uploaded, and Updated when it was last
	// renamed (when it was uploaded, before its first rename).
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Aliases are the names the icon had before its renames, as they were
	// typed, oldest first. Each still names the icon.
	Aliases []string `json:"aliases"`
	// Data is the PNG, in standard base64 with padding, as a document
	// carries it (see [builder.Icon]).
	Data string `json:"data"`
	// Revision is the revision of the icon's record.
	Revision int64 `json:"-"`
}

// iconAlias is the record of a name an icon had before it was renamed. It
// points at the record of the icon's name now: each rename points the
// icon's aliases at the new name.
type iconAlias struct {
	// Kind is "alias".
	Kind string `json:"kind"`
	// Name is the old name, as it was typed.
	Name string `json:"name"`
	// Target is the name it points at, in lower case: what follows
	// "name/" in that record's key.
	Target string `json:"target"`
}

// IconOwnerUsage is how many icons one user uploaded to the icon library and
// their bytes, and how many icons the library holds in all (see
// [IconUsage]).
type IconOwnerUsage struct {
	Icons      int
	Bytes      int
	TotalIcons int
}

// iconKey returns the record key of an icon name or alias.
func iconKey(name string) string {
	return iconKeyPrefix + strings.ToLower(name)
}

// iconNameError is the refusal of a name that is not an icon name, in the
// words of [builder.IconNameProblem]: Field and Reason read as one sentence.
func iconNameError(name string) error {
	problem := builder.IconNameProblem(name)
	if problem == "" {
		return nil
	}

	return newValidationError("icon name", strings.TrimPrefix(problem, "icon name "))
}

// ListIcons returns every icon of the icon library, ordered by name without
// regard to case.
//
// A record is returned only when it is an icon in every respect (see
// readIcon). Any other record is logged and left out, so a damaged or
// planted record never reaches a browser. Aliases are not listed: each icon
// lists its own.
func (s *Service) ListIcons(ctx context.Context) ([]LibraryIcon, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("listing icons: %w", err)
	}

	records, err := s.store.ListRecords(NamespaceIcons, iconKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing icons: %w", err)
	}

	icons := make([]LibraryIcon, 0, len(records))

	for _, record := range records {
		icon, _, err := readIconRecord(record)

		switch {
		case err != nil:
			plog.Error(plog.TypeSystem, "skipping unreadable builder icon", "icon", record.Key, "err", err)
		case icon != nil:
			icons = append(icons, *icon)
		}
	}

	slices.SortFunc(icons, func(a, b LibraryIcon) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.Name, b.Name),
		)
	})

	return icons, nil
}

// IconUsage returns how many icons the owner uploaded and their bytes, and
// how many icons the library holds in all, from the icons [Service.ListIcons]
// returns.
func IconUsage(icons []LibraryIcon, owner string) IconOwnerUsage {
	usage := IconOwnerUsage{Icons: 0, Bytes: 0, TotalIcons: len(icons)}

	for i := range icons {
		if icons[i].Owner == owner {
			usage.Icons++
			usage.Bytes += icons[i].Bytes
		}
	}

	return usage
}

// GetIcon returns the icon a name names: the icon of that name, or the one
// that keeps it as an alias, ignoring case. The icon's Name is its name now.
// A name that names no icon, whatever its form, is an error matching
// [ErrNotFound].
func (s *Service) GetIcon(ctx context.Context, name string) (*LibraryIcon, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("reading an icon: %w", err)
	}

	return s.resolveIcon(name)
}

// AddIcon adds an image to the icon library under a name the owner chose,
// and returns the stored icon. The second result is false when the name
// already names an icon with the same bytes: that icon is returned as it is.
// Two names may hold the same image.
//
// The name must be an icon name (see [builder.IconNameProblem]) that names
// no icon and no alias, ignoring case; one that does is refused with an
// [IconNameTakenError] naming the icon and who uploaded it. The image must
// be a PNG of at most [builder.MaxIconPixels] a side and
// [MaxIconUploadBytes]. One a document accepts as it is (see
// [builder.ValidateIconPNG]) is stored unchanged. Any other is encoded
// again, which keeps its pixels and nothing else.
//
// A refusal for the name, the image or a full library is a
// [ValidationError] whose Field and Reason read as one sentence, and one for
// the size of the upload a [TooLargeError].
func (s *Service) AddIcon(ctx context.Context, owner, name string, upload []byte) (*LibraryIcon, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("adding an icon: %w", err)
	}

	if err := validateText("owner", owner, MaxOwnerLength, true); err != nil {
		return nil, false, err
	}

	data, width, height, err := iconUpload(name, upload)
	if err != nil {
		return nil, false, err
	}

	if existing, found, err := s.iconOfName(name, builder.IconID(data)); found || err != nil {
		return existing, false, err
	}

	icons, err := s.ListIcons(ctx)
	if err != nil {
		return nil, false, err
	}

	if err := checkIconLimits(IconUsage(icons, owner), len(data)); err != nil {
		return nil, false, err
	}

	return s.createIcon(owner, name, data, width, height)
}

// iconUpload checks the name and the image of an icon being added, and
// returns the PNG it is stored as and its width and height (see iconPNG).
func iconUpload(name string, upload []byte) ([]byte, int, int, error) {
	if err := iconNameError(name); err != nil {
		return nil, 0, 0, err
	}

	if len(upload) > MaxIconUploadBytes {
		return nil, 0, 0, newTooLargeError(kindIcon, int64(len(upload)), MaxIconUploadBytes)
	}

	return iconPNG(upload)
}

// createIcon stores a new icon of the given owner under a name no icon has,
// or under an alias whose icon is gone. The second result is false when
// another request stored the same image under the name first.
func (s *Service) createIcon(owner, name string, data []byte, width, height int) (*LibraryIcon, bool, error) {
	id := builder.IconID(data)
	now := s.clock().UTC()
	icon := LibraryIcon{
		Kind:     iconRecordKind,
		Name:     name,
		ID:       id,
		Owner:    owner,
		Width:    width,
		Height:   height,
		Bytes:    len(data),
		Created:  now,
		Updated:  now,
		Aliases:  []string{},
		Data:     base64.StdEncoding.EncodeToString(data),
		Revision: 0,
	}

	value, err := encodeIconRecord(icon)
	if err != nil {
		return nil, false, err
	}

	record, err := s.store.CreateRecord(NamespaceIcons, iconKey(name), value)

	switch {
	case err == nil:
		icon.Revision = record.Revision

		return &icon, true, nil
	case errors.Is(err, store.ErrRecordExist):
		// Another request took the name first, or the name is an alias
		// whose icon is gone, which a new icon may take over.
		return s.addIconOverAlias(name, id, &icon, value)
	}

	return nil, false, storeError(kindIcon, name, store.AnyRevision, err)
}

// iconOfName reports whether name already names an icon. The second result
// is true when it names one with the image id, which is returned. A name
// that names another image is refused with an [IconNameTakenError]; one
// that names nothing, or only an alias whose icon is gone, is not found.
func (s *Service) iconOfName(name, id string) (*LibraryIcon, bool, error) {
	existing, err := s.resolveIcon(name)

	switch {
	case errors.Is(err, ErrNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	case existing.ID != id:
		return nil, false, &IconNameTakenError{Name: name, Icon: existing.Name, Owner: existing.Owner}
	}

	return existing, true, nil
}

// addIconOverAlias finishes an upload whose name's record was created by
// someone else first: when it names an icon with the same image, that icon
// is the answer; when it is an alias whose icon is gone, the new icon
// replaces it; otherwise the name is taken.
func (s *Service) addIconOverAlias(name, id string, icon *LibraryIcon, value []byte) (*LibraryIcon, bool, error) {
	if existing, found, err := s.iconOfName(name, id); found || err != nil {
		return existing, false, err
	}

	record, err := s.store.GetRecord(NamespaceIcons, iconKey(name))
	if err != nil {
		return nil, false, storeError(kindIcon, name, store.AnyRevision, err)
	}

	if _, alias, err := readIconRecord(record); err != nil || alias == nil {
		return nil, false, &ConflictError{
			Kind: kindIcon, ID: name, Expected: store.AnyRevision, Actual: record.Revision, Reason: "the name was just taken",
		}
	}

	updated, err := s.store.UpdateRecord(NamespaceIcons, iconKey(name), value, record.Revision)
	if err != nil {
		return nil, false, storeError(kindIcon, name, record.Revision, err)
	}

	icon.Revision = updated.Revision

	return icon, true, nil
}

// checkIconLimits refuses an icon of the given bytes that would take the
// library past [MaxIcons], or its uploader past [MaxLibraryIcons] or
// [MaxLibraryIconBytes].
func checkIconLimits(usage IconOwnerUsage, size int) error {
	switch {
	case usage.TotalIcons >= MaxIcons:
		return newValidationError("icon library", fmt.Sprintf("is full: it holds at most %d icons", MaxIcons))
	case usage.Icons >= MaxLibraryIcons:
		return newValidationError(
			"icon library",
			fmt.Sprintf("is full for you: each user may upload at most %d icons", MaxLibraryIcons),
		)
	case usage.Bytes+size > MaxLibraryIconBytes:
		return newValidationError(
			"icon library",
			fmt.Sprintf("is full for you: the icons each user uploads may take at most %d bytes", MaxLibraryIconBytes),
		)
	}

	return nil
}

// RenameIcon gives the icon a name names a new name, and returns the icon
// as renamed. The old name keeps naming it, as an alias. The caller must be
// the user who uploaded the icon, or anyOwner must be set (the caller holds
// the builder-icons update permission); otherwise the error matches
// [ErrForbidden].
//
// A change of case only renames the icon in place. Any other new name must
// be one no icon and no alias has, ignoring case ([IconNameTakenError]),
// except an alias of this same icon, which the icon takes back. The icon
// moves to the new name's record first, and the old record becomes the
// alias; when the old record changed in between, the move is undone and the
// error matches [ErrConflict]. Every other alias the icon lists is then
// pointed at the new name, so each stays one hop from the icon however
// often it is renamed.
func (s *Service) RenameIcon(ctx context.Context, caller, name, newName string, anyOwner bool) (*LibraryIcon, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("renaming an icon: %w", err)
	}

	if err := validateText("owner", caller, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	icon, err := s.resolveIcon(name)
	if err != nil {
		return nil, err
	}

	if !mayChangeIcon(icon, caller, anyOwner) {
		return nil, fmt.Errorf("renaming icon %q of %s: %w", icon.Name, iconOwnerText(icon.Owner), ErrForbidden)
	}

	if err := iconNameError(newName); err != nil {
		return nil, err
	}

	if newName == icon.Name {
		return icon, nil
	}

	renamed := *icon
	renamed.Name = newName
	renamed.Updated = s.clock().UTC()

	if strings.EqualFold(newName, icon.Name) {
		return s.writeRenamedIcon(icon, &renamed)
	}

	renamed.Aliases = append(withoutName(icon.Aliases, newName), icon.Name)

	return s.moveIcon(icon, &renamed)
}

// writeRenamedIcon writes an icon whose name changed only in case over its
// own record, against the revision it was read at.
func (s *Service) writeRenamedIcon(icon, renamed *LibraryIcon) (*LibraryIcon, error) {
	value, err := encodeIconRecord(*renamed)
	if err != nil {
		return nil, err
	}

	record, err := s.store.UpdateRecord(NamespaceIcons, iconKey(icon.Name), value, icon.Revision)
	if err != nil {
		return nil, storeError(kindIcon, icon.Name, icon.Revision, err)
	}

	renamed.Revision = record.Revision

	return renamed, nil
}

// moveIcon writes the renamed icon to the record of its new name, then turns
// the record of its old name into an alias of the new one. When the new
// name's record holds an alias of this icon, the icon takes it back. A
// failure of the second write undoes the first.
func (s *Service) moveIcon(icon, renamed *LibraryIcon) (*LibraryIcon, error) {
	value, err := encodeIconRecord(*renamed)
	if err != nil {
		return nil, err
	}

	alias, err := json.Marshal(iconAlias{
		Kind: aliasRecordKind, Name: icon.Name, Target: strings.ToLower(renamed.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("encoding the alias %s: %w", icon.Name, err)
	}

	newKey := iconKey(renamed.Name)

	// What takes the new key back to what it held, should the old one not
	// become an alias.
	var undo func(revision int64) error

	created, err := s.store.CreateRecord(NamespaceIcons, newKey, value)

	switch {
	case err == nil:
		undo = func(revision int64) error { return s.store.DeleteRecord(NamespaceIcons, newKey, revision) }
	case errors.Is(err, store.ErrRecordExist):
		var taken store.Record

		created, taken, err = s.reclaimAlias(icon, renamed, value)
		if err != nil {
			return nil, err
		}

		undo = func(revision int64) error {
			_, err := s.store.UpdateRecord(NamespaceIcons, newKey, taken.Value, revision)

			return err
		}
	default:
		return nil, storeError(kindIcon, renamed.Name, store.AnyRevision, err)
	}

	if _, err := s.store.UpdateRecord(NamespaceIcons, iconKey(icon.Name), alias, icon.Revision); err != nil {
		if undoErr := undo(created.Revision); undoErr != nil {
			plog.Error(
				plog.TypeSystem, "undoing a builder icon rename failed",
				"icon", icon.Name, "name", renamed.Name, "err", undoErr,
			)
		}

		return nil, storeError(kindIcon, icon.Name, icon.Revision, err)
	}

	renamed.Revision = created.Revision

	s.retargetAliases(renamed)

	return renamed, nil
}

// retargetAliases points every alias the renamed icon lists at the record of
// its name now, so an alias never leads through another. An alias whose
// record is gone, or holds no alias any more, is left alone. A failure is
// logged and not returned: the rename is done, the alias still resolves
// through the alias it pointed at, and the next rename retargets it again.
func (s *Service) retargetAliases(renamed *LibraryIcon) {
	target := strings.ToLower(renamed.Name)

	for _, name := range renamed.Aliases {
		key := iconKey(name)

		record, err := s.store.GetRecord(NamespaceIcons, key)
		if err != nil {
			if !errors.Is(err, store.ErrRecordNotExist) {
				plog.Error(
					plog.TypeSystem, "reading a builder icon alias failed",
					"icon", renamed.Name, "alias", name, "err", err,
				)
			}

			continue
		}

		_, alias, err := readIconRecord(record)
		if err != nil || alias == nil || alias.Target == target {
			continue
		}

		alias.Target = target

		value, err := json.Marshal(*alias)
		if err == nil {
			_, err = s.store.UpdateRecord(NamespaceIcons, key, value, record.Revision)
		}

		if err != nil {
			plog.Error(
				plog.TypeSystem, "pointing a builder icon alias at its icon failed",
				"icon", renamed.Name, "alias", name, "err", err,
			)
		}
	}
}

// reclaimAlias writes the renamed icon over the record its new name already
// has, when that record is an alias that names this same icon, or no icon at
// all: renaming an icon back to a name it had takes the name back. It
// returns the record written and the one it replaced. Any other record means
// the name is taken.
func (s *Service) reclaimAlias(icon, renamed *LibraryIcon, value []byte) (store.Record, store.Record, error) {
	var none store.Record

	newKey := iconKey(renamed.Name)

	taken, err := s.store.GetRecord(NamespaceIcons, newKey)
	if err != nil {
		return none, none, storeError(kindIcon, renamed.Name, store.AnyRevision, err)
	}

	other, alias, err := readIconRecord(taken)
	if err != nil {
		return none, none, err
	}

	if alias != nil {
		other, err = s.resolveIcon(renamed.Name)

		switch {
		case errors.Is(err, ErrNotFound):
			other = nil
		case err != nil:
			return none, none, err
		}
	}

	if other != nil && iconKey(other.Name) != iconKey(icon.Name) {
		return none, none, &IconNameTakenError{Name: renamed.Name, Icon: other.Name, Owner: other.Owner}
	}

	written, err := s.store.UpdateRecord(NamespaceIcons, newKey, value, taken.Revision)
	if err != nil {
		return none, none, storeError(kindIcon, renamed.Name, taken.Revision, err)
	}

	return written, taken, nil
}

// mayChangeIcon reports whether caller may rename or delete the icon: its
// uploader may, and with anyOwner (the caller holds the builder-icons
// permission of that verb) anyone may. An icon the server added has no
// uploader (see [ServerIconOwner]), so only anyOwner allows it.
func mayChangeIcon(icon *LibraryIcon, caller string, anyOwner bool) bool {
	return anyOwner || (icon.Owner != ServerIconOwner && icon.Owner == caller)
}

// iconOwnerText names who holds an icon in an error: the user who uploaded
// it, or "the server" for one the server added.
func iconOwnerText(owner string) string {
	if owner == ServerIconOwner {
		return "the server"
	}

	return owner
}

// withoutName returns names without the one equal to name ignoring case.
func withoutName(names []string, name string) []string {
	kept := make([]string, 0, len(names))

	for _, other := range names {
		if !strings.EqualFold(other, name) {
			kept = append(kept, other)
		}
	}

	return kept
}

// DeleteIcon removes the icon a name names from the icon library, with
// every alias that names it: deleting through an alias deletes the icon.
// Nodes that named it show their built-in icon, and documents that carry a
// copy keep it. The caller must be the user who uploaded the icon, or
// anyOwner must be set (the caller holds the builder-icons delete
// permission); otherwise the error matches [ErrForbidden].
//
// A name that names no icon is an error matching [ErrNotFound]. With
// anyOwner, a record of that name this server cannot read is removed. When
// the icon is gone but an alias could not be removed, the error matches
// [ErrCleanup].
func (s *Service) DeleteIcon(ctx context.Context, caller, name string, anyOwner bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deleting an icon: %w", err)
	}

	if err := validateText("owner", caller, MaxOwnerLength, true); err != nil {
		return err
	}

	icon, err := s.resolveIcon(name)

	switch {
	case errors.Is(err, ErrCorrupt) && anyOwner:
		return s.deleteUnreadableIcon(name, err)
	case err != nil:
		return err
	case !mayChangeIcon(icon, caller, anyOwner):
		return fmt.Errorf("deleting icon %q of %s: %w", icon.Name, iconOwnerText(icon.Owner), ErrForbidden)
	}

	records, err := s.store.ListRecords(NamespaceIcons, iconKeyPrefix)
	if err != nil {
		return fmt.Errorf("listing icons: %w", err)
	}

	aliases := aliasesOf(records, icon)

	if err := s.store.DeleteRecord(NamespaceIcons, iconKey(icon.Name), icon.Revision); err != nil {
		return storeError(kindIcon, icon.Name, icon.Revision, err)
	}

	var failures []error

	for _, record := range aliases {
		if err := s.store.DeleteRecord(NamespaceIcons, record.Key, record.Revision); err != nil &&
			!errors.Is(err, store.ErrRecordNotExist) {
			failures = append(failures, storeError(kindIcon, record.Key, record.Revision, err))
		}
	}

	return newCleanupError("deleting the aliases of icon "+icon.Name, failures)
}

// deleteUnreadableIcon removes the record of a name when this server cannot
// read it, and otherwise returns the error that resolving the name gave: a
// record further along its aliases is not the name's to remove.
func (s *Service) deleteUnreadableIcon(name string, resolveErr error) error {
	record, err := s.store.GetRecord(NamespaceIcons, iconKey(name))
	if err != nil {
		return storeError(kindIcon, name, store.AnyRevision, err)
	}

	if _, _, err := readIconRecord(record); err == nil {
		return resolveErr
	}

	if err := s.store.DeleteRecord(NamespaceIcons, record.Key, record.Revision); err != nil {
		return storeError(kindIcon, name, record.Revision, err)
	}

	return nil
}

// aliasesOf returns the alias records whose chain ends at the icon, and
// those of the aliases the icon lists whose chain ends at no icon: what
// deleting the icon would leave pointing at nothing.
func aliasesOf(records store.Records, icon *LibraryIcon) []store.Record {
	var (
		targets = make(map[string]string, len(records))
		aliases = make([]store.Record, 0, len(records))
		icons   = make(map[string]bool, len(records))
	)

	for _, record := range records {
		other, alias, err := readIconRecord(record)

		switch {
		case err != nil:
		case alias != nil:
			targets[record.Key] = iconKeyPrefix + alias.Target
			aliases = append(aliases, record)
		case other != nil:
			icons[record.Key] = true
		}
	}

	listed := make(map[string]bool, len(icon.Aliases))
	for _, name := range icon.Aliases {
		listed[iconKey(name)] = true
	}

	home := iconKey(icon.Name)
	found := make([]store.Record, 0, len(icon.Aliases))

	for _, record := range aliases {
		end := record.Key

		for range maxAliasHops + 1 {
			next, ok := targets[end]
			if !ok {
				break
			}

			end = next
		}

		if end == home || (listed[record.Key] && !icons[end]) {
			found = append(found, record)
		}
	}

	return found
}

// CleanupLegacyIcons removes the records of the icon library that are not
// in its layout: those of the per-user libraries of earlier builds, keyed by
// a user's scope and an icon's digest. It returns how many it removed.
func (s *Service) CleanupLegacyIcons(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("cleaning icons: %w", err)
	}

	keys, err := s.store.ListRecordKeys(NamespaceIcons, "")
	if err != nil {
		return 0, fmt.Errorf("listing icon records: %w", err)
	}

	var (
		removed  int
		failures []error
	)

	for _, key := range keys {
		if strings.HasPrefix(key, iconKeyPrefix) {
			continue
		}

		err := s.store.DeleteRecord(NamespaceIcons, key, store.AnyRevision)

		switch {
		case err == nil:
			removed++
		case !errors.Is(err, store.ErrRecordNotExist):
			failures = append(failures, storeError(kindIcon, key, store.AnyRevision, err))
		}
	}

	return removed, newCleanupError("removing icon records of the per-user layout", failures)
}

// resolveIcon returns the icon a name names, following aliases up to
// [maxAliasHops]. A name that is not an icon name, has no record, or ends in
// an alias whose icon is gone is not found; a record that is not an icon or
// an alias in every respect is corrupt.
func (s *Service) resolveIcon(name string) (*LibraryIcon, error) {
	if builder.IconNameProblem(name) != "" {
		return nil, newNotFoundError(kindIcon, name)
	}

	key := iconKey(name)

	for range maxAliasHops + 1 {
		record, err := s.store.GetRecord(NamespaceIcons, key)
		if err != nil {
			return nil, storeError(kindIcon, name, store.AnyRevision, err)
		}

		icon, alias, err := readIconRecord(record)
		if err != nil {
			return nil, err
		}

		if icon != nil {
			return icon, nil
		}

		key = iconKeyPrefix + alias.Target
	}

	return nil, newNotFoundError(kindIcon, name)
}

// encodeIconRecord returns the record value of an icon.
func encodeIconRecord(icon LibraryIcon) ([]byte, error) {
	if icon.Aliases == nil {
		icon.Aliases = []string{}
	}

	value, err := json.Marshal(icon)
	if err != nil {
		return nil, fmt.Errorf("encoding icon %s: %w", icon.Name, err)
	}

	return value, nil
}

// iconPNG returns the PNG an uploaded image is stored as, and its width and
// height in pixels: the upload itself when a document accepts it as it is,
// and otherwise its pixels encoded again, which drops text, metadata, color
// profiles, further frames, a palette the pixels do not use and anything
// after the pixels or the end of the image.
func iconPNG(upload []byte) ([]byte, int, int, error) {
	if width, height, err := builder.ValidateIconPNG(upload); err == nil {
		return upload, width, height, nil
	}

	const notPNG = "is not a PNG image"

	// Only the header is read, so an image that claims to be huge is
	// refused for its size before any pixel is decoded.
	config, err := png.DecodeConfig(bytes.NewReader(upload))
	if err != nil {
		return nil, 0, 0, newValidationCause(kindIcon, notPNG, err)
	}

	if config.Width > builder.MaxIconPixels || config.Height > builder.MaxIconPixels {
		return nil, 0, 0, newValidationError(kindIcon, fmt.Sprintf(
			"is %d x %d pixels; the limit is %d x %d",
			config.Width, config.Height, builder.MaxIconPixels, builder.MaxIconPixels,
		))
	}

	data, err := builder.NormalizeIconPNG(upload)
	if err != nil {
		return nil, 0, 0, newValidationCause(kindIcon, notPNG, err)
	}

	width, height, err := builder.ValidateIconPNG(data)
	if err != nil {
		return nil, 0, 0, newValidationCause(kindIcon, notPNG, err)
	}

	return data, width, height, nil
}

// readIconRecord returns what a record of the icon library holds: an icon,
// or an alias. Exactly one of the two is set when the error is nil.
//
// The error never quotes what the record holds.
func readIconRecord(record store.Record) (*LibraryIcon, *iconAlias, error) {
	var head struct {
		Kind string `json:"kind"`
	}

	if err := json.Unmarshal(record.Value, &head); err != nil {
		return nil, nil, newCorruptError(kindIcon, record.Key, "the record is not valid JSON")
	}

	switch head.Kind {
	case iconRecordKind:
		icon, err := readIcon(record)

		return icon, nil, err
	case aliasRecordKind:
		alias, err := readAlias(record)

		return nil, alias, err
	}

	return nil, nil, newCorruptError(kindIcon, record.Key, "the record is neither an icon nor an alias")
}

// readIcon returns the icon a record holds. The record is an icon only when
// it decodes strictly, has the name its key does, names an uploader (or
// [ServerIconOwner]) and the ID of an image, lists aliases that are icon
// names other than its own, and holds a PNG a document accepts whose ID is
// that ID. Its size and its base64 are taken from that PNG, never from the
// record.
func readIcon(record store.Record) (*LibraryIcon, error) {
	var icon LibraryIcon

	if err := decodeMetadata(kindIcon, record.Value, &icon); err != nil {
		return nil, newCorruptError(kindIcon, record.Key, err.Error())
	}

	switch {
	case builder.IconNameProblem(icon.Name) != "" || iconKey(icon.Name) != record.Key:
		return nil, newCorruptError(kindIcon, record.Key, "the record names another icon")
	case icon.Owner != ServerIconOwner && validateText("owner", icon.Owner, MaxOwnerLength, true) != nil:
		return nil, newCorruptError(kindIcon, record.Key, "the record names no usable owner")
	case !builder.IsDigest(icon.ID):
		return nil, newCorruptError(kindIcon, record.Key, "the record has no image ID")
	case aliasesProblem(icon.Name, icon.Aliases):
		return nil, newCorruptError(kindIcon, record.Key, "the aliases are not names an icon may have")
	}

	data, err := base64.StdEncoding.Strict().DecodeString(icon.Data)
	if err != nil {
		return nil, newCorruptError(kindIcon, record.Key, "the image is not base64")
	}

	width, height, err := builder.ValidateIconPNG(data)
	if err != nil {
		return nil, newCorruptError(kindIcon, record.Key, "the image is not an accepted PNG")
	}

	if builder.IconID(data) != icon.ID {
		return nil, newCorruptError(kindIcon, record.Key, "the image does not match its ID")
	}

	icon.Width, icon.Height, icon.Bytes = width, height, len(data)
	icon.Data = base64.StdEncoding.EncodeToString(data)
	icon.Revision = record.Revision

	if icon.Aliases == nil {
		icon.Aliases = []string{}
	}

	return &icon, nil
}

// aliasesProblem reports whether aliases are not the old names of an icon
// named name: each an icon name, none equal to name or to another ignoring
// case.
func aliasesProblem(name string, aliases []string) bool {
	seen := map[string]bool{strings.ToLower(name): true}

	for _, alias := range aliases {
		if builder.IconNameProblem(alias) != "" || seen[strings.ToLower(alias)] {
			return true
		}

		seen[strings.ToLower(alias)] = true
	}

	return false
}

// readAlias returns the alias a record holds. The record is an alias only
// when it decodes strictly, has the name its key does, and points at the
// lower case form of another icon name.
func readAlias(record store.Record) (*iconAlias, error) {
	var alias iconAlias

	if err := decodeMetadata(kindIcon, record.Value, &alias); err != nil {
		return nil, newCorruptError(kindIcon, record.Key, err.Error())
	}

	switch {
	case builder.IconNameProblem(alias.Name) != "" || iconKey(alias.Name) != record.Key:
		return nil, newCorruptError(kindIcon, record.Key, "the alias names another icon")
	case builder.IconNameProblem(alias.Target) != "" || alias.Target != strings.ToLower(alias.Target) ||
		iconKeyPrefix+alias.Target == record.Key:
		return nil, newCorruptError(kindIcon, record.Key, "the alias points at no other name")
	}

	return &alias, nil
}
