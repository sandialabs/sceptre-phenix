package experiment

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/store/storetest"
	ifaces "phenix/types/interfaces"
	dct "phenix/util/diskchain/diskchaintest"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/shell"
)

// markerMM is a startMM that also launches the VMs and answers their hosts and VLANs. It calls onRead as minimega
// reads the experiment's script.
type markerMM struct {
	startMM

	onRead func()
}

func (m *markerMM) ReadScriptFromFile(string, string) error {
	if m.onRead != nil {
		m.onRead()
	}

	return m.readErr
}

func (*markerMM) LaunchVMs(string, ...string) error { return nil }

func (*markerMM) GetVMHosts(...mm.Option) map[string]string { return map[string]string{} }

func (*markerMM) GetVLANs(...mm.Option) (map[string]int, error) { return map[string]int{}, nil }

// markerFiles deletes no cluster file. Any other file.ClusterFiles method panics.
type markerFiles struct {
	file.ClusterFiles
}

func (markerFiles) DeleteFile(string) error { return nil }

// markerShell is the default shell with only the processes in alive running.
type markerShell struct {
	shell.Shell

	alive map[int]bool
}

func (s markerShell) ProcessExists(pid int) bool { return s.alive[pid] }

// useMarkerShell has only this process and the processes alive run until the test ends.
func useMarkerShell(t *testing.T, alive ...int) {
	t.Helper()

	running := map[int]bool{os.Getpid(): true}
	for _, pid := range alive {
		running[pid] = true
	}

	original := shell.DefaultShell
	t.Cleanup(func() { shell.DefaultShell = original }) //nolint:reassign // restore default

	shell.DefaultShell = markerShell{Shell: original, alive: running} //nolint:reassign // install test double
}

// useMarkerStart stores experiment exp, stopped, with a VM whose disk is good, and the given status. It fakes the
// minimega and the cluster files that a start uses, and returns the minimega.
func useMarkerStart(t *testing.T, status map[string]any, fake *markerMM) *markerMM {
	t.Helper()

	image := t.TempDir() + "/a.qc2"
	dct.WriteQcow2(t, image, dct.Qcow2{})

	storeDiskExperiment(t, image)

	c, _ := store.NewConfig("experiment/exp")
	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(experiment exp) error = %v", err)
	}

	c.Status = status
	if err := store.Update(c); err != nil {
		t.Fatalf("store.Update(experiment exp) error = %v", err)
	}

	original := file.DefaultClusterFiles
	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore default

	file.DefaultClusterFiles = markerFiles{} //nolint:reassign // install test double

	useStartMM(t, &fake.startMM)
	mm.DefaultMM = fake //nolint:reassign // useStartMM restores the default

	return fake
}

// markerStatus is a stored status that holds the start marker m.
func markerStatus(m ifaces.StartMarker) map[string]any {
	return map[string]any{"starting": map[string]any{"since": m.Since, "pid": m.PID, "host": m.Host}}
}

// thisHost is the host name of this host, as the start marker records it.
func thisHost(t *testing.T) string {
	t.Helper()

	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname() error = %v", err)
	}

	return host
}

// wantOwnMarker fails the test unless m is a start marker of this process, from at most a minute ago.
func wantOwnMarker(t *testing.T, m ifaces.StartMarker) {
	t.Helper()

	since, err := time.Parse(time.RFC3339, m.Since)
	if err != nil || time.Since(since) > time.Minute || !strings.HasSuffix(m.Since, "Z") {
		t.Errorf("start marker since = %q, want a recent RFC 3339 UTC time", m.Since)
	}

	if m.PID != os.Getpid() || m.Host != thisHost(t) {
		t.Errorf("start marker process = %d on %q, want %d on %q", m.PID, m.Host, os.Getpid(), thisHost(t))
	}
}

// A marker of this host is live while its process runs. A marker of another host is live until it is
// StartMarkerMaxAge old. The zero marker, a marker with no process and a marker with a bad time are not live.
func TestStartMarkerLiveOn(t *testing.T) {
	t.Parallel()

	var (
		now    = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
		recent = now.Add(-time.Hour).Format(time.RFC3339)
		old    = now.Add(-StartMarkerMaxAge).Format(time.RFC3339)
		exists = func(pid int) bool { return pid == 42 }
	)

	tests := map[string]struct {
		marker ifaces.StartMarker
		want   bool
	}{
		"zero":                          {marker: ifaces.StartMarker{}, want: false},
		"this host, process runs":       {marker: ifaces.StartMarker{Since: old, PID: 42, Host: "h1"}, want: true},
		"this host, process gone":       {marker: ifaces.StartMarker{Since: recent, PID: 43, Host: "h1"}, want: false},
		"this host, no process":         {marker: ifaces.StartMarker{Since: recent, PID: 0, Host: "h1"}, want: false},
		"other host, recent":            {marker: ifaces.StartMarker{Since: recent, PID: 43, Host: "h2"}, want: true},
		"other host, old":               {marker: ifaces.StartMarker{Since: old, PID: 42, Host: "h2"}, want: false},
		"other host, time not RFC 3339": {marker: ifaces.StartMarker{Since: "yesterday", PID: 42, Host: "h2"}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := StartMarkerLiveOn(tc.marker, "h1", now, exists); got != tc.want {
				t.Errorf("StartMarkerLiveOn(%+v) = %t, want %t", tc.marker, got, tc.want)
			}
		})
	}
}

