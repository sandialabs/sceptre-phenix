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
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

const (
	MountPathTimeout = 2 * time.Second
	// how often an unmount checks whether the mount's file requests finished.
	unmountPollInterval = 50 * time.Millisecond
)

// unmountWaitTimeout is how long an unmount waits for the mount's file
// requests, such as downloads, to finish before giving up with 409.
var unmountWaitTimeout = 30 * time.Second //nolint:gochecknoglobals // shortened in tests

type mountState int

const (
	mountMounted mountState = iota
	mountMounting
	mountUnmounting
)

// MountInfo is a small struct for tracking number of users interacting with a mount and the
// lock for that mount.
type MountInfo struct {
	users int
	lock  *sync.RWMutex

	state mountState
	// closed once a mounting entry is mounted or failed; err tells which
	ready chan struct{}
	err   error
}

var (
	// generally, use exclusive WLock for mount/unmount ops; RLock for file interactions.
	activeMounts = make(map[string]*MountInfo) //nolint:gochecknoglobals // global state
	// locks activeMounts map itself, and the users and state of its entries. It
	// is never held across a mount, an unmount or a file request, so one VM's
	// mount does not wait on another's.
	activeMountsMu sync.RWMutex //nolint:gochecknoglobals // global lock

	// replaced in tests.
	mountFilesystem   = vm.MountFilesystem   //nolint:gochecknoglobals // see above
	unmountFilesystem = vm.UnmountFilesystem //nolint:gochecknoglobals // see above
)

// lockMountForFiles returns the mount at key, read-locked for a file request,
// if it is mounted. The caller unlocks it.
func lockMountForFiles(key string) (*MountInfo, bool) {
	activeMountsMu.RLock()
	defer activeMountsMu.RUnlock()

	info, ok := activeMounts[key]
	if !ok || info.state != mountMounted {
		return nil, false
	}

	// fails only while an unmount holds the lock
	if !info.lock.TryRLock() {
		return nil, false
	}

	return info, true
}

// lockMountForUnmount takes the mount's lock once its file requests finish,
// giving up after unmountWaitTimeout or when ctx is done. New file requests
// are refused meanwhile, since the mount is unmounting.
func lockMountForUnmount(ctx context.Context, info *MountInfo) bool {
	timeout := time.NewTimer(unmountWaitTimeout)
	defer timeout.Stop()

	ticker := time.NewTicker(unmountPollInterval)
	defer ticker.Stop()

	for !info.lock.TryLock() {
		select {
		case <-ctx.Done():
			return false
		case <-timeout.C:
			return false
		case <-ticker.C:
		}
	}

	return true
}

// MountVM - POST /experiments/{exp}/vms/{name}/mount.
func MountVM(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	mapKey := mm.GetLocalMountPath(vars["exp"], vars["name"])

	mounted := mountAuditFields(vars["exp"], vars["name"], mapKey)

	if !role.Allowed("vms/mount", "post", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		audit.Denied(r, audit.VMMount, "mounting vm not allowed", mounted)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)

	activeMountsMu.Lock()

	mountInfo, exists := activeMounts[mapKey]
	if exists {
		if mountInfo.state == mountUnmounting {
			activeMountsMu.Unlock()

			http.Error(w, "unmount in progress", http.StatusConflict)

			return
		}

		plog.Info(
			plog.TypeAction,
			"adding additional user to mount",
			"mount",
			mapKey,
			"count",
			mountInfo.users,
			"user",
			user,
		)

		mountInfo.users += 1
		mounting := mountInfo.state == mountMounting

		activeMountsMu.Unlock()

		if mounting {
			waitForMount(w, r, mapKey, mountInfo, mounted)

			return
		}

		audit.Success(r, audit.VMMount, "vm mounted", mounted)
		w.WriteHeader(http.StatusOK)

		return
	}

	mountInfo = &MountInfo{
		users: 1,
		lock:  &sync.RWMutex{},
		state: mountMounting,
		ready: make(chan struct{}),
		err:   nil,
	}
	activeMounts[mapKey] = mountInfo

	activeMountsMu.Unlock()

	err := mountFilesystem(vars["exp"], vars["name"])

	activeMountsMu.Lock()

	mountInfo.err = err

	if err != nil {
		if activeMounts[mapKey] == mountInfo {
			delete(activeMounts, mapKey)
		}
	} else {
		mountInfo.state = mountMounted
	}

	activeMountsMu.Unlock()

	close(mountInfo.ready)

	if err != nil {
		plog.Error(plog.TypeSystem, "creating mount", "mount", mapKey, "err", err)
		audit.Failure(r, audit.VMMount, "mounting vm failed", mounted, err)
		http.Error(w, fmt.Sprintf("Error mounting: %s", err), http.StatusInternalServerError)

		return
	}

	audit.Success(r, audit.VMMount, "vm mounted", mounted)
	w.WriteHeader(http.StatusOK)
}

