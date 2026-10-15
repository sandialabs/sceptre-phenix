package web

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/livecapture"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/capability"
	"phenix/web/middleware"
	"phenix/web/util"
)

// WebShark (github.com/QXIP/webshark) dissects packets in the browser, with
// Wireshark compiled to WebAssembly. phēnix serves its UI build from the
// first of these directories that has it (the working directory's, like the
// tunneler downloads, then where the image and package install it), and
// stands in for the small server it normally runs with: that server lists
// and serves capture files from a directory and reports when one grows, all
// under /webshark/.
var websharkDirs = []string{"webshark", "/opt/phenix/webshark"} //nolint:gochecknoglobals // constant list

// findWebShark returns the directory of the WebShark build, if there is one.
func findWebShark() (string, bool) {
	for _, dir := range websharkDirs {
		if info, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && info.Mode().IsRegular() {
			return dir, true
		}
	}

	return "", false
}

const (
	sharkHeartbeat = 15 * time.Second

	// WebShark downloads and dissects the whole file again whenever a live
	// capture grows, so it is told at most this often, and less often the
	// larger the capture.
	sharkMinUpdate      = time.Second
	sharkMaxUpdate      = 10 * time.Second
	sharkBytesPerSecond = 10 << 20

	maxSharkRequestBytes = 4096
)

var (
	errSharkCaptureNotFound = errors.New("capture not found")

	// captureExtensions are the files WebShark opens.
	captureExtensions = []string{".pcap", ".pcapng", ".cap"} //nolint:gochecknoglobals // constant list
)

func isCaptureFile(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".gz")

	for _, ext := range captureExtensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}

	return false
}

type sharkCaptureRequest struct {
	Path      string `json:"path"`
	VM        string `json:"vm"`
	Interface *int   `json:"interface"`
}

type sharkCaptureResponse struct {
	Capture string `json:"capture"`
	Live    bool   `json:"live"`
	Title   string `json:"title"`
	ID      string `json:"id"`
	Expires string `json:"expires"`
}

