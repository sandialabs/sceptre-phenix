package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

const MountPathTimeout = 2 * time.Second

// MountInfo is a small struct for tracking number of users interacting with a mount and the
// lock for that mount.
type MountInfo struct {
	users int
	lock  *sync.RWMutex
}

var (
	// generally, use exclusive WLock for mount/unmount ops; RLock for file interactions.
	activeMounts = make(map[string]*MountInfo) //nolint:gochecknoglobals // global state
	// locks activeMounts map itself.
	activeMountsMu sync.RWMutex //nolint:gochecknoglobals // global lock
)

// MountVM - POST /experiments/{exp}/vms/{name}/mount.
func MountVM(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	mapKey := mm.GetLocalMountPath(vars["exp"], vars["name"])

	if !role.Allowed("vms/mount", "post", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"mounting vm not allowed",
			"user",
			user,
			"mount",
			mapKey,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	activeMountsMu.Lock()
	defer activeMountsMu.Unlock()

	mountInfo, exists := activeMounts[mapKey]
	if exists {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
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
	} else {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Info(
			plog.TypeAction,
			"vm mounted",
			"exp",
			vars["exp"],
			"vm",
			vars["vm"],
			"path",
			mapKey,
			"user",
			user,
		)

		if err := vm.MountFilesystem(vars["exp"], vars["name"]); err != nil {
			plog.Error(plog.TypeSystem, "creating mount", "mount", mapKey, "err", err)

			return weberror.New(http.StatusInternalServerError, fmt.Sprintf("Error mounting: %s", err))
		}

		activeMounts[mapKey] = &MountInfo{1, &sync.RWMutex{}}
	}

	w.WriteHeader(http.StatusOK)

	return nil
}

// UnmountVM - POST /experiments/{exp}/vms/{name}/unmount.
func UnmountVM(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	mapKey := mm.GetLocalMountPath(vars["exp"], vars["name"])

	if !role.Allowed("vms/mount", "delete", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"unmounting vm not allowed",
			"user",
			user,
			"mount",
			mapKey,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	activeMountsMu.Lock()
	defer activeMountsMu.Unlock()

	mountInfo, exists := activeMounts[mapKey]
	if exists {
		mountInfo.users -= 1

		if mountInfo.users == 0 {
			user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
			plog.Info(
				plog.TypeAction,
				"unmounting",
				"mount",
				mapKey,
				"user",
				user,
			)

			mountInfo.lock.Lock()

			if err := vm.UnmountFilesystem(vars["exp"], vars["name"]); err != nil {
				mountInfo.lock.Unlock()

				plog.Error(plog.TypeSystem, "unmounting", "mount", mapKey, "err", err)

				return weberror.New(http.StatusInternalServerError, fmt.Sprintf("Error unmounting: %s", err))
			}

			delete(activeMounts, mapKey)
		} else {
			user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
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
		}
	} else {
		plog.Warn(
			plog.TypeSystem,
			"tried to unmount VM whose lock was not in map",
			"vm",
			vars["name"],
		)
	}

	w.WriteHeader(http.StatusOK)

	return nil
}

// GetMountFiles - GET /experiments/{exp}/vms/{name}/mount/files?path=
// Note: error may be returned inside json body as Readdir can return an error with entries.
//
//nolint:funlen // handler
func GetMountFiles(w http.ResponseWriter, r *http.Request) error {
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

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	activeMountsMu.RLock()

	mountInfo, exists := activeMounts[basePath]

	activeMountsMu.RUnlock()

	if !exists {
		return weberror.New(http.StatusBadRequest, "No existing mount for "+basePath)
	}

	mountInfo.lock.RLock()
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

			return weberror.New(http.StatusInternalServerError, fmt.Sprintf("Error getting path %s: %v", combinedPath, err))
		}

		if !info.IsDir() {
			return weberror.New(http.StatusBadRequest, "Expected directory not file: "+combinedPath)
		}
	case <-time.After(MountPathTimeout):
		plog.Error(plog.TypeSystem, "timeout getting mount path", "path", combinedPath)

		return weberror.New(http.StatusInternalServerError, "timeout getting path "+combinedPath)
	}

	if !strings.HasPrefix(combinedPath, basePath) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Error(
			plog.TypeSecurity,
			"user attempted getting path outside of mount",
			"path",
			combinedPath,
			"user",
			user,
		)

		return weberror.New(http.StatusBadRequest, fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath))
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

		return weberror.New(http.StatusInternalServerError, err)
	}

	return nil
}

// DownloadMountFile - GET /experiments/{exp}/vms/{name}/files/download?path=.
func DownloadMountFile(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	basePath := mm.GetLocalMountPath(vars["exp"], vars["name"])

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("vms/mount", "get", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"downloading vm mount files not allowed not allowed",
			"user",
			user,
			"mount",
			basePath,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	activeMountsMu.RLock()

	mountInfo, exists := activeMounts[basePath]

	activeMountsMu.RUnlock()

	if !exists {
		return weberror.New(http.StatusBadRequest, "No existing mount for "+basePath)
	}

	mountInfo.lock.RLock()
	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])

	fileInfo, err := os.Stat(combinedPath)
	if err != nil {
		plog.Error(plog.TypeSystem, "error getting path", "err", err.Error(), "path", combinedPath)

		return weberror.New(http.StatusInternalServerError, fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error()))
	}

	if !strings.HasPrefix(combinedPath, basePath) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Error(
			plog.TypeSecurity,
			"user attempted downloading file outside of mount",
			"path",
			combinedPath,
			"user",
			user,
		)

		return weberror.New(http.StatusBadRequest, fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath))
	}

	if fileInfo.IsDir() {
		return weberror.New(http.StatusBadRequest, "Can't download directory: "+combinedPath)
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"download for file",
		"file",
		combinedPath,
		"user",
		user,
	)

	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(fileInfo.Name()))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, combinedPath)

	return nil
}

