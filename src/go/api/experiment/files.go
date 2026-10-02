package experiment

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"phenix/util"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
)

var (
	// ErrFileNotFound is returned for a path that is not one of the
	// experiment's files.
	ErrFileNotFound = errors.New("file not found")

	// ErrInvalidFilePath is returned for a path that is not a clean path
	// relative to the experiment's files directory, or that phenix cannot
	// safely hand to minimega.
	ErrInvalidFilePath = errors.New("invalid file path")

	// ErrDownloadTooLarge is returned by ZipFiles when the files asked for are
	// more, or larger in total, than its limits allow.
	ErrDownloadTooLarge = errors.New("download too large")
)

// cleanFilePath returns filePath if it is a clean path relative to the
// experiment's files directory, as the listing shows paths. A path that
// cleaning would change (`..`, a leading, doubled, or trailing slash) or an
// empty one is not.
func cleanFilePath(filePath string) (string, error) {
	clean := strings.TrimPrefix(path.Clean("/"+filePath), "/")
	if clean == "" || clean != filePath {
		return "", fmt.Errorf("%w: %q", ErrInvalidFilePath, filePath)
	}

	return clean, nil
}

// CaptureWritingError is returned for a file one of the experiment's running
// captures is still writing. It wraps mm.ErrCaptureExists.
type CaptureWritingError struct {
	// Path is the file, relative to the experiment's files directory.
	Path    string
	Capture mm.Capture
}

func (e *CaptureWritingError) Error() string {
	return fmt.Sprintf("%v: %s is still being captured", mm.ErrCaptureExists, e.Path)
}

func (e *CaptureWritingError) Unwrap() error {
	return mm.ErrCaptureExists
}

// CaptureFile returns the path, relative to the experiment's files directory,
// of the file a capture writes (minimega's path for it), and false for a
// capture written anywhere else; phenix always has minimega write captures
// there.
func CaptureFile(name, capturePath string) (string, bool) {
	_, file, ok := strings.Cut(capturePath, "/"+name+"/files/")

	return file, ok && file != ""
}

// captureChecker returns a check that fails with a *CaptureWritingError for a
// pcap one of the experiment's captures is still writing. Only a pcap capture
// writes to a file, and only a running experiment has captures; asking
// minimega about a stopped one would recreate its namespace (see Running).
// The captures are listed once, for the first pcap checked.
func captureChecker(name string) func(clean string) error {
	var (
		listed   bool
		captures []mm.Capture
	)

	return func(clean string) error {
		if !strings.HasSuffix(clean, ".pcap") {
			return nil
		}

		if !listed {
			listed = true

			if Running(name) {
				captures = mm.GetExperimentCaptures(mm.NS(name))
			}
		}

		for _, c := range captures {
			if file, ok := CaptureFile(name, c.Filepath); ok && file == clean {
				return &CaptureWritingError{Path: clean, Capture: c}
			}
		}

		return nil
	}
}

// localFilePath is where the headnode keeps the experiment's file.
func localFilePath(name, clean string) string {
	return fmt.Sprintf("%s/images/%s/files/%s", common.PhenixBase, name, clean)
}

// localFileSize returns the size of the headnode's copy of the file, and
// false if the headnode has no regular file there. Lstat: a symlink is not
// followed out of the files directory.
func localFileSize(local string) (int64, bool) {
	info, err := os.Lstat(local)
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}

	return info.Size(), true
}

// fetchFile returns the headnode path of the experiment's file (a clean
// path), copying it from the mesh node that has it first if the headnode does
// not. Most files are already on the headnode (it wrote them, or an earlier
// view copied them here), so they are found without asking minimega to list
// the whole cluster's files first.
func fetchFile(name, clean string) (string, error) {
	local := localFilePath(name, clean)

	if _, ok := localFileSize(local); ok {
		return local, nil
	}

	files, err := file.GetExperimentFiles(name, "")
	if err != nil {
		return "", fmt.Errorf("getting list of experiment files: %w", err)
	}

	for _, f := range files {
		if clean == f.Path {
			headnode, _ := os.Hostname()

			_ = file.CopyFile(fmt.Sprintf("/%s/files/%s", name, f.Path), headnode, nil)

			if _, ok := localFileSize(local); !ok {
				return "", fmt.Errorf("copying %s to the headnode: %w", clean, ErrFileNotFound)
			}

			return local, nil
		}
	}

	return "", fmt.Errorf("%s: %w", clean, ErrFileNotFound)
}

// ZipLimits bound what one ZipFiles call archives.
type ZipLimits struct {
	MaxFiles int
	MaxBytes int64
}

// storedExtensions are files already compressed, stored in a zip as they are
// rather than deflated again.
var storedExtensions = []string{ //nolint:gochecknoglobals // constant list
	".7z", ".bz2", ".gz", ".hdd", ".jpg", ".png", ".qc2", ".qcow2", ".tgz", ".xz", ".zip", ".zst",
}

