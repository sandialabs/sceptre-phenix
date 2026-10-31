package experiment

import (
	"archive/zip"
	"context"
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

	// ErrDownloadTooLarge is returned by ZipFiles when the files asked for exceed
	// its limits in number or total size, or one grows past its checked size.
	ErrDownloadTooLarge = errors.New("download too large")

	// ErrFileTruncated tells ZipHooks.OnFile that a file grew since the plan, so
	// only its planned size of it was archived.
	ErrFileTruncated = errors.New("changed while archiving")
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
	return fmt.Sprintf("%v: minimega is still writing a capture to %s", mm.ErrCaptureExists, e.Path)
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
	".7z", ".bz2", ".gz", ".hdd", ".jpg", ".png", ".qc2", ".qcow", ".qcow2", ".tgz", ".xz", ".zip", ".zst",
}

// ZipFiles writes a zip archive of the experiment's files at paths (relative to its files directory) to the writer start
// returns, one file at a time. It checks and totals every path first, so ErrInvalidFilePath, ErrFileNotFound,
// mm.ErrCaptureExists or ErrDownloadTooLarge means nothing was written. It returns the paths archived, even on a later
// failure. It is PlanZip followed by ZipPlan.Write.
func ZipFiles(name string, paths []string, limits ZipLimits, start func() io.Writer) ([]string, error) {
	plan, err := PlanZip(name, paths, limits)
	if err != nil {
		return nil, err
	}

	return plan.Write(context.Background(), start(), ZipHooks{OnStart: nil, OnFile: nil, OnBytes: nil, SkipFailed: false})
}

// ZipPlan is a checked zip archive of an experiment's files, ready to write.
type ZipPlan struct {
	// the experiment
	Name string
	// the files to archive, in order, once each
	Paths []string
	// each file's size, as the check found it
	Sizes map[string]int64
	// the files' sizes in total
	Total int64
}

// ZipHooks tell the caller of ZipPlan.Write how it goes.
type ZipHooks struct {
	// called before each file is archived
	OnStart func(path string)
	// called after each file, with the error that kept it out, if any, or one
	// wrapping ErrFileTruncated
	OnFile func(path string, err error)
	// called with the bytes of each chunk of a file read
	OnBytes func(n int64)
	// archive the other files when one fails, and the planned size of one that
	// grew, listing both, and why, in a MISSING.txt entry
	SkipFailed bool
}

// missingEntry names the archive entry that lists the files left out.
const missingEntry = "MISSING.txt"

// PlanZip checks every path as File does and totals the files' sizes, the
// files only a mesh node has sized from the cluster's listing, without
// copying anything. It fails with ErrInvalidFilePath, ErrFileNotFound,
// mm.ErrCaptureExists or ErrDownloadTooLarge.
func PlanZip(name string, paths []string, limits ZipLimits) (ZipPlan, error) {
	paths = util.Unique(paths)

	if len(paths) == 0 {
		return ZipPlan{}, fmt.Errorf("%w: no files given", ErrInvalidFilePath)
	}

	if len(paths) > limits.MaxFiles {
		return ZipPlan{}, fmt.Errorf(
			"%w: the download has %d files, and the limit is %d", ErrDownloadTooLarge, len(paths), limits.MaxFiles,
		)
	}

	var (
		capturing = captureChecker(name)
		listing   file.Files
		listed    bool
		plan      = ZipPlan{Name: name, Paths: paths, Sizes: make(map[string]int64, len(paths)), Total: 0}
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
			return ZipPlan{}, err
		}

		err = capturing(clean)
		if err != nil {
			return ZipPlan{}, err
		}

		size, ok := localFileSize(localFilePath(name, clean))
		if !ok {
			size, err = listedSize(clean)
			if err != nil {
				return ZipPlan{}, err
			}
		}

		plan.Sizes[p] = size

		plan.Total += size
		if plan.Total > limits.MaxBytes {
			return ZipPlan{}, fmt.Errorf("%w: the files total more than %d bytes", ErrDownloadTooLarge, limits.MaxBytes)
		}
	}

	return plan, nil
}

