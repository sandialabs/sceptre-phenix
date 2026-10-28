package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

const (
	// builderLayerSetsPath is the path of the layer set library. The routes
	// of one set, and of the default set of the server, are below it.
	builderLayerSetsPath = builderRoutePrefix + "layersets"

	// builderLayerSetSchemaPath is the path of the schema route of the layer
	// set file format.
	builderLayerSetSchemaPath = "/schemas/builder/layersets/v1"

	// builderLayerSetBytes bounds the body of a request that adds or
	// replaces a layer set (1 MiB): a set at its largest, and its JSON.
	builderLayerSetBytes = 1 << 20

	// builderLayerSetDefaultBytes bounds the body of a request that chooses
	// the default set (4 KiB).
	builderLayerSetDefaultBytes = 4 << 10

	// builderKindLayerSet is the kind a refused layer set request names.
	builderKindLayerSet = "layer set"
)

// builderLayerSetResponse is one layer set of the library. Builtin is true
// for a set every server has, which nobody may change or delete: it has
// version 1 and no times or users. MatchConflict is never empty. CreatedBy
// and UpdatedBy name who added the set and who last changed its content.
type builderLayerSetResponse struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	IconKey       string       `json:"iconKey"`
	Icon          string       `json:"icon"`
	MatchConflict string       `json:"matchConflict"`
	LayerOutlines bool         `json:"layerOutlines"`
	Layers        []bdoc.Layer `json:"layers"`
	Builtin       bool         `json:"builtin"`
	Version       int64        `json:"version"`
	ETag          string       `json:"etag"`
	Created       time.Time    `json:"created,omitzero"`
	Updated       time.Time    `json:"updated,omitzero"`
	CreatedBy     string       `json:"createdBy,omitempty"`
	UpdatedBy     string       `json:"updatedBy,omitempty"`
}

// builderLayerSetListResponse is the layer set library: the built-in sets
// first, then the others by name. DefaultID is the default set of the server
// for new diagrams, or null, and DefaultETag the entity tag that a change of
// that choice must name. Damaged counts the records the server cannot read,
// which the list leaves out.
type builderLayerSetListResponse struct {
	LayerSets   []builderLayerSetResponse `json:"layerSets"`
	DefaultID   *string                   `json:"defaultId"`
	DefaultETag string                    `json:"defaultEtag"`
	Damaged     int                       `json:"damaged"`
	Limits      builderLayerSetLimits     `json:"limits"`
}

// builderLayerSetLimits is what the library and one set may hold.
type builderLayerSetLimits struct {
	LayerSets        int `json:"layerSets"`
	Layers           int `json:"layers"`
	NameBytes        int `json:"nameBytes"`
	DescriptionBytes int `json:"descriptionBytes"`
	LabelBytes       int `json:"labelBytes"`
	Matchers         int `json:"matchers"`
	MatcherPathBytes int `json:"matcherPathBytes"`
	PatternBytes     int `json:"patternBytes"`
}

// builderLayerSetDefault is the default layer set of the server: the ID of a
// set, or null for none. A request with null or "" removes the choice.
type builderLayerSetDefault struct {
	ID *string `json:"id"`
}

// builderLayerSetDefaultResponse is the default layer set of the server,
// with the entity tag that a change of the choice must name.
type builderLayerSetDefaultResponse struct {
	ID   *string `json:"id"`
	ETag string  `json:"etag"`
}

// newBuilderLayerSetDefaultResponse returns the choice as the caller is
// answered with it.
func newBuilderLayerSetDefaultResponse(chosen bapi.LayerSetDefault) builderLayerSetDefaultResponse {
	response := builderLayerSetDefaultResponse{ID: nil, ETag: chosen.ETag}
	if chosen.ID != "" {
		response.ID = &chosen.ID
	}

	return response
}

// newBuilderLayerSetResponse returns a set as the caller is answered with
// it.
func newBuilderLayerSetResponse(set *bapi.LibraryLayerSet) builderLayerSetResponse {
	conflict := set.MatchConflict
	if conflict == "" {
		conflict = bdoc.MatchConflictLowest
	}

	layers := set.Layers
	if layers == nil {
		layers = []bdoc.Layer{}
	}

	return builderLayerSetResponse{
		ID:            set.ID,
		Name:          set.Name,
		Description:   set.Description,
		IconKey:       set.IconKey,
		Icon:          set.Icon,
		MatchConflict: conflict,
		LayerOutlines: set.LayerOutlines,
		Layers:        layers,
		Builtin:       set.Builtin(),
		Version:       set.Version,
		ETag:          set.ETag(),
		Created:       set.Created,
		Updated:       set.Updated,
		CreatedBy:     set.CreatedBy,
		UpdatedBy:     set.UpdatedBy,
	}
}

