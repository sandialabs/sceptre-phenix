package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/disk"
	"phenix/api/job"
	"phenix/store"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

// diskChangeDebounce is how long images must stay unchanged before clients
// are told they changed; an upload writes continuously until it completes.
const diskChangeDebounce = 2 * time.Second

// WatchDisks inspects disk images in the background (when phenix inspects them
// itself; see disk.LocalInspection), at startup and whenever images in the
// minimega files directory change; after a change it also tells clients
// allowed to list disks to reload them. Listing disks then only waits on
// images that changed in the meantime.
func WatchDisks(ctx context.Context) {
	warm := func() {
		// without the cache (no qemu-img), warming would only add minimega load
		if !disk.LocalInspection() {
			return
		}

		if _, err := disk.GetImages(""); err != nil {
			plog.Warn(plog.TypeSystem, "inspecting disk images", "err", err)
		}
	}

	go warm()

	// clones that never finished, as a restart leaves
	dir := mm.GetMMFullPath("")
	if removed, err := disk.RemoveStalePartials(dir); err != nil {
		plog.Warn(plog.TypeSystem, "removing partial disk clones", "dir", dir, "err", err)
	} else if removed > 0 {
		plog.Info(plog.TypeSystem, "removed partial disk clones", "dir", dir, "count", removed)
	}

	watchFilesDirectory(ctx, mm.FilesDirectoryGiven(), diskChangeDebounce, func() {
		warm()
		broker.Broadcast(
			bt.NewRequestPolicy("disks", "list", ""),
			bt.NewResource("disks", "", "update"),
			json.RawMessage("{}"),
		)
	})
}

// watchFilesDirectory calls changed once images in the minimega files
// directory stop changing for debounce (see disk.Watch), until ctx is done.
// Until minimega gives its files directory, which closes given, phenix uses
// <PhenixBase>/images; if minimega's differs, the watch moves to it and
// changed is called, as the disk list changes with it.
func watchFilesDirectory(ctx context.Context, given <-chan struct{}, debounce time.Duration, changed func()) {
	watch := func(dir string) context.CancelFunc {
		watchCtx, stop := context.WithCancel(ctx)

		if err := disk.Watch(watchCtx, dir, debounce, changed); err != nil {
			plog.Warn(plog.TypeSystem, "disk list will not update on its own", "dir", dir, "err", err)
		}

		return stop
	}

	dir := mm.GetMMFullPath("")
	stop := watch(dir)

	go func() {
		select {
		case <-ctx.Done():
		case <-given:
			if moved := mm.GetMMFullPath(""); moved != dir {
				stop()
				stop = watch(moved)
				changed()
			}

			<-ctx.Done()
		}

		stop()
	}()
}

// GetDisks - GET /disks.
//
// Lists the images whose names the role may list: a role sees an image
// outside the minimega files directory only when it may get an experiment
// using it, or when the image backs one it sees. expName limits the images
// topologies name to that experiment's, which the role must be allowed to get.
func GetDisks(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetDisks")

	var (
		ctx             = r.Context()
		role, _         = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		query           = r.URL.Query()
		expName         = query.Get("expName")
		diskType        = query.Get("diskType")
		defaultDiskType = disk.VMImage | disk.ContainerImage | disk.ISOImage | disk.UNKNOWN
	)

	if !role.Allowed("disks", "list") || expName != "" && !role.Allowed("experiments", "get", expName) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"listing disks not allowed",
			"user",
			user,
			"exp",
			expName,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if len(diskType) > 0 {
		defaultDiskType = 0
		for s := range strings.SplitSeq(diskType, ",") {
			defaultDiskType |= disk.StringToKind(s)
		}
	}

	// the UI's refresh button asks for every image to be inspected again
	if query.Get("refresh") == "true" {
		disk.ClearCache()
	}

	disks, err := disk.GetImages(expName)
	if errors.Is(err, store.ErrNotExist) {
		http.Error(w, err.Error(), http.StatusNotFound)

		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	filtered := []disk.Details{}

	for _, disk := range disks {
		if disk.Kind&defaultDiskType != 0 && role.Allowed("disks", "list", disk.Name) {
			filtered = append(filtered, disk)
		}
	}

	allowed := visibleDisks(role, filtered)

	slices.SortFunc(allowed, func(a, b disk.Details) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.FullPath, b.FullPath))
	})

	body, err := json.Marshal(util.WithRoot("disks", allowed))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// visibleDisks returns the disks the role may see: those inside the minimega