// Write writes the zip archive of the plan's files to w, one at a time from the
// headnode's copy, and returns the paths archived, each flushed to w once done,
// including on a failure. A file it cannot archive, or one grown past its planned
// size, fails the archive unless hooks.SkipFailed. It stops with ctx's error once
// ctx is done, leaving w with a partial archive.
func (p ZipPlan) Write(ctx context.Context, w io.Writer, hooks ZipHooks) ([]string, error) {
	var (
		archive  = zip.NewWriter(w)
		archived = make([]string, 0, len(p.Paths))
		missing  strings.Builder
		changed  strings.Builder
	)

	for _, filePath := range p.Paths {
		if err := ctx.Err(); err != nil {
			return archived, err //nolint:wrapcheck // the caller tells cancellation apart
		}

		if hooks.OnStart != nil {
			hooks.OnStart(filePath)
		}

		err := addToZip(ctx, archive, p.Name, filePath, p.Sizes[filePath], hooks.OnBytes, hooks.SkipFailed)

		truncated := errors.Is(err, ErrFileTruncated)
		if err == nil || truncated {
			// a file counts as sent only once its bytes reach w
			if flushErr := archive.Flush(); flushErr != nil {
				return archived, fmt.Errorf("writing zip archive: %w", flushErr)
			}
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return archived, ctxErr //nolint:wrapcheck // the caller tells cancellation apart
		}

		if hooks.OnFile != nil {
			hooks.OnFile(filePath, err)
		}

		if err == nil || truncated {
			archived = append(archived, filePath)

			if truncated {
				fmt.Fprintf(&changed, "%s: %v\n", filePath, err)
			}

			continue
		}

		if !hooks.SkipFailed {
			return archived, err
		}

		fmt.Fprintf(&missing, "%s: %v\n", filePath, err)
	}

	if missing.Len() > 0 || changed.Len() > 0 {
		var notes []string

		if missing.Len() > 0 {
			notes = append(notes, "phenix could not add these files to the archive:\n\n"+missing.String())
		}

		if changed.Len() > 0 {
			notes = append(notes, "These files changed while phenix archived them, so the archive holds only part of each:\n\n"+
				changed.String())
		}

		entry, err := archive.Create(missingEntry)
		if err == nil {
			_, err = io.WriteString(entry, strings.Join(notes, "\n"))
		}

		if err != nil {
			return archived, fmt.Errorf("adding %s to zip archive: %w", missingEntry, err)
		}
	}

	err := archive.Close()
	if err != nil {
		return archived, fmt.Errorf("finishing zip archive: %w", err)
	}

	return archived, nil
}

// addToZip copies at most size bytes of the experiment's file into the archive.
// A grown file fails with ErrDownloadTooLarge, or with keepPrefix is archived
// as its first size bytes and fails with ErrFileTruncated.
func addToZip(
	ctx context.Context, archive *zip.Writer, name, clean string, size int64, onBytes func(n int64), keepPrefix bool,
) error {
	local, err := fetchFile(name, clean)
	if err != nil {
		return err
	}

	f, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("opening %s: %w", clean, err)
	}

	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("reading %s: %w", clean, err)
	}

	if info.Size() > size && !keepPrefix {
		return grewError(clean, size)
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

	_, err = io.Copy(w, io.LimitReader(&ctxReader{ctx: ctx, r: f, onBytes: onBytes}, size))
	if err != nil {
		return fmt.Errorf("adding %s to zip archive: %w", clean, err)
	}

	// a byte past the planned size, read but not archived, means it grew
	if n, _ := f.Read(make([]byte, 1)); n > 0 {
		if keepPrefix {
			return fmt.Errorf("%w, so the archive includes only the first %d bytes", ErrFileTruncated, size)
		}

		return grewError(clean, size)
	}

	return nil
}

func grewError(clean string, size int64) error {
	return fmt.Errorf("%w: %s grew to more than the %d bytes that phenix checked", ErrDownloadTooLarge, clean, size)
}

// ctxReader reads r until ctx is done, telling onBytes of each read.
type ctxReader struct {
	ctx     context.Context //nolint:containedctx // bounds one copy
	r       io.Reader
	onBytes func(n int64)
}

func (c *ctxReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err //nolint:wrapcheck // the caller tells cancellation apart
	}

	n, err := c.r.Read(b)
	if n > 0 && c.onBytes != nil {
		c.onBytes(int64(n))
	}

	return n, err //nolint:wrapcheck // io.Copy expects io.EOF as is
}

// DeleteFiles deletes the experiment's files at paths (relative to its files directory) from the headnode and every mesh
// node that has them. It refuses, with the reason, a path File would, one not listed, one minimega could misparse, and a
// pcap a running capture is writing; after a failure deleting the rest, any of them may remain.
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
			refused[p] = fmt.Errorf("%w: minimega cannot safely delete %q because of its characters", ErrInvalidFilePath, p)

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
