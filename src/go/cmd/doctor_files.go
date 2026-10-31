package cmd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/viper"

	"phenix/store"
)

// The layout of the meta pages at the start of a BoltDB file, as
// go.etcd.io/bbolt (db.go and page.go) writes them in the host's byte order.
const (
	boltMagic       uint32 = 0xED0CDAED
	boltVersion     uint32 = 2
	boltPageHeader         = 16 // page id, flags, count and overflow
	boltMetaSize           = 64
	boltMetaSummed         = 56 // the meta bytes its checksum covers
	boltMinPageSize        = 512

	// bbolt reads this much to find the page size, and refuses a shorter file.
	boltFirstRead = 4096
)

// boltMeta is what doctor reads from one meta page of a BoltDB file.
type boltMeta struct {
	magic, version, pageSize uint32
	sumOK                    bool
	freelist, pages, txid    uint64 // the page of the free page list, the high water mark and the transaction
}

// valid reports whether bbolt accepts the meta page.
func (m boltMeta) valid() bool {
	return m.magic == boltMagic && m.version == boltVersion && m.sumOK
}

// parseBoltMeta decodes the meta page at the start of page, and reports
// false when page is too short to hold one.
func parseBoltMeta(page []byte) (boltMeta, bool) {
	if len(page) < boltPageHeader+boltMetaSize {
		return boltMeta{magic: 0, version: 0, pageSize: 0, sumOK: false, freelist: 0, pages: 0, txid: 0}, false
	}

	m := page[boltPageHeader : boltPageHeader+boltMetaSize]
	order := binary.NativeEndian
	hash := fnv.New64a()
	_, _ = hash.Write(m[:boltMetaSummed])
	sum := order.Uint64(m[boltMetaSummed:])

	return boltMeta{
		magic:    order.Uint32(m[0:]),
		version:  order.Uint32(m[4:]),
		pageSize: order.Uint32(m[8:]),
		sumOK:    sum == 0 || sum == hash.Sum64(),
		freelist: order.Uint64(m[32:]),
		pages:    order.Uint64(m[40:]),
		txid:     order.Uint64(m[48:]),
	}, true
}

// osReadAt reads at most n bytes of path from offset off, through a
// read-only file that takes no lock, so a running phenix ui is not stalled.
func osReadAt(path string, off int64, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	buf := make([]byte, n)

	read, err := f.ReadAt(buf, off)
	if errors.Is(err, io.EOF) {
		err = nil
	}

	return buf[:read], err
}

// storeFile is the path of the store and its file information, when the
// store is a regular file; store.endpoint reports every other case.
func (in *doctorInput) storeFile() (string, os.FileInfo) {
	path, err := store.BoltPath(in.text("store.endpoint"))
	if err != nil {
		return "", nil
	}

	info, err := in.env.stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil
	}

	return path, info
}

