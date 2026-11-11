package mm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
)

// useMinimegaVersion sets what this process knows of minimega's version until the test ends: answer as its first
// answer (nil for none yet), whether ui.features forces the capture support on, and whether a startup check runs.
// It also makes a version query fail, so a test that asks minimega must replace queryVersion itself.
func useMinimegaVersion(t *testing.T, answer *string, forced, watching bool) {
	t.Helper()

	minimegaVersion.mu.Lock()
	saved := struct {
		forced, answered, watching bool
		answer                     string
	}{minimegaVersion.forced, minimegaVersion.answered, minimegaVersion.watching, minimegaVersion.answer}

	minimegaVersion.forced, minimegaVersion.watching = forced, watching
	minimegaVersion.answered, minimegaVersion.answer = answer != nil, ""

	if answer != nil {
		minimegaVersion.answer = *answer
	}
	minimegaVersion.mu.Unlock()

	query, retry := queryVersion, versionRetry
	queryVersion = func(context.Context) (string, error) { return "", errors.New("no version query in this test") }

	t.Cleanup(func() {
		queryVersion, versionRetry = query, retry

		minimegaVersion.mu.Lock()
		minimegaVersion.forced, minimegaVersion.answered = saved.forced, saved.answered
		minimegaVersion.watching, minimegaVersion.answer = saved.watching, saved.answer
		minimegaVersion.mu.Unlock()
	})
}

// answered is a pointer to a version answer, for useMinimegaVersion.
func answered(answer string) *string { return &answer }

// versionQueries is a fake version query that fails fails times, then answers answer, counting its calls.
type versionQueries struct {
	mu     sync.Mutex
	fails  int
	answer string
	calls  int
}

func (q *versionQueries) query(context.Context) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.calls++
	if q.calls <= q.fails {
		return "", errors.New("minimega is not there")
	}

	return q.answer, nil
}

func (q *versionQueries) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.calls
}

// versionCases are version answers and the capture support each gives. Its answers seed FuzzParseMinimegaVersion.
//
//nolint:gochecknoglobals // read-only test table
var versionCases = map[string]struct {
	answer string
	forced bool
	want   CaptureSupport
}{
	"older release": {
		answer: "minimega 3.3.0 1a2b3c4d 2026-01-02",
		want: CaptureSupport{
			Available: false, Reason: "requires minimega 3.4.0 or later; the head node runs 3.3.0", Version: "3.3.0",
		},
	},
	"first release": {
		answer: "minimega 3.4.0 1a2b3c4d 2026-10-01",
		want:   CaptureSupport{Available: true, Reason: "", Version: "3.4.0"},
	},
	"release candidate": {
		answer: "minimega 3.4.0-rc1 1a2b3c4d 2026-09-01",
		want:   CaptureSupport{Available: true, Reason: "", Version: "3.4.0-rc1"},
	},
	"later minor release, compared as a number": {
		answer: "minimega 3.10.0 1a2b3c4d 2027-01-01",
		want:   CaptureSupport{Available: true, Reason: "", Version: "3.10.0"},
	},
	"version with a v": {
		answer: "minimega v3.4.1",
		want:   CaptureSupport{Available: true, Reason: "", Version: "3.4.1"},
	},
	"short older version": {
		answer: "minimega 2.9",
		want: CaptureSupport{
			Available: false, Reason: "requires minimega 3.4.0 or later; the head node runs 2.9", Version: "2.9",
		},
	},
	"development build": {
		answer: "minimega devel (go1.24)\n",
		want: CaptureSupport{
			Available: false,
			Reason:    `requires minimega 3.4.0 or later; cannot read the minimega version "minimega devel (go1.24)"`,
			Version:   "",
		},
	},
	"empty answer": {
		answer: "",
		want: CaptureSupport{
			Available: false, Reason: `requires minimega 3.4.0 or later; cannot read the minimega version ""`, Version: "",
		},
	},
	"forced on an older release": {
		answer: "minimega 3.3.0 1a2b3c4d 2026-01-02",
		forced: true,
		want:   CaptureSupport{Available: true, Reason: "", Version: "3.3.0"},
	},
	"forced on a development build": {
		answer: "minimega devel",
		forced: true,
		want:   CaptureSupport{Available: true, Reason: "", Version: ""},
	},
}

