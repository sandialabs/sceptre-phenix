// Opening the console page in a new window. A second window.open on a named
// window that is open navigates that window and loses its session, so each
// console tab gets a unique name. Before it opens one, the UI asks the open
// console tabs whether one already shows the same VMs.
import { SnackbarProgrammatic as Snackbar } from 'buefy';

import { LONG_TOAST_DURATION } from '@/utils/toast.js';

/** The BroadcastChannel of the console tabs. */
export const CONSOLE_CHANNEL = 'phenix-vnc';
/** How long the query waits for a console tab to answer, in milliseconds. */
export const QUERY_WAIT_MS = 150;
export const DUPLICATE_MESSAGE = 'This console is already open in another tab';
export const OPEN_ANOTHER = 'Open another anyway';
export const BLOCKED_MESSAGE = 'The browser blocked the new console tab';
export const OPEN_BLOCKED = 'Open the console';

const channelOf = () =>
  typeof globalThis.BroadcastChannel === 'function'
    ? new globalThis.BroadcastChannel(CONSOLE_CHANNEL)
    : null;

const randomHex = (bytes) =>
  Array.from(crypto.getRandomValues(new Uint8Array(bytes)), (b) =>
    b.toString(16).padStart(2, '0'),
  ).join('');

// a channel gets the messages of the other channels in its own tab too
const TAB_ID = randomHex(8);

/** A window name that no other console tab has. */
export function newConsoleWindowName() {
  return `phenix-vnc-${randomHex(8)}`;
}

const sameVms = (a, b) => {
  const set = new Set(a);
  return set.size === new Set(b).size && b.every((vm) => set.has(vm));
};

function askConsoleTabs(vms) {
  const channel = channelOf();
  if (!channel) return Promise.resolve(false);
  const id = randomHex(8);
  return new Promise((resolve) => {
    const done = (found) => {
      clearTimeout(timer);
      channel.close();
      resolve(found);
    };
    const timer = setTimeout(() => done(false), QUERY_WAIT_MS);
    channel.onmessage = ({ data }) => {
      if (data?.type === 'here' && data.id === id) done(true);
    };
    channel.postMessage({ type: 'query', id, tab: TAB_ID, vms: [...vms] });
  });
}

const showSnackbar = ({ message, actionText, onAction }) =>
  new Snackbar().open({
    message,
    actionText,
    onAction,
    type: 'is-warning',
    position: 'is-bottom',
    duration: LONG_TOAST_DURATION,
  });

/**
 * Opens a console window, unless a console tab already shows the same VMs.
 * It opens without `noopener`, so the browser copies sessionStorage and the
 * new tab has the session token. The console page then calls
 * detachOpener().
 *
 * @param {string} href the console page URL
 * @param {string[]} vms the VMs of the console, as `exp/vm`
 * @param {object} [options]
 * @param {(toast: { message: string, actionText: string,
 *   onAction: () => void }) => void} [options.notify] shows the duplicate
 *   toast and the blocked toast; a Buefy snackbar if not given
 * @param {(url: string, name: string) => Window|null} [options.open]
 * @returns {Promise<'opened'|'duplicate'|'blocked'>} blocked when the
 *   browser did not open the window. Then a toast offers to open it again.
 */
export async function openConsole(
  href,
  vms,
  { notify = showSnackbar, open = (...args) => window.open(...args) } = {},
) {
  // a click on the toast action lets the browser open the window
  const openWindow = () => {
    if (open(href, newConsoleWindowName())) return true;
    notify({
      message: BLOCKED_MESSAGE,
      actionText: OPEN_BLOCKED,
      onAction: openWindow,
    });
    return false;
  };
  if (await askConsoleTabs(vms)) {
    notify({
      message: DUPLICATE_MESSAGE,
      actionText: OPEN_ANOTHER,
      onAction: openWindow,
    });
    return 'duplicate';
  }
  return openWindow() ? 'opened' : 'blocked';
}

/**
 * The click handler of a console link that keeps its real `href`. A plain
 * click opens through openConsole. A middle click or a click with a modifier
 * key does what the browser does.
 *
 * @param {MouseEvent} event the click on the link
 * @param {string[]} vms the VMs of the console, as `exp/vm`
 * @param {Parameters<typeof openConsole>[2]} [options]
 */
export function handleConsoleLinkClick(event, vms, options) {
  if (
    event.button !== 0 ||
    event.ctrlKey ||
    event.metaKey ||
    event.shiftKey ||
    event.altKey
  ) {
    return;
  }
  event.preventDefault();
  openConsole(event.currentTarget.href, vms, options);
}

/**
 * Answers the duplicate query of other tabs while a console page is open. It
 * does not answer the queries of its own tab.
 *
 * @param {() => string[]} currentVms the VMs that the page shows now
 * @returns {() => void} stops answering
 */
export function answerConsoleQueries(currentVms) {
  const channel = channelOf();
  if (!channel) return () => {};
  channel.onmessage = ({ data }) => {
    if (data?.type !== 'query' || !Array.isArray(data.vms)) return;
    if (data.tab === TAB_ID) return;
    if (sameVms(currentVms(), data.vms)) {
      channel.postMessage({ type: 'here', id: data.id });
    }
  };
  return () => channel.close();
}

/** Lets go of the window that opened the console page. */
export function detachOpener() {
  window.opener = null;
}
