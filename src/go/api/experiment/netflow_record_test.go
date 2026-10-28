package experiment

import (
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/util/common"
	"phenix/util/polltest"
)

// netflowTestStart is the start of every recorder the tests make directly.
var netflowTestStart = time.Date(2026, 10, 28, 15, 30, 12, 345e6, time.UTC) //nolint:gochecknoglobals // constant

const netflowTestID = "20261028T153012.345Z"

// newTestRecorder records exp's capture, of at most maxSize bytes, in a new
// directory; the test closes it.
func newTestRecorder(t *testing.T, exp string, maxSize int64) (*netflowRecorder, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "netflow")

	r, err := newNetflowRecorder(exp, dir, maxSize, netflowTestStart)
	if err != nil {
		t.Fatalf("newNetflowRecorder(%s) error = %v", exp, err)
	}

	t.Cleanup(r.discard)

	return r, dir
}

// readSidecar reads the test recorder's capture metadata in dir.
func readSidecar(t *testing.T, dir string) netflowSidecar {
	t.Helper()

	meta, ok := readNetflowSidecar(filepath.Join(dir, netflowTestID+".json"))
	if !ok {
		t.Fatal("reading the netflow capture metadata failed")
	}

	return meta
}

// readCapture reads the capture's lines in dir.
func readCapture(t *testing.T, dir, id string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(dir, id+".txt"))
	if err != nil {
		t.Fatalf("reading netflow capture %s: %v", id, err)
	}

	return string(body)
}

// A recorder writes each line as received, keeps its metadata at start and
// at close, and ignores what comes after closing.
func TestNetflowRecorder(t *testing.T) {
	r, dir := newTestRecorder(t, "rec", 1<<20)

	started := netflowSidecar{
		Version: 1, Experiment: "rec", Started: netflowTestStart, Stopped: nil, Flows: 0, Truncated: false, MaxSize: 1 << 20,
	}
	if diff := cmp.Diff(started, readSidecar(t, dir)); diff != "" {
		t.Errorf("metadata at start mismatch (-want +got):\n%s", diff)
	}

	r.record([]string{" 2026 lead\tand trail ", "second\r"})
	r.record(nil)
	r.record([]string{"third"})

	stopped := netflowTestStart.Add(time.Minute)
	r.close(stopped)
	r.close(stopped.Add(time.Hour))
	r.record([]string{"after close"})

	if got, want := readCapture(t, dir, netflowTestID), " 2026 lead\tand trail \nsecond\r\nthird\n"; got != want {
		t.Errorf("recorded lines = %q, want %q", got, want)
	}

	final := started
	final.Stopped, final.Flows = &stopped, 3

	if diff := cmp.Diff(final, readSidecar(t, dir)); diff != "" {
		t.Errorf("metadata after close mismatch (-want +got):\n%s", diff)
	}

	for path, want := range map[string]fs.FileMode{
		dir: 0o750, filepath.Join(dir, netflowTestID+".txt"): 0o600, filepath.Join(dir, netflowTestID+".json"): 0o600,
	} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Errorf("mode of %s = %v, %v; want %v", path, info.Mode().Perm(), err, want)
		}
	}

	if live := liveRecorder("rec"); live != nil {
		t.Errorf("liveRecorder(rec) after close = %v, want nil", live)
	}
}

// The line that would pass the size limit, and all after it, are not
// recorded; the capture is marked truncated at once.
func TestNetflowRecorderCap(t *testing.T) {
	r, dir := newTestRecorder(t, "cap", 10)

	r.record([]string{"abcd", "efgh", "ij"})
	r.record([]string{"k"})

	if got, want := readCapture(t, dir, netflowTestID), "abcd\nefgh\n"; got != want {
		t.Errorf("recorded lines = %q, want %q", got, want)
	}

	meta := readSidecar(t, dir)
	if !meta.Truncated || meta.Flows != 2 || meta.Stopped != nil {
		t.Errorf("metadata at the size limit = %+v, want truncated, 2 flows and not stopped", meta)
	}

	c := r.capture()
	if c.Size > 10 || !c.Truncated || c.State != NetflowStateRecording {
		t.Errorf("capture() at the size limit = %+v, want at most 10 bytes, truncated and recording", c)
	}
}

