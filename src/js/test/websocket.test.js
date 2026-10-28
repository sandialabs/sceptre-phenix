// The shared websocket: handing messages to the pages' handlers, sending
// what pages ask before the socket opens, and reconnecting after it drops.
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BACK_SPREAD_MS } from '@/utils/freshness.js';

vi.mock('@/utils/debug.js', () => ({ debug: () => {} }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({ token: null }) }));
// every toast opened, with whether it has been closed
const toasts = vi.hoisted(() => []);
vi.mock('buefy', () => ({
  ToastProgrammatic: class {
    open(options) {
      const toast = { ...options, closed: false };
      toasts.push(toast);
      return {
        close() {
          toast.closed = true;
        },
      };
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

  // opens and stays open long enough to count as back
  settle() {
    this.open();
    vi.advanceTimersByTime(1500);
  }

  // the connection drops without a close frame, or fails to open
  drop() {
    this.closeWith(1006, '');
  }

  // the server closes the connection with a close frame
  closeWith(code, reason) {
    this.readyState = 3;
    this.onclose({ code, reason });
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
  toasts.length = 0;
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

// drops the open socket and lets the reconnect happen and settle, and the
// reconnect listeners, spread out, run
function reconnect() {
  sockets.at(-1).drop();
  vi.runAllTimers();
  sockets.at(-1).settle();
  vi.advanceTimersByTime(BACK_SPREAD_MS);
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

  it('skip a line that is not a JSON object, and reach the lines after it', () => {
    const seen = [];
    ws.addWsHandler((msg) => seen.push(msg));
    ws.connectWebsocket();
    sockets[0].open();
    const line = fc.oneof(
      fc
        .dictionary(fc.string(), fc.jsonValue(), { maxKeys: 3 })
        .map((msg) => ({ msg, text: JSON.stringify(msg) })),
      fc
        .oneof(
          fc.constantFrom('null', '42', '"text"', 'true', '{', 'not json'),
          fc.string().filter((t) => !t.includes('\n') && !t.includes('\r')),
        )
        .filter((text) => {
          try {
            const v = JSON.parse(text);
            return v === null || typeof v !== 'object';
          } catch {
            return true;
          }
        })
        .map((text) => ({ text })),
    );
    fc.assert(
      fc.property(fc.array(line, { maxLength: 6 }), (lines) => {
        seen.length = 0;
        sockets[0].onmessage({ data: lines.map((l) => l.text).join('\n') });
        expect(seen).toEqual(
          lines.filter((l) => l.msg).map((l) => JSON.parse(l.text)),
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

  it('asked for again about the same resource while the socket is down replace the earlier ones', () => {
    ws.connectWebsocket();
    sockets[0].settle();
    sockets[0].drop();

    const list = (page) => ({
      resource: { type: 'experiment/vms', name: 'exp', action: 'list' },
      request: { page_number: page },
    });
    const unsubscribe = {
      resource: { type: 'experiment/vms', name: 'exp', action: 'unsubscribe' },
    };
    ws.sendWsMsg(list(1));
    ws.sendWsMsg(unsubscribe);
    ws.sendWsMsg(list(2));
    ws.sendWsMsg({ n: 1 });
    ws.sendWsMsg({ n: 1 });
    ws.sendWsMsg(list(3));

    vi.runAllTimers();
    sockets[1].open();
    expect(sockets[1].sent.map((m) => JSON.parse(m))).toEqual([
      unsubscribe,
      { n: 1 },
      { n: 1 },
      list(3),
    ]);
  });

  it('queued while the socket is down are bounded, keeping the latest', () => {
    ws.connectWebsocket();
    sockets[0].settle();
    sockets[0].drop();
    for (let n = 0; n < 150; n++) ws.sendWsMsg({ n });

    vi.runAllTimers();
    sockets[1].open();
    expect(sockets[1].sent).toHaveLength(100);
    expect(JSON.parse(sockets[1].sent.at(-1))).toEqual({ n: 149 });
  });

  it('longer than the server reads are never sent, so it never closes the socket', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    ws.connectWebsocket();
    sockets[0].open();
    const bytes = (msg) => new TextEncoder().encode(JSON.stringify(msg)).length;
    fc.assert(
      fc.property(fc.string({ unit: 'binary', maxLength: 1100 }), (text) => {
        const sent = sockets[0].sent.length;
        ws.sendWsMsg({ filter: text });
        const wasSent = sockets[0].sent.length > sent;
        expect(wasSent).toBe(bytes({ filter: text }) <= 2048);
      }),
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
    sockets[0].settle();
    expect(listener).not.toHaveBeenCalled();

    reconnect();
    expect(listener).toHaveBeenCalledTimes(1);
    expect(sockets).toHaveLength(2);
  });

  it('counts a reopened socket as back once it brings a message', () => {
    const listener = vi.fn();
    ws.onWsReconnect(listener);
    ws.connectWebsocket();
    sockets[0].settle();

    sockets[0].drop();
    vi.runAllTimers();
    sockets[1].open();
    expect(listener).not.toHaveBeenCalled();
    sockets[1].receive({ n: 1 });
    vi.advanceTimersByTime(BACK_SPREAD_MS);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it('stops telling a listener once it unsubscribes', () => {
    const listener = vi.fn();
    const off = ws.onWsReconnect(listener);
    ws.connectWebsocket();
    sockets[0].settle();

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
    sockets[0].settle();

    reconnect();
    expect(listener).toHaveBeenCalledTimes(1);
    expect(console.error).toHaveBeenCalled();
  });

  it('tells at once a listener that only resets, before the new socket brings anything', () => {
    const order = [];
    ws.onWsReconnect(() => order.push('reset'));
    ws.onWsReconnect(() => order.push('ask'), { spread: true });
    ws.addWsHandler((msg) => order.push(`message ${msg.n}`));
    ws.connectWebsocket();
    sockets[0].settle();

    sockets[0].drop();
    vi.runAllTimers();
    sockets[1].open();
    sockets[1].receive({ n: 1 });
    expect(order).toEqual(['reset', 'message 1']);
    vi.advanceTimersByTime(BACK_SPREAD_MS);
    expect(order).toEqual(['reset', 'message 1', 'ask']);
  });

  it('tells listeners that ask the server spread out, each in its own time, so it is not asked all at once', () => {
    const calls = [];
    const random = vi.spyOn(Math, 'random');
    const spread = { spread: true };
    ws.onWsReconnect(() => calls.push(['a', Date.now()]), spread);
    ws.onWsReconnect(() => calls.push(['b', Date.now()]), spread);
    ws.connectWebsocket();
    sockets[0].settle();

    sockets[0].drop();
    vi.runAllTimers();
    random.mockReturnValueOnce(0.75).mockReturnValueOnce(0.25);
    sockets[1].settle();
    const back = Date.now();
    expect(calls).toEqual([]);

    vi.advanceTimersByTime(BACK_SPREAD_MS);
    expect(calls).toEqual([
      ['b', back + 0.25 * BACK_SPREAD_MS],
      ['a', back + 0.75 * BACK_SPREAD_MS],
    ]);
  });

  it('tells no listener when the socket drops again, or the user signs out, before its turn', () => {
    const listener = vi.fn();
    ws.onWsReconnect(listener, { spread: true });
    ws.connectWebsocket();
    sockets[0].settle();

    sockets[0].drop();
    vi.runAllTimers();
    sockets[1].settle();
    sockets[1].drop();
    vi.advanceTimersByTime(BACK_SPREAD_MS);
    expect(listener).not.toHaveBeenCalled();

    vi.runAllTimers();
    sockets[2].settle();
    ws.disconnectWebsocket();
    vi.advanceTimersByTime(BACK_SPREAD_MS);
    expect(listener).not.toHaveBeenCalled();
  });

  it('waits out the Retry-After of a refused request', async () => {
    const { serverUnavailable } = await import('@/utils/freshness.js');
    ws.connectWebsocket();
    sockets[0].settle();

    serverUnavailable('20');
    sockets[0].drop();
    vi.advanceTimersByTime(20_000 - 1);
    expect(sockets).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(2);
  });

  it('is called off by disconnecting, as signing out does', () => {
    ws.connectWebsocket();
    sockets[0].settle();

    sockets[0].drop();
    ws.disconnectWebsocket();
    vi.runAllTimers();
    expect(sockets).toHaveLength(1);
  });

  it('ignores a socket it has replaced', () => {
    const listener = vi.fn();
    ws.onWsReconnect(listener);
    ws.connectWebsocket();
    sockets[0].settle();
    reconnect();

    // the first socket's late events change nothing
    sockets[0].drop();
    sockets[0].receive({ n: 1 });
    vi.runAllTimers();
    expect(sockets).toHaveLength(2);
    expect(toasts.filter((t) => t.type === 'is-warning' && !t.closed)).toEqual(
      [],
    );
    expect(listener).toHaveBeenCalledTimes(1);
  });
});

describe('a dropped connection', () => {
  const SHUTDOWN =
    'The phēnix server is shutting down or restarting. The UI will reconnect when it is back.';
  const BEHIND =
    'Live updates from the phēnix server fell behind. Reconnecting…';
  const LOST =
    'Lost the connection to the phēnix server. It may be down, or your network may have dropped. Reconnecting…';
  const RECONNECTED = 'Reconnected to the phēnix server.';

  // the warnings still showing, and the reconnected notices shown
  const warnings = () =>
    toasts
      .filter((t) => t.type === 'is-warning' && !t.closed)
      .map((t) => t.message);
  const reconnectedNotices = () =>
    toasts.filter((t) => t.message === RECONNECTED);

  let freshness;
  beforeEach(async () => {
    freshness = await import('@/utils/freshness.js');
    ws.connectWebsocket();
    sockets[0].settle();
  });

  it.each([
    ['the server shutting down', 1001, 'server shutting down', SHUTDOWN],
    ['the server shutting down a socket', 1001, '', SHUTDOWN],
    ['dropping a client too slow', 1013, 'client too slow', BEHIND],
    ['no close frame', 1006, '', LOST],
    ['a normal close', 1000, '', LOST],
  ])('warns of %s until the socket is back', (_, code, reason, text) => {
    sockets[0].closeWith(code, reason);
    expect(warnings()).toEqual([text]);
    expect(toasts[0].indefinite).toBe(true);

    vi.runAllTimers();
    sockets.at(-1).open();
    // not yet: it may close again at once
    expect(warnings()).toEqual([text]);
    vi.advanceTimersByTime(1500);
    expect(warnings()).toEqual([]);
    expect(reconnectedNotices()).toEqual([
      expect.objectContaining({ type: 'is-success' }),
    ]);
  });

  it('keeps saying the server is shutting down through failed reconnects', () => {
    sockets[0].closeWith(1001, 'server shutting down');
    for (let i = 0; i < 3; i++) {
      vi.runAllTimers();
      sockets.at(-1).drop();
      expect(warnings()).toEqual([SHUTDOWN]);
      expect(freshness.serverShuttingDown()).toBe(true);
    }
    // one warning, not one for each attempt
    expect(toasts).toHaveLength(1);

    vi.runAllTimers();
    sockets.at(-1).settle();
    expect(freshness.serverShuttingDown()).toBe(false);

    // a later drop is a lost connection again
    sockets.at(-1).drop();
    expect(warnings()).toEqual([LOST]);
  });

  it('waits at least 5 s to reconnect to a server shutting down', () => {
    sockets[0].closeWith(1001, '');
    vi.advanceTimersByTime(4999);
    expect(sockets).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(2);
  });

  it('does not count a socket the server closes again at once as back', () => {
    const listener = vi.fn();
    ws.onWsReconnect(listener);

    sockets[0].closeWith(1001, 'server shutting down');
    vi.runAllTimers();
    sockets[1].open();
    sockets[1].closeWith(1001, 'server shutting down');

    expect(warnings()).toEqual([SHUTDOWN]);
    expect(toasts).toHaveLength(1);
    expect(reconnectedNotices()).toEqual([]);
    expect(listener).not.toHaveBeenCalled();
    expect(freshness.serverShuttingDown()).toBe(true);
  });

  it('says the connection was lost when a reconnect after falling behind fails', () => {
    sockets[0].closeWith(1013, 'client too slow');
    vi.runAllTimers();
    sockets.at(-1).drop();
    expect(warnings()).toEqual([LOST]);
  });

  it('marks live data out of date for the reason it closed', () => {
    const { staleReason, RESOURCES } = freshness;
    sockets[0].closeWith(1001, 'server shutting down');
    expect(staleReason({}, RESOURCES.experiments)).toBe('shutdown');

    vi.runAllTimers();
    sockets.at(-1).drop();
    expect(staleReason({}, RESOURCES.experiments)).toBe('shutdown');

    vi.runAllTimers();
    sockets.at(-1).settle();
    expect(staleReason({}, RESOURCES.experiments)).toBe(null);
  });

  it('leaves no warning after signing out, and says nothing of the next sign in', () => {
    sockets[0].drop();
    ws.disconnectWebsocket();
    expect(warnings()).toEqual([]);
    expect(freshness.wsDownReason()).toBe(null);

    ws.connectWebsocket();
    sockets.at(-1).settle();
    expect(reconnectedNotices()).toEqual([]);
  });

  it('says nothing of reconnecting when the first connect only failed', () => {
    ws.disconnectWebsocket();
    ws.connectWebsocket();
    sockets.at(-1).drop();
    expect(warnings()).toEqual([LOST]);

    vi.runAllTimers();
    sockets.at(-1).settle();
    expect(warnings()).toEqual([]);
    expect(reconnectedNotices()).toEqual([]);
  });

  it('shows no toast on the first connect', () => {
    expect(toasts).toEqual([]);
  });
});