// CreateWebSharkCapture - POST /experiments/{exp}/webshark
//
// Names a capture for WebShark to open: a saved capture file ({"path": ...},
// as the experiment's files are listed) or a VM interface's running capture
// ({"vm": ..., "interface": ...}). A file still being captured is opened
// live. The name is a capture grant of the user's (see capability.go).
func CreateWebSharkCapture(w http.ResponseWriter, r *http.Request) {
	var (
		exp = mux.Vars(r)["exp"]
		req sharkCaptureRequest
	)

	if !o.featured("webshark") {
		http.Error(w, "WebShark is not installed on this phēnix server", http.StatusNotImplemented)

		return
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSharkRequestBytes)).Decode(&req)
	if err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)

		return
	}

	target, ok := resolveSharkTarget(w, r, exp, req, audit.CaptureWebShark)
	if !ok {
		return
	}

	g, name := mintCaptureGrant(r, target, viewOps)
	title := sharkTitle(target)

	audit.Success(r, audit.CaptureWebShark, "opened capture in WebShark", mintedAuditFields(g))

	body, _ := json.Marshal(sharkCaptureResponse{
		Capture: name,
		Live:    target.Live,
		Title:   title,
		ID:      g.ID,
		Expires: g.Expires.UTC().Format(time.RFC3339),
	})

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// resolveSharkTarget finds the capture a request names, a saved capture file
// or a running capture, once it has checked that the user's role may read
// it. It writes the error response and returns false when it does not,
// auditing a refusal or failure as action a.
func resolveSharkTarget(
	w http.ResponseWriter, r *http.Request, exp string, req sharkCaptureRequest, a audit.Action,
) (capability.Target, bool) {
	role := middleware.RoleFromContext(r.Context())

	switch {
	case req.Path != "" && req.VM == "":
		if !role.Allowed("experiments/files", "get", exp) {
			audit.Denied(r, a, "opening experiment file in WebShark not allowed", audit.Fields{Experiment: exp, File: req.Path})
			http.Error(w, "forbidden", http.StatusForbidden)

			return capability.Target{}, false
		}

		if !isCaptureFile(req.Path) {
			http.Error(w, "WebShark opens .pcap, .pcapng and .cap files", http.StatusBadRequest)

			return capability.Target{}, false
		}

		var writing *experiment.CaptureWritingError

		local, err := experiment.LocalFile(exp, req.Path)

		switch {
		case err == nil:
			return capability.Target{Exp: exp, File: req.Path, VM: "", Interface: 0, Live: false, Local: local}, true
		case errors.As(err, &writing):
			req.VM, req.Interface = writing.Capture.VM, &writing.Capture.Interface
		default:
			audit.Failure(r, a, "getting experiment file for WebShark failed", audit.Fields{Experiment: exp, File: req.Path}, err)

			status := fileErrorStatus(err)
			if status == http.StatusInternalServerError {
				plog.Error(plog.TypeSystem, "getting experiment file for WebShark", "exp", exp, "file", req.Path, "err", err)
			}

			http.Error(w, err.Error(), status)

			return capability.Target{}, false
		}
	case req.VM != "" && req.Path == "":
		if req.Interface == nil || *req.Interface < 0 {
			http.Error(w, "interface must be an interface index", http.StatusBadRequest)

			return capability.Target{}, false
		}
	default:
		http.Error(w, `give either "path" or "vm" and "interface"`, http.StatusBadRequest)

		return capability.Target{}, false
	}

	// a running capture
	fields := audit.Fields{Experiment: exp, VM: req.VM, Interface: req.Interface, File: req.Path}

	if !captureReadAllowed(role, exp, req.VM) {
		audit.Denied(r, a, "opening VM capture in WebShark not allowed", fields)
		http.Error(w, "forbidden", http.StatusForbidden)

		return capability.Target{}, false
	}

	// opening it starts following it, so it is ready when WebShark asks
	tap, err := livecapture.Open(exp, req.VM, *req.Interface)
	if err != nil {
		audit.Failure(r, a, "opening VM capture for WebShark failed", fields, err)

		status := captureStreamStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "opening VM capture for WebShark", "exp", exp, "vm", req.VM, "err", err)
		}

		http.Error(w, err.Error(), status)

		return capability.Target{}, false
	}

	capture := tap.Capture()
	tap.Release()

	file, _ := experiment.CaptureFile(exp, capture.Path)

	return capability.Target{
		Exp: exp, File: file, VM: capture.VM, Interface: capture.Interface, Live: true, Local: "",
	}, true
}

// sharkBaseName is the file name WebShark shows and downloads a capture as.
func sharkBaseName(t capability.Target) string {
	if t.File != "" {
		return path.Base(t.File)
	}

	return fmt.Sprintf("%s-%d.pcap", t.VM, t.Interface)
}

// sharkTitle is how the WebShark page names a capture.
func sharkTitle(t capability.Target) string {
	if t.Live {
		return fmt.Sprintf("%s interface %d (live)", t.VM, t.Interface)
	}

	return t.File
}

// sharkSource opens what the grant's capture's bytes are read from: its
// running capture, or its file once that has stopped. The returned Tap, if
// any, must be released.
func sharkSource(g capability.Grant) (*livecapture.Tap, *os.File, error) {
	t := g.Target

	if t.Live {
		tap, err := livecapture.Open(t.Exp, t.VM, t.Interface)
		if err == nil {
			return tap, nil, nil
		}

		if !errors.Is(err, livecapture.ErrNotCapturing) || t.File == "" {
			return nil, nil, err //nolint:wrapcheck // reported as is
		}

		// the capture stopped and nothing is following it any more: its file
		// is complete now
		local, err := experiment.LocalFile(t.Exp, t.File)
		if err != nil {
			return nil, nil, err //nolint:wrapcheck // reported as is
		}

		captureGrants.Update(g.ID, func(g *capability.Grant) {
			g.Target.Live, g.Target.Local = false, local
		})

		t.Local = local
	}

	f, err := os.Open(t.Local)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", errSharkCaptureNotFound, t.File)
	}

	return nil, f, nil
}