// files directory, those outside it that an experiment the role may get uses,
// and those backing a disk it sees (whose backing images name them anyway).
// Each disk's experiments are cut to the ones the role may list.
func visibleDisks(role rbac.Role, disks []disk.Details) []disk.Details {
	var (
		visible = make([]disk.Details, 0, len(disks))
		held    = make(map[string]disk.Details)
	)

	for _, d := range disks {
		readable := slices.ContainsFunc(d.Experiments, func(use disk.ExperimentUse) bool {
			return role.Allowed("experiments", "get", use.Name)
		})

		if d.OutsideFilesDir && !readable {
			held[d.FullPath] = d
		} else {
			visible = append(visible, d)
		}
	}

	// every disk's backing images are its whole chain, so one pass admits all
	for _, d := range slices.Clone(visible) {
		for _, backing := range d.BackingImages {
			if held, ok := held[backing]; ok {
				visible = append(visible, held)
			}

			delete(held, backing)
		}
	}

	for i, d := range visible {
		var uses []disk.ExperimentUse

		for _, use := range d.Experiments {
			if role.Allowed("experiments", "list", use.Name) {
				uses = append(uses, use)
			}
		}

		visible[i].Experiments = uses
	}

	return visible
}

// CommitDisk - POST /disks/commit?disk={disk}.
//
// Writes the image into its backing image; both must be inside the minimega
// files directory, and the role must be allowed to update both.
func CommitDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		diskError(w, err)

		return
	}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		audit.Denied(r, audit.DiskCommit, "committing disk not allowed", audit.Fields{File: path})
		http.Error(w, "forbidden for "+filepath.Base(path), http.StatusForbidden)

		return
	}

	if err := disk.ValidateMinimegaPath(path); err != nil {
		diskError(w, err)

		return
	}

	info, err := disk.GetImage(path)
	if err != nil {
		diskError(w, err)

		return
	}

	if len(info.BackingImages) == 0 {
		http.Error(
			w,
			fmt.Sprintf("image %s has no backing image to commit to", path),
			http.StatusInternalServerError,
		)

		return
	}

	backing, err := disk.Resolve(info.BackingImages[0])
	if err != nil {
		diskError(w, fmt.Errorf("committing into the backing image: %w", err))

		return
	}

	committed := audit.Fields{File: path, Target: backing}

	if !role.Allowed("disks", "update", filepath.Base(backing)) {
		audit.Denied(r, audit.DiskCommit, "committing disk not allowed", committed)
		http.Error(w, "forbidden for "+filepath.Base(backing), http.StatusForbidden)

		return
	}

	if err := disk.CommitDisk(path); err != nil {
		diskFailed(w, r, audit.DiskCommit, "committing disk failed", committed, err)

		return
	}

	audit.Success(r, audit.DiskCommit, "committed disk", committed)
	w.WriteHeader(http.StatusOK)
}

// SnapshotDisk - POST /disks/snapshot?disk={disk}&new={new}
//
// disk is relative to the minimega files directory, or absolute; new is
// relative to disk's folder, or absolute. Both must be inside the files
// directory. new gets a .qcow2 extension unless it has .qc2 or .qcow2.
func SnapshotDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, err := diskAndNew(r)
	if err != nil {
		diskError(w, err)

		return
	}

	if !role.Allowed("disks", "create", filepath.Base(newPath)) {
		diskForbidden(w, r, audit.DiskSnapshot, "snapshotting disk not allowed", audit.Fields{File: path, Target: newPath})

		return
	}

	if err := disk.SnapshotDisk(path, newPath); err != nil {
		diskFailed(w, r, audit.DiskSnapshot, "snapshotting disk failed", audit.Fields{File: path, Target: newPath}, err)

		return
	}

	diskActionDone(r, audit.DiskSnapshot, "snapshotted disk", path, newPath)
	w.WriteHeader(http.StatusOK)
}

