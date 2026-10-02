// The shared websocket: handing messages to the pages' handlers, sending
// what pages ask before the socket opens, and reconnecting after it drops.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/debug.js', () => ({ debug: () => {} }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({ token: null }) }));
vi.mock('buefy', () => ({
  ToastProgrammatic: class {
    open() {
      return { close() {} };
    }
  },
}));

class FakeWebSocket {
  static OPEN = 1;
  static instances = [];

  constructor(url) {
    this.url = url;
    this.readyState = 0;
    this.sent = [];
    FakeWebSocket.instances.push(this);
  }

  send(data) {
    this.sent.push(data);
  }

  close() {}

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen();
  }

  // the server sends one or more messages, a line each
  receive(...messages) {
    this.onmessage({ data: messages.map((m) => JSON.stringify(m)).join('\n') });
  }
}

const sockets = FakeWebSocket.instances;

let ws;

beforeEach(async () => {
  // a fresh module, with no socket, handlers or listeners yet
  vi.resetModules();
  vi.useFakeTimers();
  sockets.length = 0;
  vi.stubGlobal('WebSocket', FakeWebSocket);
  vi.stubGlobal('location', { protocol: 'http:', host: 'localhost' });
  vi.spyOn(console, 'error').mockImplementation(() => {});
  ws = await import('@/utils/websocket');
});

afterEach(() => {
  ws.disconnectWebsocket();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// drops the open socket and lets the reconnect happen
function reconnect() {
  sockets.at(-1).onclose();
  vi.runAllTimers();
  sockets.at(-1).open();
}

describe('messages', () => {
  it('reach every handler, one line at a time, past a handler that throws', () => {
    const seen = [];
    ws.addWsHandler(() => {
      throw new Error('boom');
    });
    ws.addWsHandler((msg) => seen.push(msg.n));
    ws.connectWebsocket();
    sockets[0].open();

    sockets[0].receive({ n: 1 }, { n: 2 });
    expect(seen).toEqual([1, 2]);
    expect(console.error).toHaveBeenCalledTimes(2);
  });

  it('stop reaching a removed handler, and removing an unknown one leaves the rest', () => {
    const kept = vi.fn();
    const removed = vi.fn();
    ws.addWsHandler(kept);
    ws.addWsHandler(removed);
    ws.removeWsHandler(removed);
    ws.removeWsHandler(() => {});
    ws.connectWebsocket();
    sockets[0].open();

    sockets[0].receive({ n: 1 });
    expect(kept).toHaveBeenCalledTimes(1);
    expect(removed).not.toHaveBeenCalled();
  });

  it('asked for before the socket opens are sent once it does', () => {
    ws.connectWebsocket();
    ws.sendWsMsg({ n: 1 });
    expect(sockets[0].sent).toEqual([]);

    sockets[0].open();
    ws.sendWsMsg({ n: 2 });
    expect(sockets[0].sent).toEqual(['{"n":1}', '{"n":2}']);
  });

  it('are dropped while no socket is wanted', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    ws.sendWsMsg({ n: 1 });
    expect(sockets).toHaveLength(0);
    expect(warn).toHaveBeenCalled();
  });
});

describe('reconnecting', () => {
  it('tells listeners after a reconnect but not on the first connect', () => {
    const listener = vi.fn();
    ws.onWsReconnect(listener);

    ws.connectWebsocket();
    sockets[0].open();
    expect(listener).not.toHaveBeenCalled();

    reconnect();
    expect(listener).toHaveBeenCalledTimes(1);
    expect(sockets).toHaveLength(2);
  });

  it('stops telling a listener once it unsubscribes', () => {
    const listener = vi.fn();
    const off = ws.onWsReconnect(listener);
    ws.connectWebsocket();
    sockets[0].open();

    off();
    reconnect();
    expect(listener).not.toHaveBeenCalled();
  });

  it('keeps telling other listeners when one throws', () => {
    const listener = vi.fn();
    ws.onWsReconnect(() => {
      throw new Error('boom');
    });
    ws.onWsReconnect(listener);
    ws.connectWebsocket();
    sockets[0].open();

    reconnect();
    expect(listener).toHaveBeenCalledTimes(1);
    expect(console.error).toHaveBeenCalled();
  });

  it('is called off by disconnecting, as signing out does', () => {
    ws.connectWebsocket();
    sockets[0].open();

    sockets[0].onclose();
    ws.disconnectWebsocket();
    vi.runAllTimers();
    expect(sockets).toHaveLength(1);
  });
});
