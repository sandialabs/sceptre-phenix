package cmd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"phenix/store"
)

const (
	appRuntimesName = "app-runtimes"
	storeFSName     = "store-fs"
	secretFilesName = "secret-files"

	// The kernel reads at most this much of a #! line.
	shebangRead = 256

	// BoltDB marks the page of its free page list with this flag, and stores a count of 0xFFFF or more after the
	// page header.
	boltFreelistFlag  = 0x10
	boltCountOverflow = 0xFFFF
	boltNoFreelist    = math.MaxUint64
	boltFreelistRead  = boltPageHeader + 8

	// store-fs suggests bbolt compact for a store over this size with more than half its pages free.
	compactMinBytes   = 100 << mibShiftBytes
	compactFreeShares = 2

	secretModeBits = 0o044 // read for the group and for others

	// Phrases in the details of rows, which their hints look for.
	notRunnablePhrase = "is not executable by this user"
	carriagePhrase    = "ends in a carriage return"
	networkFSPhrase   = "filesystem, where the file locks and fsync that BoltDB needs can fail"
	freePagesPhrase   = "of its pages are free"
	secretModePhrase  = "is readable by its group or by others"
	secretOwnerPhrase = "belongs to"
	outsidePhrase     = "the free page list is outside the file"
)

// linuxFilesystems names the filesystem types that statfs gives on Linux, by magic number (linux/magic.h). Only
// the network and FUSE filesystems matter to store-fs.
//
//nolint:gochecknoglobals // constant table
var linuxFilesystems = map[uint32]string{
	0x6969:     "nfs",
	0x517b:     "smb",
	0xff534d42: "cifs",
	0xfe534d42: "smb2",
	0x65735546: "fuse",
	0x00c36400: "ceph",
	0x01021997: "9p",
	0x0bd00bd0: "lustre",
	0x47504653: "gpfs",
	0x01161970: "gfs2",
	0x6b414653: "afs",
	0xef53:     "ext4",
	0x58465342: "xfs",
	0x9123683e: "btrfs",
	0x2fc12fc1: "zfs",
	0x01021994: "tmpfs",
	0x794c7630: "overlayfs",
}

// networkFilesystems are the filesystem types on which BoltDB's file lock or fsync can fail.
//
//nolint:gochecknoglobals // constant list
var networkFilesystems = []string{"nfs", "smb", "cifs", "smb2", "ceph", "9p", "lustre", "gpfs", "gfs2", "afs"}

// envArgOptions are the options of GNU env that take the next field as their argument.
//
//nolint:gochecknoglobals // constant list
var envArgOptions = []string{"-u", "--unset", "-C", "--chdir"}

// linuxFilesystemName names the Linux filesystem type of magic, or gives its number.
func linuxFilesystemName(magic uint32) string {
	if name, ok := linuxFilesystems[magic]; ok {
		return name
	}

	return fmt.Sprintf("type %#x", magic)
}

// appRuntimesCheck warns about each user app and SCORCH component on PATH that this user cannot run: phenix finds
// only executable files, and the kernel needs the interpreter that a #! line names, which env looks up on PATH.
func appRuntimesCheck(in *doctorInput) doctorCheck {
	path, _ := in.env.lookup("PATH")

	var (
		names []string
		first = make(map[string]string)
	)

	for _, dir := range filepath.SplitList(path) {
		entries, err := in.env.readDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || first[name] != "" ||
				!strings.HasPrefix(name, userAppPrefix) && !strings.HasPrefix(name, scorchComponentPrefix) {
				continue
			}

			first[name] = filepath.Join(dir, name)
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		return passCheck(fmt.Sprintf("no %s* or %s* programs on PATH", userAppPrefix, scorchComponentPrefix))
	}

	slices.Sort(names)

	var problems []string

	for _, name := range names {
		resolved, err := in.env.lookPath(name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s %s, so phenix does not find %s", first[name], notRunnablePhrase, name))

			continue
		}

		if problem := in.env.shebangProblem(resolved); problem != "" {
			problems = append(problems, problem)
		}
	}

	if len(problems) > 0 {
		return warnCheck(shortList(problems))
	}

	return passCheck("this user can run " + eachOf(len(names), "program", "programs") +
		", and each interpreter of a #! line exists")
}