func TestCaptureSupportOf(t *testing.T) {
	t.Parallel()

	for name, tc := range versionCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, CaptureSupportOf(tc.answer, tc.forced)); diff != "" {
				t.Errorf("CaptureSupportOf(%q, %t) mismatch (-want +got):\n%s", tc.answer, tc.forced, diff)
			}
		})
	}
}

// While CaptureVersionRequired is false, a process starts with the support forced on: it is on before minimega
// answers, and a capture call does not ask minimega for its version.
func TestCaptureSupportForcedAtStart(t *testing.T) { //nolint:paralleltest // reads package state
	minimegaVersion.mu.Lock()
	forced := minimegaVersion.forced
	minimegaVersion.mu.Unlock()

	if want := !CaptureVersionRequired; forced != want {
		t.Errorf("capture support forced at start = %t, want %t", forced, want)
	}

	useMinimegaVersion(t, nil, forced, false)

	q := &versionQueries{answer: unsupportedAnswer}
	queryVersion = q.query

	if s := QueryCaptureSupport(t.Context()); s.Available != !CaptureVersionRequired {
		t.Errorf("QueryCaptureSupport() at start = %+v, want available %t", s, !CaptureVersionRequired)
	}

	if got, want := q.count(), 0; !CaptureVersionRequired && got != want {
		t.Errorf("version queries = %d, want %d", got, want)
	}
}

// Until minimega answers, the support is off and says so, unless ui.features forces it on.
func TestCaptureSupportStatusWithoutAnswer(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, false, true)

	want := CaptureSupport{Available: false, Reason: "requires minimega 3.4.0 or later; minimega did not answer", Version: ""}
	if diff := cmp.Diff(want, CaptureSupportStatus()); diff != "" {
		t.Errorf("CaptureSupportStatus() with no answer mismatch (-want +got):\n%s", diff)
	}

	if CaptureFeatures() {
		t.Error("CaptureFeatures() with no answer = true, want false")
	}

	SetCaptureFeaturesForced(true)

	if !CaptureFeatures() {
		t.Error("CaptureFeatures() forced with no answer = false, want true")
	}
}

// The startup check asks again every interval until minimega answers, then stops, and the answer stays.
func TestStartVersionCheckRetries(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, false, false)

	q := &versionQueries{fails: 2, answer: "minimega 3.4.0 abc 2026-10-01"}

	done := startVersionCheck(t.Context(), q.query, time.Millisecond)
	if done == nil {
		t.Fatal("startVersionCheck() = nil, want a check")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the version check did not end after minimega answered")
	}

	if got := q.count(); got != 3 {
		t.Errorf("version queries = %d, want 3 (two failures, then the answer)", got)
	}

	if !CaptureFeatures() {
		t.Errorf("CaptureFeatures() after minimega answered 3.4.0 = false, want true (%+v)", CaptureSupportStatus())
	}

	if again := startVersionCheck(t.Context(), q.query, time.Millisecond); again != nil {
		t.Error("a second startVersionCheck() started another check, want nil")
	}

	// a capture call asks no more once the startup check ran
	_ = captureSupport(t.Context())

	if got := q.count(); got != 3 {
		t.Errorf("version queries after a capture call = %d, want 3", got)
	}
}

// The startup check asks again after an empty answer, as after an error, and keeps the next answer.
func TestStartVersionCheckRetriesEmptyAnswer(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, false, false)

	var (
		mu      sync.Mutex
		calls   int
		answers = []string{"", " \n", "minimega 3.4.0 abc 2026-01-01"}
	)

	query := func(context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		calls++

		return answers[min(calls, len(answers))-1], nil
	}

	done := startVersionCheck(t.Context(), query, time.Millisecond)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the version check did not end after minimega answered")
	}

	mu.Lock()
	got := calls
	mu.Unlock()

	if got != len(answers) {
		t.Errorf("version queries = %d, want %d (two empty answers, then the version)", got, len(answers))
	}

	want := CaptureSupport{Available: true, Reason: "", Version: "3.4.0"}
	if diff := cmp.Diff(want, CaptureSupportStatus()); diff != "" {
		t.Errorf("CaptureSupportStatus() after an empty answer, then 3.4.0 mismatch (-want +got):\n%s", diff)
	}
}

