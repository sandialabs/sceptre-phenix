package disk

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// NewImagePath returns the cleaned absolute path of the image that a snapshot, copy or rename of the image at src (a
// path from Resolve) makes, named dst: relative to the folder of src, or absolute. dst must name a file, not a folder,
// which the added extension would turn into a file beside that folder. sameFormat is as for newImageName. The result
// must be an image the disk actions act on (see Resolve).
func NewImagePath(src, dst string, sameFormat bool) (string, error) {
	if dst == "" {
		return "", fmt.Errorf("%w: no new image given", ErrNotManaged)
	}

	if last := filepath.Base(dst); strings.HasSuffix(dst, "/") || last == "." || last == ".." {
		return "", fmt.Errorf("%w: new image %q does not name a file", ErrNotManaged, dst)
	}

	newPath, err := newImageName(src, dst, sameFormat)
	if err != nil {
		return "", err
	}

	return Resolve(newPath)
}

// newImageName returns dst, the name of an image made from the image at src, as an absolute path: relative to the
// folder of src. It adds the first of newImageExtensions unless dst ends in one of them. When those follow the known
// extension of src, it refuses, with ErrWrongExtension, a dst that ends in a different known image extension.
func newImageName(src, dst string, sameFormat bool) (string, error) {
	if !filepath.IsAbs(dst) {
		dst = filepath.Join(filepath.Dir(src), dst)
	}

	exts, strict := newImageExtensions(src, sameFormat)
	if slices.ContainsFunc(exts, func(ext string) bool { return strings.HasSuffix(dst, ext) }) {
		return dst, nil
	}

	if strict && ImageExtension(dst) != "" {
		return "", fmt.Errorf("%w: the new image %s must end in %s, because it keeps the format of %s",
			ErrWrongExtension, dst, strings.Join(exts, " or "), src)
	}

	return dst + exts[0], nil
}

// newImageExtensions returns the extensions that the name of an image made from the image at src may end in, the first
// being the one to add, and whether a known extension of src decided them. A snapshot (not sameFormat) is a qcow2
// image. A copy or a rename keeps the format of src: .qcow also takes the qcow2 ones, as phenix always did.
func newImageExtensions(src string, sameFormat bool) ([]string, bool) {
	qcow2 := []string{".qcow2", ".qc2"}

	if !sameFormat {
		return qcow2, false
	}

	switch ext := ImageExtension(src); ext {
	case "":
		return qcow2, false
	case ".qcow2", ".qc2":
		return qcow2, true
	case ".qcow":
		return append([]string{ext}, qcow2...), true
	default:
		return []string{ext}, true
	}
}
