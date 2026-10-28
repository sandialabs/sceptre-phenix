// Publish intent helpers.
//
// Publish never uploads document bytes. The server loads the snapshot that
// the draft cursor points at and runs its own validation again. The editor
// only describes *what* to write, and these helpers build that description.

import { count, listOf } from './announce.js';
import { sentence } from './api.js';
import { LEGACY_ANNOTATION } from './configs.js';
import { isPlainObject } from './values.js';

// Config names the server accepts: metadata.name in the config schema
// (types/schema.go) and NameRegex in api/config.
const CONFIG_NAME = /^[A-Za-z0-9_@.-]+$/;

// The symbols are named as well as shown: screen readers at their default
// punctuation level do not speak "_" or "-".
export const CONFIG_NAME_RULE =
  'Names can use only letters, numbers, underscores (_), at signs (@), ' +
  'periods (.) and hyphens (-), with no spaces.';

// Turns a diagram name into a config name the server accepts. Each run of
// other characters becomes one dash, and a valid name stays the same.
export function configName(name) {
  return String(name || '')
    .trim()
    .replace(/[^A-Za-z0-9_@.-]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

// The diagram name made into a topology name, or '' when it has no allowed
// character. The Publish dialog starts with it, and a new scenario names it.
export function defaultTopologyName(doc) {
  return configName(doc?.metadata?.name);
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

// Why a name breaks the naming rule (a space, other characters or both), or
// ''. It names the first five characters once each, by code point if invisible.
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
    parts.push(`characters that a name cannot contain: ${names.join(', ')}`);
  }

  return `it contains ${parts.join(' and ')}`;
}

// What a name field says while its name breaks the naming rule: why, then the
// rule. It is '' for a valid name or no name.
export function configNameHint(name) {
  const reason = configNameReason(String(name || '').trim());

  return reason ? `This name is not valid: ${reason}. ${CONFIG_NAME_RULE}` : '';
}

// Why `name` cannot name a config of `kind`, or ''. The message names the
// field, why, the rule and a valid alternative.
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
    `The ${kind} name "${name}" is not valid: ${configNameReason(name)}.`,
    CONFIG_NAME_RULE,
    suggestion ? `For example: ${suggestion}` : '',
  ]
    .filter(Boolean)
    .join(' ');
}

/** Chooses create or update from the names the server already has. */
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

// Whether this draft may update the existing config `name`, as the server
// decides (topologyUpdateRefusal and preflightExperiment in
// web/builder_publish.go). docs/content/builder/publishing.md has the cases.
// The server can also refuse a change the client cannot see (publishRefusal).
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

// Why this draft may not update config `name`: 'changed' (someone else changed
// it after this draft published it), 'source' (draftCanUpdate says no) or ''.
// Only the draft imported from a topology may replace its legacy diagram
// (topologyUpdateMatchesSource in web/builder_publish.go).
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

// The topology of a "builder-file/<topology>/<digest>" token, or ''.
function fileTopology(token) {
  return token.startsWith(FILE_TOKEN)
    ? token.slice(FILE_TOKEN.length).split('/')[0]
    : '';
}

// The configs of `kind` this draft last published to or came from, best
// first. Some are blank. draftCanUpdate decides which it may update.
function updateCandidates(kind, draft = {}) {
  const { source, publication, forked, documents = [] } = draft;
  const token = String(draft.sourceToken || '');

  if (kind === 'experiment') {
    return [
      publication?.experimentTarget,
      forked?.experimentTarget,
      source?.kind === 'experiment' ? source.name : '',
    ];
  }

  const opened = token.startsWith(PUBLISHED_TOKEN)
    ? token.slice(PUBLISHED_TOKEN.length)
    : '';
  const openedEntry = opened
    ? (documents || []).find(
        (entry) => entry?.kind === 'Topology' && entry.id === opened,
      )
    : null;

  return [
    publication?.topologyTarget,
    forked?.topologyTarget,
    openedEntry?.target,
    fileTopology(token),
    source?.kind === 'topology' ? source.name : '',
    source?.kind === 'experiment' ? source.topology : '',
  ];
}

// The existing config of `kind` this draft may update, which the Publish
// dialog offers in one click. It is '' for none, or when `current` holds it.
export function suggestedName(kind, current, entries = [], draft = {}) {
  const typed = String(current || '').trim();
  const name = updateCandidates(kind, draft).find(
    (candidate) =>
      typeof candidate === 'string' &&
      CONFIG_NAME.test(candidate) &&
      actionFor(candidate, entries) === 'update' &&
      updateBlocker(kind, candidate, entries, draft) === '',
  );

  return name && name !== typed ? name : '';
}

