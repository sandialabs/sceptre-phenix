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

// Limits of a user's icon library. They are plain bounds, not a quota: two
// uploads at the same moment can each pass the check and leave a library one
// icon over, and the next upload is then refused.
const (
	// MaxIconUploadBytes bounds the image [Service.AddIcon] is given (64
	// KiB). It is above [builder.MaxIconBytes], the bound on what is stored,
	// because an image that carries text or compresses badly is encoded
	// again before it is stored.
	MaxIconUploadBytes = 64 << 10

	// MaxLibraryIcons is the most icons one user's library holds.
	MaxLibraryIcons = 64

	// MaxLibraryIconBytes bounds the PNG bytes of all the icons of one
	// user's library together (1 MiB).
	MaxLibraryIconBytes = 1 << 20
)

// kindIcon names an icon in typed errors.
const kindIcon = "icon"

// LibraryIcon is one icon of a user's icon library, and the value of its
// record. A record is never updated: an icon is named by its content, so the
// same image has the same ID in every library and in every document.
type LibraryIcon struct {
	// ID is the icon id: "sha256:" and the SHA-256 of the PNG (see
	// [builder.IconID]).
	ID string `json:"id"`
	// Owner is the user whose library holds the icon.
	Owner string `json:"owner"`
	// Name is the name the icon was given, for people. It identifies
	// nothing.
	Name string `json:"name,omitempty"`
	// Width and Height are the size of the image in pixels, and Bytes the
	// length of its PNG.
	Width  int `json:"width"`
	Height int `json:"height"`
	Bytes  int `json:"bytes"`
	// Created is when the icon was added.
	Created time.Time `json:"created"`
	// Data is the PNG, in standard base64 with padding, as a document
	// carries it (see [builder.Icon]).
	Data string `json:"data"`
}

// ListIcons returns the icons of the owner's library, ordered by name
// without regard to case, then by ID.
//
// A record is returned only when it is the owner's icon in every respect
// (see readIcon). Any other record is logged and left out, so a damaged or
// planted record never reaches a browser.
func (s *Service) ListIcons(ctx context.Context, owner string) ([]LibraryIcon, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("listing icons: %w", err)
	}

	if err := validateText("owner", owner, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	records, err := s.store.ListRecords(NamespaceIcons, OwnerScope(owner)+"/")
	if err != nil {
		return nil, fmt.Errorf("listing icons: %w", err)
	}

	icons := make([]LibraryIcon, 0, len(records))

	for _, record := range records {
		icon, err := readIcon(owner, record)
		if err != nil {
			plog.Error(plog.TypeSystem, "skipping unreadable builder icon", "icon", record.Key, "err", err)

			continue
		}

		icons = append(icons, *icon)
	}

	slices.SortFunc(icons, func(a, b LibraryIcon) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.ID, b.ID),
		)
	})

	return icons, nil
}