// RebaseDisk - POST /disks/rebase?disk={disk}&backing={backing}&unsafe={unsafe}
//
// disk and backing (empty to make disk independent) are relative to the
// minimega files directory, or absolute, and must be inside it.
func RebaseDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	unsafe, err := strconv.ParseBool(mux.Vars(r)["unsafe"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		diskError(w, err)

		return
	}

	backing := mux.Vars(r)["backing"]
	if backing != "" {
		if backing, err = disk.Resolve(backing); err != nil {
			diskError(w, err)

			return
		}
	}

	rebased := audit.Fields{File: path, Target: backing, Extra: []any{"phenix.disk.unsafe", unsafe}}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		diskForbidden(w, r, audit.DiskRebase, "rebasing disk not allowed", rebased)

		return
	}

	if err := disk.RebaseDisk(path, backing, unsafe); err != nil {
		diskFailed(w, r, audit.DiskRebase, "rebasing disk failed", rebased, err)

		return
	}

	audit.Success(r, audit.DiskRebase, "rebased disk", rebased)
	w.WriteHeader(http.StatusOK)
}

// ResizeDisk - POST /disks/resize?disk={disk}&size={size}
//
// disk is relative to the minimega files directory, or absolute, and must be
// inside it. size should be a valid size (absolute or relative) per `qemu-img
// --help`.
func ResizeDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	size := mux.Vars(r)["size"]

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		diskError(w, err)

		return
	}

	resized := audit.Fields{File: path, Extra: []any{"phenix.disk.size", size}}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		diskForbidden(w, r, audit.DiskResize, "resizing disk not allowed", resized)

		return
	}

	if err := disk.ResizeDisk(path, size); err != nil {
		diskFailed(w, r, audit.DiskResize, "resizing disk failed", resized, err)

		return
	}

	audit.Success(r, audit.DiskResize, "resized disk", resized)
	w.WriteHeader(http.StatusOK)
}

// CloneDisk - POST /disks/clone?disk={disk}&new={new}
//
// disk and new are as for SnapshotDisk. The copy never replaces a file.
func CloneDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, err := diskAndNew(r)
	if err != nil {
		diskError(w, err)

		return
	}

	if !role.Allowed("disks", "create", filepath.Base(newPath)) {
		diskForbidden(w, r, audit.DiskClone, "cloning disk not allowed", audit.Fields{File: path, Target: newPath})

		return
	}

	if err := disk.CloneDisk(path, newPath); err != nil {
		diskFailed(w, r, audit.DiskClone, "cloning disk failed", audit.Fields{File: path, Target: newPath}, err)

		return
	}

	diskActionDone(r, audit.DiskClone, "cloned disk", path, newPath)
	w.WriteHeader(http.StatusOK)
}

