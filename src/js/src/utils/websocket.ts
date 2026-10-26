/// <reference types="vite/types/importMeta.d.ts" />
import { usePhenixStore } from '@/store.js';
import { debug } from '@/utils/debug.js';
import {
  afterSpread,
  forgetServer,
  retryAfterLeftMs,
  setWsDown,
  wsCloseCause,
  wsDownReason,
} from '@/utils/freshness.js';
import { ToastProgrammatic as Toast } from 'buefy';

let globalWs: WebSocket = null;
const wsListeners: Function[] = [];
// spread ones send requests, so they are called spread out after a reconnect
type ReconnectListener = { f: () => void; spread: boolean };
const reconnectListeners: ReconnectListener[] = [];

// set once a socket has stayed up, so later ones are reconnects
let everConnected: boolean = false;
// whether the current socket has stayed up (see connectionBack)
let socketUp: boolean = false;

var shouldBeConnected: boolean = false;
var errorToast = null;
var errorToastMessage: string = null;
var numFailedConnects: number = 0;

// Handle for a pending reconnect, so a disconnect (e.g. logout) can cancel a
// reconnect that was already scheduled.
let reconnectTimer: ReturnType<typeof setTimeout> = null;

// A socket that opens counts as back once it has stayed open this long or
// brought a message: a server shutting down may accept and close it again.
const STABLE_AFTER_MS = 1500;
let stableTimer: ReturnType<typeof setTimeout> = null;

// Messages requested while the socket isn't OPEN yet; flushed on open. A
// later message about the same resource and action replaces an earlier one.
const pendingMessages: { key: string | null; data: string }[] = [];
const MAX_PENDING_MESSAGES = 100;

// reconnect listener calls spread out after a reconnect, not yet made
let pendingReconnects: (() => void)[] = [];

function cancelPendingReconnects(): void {
  pendingReconnects.forEach((cancel) => cancel());
  pendingReconnects = [];
}

// What the warning says while the socket is down, by why it closed (see
// wsCloseCause).
const DOWN_MESSAGES = {
  shutdown:
    'The phēnix server is shutting down or restarting. The UI will reconnect when it is back.',
  behind: 'Live updates from the phēnix server fell behind. Reconnecting…',
  lost: 'Lost the connection to the phēnix server. It may be down, or your network may have dropped. Reconnecting…',
};

function showDownWarning(message: string): void {
  if (errorToast !== null && errorToastMessage === message) return;
  errorToast?.close();
  errorToast = new Toast().open({
    message,
    type: 'is-warning',
    indefinite: true,
  });
  errorToastMessage = message;
}

function closeDownWarning(): void {
  errorToast?.close();
  errorToast = null;
  errorToastMessage = null;
}

function cancelStableTimer(): void {
  if (stableTimer !== null) {
    clearTimeout(stableTimer);
    stableTimer = null;
  }
}

// Cap the exponential backoff so a long outage doesn't push the retry interval
// out to minutes/hours, and add jitter to avoid synchronized reconnect storms.
const MAX_RECONNECT_DELAY_S = 30;
// the least wait before reconnecting to a server that is shutting down
const SHUTDOWN_RECONNECT_MS = 5000;

function reconnectDelayMs(): number {
  const base = Math.min(2 ** numFailedConnects, MAX_RECONNECT_DELAY_S);
  // a 503's Retry-After also says when the server may take a socket again
  const delay = Math.max(
    base * 1000 + Math.random() * 1000,
    retryAfterLeftMs(),
  );
  return wsDownReason() === 'shutdown'
    ? Math.max(delay, SHUTDOWN_RECONNECT_MS)
    : delay;
}

function getUrl(): string {
  const store = usePhenixStore();
  let path = `${import.meta.env.BASE_URL}api/v1/ws`;
  if (store.token) {
    path += `?token=${store.token}`;
  }
  let proto = location.protocol == 'https:' ? 'wss://' : 'ws://';
  return proto + location.host + path;
}

export function isWsConnected(): boolean {
  return globalWs !== null;
}

// The open socket has stayed up: the connection is back.
function connectionBack(): void {
  cancelStableTimer();
  if (socketUp) return;
  socketUp = true;
  numFailedConnects = 0;
  setWsDown(false);

  const recovered = errorToast !== null && everConnected;
  closeDownWarning();
  if (recovered) {
    new Toast().open({
      message: 'Reconnected to the phēnix server.',
      type: 'is-success',
      duration: 3000,
    });
  }

  // the server forgets a client's subscriptions when its socket closes
  const reconnected = everConnected;
  everConnected = true;
  if (!reconnected) return;
  cancelPendingReconnects();
  for (const listener of [...reconnectListeners]) {
    const call = () => {
      // removed since
      if (!reconnectListeners.includes(listener)) return;
      try {
        listener.f();
      } catch (err) {
        console.error('websocket reconnect listener error', err);
      }
    };
    // so a restarted server is not asked for everything at once
    if (listener.spread) pendingReconnects.push(afterSpread(call));
    else call();
  }
}