// A failed write ends recording, truncated, keeping only whole lines.
func TestNetflowRecorderWriteError(t *testing.T) {
	r, dir := newTestRecorder(t, "werr", 1<<20)

	r.record([]string{"kept"})

	r.mu.Lock()
	_ = r.f.Close()
	r.mu.Unlock()

	r.record([]string{"lost"})
	r.record([]string{"also lost"})

	if got := readCapture(t, dir, netflowTestID); got != "kept\n" {
		t.Errorf("recorded lines after a failed write = %q, want %q", got, "kept\n")
	}

	if meta := readSidecar(t, dir); !meta.Truncated || meta.Flows != 1 {
		t.Errorf("metadata after a failed write = %+v, want truncated with 1 flow", meta)
	}
}

// Discarding removes the capture's files, and a later close does nothing.
func TestNetflowRecorderDiscard(t *testing.T) {
	r, dir := newTestRecorder(t, "discard", 1<<20)

	r.discard()
	r.close(time.Now())

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("capture directory after discard = %v, %v; want empty", entries, err)
	}

	if live := liveRecorder("discard"); live != nil {
		t.Errorf("liveRecorder(discard) after discard = %v, want nil", live)
	}
}

// A capture is never created over another.
func TestNetflowRecorderRefusesExistingCapture(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, netflowTestID+".txt")

	if err := os.WriteFile(existing, []byte("earlier\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if r, err := newNetflowRecorder("dup", dir, 1<<20, netflowTestStart); err == nil {
		r.discard()
		t.Fatal("newNetflowRecorder over an existing capture succeeded, want an error")
	}

	if body, _ := os.ReadFile(existing); string(body) != "earlier\n" {
		t.Errorf("existing capture after a refused create = %q, want it unchanged", body)
	}
}

// Recording and closing at once is safe.
func TestNetflowRecorderCloseRace(t *testing.T) {
	r, dir := newTestRecorder(t, "race", 1<<20)

	var wg sync.WaitGroup

	wg.Add(5)

	for range 4 {
		go func() {
			defer wg.Done()

			for range 50 {
				r.record([]string{"a b c"})
			}
		}()
	}

	go func() {
		defer wg.Done()

		r.close(time.Now())
	}()

	wg.Wait()

	meta := readSidecar(t, dir)
	if body := readCapture(t, dir, netflowTestID); int64(strings.Count(body, "\n")) != meta.Flows {
		t.Errorf("recorded %d lines, metadata says %d flows", strings.Count(body, "\n"), meta.Flows)
	}
}

// useNetflowBase points the experiment files of every experiment at a new
// directory, and returns experiment a's capture directory.
func useNetflowBase(t *testing.T) string {
	t.Helper()

	original := common.PhenixBase
	common.PhenixBase = t.TempDir() //nolint:reassign // point at a temporary tree

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	return filepath.Join(common.PhenixBase, "images", "a", "files", "netflow")
}

