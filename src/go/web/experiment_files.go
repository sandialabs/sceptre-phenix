package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/util"
	"phenix/web/weberror"
)

// maxFilesRequestBytes bounds the JSON body listing files to download or
// delete: well over 500 long paths.
const maxFilesRequestBytes = 1 << 20

// GetExperimentFiles - GET /experiments/{name}/files.
func GetExperimentFiles(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx          = r.Context()
		role         = middleware.RoleFromContext(ctx)
		vars         = mux.Vars(r)
		name         = vars["name"]
		query        = r.URL.Query()
		sortCol      = query.Get("sortCol")
		sortDir      = query.Get("sortDir")
		pageNum      = query.Get("pageNum")
		perPage      = query.Get("perPage")
		clientFilter = query.Get("filter")
	)

	if !role.Allowed("experiments/files", "list", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing experiment files not allowed",
			"user",
			user,
			"exp",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	page, pageSize, err := util.ParsePage(pageNum, perPage)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	files, err := experiment.Files(name, clientFilter)
	if err != nil {
		// minimega did not list every node's files; an empty or partial list
		// would read as files missing
		plog.Error(plog.TypeSystem, "getting list of files for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusBadGateway, "could not list files: "+err.Error())
	}

	if sortCol != "" && sortDir != "" {
		files.SortBy(sortCol, sortDir == "asc")
	}

	if page > 0 {
		files = files.Paginate(page, pageSize)
	}

	body, err := json.Marshal(util.WithRoot("files", files))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling file list for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	_, _ = w.Write(body)

	return nil
}

// GetExperimentFile - GET /experiments/{name}/files/{filename}.
func GetExperimentFile(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		vars  = mux.Vars(r)
		name  = vars["name"]
		file  = vars["filename"]
		query = r.URL.Query()
		path  = query.Get("path")
	)

	// the Files tab's viewer shows the file inline as text
	var (
		preview = r.Header.Get("Accept") == "text/plain"
		fields  = audit.Fields{Experiment: name, File: path, Extra: []any{"phenix.file.preview", preview}}
	)

	if !role.Allowed("experiments/files", "get", name) {
		audit.Denied(r, audit.FileDownload, "getting experiment file not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	contents, err := experiment.File(name, path)
	if err != nil {
		audit.Failure(r, audit.FileDownload, "getting experiment file failed", fields, err)

		if errors.Is(err, mm.ErrCaptureExists) {
			return weberror.New(http.StatusBadRequest, "capture still in progress")
		}

		plog.Error(
			plog.TypeSystem,
			"getting file for experiment",
			"exp",
			name,
			"file",
			path,
			"err",
			err,
		)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	if preview {
		audit.Success(r, audit.FileDownload, "downloaded file", fields)

		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(contents) //nolint:gosec // XSS via taint analysis

		return nil
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+file)
	// once per download, not per resumed range
	if rng := r.Header.Get("Range"); rng == "" || strings.HasPrefix(rng, "bytes=0-") {
		audit.Success(r, audit.FileDownload, "downloaded file", fields)
	}

	http.ServeContent(w, r, "", time.Now(), bytes.NewReader(contents))

	return nil
}
