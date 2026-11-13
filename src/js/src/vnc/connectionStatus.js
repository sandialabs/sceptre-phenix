// The words of the connection indicator at the right end of the console top
// bar, its tooltip, and the announcements of the aria-live region.
import { EXPERIMENT_STOPPED, NOT_RUNNING } from './useVncSession.js';

const LABELS = {
  idle: 'Not connected',
  loading: 'Connecting',
  connecting: 'Connecting',
  connected: 'Connected',
  checking: 'Checking the connection',
  waiting: 'Reconnecting',
  notRunning: NOT_RUNNING,
  experimentStopped: EXPERIMENT_STOPPED,
  denied: 'No permission',
  notFound: 'VM not found',
  failed: 'Connection failed',
};

const TONES = {
  idle: 'off',
  loading: 'busy',
  connecting: 'busy',
  connected: 'ok',
  checking: 'busy',
  waiting: 'wait',
};

// the states that wait for an event or the Reconnect button
const RECONNECTABLE = new Set([
  'waiting',
  'notRunning',
  'experimentStopped',
  'denied',
  'notFound',
  'failed',
]);

/**
 * The label of the indicator, for example "Connected" or "Reconnecting in
 * 5 s".
 *
 * @param {{ state: string, retrySeconds?: number|null }} status
 */
export function connectionLabel({ state, retrySeconds }) {
  if (state === 'waiting' && retrySeconds != null) {
    return `Reconnecting in ${retrySeconds} s`;
  }
  return LABELS[state] ?? LABELS.idle;
}

/**
 * The color of the indicator dot: off, busy, ok, wait or error.
 *
 * @param {string} state
 */
export function connectionTone(state) {
  return TONES[state] ?? (LABELS[state] ? 'error' : 'off');
}

/**
 * Whether the indicator offers the Reconnect button in this state.
 *
 * @param {string} state
 */
export function showsReconnect(state) {
  return RECONNECTABLE.has(state);
}

/**
 * What the aria-live region says when the state of a pane changes. A wait
 * leaves out the countdown, which changes each second.
 *
 * @param {string} vm the VM name
 * @param {{ state: string }} status
 */
export function announcement(vm, { state }) {
  return `${vm}: ${LABELS[state] ?? LABELS.idle}`;
}

/** The encoding that QEMU picks from the list that noVNC sends. */
export const ENCODING = 'Tight';

const SCALING_TEXT = {
  fit: 'scaled to fit',
  actual: 'actual size',
  remote: 'VM resized to fit',
};

/**
 * How the console scales the screen, for example "scaled 118%".
 *
 * @param {{ scaling: string, scale?: number|null }} details
 */
export function scalingText({ scaling, scale }) {
  if (scaling === 'fit' && scale) return `scaled ${Math.round(scale * 100)}%`;
  return SCALING_TEXT[scaling] ?? SCALING_TEXT.fit;
}

/** The copy/paste mode of the VM, as the console names it. */
export const CLIPBOARD_MODES = Object.freeze({
  pasteAsKeys: 'paste as keys',
  bidirectional: 'bidirectional copy/paste',
});

/**
 * The tooltip of the indicator: the resolution, the scaling, the encoding,
 * the quality and the copy/paste mode, for example
 * "1024×768 · scaled 118% · Tight · quality 6 · paste as keys".
 *
 * @param {{ width?: number, height?: number, scale?: number|null,
 *   scaling: string, quality: number, clipboardMode?: string|null }} details
 */
export function detailsLine(details) {
  const parts = [];
  if (details.width && details.height) {
    parts.push(`${details.width}×${details.height}`);
  }
  parts.push(scalingText(details), ENCODING, `quality ${details.quality}`);
  const mode = CLIPBOARD_MODES[details.clipboardMode];
  if (mode) parts.push(mode);
  return parts.join(' · ');
}