// A marker of another host is live exactly while it is younger than StartMarkerMaxAge, whatever its process.
func TestStartMarkerLiveOnOtherHostProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var (
			now = time.Unix(rapid.Int64Range(1<<30, 1<<33).Draw(t, "now"), 0).UTC()
			age = time.Duration(rapid.Int64Range(0, int64(3*StartMarkerMaxAge/time.Second)).Draw(t, "age")) * time.Second
			pid = rapid.IntRange(1, 1<<22).Draw(t, "pid")
			m   = ifaces.StartMarker{Since: now.Add(-age).Format(time.RFC3339), PID: pid, Host: "other"}
		)

		if got := StartMarkerLiveOn(m, "this", now, func(int) bool { return true }); got != (age < StartMarkerMaxAge) {
			t.Fatalf("StartMarkerLiveOn(%+v) at age %v = %t, want %t", m, age, got, age < StartMarkerMaxAge)
		}
	})
}

// Start stores a start marker of this process before minimega reads the script. The write that records the start
// clears it, and so does a start that fails.
func TestStartRecordsTheMarkerUntilItEnds(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := map[string]struct {
		readErr error
		started bool
	}{
		"a start that works": {readErr: nil, started: true},
		"a start that fails": {readErr: errors.New("minimega is gone"), started: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var seen ifaces.StartMarker

			fake := useMarkerStart(t, map[string]any{}, &markerMM{startMM: startMM{readErr: tc.readErr}})
			fake.onRead = func() { seen = experimentNamed(t, "exp").Status.Starting() }

			err := Start(notes.Context(t.Context(), false), StartWithName("exp"))
			if (err == nil) != tc.started {
				t.Fatalf("Start error = %v, want an error: %t", err, !tc.started)
			}

			wantOwnMarker(t, seen)

			exp := experimentNamed(t, "exp")
			if got := exp.Status.Starting(); !got.IsZero() {
				t.Errorf("stored start marker after Start = %+v, want none", got)
			}

			if exp.Running() != tc.started {
				t.Errorf("experiment running after Start = %t, want %t", exp.Running(), tc.started)
			}
		})
	}
}

// A dry run stores the start time with no start marker.
func TestStartDryRunClearsTheMarker(t *testing.T) { //nolint:paralleltest // replaces package globals
	useMarkerStart(t, map[string]any{}, &markerMM{})

	if err := Start(notes.Context(t.Context(), false), StartWithName("exp"), StartWithDryRun(true)); err != nil {
		t.Fatalf("Start(dry run) error = %v", err)
	}

	exp := experimentNamed(t, "exp")
	if got := exp.Status.Starting(); !got.IsZero() {
		t.Errorf("stored start marker after Start(dry run) = %+v, want none", got)
	}

	if !exp.DryRun() {
		t.Errorf("stored start time after Start(dry run) = %q, want a dry run", exp.Status.StartTime())
	}
}