// After ForgetCluster, a capture call asks minimega again, also when a startup check ran.
func TestForgetClusterAsksAgain(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, answered("minimega 3.3.0 abc 2026-01-01"), false, true)

	q := &versionQueries{answer: "minimega 3.4.0 abc 2026-10-01"}
	queryVersion = q.query

	ForgetCluster()

	if s := captureSupport(t.Context()); !s.Available {
		t.Errorf("captureSupport() after ForgetCluster and an answer of 3.4.0 = %+v, want it on", s)
	}

	if got := q.count(); got != 1 {
		t.Errorf("version queries after ForgetCluster = %d, want 1", got)
	}
}

// The startup check stops once its context is done, though minimega never answered.
func TestStartVersionCheckStopsWithContext(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, false, false)

	q := &versionQueries{fails: 1 << 30}
	ctx, cancel := context.WithCancel(t.Context())

	done := startVersionCheck(ctx, q.query, time.Millisecond)

	polltest.Until(t, "the version check asks twice", func() bool { return q.count() >= 2 })

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the version check did not end with its context")
	}

	if CaptureFeatures() {
		t.Error("CaptureFeatures() with no answer = true, want false")
	}
}

// A process with no startup check asks minimega at its capture calls, and QueryCaptureSupport, until minimega
// answers once.
func TestCaptureSupportAsksUntilAnswered(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, false, false)

	q := &versionQueries{fails: 1, answer: "minimega 3.4.0 abc 2026-10-01"}
	queryVersion = q.query

	if s := QueryCaptureSupport(t.Context()); s.Available {
		t.Errorf("QueryCaptureSupport() while minimega does not answer = %+v, want it off", s)
	}

	for range 3 {
		if s := QueryCaptureSupport(t.Context()); !s.Available {
			t.Errorf("QueryCaptureSupport() after minimega answered 3.4.0 = %+v, want it on", s)
		}
	}

	if got := q.count(); got != 2 {
		t.Errorf("version queries = %d, want 2 (one failure, then the answer)", got)
	}
}

// With ui.features forcing the support on, a capture call does not ask minimega.
func TestCaptureSupportForcedAsksNothing(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, true, false)

	q := &versionQueries{answer: "minimega 3.3.0"}
	queryVersion = q.query

	if s := captureSupport(t.Context()); !s.Available {
		t.Errorf("captureSupport() forced = %+v, want it on", s)
	}

	if got := q.count(); got != 0 {
		t.Errorf("version queries = %d, want 0", got)
	}
}

// MinimegaVersion gives the whole first answer, asking minimega until it answers once.
func TestMinimegaVersion(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, false, false)

	q := &versionQueries{fails: 1, answer: "minimega 3.4.0 abc 2026-10-01"}
	queryVersion = q.query

	if v, err := MinimegaVersion(); err == nil {
		t.Errorf("MinimegaVersion() while minimega does not answer = %q, nil; want an error", v)
	}

	for range 2 {
		if v, err := MinimegaVersion(); err != nil || v != q.answer {
			t.Errorf("MinimegaVersion() = %q, %v; want %q, nil", v, err, q.answer)
		}
	}

	if got := q.count(); got != 2 {
		t.Errorf("version queries = %d, want 2", got)
	}

	ForgetCluster()

	if CaptureFeatures() {
		t.Error("CaptureFeatures() after ForgetCluster = true, want false until minimega answers again")
	}
}

