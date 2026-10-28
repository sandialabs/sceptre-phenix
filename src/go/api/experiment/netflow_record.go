package experiment

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"phenix/store"
	"phenix/util/file"
	"phenix/util/plog"
)

const (
	// netflowIDLayout is a capture's ID: its UTC start time, fixed width so
	// IDs sort by time.
	netflowIDLayout = "20060102T150405.000Z"

	netflowDirName       = "netflow"
	netflowSidecarFormat = 1
	netflowDirMode       = 0o750
	netflowFileMode      = 0o600
	netflowMaxSidecar    = 64 << 10

	NetflowStateRecording   = "recording"
	NetflowStateStopped     = "stopped"
	NetflowStateInterrupted = "interrupted"
)

var (
	netflowIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{3}Z$`)

	// liveRecorders holds each experiment's recorder from its creation until
	// the capture is finalized or discarded.
	liveRecorders   = make(map[string]*netflowRecorder) //nolint:gochecknoglobals // package level registry
	liveRecordersMu sync.Mutex                          //nolint:gochecknoglobals // package level registry

	ErrNetflowCaptureNotFound = errors.New("netflow capture not found")
	ErrInvalidNetflowCapture  = errors.New("invalid netflow capture id")
)

// NetflowCapture is a recorded netflow capture, as the capture list shows it.
type NetflowCapture struct {
	ID        string     `json:"id"`
	Started   time.Time  `json:"started"`
	Stopped   *time.Time `json:"stopped"`
	State     string     `json:"state"`
	Size      int64      `json:"size"`
	Flows     *int64     `json:"flows"`
	Truncated bool       `json:"truncated"`
	Path      string     `json:"path"`
}

// netflowSidecar is the metadata kept beside a capture's lines in <id>.json.
type netflowSidecar struct {
	Version    int        `json:"version"`
	Experiment string     `json:"experiment"`
	Started    time.Time  `json:"started"`
	Stopped    *time.Time `json:"stopped,omitempty"`
	Flows      int64      `json:"flows"`
	Truncated  bool       `json:"truncated"`
	MaxSize    int64      `json:"maxSize"`
}

// netflowRecorder writes a capture's lines, as received, to <dir>/<id>.txt.
type netflowRecorder struct {
	mu        sync.Mutex
	exp, id   string
	dir       string
	f         *os.File // nil once recording ends (cap, error, close)
	size, max int64
	flows     int64
	truncated bool
	started   time.Time
	stopped   time.Time
	closed    bool
}

// netflowDir is where the experiment's netflow captures are recorded.
func netflowDir(filesDir string) string {
	return filepath.Join(filesDir, netflowDirName)
}

// newNetflowRecorder creates the capture's files in dir, which it makes if
// needed, and registers the recorder as the experiment's live one.
func newNetflowRecorder(exp, dir string, maxSize int64, now time.Time) (*netflowRecorder, error) {
	if err := os.MkdirAll(dir, netflowDirMode); err != nil {
		return nil, fmt.Errorf("making netflow capture directory: %w", err)
	}

	started := now.UTC()
	id := started.Format(netflowIDLayout)

	f, err := os.OpenFile(filepath.Join(dir, id+".txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, netflowFileMode)
	if err != nil {
		return nil, fmt.Errorf("creating netflow capture: %w", err)
	}

	r := &netflowRecorder{
		mu: sync.Mutex{}, exp: exp, id: id, dir: dir, f: f, size: 0, max: maxSize,
		flows: 0, truncated: false, started: started, stopped: time.Time{}, closed: false,
	}

	if err := r.writeSidecar(); err != nil {
		_ = f.Close()
		_ = os.Remove(r.path(".txt"))

		return nil, err
	}

	liveRecordersMu.Lock()
	liveRecorders[exp] = r
	liveRecordersMu.Unlock()

	return r, nil
}

func (r *netflowRecorder) path(ext string) string {
	return filepath.Join(r.dir, r.id+ext)
}

// writeSidecar writes the capture's metadata in place; r.mu is held or the
// recorder is not yet shared.
func (r *netflowRecorder) writeSidecar() error {
	meta := netflowSidecar{
		Version: netflowSidecarFormat, Experiment: r.exp, Started: r.started, Stopped: nil,
		Flows: r.flows, Truncated: r.truncated, MaxSize: r.max,
	}

	if r.closed {
		stopped := r.stopped
		meta.Stopped = &stopped
	}

	body, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encoding netflow capture metadata: %w", err)
	}

	if err := os.WriteFile(r.path(".json"), body, netflowFileMode); err != nil {
		return fmt.Errorf("writing netflow capture metadata: %w", err)
	}

	return nil
}

// record appends one datagram's lines, each followed by a newline, in one
// write. Recording ends at the size limit or on a write error.
func (r *netflowRecorder) record(lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.f == nil || len(lines) == 0 {
		return
	}

	var (
		buf     []byte
		written int64
		full    bool
	)

	for _, line := range lines {
		if r.size+int64(len(buf))+int64(len(line))+1 > r.max {
			full = true

			break
		}

		buf = append(buf, line...)
		buf = append(buf, '\n')
		written++
	}

	if len(buf) > 0 {
		n, err := r.f.Write(buf)
		if err == nil && n < len(buf) {
			err = io.ErrShortWrite
		}

		if err != nil {
			truncErr := r.f.Truncate(r.size)
			r.stop()
			plog.Error(plog.TypeSystem, "recording netflow capture", "exp", r.exp, "id", r.id, "err", err,
				"truncate_err", truncErr)

			return
		}

		r.size += int64(n)
		r.flows += written
	}

	if full {
		r.stop()
		plog.Info(plog.TypeSystem, "netflow capture reached its size limit", "exp", r.exp, "id", r.id, "max", r.max)
	}
}

// stop ends recording early, marking the capture truncated; r.mu is held.
func (r *netflowRecorder) stop() {
	r.truncated = true
	_ = r.f.Close()
	r.f = nil

	if err := r.writeSidecar(); err != nil {
		plog.Error(plog.TypeSystem, "updating netflow capture", "exp", r.exp, "id", r.id, "err", err)
	}
}

// close finalizes the capture. It is safe to call more than once.
func (r *netflowRecorder) close(now time.Time) {
	r.mu.Lock()

	if r.closed {
		r.mu.Unlock()

		return
	}

	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}

	r.closed = true
	r.stopped = now.UTC()

	if err := r.writeSidecar(); err != nil {
		plog.Error(plog.TypeSystem, "finalizing netflow capture", "exp", r.exp, "id", r.id, "err", err)
	}

	r.mu.Unlock()

	r.forget()
	file.InvalidateExperimentFiles(r.exp)
}

// discard removes a capture that recorded nothing. It is safe to call more
// than once, and makes a later close do nothing.
func (r *netflowRecorder) discard() {
	r.mu.Lock()

	if r.closed {
		r.mu.Unlock()

		return
	}

	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}

	r.closed = true

	_ = os.Remove(r.path(".txt"))
	_ = os.Remove(r.path(".json"))

	r.mu.Unlock()

	r.forget()
	file.InvalidateExperimentFiles(r.exp)
}

// forget unregisters r as its experiment's live recorder, unless replaced.
func (r *netflowRecorder) forget() {
	liveRecordersMu.Lock()
	defer liveRecordersMu.Unlock()

	if liveRecorders[r.exp] == r {
		delete(liveRecorders, r.exp)
	}
}

// capture is the recorder's capture as it is now.
func (r *netflowRecorder) capture() NetflowCapture {
	r.mu.Lock()
	defer r.mu.Unlock()

	flows := r.flows
	c := NetflowCapture{
		ID: r.id, Started: r.started, Stopped: nil, State: NetflowStateRecording, Size: r.size,
		Flows: &flows, Truncated: r.truncated, Path: netflowDirName + "/" + r.id + ".txt",
	}

	if r.closed {
		stopped := r.stopped
		c.Stopped = &stopped
		c.State = NetflowStateStopped
	}

	return c
}

// liveRecorder is the experiment's recorder still recording, or nil.
func liveRecorder(exp string) *netflowRecorder {
	liveRecordersMu.Lock()
	defer liveRecordersMu.Unlock()

	return liveRecorders[exp]
}

// netflowCapturesDir returns the experiment's capture directory, or
// ErrExperimentNotFound.
func netflowCapturesDir(exp string) (string, error) {
	spec, err := Get(exp)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return "", fmt.Errorf("experiment %s: %w", exp, ErrExperimentNotFound)
		}

		return "", err
	}

	return netflowDir(spec.FilesDir()), nil
}

// NetflowCaptures lists the experiment's recorded netflow captures, newest
// first. It never returns a nil list without an error.
func NetflowCaptures(exp string) ([]NetflowCapture, error) {
	dir, err := netflowCapturesDir(exp)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []NetflowCapture{}, nil
		}

		return nil, fmt.Errorf("listing netflow captures: %w", err)
	}

	var (
		captures = make([]NetflowCapture, 0, len(entries))
		live     = liveRecorder(exp)
	)

	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".txt")
		if !ok || !netflowIDPattern.MatchString(id) {
			continue
		}

		// ReadDir's entries describe the link, not its target
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		captures = append(captures, describeNetflowCapture(dir, id, info.Size(), live))
	}

	slices.SortFunc(captures, func(a, b NetflowCapture) int { return strings.Compare(b.ID, a.ID) })

	return captures, nil
}

// describeNetflowCapture is the capture with the ID in dir, whose lines are
// size bytes.
func describeNetflowCapture(dir, id string, size int64, live *netflowRecorder) NetflowCapture {
	if live != nil && live.id == id {
		return live.capture()
	}

	started, _ := time.Parse(netflowIDLayout, id)
	c := NetflowCapture{
		ID: id, Started: started, Stopped: nil, State: NetflowStateInterrupted, Size: size,
		Flows: nil, Truncated: false, Path: netflowDirName + "/" + id + ".txt",
	}

	meta, ok := readNetflowSidecar(filepath.Join(dir, id+".json"))
	if !ok {
		return c
	}

	c.Truncated = meta.Truncated

	if meta.Stopped != nil {
		stopped := meta.Stopped.UTC()
		flows := meta.Flows
		c.Stopped, c.Flows, c.State = &stopped, &flows, NetflowStateStopped
	}

	return c
}

// readNetflowSidecar reads a capture's metadata, if it is a small regular
// file that parses.
func readNetflowSidecar(path string) (netflowSidecar, bool) {
	var meta netflowSidecar

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > netflowMaxSidecar {
		return meta, false
	}

	body, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(body, &meta) != nil {
		return meta, false
	}

	return meta, true
}

// OpenNetflowCapture opens the lines of the experiment's capture with the ID.
// The caller closes the file.
func OpenNetflowCapture(exp, id string) (*os.File, NetflowCapture, error) {
	if !netflowIDPattern.MatchString(id) {
		return nil, NetflowCapture{}, ErrInvalidNetflowCapture
	}

	dir, err := netflowCapturesDir(exp)
	if err != nil {
		return nil, NetflowCapture{}, err
	}

	var (
		path     = filepath.Join(dir, id+".txt")
		notFound = fmt.Errorf("netflow capture %s: %w", id, ErrNetflowCaptureNotFound)
	)

	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, NetflowCapture{}, notFound
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, NetflowCapture{}, notFound
	}

	// the path may have been replaced since the Lstat
	info, err := f.Stat()
	if err != nil || !os.SameFile(before, info) {
		_ = f.Close()

		return nil, NetflowCapture{}, notFound
	}

	return f, describeNetflowCapture(dir, id, info.Size(), liveRecorder(exp)), nil
}
