package web

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
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

// maxViewBytes is the largest experiment file the Files tab's viewer is sent;
// a larger one is refused with 413, to be downloaded instead.
const maxViewBytes = 10 << 20 // 10 MiB

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

// forbidFiles audits a refused action a on the experiment's files, described
// by msg, and writes a 403 response.
func forbidFiles(w http.ResponseWriter, r *http.Request, name string, a audit.Action, msg string) {
	audit.Denied(r, a, msg+" not allowed", audit.Fields{Experiment: name})
	http.Error(w, "forbidden", http.StatusForbidden)
}

// GetExperimentFileChecksum - GET /experiments/{name}/files/{filename}/checksum?path=.
//
// It answers with the file's SHA-256, computed in the background: 202 until
// it is known, then 200 (see util.ServeChecksum).
func GetExperimentFileChecksum(w http.ResponseWriter, r *http.Request) {
	var (
		name = mux.Vars(r)["name"]
		path = r.URL.Query().Get("path")
	)

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "get", name) {
		audit.Denied(r, audit.FileDownload, "getting experiment file checksum not allowed", audit.Fields{
			Experiment: name, File: path,
		})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	local, err := experiment.LocalFile(name, path)
	if err == nil {
		err = util.ServeChecksum(w, local, path)
	}

	if err == nil {
		return
	}

	status := fileErrorStatus(err)

	switch {
	case errors.Is(err, util.ErrChangedWhileHashing):
		status = http.StatusConflict
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, util.ErrNotRegular):
		status = http.StatusNotFound
	case status == http.StatusInternalServerError:
		plog.Error(plog.TypeSystem, "getting experiment file checksum", "exp", name, "file", path, "err", err)
	}

	http.Error(w, err.Error(), status)
}

// DeleteExperimentFile - DELETE /experiments/{name}/files/{filename}?path=.
func DeleteExperimentFile(w http.ResponseWriter, r *http.Request) {
	var (
		name = mux.Vars(r)["name"]
		path = r.URL.Query().Get("path")
	)

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "delete", name) {
		forbidFiles(w, r, name, audit.FileDelete, "deleting experiment file")

		return
	}

	deleted := audit.Fields{Experiment: name, File: path}

	err := experiment.DeleteFile(name, path)
	if err != nil {
		audit.Failure(r, audit.FileDelete, "deleting experiment file failed", deleted, err)

		status := fileErrorStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "deleting experiment file", "exp", name, "file", path, "err", err)
		}

		http.Error(w, err.Error(), status)

		return
	}

	revokeExperimentFileGrants(name, path)

	audit.Success(r, audit.FileDelete, "deleted file", deleted)

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
		forbidFiles(w, r, name, audit.FileDelete, "deleting experiment files")

		return
	}

	paths, ok := decodeFilesRequest(w, r)
	if !ok {
		return
	}

	deleted, refused, err := experiment.DeleteFiles(name, paths)
	if err != nil {
		audit.Failure(r, audit.FileDelete, "deleting experiment files failed", audit.Fields{
			Experiment: name, Extra: []any{"phenix.file.count", len(paths)},
		}, err)

		status := fileErrorStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "deleting experiment files", "exp", name, "err", err)
		}

		http.Error(w, err.Error(), status)

		return
	}

	for _, path := range deleted {
		revokeExperimentFileGrants(name, path)
		audit.Success(r, audit.FileDelete, "deleted file", audit.Fields{Experiment: name, File: path})
	}

	failed := make([]fileDeleteFailure, 0, len(refused))

	// In the order asked for, once each.
	for _, path := range paths {
		if err, ok := refused[path]; ok {
			audit.Failure(r, audit.FileDelete, "deleting experiment file failed", audit.Fields{
				Experiment: name, File: path,
			}, err)

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
		forbidFiles(w, r, name, audit.FileDownload, "downloading experiment files")

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
		// built as it is sent, so it cannot be resumed or cached
		w.Header().Set("Accept-Ranges", "none")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)

		return w
	})

	// Each file sent, even when a later one fails.
	for _, path := range zipped {
		audit.Success(r, audit.FileDownload, "downloaded file", audit.Fields{
			Experiment: name, File: path, Extra: []any{"phenix.file.capture", isCaptureFile(path)},
		})
	}

	if err != nil {
		audit.Failure(r, audit.FileDownload, "downloading experiment files failed", audit.Fields{
			Experiment: name, Extra: []any{"phenix.file.count", len(paths)},
		}, err)

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
	}
}
