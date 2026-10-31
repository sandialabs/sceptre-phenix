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
	"phenix/web/weberror"
)

// diskChangeDebounce is how long images must stay unchanged before clients
// are told they changed; an upload writes continuously until it completes.
const diskChangeDebounce = 2 * time.Second

// flagTrue is the value of a true query flag or header.
const flagTrue = "true"

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

	// clones that never finished, as a restart leaves: those this process
	// did not start, in the files directory phenix uses now and, once
	// minimega gives it, in minimega's
	started := time.Now()
	sweep := func(dir string) {
		if removed, err := disk.RemoveStalePartials(dir, started); err != nil {
			plog.Warn(plog.TypeSystem, "removing partial disk clones", "dir", dir, "err", err)
		} else if removed > 0 {
			plog.Info(plog.TypeSystem, "removed partial disk clones", "dir", dir, "count", removed)
		}
	}

	dir := mm.GetMMFullPath("")
	sweep(dir)

	go func() {
		select {
		case <-ctx.Done():
		case <-mm.FilesDirectoryGiven():
			if given := mm.GetMMFullPath(""); given != dir {
				sweep(given)
			}
		}
	}()

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
			plog.Warn(plog.TypeSystem, "disk list will not update automatically", "dir", dir, "err", err)
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
// Lists the images the role may list, sorted by name and paged by pageNum and perPage (total counts all): one outside
// the minimega files directory only when the role may get an experiment using it, or it backs one the role sees.
// expName limits the topologies' images to that experiment's, which the role must be allowed to get.
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

	// the UI's refresh button asks for every image to be inspected again
	if query.Get("refresh") == flagTrue {
		disk.ClearCache()
	}

	disks, err := disk.GetImages(expName)
	if errors.Is(err, store.ErrNotExist) {
		return weberror.New(http.StatusNotFound, err.Error())
	}

	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
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

	body, err := json.Marshal(diskList{Disks: util.Page(allowed, page, pageSize), Total: len(allowed)})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// diskList is a page of GET /disks, and how many disks the role sees.
type diskList struct {
	Disks []disk.Details `json:"disks"`
	Total int            `json:"total"`
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
func CommitDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		return diskError(err)
	}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		audit.Denied(r, audit.DiskCommit, "committing disk not allowed", audit.Fields{File: path})

		return weberror.New(http.StatusForbidden, "forbidden for "+filepath.Base(path))
	}

	if err := disk.ValidateMinimegaPath(path); err != nil {
		return diskError(err)
	}

	info, err := disk.GetImage(path)
	if err != nil {
		return diskError(err)
	}

	if err := info.Refusal(); err != nil {
		return diskError(err)
	}

	if len(info.BackingImages) == 0 {
		return weberror.New(http.StatusInternalServerError, fmt.Sprintf("image %s has no backing image to commit to", path))
	}

	backing, err := disk.Resolve(info.BackingImages[0])
	if err != nil {
		return diskError(fmt.Errorf("committing into the backing image: %w", err))
	}

	committed := audit.Fields{File: path, Target: backing}

	if !role.Allowed("disks", "update", filepath.Base(backing)) {
		audit.Denied(r, audit.DiskCommit, "committing disk not allowed", committed)

		return weberror.New(http.StatusForbidden, "forbidden for "+filepath.Base(backing))
	}

	if err := disk.CommitDisk(path); err != nil {
		return diskFailed(r, audit.DiskCommit, "committing disk failed", committed, err)
	}

	audit.Success(r, audit.DiskCommit, "committed disk", committed)
	w.WriteHeader(http.StatusOK)

	return nil
}

// mayCopyDisk reports whether the role may snapshot or clone the disk at path
// to newPath: it must be allowed to create the new image and to get the one
// it reads, as a download of the copy would hand out what the source holds.
func mayCopyDisk(role rbac.Role, path, newPath string) bool {
	return role.Allowed("disks", "create", filepath.Base(newPath)) &&
		role.Allowed("disks", "get", filepath.Base(path))
}

// SnapshotDisk - POST /disks/snapshot?disk={disk}&new={new}
//
// disk is relative to the minimega files directory, or absolute; new is
// relative to disk's folder, or absolute. Both must be inside the files
// directory. new gets a .qcow2 extension unless it has .qc2 or .qcow2.
func SnapshotDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, err := diskAndNew(r)
	if err != nil {
		return diskError(err)
	}

	if !mayCopyDisk(role, path, newPath) {
		return diskForbidden(r, audit.DiskSnapshot, "snapshotting disk not allowed", audit.Fields{File: path, Target: newPath})
	}

	if err := disk.SnapshotDisk(path, newPath); err != nil {
		return diskFailed(r, audit.DiskSnapshot, "snapshotting disk failed", audit.Fields{File: path, Target: newPath}, err)
	}

	diskActionDone(r, audit.DiskSnapshot, "snapshotted disk", path, newPath)
	w.WriteHeader(http.StatusOK)

	return nil
}

