// A start or stop progress: { operation, percent: 0..1, phase, done, total,
// step }, from the websocket and list items. An old server sends { percent }.

// The name each phase shows, by the server's phase id.
export const PHASE_NAMES = Object.freeze({
  'pre-start-apps': 'Pre-start apps',
  prepare: 'Preparing',
  'launch-vms': 'Launching VMs',
  'start-vms': 'Starting VMs',
  'post-start-apps': 'Post-start apps',
  'cleanup-apps': 'Cleanup apps',
  'stop-vms': 'Stopping VMs',
  cleanup: 'Cleaning up',
});

// The phase that ends the bar.
const DONE = 'done';
// The phase this page shows while its own request waits on the server.
const SENDING = 'request';

const isObject = (value) => typeof value === 'object' && value !== null;
const text = (value) => (typeof value === 'string' ? value.trim() : '');
const count = (value) =>
  typeof value === 'number' && Number.isFinite(value) && value > 0
    ? Math.floor(value)
    : 0;

// The operation a progress or failure is of: 'start', 'stop' or ''.
const operationOf = (value) =>
  value?.operation === 'start' || value?.operation === 'stop'
    ? value.operation
    : '';

// The name a phase shows: its display name, its id when unknown, or ''.
function phaseName(value) {
  const phase = text(value?.phase);
  if (phase === DONE) return '';
  if (phase === SENDING) {
    const operation = operationOf(value);
    return operation ? `Sending ${operation} request` : 'Sending request';
  }
  return Object.hasOwn(PHASE_NAMES, phase) ? PHASE_NAMES[phase] : phase;
}

// "<Phase name> <done>/<total>: <step>", without the parts that are empty
// (no count when the total is 0); '' for an old server's { percent }.
export function progressLabel(progress) {
  if (!isObject(progress)) return '';
  const total = count(progress.total);
  const head = [
    phaseName(progress),
    total ? `${count(progress.done)}/${total}` : '',
  ]
    .filter(Boolean)
    .join(' ');
  return [head, text(progress.step)].filter(Boolean).join(': ');
}

// "Failed at <phase name>: <step>: <error>", without the parts that are empty.
export function failureLabel(failure) {
  const phase = isObject(failure) ? phaseName(failure) : '';
  return [
    phase ? `Failed at ${phase}` : 'Failed',
    text(failure?.step),
    text(failure?.error),
  ]
    .filter(Boolean)
    .join(': ');
}

// The percent the bar shows, a whole number from 0 to 100.
export function progressPercent(progress) {
  const percent = isObject(progress) ? progress.percent : undefined;
  if (typeof percent !== 'number' || !Number.isFinite(percent)) return 0;
  return Math.min(100, Math.max(0, Math.round(percent * 100)));
}

// Whether a progress is still under way: a phase other than done.
export const progressActive = (progress) =>
  isObject(progress) && progress.phase !== DONE;

// What changes when the screen reader announces again: the operation and
// phase, not the counts.
export const progressPhaseKey = (progress) =>
  isObject(progress) ? `${operationOf(progress)}:${text(progress.phase)}` : '';

// The progress this page shows from the click until the server answers.
export const sendingProgress = (operation) => ({
  operation,
  percent: 0,
  phase: SENDING,
});

export const isSending = (progress) =>
  isObject(progress) && progress.phase === SENDING;

// The progress a list item reports: its progress, or for an old server the
// percent of a starting or stopping experiment; undefined when idle.
export function listedProgress(item) {
  if (isObject(item?.progress)) return item.progress;
  if (item?.status === 'starting' || item?.status === 'stopping') {
    const percent = typeof item.percent === 'number' ? item.percent : 0;
    return { percent };
  }
  return undefined;
}

// The newer progress `next` after `prev`: undefined once done, and within one
// operation the percent never goes back. A new operation starts afresh.
export function mergeProgress(prev, next) {
  if (!isObject(next) || next.phase === DONE) return undefined;
  if (!isObject(prev)) return next;
  const [before, after] = [operationOf(prev), operationOf(next)];
  if (before && after && before !== after) return next;
  const percent = Math.max(
    progressPercent(prev) / 100,
    typeof next.percent === 'number' && Number.isFinite(next.percent)
      ? next.percent
      : 0,
  );
  return { ...(before && { operation: before }), ...next, percent };
}

// Whether a start message leaves the bar of `progress` up: the server keeps
// reporting it (post-start apps), unlike this page's own guess.
const continues = (progress, operation) =>
  operationOf(progress) === operation &&
  !!text(progress.phase) &&
  !isSending(progress) &&
  progressActive(progress);

// The { progress, failure } after a websocket message with this action and
// body; a failure with no body (old server) keeps the failure as it was.
export function afterMessage({ progress, failure }, action, body) {
  switch (action) {
    case 'starting':
    case 'stopping': {
      const operation = action === 'starting' ? 'start' : 'stop';
      const kept = isSending(progress) && operationOf(progress) === operation;
      return {
        progress: kept ? progress : { operation, percent: 0 },
        failure: null,
      };
    }
    case 'progress':
      return { progress: mergeProgress(progress, body), failure };
    case 'start':
      return {
        progress: continues(progress, action) ? progress : undefined,
        failure,
      };
    case 'stop':
      // a stop sends no done: its stop message ends the bar
      return { progress: undefined, failure };
    case 'errorStarting':
    case 'errorStopping':
      return {
        progress: undefined,
        failure: isObject(body)
          ? {
              operation: action === 'errorStarting' ? 'start' : 'stop',
              ...body,
            }
          : failure,
      };
    default:
      return { progress, failure };
  }
}
