import { h } from 'vue';

import RestartExperimentDialog from '@/components/experiment/RestartExperimentDialog.vue';
import StartExperimentDialog from '@/components/experiment/StartExperimentDialog.vue';
import StopExperimentDialog from '@/components/experiment/StopExperimentDialog.vue';
import { escapeHTML } from '@/utils/escapeHTML.js';
import { openNotification } from '@/utils/notify.js';
import {
  LISTED_FAILURES,
  actionState,
  partitionForAction,
  skippedText,
} from '@/utils/operations.js';
import { namedList, plural, pluralWord } from '@/utils/plural.js';
import { startResult } from '@/utils/startOptions.js';

// The Experiments table's actions: the roles each needs and the states that
// rule it out. Reasons read after "Can't <verb>: ".
export const EXPERIMENT_ACTIONS = {
  start: {
    name: 'start',
    label: 'Start',
    icon: 'play',
    type: 'is-success',
    rbac: [['experiments/start', 'update']],
    verbPast: 'Started',
    // what the selection must hold some of
    noneReady: 'none of the selected experiments is stopped',
  },
  stop: {
    name: 'stop',
    label: 'Stop',
    icon: 'stop',
    type: 'is-danger',
    rbac: [['experiments/stop', 'update']],
    verbPast: 'Stopped',
    noneReady: 'none of the selected experiments is running',
  },
  restart: {
    name: 'restart',
    label: 'Restart',
    icon: 'sync-alt',
    type: 'is-warning',
    rbac: [
      ['experiments/stop', 'update'],
      ['experiments/start', 'update'],
    ],
    verbPast: 'Restarted',
    noneReady: 'none of the selected experiments is running',
  },
  delete: {
    name: 'delete',
    label: 'Delete',
    icon: 'trash',
    type: 'is-danger',
    rbac: [['experiments', 'delete']],
    verbPast: 'Deleted',
    noneReady: 'none of the selected experiments is stopped',
  },
};

// the actions a selection offers, in toolbar order
export const BULK_ACTIONS = ['start', 'stop', 'restart', 'delete'];

const updating = (status) => status === 'starting' || status === 'stopping';

// Why the experiment's state rules the action out now, or null. `deleting`,
// `starting`, `stopping` and `restarting` say whether that request is out.
export function experimentBlocked(
  name,
  exp,
  {
    deleting = false,
    starting = false,
    stopping = false,
    restarting = false,
  } = {},
) {
  if (updating(exp.status)) return `the experiment is ${exp.status}`;
  if (deleting) return 'the experiment is being deleted';
  if (starting) return 'the experiment is being started';
  if (stopping) return 'the experiment is being stopped';
  if (restarting) return 'the experiment is being restarted';
  switch (name) {
    case 'start':
      return exp.running ? 'the experiment is already running' : null;
    case 'stop':
    case 'restart':
      return exp.running ? null : 'the experiment is not running';
    case 'delete':
      return exp.running ? 'the experiment is running' : null;
  }
  return null;
}

// Whether the role may take the action on the experiment; `can(resource,
// verb, name)` answers RBAC.
export const experimentPermitted = (name, exp, can) =>
  EXPERIMENT_ACTIONS[name].rbac.every(([resource, verb]) =>
    can(resource, verb, exp.name),
  );

// Whether and how to offer the action for one experiment (see actionState).
export function experimentActionState(name, exp, { can, ...pending }) {
  return actionState(EXPERIMENT_ACTIONS[name], {
    permitted: experimentPermitted(name, exp, can),
    blocked: experimentBlocked(name, exp, pending),
  });
}

