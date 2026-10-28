package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/job"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
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
// experiment files, or returns its 400 if it cannot.
func decodeFilesRequest(w http.ResponseWriter, r *http.Request) ([]string, error) {
	var req filesRequest

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFilesRequestBytes)).Decode(&req)
	if err != nil {
		return nil, weberror.New(http.StatusBadRequest, "invalid request body: "+err.Error())
	}

	if len(req.Paths) == 0 {
		return nil, weberror.New(http.StatusBadRequest, "no files given")
	}

	return req.Paths, nil
}

// forbidFiles audits a refused action a on the experiment's files, described
// by msg, and returns its 403.
func forbidFiles(r *http.Request, name string, a audit.Action, msg string) error {
	audit.Denied(r, a, msg+" not allowed", audit.Fields{Experiment: name})

	return weberror.New(http.StatusForbidden, "forbidden")
}

// GetExperimentFileChecksum - GET /experiments/{name}/files/{filename}/checksum?path=.
//
// It answers with the file's SHA-256, computed in the background: 202 until
// it is known, then 200 (see util.ServeChecksum).
func GetExperimentFileChecksum(w http.ResponseWriter, r *http.Request) error {
	var (
		name = mux.Vars(r)["name"]
		path = r.URL.Query().Get("path")
	)

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "get", name) {
		audit.Denied(r, audit.FileDownload, "getting experiment file checksum not allowed", audit.Fields{
			Experiment: name, File: path,
		})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	local, err := experiment.LocalFile(name, path)
	if err == nil {
		err = util.ServeChecksum(w, local, path)
	}

	if err == nil {
		return nil
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

	return weberror.New(status, err.Error())
}

// DeleteExperimentFile - DELETE /experiments/{name}/files/{filename}?path=.
func DeleteExperimentFile(w http.ResponseWriter, r *http.Request) error {
	var (
		name = mux.Vars(r)["name"]
		path = r.URL.Query().Get("path")
	)

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "delete", name) {
		return forbidFiles(r, name, audit.FileDelete, "deleting experiment file")
	}

	deleted := audit.Fields{Experiment: name, File: path}

	err := experiment.DeleteFile(name, path)
	if err != nil {
		audit.Failure(r, audit.FileDelete, "deleting experiment file failed", deleted, err)

		status := fileErrorStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "deleting experiment file", "exp", name, "file", path, "err", err)
		}

		return weberror.New(status, err.Error())
	}

	revokeExperimentFileGrants(name, path)

	audit.Success(r, audit.FileDelete, "deleted file", deleted)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// DeleteExperimentFiles - POST /experiments/{name}/files/delete.
//
// It deletes the files it can and lists the rest, with the reason, rather
// than failing the whole request for one of them.
func DeleteExperimentFiles(w http.ResponseWriter, r *http.Request) error {
	name := mux.Vars(r)["name"]

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "delete", name) {
		return forbidFiles(r, name, audit.FileDelete, "deleting experiment files")
	}

	paths, err := decodeFilesRequest(w, r)
	if err != nil {
		return err
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

		return weberror.New(status, err.Error())
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
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// DownloadExperimentFiles - POST /experiments/{name}/files/download.
//
// It streams a zip archive of the files, one at a time; see
// experiment.ZipFiles.
func DownloadExperimentFiles(w http.ResponseWriter, r *http.Request) error {
	name := mux.Vars(r)["name"]

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "get", name) {
		return forbidFiles(r, name, audit.FileDownload, "downloading experiment files")
	}

	paths, err := decodeFilesRequest(w, r)
	if err != nil {
		return err
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

		return weberror.New(status, err.Error())
	}

	return nil
}

// archiveKind is the kind of the background jobs that archive experiment
// files.
const archiveKind = "files.archive"

// mayDownloadArchive reports whether the role may still get the files of the
// archive job's experiment, as it had to when it started the job.
func mayDownloadArchive(role rbac.Role, j job.Job) bool {
	return role.Allowed("experiments/files", "get", j.Experiment)
}

// ArchiveExperimentFiles - POST /experiments/{name}/files/archive.
//
// It checks the files as DownloadExperimentFiles does, then builds their zip
// archive in a background job, for GET /jobs/{id}/artifact. A file that fails
// is left out and listed in the archive's MISSING.txt, and the archive is
// named <exp>-files-incomplete.zip. The files are audited as downloaded when
// the archive is (see auditArchiveDownload), and the job when it fails (see
// auditArchiveJob).
func ArchiveExperimentFiles(w http.ResponseWriter, r *http.Request) error {
	name := mux.Vars(r)["name"]

	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("experiments/files", "get", name) {
		return forbidFiles(r, name, audit.FileDownload, "archiving experiment files")
	}

	paths, err := decodeFilesRequest(w, r)
	if err != nil {
		return err
	}

	plan, err := experiment.PlanZip(name, paths, experiment.ZipLimits{MaxFiles: maxZipFiles, MaxBytes: maxZipBytes})
	if err != nil {
		status := fileErrorStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "archiving experiment files", "exp", name, "err", err)
		}

		return weberror.New(status, err.Error())
	}

	return startJob(w, r, job.Spec{
		Kind:       archiveKind,
		Owner:      "",
		Experiment: name,
		Title:      fmt.Sprintf("Archive %s of %s", countOf(len(plan.Paths), "file"), name),
		Params:     nil,
		Items:      plan.Paths,
		BytesTotal: plan.Total,
		Cancelable: true,
		Run: func(ctx context.Context, run *job.Run) error {
			return archiveFiles(ctx, run, plan)
		},
	})
}

