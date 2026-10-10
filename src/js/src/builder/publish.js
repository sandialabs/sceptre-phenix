// Publish intent helpers.
//
// Publish never uploads document bytes. The server loads the snapshot that
// the draft cursor points at and runs its own validation again. The editor
// only describes *what* to write, and these helpers build that description.

import { count, listOf } from './announce.js';
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

// How many of the refused characters of a name a reason names. The reason
// ends in "…" when the name has more.
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
// mark that only changes the character before it, or a character that
// Unicode says not to show (U+3164).
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
 * named once, in the order of the name, and only the first five, then "…".
 * A character is quoted, or named by its code point (U+200B) when it shows
 * nothing of its own. '' for a name that keeps the rule, and for no name.
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
 * for no name, so a valid name shows no rule.
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

  // phenix refuses to create an experiment of this name, in any letter
  // case, because it means every experiment.
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
 * A draft may update a topology in these cases:
 * - The topology holds one of the draft's own published diagrams: a diagram
 *   it published (whatever it was loaded from), the diagram it was opened
 *   from, or a diagram of exactly its saved snapshot. That is how a draft
 *   publishes again after more edits.
 * - The topology's diagram is read from the Builder file it names, and the
 *   draft was opened from that file.
 * - The draft was generated from the topology, unless that was an upload.
 * - The draft's source experiment was built from the topology (see
 *   updateBlocker for a topology that has a legacy Builder diagram).
 * The server also refuses a topology that anyone else changed after the
 * draft published it. It also refuses a topology whose Builder file, or
 * whose own spec, is no longer what the draft was opened from. The client
 * cannot see either case. The server says so when it refuses (see
 * publishRefusal).
 *
 * A draft may update an experiment in these cases:
 * - The experiment is the draft's own source, unless that was an upload.
 * - The draft last published it, with a topology that still holds that
 *   publication.
 * A draft saved as a new draft from another draft (forked) may also update
 * what that draft had published when it was forked, as though it had
 * published it. The server also refuses an experiment that anyone else
 * changed after the draft published it. The server accepts an unchanged
 * republish of an experiment only when the stored spec still equals the
 * draft's projection. The creation of an experiment adds defaults. So the
 * client cannot predict that case, and never promises it.
 *
 * @param {string} kind 'topology' or 'experiment'
 * @param {string} name existing config name
 * @param {object} draft source: the document's source. id, sourceToken,
 *   digest (of the saved snapshot), publication (the last one) and forked
 *   (the forked draft's): from the draft record. documents: the current
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
 * Why this draft may not update the existing config `name`:
 * - 'changed': the server refused it because someone else changed it after
 *   this draft published it
 * - 'source': draftCanUpdate() says no
 * - '': the draft may update it
 *
 * A topology that still has a diagram of the legacy Builder (a builder-xml
 * annotation) is updated only by the draft imported from that topology
 * itself. That import converted the diagram, which the update replaces. A
 * draft imported from an experiment built from the topology never held it
 * (topologyUpdateMatchesSource in web/builder_publish.go).
 *
 * @param {string} kind 'topology' or 'experiment'
 * @param {string} name existing config name
 * @param {Array} entries the server's configs of that kind, as names or
 *   source entries
 * @param {object} draft as draftCanUpdate() takes it, and changedTargets:
 *   the "<kind>/<name>" of each config the server refused for that reason
 *   (see publishRefusal)
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
// of its topology (FromLegacyTopology in types/builder/legacy.go). The
// document's source keeps this warning. Nothing of that diagram is in the
// document.
const LEGACY_UNREAD = /^The legacy diagram of topology .+ could not be read \(/;

/**
 * What an update of the server's topology `name` from this diagram does
 * with the topology's legacy Builder diagram (see ReplaceLegacyDiagram in
 * api/builder/publish.go):
 * - '': the topology has none
 * - 'replaced': the diagram was converted into this one
 * - 'removed': the import could not read it, so nothing of it is kept
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
 * The name of the button that publishes. The name says what the button
 * does, so the control that overwrites an existing config says so:
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
 * topologies and shows none of their nodes. Such a diagram was combined on
 * import but not all its includes could be read, or its includes were never
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
 * config. It names each config replaced. An intent that only creates
 * configs needs no confirmation. The scenarios the diagram lists are never
 * replaced: publishing only adds the topology to their topology
 * annotation. Its button repeats the publish label.
 *
 * @param {object} intent as buildPublishIntent() builds it
 * @returns {{title: string, message: string, confirmLabel: string}|null}
 */
export function overwriteConfirmation(intent) {
  const replaced = ['topology', 'experiment']
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
    confirmLabel: publishLabel(
      intent.topology.action,
      intent.experiment?.action,
    ),
  };
}

/**
 * What the Publish dialog says of the scenarios the diagram lists: that
 * publishing adds the topology to each, in either mode.
 *
 * @param {string[]} [names] the scenarios the document lists
 * @returns {string} '' when it lists none
 */
export function scenarioStageHint(names = []) {
  if (names.length === 0) {
    return '';
  }

  const which =
    names.length === 1
      ? `the scenario ${names[0]}`
      : `each of the scenarios ${listOf(names)}`;

  return `Publishing adds this topology to the topology annotation of ${which}, so experiments of the topology can use it.`;
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

// The names of a comma-separated topology annotation, trimmed, as the
// server compares them (hasTopologyAnnotation in builder_publish.go).
function topologyNames(value) {
  const names = value.split(',').map((name) => name.trim());

  return names.filter(Boolean);
}

/**
 * Merges two topology annotations of a scenario (comma-separated topology
 * names): the stored one byte for byte, then each name of the uploaded one
 * the stored one does not name, after a comma, as the server adds a
 * topology on publish (addTopologyAnnotation in builder_publish.go). No
 * topology the stored annotation names is lost.
 *
 * @param {string} stored
 * @param {string} uploaded
 * @returns {string}
 */
export function mergeTopologyAnnotation(stored, uploaded) {
  const named = new Set(topologyNames(stored));
  let merged = stored;

  topologyNames(uploaded).forEach((name) => {
    if (named.has(name)) {
      return;
    }

    named.add(name);
    merged = merged === '' ? name : `${merged},${name}`;
  });

  return merged;
}

// The annotations of a config's metadata, or none when it has no object
// of them.
function annotationsOf(config) {
  const annotations = config?.metadata?.annotations;
  const object =
    Boolean(annotations) &&
    typeof annotations === 'object' &&
    !Array.isArray(annotations);

  return object ? annotations : {};
}

/**
 * The Scenario config that replaces a stored one with an uploaded one: the
 * uploaded config's apiVersion, kind, name and spec, and the stored
 * config's annotations with the uploaded config's over them. The topology
 * annotations merge (mergeTopologyAnnotation), since phenix creates an
 * experiment from a scenario only while that annotation names the
 * experiment's topology.
 *
 * @param {object} stored the config the server holds (GET /configs)
 * @param {object} uploaded the config read from the file
 * @returns {object}
 */
export function replacedScenarioConfig(stored, uploaded) {
  const kept = annotationsOf(stored);
  const given = annotationsOf(uploaded);
  const annotations = { ...kept, ...given };
  const { topology } = kept;

  if (typeof topology === 'string' && typeof given.topology === 'string') {
    annotations.topology = mergeTopologyAnnotation(topology, given.topology);
  }

  const { name } = uploaded.metadata;

  return {
    ...uploaded,
    metadata: Object.keys(annotations).length
      ? { name, annotations }
      : { name },
  };
}

/**
 * Builds the publish intent for a form.
 *
 * An existing topology or experiment is updated. The server allows this
 * only when updateBlocker() finds no block. Any other existing name is
 * refused here, before anything is sent. With an experiment, the scenario
 * it is published with is one of the scenarios the document lists, by
 * name, or none. The server adds the topology to every listed scenario in
 * both cases.
 *
 * @param {object} form mode, topologyName, experimentName, scenarioName (''
 *   for no scenario)
 * @param {object} context scenarios (the names the document lists),
 *   topologies, experiments, and draft: what draftCanUpdate() needs to know
 *   about the draft
 * @returns {{intent: object|null, error: string, field: string}} `field` is
 *   the form field the error is about: topologyName, experimentName,
 *   scenarioName, or '' for none
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
    const scenarioName = String(form.scenarioName || '').trim();

    if (scenarioName) {
      if (!(context.scenarios || []).includes(scenarioName)) {
        return fail(
          `The scenario "${scenarioName}" is not one of this diagram's scenarios. ` +
            'Choose one of them, or No scenario.',
          'scenarioName',
        );
      }

      intent.scenario = { name: scenarioName };
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

// What the codes of the server's publish refusals (codes.go) say: the kind
// of config a refusal is about, and for some codes, the rule that the
// dialog explains in its own words:
// - `stale`: a create or update that does not match the configs the server
//   has (the list the dialog chose the action from changed after it was
//   read), with whether the config exists.
// - `source`: an update that draftCanUpdate() expected the server to
//   accept. For example, the config was republished from another diagram
//   after the list of published diagrams was read.
// - `changed`: an update of a config this draft published, which someone
//   else changed after that. Publishing would overwrite that change.
// The code of any other publish refusal names its kind by its second word
// (publish.experiment.running). The refusals of the topology as a whole
// (what only publishing refuses, and a hostname that an included topology
// also defines) are about the topology. The refusal of an included
// topology's hostnames is about the experiment.
const REFUSAL_RULES = {
  'publish.topology.exists': { kind: 'topology', stale: true, exists: true },
  'publish.topology.missing': { kind: 'topology', stale: true, exists: false },
  'publish.experiment.exists': {
    kind: 'experiment',
    stale: true,
    exists: true,
  },
  'publish.experiment.missing': {
    kind: 'experiment',
    stale: true,
    exists: false,
  },
  'publish.topology.not-source': { kind: 'topology', source: true },
  'publish.experiment.not-source': { kind: 'experiment', source: true },
  'publish.topology.changed': { kind: 'topology', changed: true },
  'publish.experiment.changed': { kind: 'experiment', changed: true },
  'publish.blocked': { kind: 'topology' },
  'publish.include.clash': { kind: 'topology' },
  'publish.include.hostname': { kind: 'experiment' },
};

// The kinds of config a publish refusal's code may name by its second word.
const REFUSAL_KINDS = ['topology', 'experiment', 'scenario'];

// What a publish refusal's code says, as REFUSAL_RULES has it: the kind of
// config it is about ('' for none) and its rule.
function refusalRule(code) {
  if (REFUSAL_RULES[code]) {
    return REFUSAL_RULES[code];
  }

  const [scope, kind] = String(code || '').split('.');

  return {
    kind: scope === 'publish' && REFUSAL_KINDS.includes(kind) ? kind : '',
  };
}

// The form field that a refusal of the config of kind `kind` and name
// `name` is about, in buildPublishIntent's terms. A scenario has a field
// only when it is the experiment's scenario, which the form picks.
function targetField(kind, intent = {}, name = '') {
  if (kind === 'topology') {
    return 'topologyName';
  }

  if (kind === 'experiment') {
    return 'experimentName';
  }

  return kind === 'scenario' && name && intent.scenario?.name === name
    ? 'scenarioName'
    : '';
}

function staleActionRefusal(kind, intent, exists, context) {
  const { name } = intent[kind];

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
 * A publish the server refused (409 or 422), as a message for the dialog and
 * the form field it is about. The refusal's code selects both. When the
 * server refuses a create or update because the config does or does not
 * exist, the dialog chose the action from an out-of-date list. The message
 * then says what publishing again will do. When the server refuses an
 * update that this draft may not make, the message says so and asks for
 * another name. Any other refusal keeps the server's reason, on the field
 * of the config its code is about. A scenario gets the refusal on its
 * field only when it is the experiment's scenario.
 *
 * @param {{code?: string, reason?: string, scenario?: string}} refusal as
 *   serverRefusal() reads it: the server's code, its reason, and the
 *   scenario a refusal about one of the draft's scenarios names
 * @param {object} intent the refused intent
 * @param {object} [context] topologies, experiments and draft, as
 *   buildPublishIntent takes them, read again after the refusal
 * @returns {{message: string, field: string, changed?: string}} `field`: as
 *   in buildPublishIntent, or '' for none. `changed`: the "<kind>/<name>"
 *   of a config refused because someone else changed it after this draft
 *   published it
 */
export function publishRefusal(refusal = {}, intent = {}, context = {}) {
  const { code = '', reason = '', scenario = '' } = refusal || {};
  const text = String(reason || '').trim();
  const rule = refusalRule(code);
  const name = intent[rule.kind]?.name;

  if (rule.stale && name) {
    return staleActionRefusal(rule.kind, intent, rule.exists, context);
  }

  if (rule.source && name) {
    return {
      message: updateProblem(rule.kind, name),
      field: targetField(rule.kind),
    };
  }

  // The dialog keeps the name blocked from now on (see updateBlocker).
  if (rule.changed && name) {
    return {
      message: updateProblem(rule.kind, name, 'changed'),
      field: targetField(rule.kind),
      changed: `${rule.kind}/${name}`,
    };
  }

  const field =
    rule.kind === 'scenario'
      ? targetField(rule.kind, intent, scenario)
      : name
        ? targetField(rule.kind, intent)
        : '';

  return {
    message:
      sentence(text) ||
      'A config this publish would write has changed on the server. Try again.',
    field,
  };
}

/**
 * The diagram's checks as the Publish dialog lists them. There, a warning
 * about what publishing refuses is an error: an interface with no VLAN, an
 * address that two interfaces use, or a hostname that phenix refuses (see
 * collectWarnings in validate.js). The draft still saves with it, as the
 * checks elsewhere say, but it is not published. Otherwise phenix would
 * refuse the hostname, or store the topology. Then, when the experiment
 * starts, minimega would refuse the interface, or the addresses would
 * clash.
 *
 * @param {object[]} issues validateDocument() issues
 * @returns {object[]} the issues. Each issue that blocks publishing is an
 *   error, in both `level` and `severity`, with its code
 */
export function publishChecks(issues = []) {
  return issues.map((issue) =>
    issue.blocksPublish && issue.level !== 'error'
      ? { ...issue, level: 'error', severity: 'error' }
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

// --- What publishing changes --------------------------------------------------
//
// The Publish dialog asks the server what publishing would change (a dry
// run, see DescribePublishChanges in api/builder/changes.go) and says it in
// these words. Text carries the meaning: the words say what is added,
// removed or kept, not a color.

// How many devices a disk image's line names before it counts the rest.
const NAMED_DEVICES = 5;

/**
 * What publishing does to a config.
 *
 * @param {'Topology'|'Experiment'} kind
 * @param {{name: string, action: string}} change action 'create', 'update'
 *   or 'unchanged'
 * @returns {string} "Creates Topology config riverside"
 */
export function configChangeText(kind, change) {
  const { name = '', action = '' } = change || {};

  if (action === 'create') {
    return `Creates ${kind} config ${name}`;
  }

  if (action === 'update') {
    return `Updates ${kind} config ${name}`;
  }

  return `${kind} config ${name} is unchanged: it already holds this diagram`;
}

/**
 * What publishing does to a topology the Topology config includes.
 *
 * @param {{name: string, change: string}} entry change 'added', 'removed'
 *   or 'kept'
 * @returns {string} "Removes included topology plant-a"
 */
export function includeChangeText({ name, change } = {}) {
  if (change === 'added') {
    return `Adds included topology ${name}`;
  }

  return change === 'removed'
    ? `Removes included topology ${name}`
    : `Keeps included topology ${name}`;
}

/**
 * What publishing does to a Scenario config the diagram lists.
 *
 * @param {{name: string, change: string}} entry change 'annotate' or
 *   'unchanged'
 * @param {string} topology the topology's name
 * @returns {string} "Adds topology riverside to Scenario water-ops"
 */
export function scenarioChangeText({ name, change } = {}, topology = '') {
  return change === 'annotate'
    ? `Adds topology ${topology} to Scenario ${name}`
    : `Scenario ${name} already names topology ${topology}`;
}

// The devices of a disk image's line: the first few, then how many more.
function devicesText(devices = []) {
  const more = devices.length - NAMED_DEVICES;

  return listOf(
    more > 0 ? [...devices.slice(0, NAMED_DEVICES), `${more} more`] : devices,
  );
}

/**
 * What publishing does to a disk image the topology's devices use, and
 * whether the server has it, when that is known.
 *
 * @param {{name: string, change: string, devices: string[],
 *   onServer: boolean|null}} entry change 'added', 'removed' or 'kept'
 * @returns {string} "Disk image ubuntu.qc2 is new (used by web-1 and
 *   web-2); the server does not have it"
 */
export function imageChangeText({
  name,
  change,
  devices = [],
  onServer = null,
} = {}) {
  const used = devicesText(devices);

  if (change === 'removed') {
    return `Disk image ${name} is no longer used (was used by ${used})`;
  }

  const line =
    change === 'added'
      ? `Disk image ${name} is new (used by ${used})`
      : `Disk image ${name} is still used (by ${used})`;

  if (typeof onServer !== 'boolean') {
    return line;
  }

  return onServer
    ? `${line}; the server has it`
    : `${line}; the server does not have it`;
}

/**
 * What publishing does to the VLAN alias of a network of the experiment.
 *
 * @param {{name: string, from: number|null, to: number|null,
 *   change: string}} entry change 'added', 'removed', 'changed' or 'kept'
 * @returns {string} "VLAN alias for network ot changes from 101 to 120"
 */
export function aliasChangeText({ name, from, to, change } = {}) {
  switch (change) {
    case 'added':
      return `VLAN alias for network ${name} is set to ${to}`;
    case 'removed':
      return `VLAN alias ${from} for network ${name} is removed`;
    case 'changed':
      return `VLAN alias for network ${name} changes from ${from} to ${to}`;
    default:
      return `VLAN alias for network ${name} stays ${to}`;
  }
}

/**
 * How many items of the lists outside the configs (included topologies,
 * scenarios, disk images and VLAN aliases) the changes name.
 *
 * @param {object} [changes] as readPublishPreview in api.js reads them
 * @returns {number}
 */
export function changedItems(changes) {
  if (!changes) {
    return 0;
  }

  return ['includes', 'scenarios', 'images', 'vlanAliases'].reduce(
    (total, key) => total + (changes[key] || []).length,
    0,
  );
}

/**
 * The Publish dialog's lists of what publishing changes, one per kind of
 * change that has lines: the configs, included topologies, scenarios, disk
 * images and VLAN aliases.
 *
 * @param {object} [changes] as readPublishPreview in api.js reads them
 * @returns {{key: string, title: string, lines: string[]}[]}
 */
export function publishChangeGroups(changes) {
  if (!changes) {
    return [];
  }

  const topology = changes.topology?.name || '';
  const groups = [
    {
      key: 'configs',
      title: 'Configs',
      lines: [
        changes.topology && configChangeText('Topology', changes.topology),
        changes.experiment &&
          configChangeText('Experiment', changes.experiment),
      ].filter(Boolean),
    },
    {
      key: 'includes',
      title: 'Included topologies',
      lines: (changes.includes || []).map(includeChangeText),
    },
    {
      key: 'scenarios',
      title: 'Scenarios',
      lines: (changes.scenarios || []).map((entry) =>
        scenarioChangeText(entry, topology),
      ),
    },
    {
      key: 'images',
      title: 'Disk images',
      lines: (changes.images || []).map(imageChangeText),
    },
    {
      key: 'aliases',
      title: 'VLAN aliases',
      lines: (changes.vlanAliases || []).map(aliasChangeText),
    },
  ];

  return groups.filter((group) => group.lines.length > 0);
}

/**
 * What the Publish dialog announces when its list of changes is read again:
 * what publishing does to the configs, how many more lines follow, and how
 * many warnings the server listed with them.
 *
 * @param {object} [changes] as readPublishPreview in api.js reads them
 * @param {number} [warnings] how many warnings the dry run answered with
 * @returns {string}
 */
export function publishChangesSummary(changes, warnings = 0) {
  const [configs] = publishChangeGroups(changes);
  const lines = configs?.key === 'configs' ? configs.lines : [];
  const more = changedItems(changes);
  const said = lines.length ? `${lines.join('. ')}.` : '';
  const rest = more
    ? ` ${count(more, 'more line')} below.`
    : ' Nothing outside the Topology changes.';
  const warned = warnings > 0 ? ` ${count(warnings, 'warning')} below.` : '';

  return `What publishing changes: ${said}${rest}${warned}`.replace(
    /\s+/g,
    ' ',
  );
}