// The code of the refusal of a draft whose source config changed after the
// diagram was made from it (checkSourceFreshness in web/builder_publish.go).
export const SOURCE_CHANGED = 'publish.source.changed';

// That refusal among the issues of a dry run, or null. Publish refuses it
// under any name, so the dialog warns of it before Publish is selected.
export function sourceChangedIssue(issues = []) {
  return (issues || []).find((issue) => issue?.code === SOURCE_CHANGED) || null;
}

// Whether the server's topology `name` still has a legacy Builder diagram,
// which an update replaces.
export function hasLegacyDiagram(name, entries = []) {
  return (entries || []).some(
    (entry) => entry?.name === name && entry.builder === LEGACY_ANNOTATION,
  );
}

// The warning of an import that could not read the legacy Builder diagram
// of its topology (FromLegacyTopology in types/builder/legacy.go). The
// document's source keeps this warning. Nothing of that diagram is in the
// document.
const LEGACY_UNREAD =
  /^The Builder could not read the legacy diagram of topology .+ \(/;

// What an update of topology `name` does with its legacy Builder diagram: ''
// for none, 'replaced' when converted, 'removed' when the import could not read
// it (ReplaceLegacyDiagram in api/builder/publish.go).
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
      'changed after this diagram published it. Publishing would overwrite that change. ' +
      `Import the ${kind} again to edit it as it is now, or enter another name to create a new ${kind}.`
    );
  }

  const loaded =
    kind === 'topology'
      ? 'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it'
      : 'You did not import the diagram from it or publish the diagram to it';

  return `already exists. This diagram cannot update it. ${loaded}. ${instead}`;
}

/** The hint under a config name field: what publishing does with that name. */
export function targetHint(kind, exists, blocker = '', legacy = false) {
  if (!exists) {
    return `Publishing creates a new ${kind}.`;
  }

  if (blocker) {
    return `${article(kind)} ${kind} with this name ${blockedReason(kind, blocker)}`;
  }

  const updated = `${article(kind)} ${kind} with this name exists. Publishing updates it.`;

  if (!legacy || kind !== 'topology') {
    return updated;
  }

  return legacy === 'removed'
    ? `${updated} Builder could not read its legacy Builder diagram. Publishing removes it.`
    : `${updated} This diagram replaces its legacy Builder diagram.`;
}

// The name of the Publish button. It says when it overwrites a config:
// "Update topology", "Create topology and update experiment".
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

// What the Publish summary adds for a diagram whose included topologies show
// no nodes (not read or not resolved). Publishing keeps them by reference. It
// is '' for none, else it starts with a space.
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

// The confirmation Publish asks for before it replaces configs, which no one
// can undo: phenix keeps no earlier version. It is null when it only creates.
// Publishing never replaces a scenario. It only adds to its annotation.
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
      'on the server with this diagram. You cannot undo this.',
    confirmLabel: publishLabel(
      intent.topology.action,
      intent.experiment?.action,
    ),
  };
}

// What the Publish dialog says of the listed scenarios: publishing adds the
// topology to each, in either mode. It is '' for none.
export function scenarioStageHint(names = []) {
  if (names.length === 0) {
    return '';
  }

  const which =
    names.length === 1
      ? `the scenario ${names[0]}`
      : `each of the scenarios ${listOf(names)}`;

  return `Publishing adds this topology to the topology annotation of ${which}. Experiments of the topology can then use it.`;
}

/** The refusal of an existing config name that this draft may not update. */
function updateProblem(kind, name, blocker = 'source') {
  return blocker === 'source'
    ? `${article(kind)} ${kind} named "${name}" ${blockedReason(kind, blocker)}`
    : `The ${kind} "${name}" ${blockedReason(kind, blocker)}`;
}

/** Names of the server's scenarios, from strings or source entries. */
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

// Merges two topology annotations of a scenario: the stored one byte for byte,
// then each new uploaded name after a comma, as addTopologyAnnotation in
// builder_publish.go does. It never loses a stored name.
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

  return isPlainObject(annotations) ? annotations : {};
}

// An uploaded Scenario config over a stored one: the stored annotations stay
// under the uploaded ones, and the topology annotations merge. phenix creates
// an experiment only while that annotation names its topology.
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