// RebaseDisk - POST /disks/rebase?disk={disk}&backing={backing}&unsafe={unsafe}
//
// disk and backing (empty to make disk independent) are relative to the
// minimega files directory, or absolute, and must be inside it.
func RebaseDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	unsafe, err := strconv.ParseBool(mux.Vars(r)["unsafe"])
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		return diskError(err)
	}

	backing := mux.Vars(r)["backing"]
	if backing != "" {
		if backing, err = disk.Resolve(backing); err != nil {
			return diskError(err)
		}
	}

	rebased := audit.Fields{File: path, Target: backing, Extra: []any{"phenix.disk.unsafe", unsafe}}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		return diskForbidden(r, audit.DiskRebase, "rebasing disk not allowed", rebased)
	}

	if err := disk.RebaseDisk(path, backing, unsafe); err != nil {
		return diskFailed(r, audit.DiskRebase, "rebasing disk failed", rebased, err)
	}

	audit.Success(r, audit.DiskRebase, "rebased disk", rebased)
	w.WriteHeader(http.StatusOK)

	return nil
}

// ResizeDisk - POST /disks/resize?disk={disk}&size={size}
//
// disk is relative to the minimega files directory, or absolute, and must be
// inside it. size should be a valid size (absolute or relative) per `qemu-img
// --help`.
func ResizeDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	size := mux.Vars(r)["size"]

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		return diskError(err)
	}

	resized := audit.Fields{File: path, Extra: []any{"phenix.disk.size", size}}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		return diskForbidden(r, audit.DiskResize, "resizing disk not allowed", resized)
	}

	if err := disk.ResizeDisk(path, size); err != nil {
		return diskFailed(r, audit.DiskResize, "resizing disk failed", resized, err)
	}

	audit.Success(r, audit.DiskResize, "resized disk", resized)
	w.WriteHeader(http.StatusOK)

	return nil
}

// CloneDisk - POST /disks/clone?disk={disk}&new={new}
//
// disk and new are as for SnapshotDisk. The copy never replaces a file.
func CloneDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, err := diskAndNew(r)
	if err != nil {
		return diskError(err)
	}

	if !mayCopyDisk(role, path, newPath) {
		return diskForbidden(r, audit.DiskClone, "cloning disk not allowed", audit.Fields{File: path, Target: newPath})
	}

	if err := disk.CloneDisk(path, newPath); err != nil {
		return diskFailed(r, audit.DiskClone, "cloning disk failed", audit.Fields{File: path, Target: newPath}, err)
	}

	diskActionDone(r, audit.DiskClone, "cloned disk", path, newPath)
	w.WriteHeader(http.StatusOK)

	return nil
}

// cloneKind is the kind of the background jobs that clone a disk image.
const cloneKind = "disk.clone"

// CloneDiskJob - POST /disks/clone/jobs?disk={disk}&new={new}
//
// Clones as CloneDisk does in a cancelable background job reporting the bytes copied, answering 202 with the job once
// the request checks out. The clone is audited when the job finishes (see auditCloneJob).
func CloneDiskJob(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, err := diskAndNew(r)
	if err != nil {
		return diskError(err)
	}

	cloned := audit.Fields{File: path, Target: newPath}

	if !mayCopyDisk(role, path, newPath) {
		return diskForbidden(r, audit.DiskClone, "cloning disk not allowed", cloned)
	}

	// a clone CloneDisk would refuse fails before the job starts, as there
	size, err := disk.CloneSize(path, newPath)
	if err != nil {
		return diskFailed(r, audit.DiskClone, "cloning disk failed", cloned, err)
	}

	newName := filepath.Base(newPath)

	return startJob(w, r, job.Spec{
		Kind:       cloneKind,
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

			return nil
		},
	})
}

// auditCloneJob audits, as the user who started it, the clone a finished
// clone job made, or failed to make: a job canceled before its copy finished
// made none.
func auditCloneJob(j job.Job) {
	cloned := audit.Fields{File: j.Params["disk"], Target: j.Params["new"], Extra: []any{"phenix.job.id", j.ID}}

	if succeeded, _, _, _ := j.Counts(); succeeded > 0 {
		audit.Background(j.Owner, audit.DiskClone, "cloned disk", audit.OutcomeSuccess, cloned)

		return
	}

	audit.BackgroundFailure(j.Owner, audit.DiskClone, "cloning disk failed", cloned, jobError(j))
}

