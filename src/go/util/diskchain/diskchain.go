package diskchain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// MaxDepth is how many images Walk follows before it gives up on a chain.
const MaxDepth = 64

// Kind is the problem, if any, that ended a walk.
type Kind int

// The kinds of Result. Missing, Loop and TooDeep mean qemu cannot open the image (see Result.Broken); the others mean
// phenix could not check the rest of the chain.
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
	// Detail says more about an Unreadable or Unsupported problem.
	Detail string
}

// Broken reports whether qemu cannot open the image: a file in its chain is missing, the chain loops, or it is too
// deep.
func (r Result) Broken() bool {
	return r.Problem == Missing || r.Problem == Loop || r.Problem == TooDeep
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
// there. expect is the format the image above current said current has, or "" when qemu probes it.
func follow(res *Result, current string, h Header, err error, expect Format) (string, bool) {
	switch {
	case errors.Is(err, ErrUnsupportedVersion):
		*res = res.stop(Unsupported, current, fmt.Sprintf("a qcow2 version %d image", h.Version))
	case err != nil:
		*res = res.stop(Unreadable, current, reason(err))
	case expect == FormatQcow2 && h.Format != FormatQcow2:
		*res = res.stop(Unreadable, current, "not the qcow2 image its parent names")
	case h.Format == FormatQED, h.Format == FormatVMDK:
		*res = res.stop(Unsupported, current, "a "+string(h.Format)+" image")
	case h.Format == FormatOther, h.Backing == "" && !h.DataFile:
		return "", true
	case h.DataFile:
		*res = res.stop(DataFile, current, "")
	case hasProtocol(h.Backing):
		*res = res.stop(NotAFile, h.Backing, "")
	case h.BackingFormat != "" && h.BackingFormat != "raw" && h.BackingFormat != string(FormatQcow2):
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
