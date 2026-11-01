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
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"phenix/api/disk"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

// GetDisks - GET /disks.
//
// Lists the images the role may list, sorted by name and paged by pageNum and
// perPage (total counts all).
func GetDisks(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetDisks")

	var (
		ctx             = r.Context()
		role, _         = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		query           = r.URL.Query()
		expName         = query.Get("expName")
		diskType        = query.Get("diskType")
		defaultDiskType = disk.VMImage | disk.ContainerImage | disk.ISOImage | disk.UNKNOWN
	)

	if !role.Allowed("disks", "list") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"listing disks not allowed",
			"user",
			user,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	page, pageSize, err := util.ParsePage(query.Get("pageNum"), query.Get("perPage"))
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if len(diskType) > 0 {
		defaultDiskType = 0
		for s := range strings.SplitSeq(diskType, ",") {
			defaultDiskType |= disk.StringToKind(s)
		}
	}

	disks, err := disk.GetImages(expName)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	filtered := []disk.Details{}

	for _, disk := range disks {
		if disk.Kind&defaultDiskType != 0 {
			filtered = append(filtered, disk)
		}
	}

	allowed := []disk.Details{}

	for _, disk := range filtered {
		if role.Allowed("disks", "list", disk.Name) {
			allowed = append(allowed, disk)
		}
	}

	sort.Slice(allowed, func(i, j int) bool {
		return allowed[i].Name < allowed[j].Name
	})

	body, err := json.Marshal(map[string]any{"disks": util.Page(allowed, page, pageSize), "total": len(allowed)})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// CommitDisk - POST /disks/commit?disk={disk}.
func CommitDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path := mux.Vars(r)["disk"]

	info, err := disk.GetImage(path)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	if len(info.BackingImages) == 0 {
		return weberror.New(http.StatusInternalServerError, fmt.Sprintf("image %s has no backing image to commit to", path))
	}

	committed := audit.Fields{File: path, Target: info.BackingImages[0]}

	if !role.Allowed("disks", "update", info.BackingImages[0]) {
		audit.Denied(r, audit.DiskCommit, "committing disk not allowed", committed)

		return weberror.New(http.StatusForbidden, "forbidden for "+info.BackingImages[0])
	}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		audit.Denied(r, audit.DiskCommit, "committing disk not allowed", committed)

		return weberror.New(http.StatusForbidden, "forbidden for "+path)
	}

	err = disk.CommitDisk(path)
	if err != nil {
		audit.Failure(r, audit.DiskCommit, "committing disk failed", committed, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.DiskCommit, "committed disk", committed)
	w.WriteHeader(http.StatusOK)

	return nil
}