// RenameDisk - POST /disks/rename?disk={disk}&new={new}
//
// disk and new are as for SnapshotDisk. The rename never replaces a file.
func RenameDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, err := diskAndNew(r)
	if err != nil {
		return diskError(err)
	}

	if !role.Allowed("disks", "update", filepath.Base(path)) {
		return diskForbidden(r, audit.DiskRename, "renaming disk not allowed", audit.Fields{File: path, Target: newPath})
	}

	if err := disk.RenameDisk(path, newPath); err != nil {
		return diskFailed(r, audit.DiskRename, "renaming disk failed", audit.Fields{File: path, Target: newPath}, err)
	}

	diskActionDone(r, audit.DiskRename, "renamed disk", path, newPath)
	w.WriteHeader(http.StatusOK)

	return nil
}

// DeleteDisk - DELETE /disks?disk={disk}
//
// disk is relative to the minimega files directory, or absolute, and must be
// inside it.
func DeleteDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		return diskError(err)
	}

	if !role.Allowed("disks", "delete", filepath.Base(path)) {
		return diskForbidden(r, audit.DiskDelete, "deleting disk not allowed", audit.Fields{File: path})
	}

	if err := disk.DeleteDisk(path); err != nil {
		return diskFailed(r, audit.DiskDelete, "deleting disk failed", audit.Fields{File: path}, err)
	}

	audit.Success(r, audit.DiskDelete, "deleted disk", audit.Fields{File: path})
	w.WriteHeader(http.StatusOK)

	return nil
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

// diskError is err with the status its cause calls for.
func diskError(err error) error {
	status := http.StatusInternalServerError

	switch {
	case errors.Is(err, disk.ErrNotManaged), errors.Is(err, disk.ErrUnsafeName):
		status = http.StatusBadRequest
	case errors.Is(err, disk.ErrExists):
		status = http.StatusConflict
	case errors.Is(err, fs.ErrNotExist):
		status = http.StatusNotFound
	}

	return weberror.New(status, err.Error())
}

// diskForbidden audits, as msg with fields f, a disk action a the role may not
// take, and returns its 403.
func diskForbidden(r *http.Request, a audit.Action, msg string, f audit.Fields) error {
	audit.Denied(r, a, msg, f)

	return weberror.New(http.StatusForbidden, "forbidden")
}

// diskFailed audits, as msg with fields f, a disk action a that failed with
// err, and returns diskError's error.
func diskFailed(r *http.Request, a audit.Action, msg string, f audit.Fields, err error) error {
	audit.Failure(r, a, msg, f, err)

	return diskError(err)
}

// diskActionDone audits a disk action a that made image to from image from.
func diskActionDone(r *http.Request, a audit.Action, msg, from, to string) {
	audit.Success(r, a, msg, audit.Fields{File: from, Target: to})
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

	localFile, err := os.OpenFile( //nolint:gosec // G703 multipart keeps only the base of the file name
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
//
// disk is relative to the minimega files directory, or absolute; either way it
// must be within that directory, and not in a folder the disk list leaves
// out.
func DownloadDisk(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, err := disk.Resolve(mux.Vars(r)["disk"])
	if err != nil {
		plog.Error(plog.TypeSystem, "downloading disk", "err", err)
		return diskError(err)
	}

	downloaded := audit.Fields{File: path}

	if !role.Allowed("disks", "get", filepath.Base(path)) {
		return diskForbidden(r, audit.DiskDownload, "downloading disk not allowed", downloaded)
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

		return diskError(fmt.Errorf("disk image: %w", err))
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

// GetDiskChecksum - GET /disks/checksum?disk=.
//
// It answers with the image's SHA-256, computed in the background: 202 until
// it is known, then 200 (see util.ServeChecksum).
func GetDiskChecksum(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	name := mux.Vars(r)["disk"]

	path, err := disk.Resolve(name)
	if err != nil {
		return diskError(err)
	}

	if !role.Allowed("disks", "get", filepath.Base(path)) {
		return diskForbidden(r, audit.DiskDownload, "getting disk checksum not allowed", audit.Fields{File: path})
	}

	err = util.ServeChecksum(w, path, name)

	switch {
	case err == nil:
		return nil
	case errors.Is(err, util.ErrNotRegular):
		return weberror.New(http.StatusBadRequest, "Cannot get checksum of directory: "+path)
	case errors.Is(err, util.ErrChangedWhileHashing):
		return weberror.New(http.StatusConflict, err.Error())
	default:
		plog.Error(plog.TypeSystem, "getting disk checksum", "disk", path, "err", err)

		return diskError(fmt.Errorf("disk image: %w", err))
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
