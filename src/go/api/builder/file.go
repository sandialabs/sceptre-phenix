package builder

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"phenix/types/builder"
)

// DocumentFile is a Builder document read from the text of a file.
type DocumentFile struct {
	// Data is the canonical JSON encoding of the document, and Digest its
	// digest: the one a document reference pins a file with. It is not the
	// digest of the file's own bytes.
	Data   []byte
	Digest string
	// Document is the decoded, validated document.
	Document *builder.Document
}

// DocumentFileReason says why a Builder file cannot be used. The reasons are
// a closed set: a [DocumentFileError] never says more than its reason and the
// path it was asked for.
type DocumentFileReason string

const (
	// DocumentFileOutside is a path that is not below the directory Builder
	// files are read from, or is below a directory excluded from it.
	DocumentFileOutside DocumentFileReason = "outside"
	// DocumentFileMissing is a path nothing exists at, one that goes on
	// through a regular file included.
	DocumentFileMissing DocumentFileReason = "missing"
	// DocumentFileUnreadable is a file that cannot be opened or read:
	// permissions, a symbolic link that leaves the directory or has an
	// absolute target, an I/O error, or a path a document reference may not
	// hold.
	DocumentFileUnreadable DocumentFileReason = "unreadable"
	// DocumentFileNotRegular is a directory, a named pipe, a device or any
	// other path that is not a regular file.
	DocumentFileNotRegular DocumentFileReason = "not-regular"
	// DocumentFileTooLarge is a file, or a document, of more than
	// [MaxDocumentBytes].
	DocumentFileTooLarge DocumentFileReason = "too-large"
	// DocumentFileInvalid is a file that does not hold a valid Builder
	// document.
	DocumentFileInvalid DocumentFileReason = "invalid"
	// DocumentFileDigest is a file whose document does not have the digest
	// the reference naming the file pins it with. [ReadDocumentFile] never
	// returns it: the caller that holds the reference does.
	DocumentFileDigest DocumentFileReason = "digest"
)

// DocumentFileError is why a Builder file named by a document reference
// cannot be used. Its message is a fixed sentence for its reason, with the
// path the reference names and, where they apply, the directory files are
// read from and the topology that names the file. It holds nothing of the
// file's content and wraps no cause: what a file fails to decode or validate
// with quotes the file, and the caller asking for the document did not
// supply it.
type DocumentFileError struct {
	Reason DocumentFileReason
	// Path is the path the reference names.
	Path string
	// Root is the directory Builder files are read from, for
	// [DocumentFileOutside].
	Root string
	// Topology is the topology whose reference pins the file, for
	// [DocumentFileDigest].
	Topology string
}

func (e *DocumentFileError) Error() string {
	switch e.Reason {
	case DocumentFileOutside:
		return fmt.Sprintf(
			"Builder file %s is outside %s, the directory phenix reads Builder files from.", e.Path, e.Root,
		)
	case DocumentFileMissing:
		return fmt.Sprintf("Builder file %s does not exist on this phenix server.", e.Path)
	case DocumentFileUnreadable:
		return fmt.Sprintf("Builder file %s cannot be read by phenix.", e.Path)
	case DocumentFileNotRegular:
		return fmt.Sprintf("Builder file %s is not a regular file.", e.Path)
	case DocumentFileTooLarge:
		return fmt.Sprintf("Builder file %s is larger than 5 MiB.", e.Path)
	case DocumentFileInvalid:
		return fmt.Sprintf(
			"Builder file %s is not a valid Builder document. Upload it in the Builder to see why.", e.Path,
		)
	case DocumentFileDigest:
		return fmt.Sprintf("Builder file %s does not match the digest topology %s records for it.", e.Path, e.Topology)
	}

	return fmt.Sprintf("Builder file %s cannot be used.", e.Path)
}

// Unwrap lets [errors.Is] match the error of this package the reason
// corresponds to: [ErrNotFound], [ErrTooLarge], or else [ErrInvalid].
func (e *DocumentFileError) Unwrap() error {
	switch e.Reason {
	case DocumentFileMissing:
		return ErrNotFound
	case DocumentFileTooLarge:
		return ErrTooLarge
	case DocumentFileOutside, DocumentFileUnreadable, DocumentFileNotRegular, DocumentFileInvalid, DocumentFileDigest:
	}

	return ErrInvalid
}