// UploadMountFile - PUT /experiments/{exp}/vms/{name}/files/download?path=.
func UploadMountFile(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	basePath := mm.GetLocalMountPath(vars["exp"], vars["name"])

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("vms/mount", "patch", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"uploading vm mount files not allowed not allowed",
			"user",
			user,
			"mount",
			basePath,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	activeMountsMu.RLock()

	mountInfo, exists := activeMounts[basePath]

	activeMountsMu.RUnlock()

	if !exists {
		return weberror.New(http.StatusBadRequest, "No existing mount for "+basePath)
	}

	mountInfo.lock.RLock()
	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])
	//nolint:gosec // Path traversal via taint analysis
	if _, err := os.Stat(combinedPath); err != nil {
		plog.Error(plog.TypeSystem, "error getting path", "err", err.Error(), "path", combinedPath)

		return weberror.New(http.StatusInternalServerError, fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error()))
	}

	if !strings.HasPrefix(combinedPath, basePath) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Error(
			plog.TypeSecurity,
			"user attempted uploading file outside of mount",
			"path",
			combinedPath,
			"user",
			user,
		)

		return weberror.New(http.StatusBadRequest, fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath))
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

		return weberror.New(http.StatusInternalServerError, "Error uploading: "+err.Error())
	}

	defer func() { _ = clientFile.Close() }()

	localFile, err := os.OpenFile( //nolint:gosec // Path traversal via taint analysis
		filepath.Join(combinedPath, handler.Filename),
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

		return weberror.New(http.StatusInternalServerError, "Error uploading: "+err.Error())
	}

	defer func() { _ = localFile.Close() }()

	_, _ = io.Copy(localFile, clientFile)

	return nil
}

// CopyExperimentFileToMount copies a same-experiment file into a mounted VM directory.
func CopyExperimentFileToMount(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	basePath := mm.GetLocalMountPath(vars["exp"], vars["name"])

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("vms/mount", "patch", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if !role.Allowed("experiments/files", "get", vars["exp"]) {
		return weberror.New(http.StatusForbidden, "forbidden")
	}

	activeMountsMu.RLock()
	mountInfo, exists := activeMounts[basePath]
	activeMountsMu.RUnlock()

	if !exists {
		return weberror.New(http.StatusBadRequest, "No existing mount for "+basePath)
	}

	mountInfo.lock.RLock()
	defer mountInfo.lock.RUnlock()

	query := r.URL.Query()
	destDir := filepath.Join(basePath, query.Get("path"))
	if err := validatePathWithin(destDir, basePath); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	info, err := os.Stat(destDir)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "Error getting path "+destDir+": "+err.Error())
	}

	if !info.IsDir() {
		return weberror.New(http.StatusBadRequest, "Expected directory not file: "+destDir)
	}

	exp, err := experiment.Get(vars["exp"])
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "Error getting experiment: "+err.Error())
	}

	source, err := getExperimentFilePath(vars["exp"], query.Get("source"), exp.FilesDir())
	if err != nil {
		return weberror.New(http.StatusBadRequest, "Error getting source file: "+err.Error())
	}

	sourceFile, err := os.Open(source) //nolint:gosec // source validated against experiment file list
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "Error opening source file: "+err.Error())
	}
	defer func() { _ = sourceFile.Close() }()

	destName := filepath.Base(source)
	destPath := filepath.Join(destDir, destName)
	if err := validatePathWithin(destPath, destDir); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	destFile, err := os.OpenFile(
		destPath,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		0o600,
	) //nolint:gosec // destination validated against mount path
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "Error creating destination file: "+err.Error())
	}
	defer func() { _ = destFile.Close() }()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return weberror.New(http.StatusInternalServerError, "Error copying file: "+err.Error())
	}

	return nil
}

// getExperimentFilePath returns a source path only when it matches a known experiment file.
func getExperimentFilePath(expName, source, filesDir string) (string, error) {
	if source == "" {
		return "", errors.New("no source file provided")
	}

	files, err := experiment.Files(expName, "")
	if err != nil {
		return "", fmt.Errorf("listing experiment files: %w", err)
	}

	for _, f := range files {
		if source != f.Path {
			continue
		}

		headnode, _ := os.Hostname()
		_ = file.CopyFile(fmt.Sprintf("/%s/files/%s", expName, f.Path), headnode, nil)

		path := filepath.Join(filesDir, f.Path)
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}

		if info.IsDir() {
			return "", errors.New("source is a directory")
		}

		return path, nil
	}

	return "", errors.New("file not found")
}

// validatePathWithin returns an error when path escapes base.
func validatePathWithin(path, base string) error {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	if rel == ".." || strings.HasPrefix(rel, "../") {
		return errors.New("path is not within base")
	}

	return nil
}
