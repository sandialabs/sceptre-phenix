package web

import (
	"encoding/json"

	bapi "phenix/api/builder"
	"phenix/store"
	"phenix/util/plog"
	"phenix/web/rbac"
)

// builderExperimentLink is what an Experiment config says of the Builder
// publication that made it.
type builderExperimentLink struct {
	// name is the experiment's name, and topology the topology phenix
	// recorded it was built from (its "topology" annotation).
	name     string
	topology string
	// draftID and documentID are the draft and the published document of
	// that publication (see [builderExperimentAnnotation]).
	draftID    string
	documentID string
}

// How well an experiment answers for a publication, best first. Among the
// experiments of one rank the smallest name in byte order is taken, so
// the same experiment is named every time.
const (
	// builderExperimentRankTarget is the experiment a draft's last
	// publication named.
	builderExperimentRankTarget = iota
	// builderExperimentRankDocument is an experiment that records the
	// published document itself.
	builderExperimentRankDocument
	// builderExperimentRankName is any other experiment of the publication.
	builderExperimentRankName
)

// builderExperimentChoice is the best experiment offered so far.
type builderExperimentChoice struct {
	name string
	rank int
}

// offer takes the experiment when it is better than the one held.
func (c *builderExperimentChoice) offer(name string, rank int) {
	if c.name == "" || rank < c.rank || (rank == c.rank && name < c.name) {
		c.name, c.rank = name, rank
	}
}

// builderExperimentLinks returns what the Experiment configs among configs
// say of the Builder publications that made them: one link for each whose
// [builderExperimentAnnotation] decodes and names a draft or a published
// document. Nothing of this is stored, so a renamed experiment is found under
// its new name and a deleted one is not found.
func builderExperimentLinks(configs store.Configs) []builderExperimentLink {
	links := make([]builderExperimentLink, 0, len(configs))

	for i := range configs {
		config := &configs[i]

		value, ok := config.Metadata.Annotations[builderExperimentAnnotation]
		if !ok || config.Kind != kindExperiment || config.Metadata.Name == "" {
			continue
		}

		var record builderExperimentPublication
		if err := json.Unmarshal([]byte(value), &record); err != nil {
			continue
		}

		if record.DraftID == "" && record.DocumentID == "" {
			continue
		}

		links = append(links, builderExperimentLink{
			name:       config.Metadata.Name,
			topology:   config.Metadata.Annotations["topology"],
			draftID:    record.DraftID,
			documentID: record.DocumentID,
		})
	}

	return links
}

// builderListExperimentLinks lists the Experiment configs with list, once,
// and returns their links (see [builderExperimentLinks]). The experiment is
// only an addition to a response: a listing that fails is logged and yields
// no links, so the experiment is left out of the response, which is never
// refused for it.
func builderListExperimentLinks(list func(kind string) (store.Configs, error)) []builderExperimentLink {
	configs, err := list(kindExperiment)
	if err != nil {
		plog.Error(plog.TypeSystem, "listing experiments for builder links", "err", err)

		return nil
	}

	return builderExperimentLinks(configs)
}

// builderDocumentExperiment names the experiment made by the publication
// behind a published document, or "" when there is none the role may get.
//
// An experiment answers when it is built from the topology the document was
// published to, and either records this very document, or records the draft
// that published it. The second case keeps the link when that draft later
// publishes the topology again without the experiment: the document is a new
// one then, and the experiment still names the old one. Of several, one that
// records the document is preferred.
func builderDocumentExperiment(
	role rbac.Role,
	links []builderExperimentLink,
	document *bapi.PublishedDocument,
) string {
	if document.Kind != builderKindTopology {
		return ""
	}

	var choice builderExperimentChoice

	for _, link := range links {
		byDocument := link.documentID != "" && link.documentID == document.ID
		byDraft := document.DraftID != "" && link.draftID == document.DraftID

		if link.topology != document.Target || (!byDocument && !byDraft) {
			continue
		}

		// An experiment the caller may not open is never named: another that
		// answers is used in its place.
		if !builderSourceGetAllowed(role, kindExperiment, link.name) {
			continue
		}

		rank := builderExperimentRankName
		if byDocument {
			rank = builderExperimentRankDocument
		}

		choice.offer(link.name, rank)
	}

	return choice.name
}

// builderDraftExperiment names the experiment a draft's publication made, or
// "" when there is none the role may get.
//
// An experiment answers when it records the draft itself, or one of its
// published documents (see [draftOwnsDocument]): the experiments the draft
// may update (see [experimentHoldsDraftPublication]). The digest the
// experiment records is not compared: one started or edited since is still
// the experiment. Of several, the one the draft's last publication named is
// preferred, then one that records the document of that publication. That
// name is only a preference, never the link: a renamed experiment is found by
// what it records.
func builderDraftExperiment(role rbac.Role, links []builderExperimentLink, meta *bapi.DraftMetadata) string {
	var (
		choice   builderExperimentChoice
		target   string
		document string
	)

	if meta.Publication != nil {
		target, document = meta.Publication.ExperimentTarget, meta.Publication.DocumentID
	}

	for _, link := range links {
		if link.draftID != meta.ID && !draftOwnsDocument(meta, link.documentID) {
			continue
		}

		if !builderSourceGetAllowed(role, kindExperiment, link.name) {
			continue
		}

		rank := builderExperimentRankName

		switch {
		case target != "" && link.name == target:
			rank = builderExperimentRankTarget
		case document != "" && link.documentID == document:
			rank = builderExperimentRankDocument
		}

		choice.offer(link.name, rank)
	}

	return choice.name
}

// draftExperiment names the experiment the draft's publication made, for a
// response that describes one draft. The experiments are listed only for a
// draft that could have one: it published, it forks a draft that had, or it
// was opened from a published document.
func (b *builderAPI) draftExperiment(actor builderActor, meta *bapi.DraftMetadata) string {
	if meta.Publication == nil && meta.Forked == nil && openedDocumentID(meta) == "" {
		return ""
	}

	return builderDraftExperiment(actor.role, builderListExperimentLinks(b.listConfigs), meta)
}
