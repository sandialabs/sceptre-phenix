package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	// (128 KiB): the base64 of the largest upload, its name, and its JSON.
	builderIconRequestBytes = 128 << 10

	// builderIconRenameBytes bounds the body of a request renaming an icon
	// (4 KiB): a name and its JSON.
	builderIconRenameBytes = 4 << 10
)

// builderIconUpload adds an image to the icon library under a name the
// caller chose. Data is a PNG in standard base64 with padding. The server
// never reads a file name, extension or declared type.
type builderIconUpload struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// builderIconRename gives an icon a new name. The old name keeps naming it,
// as an alias.
type builderIconRename struct {
	Name string `json:"name"`
}

// builderIconSummary is one icon of the icon library without its image. Owner is empty for a server icon.
// Any Builder user may change an uploaded icon, and no user may change a server icon (see [bapi.MayChangeIcon]).
type builderIconSummary struct {
	Name      string    `json:"name"`
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Bytes     int       `json:"bytes"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	CreatedBy string    `json:"createdBy,omitempty"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	Aliases   []string  `json:"aliases"`
	CanRename bool      `json:"canRename"`
	CanDelete bool      `json:"canDelete"`
	Revision  int64     `json:"revision"`
	ETag      string    `json:"etag"`
}

// builderIconResponse is one icon of the icon library with its image. Data
// is the stored PNG as base64. The server sends the image only as text inside
// JSON, never as an image that a browser could open.
type builderIconResponse struct {
	builderIconSummary

	Data string `json:"data"`
}

// builderIconListResponse is the icon library, without the images, and the
// most icons it holds. ETag is the entity tag of the list, which a request
// names in If-None-Match to get 304 while the list is the same.
type builderIconListResponse struct {
	Icons    []builderIconSummary `json:"icons"`
	MaxIcons int                  `json:"maxIcons"`
	ETag     string               `json:"etag"`
}

// newBuilderIconSummary returns an icon without its image, as the list
// gives it.
func newBuilderIconSummary(icon *bapi.LibraryIcon) builderIconSummary {
	changeable := bapi.MayChangeIcon(icon)

	return builderIconSummary{
		Name:      icon.Name,
		ID:        icon.ID,
		Owner:     icon.Owner,
		Width:     icon.Width,
		Height:    icon.Height,
		Bytes:     icon.Bytes,
		Created:   icon.Created,
		Updated:   icon.Updated,
		CreatedBy: icon.CreatedBy,
		UpdatedBy: icon.UpdatedBy,
		Aliases:   append([]string{}, icon.Aliases...),
		CanRename: changeable,
		CanDelete: changeable,
		Revision:  icon.Revision,
		ETag:      icon.ETag(),
	}
}

// newBuilderIconResponse returns an icon with its image, as the caller is
// answered with it.
func newBuilderIconResponse(icon *bapi.LibraryIcon) builderIconResponse {
	return builderIconResponse{builderIconSummary: newBuilderIconSummary(icon), Data: icon.Data}
}

// builderIconListETag returns the entity tag of a list of the library: a
// hash of its JSON without the tag.
func builderIconListETag(list builderIconListResponse) (string, error) {
	list.ETag = ""

	body, err := json.Marshal(list)
	if err != nil {
		return "", weberror.NewWebError(err, "unable to encode the icon library").
			SetStatus(http.StatusInternalServerError)
	}

	sum := sha256.Sum256(body)

	return `"` + hex.EncodeToString(sum[:16]) + `"`, nil
}

// builderNoneMatch reports whether the If-None-Match header of r names
// etag, by the weak comparison of RFC 9110: a weak tag matches the strong tag
// of the same value, and "*" matches any tag.
func builderNoneMatch(r *http.Request, etag string) bool {
	for tag := range strings.SplitSeq(r.Header.Get("If-None-Match"), ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
			return true
		}
	}

	return false
}

// builderNotModified answers 304 with the entity tag of what the caller
// already has, and no body.
func builderNotModified(w http.ResponseWriter, etag string) error {
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusNotModified)

	return nil
}

// builderIconNotFound is the 404 of a name that names no icon. It never
// repeats the path it was asked for.
func builderIconNotFound() *weberror.WebError {
	return weberror.NewWebError(nil, "phenix cannot find the icon").SetStatus(http.StatusNotFound)
}

// builderIconError maps a refusal of the icon library to its answer. The
// answer tells, in the words of the service, what is wrong with the upload,
// the name or the library. These words name no stored record and repeat
// nothing of the image. Any other failure gets message.
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
		return builderWebError(err, "the icon changed while this request ran. Try again")
	}

	return builderWebError(err, "%s", message)
}