// documentFileParsing admits the text of one Builder file at a time to the
// parser. Parsed YAML takes many times the memory of its text, about 400 MiB
// for 5 MiB of short list items, and a file is read again on every request
// for its document, so requests made at once would otherwise multiply that.
// Only parsing waits here, never the read of a file, which can hang.
var documentFileParsing sync.Mutex //nolint:gochecknoglobals // bounds the memory of this process

// parseDocumentText is how [ReadDocumentFile] parses the text of a file.
// Tests replace it.
var parseDocumentText = ParseDocumentText //nolint:gochecknoglobals // a seam for tests

// ParseDocumentText decodes and validates the text of a Builder file, which
// holds a document as JSON or as YAML (see [builder.JSONFromText]), within
// [MaxDocumentBytes]. The text is used as it is: a "${NAME}" in it is not
// expanded, as it is in a config file. The error says what is wrong with the
// text, quoting it, so it is for a caller that supplied the text.
func ParseDocumentText(text []byte) (*DocumentFile, error) {
	if len(text) == 0 {
		return nil, newValidationError("document", "must not be empty")
	}

	if int64(len(text)) > MaxDocumentBytes {
		return nil, newTooLargeError("document", int64(len(text)), MaxDocumentBytes)
	}

	data, err := builder.JSONFromText(text)
	if err != nil {
		return nil, newValidationCause("document", "is not a valid builder document", err)
	}

	canonical, doc, err := canonicalDocument(data)
	if err != nil {
		return nil, err
	}

	return &DocumentFile{Data: canonical, Digest: digestOf(canonical), Document: doc}, nil
}