// writeCaptureFiles writes files, by name, to dir.
func writeCaptureFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The capture list shows each capture's state from its metadata and the live
// recorder, newest first, ignoring other files.
//
//nolint:paralleltest // replaces the store and the experiment files directory
func TestNetflowCaptures(t *testing.T) {
	useNetflows(t, "a")
	dir := useNetflowBase(t)

	stopped := `{"version":1,"experiment":"a","started":"2026-01-01T00:00:00.001Z",` +
		`"stopped":"2026-01-01T01:00:00Z","flows":7,"truncated":true,"maxSize":10}`

	writeCaptureFiles(t, dir, map[string]string{
		"20260101T000000.001Z.txt":   "1234567",
		"20260101T000000.001Z.json":  stopped,
		"20260102T000000.000Z.txt":   "ab",
		"20260102T000000.000Z.json":  `{"version":1,"started":"2026-01-02T00:00:00Z","flows":3,"truncated":true}`,
		"20260103T000000.000Z.txt":   "",
		"20260104T000000.000Z.txt":   "x",
		"20260104T000000.000Z.json":  `{"version":1,`,
		"20260105T000000.000Z.json":  stopped,
		"20260106T000000.000Z-2.txt": "",
		"notes.txt":                  "",
	})

	if err := os.Symlink(filepath.Join(dir, "notes.txt"), filepath.Join(dir, "20260107T000000.000Z.txt")); err != nil {
		t.Fatal(err)
	}

	r, err := newNetflowRecorder("a", dir, 1<<20, time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(r.discard)

	r.record([]string{"one", "two"})

	at := func(s string) *time.Time {
		tm, _ := time.Parse(time.RFC3339Nano, s)

		return &tm
	}
	flows := func(n int64) *int64 { return &n }
	capture := func(id, started string, stopped *time.Time, state string, size int64, n *int64, truncated bool) NetflowCapture {
		return NetflowCapture{
			ID: id, Started: *at(started), Stopped: stopped, State: state, Size: size, Flows: n,
			Truncated: truncated, Path: "netflow/" + id + ".txt",
		}
	}

	want := []NetflowCapture{
		capture("20260108T000000.000Z", "2026-01-08T00:00:00Z", nil, NetflowStateRecording, 8, flows(2), false),
		capture("20260104T000000.000Z", "2026-01-04T00:00:00Z", nil, NetflowStateInterrupted, 1, nil, false),
		capture("20260103T000000.000Z", "2026-01-03T00:00:00Z", nil, NetflowStateInterrupted, 0, nil, false),
		capture("20260102T000000.000Z", "2026-01-02T00:00:00Z", nil, NetflowStateInterrupted, 2, nil, true),
		capture("20260101T000000.001Z", "2026-01-01T00:00:00.001Z", at("2026-01-01T01:00:00Z"),
			NetflowStateStopped, 7, flows(7), true),
	}

	got, err := NetflowCaptures("a")
	if err != nil {
		t.Fatalf("NetflowCaptures(a) error = %v", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("NetflowCaptures(a) mismatch (-want +got):\n%s", diff)
	}

	r.close(time.Date(2026, 1, 8, 1, 0, 0, 0, time.UTC))

	got, _ = NetflowCaptures("a")
	if len(got) == 0 || got[0].State != NetflowStateStopped || *got[0].Flows != 2 {
		t.Errorf("NetflowCaptures(a)[0] after close = %+v, want stopped with 2 flows", got)
	}
}

// An experiment with no capture directory has no captures; an unknown one is
// an error.
//
//nolint:paralleltest // replaces the store and the experiment files directory
func TestNetflowCapturesEmptyAndUnknown(t *testing.T) {
	useNetflows(t, "a")
	useNetflowBase(t)

	got, err := NetflowCaptures("a")
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("NetflowCaptures(a) with no directory = %#v, %v; want an empty list", got, err)
	}

	if _, err := NetflowCaptures("missing"); !errors.Is(err, ErrExperimentNotFound) {
		t.Errorf("NetflowCaptures(missing) error = %v, want %v", err, ErrExperimentNotFound)
	}
}

// A capture opens by a valid ID only, and never through a link.
//
//nolint:paralleltest // replaces the store and the experiment files directory
func TestOpenNetflowCapture(t *testing.T) {
	useNetflows(t, "a")
	dir := useNetflowBase(t)

	writeCaptureFiles(t, dir, map[string]string{netflowTestID + ".txt": "line\n", "other.txt": "secret\n"})

	if err := os.Symlink(filepath.Join(dir, "other.txt"), filepath.Join(dir, "20260101T000000.000Z.txt")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		exp, id string
		want    error
	}{
		{exp: "a", id: netflowTestID + "-2", want: ErrInvalidNetflowCapture},
		{exp: "a", id: "../other", want: ErrInvalidNetflowCapture},
		{exp: "a", id: "", want: ErrInvalidNetflowCapture},
		{exp: "a", id: "20991231T000000.000Z", want: ErrNetflowCaptureNotFound},
		{exp: "a", id: "20260101T000000.000Z", want: ErrNetflowCaptureNotFound},
		{exp: "missing", id: netflowTestID, want: ErrExperimentNotFound},
	} {
		f, _, err := OpenNetflowCapture(tc.exp, tc.id)
		if f != nil {
			_ = f.Close()
		}

		if !errors.Is(err, tc.want) {
			t.Errorf("OpenNetflowCapture(%s, %q) error = %v, want %v", tc.exp, tc.id, err, tc.want)
		}
	}

	f, c, err := OpenNetflowCapture("a", netflowTestID)
	if err != nil {
		t.Fatalf("OpenNetflowCapture(a, %s) error = %v", netflowTestID, err)
	}

	defer f.Close()

	body, _ := io.ReadAll(f)
	if string(body) != "line\n" || c.Size != 5 || c.ID != netflowTestID {
		t.Errorf("OpenNetflowCapture(a, %s) = %q, %+v; want its line and size 5", netflowTestID, body, c)
	}
}

// startTestNetflow starts exp's capture, recording up to maxSize bytes, with
// the mesh answering at once.
func startTestNetflow(t *testing.T, fake *meshMM, exp string, maxSize int64) *Netflow {
	t.Helper()

	fake.release()

	if err := StartNetflow(exp, maxSize); err != nil {
		t.Fatalf("StartNetflow(%s) error = %v", exp, err)
	}

	flow := GetNetflow(exp)
	if flow == nil {
		t.Fatalf("GetNetflow(%s) after StartNetflow = nil", exp)
	}

	return flow
}

// sendNetflow sends each datagram to the capture's listener.
func sendNetflow(t *testing.T, flow *Netflow, datagrams ...string) {
	t.Helper()

	port := flow.Conn.LocalAddr().(*net.UDPAddr).Port //nolint:forcetypeassert // a UDP listener

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatalf("dialing the netflow listener: %v", err)
	}

	defer conn.Close()

	for _, d := range datagrams {
		if _, err := conn.Write([]byte(d)); err != nil {
			t.Fatalf("sending a netflow datagram: %v", err)
		}
	}
}

