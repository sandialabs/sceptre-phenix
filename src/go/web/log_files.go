package web

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"

	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

// The log files the server hands out: its own system log and, when
// ui.logs.minimega-path names it, minimega's.
const (
	logFilePhenix   = "phenix"
	logFileMinimega = "minimega"
)

// logFileNames are the downloadable log files, in the order they are listed.
//
//nolint:gochecknoglobals // constant list
var logFileNames = []string{logFilePhenix, logFileMinimega}

// LogFile is one downloadable log file, as GET /logs/files lists it.
type LogFile struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	// Size and Modified describe an available file; Reason says why one is not.
	Size     *int64     `json:"size,omitempty"`
	Modified *time.Time `json:"modified,omitempty"`
	Reason   string     `json:"reason,omitempty"`
}

type logFileList struct {
	Files []LogFile `json:"files"`
}

// GetLogFiles - GET /logs/files: the log files GET /logs/files/{name}
// downloads, each with whether it can be downloaded now and why not.
func GetLogFiles(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	if !role.Allowed("logs", "get") {
		return weberror.New(http.StatusForbidden, "forbidden")
	}

	files := make([]LogFile, 0, len(logFileNames))

	for _, name := range logFileNames {
		files = append(files, logFileStatus(name))
	}

	body, err := json.Marshal(logFileList{Files: files})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "listing the log files failed")
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// DownloadLogFile - GET /logs/files/{name}: streams the phenix or minimega
// log file as an attachment, with byte ranges and HEAD (util.ServeDownload).
func DownloadLogFile(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	name := mux.Vars(r)["name"]
	fields := audit.Fields{Extra: []any{"phenix.log.name", name}}

	if !role.Allowed("logs", "get") {
		audit.Denied(r, audit.LogDownload, "downloading log file not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	f, path, werr := openLogFile(name)
	if werr != nil {
		audit.Failure(r, audit.LogDownload, "downloading log file failed", fields, werr)

		return werr
	}

	defer func() { _ = f.Close() }()

	fields.File = path

	served, err := util.ServeOpenFile(w, r, f, util.Download{
		Path:        path,
		Name:        filepath.Base(path),
		ContentType: "text/plain; charset=utf-8",
		MaxSize:     0,
	})
	if err != nil {
		audit.Failure(r, audit.LogDownload, "downloading log file failed", fields, err)

		if errors.Is(err, util.ErrNotRegular) {
			return weberror.New(http.StatusNotFound, "the "+name+" log file is not a regular file")
		}

		return weberror.New(http.StatusInternalServerError, "the "+name+" log file cannot be read")
	}

	// once per download, not per resumed range or HEAD
	if served.Started {
		if rng := r.Header.Get("Range"); rng != "" {
			fields.Extra = append(fields.Extra, "http.request.range", rng)
		}

		audit.Success(r, audit.LogDownload, "downloaded log file", fields)
	}

	return nil
}

// logFileStatus is the named log file as GET /logs/files lists it.
func logFileStatus(name string) LogFile {
	file := LogFile{Name: name, Available: false, Size: nil, Modified: nil, Reason: ""}

	f, _, werr := openLogFile(name)
	if werr != nil {
		file.Reason = werr.Message

		return file
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()

	switch {
	case err != nil:
		file.Reason = "the " + name + " log file cannot be read"
	case !info.Mode().IsRegular():
		file.Reason = "the " + name + " log file is not a regular file"
	default:
		size, modified := info.Size(), info.ModTime().UTC()
		file.Available, file.Size, file.Modified = true, &size, &modified
	}

	return file
}

// openLogFile opens the named log file, or answers why it cannot: an unknown
// name or a file the server is not told of, missing or may not read.
func openLogFile(name string) (*os.File, string, *weberror.WebError) {
	var path string

	switch name {
	case logFilePhenix:
		if o.systemLog != nil {
			path = o.systemLog()
		}

		if path == "" {
			return nil, "", weberror.New(http.StatusNotFound, "the phenix server writes no system log file")
		}
	case logFileMinimega:
		path = o.minimegaLogs
		if path == "" {
			return nil, "", weberror.New(
				http.StatusNotFound,
				"the minimega log file is not configured (set ui.logs.minimega-path)",
			)
		}
	default:
		return nil, "", weberror.New(http.StatusNotFound, "no such log file: "+name)
	}

	f, err := os.Open(path)
	if err != nil {
		plog.Debug(plog.TypeSystem, "opening log file for download", "log", name, "err", err)

		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, path, weberror.New(http.StatusNotFound, "the "+name+" log file does not exist yet")
		case errors.Is(err, fs.ErrPermission):
			return nil, path, weberror.New(
				http.StatusServiceUnavailable,
				"the phenix server does not have permission to read the "+name+" log file",
			)
		default:
			return nil, path, weberror.New(http.StatusInternalServerError, "the "+name+" log file cannot be read")
		}
	}

	return f, path, nil
}
