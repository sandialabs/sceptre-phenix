// The VNC session of one console pane or live tile: it gets the session JSON,
// gives the console the websocket URL to connect to, reconnects with backoff,
// and follows the VM and its experiment on the event stream.
import {
  getCurrentInstance,
  getCurrentScope,
  onBeforeUnmount,
  onScopeDispose,
  ref,
  shallowRef,
  toValue,
  watch,
} from 'vue';

import { getVncSession } from '@/api/vnc.js';
import { usePhenixStore } from '@/store.js';
import {
  UNREACHABLE,
  noResponseMessage,
  serverNotAnswering,
} from '@/utils/unreachable.js';
import { addWsHandler, removeWsHandler } from '@/utils/websocket';

/**
 * The retry delays after each failure in a row, in milliseconds. The last
 * delay repeats. The fast schedule is for a lost connection or server. The
 * slow schedule is for a VNC server that phēnix cannot reach or that is not
 * available.
 */
export const FAST_RETRY_MS = [1000, 2000, 5000, 10_000, 30_000];
export const SLOW_RETRY_MS = [30_000, 60_000];

/**
 * How long a connection must stay up before the retry schedule starts again.
 * A server that accepts and then drops each connection keeps backing off.
 */
export const STABLE_MS = 10_000;

export const NOT_RUNNING = 'VM is not running';
export const EXPERIMENT_STOPPED = 'Experiment stopped';
export const NOT_AVAILABLE = 'The VNC server of this VM is not available';
export const DENIED =
  'You do not have permission to open the console of this VM';

/**
 * The message after a probe that cannot reach the VNC port of the VM.
 *
 * @param {string} vm the VM name
 * @param {string} host the host of the VNC endpoint
 * @param {number} port the port of the VNC endpoint
 */
export const unreachableMessage = (vm, host, port) =>
  `phēnix cannot connect to the VNC port of ${vm} on ${host} (port ${port}). ` +
  'Make sure the phēnix host can reach that host.';

/**
 * The delay before the retry after `failures` failures in a row.
 *
 * @param {'fast'|'slow'} schedule
 * @param {number} failures one or more
 * @returns {number} milliseconds
 */
export function retryDelayMs(schedule, failures) {
  const delays = schedule === 'slow' ? SLOW_RETRY_MS : FAST_RETRY_MS;
  return delays[Math.min(Math.max(failures, 1), delays.length) - 1];
}

// experiment/vm actions that end the console: the VM does not run
const VM_GONE = new Set(['shutdown', 'delete', 'redeploying']);

// states that end the session until an event or retryNow starts it again
const ENDED = new Set([
  'notRunning',
  'experimentStopped',
  'denied',
  'notFound',
  'failed',
]);

/**
 * @typedef {'idle'|'loading'|'connecting'|'connected'|'checking'|'waiting'|
 *   'notRunning'|'experimentStopped'|'denied'|'notFound'|'failed'} VncState
 *
 * - idle: not enabled, or closed.
 * - loading: getting the session JSON.
 * - connecting: `connection` is set, and the console connects RFB to it.
 * - connected: the console reported the RFB `connect` event.
 * - checking: the websocket failed, and the probe runs.
 * - waiting: a retry runs in `retrySeconds` seconds.
 * - notRunning, experimentStopped, denied, notFound, failed: no retry.
 *   `message` tells why.
 */

/**
 * @typedef {object} VncConnection
 * @property {string} url the websocket URL to give RFB, with `?token=`
 * @property {number} attempt one more for each connection the session asks for
 */

/**
 * The VNC session of one VM. Call it in a component's setup (it cleans up
 * in onBeforeUnmount) or in an effect scope (it cleans up when the scope
 * stops).
 *
 * The console connects one RFB to each new `connection` value, and
 * disconnects it when `connection` becomes null. It reports the RFB events
 * with `connected()`, `disconnected()` and `securityFailure()`.
 *
 * The VM state comes from the event stream, not from the session JSON. Set
 * `paused.value` from the VM row the page has when it opens.
 *
 * @param {object} options
 * @param {import('vue').MaybeRefOrGetter<string>} options.experiment
 * @param {import('vue').MaybeRefOrGetter<string>} options.vm
 * @param {import('vue').MaybeRefOrGetter<boolean>} [options.enabled] false
 *   keeps the session idle, as for a tile that is not live
 */
