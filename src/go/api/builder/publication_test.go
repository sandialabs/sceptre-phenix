package builder

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"phenix/store"
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

// TestMarkPublishedRetryIsIdempotent covers the caller that publishes, loses
// the response, and repeats the identical request while still holding the
// revision it observed before the first attempt.
func TestMarkPublishedRetryIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	observed := meta.Revision

	published, err := h.service.MarkPublished(ctx, markPublishedRequest(meta, observed))
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	if published.Revision == observed {
		t.Fatal("a successful publication must advance the record revision")
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
				t.Fatalf("retrying an identical publication returned error: %s", fmtErr(err))
			}

			switch {
			case retried.Revision != published.Revision:
				t.Fatalf("retry revision = %d, want the unchanged %d", retried.Revision, published.Revision)
			case retried.Dirty():
				t.Fatal("a retried publication must leave the draft clean")
			case retried.Publication.PublishedAt != published.Publication.PublishedAt:
				t.Fatal("a retry must not rewrite the publication timestamp")
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
		t.Fatalf("MarkPublished returned error: %v", err)
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
				t.Fatalf("MarkPublished error = %s, want ErrConflict for a stale revision", fmtErr(err))
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
				t.Fatalf("MarkPublished error = %s, want ErrInvalid", fmtErr(err))
			}
		})
	}
}

func TestDirtyTracksTheExactPublishedOperation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	if !meta.Dirty() {
		t.Fatal("a never published draft must be dirty")
	}

	published, err := h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		SnapshotID: meta.History[0].ID, Mode: PublishModeTopologyExperiment,
		TopologyTarget: "topo", TopologyAction: TopologyActionUpdate,
		ExperimentTarget: "exp-1", ScenarioTarget: "", DocumentID: "",
	})
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	switch {
	case published.Dirty():
		t.Fatal("a published draft at its current snapshot must be clean")
	case !published.PublishedAs(PublishModeTopologyExperiment, "topo"):
		t.Fatal("the draft must be clean for the operation it was published with")
	case published.PublishedAs(PublishModeTopology, "topo"):
		t.Fatal("the draft must be dirty for a different mode")
	case published.PublishedAs(PublishModeTopologyExperiment, "other"):
		t.Fatal("the draft must be dirty for a different topology target")
	}

	edited := appendTestSnapshot(t, h, published, "topo-v2", testActor)

	if edited.PublishedAs(PublishModeTopologyExperiment, "topo") {
		t.Fatal("an edited draft must be dirty for every operation")
	}
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
		"publishedAt": fakeTime(1), "publishedBy": testActor,
	}
}

func TestTamperedPublicationStateIsRejected(t *testing.T) {
	tests := map[string]func(map[string]any){
		"names a snapshot that is not in the history": func(state map[string]any) {
			state["snapshotId"] = "id-999"
		},
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
				t.Fatalf("GetDraft error = %s, want ErrCorrupt", fmtErr(err))
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
		t.Fatalf("GetDraft of a well formed publication returned error: %s", fmtErr(err))
	}

	if stored.Dirty() {
		t.Fatal("a draft published at its current snapshot must be clean")
	}
}

// TestPruningForgetsAnAgedOutPublication keeps the metadata invariant that a
// publication always names a snapshot the draft still holds.
func TestPruningForgetsAnAgedOutPublication(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	published := meta.History[0].ID

	meta, err := h.service.MarkPublished(ctx, markPublishedRequest(meta, meta.Revision))
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	for i := range MaxSnapshots {
		meta = appendTestSnapshot(t, h, meta, "topo-v"+strconv.Itoa(i), testActor)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %s", fmtErr(err))
	}

	switch {
	case stored.hasSnapshot(published):
		t.Fatal("the published snapshot should have aged out of the history")
	case stored.Publication != nil:
		t.Fatal("a publication whose snapshot aged out must be forgotten, not left dangling")
	case !stored.Dirty():
		t.Fatal("a draft with no publication must be dirty")
	}
}