// onlyCapture returns exp's one capture once it has n flows.
func onlyCapture(t *testing.T, exp string, n int64) NetflowCapture {
	t.Helper()

	var c NetflowCapture

	polltest.Until(t, "the capture recording its flows", func() bool {
		got, err := NetflowCaptures(exp)
		if err != nil || len(got) != 1 || got[0].Flows == nil || *got[0].Flows < n {
			return false
		}

		c = got[0]

		return true
	})

	return c
}

// A started capture records every non-empty line minimega sends, untrimmed,
// until it is stopped or its experiment stops.
//
//nolint:paralleltest // replaces the store, minimega and the netflow registry
func TestNetflowRecordsUntilStopped(t *testing.T) {
	for name, stop := range map[string]func(t *testing.T){
		"StopNetflow": func(t *testing.T) {
			t.Helper()

			if err := StopNetflow("a"); err != nil {
				t.Fatalf("StopNetflow(a) error = %v", err)
			}
		},
		"stop hook": func(*testing.T) { stopHook("a") },
	} {
		t.Run(name, func(t *testing.T) {
			fake := useNetflows(t, "a")
			dir := useNetflowBase(t)
			flow := startTestNetflow(t, fake, "a", 1<<20)

			sendNetflow(t, flow, "one\t1\n\n  \ntwo 2\n", " three\n")

			c := onlyCapture(t, "a", 3)
			if c.State != NetflowStateRecording {
				t.Errorf("capture state while running = %s, want %s", c.State, NetflowStateRecording)
			}

			stop(t)

			if got, want := readCapture(t, dir, c.ID), "one\t1\ntwo 2\n three\n"; got != want {
				t.Errorf("recorded lines = %q, want %q", got, want)
			}

			got, _ := NetflowCaptures("a")
			if len(got) != 1 || got[0].State != NetflowStateStopped || got[0].Stopped == nil {
				t.Errorf("NetflowCaptures(a) after stopping = %+v, want one stopped capture", got)
			}
		})
	}
}