// builderLayerSetError maps a failed request of the layer set library to its status, code and issues.
func builderLayerSetError(w http.ResponseWriter, err error, id string) *weberror.WebError {
	var (
		stale *bapi.LayerSetStaleError
		full  *bapi.LayerSetLimitError
	)

	switch {
	case errors.As(err, &stale):
		w.Header().Set("ETag", stale.ETag)

		return weberror.NewWebError(nil, "layer set %s changed after you last read it", stale.ID).
			SetStatus(http.StatusPreconditionFailed).WithCode(string(bdoc.CodeLayerSetStale))
	case errors.Is(err, bapi.ErrForbidden):
		return weberror.NewWebError(nil, "you cannot change or delete a built-in layer set. Duplicate it and change the copy").
			SetStatus(http.StatusForbidden).WithCode(string(bdoc.CodeLayerSetReadOnly))
	case errors.As(err, &full):
		return weberror.NewWebError(nil, "the layer set library holds %d sets, the most it can hold", full.Limit).
			SetStatus(http.StatusRequestEntityTooLarge).WithCode(string(bdoc.CodeLayerSetLibraryFull))
	case errors.Is(err, bdoc.ErrInvalidLayerSet):
		return weberror.NewWebError(err, "the layer set is not valid").SetStatus(http.StatusUnprocessableEntity)
	case errors.Is(err, bapi.ErrNotFound):
		return builderNotFound(builderKindLayerSet, id)
	case errors.Is(err, bapi.ErrCorrupt):
		return weberror.NewWebError(err, "this version of phenix cannot read layer set %s", id).
			SetStatus(http.StatusConflict)
	case errors.Is(err, bapi.ErrBusy):
		w.Header().Set("Retry-After", "1")
	}

	return builderWebError(err, "unable to complete the layer set request")
}

// builderLayerSetLimitsNow returns the limits of the layer set library.
func builderLayerSetLimitsNow() builderLayerSetLimits {
	return builderLayerSetLimits{
		LayerSets:        bapi.MaxLayerSets,
		Layers:           bdoc.MaxLayers,
		NameBytes:        bdoc.MaxLayerSetNameBytes,
		DescriptionBytes: bdoc.MaxLayerSetDescriptionBytes,
		LabelBytes:       bdoc.MaxLayerLabelBytes,
		Matchers:         bdoc.MaxLayerMatchers,
		MatcherPathBytes: bdoc.MaxMatcherPathBytes,
		PatternBytes:     bdoc.MaxMatcherPatternBytes,
	}
}

// layerSetRoutes registers the routes of the layer set library. The route
// of the default set comes before the route of one set, which would match
// its path too.
func (b *builderAPI) layerSetRoutes(router *mux.Router) {
	const setPath = builderLayerSetsPath + "/{layerset}"

	router.Handle(builderLayerSetsPath, builderHandler(b.listLayerSets)).
		Methods("GET")
	router.Handle(builderLayerSetsPath, builderHandler(b.createLayerSet)).
		Methods("POST")
	router.Handle(builderLayerSetsPath+"/default", builderHandler(b.getDefaultLayerSet)).
		Methods("GET")
	router.Handle(builderLayerSetsPath+"/default", builderHandler(b.putDefaultLayerSet)).
		Methods("PUT")
	router.Handle(setPath, builderHandler(b.getLayerSet)).
		Methods("GET")
	router.Handle(setPath, builderHandler(b.putLayerSet)).
		Methods("PUT")
	router.Handle(setPath, builderHandler(b.deleteLayerSet)).
		Methods("DELETE")
}

// listLayerSets - GET /builder/layersets.
//
// The layer set library belongs to the server. Every Builder user sees every
// set, and the default set of the server.
func (b *builderAPI) listLayerSets(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderListLayerSets")

	if _, err := builderAuthorize(r, "listing Builder layer sets"); err != nil {
		return err
	}

	sets, damaged, err := b.drafts.ListLayerSets(r.Context())
	if err != nil {
		return builderWebError(err, "unable to list the layer sets")
	}

	chosen, err := b.drafts.DefaultLayerSet(r.Context())
	if err != nil {
		return builderWebError(err, "unable to read the default layer set")
	}

	response := builderLayerSetListResponse{
		LayerSets:   make([]builderLayerSetResponse, 0, len(sets)),
		DefaultID:   newBuilderLayerSetDefaultResponse(chosen).ID,
		DefaultETag: chosen.ETag,
		Damaged:     damaged,
		Limits:      builderLayerSetLimitsNow(),
	}

	for i := range sets {
		response.LayerSets = append(response.LayerSets, newBuilderLayerSetResponse(&sets[i]))
	}

	return builderWriteJSON(w, http.StatusOK, "", response)
}

// getLayerSet - GET /builder/layersets/{layerset}.
func (b *builderAPI) getLayerSet(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetLayerSet")

	if _, err := builderAuthorize(r, "getting a Builder layer set"); err != nil {
		return err
	}

	id := mux.Vars(r)["layerset"]

	set, err := b.drafts.GetLayerSet(r.Context(), id)
	if err != nil {
		return builderLayerSetError(w, err, id)
	}

	return builderWriteJSON(w, http.StatusOK, set.ETag(), newBuilderLayerSetResponse(set))
}

