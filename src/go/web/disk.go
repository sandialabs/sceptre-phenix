package web

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"phenix/api/disk"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

// GetDisks - GET /disks.
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

	if !role.Allowed("disks", "list") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"listing disks not allowed",
			"user",
			user,
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

	disks, err := disk.GetImages(expName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
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

	body, err := json.Marshal(util.WithRoot("disks", allowed))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

var (
	errDiskOutsideFiles = errors.New("disk is not within the minimega files directory")
	errDiskPathDollar   = errors.New("disk path must not contain '$'")
	errDiskFormat       = errors.New("disk image format is not qcow2 or raw")
)

// uploadedDiskPerm is the mode of a disk file created by an upload.
const uploadedDiskPerm fs.FileMode = 0o600

// CommitDisk - POST /disks/commit?disk={disk}.
func CommitDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, ok := diskPaths(w, mux.Vars(r)["disk"])
	if !ok {
		return
	}

	info, err := disk.GetImage(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

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

	if !role.Allowed("disks", "update", info.BackingImages[0]) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"committing disk not allowed",
			"user",
			user,
			"from_disk",
			path,
			"to_disk",
			info.BackingImages[0],
		)
		http.Error(w, "forbidden for "+info.BackingImages[0], http.StatusForbidden)

		return
	}

	if !diskAllowed(role, "update", path) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"committing disk not allowed",
			"user",
			user,
			"from_disk",
			path,
			"to_disk",
			info.BackingImages[0],
		)
		http.Error(w, "forbidden for "+path, http.StatusForbidden)

		return
	}

	err = chainInFiles(path, info)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	err = disk.CommitDisk(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"committed disk",
		"user",
		user,
		"from_disk",
		path,
		"to_disk",
		info.BackingImages[0],
	)
	w.WriteHeader(http.StatusOK)
}

// SnapshotDisk - POST /disks/snapshot?disk={disk}&new={new}
// disk should be absolute
// new may be absolute, but will be put in same dir as disk if not. Extension will be set to qcow2.
//
//nolint:dupl // similar to CloneDisk
func SnapshotDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, ok := diskPathPair(w, mux.Vars(r)["disk"], mux.Vars(r)["new"])
	if !ok {
		return
	}

	if !diskAllowed(role, "get", path) || !destAllowed(role, newPath) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"snapshotting disk not allowed",
			"user",
			user,
			"from_disk",
			path,
			"to_disk",
			newPath,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := disk.SnapshotDisk(path, newPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"snapshotted disk",
		"user",
		user,
		"from_disk",
		path,
		"to_disk",
		newPath,
	)
	w.WriteHeader(http.StatusOK)
}

// RebaseDisk - POST /disks/rebase?disk={disk}&backing={backing}&unsafe={unsafe}
// disk and backing should be absolute.
func RebaseDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path, ok := diskPaths(w, mux.Vars(r)["disk"])
	if !ok {
		return
	}

	// An empty backing file makes the disk standalone.
	backing := mux.Vars(r)["backing"]
	if backing != "" {
		if backing, ok = diskPaths(w, backing); !ok {
			return
		}
	}

	unsafe, err := strconv.ParseBool(mux.Vars(r)["unsafe"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	if !diskAllowed(role, "update", path) || (backing != "" && !diskAllowed(role, "get", backing)) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"rebasing disk not allowed",
			"user",
			user,
			"disk",
			path,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// A safe rebase reads the old backing chain and the new one.
	if !unsafe {
		for _, p := range []string{path, backing} {
			if p == "" {
				continue
			}

			info, err := disk.GetImage(p)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}

			err = chainInFiles(p, info)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)

				return
			}
		}
	}

	err = disk.RebaseDisk(path, backing, unsafe)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"rebased disk",
		"user",
		user,
		"disk",
		path,
		"onto",
		backing,
		"unsafe",
		unsafe,
	)
	w.WriteHeader(http.StatusOK)
}