// Start refuses an experiment that another live process starts. It tells minimega nothing and keeps the marker.
func TestStartRefusesALiveMarker(t *testing.T) { //nolint:paralleltest // replaces package globals
	since := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)

	tests := map[string]struct {
		marker ifaces.StartMarker
		want   string // with {host} for this host's name
	}{
		"a process of this host": {
			marker: ifaces.StartMarker{Since: since, PID: 4242, Host: "{host}"},
			want:   "experiment exp is already starting (process 4242 since " + since + ")",
		},
		"another host": {
			marker: ifaces.StartMarker{Since: since, PID: 7, Host: "elsewhere"},
			want:   "experiment exp is already starting (process 7 on host elsewhere since " + since + ")",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useMarkerShell(t, 4242)

			marker := tc.marker
			marker.Host = strings.ReplaceAll(marker.Host, "{host}", thisHost(t))

			read := false
			fake := useMarkerStart(t, markerStatus(marker), &markerMM{})
			fake.onRead = func() { read = true }

			err := Start(notes.Context(t.Context(), false), StartWithName("exp"))

			var starting *StartingError
			if !errors.As(err, &starting) || !errors.Is(err, ErrExperimentStarting) || err.Error() != tc.want {
				t.Fatalf("Start error = %v, want a StartingError %q", err, tc.want)
			}

			if read || len(fake.cleared) != 0 {
				t.Errorf("Start read the script: %t, cleared namespaces %q; want neither", read, fake.cleared)
			}

			if diff := cmp.Diff(marker, experimentNamed(t, "exp").Status.Starting()); diff != "" {
				t.Errorf("stored start marker after a refused Start mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Start replaces a stale start marker with its own, and clears it when it ends.
func TestStartReplacesAStaleMarker(t *testing.T) { //nolint:paralleltest // replaces package globals
	old := time.Now().UTC().Add(-StartMarkerMaxAge - time.Minute).Format(time.RFC3339)

	tests := map[string]ifaces.StartMarker{
		"a process of this host that does not run": {Since: time.Now().UTC().Format(time.RFC3339), PID: 4243, Host: "{host}"},
		"another host, too old":                    {Since: old, PID: 7, Host: "elsewhere"},
	}

	for name, marker := range tests {
		t.Run(name, func(t *testing.T) {
			useMarkerShell(t)

			marker.Host = strings.ReplaceAll(marker.Host, "{host}", thisHost(t))

			var seen ifaces.StartMarker

			fake := useMarkerStart(t, markerStatus(marker), &markerMM{startMM: startMM{readErr: errors.New("stop here")}})
			fake.onRead = func() { seen = experimentNamed(t, "exp").Status.Starting() }

			if err := Start(notes.Context(t.Context(), false), StartWithName("exp")); err == nil {
				t.Fatal("Start error = nil, want the script error")
			}

			wantOwnMarker(t, seen)

			if got := experimentNamed(t, "exp").Status.Starting(); !got.IsZero() {
				t.Errorf("stored start marker after Start = %+v, want none", got)
			}
		})
	}
}

// claimStart reads the experiment again when another write changes it before the marker is stored, and gives up after
// maxClaimAttempts.
func TestClaimStartRetriesAConflict(t *testing.T) { //nolint:paralleltest // replaces the package store
	tests := map[string]struct {
		conflicts int
		wantErr   bool
	}{
		"one conflict":    {conflicts: 1, wantErr: false},
		"every attempt":   {conflicts: maxClaimAttempts, wantErr: true},
		"no conflict":     {conflicts: 0, wantErr: false},
		"two conflicts":   {conflicts: maxClaimAttempts - 1, wantErr: false},
		"more than tried": {conflicts: maxClaimAttempts + 1, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			db := &conflictStore{Store: storetest.NewBolt(t), conflicts: tc.conflicts}
			storetest.Use(t, db)
			storeExperiment(t, "exp", map[string]any{})

			_, _, marker, err := claimStart("exp")
			if (err != nil) != tc.wantErr || (tc.wantErr && !errors.Is(err, store.ErrConflict)) {
				t.Fatalf("claimStart error = %v, want an ErrConflict: %t", err, tc.wantErr)
			}

			want := min(tc.conflicts+1, maxClaimAttempts)
			if db.updates != want {
				t.Errorf("claimStart wrote %d times, want %d", db.updates, want)
			}

			if !tc.wantErr {
				wantOwnMarker(t, marker)
			}
		})
	}
}

// conflictStore fails the first conflicts writes with store.ErrConflict, and counts the writes.
type conflictStore struct {
	store.Store

	conflicts, updates int
}

func (s *conflictStore) Update(c *store.Config) error {
	s.updates++

	if s.updates <= s.conflicts {
		return store.ErrConflict
	}

	return s.Store.Update(c) //nolint:wrapcheck // test double
}

// ClearStaleStartMarkers removes the markers of processes of this host that do not run, of this process and of other
// hosts that are too old. It keeps the live markers and the rest of each status.
func TestClearStaleStartMarkers(t *testing.T) { //nolint:paralleltest // replaces package globals
	storetest.Use(t)
	useMarkerShell(t, 4242)

	var (
		host   = thisHost(t)
		now    = time.Now().UTC().Format(time.RFC3339)
		old    = time.Now().UTC().Add(-StartMarkerMaxAge - time.Minute).Format(time.RFC3339)
		live   = ifaces.StartMarker{Since: now, PID: 4242, Host: host}
		remote = ifaces.StartMarker{Since: now, PID: 7, Host: "elsewhere"}
		stale  = map[string]ifaces.StartMarker{
			"dead": {Since: now, PID: 4243, Host: host},
			"self": {Since: now, PID: os.Getpid(), Host: host},
			"old":  {Since: old, PID: 7, Host: "elsewhere"},
		}
	)

	for name, m := range stale {
		status := markerStatus(m)
		status["interrupted"] = "kept"
		storeExperiment(t, name, status)
	}

	storeExperiment(t, "live", markerStatus(live))
	storeExperiment(t, "remote", markerStatus(remote))
	storeExperiment(t, "none", map[string]any{})

	if err := ClearStaleStartMarkers(); err != nil {
		t.Fatalf("ClearStaleStartMarkers() error = %v", err)
	}

	got := make(map[string]ifaces.StartMarker)

	for _, name := range []string{"dead", "self", "old", "live", "remote", "none"} {
		exp := experimentNamed(t, name)
		got[name] = exp.Status.Starting()

		if _, ok := stale[name]; ok && exp.Status.Interrupted() != "kept" {
			t.Errorf("experiment %s interrupted after ClearStaleStartMarkers() = %q, want kept", name, exp.Status.Interrupted())
		}
	}

	want := map[string]ifaces.StartMarker{"dead": {}, "self": {}, "old": {}, "live": live, "remote": remote, "none": {}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("start markers after ClearStaleStartMarkers() mismatch (-want +got):\n%s", diff)
	}
}
