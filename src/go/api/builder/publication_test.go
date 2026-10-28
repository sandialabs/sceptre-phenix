package builder

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
)

// markPublishedRequest builds the request a topology publication uses, so the
// tests below only vary the fields they are about.
func markPublishedRequest(meta *DraftMetadata, expected int64) MarkPublishedRequest {
	return MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: expected,
		SnapshotID: meta.History[meta.Cursor].ID, Mode: PublishModeTopology,
		TopologyTarget: "topo", TopologyAction: TopologyActionCreate,
		ExperimentTarget: "", ScenarioTarget: "", DocumentID: "doc-1",
	}
}

// TestMarkPublishedRetryIsIdempotent repeats an identical publish request with
// the revision read before the first attempt, as after a lost response.
func TestMarkPublishedRetryIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	observed := meta.Revision

	published, err := h.service.MarkPublished(ctx, markPublishedRequest(meta, observed))
	if err != nil {
		t.Fatalf("MarkPublished(%s) returned error: %v", meta.ID, err)
	}

	if published.Revision == observed {
		t.Errorf("MarkPublished(%s) revision = %d, want it advanced from %d", meta.ID, published.Revision, observed)
	}

	retries := map[string]int64{
		"pre-mark revision": observed,
		"current revision":  published.Revision,
		"any revision":      store.AnyRevision,
	}

	for name, expected := range retries {
		t.Run(name, func(t *testing.T) {
			retried, err := h.service.MarkPublished(ctx, markPublishedRequest(meta, expected))
			if err != nil {
				t.Fatalf("MarkPublished(%s) retry returned error: %s", meta.ID, fmtErr(err))
			}

			// The retry changes nothing: not the revision, nor the publication and its time.
			if diff := cmp.Diff(published, retried); diff != "" {
				t.Errorf("MarkPublished(%s) retry mismatch (-want +got):\n%s", meta.ID, diff)
			}

			if retried.Dirty() {
				t.Errorf("MarkPublished(%s) retry Dirty() = true, want false", meta.ID)
			}
		})
	}
}

func TestMarkPublishedRetryWithDifferentDetailsConflicts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	observed := meta.Revision

	if _, err := h.service.MarkPublished(ctx, markPublishedRequest(meta, observed)); err != nil {
		t.Fatalf("MarkPublished(%s) returned error: %v", meta.ID, err)
	}

	tests := map[string]func(*MarkPublishedRequest){
		"different mode": func(req *MarkPublishedRequest) {
			req.Mode = PublishModeTopologyExperiment
			req.ExperimentTarget = "exp-1"
		},
		"different topology target": func(req *MarkPublishedRequest) { req.TopologyTarget = "other" },
		"different topology action": func(req *MarkPublishedRequest) { req.TopologyAction = TopologyActionUpdate },
		"different document":        func(req *MarkPublishedRequest) { req.DocumentID = "doc-2" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := markPublishedRequest(meta, observed)
			mutate(&req)

			if _, err := h.service.MarkPublished(ctx, req); !errors.Is(err, ErrConflict) {
				t.Errorf("MarkPublished(%s) with a stale revision error = %s, want ErrConflict", meta.ID, fmtErr(err))
			}
		})
	}
}

func TestPublicationTargetValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	tests := map[string]func(*MarkPublishedRequest){
		"topology mode with an experiment target": func(req *MarkPublishedRequest) {
			req.ExperimentTarget = "exp-1"
		},
		"topology mode with a scenario target": func(req *MarkPublishedRequest) {
			req.ScenarioTarget = "scen-1"
		},
		"experiment mode without an experiment target": func(req *MarkPublishedRequest) {
			req.Mode = PublishModeTopologyExperiment
		},
		"missing topology target": func(req *MarkPublishedRequest) { req.TopologyTarget = "" },
		"unknown topology action": func(req *MarkPublishedRequest) { req.TopologyAction = "replace" },
		"oversized topology target": func(req *MarkPublishedRequest) {
			req.TopologyTarget = string(make([]byte, 0, MaxTargetLength+1))
			for range MaxTargetLength + 1 {
				req.TopologyTarget += "t"
			}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := markPublishedRequest(meta, meta.Revision)
			mutate(&req)

			if _, err := h.service.MarkPublished(ctx, req); !errors.Is(err, ErrInvalid) {
				t.Errorf("MarkPublished(%s) error = %s, want ErrInvalid", meta.ID, fmtErr(err))
			}
		})
	}
}