// ResizeDisk - POST /disks/resize?disk={disk}&size={size}
// disk should be absolute. size should be a valid size (absolute or relative) per `qemu-img --help`.
func ResizeDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	size := mux.Vars(r)["size"]

	path, ok := diskPaths(w, mux.Vars(r)["disk"])
	if !ok {
		return
	}

	if !diskAllowed(role, "update", path) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"resizing disk not allowed",
			"user",
			user,
			"disk",
			path,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// Resizing an image also resizes the files it keeps its data in.
	info, err := disk.GetImage(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	err = chainInFiles(path, info)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	err = disk.ResizeDisk(path, size)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"resized disk",
		"user",
		user,
		"disk",
		path,
		"size",
		size,
	)
	w.WriteHeader(http.StatusOK)
}

// CloneDisk - POST /disks/clone?disk={disk}&new={new}
// disk should be absolute
// new may be absolute, but will be put in same dir as disk if not. Extension will be set to qcow2.
//
//nolint:dupl // similar to SnapshotDisk
func CloneDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, ok := diskPathPair(w, mux.Vars(r)["disk"], mux.Vars(r)["new"])
	if !ok {
		return
	}

	if !diskAllowed(role, "get", path) || !destAllowed(role, newPath) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"cloning disk not allowed",
			"user",
			user,
			"from_disk",
			path,
			"to_disk",
			newPath,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := disk.CloneDisk(path, newPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"cloned disk",
		"user",
		user,
		"from_disk",
		path,
		"to_disk",
		newPath,
	)
	w.WriteHeader(http.StatusOK)
}

// RenameDisk - POST /disks/rename?disk={disk}&new={new}
// disk should be absolute
// new may be absolute, but will be put in same dir as disk if not. Extension will be set to qcow2.
func RenameDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, newPath, ok := diskPathPair(w, mux.Vars(r)["disk"], mux.Vars(r)["new"])
	if !ok {
		return
	}

	if !role.Allowed("disks", "update", filepath.Base(path)) || !destAllowed(role, newPath) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"renaming disk not allowed",
			"user",
			user,
			"from_disk",
			path,
			"to_disk",
			newPath,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := disk.RenameDisk(path, newPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"renamed disk",
		"user",
		user,
		"from_disk",
		path,
		"to_disk",
		newPath,
	)
	w.WriteHeader(http.StatusOK)
}

// DeleteDisk - DELETE /disks?disk={disk}
// disk should be absolute.
func DeleteDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	path, ok := diskPaths(w, mux.Vars(r)["disk"])
	if !ok {
		return
	}

	if !role.Allowed("disks", "delete", filepath.Base(path)) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"deleting disk not allowed",
			"user",
			user,
			"disk",
			path,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := disk.DeleteDisk(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"deleted disk",
		"user",
		user,
		"disk",
		path,
	)
	w.WriteHeader(http.StatusOK)
}

// UploadDisk - POST /disks.
func UploadDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	if !role.Allowed("disks", "upload") {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"uploading disk not allowed",
			"user",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	clientFile, handler, err := r.FormFile("file")
	if err != nil {
		plog.Error(plog.TypeSystem, err.Error())
		http.Error(w, "Error uploading: "+err.Error(), http.StatusBadRequest)

		return
	}

	defer func() { _ = clientFile.Close() }()

	// mime/multipart already strips directories from the file name; refuse the
	// names that would still resolve to the files directory or its parent.
	name := filepath.Base(handler.Filename)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		http.Error(w, "Error uploading: invalid file name", http.StatusBadRequest)

		return
	}

	path, ok := diskPaths(w, name)
	if !ok {
		return
	}

	localFile, err := openInFiles(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, uploadedDiskPerm)
	if err != nil {
		plog.Error(plog.TypeSystem, err.Error())
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)

		return
	}

	_, err = io.Copy(localFile, clientFile)
	if closeErr := localFile.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "writing uploaded disk", "disk", path, "err", err)
		http.Error(w, "Error uploading: "+err.Error(), http.StatusInternalServerError)

		return
	}

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"uploaded disk",
		"user",
		user,
		"disk",
		path,
	)
}

