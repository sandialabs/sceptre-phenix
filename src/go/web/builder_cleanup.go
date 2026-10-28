package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/util/plog"
)

// builderSweepEvery is how often the server removes orphaned Builder storage
// while it runs: as often as content becomes old enough to remove.
const builderSweepEvery = bapi.OrphanGracePeriod

// builderSweep is when the server removes orphaned Builder storage after it
// starts: every `every`, until ctx ends. A nil ctx runs no sweeps after the
// one at start.
type builderSweep struct {
	ctx   context.Context //nolint:containedctx // the life of the server, which the sweeps end with
	every time.Duration
}

// builderOrphans is what a removal of orphaned Builder storage removed, or
// with a dry run would remove, and what stopped a part of it.
type builderOrphans struct {
	documents int
	content   int
	err       error
}

// builderCleanupResponse is what a cleanup of Builder storage removed, or
// with DryRun would remove: the published, unchanged drafts by ID, and how
// many published documents and content records no longer belonged to
// anything.
type builderCleanupResponse struct {
	DryRun    bool     `json:"dryRun"`
	Drafts    []string `json:"drafts"`
	Documents int      `json:"documents"`
	Content   int      `json:"content"`
}

// withBuilderSweep makes the server remove orphaned Builder storage every
// `every` (see [builderAPI.sweepEvery]), until ctx ends.
func withBuilderSweep(ctx context.Context, every time.Duration) builderOption {
	return func(api *builderAPI) { api.sweep = builderSweep{ctx: ctx, every: every} }
}

// storageRoutes registers the route that cleans up Builder storage.
func (b *builderAPI) storageRoutes(router *mux.Router) {
	router.Handle("/builder/cleanup", builderHandler(b.cleanupStorage)).
		Methods("POST")
}

// sweepEvery runs [builderAPI.sweepStorage] once every `every`, until ctx
// ends.
func (b *builderAPI) sweepEvery(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.sweepStorage(ctx)
		}
	}
}

// sweepStorage removes the orphaned Builder storage and logs any failure.
// The server runs it at start and then every [builderSweepEvery].
func (b *builderAPI) sweepStorage(ctx context.Context) {
	live, complete, err := b.liveDocuments()
	orphans := b.removeOrphans(ctx, live, complete && err == nil, false)

	if err := errors.Join(err, orphans.err); err != nil {
		plog.Error(plog.TypeSystem, "cleaning up orphaned builder storage", "err", err)
	}

	if orphans.documents > 0 || orphans.content > 0 {
		plog.Info(
			plog.TypeSystem, "removed orphaned builder storage",
			"documents", orphans.documents, "content", orphans.content,
		)
	}
}

// removeOrphans removes unreferenced published documents and content records past [bapi.OrphanGracePeriod].
// It removes documents only when live is complete, so a topology that cannot be read keeps its document.
func (b *builderAPI) removeOrphans(
	ctx context.Context,
	live []bapi.LiveDocument,
	complete, dryRun bool,
) builderOrphans {
	var (
		orphans                  builderOrphans
		documentsErr, contentErr error
	)

	if complete {
		orphans.documents, documentsErr = b.drafts.CleanupOrphanedDocuments(ctx, live, dryRun)
	}

	orphans.content, contentErr = b.drafts.CleanupOrphanedContent(ctx, dryRun)
	orphans.err = errors.Join(documentsErr, contentErr)

	return orphans
}

// liveDocuments returns the published document that the reference of each
// topology names. It also reports whether every reference decoded. When one
// does not, the documents it returns are those of the other topologies.
func (b *builderAPI) liveDocuments() ([]bapi.LiveDocument, bool, error) {
	topologies, err := b.listConfigs(builderKindTopology)
	if err != nil {
		return nil, false, fmt.Errorf("listing the topologies that reference Builder documents: %w", err)
	}

	var (
		live     = make([]bapi.LiveDocument, 0, len(topologies))
		complete = true
	)

	for _, topology := range topologies {
		value, ok := topology.Metadata.Annotations[bapi.DocumentAnnotation]
		if !ok {
			continue
		}

		reference, err := bapi.DecodeReference(value)
		if err != nil {
			plog.Error(
				plog.TypeSystem,
				"skipping builder document cleanup because a topology reference is invalid",
				"topology", topology.FullName(),
				"err", err,
			)

			complete = false

			continue
		}

		if id := reference.StoredID(topology.Metadata.Name); id != "" {
			live = append(live, bapi.LiveDocument{Target: topology.Metadata.Name, ID: id})
		}
	}

	return live, complete, nil
}

// cleanupStorage - POST /builder/cleanup.
// Every Builder user may clean up, as every Builder user may delete any draft. A failure gives a Warning header, not an error.
func (b *builderAPI) cleanupStorage(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderCleanupStorage")

	actor, err := builderAuthorize(r, "cleaning up Builder storage")
	if err != nil {
		return err
	}

	dryRun, err := parseDryRun(r.URL.Query())
	if err != nil {
		return err
	}

	ctx := r.Context()

	live, complete, drafts, draftsErr := b.deletePublishedDrafts(ctx, actor.user, dryRun)
	if draftsErr != nil && !errors.Is(draftsErr, bapi.ErrCleanup) {
		return builderWebError(draftsErr, "unable to clean up Builder drafts")
	}

	// The drafts are deleted now, so a failure from here on only warns.
	orphans := b.removeOrphans(ctx, live, complete, dryRun)

	if partial := errors.Join(draftsErr, orphans.err); partial != nil {
		builderWarnCleanup(w, partial, "clean up storage", actor.user)
	}

	if !dryRun {
		plog.Info(
			plog.TypeAction, "cleaned up builder storage",
			"user", actor.user, "drafts", len(drafts), "documents", orphans.documents, "content", orphans.content,
		)
	}

	return builderWriteJSON(w, http.StatusOK, "", builderCleanupResponse{
		DryRun:    dryRun,
		Drafts:    drafts,
		Documents: orphans.documents,
		Content:   orphans.content,
	})
}

// deletePublishedDrafts reads the live documents (see
// [builderAPI.liveDocuments]) and deletes the published, unchanged drafts
// (see [bapi.Service.DeletePublishedDrafts]). It holds [builderPublishLock],
// as a publication does from its read of the draft until it marks the draft
// published. Thus it never deletes a draft that a publication is writing.
func (b *builderAPI) deletePublishedDrafts(
	ctx context.Context,
	actor string,
	dryRun bool,
) ([]bapi.LiveDocument, bool, []string, error) {
	builderPublishLock.Lock()
	defer builderPublishLock.Unlock()

	live, complete, err := b.liveDocuments()
	if err != nil {
		return nil, false, nil, err
	}

	drafts, err := b.drafts.DeletePublishedDrafts(ctx, actor, live, dryRun)

	return live, complete, drafts, err
}