// reasonOutsideMount is the event.reason of a refused path outside a mount.
const reasonOutsideMount = "outside mount"

var (
	// errMountBusy is why an unmount gave up: file requests kept the mount busy.
	errMountBusy = errors.New("mount busy")
	// errNotDirectory is why a copy into a mount's file, not folder, failed.
	errNotDirectory = errors.New("destination is not a directory")
)

// mountAuditFields name a VM's mount, at path, in an audit event.
func mountAuditFields(exp, vm, path string) audit.Fields {
	return audit.Fields{Experiment: exp, VM: vm, Extra: []any{"phenix.mount.path", path}}
}

// waitForMount answers a mount request for a VM another request is mounting,
// once that mount finishes, and audits it with fields. The request's user was
// already counted.
func waitForMount(w http.ResponseWriter, r *http.Request, mapKey string, mountInfo *MountInfo, fields audit.Fields) {
	select {
	case <-mountInfo.ready:
	case <-r.Context().Done():
		activeMountsMu.Lock()
		if activeMounts[mapKey] == mountInfo {
			mountInfo.users -= 1
		}
		activeMountsMu.Unlock()

		return
	}

	if mountInfo.err != nil {
		audit.Failure(r, audit.VMMount, "mounting vm failed", fields, mountInfo.err)
		http.Error(w, fmt.Sprintf("Error mounting: %s", mountInfo.err), http.StatusInternalServerError)

		return
	}

	audit.Success(r, audit.VMMount, "vm mounted", fields)
	w.WriteHeader(http.StatusOK)
}

// UnmountVM - POST /experiments/{exp}/vms/{name}/unmount.
//
//nolint:funlen // handler
func UnmountVM(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	mapKey := mm.GetLocalMountPath(vars["exp"], vars["name"])

	unmounted := mountAuditFields(vars["exp"], vars["name"], mapKey)

	if !role.Allowed("vms/mount", "delete", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		audit.Denied(r, audit.VMUnmount, "unmounting vm not allowed", unmounted)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)

	activeMountsMu.Lock()

	mountInfo, exists := activeMounts[mapKey]
	if !exists {
		activeMountsMu.Unlock()

		plog.Warn(
			plog.TypeSystem,
			"tried to unmount VM whose lock was not in map",
			"vm",
			vars["name"],
		)

		w.WriteHeader(http.StatusOK)

		return
	}

	if mountInfo.state != mountMounted {
		activeMountsMu.Unlock()

		http.Error(w, "mount or unmount in progress", http.StatusConflict)

		return
	}

	mountInfo.users -= 1

	if mountInfo.users > 0 {
		activeMountsMu.Unlock()

		plog.Info(
			plog.TypeAction,
			"call to unmount but skipping since users remain",
			"mount",
			mapKey,
			"count",
			mountInfo.users,
			"user",
			user,
		)

		// the user's use of the mount ended, though others' have not
		audit.Success(r, audit.VMUnmount, "vm unmounted", unmounted)
		w.WriteHeader(http.StatusOK)

		return
	}

	mountInfo.state = mountUnmounting

	activeMountsMu.Unlock()

	plog.Info(
		plog.TypeAction,
		"unmounting",
		"mount",
		mapKey,
		"user",
		user,
	)

	// waits only for this mount's own file requests
	if !lockMountForUnmount(r.Context(), mountInfo) {
		activeMountsMu.Lock()
		mountInfo.state = mountMounted
		mountInfo.users += 1
		activeMountsMu.Unlock()

		plog.Warn(plog.TypeSystem, "unmount gave up waiting for file requests", "mount", mapKey)
		audit.Failure(r, audit.VMUnmount, "unmounting vm failed", unmounted, errMountBusy)
		http.Error(w, "mount busy", http.StatusConflict)

		return
	}

	err := unmountFilesystem(vars["exp"], vars["name"])

	mountInfo.lock.Unlock()

	activeMountsMu.Lock()

	if err != nil {
		mountInfo.state = mountMounted
	} else if activeMounts[mapKey] == mountInfo {
		delete(activeMounts, mapKey)
	}

	activeMountsMu.Unlock()

	if err != nil {
		plog.Error(plog.TypeSystem, "unmounting", "mount", mapKey, "err", err)
		audit.Failure(r, audit.VMUnmount, "unmounting vm failed", unmounted, err)
		http.Error(
			w,
			fmt.Sprintf("Error unmounting: %s", err),
			http.StatusInternalServerError,
		)

		return
	}

	audit.Success(r, audit.VMUnmount, "vm unmounted", unmounted)
	w.WriteHeader(http.StatusOK)
}