// A capture that does not start, or is not recorded, leaves no files and no
// live recorder.
//
//nolint:paralleltest // replaces the store, minimega and the netflow registry
func TestNetflowLeavesNoFiles(t *testing.T) {
	t.Run("mesh failure", func(t *testing.T) {
		fake := useNetflows(t, "a")
		dir := useNetflowBase(t)
		fake.fail = errors.New("mesh down")
		fake.release()

		if err := StartNetflow("a", 1<<20); err == nil {
			t.Fatal("StartNetflow(a) with the mesh failing succeeded, want an error")
		}

		checkNoCaptures(t, dir, "a")
	})

	t.Run("stopped while starting", func(t *testing.T) {
		fake := useNetflows(t, "a")
		dir := useNetflowBase(t)
		started := make(chan error, 1)

		go func() { started <- StartNetflow("a", 1<<20) }()

		polltest.Within(t, "the start asking the mesh", func() { <-fake.entered })
		stopHook("a")
		fake.release()

		var err error

		polltest.Within(t, "the start finishing", func() { err = <-started })

		if !errors.Is(err, ErrExperimentNotRunning) {
			t.Errorf("StartNetflow(a) stopped while starting error = %v, want %v", err, ErrExperimentNotRunning)
		}

		checkNoCaptures(t, dir, "a")
	})

	t.Run("not recorded", func(t *testing.T) {
		fake := useNetflows(t, "a")
		dir := useNetflowBase(t)
		flow := startTestNetflow(t, fake, "a", 0)

		sendNetflow(t, flow, "one\n")
		stopHook("a")

		checkNoCaptures(t, dir, "a")
	})
}

// checkNoCaptures fails unless dir holds no capture files and exp has no live
// recorder.
func checkNoCaptures(t *testing.T, dir, exp string) {
	t.Helper()

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("capture directory = %v, want no files", entries)
	}

	if live := liveRecorder(exp); live != nil {
		t.Errorf("liveRecorder(%s) = %v, want nil", exp, live)
	}
}

// A capture that cannot be recorded still starts and streams.
//
//nolint:paralleltest // replaces the store, minimega and the netflow registry
func TestNetflowStreamsWhenRecordingFails(t *testing.T) {
	fake := useNetflows(t, "a")
	dir := useNetflowBase(t)

	// a file where the capture directory belongs
	writeCaptureFiles(t, filepath.Dir(dir), map[string]string{"netflow": "not a directory"})

	flow := startTestNetflow(t, fake, "a", 1<<20)
	if flow.recorder != nil {
		t.Errorf("recorder of a capture that cannot be recorded = %v, want nil", flow.recorder)
	}

	updates := flow.NewChannel("viewer")

	sendNetflow(t, flow, "2026-01-01T00:00:00Z 1s 6 10.0.0.1:1 <-> 10.0.0.2:2 1 2\n")

	polltest.Within(t, "the flow streaming", func() {
		if body := <-updates; body["proto"] != 6 {
			t.Errorf("streamed flow = %v, want proto 6", body)
		}
	})

	if body, _ := os.ReadFile(dir); string(body) != "not a directory" {
		t.Errorf("file in the capture directory's place = %q, want it unchanged", body)
	}
}