// Splits a selection into `allowed` and `skipped` (grouped by reason);
// `deleting`, `starting`, `stopping` and `restarting` map names with a
// request out to true.
export function partitionExperiments(
  name,
  names,
  experiments,
  { can, deleting = {}, starting = {}, stopping = {}, restarting = {} },
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
            restarting: !!restarting[exp.name],
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

// Opens the dialog `component` (StartExperimentDialog.vue and its siblings),
// whose props are plain text. It resolves to what the dialog emits as `event`
// (the request body or options, {} for the defaults), or null when canceled.
function openDialog(buefy, component, event, props) {
  return new Promise((resolve) => {
    buefy.modal.open({
      component,
      props,
      hasModalCard: true,
      trapFocus: true,
      // the look of a Buefy confirm dialog (its .dialog styles)
      customClass: 'dialog',
      canCancel: ['escape', 'outside'],
      events: { [event]: resolve, cancel: () => resolve(null) },
      onCancel: () => resolve(null),
    });
  });
}

// the title of a dialog for one experiment or a selection of `count`
const dialogTitle = (label, count) =>
  `${label} the ${pluralWord(count, 'Experiment')}`;

// Asks whether and how to start the experiment `name`: resolves to the
// request body (see startRequestBody), or null when canceled.
export const confirmStart = (buefy, name) =>
  openDialog(buefy, StartExperimentDialog, 'start', {
    title: dialogTitle('Start', 1),
    message: `This will start the ${name} experiment.`,
  });

// Asks whether and how to start the experiments `names` of a selection,
// saying which it skips and why (see partitionExperiments), as confirmStart.
export const confirmStartSelection = (buefy, names, skipped = []) =>
  openDialog(buefy, StartExperimentDialog, 'start', {
    title: dialogTitle('Start', names.length),
    message: `This will start ${namedList(names, 'experiment')}.`,
    note: skippedText(skipped, 'experiment'),
  });

// Asks whether and how to stop the experiment `name`: resolves to the stop
// options (see stopRequestBody), or null when canceled.
export const confirmStop = (buefy, name) =>
  openDialog(buefy, StopExperimentDialog, 'stop', {
    title: dialogTitle('Stop', 1),
    message: `This will stop the ${name} experiment.`,
  });

// Asks whether and how to stop the experiments `names` of a selection, as
// confirmStartSelection; the options apply to each.
export const confirmStopSelection = (buefy, names, skipped = []) =>
  openDialog(buefy, StopExperimentDialog, 'stop', {
    title: dialogTitle('Stop', names.length),
    message: `This will stop ${namedList(names, 'experiment')}.`,
    note: skippedText(skipped, 'experiment'),
  });

// Asks whether and how to restart the experiment `name`: resolves to the
// request body (see restartRequestBody), or null when canceled.
export const confirmRestart = (buefy, name) =>
  openDialog(buefy, RestartExperimentDialog, 'restart', {
    title: dialogTitle('Restart', 1),
    message: `This will stop the ${name} experiment and start it again.`,
  });

// Asks whether and how to restart the experiments `names` of a selection, as
// confirmStartSelection; the options apply to each.
export const confirmRestartSelection = (buefy, names, skipped = []) =>
  openDialog(buefy, RestartExperimentDialog, 'restart', {
    title: dialogTitle('Restart', names.length),
    message:
      `This will stop ${namedList(names, 'experiment')} and start ` +
      `${names.length == 1 ? 'it' : 'them'} again.`,
    note: skippedText(skipped, 'experiment'),
  });

// the lines that list warnings, at most LISTED_FAILURES
const warningLines = (warnings) => [
  ...warnings
    .slice(0, LISTED_FAILURES)
    .map((warning) => h('span', { class: 'error-step' }, warning)),
  ...(warnings.length > LISTED_FAILURES
    ? [
        h(
          'span',
          { class: 'error-step' },
          `and ${warnings.length - LISTED_FAILURES} more`,
        ),
      ]
    : []),
];

// Tells what a start of the experiment `name` did when the server answered
// with a StartResult (see startResult): a dry run, with its minimega script
// and warnings, or the minimega errors the start took as warnings. A plain
// start shows nothing here; the websocket tells of it. `verbPast` names what
// a stop or restart did.
export function notifyStartResult(
  name,
  { dryRun, warnings, mmScript },
  verbPast = 'Started',
) {
  if (!dryRun && !warnings.length) return;
  const count = plural(warnings.length, 'warning');
  const title = dryRun
    ? `Dry run of the ${name} experiment`
    : `${verbPast} the ${name} experiment with ${count}`;
  const lines = dryRun
    ? [
        h('p', 'phēnix did not call minimega. The experiment stays stopped.'),
        mmScript && h('p', `minimega script: ${mmScript}`),
        warnings.length && h('p', `${count}:`),
      ]
    : [];

  openNotification({
    type: warnings.length ? 'is-warning' : 'is-info',
    hasIcon: true,
    position: 'is-top',
    indefinite: true,
    ariaCloseLabel: 'Dismiss',
    message: h('div', { role: warnings.length ? 'alert' : 'status' }, [
      h('span', { class: 'error-title' }, [h('b', title)]),
      ...lines.filter(Boolean),
      ...warningLines(warnings),
    ]),
  });
}

// Tells of the warnings in a stop answer of the experiment `name`, such as
// VMs that a forced stop left in minimega; nothing when there are none.
export const notifyStopResult = (name, data) =>
  notifyStartResult(name, { ...startResult(data), dryRun: false }, 'Stopped');

// Tells of the warnings in a restart answer of the experiment `name`.
export const notifyRestartResult = (name, data) =>
  notifyStartResult(name, startResult(data), 'Restarted');

// Tells of the warnings of a schedule of the experiment `name`, each on its
// own line, in a notice that stays until it is dismissed; nothing when there
// are none.
export function notifyScheduleWarnings(name, warnings) {
  if (!Array.isArray(warnings) || !warnings.length) return;
  openNotification({
    type: 'is-warning',
    hasIcon: true,
    position: 'is-top',
    indefinite: true,
    ariaCloseLabel: 'Dismiss',
    message: h('div', { role: 'alert' }, [
      h('span', { class: 'error-title' }, [
        h(
          'b',
          `Scheduled the ${name} experiment with ${plural(warnings.length, 'warning')}`,
        ),
      ]),
      ...warnings.map((warning) => h('span', { class: 'error-step' }, warning)),
    ]),
  });
}

// Whether a failed stop left the experiment running because minimega could
// not kill its VMs, so that forcing the stop could mark it stopped.
export const stopCanBeForced = (err) =>
  err?.response?.data?.metadata?.retry === 'force';

// Asks whether to force the stops or restarts (`restart`) of the experiments
// `names` (one name or a list) after minimega could not kill their VMs;
// `onForce` sends them again with force.
function offerForced(buefy, names, onForce, restart) {
  const list = Array.isArray(names) ? names : [names];
  const one = list.length == 1;
  const [noun, label] = restart ? ['restart', 'Restart'] : ['stop', 'Stop'];
  buefy.dialog.confirm({
    title: `Force the ${label}${one ? '' : 's'}`,
    message:
      'minimega could not kill the VMs of ' +
      namedList(
        list.map((name) => `<b>${escapeHTML(name)}</b>`),
        'experiment',
      ) +
      `, so ${one ? 'it is' : 'they are'} still running. Forcing the ${noun} ` +
      `marks ${one ? 'it' : 'them'} stopped anyway` +
      (restart ? `, then starts ${one ? 'it' : 'them'} again. ` : '. ') +
      `You must then clear by hand any of ${one ? 'its' : 'their'} VMs ` +
      'that are still in minimega.',
    cancelText: 'Cancel',
    confirmText: `Force ${label}`,
    type: 'is-danger',
    hasIcon: true,
    onConfirm: onForce,
  });
}

// Asks whether to force the stop of the experiments `names` after minimega
// could not kill their VMs; `onForce` sends the stops.
export const offerForcedStop = (buefy, names, onForce) =>
  offerForced(buefy, names, onForce, false);

// Asks whether to force the restart of the experiments `names` after
// minimega could not kill their VMs; `onForce` sends the restarts.
export const offerForcedRestart = (buefy, names, onForce) =>
  offerForced(buefy, names, onForce, true);