// ZipFiles writes a zip archive of the experiment's files at paths (relative
// to its files directory, as listed) to the writer start returns, one file at
// a time from the headnode's copy, so no more than a file's buffer is held in
// memory. Before calling start it checks every path as File does and totals
// the files' sizes: a failure then (ErrInvalidFilePath, ErrFileNotFound,
// mm.ErrCaptureExists, ErrDownloadTooLarge) has written nothing. It returns
// the paths archived.
func ZipFiles(name string, paths []string, limits ZipLimits, start func() io.Writer) ([]string, error) {
	paths = util.Unique(paths)

	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: no files given", ErrInvalidFilePath)
	}

	if len(paths) > limits.MaxFiles {
		return nil, fmt.Errorf("%w: %d files asked for, at most %d allowed", ErrDownloadTooLarge, len(paths), limits.MaxFiles)
	}

	var (
		capturing = captureChecker(name)
		listing   file.Files
		listed    bool
		total     int64
	)

	// A file only a mesh node has is sized from the listing, without copying
	// it to the headnode before the total is known.
	listedSize := func(clean string) (int64, error) {
		if !listed {
			var err error

			listing, err = file.GetExperimentFiles(name, "")
			if err != nil {
				return 0, fmt.Errorf("getting list of experiment files: %w", err)
			}

			listed = true
		}

		for _, f := range listing {
			if f.Path == clean {
				return f.Size, nil
			}
		}

		return 0, fmt.Errorf("%s: %w", clean, ErrFileNotFound)
	}

	for _, p := range paths {
		clean, err := cleanFilePath(p)
		if err != nil {
			return nil, err
		}

		err = capturing(clean)
		if err != nil {
			return nil, err
		}

		size, ok := localFileSize(localFilePath(name, clean))
		if !ok {
			size, err = listedSize(clean)
			if err != nil {
				return nil, err
			}
		}

		total += size
		if total > limits.MaxBytes {
			return nil, fmt.Errorf("%w: the files total more than %d bytes", ErrDownloadTooLarge, limits.MaxBytes)
		}
	}

	archive := zip.NewWriter(start())

	for _, p := range paths {
		err := addToZip(archive, name, p)
		if err != nil {
			return nil, err
		}
	}

	err := archive.Close()
	if err != nil {
		return nil, fmt.Errorf("finishing zip archive: %w", err)
	}

	return paths, nil
}

// addToZip copies the experiment's file (a clean path) into the archive.
func addToZip(archive *zip.Writer, name, clean string) error {
	local, err := fetchFile(name, clean)
	if err != nil {
		return err
	}

	f, err := os.Open(local) //nolint:gosec // path checked by cleanFilePath and fetchFile
	if err != nil {
		return fmt.Errorf("opening %s: %w", clean, err)
	}

	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("reading %s: %w", clean, err)
	}

	method := zip.Deflate
	if slices.Contains(storedExtensions, strings.ToLower(path.Ext(clean))) {
		method = zip.Store
	}

	w, err := archive.CreateHeader(&zip.FileHeader{ //nolint:exhaustruct // zip fills in the rest
		Name:     clean,
		Method:   method,
		Modified: info.ModTime(),
	})
	if err != nil {
		return fmt.Errorf("adding %s to zip archive: %w", clean, err)
	}

	_, err = io.Copy(w, f)
	if err != nil {
		return fmt.Errorf("adding %s to zip archive: %w", clean, err)
	}

	return nil
}

// DeleteFiles deletes the experiment's files at paths (relative to its files
// directory, as listed) from the headnode and every mesh node that has them.
// It refuses, with the reason, a path File would, one not listed (including a
// directory), one minimega could read as a glob or several arguments, and a
// pcap a running capture is still writing, and deletes the rest together. A
// failure deleting them is returned as err, after which any of them may
// remain.
func DeleteFiles(name string, paths []string) ([]string, map[string]error, error) {
	refused := make(map[string]error)

	if !file.CommandSafe(name) {
		return nil, nil, fmt.Errorf("%w: experiment name %q", ErrInvalidFilePath, name)
	}

	files, err := file.GetExperimentFiles(name, "")
	if err != nil {
		return nil, nil, fmt.Errorf("getting list of experiment files: %w", err)
	}

	listed := make(map[string]bool, len(files))
	for _, f := range files {
		listed[f.Path] = true
	}

	var (
		capturing = captureChecker(name)
		accepted  []string
		names     []string
	)

	for _, p := range util.Unique(paths) {
		clean, err := cleanFilePath(p)
		if err != nil {
			refused[p] = err

			continue
		}

		if !file.CommandSafe(clean) {
			refused[p] = fmt.Errorf("%w: %q has characters minimega cannot delete safely", ErrInvalidFilePath, p)

			continue
		}

		if !listed[clean] {
			refused[p] = fmt.Errorf("%s: %w", p, ErrFileNotFound)

			continue
		}

		err = capturing(clean)
		if err != nil {
			refused[p] = err

			continue
		}

		accepted = append(accepted, clean)
		names = append(names, name+"/files/"+clean)
	}

	if len(names) == 0 {
		return nil, refused, nil
	}

	// DeleteExistingFiles clears the cached listings too.
	err = file.DeleteExistingFiles(names)
	if err != nil {
		return nil, refused, fmt.Errorf("deleting experiment files: %w", err)
	}

	return accepted, refused, nil
}

// DeleteFile deletes one of the experiment's files, as DeleteFiles does.
func DeleteFile(name, filePath string) error {
	_, refused, err := DeleteFiles(name, []string{filePath})
	if err != nil {
		return err
	}

	return refused[filePath]
}
