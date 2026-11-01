import { escapeHTML } from '@/utils/escapeHTML.js';
import { actionState, partitionForAction } from '@/utils/operations.js';
import { namedList } from '@/utils/plural.js';

// The Experiments table's actions: the role each needs and the states that rule
// it out. Reasons read after "Can't <verb>: ".
export const EXPERIMENT_ACTIONS = {
  start: {
    name: 'start',
    label: 'Start',
    icon: 'play',
    type: 'is-success',
    rbac: ['experiments/start', 'update'],
    verbPast: 'Started',
    // what the selection must hold some of
    noneReady: 'none of the selected experiments is stopped',
  },
  stop: {
    name: 'stop',
    label: 'Stop',
    icon: 'stop',
    type: 'is-danger',
    rbac: ['experiments/stop', 'update'],
    verbPast: 'Stopped',
    noneReady: 'none of the selected experiments is running',
  },
  delete: {
    name: 'delete',
    label: 'Delete',
    icon: 'trash',
    type: 'is-danger',
    rbac: ['experiments', 'delete'],
    verbPast: 'Deleted',
    noneReady: 'none of the selected experiments is stopped',
  },
};

// the actions a selection offers, in toolbar order
export const BULK_ACTIONS = ['start', 'stop', 'delete'];

const updating = (status) => status === 'starting' || status === 'stopping';

// Why the experiment's state rules the action out now, or null. `deleting`,
// `starting` and `stopping` say whether that request is under way.
export function experimentBlocked(
  name,
  exp,
  { deleting = false, starting = false, stopping = false } = {},
) {
  if (updating(exp.status)) return `the experiment is ${exp.status}`;
  if (deleting) return 'the experiment is being deleted';
  if (starting) return 'the experiment is being started';
  if (stopping) return 'the experiment is being stopped';
  switch (name) {
    case 'start':
      return exp.running ? 'the experiment is already running' : null;
    case 'stop':
      return exp.running ? null : 'the experiment is not running';
    case 'delete':
      return exp.running ? 'the experiment is running' : null;
  }
  return null;
}

// Whether the role may take the action on the experiment; `can(resource,
// verb, name)` answers RBAC.
const experimentPermitted = (name, exp, can) =>
  can(...EXPERIMENT_ACTIONS[name].rbac, exp.name);

// Whether and how to offer the action for one experiment (see actionState).
export function experimentActionState(
  name,
  exp,
  { can, deleting = false, starting = false, stopping = false },
) {
  return actionState(EXPERIMENT_ACTIONS[name], {
    permitted: experimentPermitted(name, exp, can),
    blocked: experimentBlocked(name, exp, { deleting, starting, stopping }),
  });
}

// Splits a selection into `allowed` and `skipped` (grouped by reason);
// `deleting`, `starting` and `stopping` map names with a request out to true.
export function partitionExperiments(
  name,
  names,
  experiments,
  { can, deleting = {}, starting = {}, stopping = {} },
) {
  const byName = new Map(experiments.map((exp) => [exp.name, exp]));
  return partitionForAction(
    names,
    (expName) => byName.get(expName),
    (exp) =>
      experimentPermitted(name, exp, can)
        ? experimentBlocked(name, exp, {
            deleting: !!deleting[exp.name],
            starting: !!starting[exp.name],
            stopping: !!stopping[exp.name],
          })
        : "the experiment is outside your role's permissions",
    'the experiment is no longer listed',
  );
}

// How to offer an action on a selection: permitted if the role may take it on
// some, disabled if none is ready; `count` is how many it would act on.
export function bulkActionState(name, names, experiments, opts) {
  const action = EXPERIMENT_ACTIONS[name];
  const selected = experiments.filter((exp) => names.includes(exp.name));
  const permitted = selected.some((exp) =>
    experimentPermitted(name, exp, opts.can),
  );
  const { allowed } = partitionExperiments(name, names, experiments, opts);
  const state = actionState(
    {
      ...action,
      label: `${action.label} selected experiments`,
      verb: action.label.toLowerCase(),
    },
    {
      permitted,
      blocked: allowed.length ? null : action.noneReady,
    },
  );
  return { ...state, count: allowed.length };
}

// Whether a failed stop left the experiment running because minimega could
// not kill its VMs, so that forcing the stop could mark it stopped.
export const stopCanBeForced = (err) =>
  err?.response?.data?.metadata?.retry === 'force';

// Asks whether to force the stop of the experiments `names` (one name or a
// list) after minimega could not kill their VMs; `onForce` sends the stops.
export function offerForcedStop(buefy, names, onForce) {
  const list = Array.isArray(names) ? names : [names];
  const one = list.length == 1;
  buefy.dialog.confirm({
    title: one ? 'Force the Stop' : 'Force the Stops',
    message:
      'minimega could not kill the VMs of ' +
      namedList(
        list.map((name) => `<b>${escapeHTML(name)}</b>`),
        'experiment',
      ) +
      `, so ${one ? 'it is' : 'they are'} still running. Forcing the stop ` +
      `marks ${one ? 'it' : 'them'} stopped anyway. You must then clear by ` +
      `hand any of ${one ? 'its' : 'their'} VMs that are still in minimega.`,
    cancelText: 'Cancel',
    confirmText: 'Force Stop',
    type: 'is-danger',
    hasIcon: true,
    onConfirm: onForce,
  });
}