// createLayerSet - POST /builder/layersets.
//
// Adds a set to the library under a new ID. Any Builder user may add one.
// The answer is 201 with the set and its entity tag.
func (b *builderAPI) createLayerSet(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCreateLayerSet")

	actor, err := builderAuthorize(r, "adding a Builder layer set")
	if err != nil {
		return err
	}

	var request bdoc.LayerSet

	if err := builderDecodeLimit(w, r, &request, builderLayerSetBytes); err != nil {
		return err
	}

	set, err := b.drafts.CreateLayerSet(r.Context(), actor.user, request)
	if err != nil {
		return builderLayerSetError(w, err, "")
	}

	plog.Info(plog.TypeAction, "added builder layer set", "user", actor.user, "layerset", set.ID)

	return builderWriteJSON(w, http.StatusCreated, set.ETag(), newBuilderLayerSetResponse(set))
}

// putLayerSet - PUT /builder/layersets/{layerset}.
//
// Replaces the content of a set. Any Builder user may change any set but a
// built-in one. If-Match must name the current version.
func (b *builderAPI) putLayerSet(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPutLayerSet")

	actor, err := builderAuthorize(r, "changing a Builder layer set")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	var request bdoc.LayerSet

	if err := builderDecodeLimit(w, r, &request, builderLayerSetBytes); err != nil {
		return err
	}

	id := mux.Vars(r)["layerset"]

	set, err := b.drafts.UpdateLayerSet(r.Context(), actor.user, id, ifMatch, request)
	if err != nil {
		return builderLayerSetError(w, err, id)
	}

	plog.Info(plog.TypeAction, "changed builder layer set", "user", actor.user, "layerset", id, "version", set.Version)

	return builderWriteJSON(w, http.StatusOK, set.ETag(), newBuilderLayerSetResponse(set))
}

// deleteLayerSet - DELETE /builder/layersets/{layerset}.
//
// Deletes a set. Any Builder user may delete any set but a built-in one.
// If-Match must name the current version. Diagrams keep their copies of its
// layers.
func (b *builderAPI) deleteLayerSet(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderDeleteLayerSet")

	actor, err := builderAuthorize(r, "deleting a Builder layer set")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	id := mux.Vars(r)["layerset"]

	if err := b.drafts.DeleteLayerSet(r.Context(), id, ifMatch); err != nil {
		return builderLayerSetError(w, err, id)
	}

	plog.Info(plog.TypeAction, "deleted builder layer set", "user", actor.user, "layerset", id)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// getDefaultLayerSet - GET /builder/layersets/default.
//
// The set new diagrams of every user start with, or null, and the entity tag
// of the choice. A choice of a set that was deleted is null.
func (b *builderAPI) getDefaultLayerSet(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderGetDefaultLayerSet")

	if _, err := builderAuthorize(r, "getting the default Builder layer set"); err != nil {
		return err
	}

	chosen, err := b.drafts.DefaultLayerSet(r.Context())
	if err != nil {
		return builderWebError(err, "unable to read the default layer set")
	}

	return builderWriteJSON(w, http.StatusOK, chosen.ETag, newBuilderLayerSetDefaultResponse(chosen))
}

// putDefaultLayerSet - PUT /builder/layersets/default.
//
// Chooses the set that new diagrams of every user copy, or with null removes
// the choice. Any Builder user may choose it. If-Match must name the entity
// tag of the current choice. A set that does not exist gets 422.
func (b *builderAPI) putDefaultLayerSet(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPutDefaultLayerSet")

	actor, err := builderAuthorize(r, "choosing the default Builder layer set")
	if err != nil {
		return err
	}

	ifMatch, err := builderIfMatch(r)
	if err != nil {
		return err
	}

	var request builderLayerSetDefault

	if err := builderDecodeLimit(w, r, &request, builderLayerSetDefaultBytes); err != nil {
		return err
	}

	id := ""
	if request.ID != nil {
		id = *request.ID
	}

	chosen, err := b.drafts.SetDefaultLayerSet(r.Context(), id, ifMatch)

	var stale *bapi.LayerSetStaleError

	switch {
	case errors.As(err, &stale):
		w.Header().Set("ETag", stale.ETag)

		return weberror.NewWebError(nil, "the default layer set changed after you last read it").
			SetStatus(http.StatusPreconditionFailed).WithCode(string(bdoc.CodeLayerSetStale))
	case errors.Is(err, bapi.ErrNotFound):
		return weberror.NewWebError(nil, "layer set %s does not exist", id).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodeLayerSetDefaultUnknown))
	case err != nil:
		return builderLayerSetError(w, err, id)
	}

	plog.Info(plog.TypeAction, "chose default builder layer set", "user", actor.user, "layerset", id)

	return builderWriteJSON(w, http.StatusOK, chosen.ETag, newBuilderLayerSetDefaultResponse(chosen))
}
