// The shared websocket: handing messages to the pages' handlers, sending
// what pages ask before the socket opens, and reconnecting after it drops.
import fc from 'fast-check';
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

  // a browser refuses to send on a socket that is not open
  send(data) {
    if (this.readyState !== FakeWebSocket.OPEN) throw new Error('not open');
    this.sent.push(data);
  }

  close() {}

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen();
  }

  // the connection drops, or fails to open
  drop() {
    this.readyState = 3;
    this.onclose();
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
  sockets.at(-1).drop();
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

  it('reach a handler whole and in order, whatever ends their lines', () => {
    const seen = [];
    ws.addWsHandler((msg) => seen.push(msg));
    ws.connectWebsocket();
    sockets[0].open();
    const line = fc.record({
      msg: fc.dictionary(fc.string(), fc.jsonValue(), { maxKeys: 4 }),
      end: fc.constantFrom('\n', '\r\n', '\n\n'),
    });
    fc.assert(
      fc.property(fc.array(line, { maxLength: 6 }), (lines) => {
        seen.length = 0;
        sockets[0].onmessage({
          data: lines.map((l) => JSON.stringify(l.msg) + l.end).join(''),
        });
        // as JSON reads them, which makes -0 a 0
        expect(seen).toEqual(
          lines.map((l) => JSON.parse(JSON.stringify(l.msg))),
        );
      }),
    );
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

  it('are each sent once, in order, across sockets that drop and reopen', async () => {
    const step = fc.oneof(
      fc.constantFrom('drop', 'open'),
      fc.integer().map((n) => ({ n })),
    );
    await fc.assert(
      fc.asyncProperty(fc.array(step, { maxLength: 20 }), async (steps) => {
        ws.disconnectWebsocket();
        vi.resetModules();
        sockets.length = 0;
        ws = await import('@/utils/websocket');
        ws.connectWebsocket();
        const asked = [];
        for (const step of [...steps, 'open']) {
          const socket = sockets.at(-1);
          if (step === 'drop') {
            socket.drop();
            vi.runAllTimers();
          } else if (step === 'open') {
            if (socket.readyState === 0) socket.open();
          } else {
            ws.sendWsMsg(step);
            asked.push(JSON.stringify(step));
          }
        }
        expect(sockets.flatMap((s) => s.sent)).toEqual(asked);
      }),
      { numRuns: 50 },
    );
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