func TestDirtyTracksThePublishedSnapshot(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	if !meta.Dirty() {
		t.Error("CreateDraft(topo) Dirty() = false, want true for a never published draft")
	}

	published, err := h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		SnapshotID: meta.History[0].ID, Mode: PublishModeTopologyExperiment,
		TopologyTarget: "topo", TopologyAction: TopologyActionUpdate,
		ExperimentTarget: "exp-1", ScenarioTarget: "", DocumentID: "",
	})
	if err != nil {
		t.Fatalf("MarkPublished(%s) returned error: %v", meta.ID, err)
	}

	if published.Dirty() {
		t.Errorf("MarkPublished(%s) Dirty() = true, want false at the published snapshot", meta.ID)
	}

	want := &PublicationState{
		Mode: PublishModeTopologyExperiment, TopologyTarget: "topo", TopologyAction: TopologyActionUpdate,
		ExperimentTarget: "exp-1", SnapshotID: meta.History[0].ID, Digest: meta.History[0].Digest,
		Revision: meta.Revision, PublishedBy: testActor,
	}

	if diff := publicationDiff(want, published.Publication); diff != "" {
		t.Errorf("MarkPublished(%s) publication mismatch (-want +got):\n%s", meta.ID, diff)
	}

	edited := appendTestSnapshot(t, h, published, "topo-v2", testActor)

	if !edited.Dirty() {
		t.Errorf("AppendSnapshot(%s) after publishing Dirty() = false, want true", meta.ID)
	}
}

// publicationDiff returns how two publications differ, as cmp.Diff does, in
// all but the publication time, which the test clock sets.
func publicationDiff(want, got *PublicationState) string {
	return cmp.Diff(want, got, cmpopts.IgnoreFields(PublicationState{}, "PublishedAt"))
}

// publicationOf returns the stored publication state as a generic map, so tests
// can tamper with individual fields.
func publicationOf(t *testing.T, meta *DraftMetadata) map[string]any {
	t.Helper()

	current := meta.History[meta.Cursor]

	return map[string]any{
		"mode": string(PublishModeTopology), "topologyTarget": "topo",
		"topologyAction": string(TopologyActionCreate), "snapshotId": current.ID,
		"digest": current.Digest, "revision": meta.Revision,
		"publishedAt": memrecord.Time(1), "publishedBy": testActor,
	}
}

func TestTamperedPublicationStateIsRejected(t *testing.T) {
	tests := map[string]func(map[string]any){
		"records a digest the snapshot does not have": func(state map[string]any) {
			state["digest"] = digestOf([]byte("other content"))
		},
		"records a digest that is not sha256": func(state map[string]any) {
			state["digest"] = "not-a-digest"
		},
		"names an invalid snapshot": func(state map[string]any) {
			state["snapshotId"] = "../escape"
		},
		"has no topology target": func(state map[string]any) {
			state["topologyTarget"] = ""
		},
		"has an oversized topology target": func(state map[string]any) {
			state["topologyTarget"] = strings.Repeat("t", MaxTargetLength+1)
		},
		"has an oversized experiment target": func(state map[string]any) {
			state["mode"] = string(PublishModeTopologyExperiment)
			state["experimentTarget"] = strings.Repeat("e", MaxTargetLength+1)
		},
		"names an experiment for a topology publication": func(state map[string]any) {
			state["experimentTarget"] = "exp-1"
		},
		"names a scenario for a topology publication": func(state map[string]any) {
			state["scenarioTarget"] = "scen-1"
		},
		"is an experiment publication without an experiment": func(state map[string]any) {
			state["mode"] = string(PublishModeTopologyExperiment)
		},
		"has an unknown topology action": func(state map[string]any) {
			state["topologyAction"] = "replace"
		},
		"has no actor": func(state map[string]any) {
			state["publishedBy"] = ""
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			meta := createTestDraft(t, h, "topo")
			meta = appendTestSnapshot(t, h, meta, "topo-v2", testActor)

			state := publicationOf(t, meta)
			mutate(state)

			tamperDraft(t, h, meta, func(raw map[string]any) { raw["publication"] = state })

			if _, err := h.service.GetDraft(ctx, meta.ID); !errors.Is(err, ErrCorrupt) {
				t.Errorf("GetDraft(%s) error = %s, want ErrCorrupt", meta.ID, fmtErr(err))
			}
		})
	}
}