// GetMountFiles - GET /experiments/{exp}/vms/{name}/mount/files?path=
// Note: error may be returned inside json body as Readdir can return an error with entries.
//
//nolint:funlen // handler
func GetMountFiles(w http.ResponseWriter, r *http.Request) {
	var (
		vars     = mux.Vars(r)
		basePath = mm.GetLocalMountPath(vars["exp"], vars["name"])
		role, _  = r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	)

	if !role.Allowed("vms/mount", "list", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting vm mount files not allowed not allowed",
			"user",
			user,
			"mount",
			basePath,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	mountInfo, exists := lockMountForFiles(basePath)
	if !exists {
		http.Error(w, "No existing mount for "+basePath, http.StatusBadRequest)

		return
	}

	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])
	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"getting files from mount",
		"path",
		combinedPath,
		"user",
		user,
	)

	var (
		info fs.FileInfo
		err  error
		done = make(chan struct{})
	)

	go func() {
		info, err = os.Stat(combinedPath)

		close(done)
	}()

	select {
	case <-done:
		if err != nil {
			plog.Error(plog.TypeSystem, "error getting mount path", "path", combinedPath)
			http.Error(
				w,
				fmt.Sprintf("Error getting path %s: %v", combinedPath, err),
				http.StatusInternalServerError,
			)

			return
		}

		if !info.IsDir() {
			http.Error(w, "Expected directory not file: "+combinedPath, http.StatusBadRequest)

			return
		}
	case <-time.After(MountPathTimeout):
		plog.Error(plog.TypeSystem, "timeout getting mount path", "path", combinedPath)
		http.Error(w, "timeout getting path "+combinedPath, http.StatusInternalServerError)

		return
	}

	if !file.WithinDir(basePath, combinedPath) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Error(
			plog.TypeSecurity,
			"user attempted getting path outside of mount",
			"path",
			combinedPath,
			"user",
			user,
		)
		http.Error(
			w,
			fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath),
			http.StatusBadRequest,
		)

		return
	}

	var dirEntries []fs.DirEntry

	done = make(chan struct{})

	go func() {
		dirEntries, err = os.ReadDir(combinedPath)

		close(done)
	}()

	select {
	case <-done:
		var files file.Files

		for _, e := range dirEntries {
			info, _ := e.Info()
			file := file.MakeFile(info, combinedPath)

			file.Path = strings.TrimPrefix(file.Path, basePath)

			files = append(files, file)
		}

		resp := map[string]any{"error": "", "files": files}

		if err != nil {
			plog.Error(
				plog.TypeSystem,
				fmt.Sprintf(
					"Error getting files in %s. Still read %d entries: %v",
					combinedPath,
					len(dirEntries),
					err,
				),
			)
			resp["error"] = strings.ReplaceAll(fmt.Sprintf("%v", err), basePath, "")
		}

		body, _ := json.Marshal(resp)
		_, _ = w.Write(body)
	case <-time.After(MountPathTimeout):
		err := "timeout getting files in " + combinedPath

		plog.Error(plog.TypeSystem, "timeout getting files", "err", err, "path", combinedPath)
		http.Error(w, err, http.StatusInternalServerError)
	}
}

