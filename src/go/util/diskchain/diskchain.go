package diskchain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// MaxDepth is how many images Walk follows before it gives up on a chain.
const MaxDepth = 64

// Kind is the problem, if any, that ended a walk.
type Kind int

// The kinds of Result. Missing, Loop and TooDeep mean the chain is broken (see Result.Broken). BadVersion,
// UnknownFeatures, AESEncrypted and LUKSEncrypted mean qemu refuses an image of the chain (see Result.Refused). The
// others mean phenix could not check the rest of the chain.
const (
	// Whole is a chain followed to its base.
	Whole Kind = iota
	// Missing is a chain with a file that does not exist.
	Missing
	// Loop is a chain that names one of its own images again.
	Loop
	// TooDeep is a chain of more than MaxDepth images.
	TooDeep
	// Unreadable is an image phenix cannot open or whose header qemu would refuse.
	Unreadable
	// Unsupported is a qed or vmdk image, or a backing format other than qcow2 or raw: phenix does not follow it.
	Unsupported
	// NotAFile is a backing file named by a protocol, such as nbd:// or json:.
	NotAFile
	// DataFile is a qcow2 image with an external data file.
	DataFile
	// BadVersion is an image with the qcow2 magic and a version later than 3.
	BadVersion
	// UnknownFeatures is a qcow2 image with incompatible feature bits that qemu does not know.
	UnknownFeatures
	// AESEncrypted is a qcow or qcow2 image with the old AES encryption, which qemu system emulators refuse.
	AESEncrypted
	// LUKSEncrypted is a qcow2 image with LUKS encryption, whose key phenix cannot give qemu.
	LUKSEncrypted
)

// Result is the outcome of a walk.
type Result struct {
	// Members are the images read, the walked image first, each named as the image above it names it: a relative
	// backing name is joined onto the folder of the image naming it, without resolving "." or "..".
	Members []string
	// Problem is what ended the walk; Whole for none.
	Problem Kind
	// At is the image the problem is at: the missing file, the image named again, the image not followed, or the
	// backing name of a NotAFile.
	At string
	// Detail says more about an Unreadable or Unsupported problem. It is the version of a BadVersion problem, and the
	// unknown bits of an UnknownFeatures problem, such as 0x20.
	Detail string
	// Notes are facts about the images read that do not stop the walk, in chain order.
	Notes []Note
}

// NoteKind is a fact about one image of a chain.
type NoteKind int

// The kinds of Note. A start warns about QcowV1 and Dirty (see Note.AtStart), and about Corrupt, which it refuses for
// an image that qemu opens read/write. phenix doctor reports the others.
const (
	// QcowV1 is a qcow version 1 image.
	QcowV1 NoteKind = iota + 1
	// Qcow2V2 is a qcow2 version 2 image.
	Qcow2V2
	// Dirty is a qcow2 image that qemu did not close cleanly.
	Dirty
	// Corrupt is a qcow2 image that qemu marked as corrupt. qemu opens it only read-only, such as a backing file.
	Corrupt
	// Zstd is a qcow2 image with zstd compression.
	Zstd
	// ExtendedL2 is a qcow2 image with extended L2 entries.
	ExtendedL2
	// QED is a qed image, which phenix does not follow.
	QED
	// VMDK is a vmdk image, which phenix does not follow.
	VMDK
)

// Note is a fact about the image at Path.
type Note struct {
	Path string
	Kind NoteKind
}

// Sentence describes the note in one or two sentences without a final period.
func (n Note) Sentence() string {
	switch n.Kind {
	case QcowV1:
		return n.Path + " is a qcow version 1 image, an old format that some qemu builds cannot open. Convert it to qcow2"
	case Qcow2V2:
		return n.Path + " is a qcow2 version 2 image"
	case Dirty:
		return "qemu did not close " + n.Path + " cleanly. Run qemu-img check -r all on it"
	case Corrupt:
		return "qemu marked " + n.Path + " as corrupt"
	case Zstd, ExtendedL2:
		version, feature := n.Kind.MinQemu()

		return fmt.Sprintf("%s uses %s, which needs qemu %s or later", n.Path, feature, version)
	case QED:
		return n.Path + " is a qed image"
	case VMDK:
		return n.Path + " is a vmdk image"
	}

	return ""
}

// AtStart reports whether a start warns about the note: a qcow version 1 image, or one qemu did not close cleanly.
func (n Note) AtStart() bool {
	return n.Kind == QcowV1 || n.Kind == Dirty
}

// MinQemu returns the oldest qemu version that opens an image with the note, and the feature that needs it, or ""
// for a note that needs no recent qemu.
func (k NoteKind) MinQemu() (string, string) {
	switch k {
	case Zstd:
		return "5.1", "zstd compression"
	case ExtendedL2:
		return "5.2", "extended L2 entries"
	case QcowV1, Qcow2V2, Dirty, Corrupt, QED, VMDK:
	}

	return "", ""
}