// SnapshotDisk - POST /disks/snapshot?disk={disk}&new={new}
// disk should be absolute
// new may be absolute, but will be put in same dir as disk if not. Extension will be set to qcow2.
//
//nolint:dupl // similar to RenameDisk
func SnapshotDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path := mux.Vars(r)["disk"]
	newPath := normalizeDstDisk(path, mux.Vars(r)["new"])

	if !role.Allowed("disks", "create", filepath.Base(newPath)) {
		audit.Denied(r, audit.DiskSnapshot, "snapshotting disk not allowed", audit.Fields{File: path, Target: newPath})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := disk.SnapshotDisk(path, newPath)
	if err != nil {
		audit.Failure(r, audit.DiskSnapshot, "snapshotting disk failed", audit.Fields{File: path, Target: newPath}, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.DiskSnapshot, "snapshotted disk", audit.Fields{File: path, Target: newPath})
	w.WriteHeader(http.StatusOK)

	return nil
}

// RebaseDisk - POST /disks/rebase?disk={disk}&backing={backing}&unsafe={unsafe}
// disk and backing should be absolute.
func RebaseDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path := mux.Vars(r)["disk"]
	backing := mux.Vars(r)["backing"]

	unsafe, err := strconv.ParseBool(mux.Vars(r)["unsafe"])
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	rebased := audit.Fields{File: path, Target: backing, Extra: []any{"phenix.disk.unsafe", unsafe}}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		audit.Denied(r, audit.DiskRebase, "rebasing disk not allowed", rebased)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err = disk.RebaseDisk(path, backing, unsafe)
	if err != nil {
		audit.Failure(r, audit.DiskRebase, "rebasing disk failed", rebased, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.DiskRebase, "rebased disk", rebased)
	w.WriteHeader(http.StatusOK)

	return nil
}

// ResizeDisk - POST /disks/resize?disk={disk}&size={size}
// disk should be absolute. size should be a valid size (absolute or relative) per `qemu-img --help`.
func ResizeDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path := mux.Vars(r)["disk"]
	size := mux.Vars(r)["size"]

	resized := audit.Fields{File: path, Extra: []any{"phenix.disk.size", size}}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		audit.Denied(r, audit.DiskResize, "resizing disk not allowed", resized)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := disk.ResizeDisk(path, size)
	if err != nil {
		audit.Failure(r, audit.DiskResize, "resizing disk failed", resized, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.DiskResize, "resized disk", resized)
	w.WriteHeader(http.StatusOK)

	return nil
}

// CloneDisk - POST /disks/clone?disk={disk}&new={new}
// disk should be absolute
// new may be absolute, but will be put in same dir as disk if not. Extension will be set to qcow2.
func CloneDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path := mux.Vars(r)["disk"]
	newPath := normalizeDstDisk(path, mux.Vars(r)["new"])

	if !role.Allowed("disks", "create") {
		audit.Denied(r, audit.DiskClone, "cloning disk not allowed", audit.Fields{File: path, Target: newPath})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := disk.CloneDisk(path, newPath)
	if err != nil {
		audit.Failure(r, audit.DiskClone, "cloning disk failed", audit.Fields{File: path, Target: newPath}, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.DiskClone, "cloned disk", audit.Fields{File: path, Target: newPath})
	w.WriteHeader(http.StatusOK)

	return nil
}

// RenameDisk - POST /disks/rename?disk={disk}&new={new}
// disk should be absolute
// new may be absolute, but will be put in same dir as disk if not. Extension will be set to qcow2.
//
//nolint:dupl // similar to SnapshotDisk
func RenameDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path := mux.Vars(r)["disk"]
	newPath := normalizeDstDisk(path, mux.Vars(r)["new"])

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		audit.Denied(r, audit.DiskRename, "renaming disk not allowed", audit.Fields{File: path, Target: newPath})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := disk.RenameDisk(path, newPath)
	if err != nil {
		audit.Failure(r, audit.DiskRename, "renaming disk failed", audit.Fields{File: path, Target: newPath}, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.DiskRename, "renamed disk", audit.Fields{File: path, Target: newPath})
	w.WriteHeader(http.StatusOK)

	return nil
}

// DeleteDisk - DELETE /disks?disk={disk}
// disk should be absolute.
func DeleteDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path := mux.Vars(r)["disk"]

	if !role.Allowed("disks", "delete", filepath.Base(path)) {
		audit.Denied(r, audit.DiskDelete, "deleting disk not allowed", audit.Fields{File: path})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := disk.DeleteDisk(path)
	if err != nil {
		audit.Failure(r, audit.DiskDelete, "deleting disk failed", audit.Fields{File: path}, err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	audit.Success(r, audit.DiskDelete, "deleted disk", audit.Fields{File: path})
	w.WriteHeader(http.StatusOK)

	return nil
}

// UploadDisk - POST /disks.
func UploadDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	clientFile, handler, err := r.FormFile("file")

	if !role.Allowed("disks", "upload") {
		audit.Denied(r, audit.DiskUpload, "uploading disk not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if err != nil {
		plog.Error(plog.TypeSystem, err.Error())
		audit.Failure(r, audit.DiskUpload, "uploading disk failed", audit.Fields{}, err)

		return weberror.New(http.StatusInternalServerError, "Error uploading: "+err.Error())
	}

	defer func() { _ = clientFile.Close() }()

	target := mm.GetMMFullPath(handler.Filename)

	localFile, err := os.OpenFile( //nolint:gosec // Path traversal via taint analysis
		target,
		os.O_WRONLY|os.O_CREATE,
		0o600,
	)
	if err != nil {
		plog.Error(plog.TypeSystem, err.Error())
		audit.Failure(r, audit.DiskUpload, "uploading disk failed", audit.Fields{Target: target}, err)

		return weberror.New(http.StatusInternalServerError, "Error uploading: "+err.Error())
	}

	defer func() { _ = localFile.Close() }()

	written, err := io.Copy(localFile, clientFile)
	if err != nil {
		plog.Error(plog.TypeSystem, "uploading disk", "disk", localFile.Name(), "err", err)
		audit.Failure(r, audit.DiskUpload, "uploading disk failed", audit.Fields{Target: localFile.Name()}, err)

		return weberror.New(http.StatusInternalServerError, "Error uploading: "+err.Error())
	}

	audit.Success(r, audit.DiskUpload, "uploaded disk", audit.Fields{
		Target: localFile.Name(), Extra: []any{"file.size", written},
	})

	return nil
}

// DownloadDisk - GET /disks/download?disk={disk}
// disk may be relative to filedir or absolute. If absolute must be in the files dir.
func DownloadDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path := mux.Vars(r)["disk"]

	fileDir := mm.GetMMFilesDirectory()

	if !filepath.IsAbs(path) {
		path = filepath.Join(fileDir, path)
	} else if !strings.HasPrefix(path, fileDir) {
		errString := fmt.Sprintf("Error getting path %s: Path is not within files directory", path)
		plog.Error(plog.TypeSystem, errString)

		return weberror.New(http.StatusBadRequest, errString)
	}

	downloaded := audit.Fields{File: path}

	if !role.Allowed("disks", "get", filepath.Base(path)) {
		audit.Denied(r, audit.DiskDownload, "downloading disk not allowed", downloaded)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	served, err := util.ServeDownload(w, r, util.Download{
		Path:        path,
		Name:        filepath.Base(path),
		ContentType: "application/octet-stream",
		MaxSize:     0,
	})
	if err != nil {
		audit.Failure(r, audit.DiskDownload, "downloading disk failed", downloaded, err)

		if errors.Is(err, util.ErrNotRegular) {
			return weberror.New(http.StatusBadRequest, "Cannot download directory: "+path)
		}

		errString := fmt.Sprintf("Error getting path %s: %s", path, err.Error())
		plog.Error(plog.TypeSystem, errString)

		if errors.Is(err, fs.ErrNotExist) {
			return weberror.New(http.StatusNotFound, errString)
		}

		return weberror.New(http.StatusInternalServerError, errString)
	}

	// once per download, not per resumed range or HEAD
	if served.Started {
		if rng := r.Header.Get("Range"); rng != "" {
			downloaded.Extra = []any{"http.request.range", rng}
		}

		audit.Success(r, audit.DiskDownload, "downloaded disk", downloaded)
	}

	return nil
}

// for output disk names - makes absolute and adds qcow2 file extension.
func normalizeDstDisk(src, dst string) string {
	if !filepath.IsAbs(dst) {
		dst = filepath.Join(filepath.Dir(src), dst)
	}

	if !strings.HasSuffix(dst, ".qcow2") && !strings.HasSuffix(dst, ".qc2") {
		dst += ".qcow2"
	}

	return dst
}
