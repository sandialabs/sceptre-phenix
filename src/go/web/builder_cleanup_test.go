package web

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	bdoc "phenix/types/builder"
)

// builderCleanupPath is the route that cleans up Builder storage.
const builderCleanupPath = "/builder/cleanup"

// builderPublishedDraft creates a draft named name and publishes it as the
// topology name. It returns the draft as the publish left it.
func builderPublishedDraft(t *testing.T, harness *builderHarness, name string) (builderDraftResponse, *bdoc.Document) {
	t.Helper()

	document := bdoc.NewDocument(name)
	draft := createBuilderPublishDraft(t, harness, document)
	published, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"`+name+`","action":"create"}}`, http.StatusOK)

	return published.Draft, document
}

// builderCleanup asks for a cleanup as alice, with the dryRun query when it
// is not "", and returns the answer.
func builderCleanup(t *testing.T, harness *builderHarness, dryRun string) builderCleanupResponse {
	t.Helper()

	path := builderCleanupPath
	if dryRun != "" {
		path += "?dryRun=" + dryRun
	}

	var response builderCleanupResponse

	harness.doDecode(builderRequest{method: http.MethodPost, path: path, user: builderAlice}, http.StatusOK, &response)

	return response
}

// TestBuilderCleanupDeletesPublishedUnchangedDrafts deletes only the drafts
// whose current snapshot is the one they last published, with no newer
// snapshot, and keeps what they published.
func TestBuilderCleanupDeletesPublishedUnchangedDrafts(t *testing.T) {
	harness := newBuilderHarness(t)

	clean, _ := builderPublishedDraft(t, harness, "clean")
	unpublished := harness.createDraft(builderAlice, "unpublished")

	edited, editedDoc := builderPublishedDraft(t, harness, "edited")
	edited = editBuilderDraft(t, harness, edited, editedDoc, "aa")

	// Published, edited, then undone: the edit is still a newer snapshot.
	undone, undoneDoc := builderPublishedDraft(t, harness, "undone")
	undone = editBuilderDraft(t, harness, undone, undoneDoc, "aa")
	harness.doDecode(builderRequest{
		method: http.MethodPatch, path: "/builder/drafts/" + undone.ID + "/cursor",
		body: `{"index":0}`, user: builderAlice, ifMatch: undone.ETag,
	}, http.StatusOK, nil)

	drafts := harness.store.Keys(bapi.NamespaceDrafts)

	dryRun := builderCleanup(t, harness, "true")

	want := builderCleanupResponse{DryRun: true, Drafts: []string{clean.ID}, Documents: 0, Content: 0}
	if diff := cmp.Diff(want, dryRun); diff != "" {
		t.Errorf("POST %s?dryRun=true mismatch (-want +got):\n%s", builderCleanupPath, diff)
	}

	if diff := cmp.Diff(drafts, harness.store.Keys(bapi.NamespaceDrafts)); diff != "" {
		t.Errorf("drafts after a dry run mismatch (-want +got):\n%s", diff)
	}

	removed := builderCleanup(t, harness, "")

	want.DryRun = false
	if diff := cmp.Diff(want, removed); diff != "" {
		t.Errorf("POST %s mismatch (-want +got):\n%s", builderCleanupPath, diff)
	}

	for _, kept := range []string{unpublished.ID, edited.ID, undone.ID} {
		harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts/" + kept, user: builderAlice},
			http.StatusOK, nil)
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts/" + clean.ID, user: builderAlice},
		http.StatusNotFound, nil)

	if keys := harness.store.Keys(bapi.NamespaceContent); slices.ContainsFunc(keys, func(key string) bool {
		return strings.HasPrefix(key, "drafts/"+clean.ID+"/")
	}) {
		t.Errorf("content after the cleanup = %q, want none of draft %s", keys, clean.ID)
	}

	// What the draft published stays.
	if _, err := harness.getConfig("Topology/clean"); err != nil {
		t.Errorf("getConfig(Topology/clean) after the cleanup returned error: %v", err)
	}

	if again := builderCleanup(t, harness, ""); len(again.Drafts) != 0 {
		t.Errorf("POST %s again deleted %q, want nothing", builderCleanupPath, again.Drafts)
	}
}

// TestBuilderCleanupKeepsADraftWhoseTopologyIsGone keeps a published,
// unchanged draft whose topology was deleted: the draft is then the only
// copy of its diagram.
func TestBuilderCleanupKeepsADraftWhoseTopologyIsGone(t *testing.T) {
	harness := newBuilderHarness(t)

	clean, _ := builderPublishedDraft(t, harness, "clean")
	gone, _ := builderPublishedDraft(t, harness, "gone")

	harness.configs = slices.DeleteFunc(harness.configs, func(config store.Config) bool {
		return config.FullName() == "Topology/gone"
	})

	for _, dryRun := range []string{"true", ""} {
		if got := builderCleanup(t, harness, dryRun); !slices.Equal(got.Drafts, []string{clean.ID}) {
			t.Errorf("POST %s?dryRun=%s deleted drafts %q, want only %s", builderCleanupPath, dryRun, got.Drafts, clean.ID)
		}
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts/" + gone.ID, user: builderAlice},
		http.StatusOK, nil)
}

// TestBuilderCleanupWarnsWhenTheOrphanRemovalFails deletes the drafts, and
// answers with them and a Warning, when a published record cannot be read.
func TestBuilderCleanupWarnsWhenTheOrphanRemovalFails(t *testing.T) {
	harness := newBuilderHarness(t)

	clean, _ := builderPublishedDraft(t, harness, "clean")

	if _, err := harness.store.CreateRecord(bapi.NamespacePublished, "damaged", []byte("not metadata")); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	for _, path := range []string{builderCleanupPath + "?dryRun=true", builderCleanupPath} {
		var response builderCleanupResponse

		recorder := harness.doDecode(builderRequest{method: http.MethodPost, path: path, user: builderAlice},
			http.StatusOK, &response)

		if !slices.Equal(response.Drafts, []string{clean.ID}) {
			t.Errorf("POST %s deleted drafts %q, want %s", path, response.Drafts, clean.ID)
		}

		if builderWarning(recorder) == "" {
			t.Errorf("POST %s: no Warning, want one for the damaged record", path)
		}
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts/" + clean.ID, user: builderAlice},
		http.StatusNotFound, nil)
}

// builderClockHarness returns a harness whose service and store share a
// clock that the test moves.
func builderClockHarness(t *testing.T) (*builderHarness, *atomic.Int64) {
	t.Helper()

	var (
		fake  = memrecord.New()
		clock = new(atomic.Int64)
		now   = func() time.Time { return memrecord.Time(clock.Load()) }
	)

	fake.Stamp = now

	service, err := bapi.New(bapi.WithStore(fake), bapi.WithClock(now))
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	harness := newBuilderHarnessWith(t, []builderOption{withBuilderService(service)})
	harness.service, harness.store = service, fake

	return harness, clock
}

// TestBuilderCleanupRemovesOrphans removes the published documents and the
// content that nothing names, once they are past the grace period.
func TestBuilderCleanupRemovesOrphans(t *testing.T) {
	harness, clock := builderClockHarness(t)

	orphan := builderPutDocument(t, harness, builderKindTopology, "gone", "gone")

	if _, err := harness.store.CreateRecord(bapi.NamespaceContent, "drafts/vanished/snapshot", []byte("orphan")); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	// Within the grace period, a publication or a save may still name them.
	if recent := builderCleanup(t, harness, "true"); recent.Documents != 0 || recent.Content != 0 {
		t.Errorf("POST %s?dryRun=true of recent orphans = %+v, want nothing", builderCleanupPath, recent)
	}

	clock.Add(int64(2 * bapi.OrphanGracePeriod / time.Second))

	want := builderCleanupResponse{DryRun: true, Drafts: []string{}, Documents: 1, Content: 1}
	if diff := cmp.Diff(want, builderCleanup(t, harness, "true")); diff != "" {
		t.Errorf("POST %s?dryRun=true mismatch (-want +got):\n%s", builderCleanupPath, diff)
	}

	want.DryRun = false
	if diff := cmp.Diff(want, builderCleanup(t, harness, "false")); diff != "" {
		t.Errorf("POST %s?dryRun=false mismatch (-want +got):\n%s", builderCleanupPath, diff)
	}

	if _, err := harness.service.GetPublishedDocument(t.Context(), orphan.ID); err == nil {
		t.Errorf("GetPublishedDocument(%s) after the cleanup found it, want it removed", orphan.ID)
	}

	if keys := harness.store.Keys(bapi.NamespaceContent); len(keys) != 0 {
		t.Errorf("content after the cleanup = %q, want none", keys)
	}
}

// TestBuilderCleanupAccess lets every Builder user clean up, refuses a role
// without the Builder, and refuses a dryRun that is not a boolean.
func TestBuilderCleanupAccess(t *testing.T) {
	harness := newBuilderHarness(t)

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{
			name: "builder only", user: builderAlice, role: builderOnlyRole(),
			request: builderRequest{method: http.MethodPost, path: builderCleanupPath + "?dryRun=true"},
			status:  http.StatusOK, code: "",
		},
		{
			name: "no builder", user: builderAlice, role: builderConfigsOnlyRole(),
			request: builderRequest{method: http.MethodPost, path: builderCleanupPath + "?dryRun=true"},
			status:  http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{
			name: "dryRun not a boolean", user: builderAlice, role: nil,
			request: builderRequest{method: http.MethodPost, path: builderCleanupPath + "?dryRun=maybe"},
			status:  http.StatusBadRequest, code: bdoc.CodeRequestInvalid,
		},
	})
}

// TestBuilderSweepEvery removes orphaned storage on each tick of the sweep
// that the server starts with.
func TestBuilderSweepEvery(t *testing.T) {
	harness, clock := builderClockHarness(t)

	if _, err := harness.store.CreateRecord(bapi.NamespaceContent, "drafts/vanished/snapshot", []byte("orphan")); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// The sweep at start keeps the orphan, which is too recent. The sweep
	// lists the topologies from its own goroutine, so it gets a list of its
	// own.
	_, err := newBuilderAPI(
		withBuilderService(harness.service),
		withBuilderConfigs(
			func(string) (store.Configs, error) { return store.Configs{}, nil },
			func(string) (*store.Config, error) { return nil, store.ErrNotExist },
		),
		withBuilderTemplateFiles(""),
		withBuilderSweep(ctx, time.Millisecond),
	)
	if err != nil {
		t.Fatalf("newBuilderAPI returned error: %v", err)
	}

	if got := harness.store.Count(bapi.NamespaceContent); got != 1 {
		t.Fatalf("content after the sweep at start = %d records, want the recent orphan kept", got)
	}

	clock.Add(int64(2 * bapi.OrphanGracePeriod / time.Second))

	deadline := time.Now().Add(10 * time.Second)

	for harness.store.Count(bapi.NamespaceContent) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the sweep did not remove the orphaned content within 10s")
		}

		time.Sleep(time.Millisecond)
	}
}

// TestBuilderSweepEveryStops returns from the sweep when its context ends.
func TestBuilderSweepEveryStops(t *testing.T) {
	harness := newBuilderHarness(t)
	api := &builderAPI{drafts: harness.service, listConfigs: harness.listConfigs}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan struct{})

	go func() {
		api.sweepEvery(ctx, time.Hour)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("sweepEvery did not return within 10s after its context ended")
	}
}
