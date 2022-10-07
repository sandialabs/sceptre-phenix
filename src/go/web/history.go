package web

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"phenix/store"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"

	log "github.com/activeshadow/libminimega/minilog"
)

// POST /history
func GetHistory(w http.ResponseWriter, r *http.Request) error {
	log.Debug("GetHistory HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("history", "get") {
		err := weberror.NewWebError(nil, "getting history not allowed for %s", ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		err := weberror.NewWebError(err, "unable to parse request")
		return err.SetStatus(http.StatusInternalServerError)
	}

	var event store.Event

	if err := json.Unmarshal(body, &event); err != nil {
		return weberror.NewWebError(err, "invalid history event filter provided")
	}

	events, err := store.GetEventsBy(event)
	if err != nil {
		err := weberror.NewWebError(err, "unable to get matching history events")
		return err.SetStatus(http.StatusInternalServerError)
	}

	// sort in descending order, so most recent event is first
	events.SortByTimestamp(false)

	body, err = json.Marshal(util.WithRoot("history", events))
	if err != nil {
		err := weberror.NewWebError(err, "unable to process history events")
		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)

	return nil
}