// Broken reports whether qemu cannot open the image: a file in its chain is missing, the chain loops, or it is too
// deep.
func (r Result) Broken() bool {
	return r.Problem == Missing || r.Problem == Loop || r.Problem == TooDeep
}

// Refused reports whether qemu refuses to open an image of the chain for its version, its feature bits or its
// encryption.
func (r Result) Refused() bool {
	switch r.Problem {
	case BadVersion, UnknownFeatures, AESEncrypted, LUKSEncrypted:
		return true
	case Whole, Missing, Loop, TooDeep, Unreadable, Unsupported, NotAFile, DataFile:
	}

	return false
}

// Sentence describes the problem in one sentence without a final period, or returns "" for a whole chain.
func (r Result) Sentence() string {
	switch r.Problem {
	case Whole:
		return ""
	case Missing:
		if len(r.Members) == 0 {
			return "disk image " + r.At + " is missing"
		}

		return "backing file " + r.At + " is missing"
	case Loop:
		return "backing chain loops back to " + r.At
	case TooDeep:
		return fmt.Sprintf("backing chain is more than %d images deep", MaxDepth)
	case Unreadable:
		return fmt.Sprintf("cannot read %s (%s), so phenix does not check past it", r.At, r.Detail)
	case Unsupported:
		return fmt.Sprintf("%s is %s, so phenix does not check past it", r.At, r.Detail)
	case NotAFile:
		return "backing file " + r.At + " is not a local file, so phenix does not check it"
	case DataFile:
		return r.At + " has an external data file, so phenix does not check it"
	case BadVersion:
		return r.At + " is a qcow2 version " + r.Detail + " image, which qemu cannot open"
	case UnknownFeatures:
		return r.At + " uses qcow2 features that qemu does not know (incompatible feature bits " + r.Detail + ")"
	case AESEncrypted:
		return r.At + " uses the old qcow AES encryption, which qemu does not open for VMs"
	case LUKSEncrypted:
		return r.At + " uses LUKS encryption, and phenix cannot give qemu its key"
	}

	return ""
}

// MapPaths returns r with each file it names passed through f, such as to clean the paths; the backing name of a
// NotAFile is kept as it is.
func (r Result) MapPaths(f func(string) string) Result {
	members := make([]string, 0, len(r.Members))
	for _, m := range r.Members {
		members = append(members, f(m))
	}

	r.Members = members

	if r.Notes != nil {
		notes := make([]Note, 0, len(r.Notes))
		for _, n := range r.Notes {
			notes = append(notes, Note{Path: f(n.Path), Kind: n.Kind})
		}

		r.Notes = notes
	}

	if r.Problem != NotAFile && r.At != "" {
		r.At = f(r.At)
	}

	return r
}

// Walk follows the backing chain of the image at path as qemu opens it, reading only headers. It stops at the first
// problem, or once ctx is done, which it reports as Unreadable.
func Walk(ctx context.Context, path string) Result {
	var (
		res     Result
		visited []os.FileInfo
		current = path
		raw     bool   // the current image is a raw backing file: it is only opened
		expect  Format // the format the backing format extension named, if any
	)

	for {
		if err := ctx.Err(); err != nil {
			return res.stop(Unreadable, current, err.Error())
		}

		if len(res.Members) == MaxDepth {
			return res.stop(TooDeep, current, "")
		}

		f, err := OpenRegular(current)
		if err != nil {
			if notExist(err) {
				return res.stop(Missing, current, "")
			}

			return res.stop(Unreadable, current, reason(err))
		}

		info, err := f.Stat()
		if err != nil {
			_ = f.Close()

			return res.stop(Unreadable, current, reason(err))
		}

		for _, seen := range visited {
			if os.SameFile(seen, info) {
				_ = f.Close()

				return res.stop(Loop, current, "")
			}
		}

		visited = append(visited, info)
		res.Members = append(res.Members, current)

		if raw {
			_ = f.Close()

			return res
		}

		h, err := ParseHeader(f)
		_ = f.Close()

		next, stop := follow(&res, current, h, err, expect)
		if stop {
			return res
		}

		raw = h.BackingFormat == "raw"
		expect = Format(h.BackingFormat)
		current = next
	}
}

// Exists checks only that the image at path is a regular file phenix can open, for an image qemu reads as raw, such
// as a CD-ROM image: the result is Whole, Missing or Unreadable.
func Exists(ctx context.Context, path string) Result {
	var res Result

	if err := ctx.Err(); err != nil {
		return res.stop(Unreadable, path, err.Error())
	}

	f, err := OpenRegular(path)
	if err != nil {
		if notExist(err) {
			return res.stop(Missing, path, "")
		}

		return res.stop(Unreadable, path, reason(err))
	}

	_ = f.Close()
	res.Members = []string{path}

	return res
}

