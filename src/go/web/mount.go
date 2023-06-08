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

	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/rbac"
	"phenix/web/util"

	"github.com/gorilla/mux"
)

// Small struct for tracking number of users interacting with a mount and the
// lock for that mount.
type MountInfo struct {
	users int
	lock  *sync.RWMutex
}

var (
	// generally, use exclusive WLock for mount/unmount ops; RLock for file interactions
	activeMounts = make(map[string]*MountInfo)
	// locks activeMounts map itself
	activeMountsMu sync.RWMutex
)

// POST /experiments/{exp}/vms/{name}/mount
func MountVM(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	role := r.Context().Value("role").(rbac.Role)

	if !role.Allowed("vms/mount", "post", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	mapKey := mm.GetLocalMountPath(vars["exp"], vars["name"])

	activeMountsMu.Lock()
	defer activeMountsMu.Unlock()

	mountInfo, exists := activeMounts[mapKey]
	if exists {
		plog.Info("adding additional user to mount", "mount", mapKey, "count", mountInfo.users)

		mountInfo.users += 1
	} else {
		plog.Info("creating mount", "mount", mapKey)

		_, err := mm.ExecC2Command(mm.C2NS(vars["exp"]), mm.C2VM(vars["name"]), mm.C2Mount(), mm.C2IDClientsByUUID(), mm.C2Timeout(5*time.Second))

		// if already mounted, that's ok, but still add to map
		if err != nil && !strings.Contains(err.Error(), "already connected") {
			plog.Error("creating mount", "mount", mapKey, "err", err)
			http.Error(w, fmt.Sprintf("Error mounting: %s", err), http.StatusInternalServerError)

			return
		}

		activeMounts[mapKey] = &MountInfo{1, &sync.RWMutex{}}
	}

	w.WriteHeader(http.StatusOK)
}

// POST /experiments/{exp}/vms/{name}/unmount
func UnmountVM(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	role := r.Context().Value("role").(rbac.Role)

	if !role.Allowed("vms/mount", "delete", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	mapKey := mm.GetLocalMountPath(vars["exp"], vars["name"])

	activeMountsMu.Lock()
	defer activeMountsMu.Unlock()

	mountInfo, exists := activeMounts[mapKey]
	if exists {
		mountInfo.users -= 1

		if mountInfo.users == 0 {
			plog.Info("unmounting", "mount", mapKey)

			mountInfo.lock.Lock()

			_, err := mm.ExecC2Command(mm.C2NS(vars["exp"]), mm.C2VM(vars["name"]), mm.C2Unmount(), mm.C2Timeout(5*time.Second), mm.C2SkipActiveClientCheck(true))
			if err != nil {
				mountInfo.lock.Unlock()

				plog.Error("unmounting", "mount", mapKey, "err", err)
				http.Error(w, fmt.Sprintf("Error unmounting: %s", err), http.StatusInternalServerError)

				return
			}

			delete(activeMounts, mapKey)
		} else {
			plog.Info("call to unmount but skipping since users remain", "mount", mapKey, "count", mountInfo.users)
		}
	} else {
		plog.Warn("tried to unmount VM whose lock was not in map", "vm", vars["name"])
	}

	w.WriteHeader(http.StatusOK)
}

// GET /experiments/{exp}/vms/{name}/mount/files?path=
func GetMountFiles(w http.ResponseWriter, r *http.Request) {
	var (
		vars     = mux.Vars(r)
		basePath = mm.GetLocalMountPath(vars["exp"], vars["name"])
		role     = r.Context().Value("role").(rbac.Role)
	)

	if !role.Allowed("vms/mount", "list", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	plog.Info("getting files from mount", "mount", basePath)

	activeMountsMu.RLock()
	mountInfo, exists := activeMounts[basePath]
	activeMountsMu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("No existing mount for %s", basePath), http.StatusBadRequest)
		return
	}

	mountInfo.lock.RLock()
	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])

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
			errString := fmt.Sprintf("Error getting path %s: %v", combinedPath, err)

			plog.Error(errString)
			http.Error(w, errString, http.StatusInternalServerError)

			return
		}

		if !info.IsDir() {
			http.Error(w, fmt.Sprintf("Expected directory not file: %s", combinedPath), http.StatusBadRequest)
			return
		}
	case <-time.After(2 * time.Second):
		err := fmt.Sprintf("timeout getting path %s", combinedPath)

		plog.Error(err)
		http.Error(w, err, http.StatusInternalServerError)

		return
	}

	if !strings.HasPrefix(combinedPath, basePath) {
		errString := fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath)

		plog.Error(errString)
		http.Error(w, errString, http.StatusBadRequest)
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
		if err != nil {
			errString := fmt.Sprintf("Error getting files in %s: %v", combinedPath, err)

			plog.Error(errString)
			http.Error(w, errString, http.StatusInternalServerError)

			return
		}

		var files file.Files

		for _, e := range dirEntries {
			info, _ := e.Info()
			file := file.MakeFile(info, combinedPath)

			file.Path = strings.TrimPrefix(file.Path, basePath)

			files = append(files, file)
		}

		body, _ := json.Marshal(util.WithRoot("files", files))
		w.Write(body)
	case <-time.After(2 * time.Second):
		err := fmt.Sprintf("timeout getting files in %s", combinedPath)

		plog.Error(err)
		http.Error(w, err, http.StatusInternalServerError)
	}
}