// askMinimegaVersion sends the version command to the head node and gives its answer, or an error that says what
// failed.
func TestAskMinimegaVersion(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	t.Run("answer", func(t *testing.T) {
		received := mmtest.Use(t, func(mmtest.Command) []*minicli.Response {
			return []*minicli.Response{mmtest.Text("head", supportedAnswer)}
		})

		if got, err := askMinimegaVersion(t.Context()); err != nil || got != supportedAnswer {
			t.Errorf("askMinimegaVersion() = %q, %v; want %q, nil", got, err, supportedAnswer)
		}

		if diff := cmp.Diff([]string{"version"}, mmtest.Bases(received())); diff != "" {
			t.Errorf("askMinimegaVersion() commands mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("error", func(t *testing.T) {
		mmtest.Use(t, func(mmtest.Command) []*minicli.Response {
			return []*minicli.Response{mmtest.Error("head", "boom")}
		})

		got, err := askMinimegaVersion(t.Context())
		if err == nil || !strings.HasPrefix(err.Error(), "asking minimega for its version: ") ||
			!strings.Contains(err.Error(), "boom") {
			t.Errorf("askMinimegaVersion() = %q, %v; want an error that asks minimega for its version and says boom", got, err)
		}
	})
}

// The first version answer logs one line about the capture support: info when minimega supports captures or is
// older, a warning when the version cannot be read, and only the answer when the support is forced on.
func TestKnownVersionLogsCaptureSupport(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := map[string]struct {
		answer, msg, level string
		forced             bool
	}{
		"supported":  {answer: supportedAnswer, msg: "minimega supports experiment captures", level: "INFO"},
		"older":      {answer: unsupportedAnswer, msg: "minimega does not support experiment captures", level: "INFO"},
		"unreadable": {answer: "minimega devel", msg: "cannot read the minimega version", level: "WARN"},
		"forced":     {answer: unsupportedAnswer, msg: "minimega answered the version query", level: "INFO", forced: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useMinimegaVersion(t, nil, tc.forced, false)

			logs := plogtest.Capture(t)

			if got, err := knownVersion(t.Context(), func(context.Context) (string, error) { return tc.answer, nil }); err != nil ||
				got != tc.answer {
				t.Fatalf("knownVersion() = %q, %v; want %q, nil", got, err, tc.answer)
			}

			records := logs.Records(t, plogtest.Message(tc.msg))
			if len(records) != 1 || records[0]["level"] != tc.level {
				t.Errorf("knownVersion(%q) logged %v with message %q, want one %s record", tc.answer, records, tc.msg, tc.level)
			}
		})
	}
}

// When minimega answers another caller while a query waits, knownVersion keeps and gives that first answer.
func TestKnownVersionKeepsFirstAnswer(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, false, false)

	query := func(ctx context.Context) (string, error) {
		// another caller gets its answer first
		if _, err := knownVersion(ctx, func(context.Context) (string, error) { return unsupportedAnswer, nil }); err != nil {
			return "", err
		}

		return supportedAnswer, nil
	}

	if got, err := knownVersion(t.Context(), query); err != nil || got != unsupportedAnswer {
		t.Errorf("knownVersion() after another caller's answer = %q, %v; want %q, nil", got, err, unsupportedAnswer)
	}

	want := CaptureSupport{Available: false, Reason: "requires minimega 3.4.0 or later; the head node runs 3.3.0", Version: "3.3.0"}
	if diff := cmp.Diff(want, CaptureSupportStatus()); diff != "" {
		t.Errorf("CaptureSupportStatus() mismatch (-want +got):\n%s", diff)
	}
}

// StartVersionCheck asks minimega for its version in the background, through queryVersion.
func TestStartVersionCheck(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimegaVersion(t, nil, false, false)

	q := &versionQueries{fails: 1, answer: supportedAnswer}
	queryVersion, versionRetry = q.query, time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	StartVersionCheck(ctx)

	polltest.Until(t, "minimega answers the startup check", CaptureFeatures)

	if got := q.count(); got != 2 {
		t.Errorf("version queries = %d, want 2 (one failure, then the answer)", got)
	}
}
