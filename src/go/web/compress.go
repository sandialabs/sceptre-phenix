package web

import (
	"bufio"
	"compress/gzip"
	"errors"
	"mime"
	"net"
	"net/http"
	"strconv"
	"sync"
)

// Responses smaller than this gain little from compression, so they are sent
// as-is when the handler declares a Content-Length below it.
const minCompressSize = 1024

// sniffLen is how much of the body net/http reads to sniff its content type.
const sniffLen = 512

var (
	// API responses worth compressing. Downloads (disks, captures, zips) are
	// binary or already compressed and are streamed through untouched.
	compressibleTypes = map[string]bool{ //nolint:gochecknoglobals // constant lookup table
		"application/json":   true,
		"application/yaml":   true,
		"application/x-yaml": true,
		"text/plain":         true,
		"text/yaml":          true,
	}

	gzipWriters = sync.Pool{ //nolint:gochecknoglobals // writer reuse across requests
		New: func() any {
			// BestSpeed: JSON still shrinks several fold, and responses are
			// compressed on every request rather than once like static assets
			zw, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)

			return zw
		},
	}

	errNotHijacker = errors.New("response writer does not support hijacking")
)

// CompressResponses gzip-compresses JSON, YAML and plain-text API responses for
// clients that accept it. Large list responses (VMs, disks, configs, logs) are
// repetitive JSON that shrinks several fold, which matters on the slow links
// the UI is often used over. Websocket upgrades and every other content type
// pass through as-is.
func CompressResponses(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")

		if r.Header.Get("Upgrade") != "" || !acceptsGzip(r) {
			h.ServeHTTP(w, r)

			return
		}

		cw := &compressWriter{ResponseWriter: w, zw: nil, decided: false, code: 0, sniff: nil}
		defer cw.close()

		h.ServeHTTP(cw, r)
	})
}

// compressWriter decides whether to compress when the handler writes its
// header, since that is when Content-Type and Content-Length are known. A
// handler that never sets Content-Type has it sniffed, as net/http does, from
// the first 512 bytes of the body: sniffing only the first Write would label a
// template whose output starts with a newline as text/plain.
type compressWriter struct {
	http.ResponseWriter

	zw      *gzip.Writer
	decided bool
	code    int    // status held back until the content type is known
	sniff   []byte // body held back until the content type is known
}

func (cw *compressWriter) WriteHeader(code int) {
	// informational (1xx) headers precede the real one
	if cw.decided || code < http.StatusOK {
		cw.ResponseWriter.WriteHeader(code)

		return
	}

	if cw.Header().Get("Content-Type") == "" && bodyAllowed(code) {
		cw.code = code

		return
	}

	cw.decide(code)
}

func (cw *compressWriter) Write(b []byte) (int, error) {
	if !cw.decided {
		if cw.Header().Get("Content-Type") != "" {
			cw.decide(cw.status())
		} else {
			cw.sniff = append(cw.sniff, b...)
			if len(cw.sniff) < sniffLen {
				return len(b), nil
			}

			if err := cw.flushSniffed(); err != nil {
				return 0, err
			}

			return len(b), nil
		}
	}

	return cw.write(b)
}

func (cw *compressWriter) Flush() {
	_ = cw.flushSniffed()

	if cw.zw != nil {
		_ = cw.zw.Flush()
	}

	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (cw *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := cw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}

	return nil, nil, errNotHijacker
}

// Unwrap lets [http.ResponseController] reach the underlying writer.
func (cw *compressWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}

func (cw *compressWriter) close() {
	_ = cw.flushSniffed()

	if cw.zw == nil {
		return
	}

	_ = cw.zw.Close()
	cw.zw.Reset(nil)
	gzipWriters.Put(cw.zw)
	cw.zw = nil
}

func (cw *compressWriter) status() int {
	if cw.code != 0 {
		return cw.code
	}

	return http.StatusOK
}

func (cw *compressWriter) decide(code int) {
	cw.decided = true

	if shouldCompress(code, cw.Header()) {
		cw.Header().Set("Content-Encoding", "gzip")
		cw.Header().Del("Content-Length")

		zw, _ := gzipWriters.Get().(*gzip.Writer)
		zw.Reset(cw.ResponseWriter)
		cw.zw = zw
	}

	cw.ResponseWriter.WriteHeader(code)
}

// flushSniffed sends a held-back status and body, sniffing the content type
// from whatever body there is.
func (cw *compressWriter) flushSniffed() error {
	if cw.decided || (cw.code == 0 && len(cw.sniff) == 0) {
		return nil
	}

	if len(cw.sniff) > 0 && cw.Header().Get("Content-Type") == "" {
		cw.Header().Set("Content-Type", http.DetectContentType(cw.sniff))
	}

	cw.decide(cw.status())

	body := cw.sniff
	cw.sniff = nil

	if len(body) == 0 {
		return nil
	}

	_, err := cw.write(body)

	return err
}

func (cw *compressWriter) write(b []byte) (int, error) {
	if cw.zw != nil {
		return cw.zw.Write(b)
	}

	return cw.ResponseWriter.Write(b)
}

func bodyAllowed(code int) bool {
	return code != http.StatusNoContent && code != http.StatusNotModified
}

func shouldCompress(code int, header http.Header) bool {
	switch {
	case code < http.StatusOK,
		code == http.StatusNoContent,
		code == http.StatusPartialContent, // byte ranges refer to the uncompressed body
		code == http.StatusNotModified:
		return false
	}

	if header.Get("Content-Encoding") != "" {
		return false
	}

	if cl := header.Get("Content-Length"); cl != "" {
		if n, err := strconv.Atoi(cl); err == nil && n < minCompressSize {
			return false
		}
	}

	mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))

	return err == nil && compressibleTypes[mediaType]
}
