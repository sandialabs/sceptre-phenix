/// <reference types="vite/types/importMeta.d.ts" />
import { usePhenixStore } from '@/store.js';
import { ToastProgrammatic as Toast } from 'buefy';

let globalWs: WebSocket = null;
const wsListeners: Function[] = [];

var shouldBeConnected = false;
var errorToast = null;

function getUrl() {
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

  ['onclose', 'onerror', 'onopen'].forEach((eventType) => {
    globalWs[eventType] = (event) => {
      if (eventType === 'onclose' && shouldBeConnected) {
        if (errorToast === null) {
          errorToast = new Toast().open({
            message: `websocket connection closed. Trying to reconnect...`,
            type: 'is-warning',
            indefinite: true,
          });
        }

        setTimeout(() => {
          connectWebsocket();
        }, 1000);
      } else if (eventType === 'onopen') {
        console.log('connected websocket');
        if (errorToast !== null) {
          errorToast.close();
          errorToast = null;
        }
      } else if (eventType === 'onerror') {
        console.warn('websocket error: ', event);
      }
    };
  });
}

export function disconnectWebsocket(): void {
  shouldBeConnected = false;
  console.log('disconnect websocket');
  if (globalWs) {
    globalWs.close();
    globalWs = null;
  }
}

export function sendWsMsg(payload: object) {
  if (globalWs !== null) {
    globalWs.send(JSON.stringify(payload));
  } else if (shouldBeConnected) {
    console.warn('websocket is null, connecting before send');
    connectWebsocket();
    globalWs.send(JSON.stringify(payload));
  } else {
    console.log('not connected to send websocket message');
  }
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
      console.debug('WS: ' + data);

      // dispatch to listeners
      wsListeners.forEach((listener) => listener(msg));

      // global handlers
      if (
        msg.resource.type === 'apps/scorch' &&
        msg.resource.action === 'terminal-create'
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
