package capture

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/common"
	"phenix/util/mm"
)

// useFilesDir makes a new directory the phenix base until the test ends, with the files names in exp1's files
// directory, and returns that files directory.
func useFilesDir(t *testing.T, names ...string) string {
	t.Helper()

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the files directory's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	files := filepath.Join(base, "images", testExp, "files")
	if err := os.MkdirAll(files, 0o750); err != nil {
		t.Fatal(err)
	}

	for _, name := range names {
		if err := os.WriteFile(filepath.Join(files, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return files
}

// captureIDs are the IDs of captures, in order.
func captureIDs(captures []mm.NamespaceCapture) []string {
	ids := make([]string, 0, len(captures))
	for _, c := range captures {
		ids = append(ids, c.ID)
	}

	return ids
}

// A capture that Stop stopped stays in the list as stopped, which minimega no longer gives, until its file is deleted.
func TestStoppedCaptureStaysListed(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	files := useFilesDir(t, "a.pcapng", "b.pcapng")
	useExperiment(t, false)

	cluster := &fakeCluster{}
	received := useCluster(t, cluster)
	cluster.capture(map[string]string{"id": "1", "state": "running", "path": "exp1/files/a.pcapng"})
	cluster.capture(map[string]string{"id": "2", "state": "running", "path": "exp1/files/b.pcapng"})

	if _, err := Stop(t.Context(), testExp, "2"); err != nil {
		t.Fatalf("Stop(2) error = %v", err)
	}

	captures, err := List(t.Context(), testExp)
	if err != nil {
		t.Fatalf("List() after the stop error = %v", err)
	}

	want := []mm.NamespaceCapture{
		{ID: "1", State: "running", Path: "exp1/files/a.pcapng", Format: "pcapng"},
		{ID: "2", State: "stopped", Path: "exp1/files/b.pcapng", Format: "pcapng"},
	}
	if diff := cmp.Diff(want, captures); diff != "" {
		t.Errorf("List() after the stop mismatch (-want +got):\n%s", diff)
	}

	if got, err := Get(t.Context(), testExp, "2"); err != nil || got.State != "stopped" {
		t.Errorf("Get(2) = state %q, %v; want stopped, nil", got.State, err)
	}

	before := len(received())

	if _, err := Stop(t.Context(), testExp, "2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stop(2) of the stopped capture error = %v, want %v", err, ErrNotFound)
	}

	if diff := cmp.Diff([]string{listed}, received()[before:]); diff != "" {
		t.Errorf("Stop(2) of the stopped capture commands mismatch (-want +got):\n%s", diff)
	}

	if err := os.Remove(filepath.Join(files, "b.pcapng")); err != nil {
		t.Fatal(err)
	}

	captures, err = List(t.Context(), testExp)
	if err != nil {
		t.Fatalf("List() after the file's deletion error = %v", err)
	}

	if diff := cmp.Diff([]string{"1"}, captureIDs(captures)); diff != "" {
		t.Errorf("List() after the file's deletion IDs mismatch (-want +got):\n%s", diff)
	}
}

// A stopped experiment drops the captures that it kept, and its next run starts with none.
func TestKeptCapturesEndWithTheRun(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useFilesDir(t, "b.pcapng")
	useExperiment(t, false)

	cluster := &fakeCluster{}
	useCluster(t, cluster)
	cluster.capture(map[string]string{"id": "2", "state": "running", "path": "exp1/files/b.pcapng"})

	if _, err := Stop(t.Context(), testExp, "2"); err != nil {
		t.Fatalf("Stop(2) error = %v", err)
	}

	useExperiment(t, true)

	if captures, err := List(t.Context(), testExp); err != nil || len(captures) != 0 {
		t.Errorf("List() of the stopped experiment = %v, %v; want none", captures, err)
	}

	useExperiment(t, false)

	if captures, err := List(t.Context(), testExp); err != nil || len(captures) != 0 {
		t.Errorf("List() of the next run = %v, %v; want none", captures, err)
	}
}

func TestKeptMerge(t *testing.T) { //nolint:paralleltest // replaces the phenix base
	useFilesDir(t, "a.pcapng", "b.pcapng")

	const run = "2026-11-05T14:22:33Z"

	stopped := func(id, file string) mm.NamespaceCapture {
		return mm.NamespaceCapture{ID: id, State: "stopped", Path: "exp1/files/" + file}
	}

	running := func(id, file string) mm.NamespaceCapture {
		return mm.NamespaceCapture{ID: id, State: "running", Path: "exp1/files/" + file}
	}

	tests := map[string]struct {
		keep  []mm.NamespaceCapture
		start string
		live  []mm.NamespaceCapture
		want  []mm.NamespaceCapture
	}{
		"kept and live sorted by ID": {
			keep: []mm.NamespaceCapture{stopped("10", "a.pcapng")}, start: run,
			live: []mm.NamespaceCapture{running("9", "b.pcapng")},
			want: []mm.NamespaceCapture{running("9", "b.pcapng"), stopped("10", "a.pcapng")},
		},
		"an ID that minimega lists again": {
			keep: []mm.NamespaceCapture{stopped("2", "a.pcapng")}, start: run,
			live: []mm.NamespaceCapture{running("2", "b.pcapng")},
			want: []mm.NamespaceCapture{running("2", "b.pcapng")},
		},
		"a capture of an earlier run": {
			keep: []mm.NamespaceCapture{stopped("2", "a.pcapng")}, start: "2026-11-06T08:00:00Z",
			want: nil,
		},
		"a capture without its file": {
			keep: []mm.NamespaceCapture{stopped("2", "gone.pcapng")}, start: run,
			want: nil,
		},
		"a capture outside the files directory": {
			keep: []mm.NamespaceCapture{{ID: "2", State: "stopped", Path: "/elsewhere/a.pcapng"}}, start: run,
			want: nil,
		},
		"a second stop of one ID keeps the last": {
			keep:  []mm.NamespaceCapture{stopped("2", "a.pcapng"), stopped("2", "b.pcapng")},
			start: run,
			want:  []mm.NamespaceCapture{stopped("2", "b.pcapng")},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() { Forget(testExp) })

			for _, c := range tc.keep {
				kept.add(testExp, run, c)
			}

			got := kept.merge(testExp, tc.start, tc.live)
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("merge() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// One experiment keeps at most maxKept stopped captures, and drops the oldest stop first.
func TestKeptBound(t *testing.T) { //nolint:paralleltest // replaces the phenix base
	useFilesDir(t, "a.pcapng")
	t.Cleanup(func() { Forget(testExp) })

	const run = "2026-11-05T14:22:33Z"

	for i := range maxKept + 2 {
		kept.add(testExp, run, mm.NamespaceCapture{
			ID: strconv.Itoa(i + 1), State: "stopped", Path: "exp1/files/a.pcapng",
		})
	}

	got := captureIDs(kept.merge(testExp, run, nil))
	if len(got) != maxKept || got[0] != "3" || got[len(got)-1] != strconv.Itoa(maxKept+2) {
		t.Errorf("merge() after %d stops = %d captures, %s to %s; want %d, 3 to %d",
			maxKept+2, len(got), got[0], got[len(got)-1], maxKept, maxKept+2)
	}
}

// The merged list is sorted by ID, has each ID once, and has every live capture as minimega gave it.
func TestKeptMergeProperties(t *testing.T) { //nolint:paralleltest // replaces the phenix base
	useFilesDir(t, "a.pcapng")

	const run = "2026-11-05T14:22:33Z"

	ids := func(t *rapid.T, label string) []string {
		return rapid.SliceOfNDistinct(rapid.Map(rapid.IntRange(1, 40), strconv.Itoa), 0, 10, rapid.ID[string]).Draw(t, label)
	}

	rapid.Check(t, func(rt *rapid.T) {
		Forget(testExp)

		for _, id := range ids(rt, "kept") {
			kept.add(testExp, run, mm.NamespaceCapture{ID: id, State: "stopped", Path: "exp1/files/a.pcapng"})
		}

		liveIDs := ids(rt, "live")

		live := make([]mm.NamespaceCapture, 0, len(liveIDs))
		for _, id := range liveIDs {
			live = append(live, mm.NamespaceCapture{ID: id, State: "running", Path: "exp1/files/a.pcapng"})
		}

		slices.SortFunc(live, func(a, b mm.NamespaceCapture) int { return mm.CompareCaptureIDs(a.ID, b.ID) })

		got := captureIDs(kept.merge(testExp, run, live))
		if !slices.IsSortedFunc(got, mm.CompareCaptureIDs) || len(slices.Compact(slices.Clone(got))) != len(got) {
			rt.Fatalf("merge() IDs = %v, want sorted and distinct", got)
		}

		for _, c := range live {
			if !slices.Contains(got, c.ID) {
				rt.Fatalf("merge() IDs = %v, want live capture %s", got, c.ID)
			}
		}
	})

	Forget(testExp)
}