// follow returns the image to read after current, whose header is h (or err), or reports in res why the walk stops
// there, adding to res the notes about current. expect is the format the image above current said current has, or ""
// when qemu probes it.
func follow(res *Result, current string, h Header, err error, expect Format) (string, bool) {
	switch {
	case errors.Is(err, ErrUnsupportedVersion):
		*res = res.stop(BadVersion, current, strconv.Itoa(h.Version))
	case err != nil:
		*res = res.stop(Unreadable, current, reason(err))
	case expect != "" && h.Format != expect:
		*res = res.stop(Unreadable, current, "not the "+string(expect)+" image its parent names")
	case refuse(res, current, h):
	default:
		res.Notes = append(res.Notes, notes(current, h)...)

		return next(res, current, h)
	}

	return "", true
}

// refuse reports whether qemu refuses the image at current, whose header is h, stopping res there when it does.
func refuse(res *Result, current string, h Header) bool {
	var (
		problem Kind
		detail  string
	)

	switch {
	case h.Incompatible&^KnownFeatures != 0:
		problem, detail = UnknownFeatures, fmt.Sprintf("0x%x", h.Incompatible&^KnownFeatures)
	case h.Encryption == EncryptionAES:
		problem = AESEncrypted
	case h.Encryption == EncryptionLUKS:
		problem = LUKSEncrypted
	default:
		return false
	}

	*res = res.stop(problem, current, detail)

	return true
}

// notes are the facts about the image at path, whose header is h, that do not stop the walk.
func notes(path string, h Header) []Note {
	var kinds []NoteKind

	switch {
	case h.Format == FormatQcow:
		kinds = append(kinds, QcowV1)
	case h.Format == FormatQcow2 && h.Version == 2:
		kinds = append(kinds, Qcow2V2)
	case h.Format == FormatQED:
		kinds = append(kinds, QED)
	case h.Format == FormatVMDK:
		kinds = append(kinds, VMDK)
	}

	for _, f := range []struct {
		bit  uint64
		kind NoteKind
	}{{FeatureDirty, Dirty}, {FeatureCorrupt, Corrupt}, {FeatureCompression, Zstd}, {FeatureExtendedL2, ExtendedL2}} {
		if h.Incompatible&f.bit != 0 {
			kinds = append(kinds, f.kind)
		}
	}

	out := make([]Note, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, Note{Path: path, Kind: k})
	}

	return out
}

// next returns the backing file of the image at current, whose header qemu opens, or reports in res why the walk
// stops there.
func next(res *Result, current string, h Header) (string, bool) {
	switch {
	case h.Format == FormatQED, h.Format == FormatVMDK:
		*res = res.stop(Unsupported, current, "a "+string(h.Format)+" image")
	case h.Format == FormatOther, h.Backing == "" && !h.DataFile:
		return "", true
	case h.DataFile:
		*res = res.stop(DataFile, current, "")
	case hasProtocol(h.Backing):
		*res = res.stop(NotAFile, h.Backing, "")
	case !slices.Contains([]string{"", "raw", string(FormatQcow2), string(FormatQcow)}, h.BackingFormat):
		*res = res.stop(Unsupported, current, "backed by a "+h.BackingFormat+" image")
	default:
		if filepath.IsAbs(h.Backing) {
			return h.Backing, false
		}

		// Cut the last element as qemu does: filepath.Dir would clean ".." lexically, not through symlinks.
		return current[:strings.LastIndexByte(current, filepath.Separator)+1] + h.Backing, false
	}

	return "", true
}

// stop returns r ended by problem at at.
func (r Result) stop(problem Kind, at, detail string) Result {
	r.Problem, r.At, r.Detail = problem, at, detail

	return r
}

// notExist reports whether err means the file is not there, as qemu would find it: no such file, or a path through
// a file that is not a folder.
func notExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// reason is err's cause in a few words.
func reason(err error) string {
	switch {
	case errors.Is(err, ErrNotRegular):
		return "not a regular file"
	case errors.Is(err, ErrSystemDir):
		return "in a system directory"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}

	var perr *fs.PathError
	if errors.As(err, &perr) {
		return perr.Err.Error()
	}

	return err.Error()
}

// hasProtocol reports whether qemu reads name as a protocol rather than a file: a json: name, or a colon before the
// first slash.
func hasProtocol(name string) bool {
	if strings.HasPrefix(name, "json:") {
		return true
	}

	colon := strings.IndexByte(name, ':')
	slash := strings.IndexByte(name, '/')

	return colon >= 0 && (slash < 0 || colon < slash)
}