export function useVncSession({ experiment, vm, enabled = true }) {
  /** @type {import('vue').Ref<VncState>} */
  const state = ref('idle');
  /** Whether the VM is paused. A paused VM keeps its connection. */
  const paused = ref(false);
  /** The session JSON of the last good answer, or null. */
  const info = shallowRef(null);
  /** @type {import('vue').ShallowRef<VncConnection|null>} */
  const connection = shallowRef(null);
  /** Why the session waits or ended, or ''. */
  const message = ref('');
  /** The seconds to the next retry while the state is waiting, else null. */
  const retrySeconds = ref(null);

  // each start or close makes the answers to older requests stale
  let generation = 0;
  let attempts = 0;
  let failures = 0;
  let retryTimer = null;
  let countdown = null;
  let stableTimer = null;
  let disposed = false;

  const exp = () => toValue(experiment);
  const name = () => toValue(vm);

  function clearRetry() {
    clearTimeout(stableTimer);
    stableTimer = null;
    clearTimeout(retryTimer);
    clearInterval(countdown);
    retryTimer = null;
    countdown = null;
    retrySeconds.value = null;
  }

  function end(next, why) {
    generation += 1;
    clearRetry();
    connection.value = null;
    state.value = next;
    message.value = why;
  }

  function wsUrl(websocket) {
    const token = usePhenixStore().token;
    const proto = location.protocol === 'https:' ? 'wss://' : 'ws://';
    let path = `${import.meta.env.BASE_URL}api/v1/${websocket}`;
    if (token) path += `?token=${encodeURIComponent(token)}`;
    return proto + location.host + path;
  }

  function connect(session) {
    info.value = session;
    attempts += 1;
    connection.value = { url: wsUrl(session.websocket), attempt: attempts };
    state.value = 'connecting';
  }

  function scheduleRetry(schedule, why) {
    clearRetry();
    connection.value = null;
    failures += 1;
    const delay = retryDelayMs(schedule, failures);
    const at = Date.now() + delay;
    state.value = 'waiting';
    message.value = why;
    retrySeconds.value = Math.ceil(delay / 1000);
    countdown = setInterval(() => {
      retrySeconds.value = Math.max(0, Math.ceil((at - Date.now()) / 1000));
    }, 1000);
    retryTimer = setTimeout(start, delay);
  }

  // the state for a failed request; `afterProbe` says it was the probe
  function failed(err, afterProbe) {
    const status = err?.response?.status;
    const data = err?.response?.data ?? {};
    switch (status) {
      case 403:
        return end('denied', DENIED);
      case 404:
        return end('notFound', data.message || 'VM not found');
      case 409:
        paused.value = false;
        return end('notRunning', NOT_RUNNING);
      case 400:
      case 500:
        return end('failed', data.message || 'The console failed');
    }
    if (status === 502 && data.code === 'vnc_unreachable') {
      const { host, port } = data.details ?? {};
      return scheduleRetry('slow', unreachableMessage(name(), host, port));
    }
    const noServer = serverNotAnswering(err);
    if (status === 502 && afterProbe && !noServer) {
      return scheduleRetry('slow', NOT_AVAILABLE);
    }
    scheduleRetry(
      'fast',
      noResponseMessage(err) ??
        (noServer ? UNREACHABLE : null) ??
        data.message ??
        'Could not open the console',
    );
  }

  /** Gets the session JSON again and connects. */
  function start() {
    if (disposed) return;
    generation += 1;
    const mine = generation;
    clearRetry();
    connection.value = null;
    if (!toValue(enabled)) {
      state.value = 'idle';
      message.value = '';
      return;
    }
    state.value = 'loading';
    getVncSession(exp(), name()).then(
      ({ data }) => mine === generation && connect(data),
      (err) => mine === generation && failed(err, false),
    );
  }

  /** The console reports the RFB `connect` event. */
  function connected() {
    if (state.value !== 'connecting') return;
    state.value = 'connected';
    message.value = '';
    clearTimeout(stableTimer);
    stableTimer = setTimeout(() => {
      stableTimer = null;
      failures = 0;
    }, STABLE_MS);
  }

  /**
   * The console reports the RFB `disconnect` event. A websocket handshake
   * cannot carry an HTTP error, so the probe finds out why. The event detail
   * is not needed: the probe and the event stream tell more.
   */
  function disconnected() {
    if (state.value !== 'connecting' && state.value !== 'connected') return;
    const wasConnected = state.value === 'connected';
    generation += 1;
    const mine = generation;
    clearTimeout(stableTimer);
    stableTimer = null;
    connection.value = null;
    state.value = 'checking';
    getVncSession(exp(), name(), { probe: true }).then(
      ({ data }) => {
        if (mine !== generation) return;
        info.value = data;
        if (wasConnected) scheduleRetry('fast', 'The connection was lost');
        else scheduleRetry('slow', NOT_AVAILABLE);
      },
      (err) => mine === generation && failed(err, true),
    );
  }

  /**
   * The console reports the RFB `securityfailure` event. QEMU has no VNC
   * authentication, so the session ends.
   *
   * @param {string} [reason]
   */
  function securityFailure(reason) {
    end(
      'failed',
      `The VNC server refused the connection${reason ? `: ${reason}` : ''}`,
    );
  }

  /** Retries at once, as a Reconnect button does. */
  function retryNow() {
    failures = 0;
    start();
  }

  function onVmEvent(action) {
    if (VM_GONE.has(action)) {
      paused.value = false;
      return end('notRunning', NOT_RUNNING);
    }
    switch (action) {
      case 'start':
        paused.value = false;
        if (state.value === 'waiting' || ENDED.has(state.value)) retryNow();
        return;
      case 'stop':
        paused.value = true;
        return;
      case 'redeployed':
        paused.value = false;
        return retryNow();
      case 'errorRedeploying':
        if (state.value === 'notRunning') retryNow();
        break;
      // update, reset and the other error actions keep the console as it
      // is: a closed connection runs the normal reconnect
    }
  }

  function handleWsMessage(msg) {
    const resource = msg?.resource;
    if (!resource || state.value === 'idle') return;
    if (
      resource.type === 'experiment/vm' &&
      resource.name === `${exp()}/${name()}`
    ) {
      onVmEvent(resource.action);
    } else if (
      resource.type === 'experiment' &&
      resource.name === exp() &&
      resource.action === 'stop'
    ) {
      paused.value = false;
      end('experimentStopped', EXPERIMENT_STOPPED);
    }
  }

  function dispose() {
    if (disposed) return;
    disposed = true;
    stopWatch();
    removeWsHandler(handleWsMessage);
    end('idle', '');
  }

  addWsHandler(handleWsMessage);
  const stopWatch = watch(
    () => [exp(), name(), Boolean(toValue(enabled))],
    () => {
      failures = 0;
      start();
    },
    { immediate: true },
  );

  if (getCurrentInstance()) onBeforeUnmount(dispose);
  else if (getCurrentScope()) onScopeDispose(dispose);

  return {
    state,
    paused,
    info,
    connection,
    message,
    retrySeconds,
    connected,
    disconnected,
    securityFailure,
    retryNow,
    dispose,
  };
}