// AddIcon adds an image to the owner's library and returns the stored icon.
// The second result is false when the library already held an icon with the
// same bytes: that icon is returned as it is, under the name it has.
//
// The image must be a PNG of at most [builder.MaxIconPixels] a side and
// [MaxIconUploadBytes]. One a document accepts as it is (see
// [builder.ValidateIconPNG]) is stored unchanged, so an icon saved from a
// document keeps the document's ID. Any other is encoded again, which keeps
// its pixels and nothing else. The name is trimmed.
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

	name = strings.TrimSpace(name)

	if validateText("name", name, builder.MaxIconNameBytes, false) != nil {
		return nil, false, newValidationError(
			"icon name",
			fmt.Sprintf("must be at most %d bytes and contain no control characters", builder.MaxIconNameBytes),
		)
	}

	if len(upload) > MaxIconUploadBytes {
		return nil, false, newTooLargeError(kindIcon, int64(len(upload)), MaxIconUploadBytes)
	}

	data, width, height, err := iconPNG(upload)
	if err != nil {
		return nil, false, err
	}

	icons, err := s.ListIcons(ctx, owner)
	if err != nil {
		return nil, false, err
	}

	var (
		id   = builder.IconID(data)
		used = 0
	)

	for i := range icons {
		if icons[i].ID == id {
			return &icons[i], false, nil
		}

		used += icons[i].Bytes
	}

	switch {
	case len(icons) >= MaxLibraryIcons:
		return nil, false, newValidationError("icon library", fmt.Sprintf("is full: at most %d icons", MaxLibraryIcons))
	case used+len(data) > MaxLibraryIconBytes:
		return nil, false, newValidationError(
			"icon library",
			fmt.Sprintf("is full: at most %d bytes", MaxLibraryIconBytes),
		)
	}

	icon := LibraryIcon{
		ID:      id,
		Owner:   owner,
		Name:    name,
		Width:   width,
		Height:  height,
		Bytes:   len(data),
		Created: s.clock().UTC(),
		Data:    base64.StdEncoding.EncodeToString(data),
	}

	value, err := json.Marshal(icon)
	if err != nil {
		return nil, false, fmt.Errorf("encoding icon %s: %w", id, err)
	}

	_, err = s.store.CreateRecord(NamespaceIcons, iconKey(owner, id), value)

	// Another request of the owner stored the same image first.
	if errors.Is(err, store.ErrRecordExist) {
		existing, readErr := s.storedIcon(owner, id)

		return existing, false, readErr
	}

	if err != nil {
		return nil, false, storeError(kindIcon, id, store.AnyRevision, err)
	}

	return &icon, true, nil
}

// DeleteIcon removes an icon from the owner's library. Documents that use
// the icon keep their own copy. An ID that names no icon of the owner,
// whatever its form, is an error matching [ErrNotFound].
func (s *Service) DeleteIcon(ctx context.Context, owner, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deleting an icon: %w", err)
	}

	if err := validateText("owner", owner, MaxOwnerLength, true); err != nil {
		return err
	}

	if !builder.IsDigest(id) {
		return newNotFoundError(kindIcon, id)
	}

	if err := s.store.DeleteRecord(NamespaceIcons, iconKey(owner, id), store.AnyRevision); err != nil {
		return storeError(kindIcon, id, store.AnyRevision, err)
	}

	return nil
}

// storedIcon returns one icon of the owner's library.
func (s *Service) storedIcon(owner, id string) (*LibraryIcon, error) {
	record, err := s.store.GetRecord(NamespaceIcons, iconKey(owner, id))
	if err != nil {
		return nil, storeError(kindIcon, id, store.AnyRevision, err)
	}

	return readIcon(owner, record)
}

// iconKey returns the record key of an icon of the owner: the owner's scope,
// then the 64 hex digits of the icon ID, which are what follows its
// "sha256:".
func iconKey(owner, id string) string {
	_, digits, _ := strings.Cut(id, ":")

	return OwnerScope(owner) + "/" + digits
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

// readIcon returns the icon a record of the owner's library holds. The
// record is the owner's icon only when it decodes strictly, names the owner,
// names the ID its key does, has a name an icon may have, and holds a PNG a
// document accepts whose ID is that ID. Its size and its base64 are taken
// from that PNG, never from the record.
//
// The error never quotes what the record holds.
func readIcon(owner string, record store.Record) (*LibraryIcon, error) {
	var icon LibraryIcon

	if err := decodeMetadata(kindIcon, record.Value, &icon); err != nil {
		return nil, newCorruptError(kindIcon, record.Key, err.Error())
	}

	switch {
	case icon.Owner != owner:
		return nil, newCorruptError(kindIcon, record.Key, "the record names another owner")
	case !builder.IsDigest(icon.ID) || iconKey(owner, icon.ID) != record.Key:
		return nil, newCorruptError(kindIcon, record.Key, "the record names another icon")
	case validateText("name", icon.Name, builder.MaxIconNameBytes, false) != nil:
		return nil, newCorruptError(kindIcon, record.Key, "the name is not one an icon may have")
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

	return &icon, nil
}