// GET /experiments/{exp}/vms/{name}/files/download?path=
func DownloadMountFile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	basePath := mm.GetLocalMountPath(vars["exp"], vars["name"])

	role := r.Context().Value("role").(rbac.Role)
	if !role.Allowed("vms/mount", "get", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	activeMountsMu.RLock()
	mountInfo, exists := activeMounts[basePath]
	activeMountsMu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("No existing mount for %s", basePath), http.StatusBadRequest)
		return
	}

	mountInfo.lock.RLock()
	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])
	fileInfo, err := os.Stat(combinedPath)

	if err != nil {
		errString := fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error())

		plog.Error(errString)
		http.Error(w, errString, http.StatusInternalServerError)
		return
	}
	if !strings.HasPrefix(combinedPath, basePath) {
		errString := fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath)

		plog.Error(errString)
		http.Error(w, errString, http.StatusBadRequest)
		return
	}
	if fileInfo.IsDir() {
		http.Error(w, fmt.Sprintf("Can't download directory: %s", combinedPath), http.StatusBadRequest)
		return
	}

	plog.Info("download for file", "file", fileInfo.Name())

	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(fileInfo.Name()))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, combinedPath)
}

// PUT /experiments/{exp}/vms/{name}/files/download?path=
func UploadMountFile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	basePath := mm.GetLocalMountPath(vars["exp"], vars["name"])

	role := r.Context().Value("role").(rbac.Role)
	if !role.Allowed("vms/mount", "patch", fmt.Sprintf("%s/%s", vars["exp"], vars["name"])) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	activeMountsMu.RLock()
	mountInfo, exists := activeMounts[basePath]
	activeMountsMu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("No existing mount for %s", basePath), http.StatusBadRequest)
		return
	}

	mountInfo.lock.RLock()
	defer mountInfo.lock.RUnlock()

	combinedPath := filepath.Join(basePath, vars["path"])
	if _, err := os.Stat(combinedPath); err != nil {
		errString := fmt.Sprintf("Error getting path %s: %s", combinedPath, err.Error())

		plog.Error(errString)
		http.Error(w, errString, http.StatusInternalServerError)
		return
	}

	if !strings.HasPrefix(combinedPath, basePath) {
		errString := fmt.Sprintf("Error getting path %s: Path is not within mount", combinedPath)

		plog.Error(errString)
		http.Error(w, errString, http.StatusBadRequest)
		return
	}

	clientFile, handler, err := r.FormFile("file")
	if err != nil {
		plog.Error(err.Error())
		http.Error(w, fmt.Sprintf("Error uploading: %s", err.Error()), http.StatusInternalServerError)
	}

	defer clientFile.Close()

	localFile, err := os.OpenFile(filepath.Join(combinedPath, handler.Filename), os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		plog.Error(err.Error())
		http.Error(w, fmt.Sprintf("Error uploading: %s", err.Error()), http.StatusInternalServerError)
	}

	defer localFile.Close()

	io.Copy(localFile, clientFile)
}
