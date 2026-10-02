package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/util"
)

// maxFilesRequestBytes bounds the JSON body listing files to download or
// delete: well over 500 long paths.
const maxFilesRequestBytes = 1 << 20

// The most files, and bytes in total, one download of several experiment
// files as a zip archive may have; larger downloads are refused with 413.
const (
	maxZipFiles = 500
	maxZipBytes = 2 << 30 // 2 GiB
)

// filesRequest is the body of the requests acting on several experiment files.
type filesRequest struct {
	Paths []string `json:"paths"`
}

// fileDeleteFailure is a file DeleteExperimentFiles did not delete, and why.
type fileDeleteFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// fileErrorStatus is the HTTP status for an error acting on an experiment
// file.
func fileErrorStatus(err error) int {
	switch {
	case errors.Is(err, experiment.ErrInvalidFilePath), errors.Is(err, mm.ErrCaptureExists):
		return http.StatusBadRequest
	case errors.Is(err, experiment.ErrFileNotFound):
		return http.StatusNotFound
	case errors.Is(err, experiment.ErrDownloadTooLarge):
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}

// decodeFilesRequest reads the paths from a request acting on several
// experiment files, writing a 400 response and returning false if it cannot.
func decodeFilesRequest(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var req filesRequest

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFilesRequestBytes)).Decode(&req)
	if err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)

		return nil, false
	}

	if len(req.Paths) == 0 {
		http.Error(w, "no files given", http.StatusBadRequest)

		return nil, false
	}

	return req.Paths, true
}

// forbidFiles logs a refused request on the experiment's files and writes a
// 403 response.
func forbidFiles(w http.ResponseWriter, r *http.Request, name, action string) {
	user := middleware.UserFromContext(r.Context())
	plog.Warn(plog.TypeSecurity, action+" not allowed", "user", user, "exp", name)
	http.Error(w, "forbidden", http.StatusForbidden)
}

// DeleteExperimentFile - DELETE /experiments/{name}/files/{filename}?path=.
func DeleteExperimentFile(w http.ResponseWriter, r *http.Request) {
	var (
		name = mux.Vars(r)["name"]
		path = r.URL.Query().Get("path")
	)

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "delete", name) {
		forbidFiles(w, r, name, "deleting experiment file")

		return
	}

	err := experiment.DeleteFile(name, path)
	if err != nil {
		status := fileErrorStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "deleting experiment file", "exp", name, "file", path, "err", err)
		}

		http.Error(w, err.Error(), status)

		return
	}

	user := middleware.UserFromContext(r.Context())
	plog.Info(plog.TypeAction, "deleted file", "user", user, "exp", name, "file", path)

	w.WriteHeader(http.StatusNoContent)
}

// DeleteExperimentFiles - POST /experiments/{name}/files/delete.
//
// It deletes the files it can and lists the rest, with the reason, rather
// than failing the whole request for one of them.
func DeleteExperimentFiles(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "delete", name) {
		forbidFiles(w, r, name, "deleting experiment files")

		return
	}

	paths, ok := decodeFilesRequest(w, r)
	if !ok {
		return
	}

	deleted, refused, err := experiment.DeleteFiles(name, paths)
	if err != nil {
		status := fileErrorStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "deleting experiment files", "exp", name, "err", err)
		}

		http.Error(w, err.Error(), status)

		return
	}

	user := middleware.UserFromContext(r.Context())

	for _, path := range deleted {
		plog.Info(plog.TypeAction, "deleted file", "user", user, "exp", name, "file", path)
	}

	failed := make([]fileDeleteFailure, 0, len(refused))

	// In the order asked for, once each.
	for _, path := range paths {
		if err, ok := refused[path]; ok {
			failed = append(failed, fileDeleteFailure{Path: path, Error: err.Error()})
			delete(refused, path)
		}
	}

	if deleted == nil {
		deleted = []string{}
	}

	body, err := json.Marshal(map[string]any{"deleted": deleted, "failed": failed})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// DownloadExperimentFiles - POST /experiments/{name}/files/download.
//
// It streams a zip archive of the files, one at a time; see
// experiment.ZipFiles.
func DownloadExperimentFiles(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "get", name) {
		forbidFiles(w, r, name, "downloading experiment files")

		return
	}

	paths, ok := decodeFilesRequest(w, r)
	if !ok {
		return
	}

	var started bool

	limits := experiment.ZipLimits{MaxFiles: maxZipFiles, MaxBytes: maxZipBytes}

	zipped, err := experiment.ZipFiles(name, paths, limits, func() io.Writer {
		started = true

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", util.Attachment(name+"-files.zip"))
		w.WriteHeader(http.StatusOK)

		return w
	})
	if err != nil {
		if started {
			// The archive is partly sent; aborting the response tells the
			// client it is incomplete rather than ending it cleanly.
			plog.Error(plog.TypeSystem, "streaming experiment files zip", "exp", name, "err", err)
			panic(http.ErrAbortHandler)
		}

		status := fileErrorStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "zipping experiment files", "exp", name, "err", err)
		}

		http.Error(w, err.Error(), status)

		return
	}

	user := middleware.UserFromContext(r.Context())

	for _, path := range zipped {
		plog.Info(plog.TypeAction, "downloaded file", "user", user, "exp", name, "file", path)
	}
}
