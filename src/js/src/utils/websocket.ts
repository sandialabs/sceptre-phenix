/// <reference types="vite/types/importMeta.d.ts" />
import { usePhenixStore } from '@/store.js';
import { ToastProgrammatic as Toast } from 'buefy';

let globalWs: WebSocket = null;
const wsListeners: Function[] = [];

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
    console.log('connected websocket');
    numFailedConnects = 0;

    if (errorToast !== null) {
      errorToast.close();
      errorToast = null;
    }

    // flush anything queued while the socket was connecting
    const queued = pendingMessages.splice(0);
    queued.forEach((msg) => globalWs.send(msg));
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
    console.log(
      `next websocket reconnect attempt in ${Math.round(delay / 1000)}s`,
    );
    numFailedConnects += 1;
    reconnectTimer = setTimeout(connectWebsocket, delay);
  };

  globalWs.onerror = (event) => {
    console.warn('websocket error: ', event);
  };
}

export function disconnectWebsocket(): void {
  shouldBeConnected = false;
  console.log('disconnected websocket');

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
    console.warn('websocket not open, queueing message');
    pendingMessages.push(data);
    return;
  }

  if (shouldBeConnected) {
    console.warn('websocket is null, connecting before send');
    pendingMessages.push(data);
    connectWebsocket();
    return;
  }

  console.log('not connected to send websocket message');
}

export function addWsHandler(f: (msg: object) => void): void {
  wsListeners.push(f);
}

export function removeWsHandler(f: (msg: object) => void): void {
  wsListeners.splice(wsListeners.indexOf(f), 1);
}

function globalWsMessageHandler(event: MessageEvent): void {
  event.data.split(/\r?\n/).forEach((data) => {
    if (data) {
      let msg = JSON.parse(data);
      console.debug(
        'websocket msg (' + wsListeners.length + ' listeners):\n',
        msg,
      );

      // dispatch to listeners
      wsListeners.forEach((listener) => listener(msg));

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
