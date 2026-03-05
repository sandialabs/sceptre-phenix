package web

import (
	"encoding/json"
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

	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

const (
	MountTimeout     = 5 * time.Second
	MountPathTimeout = 2 * time.Second
)

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
func MountVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
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

		_, err := mm.ExecC2Command(
			mm.C2NS(vars["exp"]),
			mm.C2VM(vars["name"]),
			mm.C2Mount(),
			mm.C2IDClientsByUUID(),
			mm.C2Timeout(MountTimeout),
		)

		// if already mounted, that's ok, but still add to map
		if err != nil && !strings.Contains(err.Error(), "already connected") {
			plog.Error(plog.TypeSystem, "creating mount", "mount", mapKey, "err", err)
			http.Error(w, fmt.Sprintf("Error mounting: %s", err), http.StatusInternalServerError)

			return
		}

		activeMounts[mapKey] = &MountInfo{1, &sync.RWMutex{}}
	}

	w.WriteHeader(http.StatusOK)
}

// UnmountVM - POST /experiments/{exp}/vms/{name}/unmount.
func UnmountVM(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
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

			_, err := mm.ExecC2Command(
				mm.C2NS(vars["exp"]),
				mm.C2VM(vars["name"]),
				mm.C2Unmount(),
				mm.C2Timeout(MountTimeout),
				mm.C2SkipActiveClientCheck(true),
			)
			if err != nil {
				mountInfo.lock.Unlock()

				plog.Error(plog.TypeSystem, "unmounting", "mount", mapKey, "err", err)
				http.Error(
					w,
					fmt.Sprintf("Error unmounting: %s", err),
					http.StatusInternalServerError,
				)

				return
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

	activeMountsMu.RLock()

	mountInfo, exists := activeMounts[basePath]

	activeMountsMu.RUnlock()

	if !exists {
		http.Error(w, "No existing mount for "+basePath, http.StatusBadRequest)

		return
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	activeMountsMu.RLock()

	mountInfo, exists := activeMounts[basePath]

	activeMountsMu.RUnlock()

	if !exists {
		http.Error(w, "No existing mount for "+basePath, http.StatusBadRequest)

		return
	}

	mountInfo.lock.RLock()
	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])

	fileInfo, err := os.Stat(combinedPath)
	if err != nil {
		plog.Error(plog.TypeSystem, "error getting path", "err", err.Error(), "path", combinedPath)
		http.Error(
			w,
			fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error()),
			http.StatusInternalServerError,
		)

		return
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
		http.Error(
			w,
			fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath),
			http.StatusBadRequest,
		)

		return
	}

	if fileInfo.IsDir() {
		http.Error(w, "Can't download directory: "+combinedPath, http.StatusBadRequest)

		return
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
}

// UploadMountFile - PUT /experiments/{exp}/vms/{name}/files/download?path=.
func UploadMountFile(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	activeMountsMu.RLock()

	mountInfo, exists := activeMounts[basePath]

	activeMountsMu.RUnlock()

	if !exists {
		http.Error(w, "No existing mount for "+basePath, http.StatusBadRequest)

		return
	}

	mountInfo.lock.RLock()
	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])
	//nolint:gosec // Path traversal via taint analysis
	if _, err := os.Stat(combinedPath); err != nil {
		plog.Error(plog.TypeSystem, "error getting path", "err", err.Error(), "path", combinedPath)
		http.Error(
			w,
			fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error()),
			http.StatusInternalServerError,
		)

		return
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
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)
	}

	defer func() { _ = localFile.Close() }()

	_, _ = io.Copy(localFile, clientFile)
}