// shebangProblem says why the kernel cannot run the program at path, by its #! line, or "". It looks up the program
// that env runs on PATH. A file without a #! line, or one this user cannot read, has no problem here.
func (e doctorEnv) shebangProblem(path string) string {
	head, err := e.readAt(path, 0, shebangRead)
	if err != nil || !bytes.HasPrefix(head, []byte("#!")) {
		return ""
	}

	line, _, _ := strings.Cut(string(head[2:]), "\n")
	if strings.HasSuffix(line, "\r") {
		return fmt.Sprintf("the #! line of %s %s (Windows line ends), so the kernel cannot find its interpreter",
			path, carriagePhrase)
	}

	interpreter, rest := strings.TrimLeft(line, " \t"), ""
	if i := strings.IndexAny(interpreter, " \t"); i >= 0 {
		interpreter, rest = interpreter[:i], interpreter[i+1:]
	}

	switch {
	case interpreter == "":
		return fmt.Sprintf("the #! line of %s names no interpreter", path)
	case !filepath.IsAbs(interpreter):
		return fmt.Sprintf("the #! line of %s names %s, which is not an absolute path", path, interpreter)
	}

	if problem := e.runnableProblem(interpreter); problem != "" {
		return fmt.Sprintf("the #! line of %s names %s, %s", path, interpreter, problem)
	}

	if filepath.Base(interpreter) != "env" {
		return ""
	}

	program := envProgram(rest)

	switch {
	case program == "":
		return ""
	case strings.Contains(program, "/"):
		if problem := e.runnableProblem(program); problem != "" {
			return fmt.Sprintf("the #! line of %s runs %s through env, but %s is %s", path, program, program, problem)
		}
	default:
		if _, err := e.lookPath(program); err != nil {
			return fmt.Sprintf("the #! line of %s runs %s through env, but %s is not a program on PATH", path, program, program)
		}
	}

	return ""
}

// envProgram is the program that env runs for the arguments of a #! line, after its options and variables.
func envProgram(args string) string {
	skip := false

	for field := range strings.FieldsSeq(args) {
		switch {
		case skip:
			skip = false
		case slices.Contains(envArgOptions, field):
			skip = true // the next field is the argument of the option
		case !strings.HasPrefix(field, "-") && !strings.Contains(field, "="):
			return field
		}
	}

	return ""
}

// runnableProblem says why this user cannot run the file at path, such as "which does not exist", or "".
func (e doctorEnv) runnableProblem(path string) string {
	info, err := e.stat(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "which does not exist"
	case err != nil:
		return fmt.Sprintf("which cannot be read (%v)", unwrapPathError(err))
	case info.IsDir():
		return "which is a directory"
	case e.access(path, unix.X_OK) != nil:
		return "which this user cannot run"
	}

	return ""
}

// appRuntimesHint is what to do about an app-runtimes row that warns.
func appRuntimesHint(c doctorCheck) string {
	hint := "run chmod +x <path> on the program, or install its interpreter, or install the app package again " +
		"in a working environment"
	if strings.Contains(c.Detail, carriagePhrase) {
		hint += ". Change Windows line ends to Unix line ends with dos2unix <path>"
	}

	return hint
}