// DownloadMountFile - GET /experiments/{exp}/vms/{name}/files/download?path=.
func DownloadMountFile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	basePath := mm.GetLocalMountPath(vars["exp"], vars["name"])

	downloaded := audit.Fields{Experiment: vars["exp"], VM: vars["name"]}

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("vms/mount", "get", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		audit.Denied(r, audit.FileDownload, "downloading vm mount files not allowed", downloaded)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	mountInfo, exists := lockMountForFiles(basePath)
	if !exists {
		http.Error(w, "No existing mount for "+basePath, http.StatusBadRequest)

		return
	}

	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])
	downloaded.File = combinedPath

	if !file.WithinDir(basePath, combinedPath) {
		downloaded.Reason = reasonOutsideMount
		audit.Denied(r, audit.FileDownload, "user attempted downloading file outside of mount", downloaded)
		http.Error(
			w,
			fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath),
			http.StatusBadRequest,
		)

		return
	}

	served, err := util.ServeDownload(w, r, util.Download{
		Path:        combinedPath,
		Name:        filepath.Base(combinedPath),
		ContentType: "application/octet-stream",
		Live:        false,
		MaxSize:     0,
	})
	if err != nil {
		audit.Failure(r, audit.FileDownload, "downloading vm mount file failed", downloaded, err)

		switch {
		case errors.Is(err, util.ErrNotRegular):
			http.Error(w, "Can't download directory: "+combinedPath, http.StatusBadRequest)
		case errors.Is(err, fs.ErrNotExist):
			http.Error(w, fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error()), http.StatusNotFound)
		default:
			plog.Error(plog.TypeSystem, "error getting path", "err", err.Error(), "path", combinedPath)
			http.Error(
				w,
				fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error()),
				http.StatusInternalServerError,
			)
		}

		return
	}

	// once per download, not per resumed range or HEAD
	if served.Started {
		if rng := r.Header.Get("Range"); rng != "" {
			downloaded.Extra = []any{"http.request.range", rng}
		}

		audit.Success(r, audit.FileDownload, "downloaded vm mount file", downloaded)
	}
}

// UploadMountFile - PUT /experiments/{exp}/vms/{name}/files/upload?path=.
//
//nolint:funlen // handler
func UploadMountFile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	basePath := mm.GetLocalMountPath(vars["exp"], vars["name"])

	uploaded := audit.Fields{Experiment: vars["exp"], VM: vars["name"]}

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("vms/mount", "patch", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		audit.Denied(r, audit.VMFileUpload, "uploading vm mount files not allowed", uploaded)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	mountInfo, exists := lockMountForFiles(basePath)
	if !exists {
		http.Error(w, "No existing mount for "+basePath, http.StatusBadRequest)

		return
	}

	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])
	uploaded.Target = combinedPath

	//nolint:gosec // Path traversal via taint analysis
	if _, err := os.Stat(combinedPath); err != nil {
		plog.Error(plog.TypeSystem, "error getting path", "err", err.Error(), "path", combinedPath)
		audit.Failure(r, audit.VMFileUpload, "uploading vm mount file failed", uploaded, err)
		http.Error(
			w,
			fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error()),
			http.StatusInternalServerError,
		)

		return
	}

	if !file.WithinDir(basePath, combinedPath) {
		uploaded.Reason = reasonOutsideMount
		audit.Denied(r, audit.VMFileUpload, "user attempted uploading file outside of mount", uploaded)
		http.Error(
			w,
			fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath),
			http.StatusBadRequest,
		)

		return
	}

	clientFile, handler, err := r.FormFile("file")
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"error uploading file",
			"err",
			err.Error(),
			"dest_path",
			combinedPath,
		)
		audit.Failure(r, audit.VMFileUpload, "uploading vm mount file failed", uploaded, err)
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)

		return
	}

	defer func() { _ = clientFile.Close() }()

	uploaded.Target = filepath.Join(combinedPath, handler.Filename)

	localFile, err := os.OpenFile( //nolint:gosec // Path traversal via taint analysis
		uploaded.Target,
		os.O_WRONLY|os.O_CREATE,
		0o600,
	)
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"error uploading file",
			"err",
			err.Error(),
			"dest_path",
			combinedPath,
		)
		audit.Failure(r, audit.VMFileUpload, "uploading vm mount file failed", uploaded, err)
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)

		return
	}

	defer func() { _ = localFile.Close() }()

	written, err := io.Copy(localFile, clientFile)
	if err != nil {
		plog.Error(plog.TypeSystem, "error uploading file", "err", err.Error(), "dest_path", uploaded.Target)
		audit.Failure(r, audit.VMFileUpload, "uploading vm mount file failed", uploaded, err)
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)

		return
	}

	uploaded.Extra = []any{"file.size", written}
	audit.Success(r, audit.VMFileUpload, "uploaded vm mount file", uploaded)
}

