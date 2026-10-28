package web

import (
	"io"
	"net/http"
	"regexp"
	"sync"

	"phenix/version"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// openAPIPath is where the assets hold the API reference's spec.
const openAPIPath = "/docs/openapi.yml"

// openAPIInfoVersion is the spec's info.version line, the only key at that
// indentation named version.
var openAPIInfoVersion = regexp.MustCompile(`(?m)^  version: .*$`)

// OpenAPISpecHandler serves the assets' openapi.yml with info.version set to
// the server's build, cached per file as StaticHandler caches assets.
func OpenAPISpecHandler(assets http.FileSystem) http.Handler {
	var cache sync.Map // assetKey -> *memAsset

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := assets.Open(openAPIPath)
		if err != nil {
			weberror.Error(w, "not found", http.StatusNotFound)

			return
		}
		defer f.Close()

		fi, err := f.Stat()
		if err != nil {
			weberror.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		key := assetKey{name: openAPIPath, size: fi.Size(), modTime: fi.ModTime()}

		var asset *memAsset
		if cached, ok := cache.Load(key); ok {
			asset, _ = cached.(*memAsset)
		} else {
			raw, err := io.ReadAll(f)
			if err != nil {
				weberror.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}

			stamped := openAPIInfoVersion.ReplaceAll(raw, []byte("  version: "+version.API()))

			asset, err = newMemAsset(stamped, fi.ModTime(), true)
			if err != nil {
				weberror.Error(w, err.Error(), http.StatusInternalServerError)

				return
			}

			cache.Store(key, asset)
		}

		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set(middleware.HeaderVersion, version.API())
		asset.serve(w, r, openAPIPath)
	})
}