// archiveFiles writes the plan's archive to <job dir>/<exp>-files.zip.partial,
// then renames it to <exp>-files.zip, or <exp>-files-incomplete.zip when a
// file was left out or cut short, and makes it the job's artifact. When the job is
// canceled, or no file could be archived, the partial file is removed.
func archiveFiles(ctx context.Context, run *job.Run, plan experiment.ZipPlan) error {
	dir, err := run.Dir()
	if err != nil {
		return err //nolint:wrapcheck // says what failed
	}

	partial := filepath.Join(dir, plan.Name+"-files.zip.partial")

	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating archive: %w", err)
	}

	var truncated bool

	archived, err := plan.Write(ctx, f, experiment.ZipHooks{
		OnStart: run.ItemStarted,
		OnFile: func(path string, err error) {
			// a truncated file's bytes are in the archive, so it is audited as sent
			if err != nil && !errors.Is(err, experiment.ErrFileTruncated) {
				run.ItemFailed(path, err)

				return
			}

			truncated = truncated || err != nil
			run.ItemSucceeded(path, nil)
		},
		OnBytes:    run.AddBytes,
		SkipFailed: true,
	})

	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("writing archive: %w", closeErr)
	}

	if err == nil && len(archived) == 0 {
		err = errors.New("no file could be archived")
	}

	if err != nil {
		_ = os.Remove(partial)

		return err
	}

	var (
		incomplete = len(archived) < len(plan.Paths) || truncated
		final      = plan.Name + "-files.zip"
	)

	if incomplete {
		final = plan.Name + "-files-incomplete.zip"
	}

	if err := os.Rename(partial, filepath.Join(dir, final)); err != nil {
		_ = os.Remove(partial)

		return fmt.Errorf("finishing archive: %w", err)
	}

	return run.SetArtifact(filepath.Join(dir, final), final, incomplete) //nolint:wrapcheck // says what failed
}

// auditArchiveJob audits, as the user who started it, an archive job that
// finished without an archive: no file can be downloaded from it.
func auditArchiveJob(j job.Job) {
	if j.Artifact != nil {
		return
	}

	audit.BackgroundFailure(j.Owner, audit.FileDownload, "downloading experiment files failed", audit.Fields{
		Experiment: j.Experiment, Extra: []any{"phenix.file.count", len(j.Items), "phenix.job.id", j.ID},
	}, jobError(j))
}

// auditArchiveDownload audits, as DownloadExperimentFiles does, each file in
// an archive job's artifact when a download of it begins: the files archived,
// not those MISSING.txt lists.
func auditArchiveDownload(r *http.Request, j job.Job) {
	extra := []any{"phenix.job.id", j.ID}

	if rng := r.Header.Get("Range"); rng != "" {
		extra = append(extra, "http.request.range", rng)
	}

	for _, item := range j.Items {
		if item.Status != job.ItemSucceeded {
			continue
		}

		audit.Success(r, audit.FileDownload, "downloaded file", audit.Fields{
			Experiment: j.Experiment, File: item.Name,
			Extra: append([]any{"phenix.file.capture", isCaptureFile(item.Name)}, extra...),
		})
	}
}

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

	files, observed, err := experiment.FilesObserved(ctx, name, clientFilter)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		// minimega did not list every node's files; an empty or partial list
		// would read as files missing
		plog.Error(plog.TypeSystem, "getting list of files for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusBadGateway, "could not list files: "+err.Error())
	}

	freshness.SetObserved(w, observed)

	if sortCol != "" && sortDir != "" {
		files.SortBy(sortCol, sortDir == "asc")
	}

	total := len(files)

	if page > 0 {
		files = files.Paginate(page, pageSize)
	}

	body, err := json.Marshal(map[string]any{"files": files, "total": total})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling file list for experiment", "exp", name, "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
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
		fields  = audit.Fields{
			Experiment: name, File: path,
			Extra: []any{"phenix.file.capture", isCaptureFile(path), "phenix.file.preview", preview},
		}
	)

	if !role.Allowed("experiments/files", "get", name) {
		audit.Denied(r, audit.FileDownload, "getting experiment file not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	local, err := experiment.LocalFile(name, path)
	if err != nil {
		audit.Failure(r, audit.FileDownload, "getting experiment file failed", fields, err)

		if status := fileErrorStatus(err); status != http.StatusInternalServerError {
			return weberror.New(status, err.Error())
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

	// a download, streamed from disk and resumable; or the viewer's text
	d := util.Download{Path: local, Name: file, ContentType: "", MaxSize: 0}
	if preview {
		d.Name = ""
		d.ContentType = "text/plain; charset=utf-8"
		d.MaxSize = maxViewBytes
	}

	served, err := util.ServeDownload(w, r, d)
	if err != nil {
		audit.Failure(r, audit.FileDownload, "getting experiment file failed", fields, err)

		switch {
		case errors.Is(err, util.ErrTooLarge):
			return weberror.New(http.StatusRequestEntityTooLarge, path+" is too large to view (over 10 MiB). Download it instead")
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, util.ErrNotRegular):
			return weberror.New(http.StatusNotFound, path+": "+experiment.ErrFileNotFound.Error())
		default:
			plog.Error(plog.TypeSystem, "sending file for experiment", "exp", name, "file", path, "err", err)

			return weberror.New(http.StatusInternalServerError, "unable to read "+path)
		}
	}

	// once per download or view, not per resumed range or HEAD
	if served.Started {
		if rng := r.Header.Get("Range"); rng != "" {
			fields.Extra = append(fields.Extra, "http.request.range", rng)
		}

		audit.Success(r, audit.FileDownload, "downloaded file", fields)
	}

	return nil
}