// LoadDocumentFile reads the Builder document in the file at path for a
// caller that chose the file itself, as the phenix CLI does. The file may be
// anywhere, a symbolic link to it is followed, and the error says what is
// wrong with the file, quoting it where that helps: a missing file and a
// permission are the operating system's error, a path that is not a regular
// file and a file that is not a valid document match [ErrInvalid], and one
// of more than [MaxDocumentBytes] matches [ErrTooLarge]. The text is read as
// [ParseDocumentText] reads it: JSON or YAML by its content, with nothing
// expanded.
func LoadDocumentFile(path string) (*DocumentFile, error) {
	// Opening a named pipe for reading waits for a writer unless the open is
	// non-blocking.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // the file the caller named
	if err != nil {
		return nil, fmt.Errorf("reading builder document: %w", err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()

	switch {
	case err != nil:
		return nil, fmt.Errorf("reading builder document: %w", err)
	case !info.Mode().IsRegular():
		return nil, newValidationError("document", "is not a regular file")
	case info.Size() > MaxDocumentBytes:
		return nil, newTooLargeError("document", info.Size(), MaxDocumentBytes)
	}

	// The file can grow after its size was read.
	data, err := io.ReadAll(io.LimitReader(file, MaxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading builder document: %w", err)
	}

	return ParseDocumentText(data)
}

// ReadDocumentFile reads the Builder document in the file at path, which a
// document reference names (see [ValidateDocumentPath]), for a caller that
// did not choose the path and so must learn nothing else about the file.
//
// The file must be below root, the directory Builder files are read from, and
// below none of the excluded directories. It is opened through [os.Root], so
// a symbolic link is followed only when its target is a relative path that
// stays below root, with no window between that check and the open. A link
// with an absolute target is refused, also when it points below root; one
// that resolves below an excluded directory is refused too, as far as the
// links in place before the open tell. The file must be a regular file of
// at most [MaxDocumentBytes], and its text a valid document (see
// [ParseDocumentText]). Nothing is cached: every call reads the file.
//
// Every failure is a [DocumentFileError].
func ReadDocumentFile(root string, excluded []string, path string) (*DocumentFile, error) {
	fail := func(reason DocumentFileReason) error {
		return &DocumentFileError{Reason: reason, Path: path, Root: root, Topology: ""}
	}

	// A reference is validated when it is decoded, so this refuses only a
	// path that reached here some other way: one that is not clean could name
	// a file above root after all, and one without a document's extension
	// the store file.
	if ValidateDocumentPath(path) != nil {
		return nil, fail(DocumentFileUnreadable)
	}

	base, relative, served := servedDocumentPath(root, excluded, path)
	if !served {
		return nil, fail(DocumentFileOutside)
	}

	data, reason := readRegularFile(base, relative)
	if reason != "" {
		return nil, fail(reason)
	}

	// Unlocked by a defer, so that a parser that panics does not leave every
	// later file waiting.
	file, err := func() (*DocumentFile, error) {
		documentFileParsing.Lock()
		defer documentFileParsing.Unlock()

		return parseDocumentText(data)
	}()

	switch {
	case errors.Is(err, ErrTooLarge):
		return nil, fail(DocumentFileTooLarge)
	case err != nil:
		// The cause quotes the file, so it goes no further.
		return nil, fail(DocumentFileInvalid)
	}

	return file, nil
}

// DocumentPathServed reports whether [ReadDocumentFile] reads a Builder file
// at path at all: the path is below root, the directory Builder files are
// read from, and below none of the excluded directories, itself or through
// the symbolic links in place now. It says nothing of the file. A caller
// about to record a path in a document reference uses it to warn of a path
// the server will refuse.
func DocumentPathServed(root string, excluded []string, path string) bool {
	_, _, served := servedDocumentPath(root, excluded, path)

	return served
}

// servedDocumentPath returns the directory [ReadDocumentFile] opens for
// path and the path relative to it, and false for a path that is not below
// root or is below an excluded directory. The directories are those of this
// process: a relative one is below its working directory.
func servedDocumentPath(root string, excluded []string, path string) (string, string, bool) {
	if root == "" {
		return "", "", false
	}

	directories := make([]string, 0, len(excluded)+1)

	for _, directory := range append([]string{root}, excluded...) {
		if directory == "" {
			continue
		}

		absolute, err := filepath.Abs(directory)
		if err != nil {
			return "", "", false
		}

		directories = append(directories, absolute)
	}

	base, exclusions := directories[0], directories[1:]

	relative, below := pathBelow(base, path)
	if !below || belowAny(exclusions, path) || linksIntoExcluded(exclusions, path) {
		return "", "", false
	}

	return base, relative, true
}

// pathBelow reports whether path names something below the directory, and
// returns the path relative to it. The directory itself is not below it. No
// link is resolved: both are compared as they are written.
func pathBelow(directory, path string) (string, bool) {
	if directory == "" {
		return "", false
	}

	relative, err := filepath.Rel(filepath.Clean(directory), path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}

	return relative, true
}

// belowAny reports whether path names something below one of the
// directories (see [pathBelow]).
func belowAny(directories []string, path string) bool {
	for _, directory := range directories {
		if _, below := pathBelow(directory, path); below {
			return true
		}
	}

	return false
}

// linksIntoExcluded reports whether path resolves, through the symbolic
// links in place now, to a file below an excluded directory, itself named
// through links or not. A path that cannot be resolved resolves nowhere:
// opening it reports why.
func linksIntoExcluded(excluded []string, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}

	for _, directory := range excluded {
		if _, below := pathBelow(directory, resolved); below {
			return true
		}

		target, err := filepath.EvalSymlinks(directory)
		if err != nil {
			continue
		}

		if _, below := pathBelow(target, resolved); below {
			return true
		}
	}

	return false
}

// readRegularFile returns the content of the regular file at the relative
// path below root, of at most [MaxDocumentBytes], or why it cannot be read.
func readRegularFile(root, relative string) ([]byte, DocumentFileReason) {
	directory, err := os.OpenRoot(root)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, DocumentFileMissing
	case err != nil:
		return nil, DocumentFileUnreadable
	}

	defer func() { _ = directory.Close() }()

	// Opening a named pipe for reading waits for a writer unless the open is
	// non-blocking.
	file, err := directory.OpenFile(relative, os.O_RDONLY|syscall.O_NONBLOCK, 0)

	switch {
	// A path through a regular file is missing as one through a name nothing
	// has is, so the answer does not say which names below root are files.
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return nil, DocumentFileMissing
	case err != nil:
		return nil, DocumentFileUnreadable
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()

	switch {
	case err != nil:
		return nil, DocumentFileUnreadable
	case !info.Mode().IsRegular():
		return nil, DocumentFileNotRegular
	case info.Size() > MaxDocumentBytes:
		return nil, DocumentFileTooLarge
	}

	// The file can grow after its size was read.
	data, err := io.ReadAll(io.LimitReader(file, MaxDocumentBytes+1))

	switch {
	case err != nil:
		return nil, DocumentFileUnreadable
	case int64(len(data)) > MaxDocumentBytes:
		return nil, DocumentFileTooLarge
	}

	return data, ""
}
