package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"phenix/api/config"
	"phenix/store"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// configsDeleteRequest is the body of POST /configs/delete.
type configsDeleteRequest struct {
	Kind string `json:"kind"`
}

// configDeleteFailure is a config that POST /configs/delete did not delete.
type configDeleteFailure struct {
	Config string `json:"config"`
	Error  string `json:"error"`
}

// DeleteConfigs - POST /configs/delete.
//
// It deletes every config of the kind the body names, or of every kind for
// "all", that the role may delete, as config.DeleteKind does: the configs it
// can, listing the others with the reason, rather than failing the whole
// request for one of them. It leaves out the configs the role may not delete,
// and answers 403 when the role may delete none of the kind's configs.
func DeleteConfigs(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("configs", "delete") {
		audit.Denied(r, audit.ConfigDelete, "deleting configs not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	var req configsDeleteRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFilesRequestBytes)).Decode(&req); err != nil {
		return weberror.New(http.StatusBadRequest, "invalid request body: "+err.Error())
	}

	if req.Kind == "" {
		return weberror.New(http.StatusBadRequest, `give the kind of the configs to delete, or "all" for every kind`)
	}

	var listed, permitted int

	deleted, failed, err := config.DeleteKind(
		req.Kind,
		config.DeleteKindSelected(func(c store.Config) bool {
			listed++

			if !role.Allowed("configs", "delete", c.FullName()) {
				return false
			}

			permitted++

			return true
		}),
		config.DeleteKindWith(func(name string) error {
			// as DELETE /configs/{kind}/{name}, it waits for a Builder publication under way
			defer lockBuilderPublishing(name)()

			return deleteConfigAtRevision(name, 0)
		}),
	)

	switch {
	case errors.Is(err, config.ErrUnknownKind):
		return weberror.New(http.StatusBadRequest, err.Error())
	case err != nil:
		audit.Failure(r, audit.ConfigDelete, "deleting configs failed", audit.Fields{}, err)

		return weberror.NewWebError(err, "unable to list the configs to delete").SetStatus(http.StatusInternalServerError)
	}

	if listed > 0 && permitted == 0 {
		audit.Denied(r, audit.ConfigDelete, "deleting configs not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	for _, name := range deleted {
		audit.Success(r, audit.ConfigDelete, "deleted config", audit.Fields{Config: name})
	}

	failures := make([]configDeleteFailure, 0, len(failed))

	for _, f := range failed {
		audit.Failure(r, audit.ConfigDelete, "deleting config failed", audit.Fields{Config: f.Config}, f.Err)

		failures = append(failures, configDeleteFailure{Config: f.Config, Error: f.Err.Error()})
	}

	if deleted == nil {
		deleted = []string{}
	}

	body, err := json.Marshal(map[string]any{"deleted": deleted, "failed": failures})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