// listIcons - GET /builder/icons.
// The icon library belongs to the server, and every Builder user sees every icon. The list has no images.
func (b *builderAPI) listIcons(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListIcons")

	if _, err := builderAuthorize(r, "listing Builder icons"); err != nil {
		return err
	}

	icons, err := b.drafts.ListIcons(r.Context())
	if err != nil {
		return builderWebError(err, "unable to list the icons")
	}

	response := builderIconListResponse{
		Icons:    make([]builderIconSummary, 0, len(icons)),
		MaxIcons: bapi.MaxIcons,
		ETag:     "",
	}

	for i := range icons {
		response.Icons = append(response.Icons, newBuilderIconSummary(&icons[i]))
	}

	if response.ETag, err = builderIconListETag(response); err != nil {
		return err
	}

	if builderNoneMatch(r, response.ETag) {
		return builderNotModified(w, response.ETag)
	}

	return builderWriteJSON(w, http.StatusOK, response.ETag, response)
}

// getIcon - GET /builder/icons/{icon}.
//
// The path names the icon by its name or by one of its aliases, ignoring
// case. The answer carries the current name of the icon and its image. A
// request whose If-None-Match names the icon's entity tag gets 304.
func (b *builderAPI) getIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetIcon")

	if _, err := builderAuthorize(r, "getting a Builder icon"); err != nil {
		return err
	}

	icon, err := b.drafts.GetIcon(r.Context(), mux.Vars(r)["icon"])
	if err != nil {
		return builderIconError(err, "unable to read the icon")
	}

	if builderNoneMatch(r, icon.ETag()) {
		return builderNotModified(w, icon.ETag())
	}

	return builderWriteJSON(w, http.StatusOK, icon.ETag(), newBuilderIconResponse(icon))
}

// createIcon - POST /builder/icons.
// It answers 201 for a new icon, 200 for the same bytes, and 409 for other bytes. The caller must use the answer data.
func (b *builderAPI) createIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateIcon")

	actor, err := builderAuthorize(r, "adding a Builder icon")
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
		return weberror.NewWebError(nil, "the request needs icon data").SetStatus(http.StatusBadRequest)
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

	return builderWriteJSON(w, status, icon.ETag(), newBuilderIconResponse(icon))
}

// renameIcon - PUT /builder/icons/{icon}.
//
// Gives the icon that the path names a new name. The old name stays as an
// alias of the icon. Any Builder user may rename an uploaded icon. An icon
// that the server added from its template files gets 403.
func (b *builderAPI) renameIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderRenameIcon")

	const action = "renaming a Builder icon"

	actor, err := builderAuthorize(r, action)
	if err != nil {
		return err
	}

	var request builderIconRename

	if err := builderDecodeLimit(w, r, &request, builderIconRenameBytes); err != nil {
		return err
	}

	name := mux.Vars(r)["icon"]

	icon, err := b.drafts.RenameIcon(r.Context(), actor.user, name, request.Name)
	if errors.Is(err, bapi.ErrForbidden) {
		return builderForbidden(actor, action+" the server added")
	}

	if err != nil {
		return builderIconError(err, "unable to rename the icon")
	}

	plog.Info(
		plog.TypeAction, "renamed builder icon",
		"user", actor.user, "icon", name, "name", icon.Name, "owner", icon.Owner,
	)

	return builderWriteJSON(w, http.StatusOK, icon.ETag(), newBuilderIconResponse(icon))
}

// deleteIcon - DELETE /builder/icons/{icon}.
// It deletes the icon with all its aliases. A server icon gets 403. If-Match must name the icon's entity tag.
func (b *builderAPI) deleteIcon(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteIcon")

	const action = "deleting a Builder icon"

	actor, err := builderAuthorize(r, action)
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	name := mux.Vars(r)["icon"]

	err = b.drafts.DeleteIcon(r.Context(), actor.user, name, ifMatch)

	var stale *bapi.IconStaleError

	switch {
	case err == nil:
	case errors.Is(err, bapi.ErrForbidden):
		return builderForbidden(actor, action+" the server added")
	case errors.As(err, &stale):
		w.Header().Set("ETag", stale.ETag)

		return weberror.NewWebError(nil, "icon %s changed after you last read it", stale.Name).
			SetStatus(http.StatusPreconditionFailed).WithCode(string(bdoc.CodeRequestStale))
	default:
		return builderIconError(err, "unable to delete the icon")
	}

	plog.Info(plog.TypeAction, "deleted builder icon", "user", actor.user, "icon", name)

	w.WriteHeader(http.StatusNoContent)

	return nil
}