// DownloadDisk - GET /disks?disk={disk}
// disk may be relative to filedir or absolute. If absolute must be in the files dir.
func DownloadDisk(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	path, ok := diskPaths(w, mux.Vars(r)["disk"])
	if !ok {
		return
	}

	if !diskAllowed(role, "get", path) {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"downloading disk not allowed",
			"user",
			user,
			"disk",
			path,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	file, err := openInFiles(path, os.O_RDONLY, 0)
	if err != nil {
		errString := fmt.Sprintf("Error getting path %s: %s", path, err.Error())
		plog.Error(plog.TypeSystem, errString)
		http.Error(w, errString, http.StatusInternalServerError)

		return
	}

	defer func() { _ = file.Close() }()

	fileInfo, err := file.Stat()
	if err != nil {
		errString := fmt.Sprintf("Error getting path %s: %s", path, err.Error())
		plog.Error(plog.TypeSystem, errString)
		http.Error(w, errString, http.StatusInternalServerError)

		return
	}

	if fileInfo.IsDir() {
		http.Error(w, "Can't download directory: "+path, http.StatusBadRequest)

		return
	}

	// Name the download as requested, not as the target of a symlink.
	name := filepath.Base(path)

	plog.Info(plog.TypeSystem, "download for file", "file", name)

	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, name, fileInfo.ModTime(), file)
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

// diskPathInFiles resolves a disk named in a request against the minimega files
// directory and returns its cleaned absolute path. The disk handlers authorize
// by base name only, so a path outside the files directory, or one that climbs
// out of it with "..", would let a role scoped to some disk names read, modify,
// or delete any host file with that base name.
func diskPathInFiles(p string) (string, error) {
	// minimega expands $VAR in every argument, even quoted, after this check.
	if strings.Contains(p, "$") {
		return "", fmt.Errorf("%w: %s", errDiskPathDollar, p)
	}

	fileDir := filepath.Clean(mm.GetMMFilesDirectory())

	if !filepath.IsAbs(p) {
		p = filepath.Join(fileDir, p)
	}

	p = filepath.Clean(p)

	// Accept the files directory's real path too, as qemu-img may report it.
	if realDir, err := filepath.EvalSymlinks(fileDir); !inDir(fileDir, p) && (err != nil || !inDir(realDir, p)) {
		return "", fmt.Errorf("%w: %s", errDiskOutsideFiles, p)
	}

	_, _, err := resolveInFiles(fileDir, p)
	if err != nil {
		return "", err
	}

	return p, nil
}

// inDir reports whether p is below dir. It compares whole path elements so that
// a sibling such as "/phenix/images-old" does not pass as inside "/phenix/images".
func inDir(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)

	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveInFiles checks that p stays inside fileDir once its symlinks are
// resolved, and returns the resolved files directory and p relative to it. It
// resolves them on the phenix host, which shares the files directory with minimega.
func resolveInFiles(fileDir, p string) (string, string, error) {
	root, err := filepath.EvalSymlinks(fileDir)
	if err != nil {
		return "", "", fmt.Errorf("resolving files directory: %w", err)
	}

	resolved, err := resolveLinks(p)
	if err != nil {
		return "", "", fmt.Errorf("resolving disk %s: %w", p, err)
	}

	if !inDir(root, resolved) {
		return "", "", fmt.Errorf("%w: %s resolves to %s", errDiskOutsideFiles, p, resolved)
	}

	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", "", err
	}

	return root, rel, nil
}

// resolveLinks resolves every symlink in p. For a path that does not exist yet,
// such as a new disk, it resolves the nearest existing parent instead.
func resolveLinks(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err == nil {
		return resolved, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	// Creating a file through a dangling link would create the link's target.
	if _, lerr := os.Lstat(p); lerr == nil {
		return "", fmt.Errorf("%s is a link to a missing file", p)
	}

	parent := filepath.Dir(p)
	if parent == p {
		return "", err
	}

	resolvedParent, err := resolveLinks(parent)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedParent, filepath.Base(p)), nil
}

// chainInFiles checks that a disk and every image in its backing chain are
// inside the files directory and keep their data there, since commit and rebase
// read and write through all of them.
func chainInFiles(path string, info disk.Details) error {
	err := qcow2OrRaw(path, info.Format)
	if err != nil {
		return err
	}

	for i, p := range info.BackingPaths {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("%w: backing image %s is not an absolute path", errDiskOutsideFiles, p)
		}

		// qemu-img follows a symlink before "..", so cleaning the path would check another file.
		if slices.Contains(strings.Split(p, string(filepath.Separator)), "..") {
			return fmt.Errorf("%w: backing image %s contains '..'", errDiskOutsideFiles, p)
		}

		var format string
		if i < len(info.BackingFormats) {
			format = info.BackingFormats[i]
		}

		err := qcow2OrRaw(p, format)
		if err != nil {
			return fmt.Errorf("backing image: %w", err)
		}
	}

	return nil
}

