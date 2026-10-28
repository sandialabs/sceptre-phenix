package util

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"phenix/web/weberror"
)

var (
	// ErrNotRegular is returned by ServeDownload for a path that is not a
	// regular file, such as a directory.
	ErrNotRegular = errors.New("not a regular file")

	// ErrTooLarge is returned by ServeDownload for a file larger than the
	// Download's MaxSize.
	ErrTooLarge = errors.New("file too large")
)

// Download describes one file for ServeDownload to send.
type Download struct {
	// Path is the file on the server, already authorized and confined by the
	// caller.
	Path string

	// Name is the file name a browser saves the download as. Without one the
	// file is sent inline, for the UI to show.
	Name string

	// ContentType is the response's media type. Without one it is guessed
	// from Name's (or else Path's) extension, and an attachment of an unknown
	// type is application/octet-stream. Never sniffing an attachment keeps a
	// binary file from being labeled, and compressed, as text.
	ContentType string

	// MaxSize, when positive, refuses a larger file with ErrTooLarge before
	// anything is sent.
	MaxSize int64
}

// Served is what ServeDownload sent.
type Served struct {
	// Started reports whether the response began a download: a GET answered
	// with the whole file, or with a range from its first byte. Resuming a
	// download, a HEAD request, and a 304, 412 or 416 response do not, so a
	// caller logs each download once however many requests it takes.
	Started bool
}

// ServeDownload streams the file at d.Path with [http.ServeContent], so ranges, conditional and HEAD requests work, with
// a strong ETag (see FileETag), Cache-Control "private, no-cache" and any Repr-Digest Digests knows. It returns an error,
// having sent nothing, if the file cannot be opened, is not regular (ErrNotRegular) or exceeds d.MaxSize (ErrTooLarge).
func ServeDownload(w http.ResponseWriter, r *http.Request, d Download) (Served, error) {
	f, err := os.Open(d.Path)
	if err != nil {
		return Served{}, fmt.Errorf("opening file for download: %w", err)
	}

	defer func() { _ = f.Close() }()

	return ServeOpenFile(w, r, f, d)
}

// ServeOpenFile is ServeDownload for a file the caller has already opened, at
// d.Path. It does not close f.
func ServeOpenFile(w http.ResponseWriter, r *http.Request, f *os.File, d Download) (Served, error) {
	// stat what is open rather than the path, so the headers describe the
	// bytes sent even if the path is replaced meanwhile
	info, err := f.Stat()
	if err != nil {
		return Served{}, fmt.Errorf("reading file for download: %w", err)
	}

	if !info.Mode().IsRegular() {
		return Served{}, fmt.Errorf("%s: %w", d.Path, ErrNotRegular)
	}

	if d.MaxSize > 0 && info.Size() > d.MaxSize {
		return Served{}, fmt.Errorf("%s is %d bytes: %w", d.Path, info.Size(), ErrTooLarge)
	}

	header := w.Header()
	header.Set("X-Content-Type-Options", "nosniff")

	if d.Name != "" {
		header.Set("Content-Disposition", Attachment(d.Name))
	}

	if ct := downloadType(d); ct != "" {
		header.Set("Content-Type", ct)
	}

	header.Set("Cache-Control", "private, no-cache")
	header.Set("ETag", FileETag(info))

	// the whole file's checksum, if known (RFC 9530): it holds for a range
	// too, so a resumed download can be verified
	if sum, ok := Digests.Lookup(d.Path, info); ok {
		header.Set("Repr-Digest", reprDigest(sum))
	}

	sw := &statusWriter{ResponseWriter: w, status: 0, failed: false, text: strings.Builder{}}

	http.ServeContent(sw, r, d.Name, info.ModTime(), f)
	sw.finish()

	return Served{Started: started(r, sw.status, header, info.Size())}, nil
}

// downloadType is the Content-Type to send for d, or "" to have net/http
// sniff it.
func downloadType(d Download) string {
	if d.ContentType != "" {
		return d.ContentType
	}

	name := d.Name
	if name == "" {
		name = d.Path
	}

	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}

	if d.Name != "" {
		return "application/octet-stream"
	}

	return ""
}

// started reports whether a response with status and header, for a file of
// size bytes, began a download (see Served.Started).
func started(r *http.Request, status int, header http.Header, size int64) bool {
	if r.Method != http.MethodGet {
		return false
	}

	switch status {
	case http.StatusOK:
		return true
	case http.StatusPartialContent:
		if cr := header.Get("Content-Range"); cr != "" {
			return strings.HasPrefix(cr, "bytes 0-")
		}

		// a multipart response has no Content-Range of its own, so look for
		// a requested range that starts at the first byte
		return rangeFromStart(r.Header.Get("Range"), size)
	default:
		return false
	}
}

// rangeFromStart reports whether the Range header value, already satisfied by
// [http.ServeContent], asks for any range starting at the first byte of a file
// of size bytes: "0-..." or a suffix "-n" with n at least size.
func rangeFromStart(header string, size int64) bool {
	specs, ok := strings.CutPrefix(header, "bytes=")
	if !ok {
		return false
	}

	for spec := range strings.SplitSeq(specs, ",") {
		start, end, ok := strings.Cut(strings.TrimSpace(spec), "-")
		if !ok {
			continue
		}

		start = strings.TrimSpace(start)
		if start == "" {
			if n, err := strconv.ParseInt(strings.TrimSpace(end), 10, 64); err == nil && n >= size {
				return true
			}

			continue
		}

		if n, err := strconv.ParseInt(start, 10, 64); err == nil && n == 0 {
			return true
		}
	}

	return false
}

// statusWriter records the status a handler sends and passes ReadFrom through for sendfile; an error status,
// such as ServeContent's 412 or 416, and its text are held back for finish to send as the JSON Error body.
type statusWriter struct {
	http.ResponseWriter

	status int
	failed bool
	text   strings.Builder
}

func (sw *statusWriter) WriteHeader(code int) {
	if sw.status == 0 && code >= http.StatusOK {
		sw.status = code
	}

	if code >= http.StatusBadRequest {
		sw.failed = true

		return
	}

	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}

	if sw.failed {
		return sw.text.Write(b) //nolint:wrapcheck // never fails
	}

	return sw.ResponseWriter.Write(b) //nolint:wrapcheck // passthrough
}

// finish answers an error status held back with its text as the JSON Error
// body, or with the status's own text when it came without any.
func (sw *statusWriter) finish() {
	if !sw.failed {
		return
	}

	msg := strings.TrimSpace(sw.text.String())
	if msg == "" {
		msg = http.StatusText(sw.status)
	}

	weberror.Error(sw.ResponseWriter, msg, sw.status)
}

func (sw *statusWriter) ReadFrom(src io.Reader) (int64, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}

	if sw.failed {
		return io.Copy(&sw.text, src) //nolint:wrapcheck // never fails
	}

	if rf, ok := sw.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src) //nolint:wrapcheck // passthrough
	}

	// hide this writer's own ReadFrom from io.Copy
	return io.Copy(struct{ io.Writer }{sw.ResponseWriter}, src) //nolint:wrapcheck // passthrough
}

// Unwrap lets [http.ResponseController] reach the underlying writer.
func (sw *statusWriter) Unwrap() http.ResponseWriter {
	return sw.ResponseWriter
}