// CloneDiskJob - POST /disks/clone/jobs?disk={disk}&new={new}
//
// Clones as CloneDisk does, in a background job that reports the bytes
// copied and can be canceled, leaving no file. It checks the request as
// CloneDisk does before answering 202 with the job.
func CloneDiskJob(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, err := diskAndNew(r)
	if err != nil {
		diskError(w, err)

		return
	}

	if !role.Allowed("disks", "create", filepath.Base(newPath)) {
		diskForbidden(w, r, "cloning disk not allowed", "from_disk", path, "to_disk", newPath)

		return
	}

	size, err := disk.CloneSize(path, newPath)
	if err != nil {
		diskError(w, err)

		return
	}

	var (
		user    = middleware.UserFromContext(r.Context())
		newName = filepath.Base(newPath)
	)

	startJob(w, r, job.Spec{
		Kind:       "disk.clone",
		Owner:      "",
		Experiment: "",
		Title:      fmt.Sprintf("Clone %s to %s", filepath.Base(path), newName),
		Params:     map[string]string{"disk": path, "new": newPath},
		Items:      []string{newName},
		BytesTotal: size,
		Cancelable: true,
		Run: func(ctx context.Context, run *job.Run) error {
			run.ItemStarted(newName)

			if err := disk.CloneDiskContext(ctx, path, newPath, run.AddBytes); err != nil {
				if ctx.Err() != nil {
					return ctx.Err() //nolint:wrapcheck // the job is canceled
				}

				run.ItemFailed(newName, err)

				return nil
			}

			run.ItemSucceeded(newName, nil)
			plog.Info(plog.TypeAction, "cloned disk", "user", user, "from_disk", path, "to_disk", newPath, "job", run.ID())

			return nil
		},
	})
}

// RenameDisk - POST /disks/rename?disk={disk}&new={new}
//
// disk and new are as for SnapshotDisk. The rename never replaces a file.
func RenameDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, err := diskAndNew(r)
	if err != nil {
		diskError(w, err)

		return
	}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		diskForbidden(w, r, audit.DiskRename, "renaming disk not allowed", audit.Fields{File: path, Target: newPath})

		return
	}

	if err := disk.RenameDisk(path, newPath); err != nil {
		diskFailed(w, r, audit.DiskRename, "renaming disk failed", audit.Fields{File: path, Target: newPath}, err)

		return
	}

	diskActionDone(r, audit.DiskRename, "renamed disk", path, newPath)
	w.WriteHeader(http.StatusOK)
}

// DeleteDisk - DELETE /disks?disk={disk}
//
// disk is relative to the minimega files directory, or absolute, and must be
// inside it.
func DeleteDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		diskError(w, err)

		return
	}

	if !role.Allowed("disks", "delete", filepath.Base(path)) {
		diskForbidden(w, r, audit.DiskDelete, "deleting disk not allowed", audit.Fields{File: path})

		return
	}

	if err := disk.DeleteDisk(path); err != nil {
		diskFailed(w, r, audit.DiskDelete, "deleting disk failed", audit.Fields{File: path}, err)

		return
	}

	audit.Success(r, audit.DiskDelete, "deleted disk", audit.Fields{File: path})
	w.WriteHeader(http.StatusOK)
}

// diskAndNew resolves an action's disk and new query values (see
// SnapshotDisk). new must name a file, not a folder, which the added
// extension would turn into a file beside that folder.
func diskAndNew(r *http.Request) (string, string, error) {
	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		return "", "", err
	}

	dst := mux.Vars(r)["new"]
	if last := filepath.Base(dst); strings.HasSuffix(dst, "/") || last == "." || last == ".." {
		return "", "", fmt.Errorf("%w: new image %q does not name a file", disk.ErrNotManaged, dst)
	}

	newPath, err := disk.Resolve(normalizeDstDisk(path, dst))
	if err != nil {
		return "", "", err
	}

	return path, newPath, nil
}

// diskError answers with err and the status its cause calls for.
func diskError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError

	switch {
	case errors.Is(err, disk.ErrNotManaged), errors.Is(err, disk.ErrUnsafeName):
		status = http.StatusBadRequest
	case errors.Is(err, disk.ErrExists):
		status = http.StatusConflict
	case errors.Is(err, fs.ErrNotExist):
		status = http.StatusNotFound
	}

	http.Error(w, err.Error(), status)
}

// diskForbidden audits, as msg with fields f, and answers a disk action a the
// role may not take.
func diskForbidden(w http.ResponseWriter, r *http.Request, a audit.Action, msg string, f audit.Fields) {
	audit.Denied(r, a, msg, f)
	http.Error(w, "forbidden", http.StatusForbidden)
}

