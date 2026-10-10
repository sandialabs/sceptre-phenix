package web

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

const (
	// builderIconRequestBytes bounds the body of a request adding an icon
	// (128 KiB): the base64 of the largest upload, its name, and its JSON.
	builderIconRequestBytes = 128 << 10

	// builderIconRenameBytes bounds the body of a request renaming an icon
	// (4 KiB): a name and its JSON.
	builderIconRenameBytes = 4 << 10
)

// builderIconUpload adds an image to the icon library under a name the
// caller chose. Data is a PNG in standard base64 with padding; no file name,
// extension or declared type is ever read.
type builderIconUpload struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// builderIconRename gives an icon a new name. The old name keeps naming it,
// as an alias.
type builderIconRename struct {
	Name string `json:"name"`
}

// builderIconResponse is one icon of the icon library. Data is the stored
// PNG as base64: the image is only ever sent as text inside JSON, never as
// an image a browser could be pointed at. CanRename and CanDelete say
// whether the caller may rename or delete it: its uploader, or a holder of
// the builder-icons permission of that verb, with the configs permission of
// the same verb.
type builderIconResponse struct {
	Name      string    `json:"name"`
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Bytes     int       `json:"bytes"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Aliases   []string  `json:"aliases"`
	Data      string    `json:"data"`
	CanRename bool      `json:"canRename"`
	CanDelete bool      `json:"canDelete"`
}

// builderIconListResponse is the icon library and the limits of the caller's
// uploads: how many icons and bytes each user may upload, and how many the
// caller uploaded.
type builderIconListResponse struct {
	Icons     []builderIconResponse `json:"icons"`
	MaxIcons  int                   `json:"maxIcons"`
	MaxBytes  int                   `json:"maxBytes"`
	UsedBytes int                   `json:"usedBytes"`
	UsedIcons int                   `json:"usedIcons"`
}

// newBuilderIconResponse returns an icon as the caller is answered with it.
func newBuilderIconResponse(actor builderActor, icon *bapi.LibraryIcon) builderIconResponse {
	aliases := append([]string{}, icon.Aliases...)
	own := icon.Owner == actor.user

	return builderIconResponse{
		Name:    icon.Name,
		ID:      icon.ID,
		Owner:   icon.Owner,
		Width:   icon.Width,
		Height:  icon.Height,
		Bytes:   icon.Bytes,
		Created: icon.Created,
		Updated: icon.Updated,
		Aliases: aliases,
		Data:    icon.Data,
		CanRename: builderBaseAllowed(actor.role, builderVerbUpdate) &&
			(own || builderIconsUpdateAllowed(actor.role)),
		CanDelete: builderBaseAllowed(actor.role, builderVerbDelete) &&
			(own || builderIconsDeleteAllowed(actor.role)),
	}
}

// builderIconNotFound is the 404 of a name that names no icon. It never
// repeats the path it was asked for.
func builderIconNotFound() *weberror.WebError {
	return weberror.NewWebError(nil, "icon not found").SetStatus(http.StatusNotFound)
}

// builderIconError maps a refusal of the icon library to its answer: what is
// wrong with the upload, the name or the library in the service's own words,
// which name no stored record and repeat nothing of the image. Any other
// failure is answered with message.
func builderIconError(err error, message string) *weberror.WebError {
	var (
		tooLarge *bapi.TooLargeError
		invalid  *bapi.ValidationError
		taken    *bapi.IconNameTakenError
	)

	switch {
	case errors.As(err, &taken):
		return builderWebError(err, "%s", taken.Sentence())
	case errors.As(err, &tooLarge):
		return builderWebError(err, "icon is larger than %d bytes", tooLarge.Limit)
	case errors.As(err, &invalid):
		return builderWebError(err, "%s %s", invalid.Field, invalid.Reason)
	case errors.Is(err, bapi.ErrNotFound):
		return builderIconNotFound()
	case errors.Is(err, bapi.ErrConflict):
		return builderWebError(err, "the icon changed while this request ran; try again")
	}

	return builderWebError(err, "%s", message)
}

// listIcons - GET /builder/icons.
//
// The icon library is the server's: every caller with configs list sees
// every icon, with who uploaded it and whether the caller may rename or
// delete it.
func (b *builderAPI) listIcons(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListIcons")

	actor, err := builderAuthorize(r, builderVerbList, "listing builder icons")
	if err != nil {
		return err
	}

	icons, err := b.drafts.ListIcons(r.Context())
	if err != nil {
		return builderWebError(err, "unable to list the icons")
	}

	usage := bapi.IconUsage(icons, actor.user)
	response := builderIconListResponse{
		Icons:     make([]builderIconResponse, 0, len(icons)),
		MaxIcons:  bapi.MaxLibraryIcons,
		MaxBytes:  bapi.MaxLibraryIconBytes,
		UsedBytes: usage.Bytes,
		UsedIcons: usage.Icons,
	}

	for i := range icons {
		response.Icons = append(response.Icons, newBuilderIconResponse(actor, &icons[i]))
	}

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// getIcon - GET /builder/icons/{icon}.
//
// The path names the icon by its name or by one of its aliases, ignoring
// case; the answer carries the name it has now.
func (b *builderAPI) getIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetIcon")

	actor, err := builderAuthorize(r, builderVerbGet, "getting a builder icon")
	if err != nil {
		return err
	}

	icon, err := b.drafts.GetIcon(r.Context(), mux.Vars(r)["icon"])
	if err != nil {
		return builderIconError(err, "unable to read the icon")
	}

	return builderWriteJSON(w, http.StatusOK, "", newBuilderIconResponse(actor, icon))
}

// createIcon - POST /builder/icons.
//
// The answer is 201 for a new icon, 200 when the name already names an icon
// with the same bytes, and 409 when it names another. Either way the caller
// must use the data of the answer: an image that is not already one a
// document accepts is stored encoded again.
func (b *builderAPI) createIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateIcon")

	actor, err := builderAuthorize(r, builderVerbCreate, "adding a builder icon")
	if err != nil {
		return err
	}

	var request builderIconUpload

	if err := builderDecodeLimit(w, r, &request, builderIconRequestBytes); err != nil {
		return err
	}

	// The decoder skips line breaks, which the data of an icon never has.
	data, err := base64.StdEncoding.Strict().DecodeString(request.Data)
	if err != nil || strings.ContainsAny(request.Data, "\r\n") {
		return weberror.NewWebError(nil, "icon data is not base64").SetStatus(http.StatusBadRequest)
	}

	if len(data) == 0 {
		return weberror.NewWebError(nil, "icon data is required").SetStatus(http.StatusBadRequest)
	}

	icon, created, err := b.drafts.AddIcon(r.Context(), actor.user, request.Name, data)
	if err != nil {
		return builderIconError(err, "unable to add the icon")
	}

	status := http.StatusOK

	if created {
		status = http.StatusCreated

		plog.Info(plog.TypeAction, "added builder icon", "user", actor.user, "icon", icon.Name, "id", icon.ID)
	}

	return builderWriteJSON(w, status, "", newBuilderIconResponse(actor, icon))
}

// renameIcon - PUT /builder/icons/{icon}.
//
// Gives the icon the path names a new name; the old one keeps naming it, as
// an alias. Its uploader may rename it, and so may a holder of
// builder-icons update. Anyone else, who sees the icon in the listing, is
// answered 403.
func (b *builderAPI) renameIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderRenameIcon")

	const action = "renaming a builder icon"

	actor, err := builderAuthorize(r, builderVerbUpdate, action)
	if err != nil {
		return err
	}

	var request builderIconRename

	if err := builderDecodeLimit(w, r, &request, builderIconRenameBytes); err != nil {
		return err
	}

	name := mux.Vars(r)["icon"]

	icon, err := b.drafts.RenameIcon(r.Context(), actor.user, name, request.Name, builderIconsUpdateAllowed(actor.role))
	if errors.Is(err, bapi.ErrForbidden) {
		return builderForbidden(actor, action+" another user uploaded")
	}

	if err != nil {
		return builderIconError(err, "unable to rename the icon")
	}

	plog.Info(
		plog.TypeAction, "renamed builder icon",
		"user", actor.user, "icon", name, "name", icon.Name, "owner", icon.Owner,
	)

	return builderWriteJSON(w, http.StatusOK, "", newBuilderIconResponse(actor, icon))
}

// deleteIcon - DELETE /builder/icons/{icon}.
//
// Deletes the icon the path names, by its name or an alias, with all its
// aliases. Its uploader may delete it, and so may a holder of builder-icons
// delete. Anyone else is answered 403; a name that names no icon is 404.
func (b *builderAPI) deleteIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteIcon")

	const action = "deleting a builder icon"

	actor, err := builderAuthorize(r, builderVerbDelete, action)
	if err != nil {
		return err
	}

	name := mux.Vars(r)["icon"]

	err = b.drafts.DeleteIcon(r.Context(), actor.user, name, builderIconsDeleteAllowed(actor.role))

	switch {
	case err == nil:
	case errors.Is(err, bapi.ErrCleanup):
		builderWarnCleanup(w, err, "deleting the icon", actor.user)
	case errors.Is(err, bapi.ErrForbidden):
		return builderForbidden(actor, action+" another user uploaded")
	default:
		return builderIconError(err, "unable to delete the icon")
	}

	plog.Info(plog.TypeAction, "deleted builder icon", "user", actor.user, "icon", name)

	w.WriteHeader(http.StatusNoContent)

	return nil
}
