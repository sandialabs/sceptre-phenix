package builder

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"phenix/store"
)

// LiveDocument names, to [Service.CleanupOrphanedDocuments], the stored
// published document that the reference of a topology names (see
// [DocumentReference.StoredID]).
type LiveDocument struct {
	// Target is the name of the topology the reference was read from.
	Target string
	// ID is the document the reference names for that topology.
	ID string
}

// CleanupOrphanedDocuments removes the published documents not in referenced,
// except those within the [OrphanGracePeriod]. Callers must pass a complete
// set: a document is live only when the topology it was published to names it.
// With dryRun it only counts.
func (s *Service) CleanupOrphanedDocuments(ctx context.Context, referenced []LiveDocument, dryRun bool) (int, error) {
	const operation = "cleaning up orphaned documents"

	live := make(map[LiveDocument]bool, len(referenced))
	for _, document := range referenced {
		live[document] = true
	}

	now := s.clock()
	orphan := func(doc *PublishedDocument) bool {
		return !live[LiveDocument{Target: doc.Target, ID: doc.ID}] && now.Sub(doc.lastPublished()) >= OrphanGracePeriod
	}

	docs, _, unreadable, err := s.listReadablePublished(ctx)
	if err != nil {
		return 0, err
	}

	if !dryRun {
		removed, err := s.deleteDocuments(operation, docs, orphan)

		return removed, errors.Join(newCleanupError(operation, unreadable), err)
	}

	var orphans int

	for i := range docs {
		if orphan(&docs[i]) {
			orphans++
		}
	}

	return orphans, newCleanupError(operation, unreadable)
}

// CleanupOrphanedContent removes the content records that no draft and no
// published document names, except those within the [OrphanGracePeriod]. It
// keeps all content of a draft or document whose metadata cannot be read.
// With dryRun it only counts.
func (s *Service) CleanupOrphanedContent(ctx context.Context, dryRun bool) (int, error) {
	drafts, unreadable, err := s.listDrafts(ctx)
	if err != nil {
		return 0, err
	}

	docs, damaged, _, err := s.listReadablePublished(ctx)
	if err != nil {
		return 0, err
	}

	live := make(map[string]bool, len(drafts)+len(docs)+len(damaged))

	for i := range drafts {
		for j := range drafts[i].History {
			live[snapshotKey(drafts[i].ID, drafts[i].History[j].ID)] = true
		}
	}

	for i := range docs {
		live[publishedKey(docs[i].ID)] = true
	}

	// The key of a published record is its document ID, so the content of
	// a document whose metadata cannot be read is kept.
	for _, id := range damaged {
		live[publishedKey(id)] = true
	}

	keys, err := s.store.ListRecordKeys(NamespaceContent, "")
	if err != nil {
		return 0, fmt.Errorf("listing content keys: %w", err)
	}

	var (
		removed int
		errs    []error
		now     = s.clock()
	)

	for _, key := range keys {
		draftID, ok := contentDraftOf(key)

		switch {
		case !ok:
			errs = append(errs, newCorruptError("content", key, "key is not shaped like a content key"))

			continue
		case live[key], draftID != "" && slices.Contains(unreadable, draftID):
			continue
		}

		record, err := s.store.GetRecord(NamespaceContent, key)

		switch {
		case errors.Is(err, store.ErrRecordNotExist):
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("reading content %s: %w", key, err))

			continue
		case now.Sub(record.Updated) < OrphanGracePeriod:
			continue
		case dryRun:
			removed++

			continue
		}

		// A record written again after it was read is no longer the orphan
		// it was, so the delete names the revision it was read at.
		err = s.store.DeleteRecord(NamespaceContent, key, record.Revision)

		switch {
		case err == nil:
			removed++
		case !errors.Is(err, store.ErrRecordNotExist) && !errors.Is(err, store.ErrRecordConflict):
			errs = append(errs, fmt.Errorf("deleting content %s: %w", key, err))
		}
	}

	return removed, newCleanupError("cleaning up orphaned content", errs)
}

// PublishedUnchanged reports whether the draft is its last publication and
// nothing more: the cursor points at the snapshot it published, and no newer
// snapshot follows it. Deleting such a draft loses no work only while the
// topology it published still names the published document (see
// [Service.DeletePublishedDrafts]).
func (d *DraftMetadata) PublishedUnchanged() bool {
	return !d.Dirty() && !d.CanRedo()
}

// DeletePublishedDrafts removes every published, unchanged draft whose
// publication is still live in referenced, each with its content in one
// transaction at the listed revision. A set that misses a document only keeps
// a draft. With dryRun it only lists the IDs.
func (s *Service) DeletePublishedDrafts(
	ctx context.Context,
	actor string,
	referenced []LiveDocument,
	dryRun bool,
) ([]string, error) {
	if err := validateText("actor", actor, MaxOwnerLength, true); err != nil {
		return nil, err
	}

	drafts, _, err := s.ListDraftsWithDamaged(ctx)
	if err != nil {
		return nil, err
	}

	live := make(map[LiveDocument]bool, len(referenced))
	for _, document := range referenced {
		live[document] = true
	}

	var (
		removed = []string{}
		errs    []error
	)

	for i := range drafts {
		draft := &drafts[i]

		if !draft.PublishedUnchanged() || !live[draft.Publication.live()] {
			continue
		}

		if dryRun {
			removed = append(removed, draft.ID)

			continue
		}

		err := s.DeleteDraft(ctx, draft.ID, actor, draft.Revision)

		switch {
		case err == nil:
			removed = append(removed, draft.ID)
		case !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound):
			errs = append(errs, err)
		}
	}

	return removed, newCleanupError("deleting published, unchanged drafts", errs)
}

// live is the document the publication stored, as the topology it was
// published to names it while the publication is live.
func (p *PublicationState) live() LiveDocument {
	return LiveDocument{Target: p.TopologyTarget, ID: p.DocumentID}
}