// storeFSCheck warns when the store's directory is on a network or FUSE filesystem, and when a store file over 100
// MiB has more than half of its pages free. It reads the free page list of the store file without BoltDB, so it never
// waits for the lock of a running phenix ui.
func storeFSCheck(in *doctorInput) doctorCheck {
	endpoint, err := store.BoltPath(in.text("store.endpoint"))
	if err != nil {
		return hostSkip("store.endpoint is not valid (the store.endpoint row checks it)")
	}

	dir := filepath.Dir(endpoint)

	var (
		parts  []string
		status = doctorPass
		known  bool
	)

	name, err := in.env.fsType(dir)

	switch {
	case err != nil:
		parts = append(parts, fmt.Sprintf("doctor cannot read the filesystem type of %s: %v", dir, unwrapPathError(err)))
	case name == "fuse":
		known, status = true, doctorWarn
		parts = append(parts, fmt.Sprintf("%s is on a FUSE filesystem, such as sshfs, a %s", dir, networkFSPhrase))
	case slices.Contains(networkFilesystems, name):
		known, status = true, doctorWarn
		parts = append(parts, fmt.Sprintf("%s is on %s, a network %s", dir, name, networkFSPhrase))
	default:
		known = true
		parts = append(parts, fmt.Sprintf("%s is on %s", dir, name))
	}

	if path, info := in.storeFile(); info != nil && info.Size() > compactMinBytes {
		free, pages, why := in.env.boltFreePages(path, info.Size())

		switch {
		case why != "":
			parts = append(parts, "the free pages of "+path+" are not checked: "+why)
		case pages > 0 && free*compactFreeShares > pages:
			known, status = true, doctorWarn
			parts = append(parts, fmt.Sprintf("%s is %s, and %d%% %s", path, doctorBytes(info.Size()),
				free*percent/pages, freePagesPhrase))
		default:
			known = true
			parts = append(parts, fmt.Sprintf("%s is %s, and %d of its %d pages are free", path, doctorBytes(info.Size()),
				free, pages))
		}
	}

	if !known {
		return hostSkip(strings.Join(parts, ". "))
	}

	for i := 1; i < len(parts); i++ {
		parts[i] = capitalize(parts[i])
	}

	return doctorCheck{Name: "", Status: status, Detail: strings.Join(parts, ". ")}
}

// boltFreePages reads the count of free pages and the count of all pages of the BoltDB file at path, of size bytes,
// from its current meta page and the header of its free page list. It takes no lock, so it says why when the file
// does not give them, such as a free page list that a write moved.
func (e doctorEnv) boltFreePages(path string, size int64) (uint64, uint64, string) {
	head, err := e.readAt(path, 0, boltFirstRead)
	if err != nil {
		return 0, 0, err.Error()
	}

	first, _ := parseBoltMeta(head)

	pageSize := uint64(max(e.pageSize, boltMinPageSize))
	if first.valid() {
		pageSize = uint64(first.pageSize)
	}

	next, err := e.readAt(path, int64(min(pageSize, math.MaxInt64)), boltPageHeader+boltMetaSize) //nolint:gosec // G115 bounded
	if err != nil {
		return 0, 0, err.Error()
	}

	second, _ := parseBoltMeta(next)

	meta, other := first, second
	if second.txid > first.txid {
		meta, other = second, first
	}

	if !meta.valid() {
		meta = other
	}

	switch {
	case !meta.valid():
		return 0, 0, "both meta pages are corrupt (the store-file row checks them)"
	case meta.freelist == boltNoFreelist:
		return 0, 0, "the file keeps no free page list, as earlier phenix releases wrote it"
	case meta.freelist >= meta.pages || meta.freelist > uint64(max(size, 0))/pageSize:
		return 0, 0, outsidePhrase
	}

	page, err := e.readAt(path, int64(meta.freelist*pageSize), boltFreelistRead) //nolint:gosec // G115 within the file size
	if err != nil {
		return 0, 0, err.Error()
	}

	if len(page) < boltPageHeader {
		return 0, 0, outsidePhrase
	}

	order := binary.NativeEndian
	if order.Uint16(page[8:])&boltFreelistFlag == 0 {
		return 0, 0, fmt.Sprintf("page %d is not the free page list, such as after a write that moved it", meta.freelist)
	}

	free := uint64(order.Uint16(page[10:]))
	if free == boltCountOverflow {
		if len(page) < boltFreelistRead {
			return 0, 0, outsidePhrase
		}

		free = order.Uint64(page[boltPageHeader:])
	}

	if free > meta.pages {
		return 0, 0, fmt.Sprintf("the free page list gives %d pages, more than the %d of the file", free, meta.pages)
	}

	return free, meta.pages, ""
}