// serveSharkCapture sends the bytes of the capture the grant name names, if
// it allows op: as far as a running capture has got, or the whole file.
func (c grantChecker) serveSharkCapture(
	w http.ResponseWriter, r *http.Request, name string, op capability.Op, attachment bool,
) {
	g, status := c.use(r, name, op)
	if status != 0 {
		sharkError(w, http.StatusText(status), status)

		return
	}

	tap, f, err := sharkSource(g)
	if err != nil {
		plog.Warn(plog.TypeSystem, "opening capture for WebShark", "exp", g.Target.Exp, "id", g.ID, "err", err)
		audit.Failure(r, audit.CaptureWebSharkRead, "reading capture for WebShark failed", grantAuditFields(r, g, op), err)
		sharkError(w, "not found", http.StatusNotFound)

		return
	}

	base := sharkBaseName(g.Target)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")

	if attachment {
		w.Header().Set("Content-Disposition", util.Attachment(base))
	}

	if tap != nil {
		defer tap.Release()

		// still growing: no Last-Modified, so a resumed download (If-Range)
		// gets the capture as it is now rather than splicing two versions
		st := tap.State()
		http.ServeContent(w, r, base, time.Time{}, io.NewSectionReader(tap, 0, st.Size))

		auditSharkRead(r, g, op, readsFromStart(r))

		return
	}

	defer f.Close()

	// a finished file: resumable, with a strong ETag
	saveAs := ""
	if attachment {
		saveAs = base
	}

	served, err := util.ServeOpenFile(w, r, f, util.Download{
		Path:        f.Name(),
		Name:        saveAs,
		ContentType: "application/octet-stream",
		Live:        false,
		MaxSize:     0,
	})
	if err != nil {
		sharkError(w, "not found", http.StatusNotFound)

		return
	}

	auditSharkRead(r, g, op, served.Started)
}

// sharkViewsAudited holds the IDs of the grants whose first WebShark view of
// their capture has been audited: WebShark reads a capture again each time
// it grows, so only the first read of each grant is.
var sharkViewsAudited sync.Map //nolint:gochecknoglobals // grant IDs, each dropped when its grant ends

// auditSharkRead audits a read of the grant's capture for op that began a
// download (started), once per download, or for a view, once per grant.
func auditSharkRead(r *http.Request, g capability.Grant, op capability.Op, started bool) {
	if !started {
		return
	}

	if op == capability.OpView {
		if _, seen := sharkViewsAudited.LoadOrStore(g.ID, struct{}{}); seen {
			return
		}

		go func() {
			<-g.Done
			sharkViewsAudited.Delete(g.ID)
		}()
	}

	audit.Success(r, audit.CaptureWebSharkRead, "read capture for WebShark", grantAuditFields(r, g, op))
}

// readsFromStart reports whether the request reads a file from its first
// byte: a GET of all of it, or of a range starting there.
func readsFromStart(r *http.Request) bool {
	rng := r.Header.Get("Range")

	return r.Method == http.MethodGet && (rng == "" || strings.HasPrefix(rng, "bytes=0-"))
}

