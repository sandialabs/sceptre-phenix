package web

import (
	"io/fs"
	"net/http"
	"sync"
	"time"

	"phenix/util/plog"
	"phenix/web/builderbundle"
)

// BuilderBundleHandler serves the topology builder's scripts as one file (see
// builderbundle). The bundle is built once, in the background at startup,
// except when rebuild is set (unbundled assets): then it is built per request
// so edits to the grapheditor show up.
func BuilderBundleHandler(grapheditor fs.FS, rebuild bool) http.Handler {
	var (
		mu     sync.Mutex
		bundle *memAsset
	)

	load := func() (*memAsset, error) {
		mu.Lock()
		defer mu.Unlock()

		if bundle != nil && !rebuild {
			return bundle, nil
		}

		src, err := builderbundle.Build(grapheditor)
		if err != nil {
			return nil, err
		}

		built, err := newMemAsset(src, time.Time{}, true)
		if err != nil {
			return nil, err
		}

		bundle = built

		return bundle, nil
	}

	if !rebuild {
		// build ahead of the first visit, which would otherwise wait on it
		go func() { _, _ = load() }()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asset, err := load()
		if err != nil {
			plog.Error(plog.TypeSystem, "building the topology builder bundle", "err", err)
			http.Error(w, "building the topology builder scripts failed", http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Add("Vary", "Accept-Encoding")

		asset.serve(w, r, "builder.bundle.js")
	})
}