export function connectWebsocket(): void {
  shouldBeConnected = true;
  cancelStableTimer();
  socketUp = false;
  const ws = new WebSocket(getUrl());
  globalWs = ws;

  // a socket since replaced (by a reconnect or a disconnect) is ignored
  ws.onmessage = (event: MessageEvent) => {
    if (ws !== globalWs) return;
    connectionBack();
    globalWsMessageHandler(event);
  };

  ws.onopen = () => {
    if (ws !== globalWs) return;
    debug('connected websocket');

    // flush anything queued while the socket was connecting
    const queued = pendingMessages.splice(0);
    queued.forEach((msg) => ws.send(msg.data));

    stableTimer = setTimeout(connectionBack, STABLE_AFTER_MS);
  };

  ws.onclose = (event: CloseEvent) => {
    if (ws !== globalWs || !shouldBeConnected) {
      return;
    }
    cancelStableTimer();
    cancelPendingReconnects();
    socketUp = false;

    // live pages may miss updates until the socket reopens
    setWsDown(true, wsCloseCause(event));
    showDownWarning(DOWN_MESSAGES[wsDownReason()] ?? DOWN_MESSAGES.lost);

    const delay = reconnectDelayMs();
    debug(`next websocket reconnect attempt in ${Math.round(delay / 1000)}s`);
    numFailedConnects += 1;
    reconnectTimer = setTimeout(connectWebsocket, delay);
  };
}

export function disconnectWebsocket(): void {
  shouldBeConnected = false;
  cancelStableTimer();
  socketUp = false;
  everConnected = false;
  numFailedConnects = 0;
  cancelPendingReconnects();
  // nothing waiting on the server's return should run after signing out
  forgetServer();
  closeDownWarning();
  debug('disconnected websocket');

  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }

  if (globalWs) {
    globalWs.close();
    globalWs = null;
  }
}

// the resource and action a message is about, or null for one without
function messageKey(payload: any): string | null {
  const resource = payload?.resource;
  if (!resource || typeof resource !== 'object') return null;
  return JSON.stringify([resource.type, resource.name, resource.action]);
}

function queueMessage(payload: object, data: string): void {
  const key = messageKey(payload);
  const i =
    key === null ? -1 : pendingMessages.findIndex((msg) => msg.key === key);
  if (i >= 0) pendingMessages.splice(i, 1);
  pendingMessages.push({ key, data });
  if (pendingMessages.length > MAX_PENDING_MESSAGES) pendingMessages.shift();
}

export function sendWsMsg(payload: object): void {
  const data = JSON.stringify(payload);

  if (globalWs !== null && globalWs.readyState === WebSocket.OPEN) {
    globalWs.send(data);
    return;
  }

  if (globalWs !== null) {
    // socket exists but is still CONNECTING (or closing) — queue and let onopen
    // flush it rather than calling send() in an invalid state.
    debug('websocket not open, queueing message');
    queueMessage(payload, data);
    return;
  }

  if (shouldBeConnected) {
    debug('websocket is null, connecting before send');
    queueMessage(payload, data);
    connectWebsocket();
    return;
  }

  console.warn('not connected to send websocket message');
}

export function addWsHandler(f: (msg: object) => void): void {
  wsListeners.push(f);
}

export function removeWsHandler(f: (msg: object) => void): void {
  const i = wsListeners.indexOf(f);
  // splice(-1, 1) would silently drop the most recently added listener
  if (i >= 0) {
    wsListeners.splice(i, 1);
  }
}

// Calls f after each reconnect (not the first connect), before the new socket's
// messages, or within BACK_SPREAD_MS with spread; returns its remover.
export function onWsReconnect(
  f: () => void,
  { spread = false }: { spread?: boolean } = {},
): () => void {
  const listener = { f, spread };
  reconnectListeners.push(listener);
  return () => {
    const i = reconnectListeners.indexOf(listener);
    if (i >= 0) {
      reconnectListeners.splice(i, 1);
    }
  };
}

function globalWsMessageHandler(event: MessageEvent): void {
  event.data.split(/\r?\n/).forEach((data) => {
    if (data) {
      let msg;
      try {
        msg = JSON.parse(data);
      } catch (err) {
        console.error('websocket message is not JSON', err);
        return;
      }
      // a line that is not an object is no message; skip it, not the rest
      if (msg === null || typeof msg !== 'object') return;
      // debug() is development-only: logging every message retains large
      // payloads (screenshots) in the devtools console
      debug('websocket msg (' + wsListeners.length + ' listeners):\n', msg);

      // dispatch to listeners; one failing listener must not block the rest
      wsListeners.forEach((listener) => {
        try {
          listener(msg);
        } catch (err) {
          console.error('websocket listener error', err);
        }
      });

      // global handlers
      if (
        msg.resource?.type === 'apps/scorch' &&
        msg.resource?.action === 'terminal-create'
      ) {
        new Toast().open({
          message: `Scorch terminal created for experiment ${msg.resource.name}`,
          type: 'is-success',
          duration: 5000,
        });
      }
    }
  });
}

// in dev, disconnect and reconnect ws after hot reloads
if (import.meta.hot) {
  import.meta.hot.on('vite:afterUpdate', () => {
    // the pages still open subscribe again once the new socket is up
    const connected = everConnected;
    disconnectWebsocket();
    everConnected = connected;
    connectWebsocket();
  });
}