func TestUntamperedPublicationStateIsAccepted(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	tamperDraft(t, h, meta, func(raw map[string]any) { raw["publication"] = publicationOf(t, meta) })

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft(%s) of a well formed publication returned error: %s", meta.ID, fmtErr(err))
	}

	if stored.Dirty() {
		t.Errorf("GetDraft(%s) Dirty() = true, want false for a draft published at its current snapshot", meta.ID)
	}

	// A publication whose snapshot was deleted from the history is kept, and
	// the draft is dirty: no snapshot it holds was published.
	deleted := publicationOf(t, stored)
	deleted["snapshotId"] = "id-999"

	tamperDraft(t, h, stored, func(raw map[string]any) { raw["publication"] = deleted })

	if stored, err = h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Fatalf("GetDraft(%s) of a publication of a deleted snapshot returned error: %s", meta.ID, fmtErr(err))
	}

	if stored.Publication == nil {
		t.Errorf("GetDraft(%s) publication = nil, want the publication of the deleted snapshot kept", meta.ID)
	}

	if !stored.Dirty() {
		t.Errorf("GetDraft(%s) Dirty() = false, want true when no snapshot it holds was published", meta.ID)
	}
}

// TestPruningKeepsAnAgedOutPublication asserts pruning keeps a publication whose
// snapshot aged out, and the draft stays dirty and publishable.
func TestPruningKeepsAnAgedOutPublication(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	published := meta.History[0].ID

	meta, err := h.service.MarkPublished(ctx, markPublishedRequest(meta, meta.Revision))
	if err != nil {
		t.Fatalf("MarkPublished(%s) returned error: %v", meta.ID, err)
	}

	want := meta.Publication

	for i := range MaxSnapshots {
		meta = appendTestSnapshot(t, h, meta, "topo-v"+strconv.Itoa(i), testActor)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft(%s) returned error: %s", meta.ID, fmtErr(err))
	}

	if stored.hasSnapshot(published) {
		t.Errorf("GetDraft(%s) history holds the published snapshot %s, want it aged out", meta.ID, published)
	}

	if diff := cmp.Diff(want, stored.Publication); diff != "" {
		t.Errorf("GetDraft(%s) publication mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if !stored.Dirty() {
		t.Errorf("GetDraft(%s) Dirty() = false, want true when the published snapshot aged out", meta.ID)
	}

	// The drafts list reads the kept publication too, rather than listing the
	// draft as damaged.
	drafts, damaged, err := h.service.ListDraftsWithDamaged(ctx)
	if err != nil {
		t.Fatalf("ListDraftsWithDamaged returned error: %s", fmtErr(err))
	}

	if len(damaged) != 0 {
		t.Errorf("ListDraftsWithDamaged damaged = %+v, want none", damaged)
	}

	if len(drafts) != 1 {
		t.Errorf("ListDraftsWithDamaged = %d drafts, want 1", len(drafts))
	} else if diff := cmp.Diff(want, drafts[0].Publication); diff != "" {
		t.Errorf("ListDraftsWithDamaged publication mismatch (-want +got):\n%s", diff)
	}

	republished, err := h.service.MarkPublished(ctx, markPublishedRequest(stored, stored.Revision))
	if err != nil {
		t.Fatalf("MarkPublished(%s) after pruning returned error: %s", meta.ID, fmtErr(err))
	}

	if republished.Publication == nil {
		t.Errorf("MarkPublished(%s) after pruning publication = nil, want the current snapshot", meta.ID)
	} else if got := republished.Publication.SnapshotID; got != stored.Current().ID {
		t.Errorf("MarkPublished(%s) after pruning snapshot = %s, want the current %s", meta.ID, got, stored.Current().ID)
	}

	if republished.Dirty() {
		t.Errorf("MarkPublished(%s) after pruning Dirty() = true, want false", meta.ID)
	}
}