// qcow2OrRaw checks that the image at p is qcow2 or raw and keeps its data in
// itself. Formats such as vmdk can name other host files to read and write.
func qcow2OrRaw(p, format string) error {
	if format != "qcow2" && format != "raw" {
		return fmt.Errorf("%w: %s is %q", errDiskFormat, p, format)
	}

	return noExternalData(p)
}

// qcow2ExternalDataFile is the qcow2 v3 incompatible feature bit for an image
// that stores its guest data in a separate file.
const qcow2ExternalDataFile = 1 << 2

// noExternalData checks that p, if it is a qcow2 image, holds its own data.
// minimega's disk info does not report an external data file, which can be anywhere.
func noExternalData(p string) error {
	file, err := openInFiles(p, os.O_RDONLY, 0)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	// The v3 header holds the incompatible feature bits at bytes 72-79.
	var header [80]byte

	_, err = io.ReadFull(file, header[:])
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil // too short to be a qcow2 v3 image
	} else if err != nil {
		return err
	}

	if string(header[:4]) != "QFI\xfb" || binary.BigEndian.Uint32(header[4:8]) < 3 {
		return nil
	}

	if binary.BigEndian.Uint64(header[72:80])&qcow2ExternalDataFile != 0 {
		return fmt.Errorf("%w: %s keeps its data in an external data file", errDiskOutsideFiles, p)
	}

	return nil
}

// openInFiles opens a file in the files directory through [os.Root], which
// refuses any symlink that leads out of it. p is absolute or relative to it.
// Symlinks are resolved first, since [os.Root] also refuses absolute ones that
// stay inside.
func openInFiles(p string, flag int, perm fs.FileMode) (*os.File, error) {
	fileDir := filepath.Clean(mm.GetMMFilesDirectory())

	if !filepath.IsAbs(p) {
		p = filepath.Join(fileDir, p)
	}

	rootDir, rel, err := resolveInFiles(fileDir, filepath.Clean(p))
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}

	defer func() { _ = root.Close() }()

	return root.OpenFile(rel, flag, perm)
}

// diskPaths is diskPathInFiles that answers 400 on failure. It runs before
// authorization, so the response names no paths and the log holds the detail.
func diskPaths(w http.ResponseWriter, p string) (string, bool) {
	path, err := diskPathInFiles(p)
	if err != nil {
		plog.Error(plog.TypeSystem, "rejecting disk path", "err", err)

		msg := "invalid disk path"
		if errors.Is(err, errDiskOutsideFiles) {
			msg = errDiskOutsideFiles.Error()
		} else if errors.Is(err, errDiskPathDollar) {
			msg = errDiskPathDollar.Error()
		}

		http.Error(w, msg, http.StatusBadRequest)

		return "", false
	}

	return path, true
}

// diskAllowed reports whether role may apply verb to the disk at p. When p is a
// symlink the operation acts on its target, so both names must be allowed.
func diskAllowed(role rbac.Role, verb, p string) bool {
	if !role.Allowed("disks", verb, filepath.Base(p)) {
		return false
	}

	resolved, err := resolveLinks(p)
	if err != nil {
		return false
	}

	return role.Allowed("disks", verb, filepath.Base(resolved))
}

// destAllowed reports whether role may write a new disk at p, and may replace
// the disk already there, since cp, mv, and qemu-img overwrite it.
func destAllowed(role rbac.Role, p string) bool {
	if !diskAllowed(role, "create", p) {
		return false
	}

	if _, err := os.Lstat(p); err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}

	return diskAllowed(role, "update", p)
}

// diskPathPair resolves a source disk and a destination named relative to it.
func diskPathPair(w http.ResponseWriter, src, dst string) (string, string, bool) {
	src, ok := diskPaths(w, src)
	if !ok {
		return "", "", false
	}

	dst, ok = diskPaths(w, normalizeDstDisk(src, dst))
	if !ok {
		return "", "", false
	}

	// cp and mv would write inside a directory, to a child that was never checked.
	if fi, err := os.Stat(dst); err == nil && fi.IsDir() {
		http.Error(w, "destination is a directory", http.StatusBadRequest)

		return "", "", false
	}

	return src, dst, true
}