// storeFSHint is what to do about a store-fs row that warns.
func storeFSHint(c doctorCheck) string {
	var hints []string

	if strings.Contains(c.Detail, networkFSPhrase) {
		hints = append(hints, "set store.endpoint to a path on a local disk")
	}

	if strings.Contains(c.Detail, freePagesPhrase) {
		hints = append(hints, "stop phenix ui, run bbolt compact -o <new file> <store file> (go install "+
			"go.etcd.io/bbolt/cmd/bbolt@latest), keep a backup, then replace the store file with the new file")
	}

	return strings.Join(hints, ". ")
}

// doctorSecretFile is a file that holds a secret, and what it holds.
type doctorSecretFile struct {
	path, holds string
	owner       bool // check its owner too
}

// secretFilesCheck warns about each file that holds a secret and that its group or others can read, or that
// belongs to a user other than root or the user that runs phenix. It reads only the file information, never the
// contents. The store-owner row checks the owner of the store file.
func secretFilesCheck(in *doctorInput) doctorCheck {
	if in.system.os == "windows" {
		return hostSkip("phenix doctor checks file modes only on systems with Unix modes")
	}

	var files []doctorSecretFile

	if key := in.text("ui.tls-key"); key != "" {
		files = append(files, doctorSecretFile{path: key, holds: "the TLS private key", owner: true})
	}

	for _, key := range []string{"ui.jwt-signing-key", "ui.users"} {
		for _, path := range in.config.files[key] {
			files = append(files, doctorSecretFile{path: path, holds: "holds " + key, owner: true})
		}
	}

	if path, info := in.storeFile(); info != nil {
		files = append(files, doctorSecretFile{path: path, holds: "the store, with password hashes and tokens", owner: false})
	}

	var (
		problems, checked []string
		seen              = make(map[string]bool)
	)

	for _, f := range files {
		if seen[f.path] {
			continue
		}

		seen[f.path] = true

		info, err := in.env.stat(f.path)
		if err != nil {
			continue // the ui.tls, store and config rows report a file they cannot read
		}

		checked = append(checked, f.path)

		if mode := info.Mode().Perm(); mode&secretModeBits != 0 {
			problems = append(problems, fmt.Sprintf("%s (%s) %s (mode %04o)", f.path, f.holds, secretModePhrase, mode))
		}

		if owner, ok := fileOwner(info); ok && f.owner && !in.secretOwnerOK(owner) {
			problems = append(problems, fmt.Sprintf("%s (%s) %s %s", f.path, f.holds, secretOwnerPhrase, in.env.userLabel(owner)))
		}
	}

	switch {
	case len(problems) > 0:
		return warnCheck(shortList(problems))
	case len(checked) == 0:
		return passCheck("no file that holds a secret is set or exists")
	}

	return passCheck("only the owner can read " + strings.Join(checked, ", "))
}

// secretOwnerOK reports whether the user owner may own a secret file: root, this user, or the user phenix resolved
// under sudo.
func (in *doctorInput) secretOwnerOK(owner int) bool {
	return owner == 0 || owner == in.uid || in.phenixUID == strconv.Itoa(owner)
}

// secretFilesHint is what to do about a secret-files row that warns.
func secretFilesHint(c doctorCheck) string {
	var hints []string

	if strings.Contains(c.Detail, secretModePhrase) {
		hints = append(hints, "run chmod 600 <path> on each file that the row names")
	}

	if strings.Contains(c.Detail, secretOwnerPhrase) {
		hints = append(hints, "run chown <user> <path> to give each file to the user that runs phenix")
	}

	return strings.Join(hints, ". ")
}