// storeFileCheck checks that path, a store file of size bytes, starts with
// two BoltDB meta pages that bbolt accepts, reading only those pages. As
// bbolt does, it finds the second at the host's page size when the first is invalid.
func (e doctorEnv) storeFileCheck(path string, size int64) doctorCheck {
	if size == 0 {
		return passCheck(path + " is empty, and phenix writes its first pages when it opens it")
	}

	head, err := e.readAt(path, 0, boltFirstRead)
	if err != nil {
		return failCheck(fmt.Sprintf("cannot read %s: %v", path, err))
	}

	first, ok := parseBoltMeta(head)

	switch {
	case !ok:
		return failCheck(fmt.Sprintf("%s is %d bytes, too short for a BoltDB file", path, size))
	case first.magic == bits.ReverseBytes32(boltMagic):
		return failCheck(path + " is a BoltDB file of a host with the other byte order, which phenix cannot read here")
	case first.valid() && (first.pageSize < boltMinPageSize || first.pageSize&(first.pageSize-1) != 0):
		return failCheck(fmt.Sprintf("%s gives a page size of %d, which is not a BoltDB page size", path, first.pageSize))
	}

	pageSize := int64(max(e.pageSize, boltMinPageSize))
	if first.valid() {
		pageSize = int64(first.pageSize)
	}

	notBolt := !first.valid() && (first.magic != boltMagic || first.version != boltVersion)

	switch {
	case (size < 2*pageSize || size < boltFirstRead) && notBolt:
		return boltRefusedCheck(path, first)
	case size < 2*pageSize:
		return failCheck(fmt.Sprintf("%s is %d bytes, shorter than its two %d-byte meta pages, so it is truncated",
			path, size, pageSize))
	case size < boltFirstRead:
		return failCheck(fmt.Sprintf("%s is %d bytes, shorter than the %d bytes BoltDB reads first, so BoltDB refuses it",
			path, size, boltFirstRead))
	}

	next, err := e.readAt(path, pageSize, boltPageHeader+boltMetaSize)
	if err != nil {
		return failCheck(fmt.Sprintf("cannot read %s: %v", path, err))
	}

	second, _ := parseBoltMeta(next)

	switch {
	case !first.valid() && !second.valid():
		return boltRefusedCheck(path, first)
	case !first.valid():
		return warnCheck(fmt.Sprintf(
			"the first meta page of %s is corrupt (%s), so BoltDB reads the second, which may not hold the last write",
			path, boltMetaProblem(first)))
	case !second.valid():
		return warnCheck("the second meta page of " + path + " is corrupt, so BoltDB reads the first, which may not hold the last write")
	}

	return passCheck(fmt.Sprintf("%s: BoltDB version %d, %d-byte pages, %d bytes", path, first.version, pageSize, size))
}

// boltRefusedCheck is the failure of the store file path, which bbolt
// refuses, by the problem of first, its first meta page.
func boltRefusedCheck(path string, first boltMeta) doctorCheck {
	switch {
	case first.magic != boltMagic:
		return failCheck(fmt.Sprintf("%s is not a BoltDB file: it starts with magic number %#x, not %#x",
			path, first.magic, boltMagic))
	case first.version != boltVersion:
		return failCheck(fmt.Sprintf("%s is BoltDB format version %d, but phenix reads version %d", path, first.version, boltVersion))
	}

	return failCheck("both meta pages of " + path + " are corrupt, so BoltDB refuses it")
}

// boltMetaProblem names why bbolt refuses m, an invalid meta page.
func boltMetaProblem(m boltMeta) string {
	switch {
	case m.magic != boltMagic:
		return fmt.Sprintf("magic number %#x", m.magic)
	case m.version != boltVersion:
		return fmt.Sprintf("format version %d", m.version)
	}

	return "bad checksum"
}

// fileOwner is the uid of the owner of the file info describes.
func fileOwner(info os.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}

	return int(st.Uid), true
}

// userLabel names the user with uid, with its name when it has one.
func (e doctorEnv) userLabel(uid int) string {
	id := strconv.Itoa(uid)
	if u, err := e.lookupUID(id); err == nil && u.Username != "" {
		return u.Username + " (uid " + id + ")"
	}

	return "uid " + id
}

// storeOwnerCheck checks the owner of the store file path, as this process
// (uid) sees it; phenixUID is the user phenix resolved, SUDO_USER's under sudo.
func (e doctorEnv) storeOwnerCheck(path string, info os.FileInfo, uid int, phenixUID string) []doctorCheck {
	owner, ok := fileOwner(info)
	if !ok {
		return nil
	}

	label := e.userLabel(owner)
	resolved, err := strconv.Atoi(phenixUID)

	switch {
	case uid != 0 && owner != uid:
		return []doctorCheck{warnCheck(fmt.Sprintf(
			"%s belongs to %s, not to this user, %s, and phenix creates the store readable by its owner only",
			path, label, e.userLabel(uid)))}
	case uid == 0 && err == nil && resolved != 0 && owner != resolved:
		user := e.userLabel(resolved)

		return []doctorCheck{warnCheck(fmt.Sprintf(
			"%s belongs to %s, but sudo phenix uses the store of %s, who cannot open it without sudo", path, label, user))}
	}

	return []doctorCheck{passCheck(path + " belongs to " + label)}
}

