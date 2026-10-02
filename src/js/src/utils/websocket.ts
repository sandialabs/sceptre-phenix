/// <reference types="vite/types/importMeta.d.ts" />
import { usePhenixStore } from '@/store.js';
import { debug } from '@/utils/debug.js';
import { ToastProgrammatic as Toast } from 'buefy';

let globalWs: WebSocket = null;
const wsListeners: Function[] = [];
const reconnectListeners: (() => void)[] = [];

// set once the socket has opened, so later opens are reconnects
let everConnected: boolean = false;

var shouldBeConnected: boolean = false;
var errorToast = null;
var numFailedConnects: number = 0;

// Handle for a pending reconnect, so a disconnect (e.g. logout) can cancel a
// reconnect that was already scheduled.
let reconnectTimer: ReturnType<typeof setTimeout> = null;

// Messages requested while the socket isn't OPEN yet; flushed on open.
const pendingMessages: string[] = [];

// Cap the exponential backoff so a long outage doesn't push the retry interval
// out to minutes/hours, and add jitter to avoid synchronized reconnect storms.
const MAX_RECONNECT_DELAY_S = 30;

function reconnectDelayMs(): number {
  const base = Math.min(2 ** numFailedConnects, MAX_RECONNECT_DELAY_S);
  return base * 1000 + Math.random() * 1000;
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

export function connectWebsocket(): void {
  shouldBeConnected = true;
  globalWs = new WebSocket(getUrl());
  globalWs.onmessage = globalWsMessageHandler;

  globalWs.onopen = () => {
    debug('connected websocket');
    numFailedConnects = 0;

    if (errorToast !== null) {
      errorToast.close();
      errorToast = null;
    }

    // flush anything queued while the socket was connecting
    const queued = pendingMessages.splice(0);
    queued.forEach((msg) => globalWs.send(msg));

    // the server forgets a client's subscriptions when its socket closes
    const reconnected = everConnected;
    everConnected = true;
    if (reconnected) {
      reconnectListeners.slice().forEach((listener) => {
        try {
          listener();
        } catch (err) {
          console.error('websocket reconnect listener error', err);
        }
      });
    }
  };

  globalWs.onclose = () => {
    if (!shouldBeConnected) {
      return;
    }

    if (errorToast === null) {
      errorToast = new Toast().open({
        message: `websocket connection closed. Trying to reconnect...`,
        type: 'is-warning',
        indefinite: true,
      });
    }

    const delay = reconnectDelayMs();
    debug(`next websocket reconnect attempt in ${Math.round(delay / 1000)}s`);
    numFailedConnects += 1;
    reconnectTimer = setTimeout(connectWebsocket, delay);
  };
}

export function disconnectWebsocket(): void {
  shouldBeConnected = false;
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
    pendingMessages.push(data);
    return;
  }

  if (shouldBeConnected) {
    debug('websocket is null, connecting before send');
    pendingMessages.push(data);
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

// Calls f each time the socket reopens after a reconnect (not on the first
// connect). Returns a function that removes it.
export function onWsReconnect(f: () => void): () => void {
  reconnectListeners.push(f);
  return () => {
    const i = reconnectListeners.indexOf(f);
    if (i >= 0) {
      reconnectListeners.splice(i, 1);
    }
  };
}

function globalWsMessageHandler(event: MessageEvent): void {
  event.data.split(/\r?\n/).forEach((data) => {
    if (data) {
      let msg = JSON.parse(data);
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
    disconnectWebsocket();
    connectWebsocket();
  });
}
