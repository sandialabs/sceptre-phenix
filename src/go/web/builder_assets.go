package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"

	"phenix/util/plog"
)

// The server sends the Builder front-end files (the files that only its page
// loads) compressed to clients that accept it. Clients may cache these files
// for good, because Vite names each file after a hash of its content.
// Every other file under /assets/ is served as [http.FileServer] serves it.
//
// The UI build lists the files in builder-assets.json, beside index.html,
// and writes each one's Brotli and gzip copies next to it, as <file>.br and
// <file>.gz, when they are smaller (src/js/plugins/builder-assets.js). The
// server sends the copy the request's Accept-Encoding prefers, and the file
// itself when it accepts neither or the build left the copy out.
const (
	builderAssetList = "builder-assets.json"

	// A year: a file's name changes whenever its content does.
	builderAssetCacheControl = "public, max-age=31536000, immutable"
)

// builderCodings returns the content codings the build compresses the
// Builder's files with, in the order the server prefers them.
func builderCodings() []string {
	return []string{"br", "gzip"}
}

// encodingSuffix returns the suffix of the file the build writes a file's
// copy in coding in, "" for the file itself.
func encodingSuffix(coding string) string {
	switch coding {
	case "br":
		return ".br"
	case "gzip":
		return ".gz"
	default:
		return ""
	}
}

// builderAssetHandler serves /assets/ from assets.
func builderAssetHandler(assets http.FileSystem) http.Handler {
	next := http.FileServer(assets)

	files, err := builderFiles(assets)
	if err != nil {
		plog.Warn(plog.TypeSystem, "serving Builder assets uncompressed", "err", err)

		return next
	}

	var (
		// Each Builder file, by its path, with the codings the build
		// compressed it with.
		served = make(map[string][]string, len(files))

		// The compressed copies, which are only ever sent as a coding of
		// their file.
		copies = make(map[string]struct{}, len(builderCodings())*len(files))
	)

	for _, file := range files {
		name := "/" + file
		served[name] = nil

		for _, coding := range builderCodings() {
			copied := name + encodingSuffix(coding)
			copies[copied] = struct{}{}

			if isFile(assets, copied) {
				served[name] = append(served[name], coding)
			}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(r.URL.Path)

		if _, ok := copies[name]; ok {
			http.NotFound(w, r)

			return
		}

		codings, ok := served[name]
		if !ok {
			next.ServeHTTP(w, r)

			return
		}

		// A header sent on several lines is one list (RFC 9110, section 5.3),
		// so a later line's weights count as much as the first's.
		accept := strings.Join(r.Header.Values("Accept-Encoding"), ",")

		coding := preferredEncoding(accept, codings)

		f, err := assets.Open(name + encodingSuffix(coding))
		if err != nil {
			// Refused as any other file would be, and never cached.
			next.ServeHTTP(w, r)

			return
		}

		defer f.Close()

		info, err := f.Stat()
		if err != nil || info.IsDir() {
			next.ServeHTTP(w, r)

			return
		}

		w.Header().Set("Cache-Control", builderAssetCacheControl)
		w.Header().Add("Vary", "Accept-Encoding")

		if coding != "" {
			w.Header().Set("Content-Encoding", coding)

			w = encodedLengthWriter{ResponseWriter: w, length: info.Size()}
		}

		// Named after the file itself, which gives the content its type.
		http.ServeContent(w, r, path.Base(name), info.ModTime(), f)
	})
}

// encodedLengthWriter gives a whole compressed file its Content-Length, so
// the file is not sent chunked. [http.ServeContent] leaves the length out
// when Content-Encoding is set. Only a 200 carries the whole file.
// ServeContent sets the length of a 206 itself. A 304, 412 or 416 has no
// content, so it has no Content-Encoding either.
type encodedLengthWriter struct {
	http.ResponseWriter

	length int64
}

func (w encodedLengthWriter) WriteHeader(status int) {
	switch status {
	case http.StatusOK:
		w.Header().Set("Content-Length", strconv.FormatInt(w.length, 10))
	case http.StatusPartialContent:
	default:
		w.Header().Del("Content-Encoding")
	}

	w.ResponseWriter.WriteHeader(status)
}

// builderFiles returns the files, relative to the UI's root, that only
// Builder loads, from the list the UI build wrote in assets.
func builderFiles(assets http.FileSystem) ([]string, error) {
	f, err := assets.Open("/" + builderAssetList)
	if err != nil {
		return nil, fmt.Errorf("opening the UI build's list of Builder files: %w", err)
	}

	defer f.Close()

	var files []string

	if err := json.NewDecoder(f).Decode(&files); err != nil {
		return nil, fmt.Errorf("reading the UI build's list of Builder files: %w", err)
	}

	if len(files) == 0 {
		return nil, errors.New("the UI build's list of Builder files is empty")
	}

	return files, nil
}

// isFile reports whether assets has a file, not a directory, named name.
func isFile(assets http.FileSystem, name string) bool {
	f, err := assets.Open(name)
	if err != nil {
		return false
	}

	defer f.Close()

	info, err := f.Stat()

	return err == nil && !info.IsDir()
}

// preferredEncoding returns the coding of offered that an Accept-Encoding
// header weighs highest. offered is in the order of preference of the
// server, and that order decides between codings of equal weight. It returns
// "" when the header accepts none of them. A coding that the header does not
// name has the weight of "*", or none.
func preferredEncoding(header string, offered []string) string {
	weights := acceptEncodingWeights(header)

	var (
		best       string
		bestWeight float64
	)

	for _, coding := range offered {
		weight, ok := weights[coding]
		if !ok {
			weight = weights["*"]
		}

		if weight > bestWeight {
			best, bestWeight = coding, weight
		}
	}

	return best
}

// acceptEncodingWeights returns the weight, from 0 to 1, of each coding an
// Accept-Encoding header names, in lowercase. A coding's first entry counts,
// and an entry whose weight is not a number from 0 to 1 is left out.
func acceptEncodingWeights(header string) map[string]float64 {
	weights := make(map[string]float64)

	for entry := range strings.SplitSeq(header, ",") {
		coding, params, _ := strings.Cut(entry, ";")

		coding = strings.ToLower(strings.TrimSpace(coding))
		if coding == "" {
			continue
		}

		weight, ok := qualityValue(params)
		if !ok {
			continue
		}

		if _, seen := weights[coding]; !seen {
			weights[coding] = weight
		}
	}

	return weights
}

// qualityValue returns the weight the q parameter among an entry's params
// gives, 1 without one, and whether it is a number from 0 to 1.
func qualityValue(params string) (float64, bool) {
	for param := range strings.SplitSeq(params, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(param), "=")
		if !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}

		weight, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || math.IsNaN(weight) || weight < 0 || weight > 1 {
			return 0, false
		}

		return weight, true
	}

	return 1, true
}