// Builds the publish intent for a form, or an error with its form field. It
// refuses an existing name that updateBlocker blocks before anything is sent.
// The experiment's scenario is one the document lists, or none.
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

// What the publish refusal codes (codes.go) say: the config kind, and a rule
// the dialog explains. `stale`: the config list changed after the dialog read
// it. `source`: an update draftCanUpdate let through. `changed`: someone else
// changed a config this draft published. Any other code names its kind in its
// second word.
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
      message: `The ${kind} "${name}" no longer exists. Publishing again creates it.`,
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

// A publish refusal (409 or 422) as a dialog message and its form field, by
// code. For a stale action, it says what publishing again does. Any other
// refusal keeps the server's reason. `changed` is the "<kind>/<name>" of a
// config someone else changed after this draft published it.
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
      'A config this publish would write changed on the server. Try again.',
    field,
  };
}

/**
 * What finishes a partial publication that the server says can be finished
 * (see readPublishResult in api.js).
 */
export const PUBLISH_RETRY_ADVICE =
  'Publish again with the same choices to finish. The server skips the steps that are already done.';

/** A sentence on a publish result, also a partial one, and how to finish it. */
export function describePublishResult(result) {
  if (!result) {
    return '';
  }

  if (result.ok) {
    return 'Published. Every stage succeeded.';
  }

  if (!result.partial) {
    return 'Publish failed. It wrote no configs.';
  }

  const failed =
    'Published with failures. Publish wrote some configs. The list below shows the failed stages.';

  return result.retry ? `${failed} ${PUBLISH_RETRY_ADVICE}` : failed;
}

/**
 * @param {object} stage publish stage result
 * @returns {boolean}
 */
export function stageFailed(stage) {
  return ['failed', 'error'].includes(stage?.status);
}

// --- What publishing changes --------------------------------------------------
// The words of the Publish dry run (DescribePublishChanges in changes.go).
// The words, not a color, say what is added, removed or kept.

// How many devices a disk image's line names before it counts the rest.
const NAMED_DEVICES = 5;

/** What publishing does to a config: "Creates Topology config riverside". */
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

// What publishing does to a topology the Topology config includes, as in
// "Removes included topology plant-a".
export function includeChangeText({ name, change } = {}) {
  if (change === 'added') {
    return `Adds included topology ${name}`;
  }

  return change === 'removed'
    ? `Removes included topology ${name}`
    : `Keeps included topology ${name}`;
}

// What publishing does to a listed Scenario config, as in "Adds topology
// riverside to Scenario water-ops".
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

// What publishing does to a disk image, and whether the server has it when
// known, as in "Disk image ubuntu.qc2 is new (web-1 and web-2 use it)".
export function imageChangeText({
  name,
  change,
  devices = [],
  onServer = null,
} = {}) {
  const used = devicesText(devices);
  const uses = devices.length === 1 ? 'uses' : 'use';

  if (change === 'removed') {
    return `Disk image ${name} is no longer in use (${used} used it)`;
  }

  const line =
    change === 'added'
      ? `Disk image ${name} is new (${used} ${uses} it)`
      : `Disk image ${name} is still in use (${used} ${uses} it)`;

  if (typeof onServer !== 'boolean') {
    return line;
  }

  return onServer
    ? `${line}, and the server has it`
    : `${line}, but the server does not have it`;
}

// What publishing does to a network's VLAN alias, as in "VLAN alias for
// network ot changes from 101 to 120".
export function aliasChangeText({ name, from, to, change } = {}) {
  switch (change) {
    case 'added':
      return `Sets the VLAN alias for network ${name} to ${to}`;
    case 'removed':
      return `Removes VLAN alias ${from} for network ${name}`;
    case 'changed':
      return `VLAN alias for network ${name} changes from ${from} to ${to}`;
    default:
      return `VLAN alias for network ${name} stays ${to}`;
  }
}

// How many items the changes name outside the configs: included topologies,
// scenarios, disk images and VLAN aliases.
export function changedItems(changes) {
  if (!changes) {
    return 0;
  }

  return ['includes', 'scenarios', 'images', 'vlanAliases'].reduce(
    (total, key) => total + (changes[key] || []).length,
    0,
  );
}

// The Publish dialog's change lists, one per kind with lines: configs,
// included topologies, scenarios, disk images and VLAN aliases.
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

// What the Publish dialog announces when it reads its changes again: the
// config changes, how many more lines follow and how many warnings.
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
