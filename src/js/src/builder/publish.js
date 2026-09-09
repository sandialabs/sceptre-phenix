// Publish intent helpers.
//
// Publish never uploads document bytes: the server loads the snapshot the
// draft cursor points at and re-runs its own validation. The editor only
// describes *what* to write, which is what these helpers build.

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
 * @param {object} context scenario (document ref), topologies, experiments,
 *   scenarios
 * @returns {{intent: object|null, error: string}}
 */
export function buildPublishIntent(form = {}, context = {}) {
  const fail = (error) => ({ intent: null, error });
  const mode = form.mode === 'topology-experiment' ? form.mode : 'topology';
  const topologyName = String(form.topologyName || '').trim();

  if (!topologyName) {
    return fail('Enter a name for the topology.');
  }

  const intent = {
    mode,
    topology: {
      name: topologyName,
      action: actionFor(topologyName, context.topologies),
    },
  };

  if (mode === 'topology-experiment') {
    const scenario = context.scenario;

    if (scenario?.kind === 'stored') {
      intent.scenario = { name: scenario.name, action: 'use' };
    } else if (scenario) {
      const scenarioName = String(form.scenarioName || '').trim();
      const scenarioAction = form.scenarioAction;

      if (!scenarioName || !['create', 'update'].includes(scenarioAction)) {
        return fail(
          'Choose whether the uploaded scenario creates a new config or updates an existing one.',
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

        if (!expectedDigest) {
          return fail(
            'Refresh the scenario list before updating the existing scenario.',
          );
        }

        intent.scenario.expectedDigest = expectedDigest;
      }
    }

    const name = String(form.experimentName || '').trim();

    if (!name) {
      return fail('Enter a name for the experiment.');
    }

    intent.experiment = { name, action: actionFor(name, context.experiments) };
  }

  return { intent, error: '' };
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
