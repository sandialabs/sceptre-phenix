// Publish intent helpers.
//
// Publish never uploads document bytes: the server loads the snapshot the
// draft cursor points at and re-runs its own validation. The editor only
// describes *what* to write, which is what these helpers build.

import { listOf } from './announce.js';
import { sentence } from './api.js';
import { LEGACY_ANNOTATION } from './configs.js';

// Config names the server accepts: metadata.name in the config schema
// (types/schema.go) and NameRegex in api/config.
const CONFIG_NAME = /^[A-Za-z0-9_@.-]+$/;

// The symbols are named as well as shown: screen readers at their default
// punctuation level do not speak "_" or "-".
export const CONFIG_NAME_RULE =
  'Names can use only letters, numbers, underscores (_), at signs (@), ' +
  'periods (.) and hyphens (-), with no spaces.';

/**
 * Turns a diagram name into a config name the server accepts: each run of
 * other characters becomes one dash, so "Untitled topology" becomes
 * "Untitled-topology". A name that is already valid is returned unchanged.
 *
 * @param {string} name
 * @returns {string}
 */
export function configName(name) {
  return String(name || '')
    .trim()
    .replace(/[^A-Za-z0-9_@.-]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

// How many of the characters a name may not use a reason names; it ends
// in "…" when the name has more.
const NAMED_CHARACTERS = 5;

// A character the naming rule allows.
const ALLOWED_CHARACTER = /^[A-Za-z0-9_@.-]$/;

function isSpace(character) {
  return /^\s$/.test(character);
}

// A character the naming rule refuses that is not a space.
function isRefused(character) {
  return !isSpace(character) && !ALLOWED_CHARACTER.test(character);
}

// A character that shows nothing of its own: a control or format character
// (U+200B, U+200E), a private-use, unassigned or surrogate code point, a
// mark that only changes the character before it, or one Unicode says to
// leave unseen (U+3164).
const UNSEEN_CHARACTER =
  /^[\p{C}\p{Mn}\p{Me}\p{Default_Ignorable_Code_Point}]$/u;

// How a reason names a character: quoted, or by its code point, such as
// U+200B, when quotes around it would look empty.
function named(character) {
  if (!UNSEEN_CHARACTER.test(character)) {
    return `"${character}"`;
  }

  const code = character.codePointAt(0).toString(16).toUpperCase();

  return `U+${code.padStart(4, '0')}`;
}

/**
 * Why a name breaks the naming rule: "it contains a space", "it contains
 * characters that are not allowed: "/", "#"", or both. Each character is
 * named once, in the order the name has them, and only the first five,
 * then "…": quoted, or by its code point (U+200B) when it shows nothing
 * of its own. '' for a name that keeps the rule, and for none.
 *
 * @param {string} name
 * @returns {string} "it contains a space and characters that are not
 *   allowed: "/"", or ''
 */
export function configNameReason(name) {
  const text = String(name || '');

  if (!text || CONFIG_NAME.test(text)) {
    return '';
  }

  const characters = [...text];
  const spaces = characters.filter(isSpace).length;
  const refused = [...new Set(characters.filter(isRefused))];
  const names = refused.slice(0, NAMED_CHARACTERS).map(named);

  if (refused.length > NAMED_CHARACTERS) {
    names.push('…');
  }

  const parts = [];

  if (spaces) {
    parts.push(spaces === 1 ? 'a space' : 'spaces');
  }

  if (refused.length) {
    parts.push(`characters that are not allowed: ${names.join(', ')}`);
  }

  return `it contains ${parts.join(' and ')}`;
}

/**
 * What a name field says under it while the name it holds breaks the
 * naming rule: why, then the rule. '' for a name that keeps the rule, and
 * for none, so a valid name shows no rule.
 *
 * @param {string} name the field's value
 * @returns {string}
 */
export function configNameHint(name) {
  const reason = configNameReason(String(name || '').trim());

  return reason
    ? `This name is not allowed: ${reason}. ${CONFIG_NAME_RULE}`
    : '';
}

/**
 * Why `name` cannot name a config of the given kind, or '' when it can. The
 * message names the field, why the name breaks the rule, the rule and a
 * valid alternative.
 *
 * @param {string} kind 'topology', 'experiment' or 'scenario'
 * @param {string} name trimmed name
 * @returns {string}
 */
export function configNameProblem(kind, name) {
  if (!name) {
    return `Enter a name for the ${kind}.`;
  }

  // phenix refuses to create an experiment of this name, in any case: it
  // means every experiment.
  if (kind === 'experiment' && name.toLowerCase() === 'all') {
    return `The experiment name "${name}" is reserved: phenix uses it to mean every experiment. Enter another name.`;
  }

  if (CONFIG_NAME.test(name)) {
    return '';
  }

  const suggestion = configName(name);

  return [
    `The ${kind} name "${name}" is not allowed: ${configNameReason(name)}.`,
    CONFIG_NAME_RULE,
    suggestion ? `For example: ${suggestion}` : '',
  ]
    .filter(Boolean)
    .join(' ');
}

/**
 * Chooses create or update from the configs the server already has.
 *
 * @param {string} name config name
 * @param {string[]} existing names already on the server
 * @returns {'create'|'update'}
 */
export function actionFor(name, existing = []) {
  const target = String(name || '').trim();
  const names = existing.map((entry) =>
    typeof entry === 'string' ? entry : entry?.name,
  );

  return names.includes(target) ? 'update' : 'create';
}

// Source tokens of a draft generated from an uploaded config, of one opened
// from a published diagram ("builder-doc/<document id>"), and of one opened
// from the diagram of the Builder file a topology names
// ("builder-file/<topology>/<digest>", with the digest of what the file
// held then).
const UPLOADED_TOKEN = 'uploaded/';
export const PUBLISHED_TOKEN = 'builder-doc/';
export const FILE_TOKEN = 'builder-file/';
// The token of a draft converted from a diagram of the legacy Builder that
// came without a topology. It is an upload's: the draft names no stored
// config, so it updates none.
export const LEGACY_TOKEN = `${UPLOADED_TOKEN}legacy-xml`;

// Whether the draft was imported from the stored topology `name` itself.
function importedFromTopology(name, draft = {}) {
  const { source } = draft;

  return (
    !String(draft.sourceToken || '').startsWith(UPLOADED_TOKEN) &&
    source?.kind === 'topology' &&
    source.name === name
  );
}

/**
 * Whether this draft may update the existing config `name`, as the server
 * decides (topologyUpdateRefusal and preflightExperiment in
 * web/builder_publish.go).
 *
 * A draft may update a topology that holds one of its own published
 * diagrams: one it published, whatever it was loaded from, the one it was
 * opened from, or one of exactly its saved snapshot. That is how it publishes
 * again after further edits. It may also update a topology whose diagram is
 * read from the Builder file it names, when the draft was opened from that
 * file. Otherwise it may update the topology it was generated from, unless
 * that was an upload, or the one its source experiment was built from (see
 * updateBlocker for a topology that has a legacy Builder diagram). The
 * server also refuses a topology changed by anyone else since the draft
 * published it, and one whose Builder file, or whose own spec, is no longer
 * what the draft was opened from. The client cannot see either: the server
 * says so when it refuses (see publishRefusal).
 *
 * An experiment is updated from its own source, unless that was an upload,
 * or when the draft last published it, with a topology that still holds that
 * publication. A draft saved as a new draft from another (forked) may also
 * update what that draft had published when it was forked, as though it
 * had published it. The server also refuses an experiment changed by anyone else
 * since the draft published it. It accepts an unchanged republish of an
 * experiment only when the stored spec still equals the draft's projection,
 * and creating an experiment fills in defaults, so the client cannot predict
 * that case and never promises it.
 *
 * @param {string} kind 'topology' or 'experiment'
 * @param {string} name existing config name
 * @param {object} draft source: the document's source; id, sourceToken,
 *   digest (of the saved snapshot), publication (the last one) and forked
 *   (the forked draft's), from the draft record; documents: the current
 *   published diagrams
 * @returns {boolean}
 */
export function draftCanUpdate(kind, name, draft = {}) {
  const { source, digest = '', documents = [], publication, forked } = draft;
  const token = String(draft.sourceToken || '');
  const uploaded = token.startsWith(UPLOADED_TOKEN);
  const opened = token.startsWith(PUBLISHED_TOKEN)
    ? token.slice(PUBLISHED_TOKEN.length)
    : '';

  // Whether a topology holds a published diagram of this draft's own. The
  // one a topology config points at is listed as current. A topology read
  // from its Builder file is listed with no document: it is the draft's own
  // when the draft was opened from that file.
  const holdsOwn = (topology) =>
    documents.some(
      (entry) =>
        entry?.kind === 'Topology' &&
        entry.target === topology &&
        ((draft.id && entry.draftId === draft.id) ||
          (publication?.documentId && entry.id === publication.documentId) ||
          (forked?.documentId && entry.id === forked.documentId) ||
          (opened && entry.id === opened) ||
          (digest && entry.digest === digest) ||
          (entry.source === 'file' &&
            token.startsWith(`${FILE_TOKEN}${topology}/`))),
    );

  if (kind === 'experiment') {
    return (
      (!uploaded && source?.kind === 'experiment' && source.name === name) ||
      [publication, forked].some(
        (published) =>
          Boolean(published?.experimentTarget) &&
          published.experimentTarget === name &&
          holdsOwn(published.topologyTarget),
      )
    );
  }

  if (holdsOwn(name)) {
    return true;
  }

  return (
    importedFromTopology(name, draft) ||
    (!uploaded && source?.kind === 'experiment' && source.topology === name)
  );
}

/**
 * Why this draft may not update the existing config `name`: 'changed' for
 * one the server refused because someone else has changed it since this
 * draft published it, 'source' when draftCanUpdate() says no, or '' when it
 * may.
 *
 * A topology that still has a diagram of the legacy Builder (a builder-xml
 * annotation) is updated only by the draft imported from that topology
 * itself: that import converted the diagram, which the update replaces. A
 * draft imported from an experiment built from the topology never held it
 * (topologyUpdateMatchesSource in web/builder_publish.go).
 *
 * @param {string} kind 'topology' or 'experiment'
 * @param {string} name existing config name
 * @param {Array} entries the server's configs of that kind, as names or
 *   source entries
 * @param {object} draft as draftCanUpdate() takes it, and changedTargets:
 *   the "<kind>/<name>" of each config the server refused so (see
 *   publishRefusal)
 * @returns {''|'changed'|'source'}
 */
export function updateBlocker(kind, name, entries = [], draft = {}) {
  if ((draft.changedTargets || []).includes(`${kind}/${name}`)) {
    return 'changed';
  }

  if (
    kind === 'topology' &&
    hasLegacyDiagram(name, entries) &&
    !importedFromTopology(name, draft)
  ) {
    return 'source';
  }

  return draftCanUpdate(kind, name, draft) ? '' : 'source';
}

/**
 * Whether the server's topology `name` still has a diagram of the legacy
 * Builder, which an update replaces.
 *
 * @param {string} name topology name
 * @param {Array} entries the server's topologies, as names or source
 *   entries
 * @returns {boolean}
 */
export function hasLegacyDiagram(name, entries = []) {
  return (entries || []).some(
    (entry) => entry?.name === name && entry.builder === LEGACY_ANNOTATION,
  );
}

// The warning of an import that could not read the legacy Builder diagram
// of its topology (FromLegacyTopology in types/builder/legacy.go), which the
// document's source keeps: nothing of that diagram is in the document.
const LEGACY_UNREAD = /^The legacy diagram of topology .+ could not be read \(/;

/**
 * What updating the server's topology `name` from this diagram does with
 * the topology's legacy Builder diagram: '' when it has none, 'replaced'
 * when the diagram was converted into this one, and 'removed' when the
 * import could not read it, so that nothing of it is kept (see
 * ReplaceLegacyDiagram in api/builder/publish.go).
 *
 * @param {string} name topology name
 * @param {Array} entries the server's topologies, as names or source
 *   entries
 * @param {object} [source] the document's source
 * @returns {''|'replaced'|'removed'}
 */
export function legacyDiagramUpdate(name, entries = [], source = null) {
  if (!hasLegacyDiagram(name, entries)) {
    return '';
  }

  const unread =
    source?.kind === 'topology' &&
    source.name === name &&
    (source.warnings || []).some((warning) => LEGACY_UNREAD.test(warning));

  return unread ? 'removed' : 'replaced';
}

function article(kind) {
  return kind === 'experiment' ? 'An' : 'A';
}

// Why an update is blocked (see updateBlocker), and what to do instead.
function blockedReason(kind, blocker) {
  const instead = `Enter another name to create a new ${kind}.`;

  if (blocker === 'changed') {
    return (
      'changed after this diagram published it, and publishing would overwrite that change. ' +
      `Import the ${kind} again to edit it as it is now, or enter another name to create a new ${kind}.`
    );
  }

  const loaded =
    kind === 'topology'
      ? 'the diagram was not imported from it, opened from its published diagram or published to it'
      : 'the diagram was not imported from it or published to it';

  return `already exists, and this diagram cannot update it: ${loaded}. ${instead}`;
}

/**
 * The hint under a config name field: what publishing does with that name.
 *
 * @param {string} kind 'topology' or 'experiment'
 * @param {boolean} exists a config of that name exists
 * @param {string} [blocker] updateBlocker() for it
 * @param {boolean|string} [legacy] the topology of that name has a diagram
 *   of the legacy Builder (see hasLegacyDiagram), which the update
 *   replaces, or removes when it is 'removed' (see legacyDiagramUpdate)
 * @returns {string}
 */
export function targetHint(kind, exists, blocker = '', legacy = false) {
  if (!exists) {
    return `A new ${kind} will be created.`;
  }

  if (blocker) {
    return `${article(kind)} ${kind} with this name ${blockedReason(kind, blocker)}`;
  }

  const updated = `${article(kind)} ${kind} with this name exists and will be updated.`;

  if (!legacy || kind !== 'topology') {
    return updated;
  }

  return legacy === 'removed'
    ? `${updated} Its legacy Builder diagram could not be read and is removed.`
    : `${updated} Its legacy Builder diagram is replaced by this diagram.`;
}

/**
 * The name of the button that publishes, which says what it does, so
 * overwriting an existing config is stated on the control that does it:
 * "Update topology", "Create topology and update experiment".
 *
 * @param {'create'|'update'} topology what happens to the topology
 * @param {'create'|'update'} [experiment] and to the experiment, when one
 *   is published
 * @returns {string}
 */
export function publishLabel(topology, experiment) {
  const first = topology === 'update' ? 'Update' : 'Create';

  if (!experiment) {
    return `${first} topology`;
  }

  const second = experiment === 'update' ? 'update' : 'create';

  return second === first.toLowerCase()
    ? `${first} topology and experiment`
    : `${first} topology and ${second} experiment`;
}

// How many included topologies a sentence names before it counts the rest,
// as the server's import warning does (maxListedIncludes in
// types/builder/generate.go).
const NAMED_INCLUDES = 8;

/**
 * What the Publish dialog's summary adds for a diagram that includes
 * topologies and shows none of their nodes: one combined on import whose
 * includes could not all be read, or one whose includes were never
 * resolved. Publishing writes them to the topology's includeTopologies.
 *
 * @param {string[]} [includes] the diagram's source.includeTopologies
 * @returns {string} " The published topology also includes site-b by
 *   reference.", with its leading space, or '' when there are none
 */
export function keptIncludesText(includes = []) {
  const names = (Array.isArray(includes) ? includes : []).filter(Boolean);

  if (names.length === 0) {
    return '';
  }

  const more = names.length - NAMED_INCLUDES;
  const named =
    more > 0 ? [...names.slice(0, NAMED_INCLUDES), `${more} more`] : names;

  return ` The published topology also includes ${listOf(named)} by reference.`;
}

/**
 * The confirmation Publish asks for before it replaces configs on the
 * server, which no one can undo: phenix keeps no earlier version of a
 * config. It names each config replaced; an intent that only creates, or
 * uses a stored scenario as it is, needs none. Its button repeats the
 * publish label, adding the scenario when that is replaced too.
 *
 * @param {object} intent as buildPublishIntent() builds it
 * @returns {{title: string, message: string, confirmLabel: string}|null}
 */
export function overwriteConfirmation(intent) {
  const replaced = ['topology', 'scenario', 'experiment']
    .filter((kind) => intent?.[kind]?.action === 'update')
    .map((kind) => ({ kind, name: intent[kind].name }));

  if (replaced.length === 0) {
    return null;
  }

  return {
    title: `Replace ${listOf(replaced.map(({ kind, name }) => `${kind} ${name}`))}?`,
    message:
      `Publishing replaces ${listOf(replaced.map(({ kind, name }) => `the ${kind} "${name}"`))} ` +
      'on the server with this diagram. This cannot be undone.',
    confirmLabel:
      publishLabel(intent.topology.action, intent.experiment?.action) +
      (intent.scenario?.action === 'update' ? ', replace scenario' : ''),
  };
}

/**
 * The refusal of an existing config name this draft may not update.
 *
 * @param {string} kind 'topology' or 'experiment'
 * @param {string} name
 * @param {string} [blocker] updateBlocker() for it
 * @returns {string}
 */
function updateProblem(kind, name, blocker = 'source') {
  return blocker === 'source'
    ? `${article(kind)} ${kind} named "${name}" ${blockedReason(kind, blocker)}`
    : `The ${kind} "${name}" ${blockedReason(kind, blocker)}`;
}

/**
 * Names of the scenarios the server offers, from either a list of strings or a
 * list of source entries.
 *
 * @param {Array} scenarios
 * @returns {string[]}
 */
export function scenarioNames(scenarios = []) {
  return scenarios
    .map((entry) => (typeof entry === 'string' ? entry : entry?.name))
    .filter(Boolean);
}

/**
 * Builds the publish intent for a form.
 *
 * A stored scenario is used as it is on the server (`use`). An uploaded
 * scenario has no config yet, so the user has to say explicitly whether it
 * creates a new config or updates an existing one; there is no default.
 *
 * @param {object} form mode, topologyName, experimentName, scenarioName,
 *   scenarioAction
 * An existing topology or experiment is updated, which the server allows only
 * when updateBlocker() finds nothing in the way; any other existing name is
 * refused here, before anything is sent.
 *
 * @param {object} form mode, topologyName, experimentName, scenarioName,
 *   scenarioAction
 * @param {object} context scenario (document ref), topologies, experiments,
 *   scenarios, and draft: what draftCanUpdate() needs to know about the draft
 * @returns {{intent: object|null, error: string, field: string}} `field` is
 *   the form field the error is about: topologyName, experimentName,
 *   scenarioName, scenarioAction, or '' for none
 */
export function buildPublishIntent(form = {}, context = {}) {
  const fail = (error, field = '') => ({ intent: null, error, field });
  const mode = form.mode === 'topology-experiment' ? form.mode : 'topology';
  const topologyName = String(form.topologyName || '').trim();
  const topologyProblem = configNameProblem('topology', topologyName);

  if (topologyProblem) {
    return fail(topologyProblem, 'topologyName');
  }

  const intent = {
    mode,
    topology: {
      name: topologyName,
      action: actionFor(topologyName, context.topologies),
    },
  };

  const topologyBlocker =
    intent.topology.action === 'update'
      ? updateBlocker(
          'topology',
          topologyName,
          context.topologies,
          context.draft,
        )
      : '';

  if (topologyBlocker) {
    return fail(
      updateProblem('topology', topologyName, topologyBlocker),
      'topologyName',
    );
  }

  if (mode === 'topology-experiment') {
    const scenario = context.scenario;

    if (scenario?.kind === 'stored') {
      intent.scenario = { name: scenario.name, action: 'use' };
    } else if (scenario) {
      const scenarioName = String(form.scenarioName || '').trim();
      const scenarioAction = form.scenarioAction;
      const scenarioProblem = configNameProblem('scenario', scenarioName);

      if (scenarioProblem) {
        return fail(scenarioProblem, 'scenarioName');
      }

      if (!['create', 'update'].includes(scenarioAction)) {
        return fail(
          'Choose whether the uploaded scenario creates a new config or updates an existing one.',
          'scenarioAction',
        );
      }

      intent.scenario = { name: scenarioName, action: scenarioAction };

      if (scenarioAction === 'update') {
        const existing = (context.scenarios || []).find(
          (entry) =>
            (typeof entry === 'string' ? entry : entry?.name) === scenarioName,
        );
        const expectedDigest =
          typeof existing === 'string' ? '' : existing?.digest;

        // The server lists every scenario with its digest, so a missing
        // digest means there is no scenario of that name to update.
        if (!expectedDigest) {
          return fail(
            `There is no scenario named "${scenarioName}" to update. ` +
              'Choose "Create a new scenario", or enter the name of an existing scenario.',
            'scenarioAction',
          );
        }

        intent.scenario.expectedDigest = expectedDigest;
      }
    }

    const name = String(form.experimentName || '').trim();
    const experimentProblem = configNameProblem('experiment', name);

    if (experimentProblem) {
      return fail(experimentProblem, 'experimentName');
    }

    intent.experiment = { name, action: actionFor(name, context.experiments) };

    const experimentBlocker =
      intent.experiment.action === 'update'
        ? updateBlocker('experiment', name, context.experiments, {
            ...context.draft,
            topologyName,
            scenarioName: intent.scenario?.name,
          })
        : '';

    if (experimentBlocker) {
      return fail(
        updateProblem('experiment', name, experimentBlocker),
        'experimentName',
      );
    }
  }

  return { intent, error: '', field: '' };
}

// The server's refusal of a create or update that does not match the configs
// it has: the list the dialog chose the action from has changed since.
// serverReason() makes each reason a sentence, so the patterns allow for its
// capital and full stop.
const STALE_ACTION =
  /^config (\S+) (already exists|does not exist); choose (?:update|create) explicitly\.?$/i;

// The server's refusal of an update draftCanUpdate() expected it to accept,
// for example because the config was republished from another diagram since
// the list of published diagrams was read.
const NOT_SOURCE =
  /^(topology|experiment) (\S+) is not the source this draft was loaded from\.?$/i;

// The server's refusal of an update to a topology or experiment this draft
// published, which someone else has changed since: publishing would
// overwrite that.
const CHANGED_SINCE =
  /^(topology|experiment) (\S+) changed after this draft published it\.?$/i;

// A refusal that names its config ("running experiment lab cannot be
// updated", "experiment lab is not valid").
const NAMED_TARGET =
  /^(?:(?:stored|uploaded|running) )?(topology|scenario|experiment) (\S+) /i;

// The server checks the targets in this order and reports the first refusal.
const TARGET_KINDS = ['topology', 'scenario', 'experiment'];

// The form field of each target, in buildPublishIntent's terms. A stored
// scenario has no field in the Publish form.
function targetField(kind, intent = {}) {
  if (kind === 'topology') {
    return 'topologyName';
  }

  if (kind === 'experiment') {
    return 'experimentName';
  }

  return kind === 'scenario' && intent.scenario?.action !== 'use'
    ? 'scenarioName'
    : '';
}

function staleActionRefusal(kind, intent, exists, context) {
  const { name, action } = intent[kind];

  if (kind === 'scenario' && action === 'use') {
    return {
      message: `The stored scenario "${name}" no longer exists. Choose another one in the Scenario dialog.`,
      field: '',
    };
  }

  // An uploaded scenario's action is chosen in the form, not from the list.
  if (kind === 'scenario') {
    return {
      message: exists
        ? `A scenario named "${name}" already exists. Choose "Update the existing scenario", or enter another name.`
        : `There is no scenario named "${name}" to update. Choose "Create a new scenario", or enter another name.`,
      field: 'scenarioAction',
    };
  }

  if (!exists) {
    return {
      message: `The ${kind} "${name}" no longer exists, so publishing again creates it.`,
      field: targetField(kind),
    };
  }

  const entries =
    kind === 'topology' ? context.topologies : context.experiments;
  const blocker = updateBlocker(kind, name, entries, {
    ...context.draft,
    topologyName: intent.topology?.name,
    scenarioName: intent.scenario?.name,
  });

  return {
    message: blocker
      ? updateProblem(kind, name, blocker)
      : `${article(kind)} ${kind} named "${name}" already exists. Publish again to update it, or enter another name to create a new ${kind}.`,
    field: targetField(kind),
  };
}

/**
 * A publish the server refused (409), as a message for the dialog and the
 * form field it is about. The server names the config it refused. When it
 * refuses a create or update because the config does or does not exist, the
 * dialog chose the action from a list that is out of date, and the message
 * says what publishing again will do. When it refuses an update this draft
 * may not make, the message says so and asks for another name.
 *
 * @param {string} reason the server's reason, from serverReason()
 * @param {object} intent the refused intent
 * @param {object} [context] topologies, experiments and draft, as
 *   buildPublishIntent takes them, read again after the refusal
 * @returns {{message: string, field: string, changed?: string}} `field` as
 *   in buildPublishIntent, or '' for none; `changed`, the "<kind>/<name>" of
 *   a config refused because someone else changed it since this draft
 *   published it
 */
export function publishRefusal(reason, intent = {}, context = {}) {
  const text = String(reason || '').trim();
  const stale = STALE_ACTION.exec(text);

  if (stale) {
    const [, name, state] = stale;
    const exists = state.toLowerCase() === 'already exists';
    // "config core already exists" does not say which config: it is the first
    // target of that name whose action the refusal is about.
    const kind = TARGET_KINDS.find(
      (candidate) =>
        intent[candidate]?.name === name &&
        (intent[candidate].action === 'create') === exists,
    );

    if (kind) {
      return staleActionRefusal(kind, intent, exists, context);
    }
  }

  const notSource = NOT_SOURCE.exec(text);
  const refusedKind = notSource?.[1].toLowerCase();

  if (refusedKind && intent[refusedKind]?.name === notSource[2]) {
    return {
      message: updateProblem(refusedKind, notSource[2]),
      field: targetField(refusedKind),
    };
  }

  const changed = CHANGED_SINCE.exec(text);
  const changedKind = changed?.[1].toLowerCase();

  // The dialog keeps the name blocked from now on (see updateBlocker).
  if (changedKind && intent[changedKind]?.name === changed[2]) {
    return {
      message: updateProblem(changedKind, changed[2], 'changed'),
      field: targetField(changedKind),
      changed: `${changedKind}/${changed[2]}`,
    };
  }

  const named = NAMED_TARGET.exec(text);
  const kind = named?.[1].toLowerCase();

  return {
    message:
      sentence(text) ||
      'A config this publish would write has changed on the server. Try again.',
    field:
      kind && intent[kind]?.name === named[2] ? targetField(kind, intent) : '',
  };
}

/**
 * The diagram's checks as the Publish dialog lists them: a warning about
 * what publishing refuses, an interface with no VLAN, an address two
 * interfaces use or a hostname phenix refuses (see collectWarnings in
 * validate.js), is an error there. The draft still saves with it, as the
 * checks elsewhere say, but it is not published: phenix would refuse the
 * hostname, or store the topology, and minimega refuse the interface, or the
 * addresses clash, when the experiment starts.
 *
 * @param {object[]} issues validateDocument() issues
 * @returns {object[]} the issues, each one that blocks publishing an error
 */
export function publishChecks(issues = []) {
  return issues.map((issue) =>
    issue.blocksPublish && issue.level !== 'error'
      ? { ...issue, level: 'error' }
      : issue,
  );
}

/**
 * Sentence describing a publish result, including a partial failure.
 *
 * @param {object} result normalized publish result
 * @returns {string}
 */
export function describePublishResult(result) {
  if (!result) {
    return '';
  }

  if (result.ok) {
    return 'Published. Every stage succeeded.';
  }

  return result.partial
    ? 'Published with failures. Some configs were written; the failed stages are listed below.'
    : 'Publish failed. No configs were written.';
}

/**
 * @param {object} stage publish stage result
 * @returns {boolean}
 */
export function stageFailed(stage) {
  return ['failed', 'error'].includes(stage?.status);
}
