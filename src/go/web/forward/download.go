package forward

import (
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/gorilla/mux"

	"phenix/util/plog"
	"phenix/web/util"
	"phenix/web/weberror"
)

// GetTunneler - GET /downloads/tunneler/{name}.
func GetTunneler(w http.ResponseWriter, r *http.Request) error {
	name := mux.Vars(r)["name"]

	// a name directly in the downloads directory, and not a hidden file
	if name == "" || filepath.Base(name) != name || name[0] == '.' {
		return weberror.New(http.StatusBadRequest, "invalid tunneler name")
	}

	path := filepath.Join("downloads/tunneler", name)

	_, err := util.ServeDownload(w, r, util.Download{
		Path:        path,
		Name:        name,
		ContentType: "application/octet-stream",
		MaxSize:     0,
	})

	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, util.ErrNotRegular):
		return weberror.New(http.StatusNotFound, "tunneler "+name+" is not installed on this server")
	default:
		plog.Error(plog.TypeSystem, "opening tunneler for download", "name", name, "err", err)

		return weberror.New(http.StatusInternalServerError, "unable to open tunneler "+name)
	}

	return nil
}