// storeRootCheck warns when this command uses a per-user default store while
// systemStore, the store of a phenix ui that runs as root, exists.
func (e doctorEnv) storeRootCheck(endpoint, source, phenixUID, systemStore string) []doctorCheck {
	if phenixUID == "0" || source != sourceDefault {
		return nil
	}

	path, err := store.BoltPath(endpoint)
	if err != nil || path == systemStore {
		return nil
	}

	_, err = e.stat(systemStore)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return []doctorCheck{passCheck(fmt.Sprintf("this command uses the store %s, and %s does not exist", path, systemStore))}
	case err != nil:
		return []doctorCheck{warnCheck(fmt.Sprintf(
			"this command uses the store %s, and cannot see whether %s, the store of a phenix ui run as root, exists: %v",
			path, systemStore, err))}
	}

	return []doctorCheck{warnCheck(fmt.Sprintf(
		"this command uses the store %s, but %s exists. A phenix ui run as root uses that store, so this command sees other experiments",
		path, systemStore))}
}

// configPathsCheck lists each config and users file in paths, in the order
// phenix searches them, and warns when phenix ignores one, since it reads only the first.
func (e doctorEnv) configPathsCheck(paths []string, cwd string) doctorCheck {
	var (
		found, ignored, unsearchable []string
	)

	for _, name := range []string{"config", usersConfigName} {
		var (
			files []string
			infos []os.FileInfo
		)

		for _, dir := range paths {
			abs := dir
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(cwd, dir)
			}

			for _, ext := range viper.SupportedExts {
				file := filepath.Join(abs, name+"."+ext)
				info, err := e.stat(file)

				switch {
				case err == nil && !info.IsDir():
					// a directory on the path twice, such as "." run in /etc/phenix, is one file
					same := func(i os.FileInfo) bool { return os.SameFile(i, info) }
					if !slices.Contains(files, file) && !slices.ContainsFunc(infos, same) {
						files, infos = append(files, file), append(infos, info)
					}
				case errors.Is(err, os.ErrPermission) && name == "config" && ext == viper.SupportedExts[0] &&
					!slices.Contains(unsearchable, abs):
					unsearchable = append(unsearchable, abs)
				}
			}
		}

		for i, file := range files {
			if i == 0 {
				found = append(found, file+" (read)")
			} else {
				found = append(found, file+" (ignored)")
				ignored = append(ignored, file)
			}
		}
	}

	detail := "searched " + strings.Join(paths, ", ") + ", and found "
	if len(found) == 0 {
		detail += "no config or users file"
	} else {
		detail += strings.Join(found, ", ")
	}

	if len(unsearchable) > 0 {
		detail += ", but this user cannot search " + strings.Join(unsearchable, ", ")
	}

	if len(ignored) > 0 {
		return warnCheck("phenix reads only the first file of each name, so it ignores " +
			strings.Join(ignored, ", ") + " (" + detail + ")")
	}

	return passCheck(detail)
}

// settingsFileCheck checks target, the file phenix settings set writes,
// against read, the config file phenix reads.
func (e doctorEnv) settingsFileCheck(target, read string) doctorCheck {
	if read != "" && target != read {
		return warnCheck(fmt.Sprintf("phenix settings set writes %s, but phenix reads %s", target, read))
	}

	if err := checkYAMLConfigFile(target); err != nil {
		return warnCheck("phenix settings set and unset refuse " + target + ", because they rewrite only YAML files")
	}

	check := e.writableFileCheck("", target)
	if check.Status != doctorPass {
		return warnCheck("phenix settings set fails: " + check.Detail)
	}

	return passCheck("phenix settings set writes " + check.Detail)
}