// diskFailed audits, as msg with fields f, and answers a disk action a that
// failed with err.
func diskFailed(w http.ResponseWriter, r *http.Request, a audit.Action, msg string, f audit.Fields, err error) {
	audit.Failure(r, a, msg, f, err)
	diskError(w, err)
}

// diskActionDone audits a disk action a that made image to from image from.
func diskActionDone(r *http.Request, a audit.Action, msg, from, to string) {
	audit.Success(r, a, msg, audit.Fields{File: from, Target: to})
}

// UploadDisk - POST /disks.
func UploadDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	clientFile, handler, err := r.FormFile("file")

	if !role.Allowed("disks", "upload") {
		audit.Denied(r, audit.DiskUpload, "uploading disk not allowed", audit.Fields{})
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if err != nil {
		plog.Error(plog.TypeSystem, err.Error())
		audit.Failure(r, audit.DiskUpload, "uploading disk failed", audit.Fields{}, err)
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)

		return
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
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)

		return
	}

	defer func() { _ = localFile.Close() }()

	written, err := io.Copy(localFile, clientFile)
	if err != nil {
		plog.Error(plog.TypeSystem, "uploading disk", "disk", localFile.Name(), "err", err)
		audit.Failure(r, audit.DiskUpload, "uploading disk failed", audit.Fields{Target: localFile.Name()}, err)
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)

		return
	}

	audit.Success(r, audit.DiskUpload, "uploaded disk", audit.Fields{
		Target: localFile.Name(), Extra: []any{"file.size", written},
	})
}

// DownloadDisk - GET /disks/download?disk={disk}
//
// disk is relative to the minimega files directory, or absolute; either way it
// must be within that directory, and not in a folder the disk list leaves
// out.
func DownloadDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		plog.Error(plog.TypeSystem, "downloading disk", "err", err)
		diskError(w, err)

		return
	}

	downloaded := audit.Fields{File: path}

	if !role.Allowed("disks", "get", filepath.Base(path)) {
		diskForbidden(w, r, audit.DiskDownload, "downloading disk not allowed", downloaded)

		return
	}

	served, err := util.ServeDownload(w, r, util.Download{
		Path:        path,
		Name:        filepath.Base(path),
		ContentType: "application/octet-stream",
		Live:        false,
		MaxSize:     0,
	})
	if err != nil {
		audit.Failure(r, audit.DiskDownload, "downloading disk failed", downloaded, err)

		if errors.Is(err, util.ErrNotRegular) {
			http.Error(w, "Can't download directory: "+path, http.StatusBadRequest)

			return
		}

		diskError(w, fmt.Errorf("disk image: %w", err))

		return
	}

	// once per download, not per resumed range or HEAD
	if served.Started {
		if rng := r.Header.Get("Range"); rng != "" {
			downloaded.Extra = []any{"http.request.range", rng}
		}

		audit.Success(r, audit.DiskDownload, "downloaded disk", downloaded)
	}
}

// GetDiskChecksum - GET /disks/checksum?disk=.
//
// It answers with the image's SHA-256, computed in the background: 202 until
// it is known, then 200 (see util.ServeChecksum).
func GetDiskChecksum(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	name := mux.Vars(r)["disk"]

	path, err := disk.Resolve(name)
	if err != nil {
		diskError(w, err)

		return
	}

	if !role.Allowed("disks", "get", filepath.Base(path)) {
		diskForbidden(w, r, audit.DiskDownload, "getting disk checksum not allowed", audit.Fields{File: path})

		return
	}

	err = util.ServeChecksum(w, path, name)

	switch {
	case err == nil:
	case errors.Is(err, util.ErrNotRegular):
		http.Error(w, "Can't checksum directory: "+path, http.StatusBadRequest)
	case errors.Is(err, util.ErrChangedWhileHashing):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		plog.Error(plog.TypeSystem, "getting disk checksum", "disk", path, "err", err)
		diskError(w, fmt.Errorf("disk image: %w", err))
	}
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
