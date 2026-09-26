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

/**
 * Why `name` cannot name a config of the given kind, or '' when it can. The
 * message names the field, the rule and a valid alternative.
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
    `The ${kind} name "${name}" is not allowed.`,
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

// Source tokens of a draft generated from an uploaded config, and of one
// opened from a published diagram ("builder-doc/<document id>").
const UPLOADED_TOKEN = 'uploaded/';
const PUBLISHED_TOKEN = 'builder-doc/';

/**
 * Whether this draft may update the existing config `name`, as the server
 * decides (topologyUpdateRefusal and preflightExperiment in
 * web/builder_beta_publish.go).
 *
 * A draft may update a topology that holds one of its own published
 * diagrams: one it published, whatever it was loaded from, the one it was
 * opened from, or one of exactly its saved snapshot. That is how it publishes
 * again after further edits. Otherwise it may update the topology it was
 * generated from, unless that was an upload, or the one its source
 * experiment was built from. The server also refuses a topology changed by
 * anyone else since the draft published it, which the client cannot see: it
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
  // one a topology config points at is listed as current.
  const holdsOwn = (topology) =>
    documents.some(
      (entry) =>
        entry?.kind === 'Topology' &&
        entry.target === topology &&
        ((draft.id && entry.draftId === draft.id) ||
          (publication?.documentId && entry.id === publication.documentId) ||
          (forked?.documentId && entry.id === forked.documentId) ||
          (opened && entry.id === opened) ||
          (digest && entry.digest === digest)),
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
    !uploaded &&
    ((source?.kind === 'topology' && source.name === name) ||
      (source?.kind === 'experiment' && source.topology === name))
  );
}

/**
 * Why this draft may not update the existing config `name`: 'legacy' for a
 * topology the legacy XML Builder owns, which the server never lets Builder
 * Beta update, 'changed' for one the server refused because someone else has
 * changed it since this draft published it, 'source' when draftCanUpdate()
 * says no, or '' when it may.
 *
 * @param {string} kind 'topology' or 'experiment'
 * @param {string} name existing config name
 * @param {Array} entries the server's configs of that kind, as names or
 *   source entries
 * @param {object} draft as draftCanUpdate() takes it, and changedTargets:
 *   the "<kind>/<name>" of each config the server refused so (see
 *   publishRefusal)
 * @returns {''|'legacy'|'changed'|'source'}
 */
export function updateBlocker(kind, name, entries = [], draft = {}) {
  const entry = (entries || []).find((item) => item?.name === name);

  if (kind === 'topology' && entry?.builder === LEGACY_ANNOTATION) {
    return 'legacy';
  }

  if ((draft.changedTargets || []).includes(`${kind}/${name}`)) {
    return 'changed';
  }

  return draftCanUpdate(kind, name, draft) ? '' : 'source';
}

function article(kind) {
  return kind === 'experiment' ? 'An' : 'A';
}

// Why an update is blocked (see updateBlocker), and what to do instead.
function blockedReason(kind, blocker) {
  const instead = `Enter another name to create a new ${kind}.`;

  if (blocker === 'legacy') {
    return `belongs to the legacy XML Builder and cannot be updated here. ${instead}`;
  }

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
 * @returns {string}
 */
export function targetHint(kind, exists, blocker = '') {
  if (!exists) {
    return `A new ${kind} will be created.`;
  }

  return blocker
    ? `${article(kind)} ${kind} with this name ${blockedReason(kind, blocker)}`
    : `${article(kind)} ${kind} with this name exists and will be updated.`;
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

// The server's refusal of an update to a topology the legacy XML Builder owns.
const LEGACY_TARGET =
  /^topology (\S+) belongs to the legacy XML Builder and cannot be updated here\.?$/i;

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

  const legacy = LEGACY_TARGET.exec(text);

  if (legacy && intent.topology?.name === legacy[1]) {
    return {
      message: updateProblem('topology', legacy[1], 'legacy'),
      field: 'topologyName',
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
 * what publishing refuses, an interface with no VLAN (see collectWarnings
 * in validate.js), is an error there. The draft still saves with it, as the
 * checks elsewhere say, but it is not published: phenix would store the
 * topology, and minimega refuse it when the experiment starts.
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
