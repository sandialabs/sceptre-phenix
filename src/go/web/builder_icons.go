package web

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

const (
	// builderIconRequestBytes bounds the body of a request adding an icon
	// (128 KiB): the base64 of the largest upload, and its JSON.
	builderIconRequestBytes = 128 << 10

	// builderIconIDPrefix starts every icon ID. The path of an icon holds
	// only what follows it, 64 hex digits.
	builderIconIDPrefix = "sha256:"
)

// builderIconUpload adds an image to the caller's icon library. Data is a
// PNG in standard base64 with padding; no file name, extension or declared
// type is ever read.
type builderIconUpload struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// builderIconResponse is one icon of the caller's library. Data is the
// stored PNG as base64: the image is only ever sent as text inside JSON,
// never as an image a browser could be pointed at. Whose library it is in is
// never sent: it is always the caller's.
type builderIconResponse struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Width   int       `json:"width"`
	Height  int       `json:"height"`
	Bytes   int       `json:"bytes"`
	Created time.Time `json:"created"`
	Data    string    `json:"data"`
}

// builderIconListResponse is the caller's icon library and its limits.
type builderIconListResponse struct {
	Icons     []builderIconResponse `json:"icons"`
	MaxIcons  int                   `json:"maxIcons"`
	MaxBytes  int                   `json:"maxBytes"`
	UsedBytes int                   `json:"usedBytes"`
}

func newBuilderIconResponse(icon *bapi.LibraryIcon) builderIconResponse {
	return builderIconResponse{
		ID:      icon.ID,
		Name:    icon.Name,
		Width:   icon.Width,
		Height:  icon.Height,
		Bytes:   icon.Bytes,
		Created: icon.Created,
		Data:    icon.Data,
	}
}

// builderIconNotFound is the 404 of an icon the caller's library does not
// hold. It never repeats the path it was asked for.
func builderIconNotFound() *weberror.WebError {
	return weberror.NewWebError(nil, "icon not found").SetStatus(http.StatusNotFound)
}

// builderIconError maps a refusal of [bapi.Service.AddIcon] to its answer:
// what is wrong with the upload or the library in the service's own words,
// which name no stored record and repeat nothing of the image.
func builderIconError(err error) *weberror.WebError {
	var (
		tooLarge *bapi.TooLargeError
		invalid  *bapi.ValidationError
	)

	switch {
	case errors.As(err, &tooLarge):
		return builderWebError(err, "icon is larger than %d bytes", tooLarge.Limit)
	case errors.As(err, &invalid):
		return builderWebError(err, "%s %s", invalid.Field, invalid.Reason)
	}

	return builderWebError(err, "unable to add the icon")
}

// listIcons - GET /builder/icons.
//
// An icon library belongs to one user. Every icon route acts on the
// caller's own library: there is no owner in the path, and no role reaches
// the library of another user.
func (b *builderAPI) listIcons(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListIcons")

	actor, err := builderAuthorize(r, builderVerbList, "listing builder icons")
	if err != nil {
		return err
	}

	icons, err := b.drafts.ListIcons(r.Context(), actor.user)
	if err != nil {
		return builderWebError(err, "unable to list the icons")
	}

	response := builderIconListResponse{
		Icons:     make([]builderIconResponse, 0, len(icons)),
		MaxIcons:  bapi.MaxLibraryIcons,
		MaxBytes:  bapi.MaxLibraryIconBytes,
		UsedBytes: 0,
	}

	for i := range icons {
		response.Icons = append(response.Icons, newBuilderIconResponse(&icons[i]))
		response.UsedBytes += icons[i].Bytes
	}

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// createIcon - POST /builder/icons.
//
// The answer is 201 for a new icon and 200 for bytes the library already
// holds. Either way the caller must use the ID and the data of the answer:
// an image that is not already one a document accepts is stored encoded
// again.
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
		return builderIconError(err)
	}

	status := http.StatusOK

	if created {
		status = http.StatusCreated

		plog.Info(plog.TypeAction, "added builder icon", "user", actor.user, "icon", icon.ID)
	}

	return builderWriteJSON(w, status, "", newBuilderIconResponse(icon))
}

// deleteIcon - DELETE /builder/icons/{icon}.
//
// The path names the icon by the 64 hex digits of its ID. Anything else,
// and an icon the caller's library does not hold, is the same 404.
func (b *builderAPI) deleteIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteIcon")

	actor, err := builderAuthorize(r, builderVerbDelete, "deleting a builder icon")
	if err != nil {
		return err
	}

	id := builderIconIDPrefix + mux.Vars(r)["icon"]

	if !bdoc.IsDigest(id) {
		return builderIconNotFound()
	}

	err = b.drafts.DeleteIcon(r.Context(), actor.user, id)

	switch {
	case err == nil:
	case errors.Is(err, bapi.ErrNotFound):
		return builderIconNotFound()
	default:
		return builderWebError(err, "unable to delete the icon")
	}

	plog.Info(plog.TypeAction, "deleted builder icon", "user", actor.user, "icon", id)

	w.WriteHeader(http.StatusNoContent)

	return nil
}