func sharkError(w http.ResponseWriter, msg string, status int) {
	body, _ := json.Marshal(map[string]any{"err": 1, "errstr": msg})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// GetWebSharkJSON - GET /webshark/json?method=...
//
// WebShark lists the captures it may open (none: phēnix hands out each one
// by name) and downloads them here.
func (c grantChecker) GetWebSharkJSON(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	switch method := query.Get("method"); method {
	case "files":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[],"pwd":"."}`))
	case "download":
		c.serveSharkCapture(w, r, query.Get("capture"), capability.OpDownload, true)
	default:
		sharkError(w, "method not allowed: "+method, http.StatusBadRequest)
	}
}

// GetWebSharkCaptureFile - GET /webshark/captures/{name}.
func (c grantChecker) GetWebSharkCaptureFile(w http.ResponseWriter, r *http.Request) {
	c.serveSharkCapture(w, r, mux.Vars(r)["name"], capability.OpView, false)
}

// GetWebSharkWatch - GET /webshark/watch?capture=...
//
// Server-sent events telling WebShark when a capture grows, so it reloads
// the capture and shows the new packets: an "init" event with the size it
// has now, then an "append" event whenever it has grown, and heartbeats. The
// watch ends when the capture's grant does.
func (c grantChecker) GetWebSharkWatch(w http.ResponseWriter, r *http.Request) {
	g, status := c.use(r, r.URL.Query().Get("capture"), capability.OpWatch)
	if status != 0 {
		sharkError(w, http.StatusText(status), status)

		return
	}

	tap, f, err := sharkSource(g)
	if err != nil {
		sharkError(w, "not found", http.StatusNotFound)

		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := c.untilRevoked(r.Context(), g)
	defer cancel()

	watch := &sharkWatch{w: w, rc: http.NewResponseController(w), heartbeat: time.NewTicker(sharkHeartbeat)}
	defer watch.heartbeat.Stop()

	if tap == nil {
		defer f.Close()

		// a saved capture does not change
		info, err := f.Stat()
		if err == nil && watch.changed(info.Size(), info.ModTime(), "init") {
			watch.wait(ctx, nil, nil)
		}

		return
	}

	defer tap.Release()

	watchSharkTap(ctx, watch, tap)
}

// watchSharkTap tells WebShark when a running capture grows, as often as
// sharkUpdateInterval allows.
func watchSharkTap(ctx context.Context, watch *sharkWatch, tap *livecapture.Tap) {
	st := tap.State()
	if !watch.changed(st.Size, st.Modified, "init") {
		return
	}

	var (
		told = st.Size
		next = time.Now()
	)

	for {
		if st.Size > told && !time.Now().Before(next) {
			if !watch.changed(st.Size, st.Modified, "append") {
				return
			}

			told = st.Size
			next = time.Now().Add(sharkUpdateInterval(st.Size))
		}

		var due <-chan time.Time
		if st.Size > told {
			due = time.After(time.Until(next))
		}

		if !watch.wait(ctx, due, st.Changed) {
			return
		}

		st = tap.State()
	}
}

// sharkWatch writes the server-sent events of a watch.
type sharkWatch struct {
	w         http.ResponseWriter
	rc        *http.ResponseController
	heartbeat *time.Ticker
}

// sharkWriteTimeout bounds one event's write, so a watcher that stopped
// reading does not keep its capture followed.
const sharkWriteTimeout = 10 * time.Second

func (e *sharkWatch) send(event string, data any) bool {
	body, _ := json.Marshal(data)

	// not every writer takes a deadline; one that does not is not bounded
	_ = e.rc.SetWriteDeadline(time.Now().Add(sharkWriteTimeout))

	if _, err := fmt.Fprintf(e.w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return false
	}

	return e.rc.Flush() == nil
}

func (e *sharkWatch) changed(size int64, mtime time.Time, kind string) bool {
	return e.send("capture-changed", map[string]any{
		"size": size, "mtime": float64(mtime.UnixNano()) / float64(time.Millisecond), "kind": kind,
	})
}

// wait sends heartbeats until either channel fires (true) or the watch ends
// (false). A nil channel never fires.
func (e *sharkWatch) wait(ctx context.Context, due <-chan time.Time, changed <-chan struct{}) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-e.heartbeat.C:
			if !e.send("heartbeat", map[string]any{"t": time.Now().UnixMilli()}) {
				return false
			}
		case <-due:
			return true
		case <-changed:
			return true
		}
	}
}

// sharkUpdateInterval is how long WebShark is left with a capture of size
// bytes before it is told the capture grew again.
func sharkUpdateInterval(size int64) time.Duration {
	return min(sharkMinUpdate+time.Duration(size/sharkBytesPerSecond)*time.Second, sharkMaxUpdate)
}

// WebSharkHandler serves the WebShark UI build from dir: the app for its
// embed routes, and its files, sending a file's precompressed .gz copy when
// the build has one (the WebAssembly is otherwise 69 MB). basePath is the
// path the phēnix UI is served under, behind a proxy when it is not "/".
func WebSharkHandler(dir, basePath string) http.Handler {
	fsys := os.DirFS(dir)
	rebaser := newSharkRebaser(fsys, basePath)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		if clean != "/webshark" && !strings.HasPrefix(clean, "/webshark/") {
			http.NotFound(w, r)

			return
		}

		rel := strings.TrimPrefix(strings.TrimPrefix(clean, "/webshark"), "/")

		if rel == "" || rel == "embed" || strings.HasPrefix(rel, "embed/") {
			rel = "index.html"
		}

		if !fs.ValidPath(rel) {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Add("Vary", "Accept-Encoding")

		if trustworthyOrigin(r) {
			// lets WebShark use threads when opened in a tab of its own (an
			// iframe in phēnix's own page cannot, and works without them).
			// Its workers need the same policy as the page that starts them.
			w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
			w.Header().Set("Cross-Origin-Embedder-Policy", "credentialless")
		}

		w.Header().Set("Content-Type", sharkContentType(rel))

		if rel == sharkServiceWorker {
			http.ServeContent(w, r, rel, time.Time{}, strings.NewReader(sharkServiceWorkerRemover))

			return
		}

		if rebaser.serve(w, r, rel) {
			return
		}

		if acceptsGzip(r) && serveSharkFile(w, r, fsys, rel+".gz", true) {
			return
		}

		if serveSharkFile(w, r, fsys, rel, false) {
			return
		}

		// the build keeps only the compressed copy of its largest files
		if gz, err := fsys.Open(rel + ".gz"); err == nil {
			defer gz.Close()

			zr, err := gzip.NewReader(gz)
			if err != nil {
				http.Error(w, "corrupt file", http.StatusInternalServerError)

				return
			}

			_, _ = io.Copy(w, zr) //nolint:gosec // our own build's file

			return
		}

		http.NotFound(w, r)
	})
}

// sharkServiceWorker is the service worker WebShark's page registers when it
// is served over HTTPS without cross-origin isolation, to add the isolation
// headers itself. phēnix sends them itself where it can (only a page of its
// own tab can be isolated, not one in phēnix's frame), and the service worker
// sends WebShark's scripts and styles on without cookies, so a proxy that
// signs users in by cookie refuses them and WebShark fails to load Wireshark.
// phēnix serves sharkServiceWorkerRemover in its place.
const sharkServiceWorker = "enable-threads.js"

// sharkServiceWorkerRemover unregisters the service worker from browsers that
// registered it, and reloads the pages it controlled, whose requests went
// through it. It does so as the service worker's update, which browsers fetch
// when a page in its scope opens, and as the page's script, where that runs.
const sharkServiceWorkerRemover = `// phēnix: removes the service worker WebShark registers here
(function () {
  if (typeof window === 'undefined') {
    self.addEventListener('install', function () { self.skipWaiting(); });
    self.addEventListener('activate', function (e) {
      e.waitUntil(self.registration.unregister().then(function () {
        return self.clients.matchAll({ type: 'window' });
      }).then(function (pages) {
        return Promise.all(pages.map(function (p) { return p.navigate(p.url); }));
      }).catch(function () {}));
    });
    return;
  }
  var sw = navigator.serviceWorker;
  var src = document.currentScript && document.currentScript.src;
  if (!sw || !src) return;
  sw.getRegistrations().then(function (registrations) {
    return Promise.all(registrations.filter(function (r) {
      var w = r.active || r.waiting || r.installing;
      return w && w.scriptURL === src;
    }).map(function (r) { return r.unregister(); }));
  }).then(function (removed) {
    if (removed.indexOf(true) !== -1 && sw.controller) location.reload();
  }).catch(function () {});
})();
`

// A base path the WebShark build's scripts can take inside a string literal
// as is.
var sharkBasePath = regexp.MustCompile(`^/[A-Za-z0-9._~%/-]*/$`)

// sharkRebaser serves the WebShark build's page, scripts and styles with the
// root WebShark hardcodes, /webshark/, moved under the base path phēnix is
// served behind: its page's base href and the URLs its scripts fetch are
// otherwise root-absolute, and miss phēnix behind a proxy that serves it
// under a path. Each file is rewritten once, when first asked for. A nil
// sharkRebaser, for phēnix served at "/", serves nothing.
type sharkRebaser struct {
	fsys     fs.FS
	replacer *strings.Replacer

	mu    sync.Mutex
	files map[string]*rebasedSharkFile
}

type rebasedSharkFile struct {
	once  sync.Once
	asset *memAsset // nil when the file does not name the root
	err   error
}

func newSharkRebaser(fsys fs.FS, basePath string) *sharkRebaser {
	if basePath == "/" {
		return nil
	}

	if !sharkBasePath.MatchString(basePath) {
		plog.Warn(
			plog.TypeSystem,
			"WebShark works only at /webshark/: the base path has characters its scripts cannot take",
			"path", basePath,
		)

		return nil
	}

	// the root as it starts a string literal, or continues a template
	// literal after an expression (`${origin}/webshark/...`)
	pairs := make([]string, 0, 8) //nolint:mnd // 4 pairs
	for _, quote := range []string{`"`, `'`, "`", "}"} {
		pairs = append(pairs, quote+"/webshark/", quote+basePath+"webshark/")
	}

	return &sharkRebaser{
		fsys:     fsys,
		replacer: strings.NewReplacer(pairs...),
		mu:       sync.Mutex{},
		files:    make(map[string]*rebasedSharkFile),
	}
}

// serve serves the build's file rel rewritten, reporting whether it did: it
// does not for a file that is not a page, script or style sheet, is not in
// the build, or does not name the root.
func (s *sharkRebaser) serve(w http.ResponseWriter, r *http.Request, rel string) bool {
	if s == nil {
		return false
	}

	switch path.Ext(rel) {
	case ".html", ".js", ".mjs", ".css":
	default:
		return false
	}

	f := s.file(rel)
	if f == nil || f.err != nil || f.asset == nil {
		return false
	}

	return f.asset.serve(w, r, rel)
}

// file returns the build's file rel rewritten, or nil when the build has no
// such file. Only the build's files are kept, so requests for other names
// cannot grow the cache.
func (s *sharkRebaser) file(rel string) *rebasedSharkFile {
	s.mu.Lock()
	f, ok := s.files[rel]
	s.mu.Unlock()

	if !ok {
		if !sharkFileExists(s.fsys, rel) && !sharkFileExists(s.fsys, rel+".gz") {
			return nil
		}

		s.mu.Lock()
		if f, ok = s.files[rel]; !ok {
			f = new(rebasedSharkFile)
			s.files[rel] = f
		}
		s.mu.Unlock()
	}

	f.once.Do(func() {
		b, mod, err := readSharkFile(s.fsys, rel)
		if err != nil {
			f.err = err

			return
		}

		out := s.replacer.Replace(string(b))
		if out == string(b) {
			return
		}

		f.asset, f.err = newMemAsset([]byte(out), mod, true)
	})

	return f
}

func sharkFileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)

	return err == nil && info.Mode().IsRegular()
}

// readSharkFile reads the build's file name, or its compressed copy when the
// build keeps only that.
func readSharkFile(fsys fs.FS, name string) ([]byte, time.Time, error) {
	if info, err := fs.Stat(fsys, name); err == nil && info.Mode().IsRegular() {
		b, err := fs.ReadFile(fsys, name)

		return b, info.ModTime(), err
	}

	f, err := fsys.Open(name + ".gz")
	if err != nil {
		return nil, time.Time{}, err
	}

	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, time.Time{}, err
	}

	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, time.Time{}, err
	}

	b, err := io.ReadAll(zr) //nolint:gosec // our own build's file

	return b, info.ModTime(), err
}

// serveSharkFile serves a regular file of the build, reporting whether there
// was one.
func serveSharkFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string, gzipped bool) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}

	defer f.Close()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}

	rs, ok := f.(io.ReadSeeker)
	if !ok {
		return false
	}

	if gzipped {
		w.Header().Set("Content-Encoding", "gzip")
	}

	http.ServeContent(w, r, name, info.ModTime(), rs)

	return true
}

// sharkContentType is the media type of a file of the WebShark build.
func sharkContentType(name string) string {
	switch path.Ext(name) {
	case ".wasm":
		return "application/wasm"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".data":
		return "application/octet-stream"
	}

	if ctype := mime.TypeByExtension(path.Ext(name)); ctype != "" {
		return ctype
	}

	return "application/octet-stream"
}

// trustworthyOrigin reports whether the browser treats the page as a secure
// context, which cross-origin isolation needs.
func trustworthyOrigin(r *http.Request) bool {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") ||
		forwardedHTTPS(r.Header.Get("Forwarded")) {
		return true
	}

	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// forwardedHTTPS reports whether a Forwarded header (RFC 7239) says the
// client's request came over HTTPS, as the first proxy it passed saw it.
func forwardedHTTPS(forwarded string) bool {
	first, _, _ := strings.Cut(forwarded, ",")
	for pair := range strings.SplitSeq(first, ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(pair), "=")
		if strings.EqualFold(k, "proto") {
			return strings.EqualFold(strings.Trim(v, `"`), "https")
		}
	}

	return false
}