// CopyExperimentFileToMount copies a same-experiment file into a mounted VM directory.
//
//nolint:funlen // handler
func CopyExperimentFileToMount(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	basePath := mm.GetLocalMountPath(vars["exp"], vars["name"])

	query := r.URL.Query()
	copied := audit.Fields{Experiment: vars["exp"], VM: vars["name"], File: query.Get("source")}

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("vms/mount", "patch", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		audit.Denied(r, audit.VMFileCopy, "copying file to vm mount not allowed", copied)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if !role.Allowed("experiments/files", "get", vars["exp"]) {
		audit.Denied(r, audit.VMFileCopy, "copying file to vm mount not allowed", copied)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	mountInfo, exists := lockMountForFiles(basePath)
	if !exists {
		http.Error(w, "No existing mount for "+basePath, http.StatusBadRequest)

		return
	}

	defer mountInfo.lock.RUnlock()

	destDir := filepath.Join(basePath, query.Get("path"))
	copied.Target = destDir

	if !file.WithinDir(basePath, destDir) {
		copied.Reason = reasonOutsideMount
		audit.Denied(r, audit.VMFileCopy, "user attempted copying file outside of mount", copied)
		http.Error(w, "path is not within base", http.StatusBadRequest)

		return
	}

	info, err := os.Stat(destDir)
	if err != nil {
		audit.Failure(r, audit.VMFileCopy, "copying file to vm mount failed", copied, err)
		http.Error(w, "Error getting path "+destDir+": "+err.Error(), http.StatusInternalServerError)

		return
	}

	if !info.IsDir() {
		audit.Failure(r, audit.VMFileCopy, "copying file to vm mount failed", copied, errNotDirectory)
		http.Error(w, "Expected directory not file: "+destDir, http.StatusBadRequest)

		return
	}

	source, err := experiment.LocalFile(vars["exp"], query.Get("source"))
	if err != nil {
		audit.Failure(r, audit.VMFileCopy, "copying file to vm mount failed", copied, err)
		http.Error(w, "Error getting source file: "+err.Error(), http.StatusBadRequest)

		return
	}

	sourceFile, err := os.Open(source) //nolint:gosec // source validated against experiment file list
	if err != nil {
		audit.Failure(r, audit.VMFileCopy, "copying file to vm mount failed", copied, err)
		http.Error(w, "Error opening source file: "+err.Error(), http.StatusInternalServerError)

		return
	}
	defer func() { _ = sourceFile.Close() }()

	destName := filepath.Base(source)
	destPath := filepath.Join(destDir, destName)
	copied.Target = destPath

	if !file.WithinDir(destDir, destPath) {
		copied.Reason = reasonOutsideMount
		audit.Denied(r, audit.VMFileCopy, "user attempted copying file outside of mount", copied)
		http.Error(w, "path is not within base", http.StatusBadRequest)

		return
	}

	destFile, err := os.OpenFile(
		destPath,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		0o600,
	) //nolint:gosec // destination validated against mount path
	if err != nil {
		audit.Failure(r, audit.VMFileCopy, "copying file to vm mount failed", copied, err)
		http.Error(w, "Error creating destination file: "+err.Error(), http.StatusInternalServerError)

		return
	}
	defer func() { _ = destFile.Close() }()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		audit.Failure(r, audit.VMFileCopy, "copying file to vm mount failed", copied, err)
		http.Error(w, "Error copying file: "+err.Error(), http.StatusInternalServerError)

		return
	}

	audit.Success(r, audit.VMFileCopy, "copied file to vm mount", copied)
}
