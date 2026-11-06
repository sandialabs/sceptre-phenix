// The minimega console session: kept between visits to the Console page,
// remembered for a reload or another tab, and ended on logout.
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const axios = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}));
vi.mock('@/utils/axios.js', () => ({ default: axios }));

const store = vi.hoisted(() => ({
  username: 'alice',
  token: 'jwt.token',
  actionListeners: [],
  $onAction(listener) {
    this.actionListeners.push(listener);
    return () => {};
  },
}));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));

const xterm = vi.hoisted(() => ({
  terminals: [],
  attached: [],
  searches: [],
  links: [],
}));

// a terminal buffer of the given rows; a row that starts with '\t' wraps
// from the one before it
const fakeBuffer = (rows = [], cursorY = rows.length - 1) => ({
  baseY: 0,
  cursorY,
  length: rows.length,
  getLine: (y) =>
    y < 0 || y >= rows.length
      ? undefined
      : {
          isWrapped: rows[y].startsWith('\t'),
          translateToString: () => rows[y].replace(/^\t/, ''),
        },
});

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    constructor(options = {}) {
      this.options = { ...options };
      this.cols = 80;
      this.rows = 24;
      this.scrolled = [];
      this.written = [];
      this.typed = [];
      this.addons = [];
      this.opened = 0;
      this.disposed = false;
      this.cleared = 0;
      this.focused = 0;
      this.selected = '';
      this.buffer = { active: fakeBuffer() };
      xterm.terminals.push(this);
    }
    loadAddon(addon) {
      this.addons.push(addon);
    }
    open(element) {
      this.opened++;
      this.element = element;
    }
    onResize(listener) {
      this.resizeListener = listener;
    }
    onData(listener) {
      this.dataListener = listener;
    }
    attachCustomKeyEventHandler(handler) {
      this.keyHandler = handler;
    }
    // what the user types, as the terminal hands it on
    input(data) {
      this.typed.push(data);
      this.dataListener(data);
    }
    write(data) {
      this.written.push(data);
    }
    clear() {
      this.cleared++;
    }
    scrollPages(n) {
      this.scrolled.push(n);
    }
    scrollToTop() {
      this.scrolled.push('top');
    }
    scrollToBottom() {
      this.scrolled.push('bottom');
    }
    focus() {
      this.focused++;
    }
    hasSelection() {
      return this.selected !== '';
    }
    getSelection() {
      return this.selected;
    }
    clearSelection() {
      this.selected = '';
    }
    dispose() {
      this.disposed = true;
    }
  },
}));
vi.mock('@xterm/addon-search', () => ({
  SearchAddon: class {
    constructor() {
      this.calls = [];
      this.found = true;
      xterm.searches.push(this);
    }
    findNext(text, options) {
      this.calls.push(['next', text, options]);
      return this.found;
    }
    findPrevious(text, options) {
      this.calls.push(['previous', text, options]);
      return this.found;
    }
    clearDecorations() {
      this.calls.push(['clear']);
    }
  },
}));
vi.mock('@xterm/addon-web-links', () => ({
  WebLinksAddon: class {
    constructor(handler) {
      this.handler = handler;
      xterm.links.push(this);
    }
  },
}));

const viewport = vi.hoisted(() => ({ room: null, measured: [] }));
vi.mock('@/utils/viewportFit.js', () => ({
  roomInWindow: (el) => {
    viewport.measured.push(el);
    return viewport.room;
  },
}));

const rbac = vi.hoisted(() => ({ allowed: true }));
vi.mock('@/utils/rbac.js', () => ({ roleAllowed: () => rbac.allowed }));
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fits = 0;
    fit() {
      this.fits++;
    }
  },
}));
vi.mock('@xterm/addon-attach', () => ({
  AttachAddon: class {
    constructor(socket) {
      this.socket = socket;
      this.disposed = false;
      xterm.attached.push(this);
    }
    dispose() {
      this.disposed = true;
    }
  },
}));

import { AxiosError } from 'axios';
import { httpError } from './helpers/fixtures.js';
import { byLabel, mountComponent, textOf } from './helpers/mount.js';
import { blockedStorage } from './helpers/storage.js';

class FakeWebSocket {
  static instances = [];

  constructor(url) {
    this.url = url;
    this.closed = false;
    FakeWebSocket.instances.push(this);
  }

  close() {
    this.closed = true;
  }
}

const notFound = () => httpError(404);

// the page's button with this text or label
const buttonNamed = (page, name) =>
  page
    .findAll('button')
    .find((b) => textOf(b) === name || b.attributes('aria-label') === name) ??
  page.find('#no-such-button');

let consoleSession;
let consoleTerminal;
let freshness;

beforeEach(async () => {
  vi.resetModules();
  FakeWebSocket.instances = [];
  xterm.terminals = [];
  xterm.attached = [];
  xterm.searches = [];
  xterm.links = [];
  rbac.allowed = true;
  viewport.room = null;
  viewport.measured = [];
  store.username = 'alice';
  store.actionListeners = [];
  axios.get.mockReset();
  axios.post.mockReset();
  axios.post.mockResolvedValue({ data: { pid: 42 } });
  axios.delete.mockReset();
  axios.delete.mockResolvedValue({ status: 204 });

  vi.stubGlobal('WebSocket', FakeWebSocket);
  vi.stubGlobal('location', { protocol: 'https:', host: 'phenix.test' });

  consoleSession = await import('@/utils/consoleSession.js');
  consoleTerminal = await import('@/utils/consoleTerminal.js');
  freshness = await import('@/utils/freshness.js');
});

afterEach(() => {
  consoleSession.closeConsole();
  freshness.setWsDown(false);
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// a request the server did not answer, as axios rejects it
const networkError = () =>
  new AxiosError('Network Error', 'ERR_NETWORK', {}, {});

// what the live websocket hears as the server shuts down
const brokerShutdown = () => freshness.setWsDown(true, 'shutdown');

describe('opening the console', () => {
  it('starts a console and remembers it for the user', async () => {
    const opened = consoleTerminal.openConsole();
    expect(consoleTerminal.consolePhase.value).toBe('starting');

    const session = await opened;
    expect(axios.get).not.toHaveBeenCalled();
    expect(axios.post).toHaveBeenCalledWith('console');
    expect(session.pid).toBe(42);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(42);
    expect(consoleSession.rememberedConsolePid('bob')).toBe(null);
    expect(consoleTerminal.consolePhase.value).toBe(null);
    expect(FakeWebSocket.instances.map((ws) => ws.url)).toEqual([
      'wss://phenix.test/api/v1/console/42/ws?token=jwt.token',
    ]);
  });

  it('keeps the console open when the page is left, and shows it again', async () => {
    const session = await consoleTerminal.openConsole();
    const page = document.createElement('div');
    session.mount(page);
    expect(xterm.terminals[0].opened).toBe(1);

    session.unmount();
    expect(page.childElementCount).toBe(0);
    expect(FakeWebSocket.instances[0].closed).toBe(false);
    expect(xterm.terminals[0].disposed).toBe(false);

    // the next visit gets the same terminal, and nothing is asked of the server
    expect(await consoleTerminal.openConsole()).toBe(session);
    const again = document.createElement('div');
    session.mount(again);
    expect(session.element.parentElement).toBe(again);
    expect(xterm.terminals).toHaveLength(1);
    expect(xterm.terminals[0].opened).toBe(1);
    expect(axios.post).toHaveBeenCalledTimes(1);
  });

  it('starts one console for visits made while it is starting', async () => {
    const [a, b] = await Promise.all([
      consoleTerminal.openConsole(),
      consoleTerminal.openConsole(),
    ]);
    expect(a).toBe(b);
    expect(axios.post).toHaveBeenCalledTimes(1);
  });

  it('attaches to the console this browser had open while it runs', async () => {
    consoleSession.rememberConsolePid('alice', 7);
    axios.get.mockResolvedValue({ data: { pid: 7 } });

    const opened = consoleTerminal.openConsole();
    expect(consoleTerminal.consolePhase.value).toBe('reconnecting');

    const session = await opened;
    expect(axios.get).toHaveBeenCalledWith('console/7');
    expect(axios.post).not.toHaveBeenCalled();
    expect(session.pid).toBe(7);
    expect(FakeWebSocket.instances[0].url).toContain('/console/7/ws');
  });

  it('starts a new console when the remembered one has ended', async () => {
    consoleSession.rememberConsolePid('alice', 7);
    axios.get.mockRejectedValue(notFound());

    const session = await consoleTerminal.openConsole();
    expect(axios.post).toHaveBeenCalledWith('console');
    expect(session.pid).toBe(42);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(42);
  });

  it('reports a check on the remembered console that fails', async () => {
    consoleSession.rememberConsolePid('alice', 7);
    const err = httpError(403);
    axios.get.mockRejectedValue(err);

    await expect(consoleTerminal.openConsole()).rejects.toBe(err);
    expect(axios.post).not.toHaveBeenCalled();
    expect(consoleTerminal.consolePhase.value).toBe(null);
  });
});

describe('a console session', () => {
  it('attaches the terminal once the socket opens and sends its size', async () => {
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    const states = [];
    session.onChange((state) => states.push(state));

    FakeWebSocket.instances[0].onopen();
    expect(xterm.attached[0].socket).toBe(FakeWebSocket.instances[0]);
    expect(states).toEqual(['open']);

    // the size it has, which a fit that changes nothing does not send
    expect(axios.post).toHaveBeenLastCalledWith(
      'console/42/size?cols=80&rows=24',
    );

    xterm.terminals[0].resizeListener({ cols: 120, rows: 40 });
    expect(axios.post).toHaveBeenLastCalledWith(
      'console/42/size?cols=120&rows=40',
    );
  });

  it('sends no size when its socket opens while the page does not show it', async () => {
    await consoleTerminal.openConsole();
    axios.post.mockClear();
    FakeWebSocket.instances[0].onopen();
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('ends when the server closes it, so the next visit starts afresh', async () => {
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onopen();

    axios.get.mockRejectedValue(notFound());
    FakeWebSocket.instances[0].onclose({ code: 1000 });
    await flushPromises();
    expect(axios.get).toHaveBeenCalledWith('console/42');
    expect(session.state).toBe('ended');
    expect(xterm.terminals[0].written.join('')).toContain('[console ended]');
    expect(consoleSession.currentConsole()).toBe(null);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(null);
    // still shown until the page is left
    expect(xterm.terminals[0].disposed).toBe(false);

    session.unmount();
    expect(xterm.terminals[0].disposed).toBe(true);

    axios.post.mockResolvedValue({ data: { pid: 43 } });
    expect((await consoleTerminal.openConsole()).pid).toBe(43);
  });

  it('is let go when it ends while the page is not showing it', async () => {
    const session = await consoleTerminal.openConsole();
    FakeWebSocket.instances[0].onopen();
    axios.get.mockRejectedValue(notFound());
    FakeWebSocket.instances[0].onclose({ code: 1000 });
    await flushPromises();

    expect(session.disposed).toBe(true);
    expect(xterm.terminals[0].disposed).toBe(true);
  });

  it('keeps a console whose connection was lost for the next visit to check', async () => {
    vi.useFakeTimers();
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onclose({ code: 1006 });
    // it waits a moment to hear whether the server is shutting down
    await vi.advanceTimersByTimeAsync(1000);
    expect(session.state).toBe('connecting');
    await vi.advanceTimersByTimeAsync(500);

    expect(axios.get).not.toHaveBeenCalled();
    expect(session.state).toBe('lost');
    expect(xterm.terminals[0].written.join('')).toContain(
      '[console connection lost]',
    );
    expect(consoleSession.currentConsole()).toBe(null);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(42);
  });

  it('ends when the server shuts down, which ends its consoles', async () => {
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onopen();

    FakeWebSocket.instances[0].onclose({
      code: 1001,
      reason: 'server shutting down',
    });
    await flushPromises();

    // the server is going away, so it is not asked about the console
    expect(axios.get).not.toHaveBeenCalled();
    expect(session.state).toBe('ended');
    const written = xterm.terminals[0].written.join('');
    expect(written).toContain('[console ended: phēnix server shutting down]');
    expect(written).not.toContain('[console connection lost]');
    expect(consoleSession.currentConsole()).toBe(null);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(null);
  });

  it('ends when its socket closes going away with no reason, as the server shuts down', async () => {
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onopen();

    FakeWebSocket.instances[0].onclose({ code: 1001, reason: '' });
    await flushPromises();

    expect(session.state).toBe('ended');
    expect(xterm.terminals[0].written.join('')).toContain(
      '[console ended: phēnix server shutting down]',
    );
  });

  // the live websocket's close from the server may come before, during the
  // wait after, or never after the console's socket drops or its probe fails
  describe.each([
    ['drops', { code: 1006 }, () => {}],
    [
      'closes and the server cannot be asked about it',
      { code: 1000 },
      () => axios.get.mockRejectedValue(networkError()),
    ],
  ])('when its socket %s', (_, event, arrange) => {
    const closeWith = async (brokerAt) => {
      vi.useFakeTimers();
      vi.spyOn(console, 'warn').mockImplementation(() => {});
      arrange();
      const session = await consoleTerminal.openConsole();
      session.mount(document.createElement('div'));
      FakeWebSocket.instances[0].onopen();

      if (brokerAt === 'before') brokerShutdown();
      FakeWebSocket.instances[0].onclose(event);
      await vi.advanceTimersByTimeAsync(700);
      if (brokerAt === 'after') brokerShutdown();
      await vi.advanceTimersByTimeAsync(800);
      return session;
    };

    it.each([
      ['before', 'ended', '[console ended: phēnix server shutting down]', null],
      ['after', 'ended', '[console ended: phēnix server shutting down]', null],
      ['never', 'lost', '[console connection lost]', 42],
    ])(
      'with the server shutdown heard %s, it is %s',
      async (brokerAt, state, text, remembered) => {
        const session = await closeWith(brokerAt);
        expect(session.state).toBe(state);
        const written = xterm.terminals[0].written.join('');
        expect(written).toContain(text);
        expect(written.match(/\[/g)).toHaveLength(1);
        expect(consoleSession.rememberedConsolePid('alice')).toBe(remembered);
      },
    );
  });

  it('says the server shut down, not that the console ended, when the console is gone as it shuts down', async () => {
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onopen();

    let answer;
    axios.get.mockReturnValue(
      new Promise((_, reject) => {
        answer = reject;
      }),
    );
    FakeWebSocket.instances[0].onclose({ code: 1000 });
    brokerShutdown();
    answer(notFound());
    await flushPromises();

    expect(session.state).toBe('ended');
    const written = xterm.terminals[0].written.join('');
    expect(written).toContain('[console ended: phēnix server shutting down]');
    expect(written).not.toContain('[console ended]');
  });

  it('writes nothing to a session let go while it waited to hear of a shutdown', async () => {
    vi.useFakeTimers();
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onopen();

    FakeWebSocket.instances[0].onclose({ code: 1006 });
    session.unmount();
    session.dispose();
    await vi.advanceTimersByTimeAsync(1500);

    expect(xterm.terminals[0].written.join('')).not.toContain('[');
  });

  it('keeps a console that dropped a websocket too slow for its output', async () => {
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onopen();

    // the server closes a websocket it drops normally, as when a console ends
    axios.get.mockResolvedValue({ data: { pid: 42 } });
    FakeWebSocket.instances[0].onclose({ code: 1000 });
    await flushPromises();

    expect(axios.get).toHaveBeenCalledWith('console/42');
    expect(session.state).toBe('lost');
    const written = xterm.terminals[0].written.join('');
    expect(written).toContain('[console connection lost]');
    expect(written).not.toContain('[console ended]');
    expect(consoleSession.rememberedConsolePid('alice')).toBe(42);
  });

  it('keeps a console the server could not be asked about', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onopen();

    axios.get.mockRejectedValue(httpError(502));
    FakeWebSocket.instances[0].onclose({ code: 1000 });
    await flushPromises();

    expect(session.state).toBe('lost');
    expect(consoleSession.rememberedConsolePid('alice')).toBe(42);
    expect(warn).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });

  it('writes nothing to a session let go while the server was asked', async () => {
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onopen();

    let answer;
    axios.get.mockReturnValue(
      new Promise((_, reject) => {
        answer = reject;
      }),
    );
    FakeWebSocket.instances[0].onclose({ code: 1000 });
    session.unmount();
    session.dispose();
    answer(notFound());
    await flushPromises();

    expect(xterm.terminals[0].written.join('')).not.toContain('[console');
    // an ended console is still forgotten, so the next visit starts afresh
    expect(consoleSession.rememberedConsolePid('alice')).toBe(null);
  });
});

describe('logging out', () => {
  it('ends the console this tab has open', async () => {
    const session = await consoleTerminal.openConsole();

    await consoleSession.endConsole();
    expect(axios.delete).toHaveBeenCalledWith('console/42');
    expect(session.disposed).toBe(true);
    expect(FakeWebSocket.instances[0].closed).toBe(true);
    expect(consoleSession.currentConsole()).toBe(null);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(null);
  });

  it('ends the console another tab has open', async () => {
    consoleSession.rememberConsolePid('alice', 7);
    axios.delete.mockRejectedValue(notFound());

    await consoleSession.endConsole();
    expect(axios.delete).toHaveBeenCalledWith('console/7');
    expect(consoleSession.rememberedConsolePid('alice')).toBe(null);
  });

  it('asks nothing of the server without a console', async () => {
    await consoleSession.endConsole();
    expect(axios.delete).not.toHaveBeenCalled();
  });

  it('lets go of the console when the store logs out by itself', async () => {
    const session = await consoleTerminal.openConsole();
    expect(store.actionListeners).toHaveLength(1);

    store.actionListeners[0]({ name: 'logout', store });
    expect(session.disposed).toBe(true);
    expect(consoleSession.currentConsole()).toBe(null);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(null);
  });
});

describe('the remembered console', () => {
  it('is not forgotten for an older one another tab replaced', () => {
    consoleSession.rememberConsolePid('alice', 8);
    consoleSession.forgetConsolePid('alice', 7);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(8);

    consoleSession.forgetConsolePid('alice', 8);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(null);
  });

  it("is each user's own, and forgotten only while a forgotten pid is it", () => {
    const users = ['alice', 'bob', 'alice.bob'];
    const step = fc.record({
      user: fc.constantFrom(...users),
      pid: fc.option(fc.integer({ min: 1, max: 4 }), { nil: undefined }),
      forget: fc.boolean(),
    });
    fc.assert(
      fc.property(fc.array(step), (steps) => {
        users.forEach((user) => consoleSession.forgetConsolePid(user));
        const remembered = new Map();
        for (const { user, pid, forget } of steps) {
          if (forget) {
            consoleSession.forgetConsolePid(user, pid);
            if (pid === undefined || remembered.get(user) === pid) {
              remembered.delete(user);
            }
          } else if (pid) {
            consoleSession.rememberConsolePid(user, pid);
            remembered.set(user, pid);
          }
        }
        for (const user of users) {
          expect(consoleSession.rememberedConsolePid(user)).toBe(
            remembered.get(user) ?? null,
          );
        }
      }),
    );
  });

  it('is ignored when storage is unavailable', () => {
    vi.stubGlobal('localStorage', blockedStorage());

    consoleSession.rememberConsolePid('alice', 8);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(null);
    consoleSession.forgetConsolePid('alice');
  });
});

describe('the Console page', () => {
  let Console;

  beforeEach(async () => {
    Console = (await import('@/views/Console.vue')).default;
  });

  // the terminal the page shows, if any
  const terminal = (page) => page.find('.console-terminal');

  it('says it is starting the console only while it does', async () => {
    const started = Promise.withResolvers();
    axios.post.mockReturnValue(started.promise);
    const first = await mountComponent(Console);
    expect(textOf(first.find('h1'))).toBe('Starting console…');

    started.resolve({ data: { pid: 42 } });
    await flushPromises();
    expect(first.find('h1').exists()).toBe(false);
    expect(terminal(first).exists()).toBe(true);
    const shown = terminal(first).element;
    first.unmount();
    expect(shown.parentElement).toBe(null);

    // coming back shows the same console straight away
    const second = await mountComponent(Console);
    expect(terminal(second).element).toBe(shown);
    expect(second.find('h1').exists()).toBe(false);
    expect(axios.post).toHaveBeenCalledTimes(1);
  });

  it('offers a new console once this one has ended', async () => {
    const page = await mountComponent(Console);
    await flushPromises();
    const ended = consoleSession.currentConsole();

    axios.get.mockRejectedValue(notFound());
    FakeWebSocket.instances[0].onclose({ code: 1000 });
    await flushPromises();
    const restart = buttonNamed(page, 'Start a new console');
    expect(restart.exists()).toBe(true);

    axios.post.mockResolvedValue({ data: { pid: 43 } });
    await restart.trigger('click');
    expect(ended.disposed).toBe(true);
    await flushPromises();
    expect(consoleSession.currentConsole().pid).toBe(43);
    expect(terminal(page).exists()).toBe(true);
  });

  it.each([
    [
      'the server could not be reached',
      networkError,
      null,
      'Could not reach the phēnix server. It may not be running, or your network connection may have failed.',
    ],
    [
      'the server could not be reached as it shuts down',
      networkError,
      'shutdown',
      'The phēnix server is shutting down or restarting. Try again when it is available.',
    ],
    [
      'the server is shutting down',
      () => httpError(503, 'the phēnix server is shutting down'),
      null,
      'The phēnix server is shutting down or restarting. Try again when it is available.',
    ],
    [
      'the server said why',
      () => httpError(500, { message: 'minimega is not running' }),
      null,
      'minimega is not running',
    ],
    [
      'the server said why in text',
      () => httpError(500, 'too many consoles\n'),
      null,
      'too many consoles',
    ],
    [
      'the server gave no reason',
      () => httpError(500, ''),
      null,
      'Could not start the console.',
    ],
  ])(
    'explains a console it could not start because %s',
    async (_, error, cause, want) => {
      if (cause) freshness.setWsDown(true, cause);
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
      axios.post.mockRejectedValue(error());
      try {
        const page = await mountComponent(Console);
        await flushPromises();
        expect(textOf(page.find('h1'))).toBe(want);
      } finally {
        warn.mockRestore();
      }
    },
  );

  it('explains a console the server does not offer', async () => {
    axios.post.mockRejectedValue(httpError(405));
    const page = await mountComponent(Console);
    await flushPromises();
    expect(textOf(page.find('h1'))).toBe('Console access is not configured.');
    expect(buttonNamed(page, 'Try again').exists()).toBe(false);
  });

  it('offers to try again after a console it could not start', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    axios.post.mockRejectedValueOnce(httpError(400, { message: 'no such' }));
    const page = await mountComponent(Console);
    await flushPromises();
    expect(textOf(page.find('h1'))).toBe('no such');

    await buttonNamed(page, 'Try again').trigger('click');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledTimes(2);
    expect(terminal(page).exists()).toBe(true);
    expect(buttonNamed(page, 'Try again').exists()).toBe(false);
  });
});

describe('a console session in a namespace', () => {
  it('starts in the namespace asked for', async () => {
    axios.post.mockResolvedValue({ data: { pid: 42, namespace: 'demo' } });
    const session = await consoleTerminal.openConsole('demo');
    expect(axios.post).toHaveBeenCalledWith('console?namespace=demo');
    expect(session.namespace).toBe('demo');
  });

  it('encodes the namespace for the query', async () => {
    axios.post.mockResolvedValue({ data: { pid: 42 } });
    const session = await consoleTerminal.openConsole('a&b');
    expect(axios.post).toHaveBeenCalledWith('console?namespace=a%26b');
    expect(session.namespace).toBe('a&b');
  });

  it('keeps the namespace of the console it attaches to again', async () => {
    consoleSession.rememberConsolePid('alice', 7);
    axios.get.mockResolvedValue({ data: { pid: 7, namespace: 'demo' } });
    const session = await consoleTerminal.openConsole('other');
    expect(axios.post).not.toHaveBeenCalled();
    expect(session.namespace).toBe('demo');
  });

  it('has no namespace when the server names none', async () => {
    consoleSession.rememberConsolePid('alice', 7);
    axios.get.mockResolvedValue({});
    expect((await consoleTerminal.openConsole()).namespace).toBe('');
  });
});

describe('what a console session offers', () => {
  let session;
  let term;

  beforeEach(async () => {
    session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    term = xterm.terminals[0];
  });

  const open = () => FakeWebSocket.instances[0].onopen();

  it('keeps 10000 lines of scrollback, and says when it is full', () => {
    expect(term.options.scrollback).toBe(10000);
    term.buffer.active = fakeBuffer(Array(10023).fill('x'));
    expect(session.keptLines()).toBe(null);
    term.buffer.active = fakeBuffer(Array(10024).fill('x'));
    expect(session.keptLines()).toBe(10024);
  });

  it('opens links in a new tab that cannot reach the page', () => {
    const windowOpen = vi.fn();
    vi.stubGlobal('open', windowOpen);
    const event = { preventDefault: vi.fn() };
    xterm.links[0].handler(event, 'https://example.test/a');
    expect(event.preventDefault).toHaveBeenCalled();
    expect(windowOpen).toHaveBeenCalledWith(
      'https://example.test/a',
      '_blank',
      'noopener,noreferrer',
    );
    expect(term.addons).toContain(xterm.links[0]);
    expect(term.addons).toContain(xterm.searches[0]);
  });

  it('types into the console only while it is open', () => {
    session.type('vm info\r');
    expect(term.typed).toEqual([]);
    open();
    session.type('vm info\r');
    expect(term.typed).toEqual(['vm info\r']);
    expect(term.focused).toBe(2);
  });

  it('gives its whole output as text, and clears it', () => {
    term.buffer.active = fakeBuffer(['first', 'second', '\t wrapped', '']);
    expect(session.text()).toBe('first\nsecond wrapped');
    expect(session.keptLines()).toBe(null);
    session.clear();
    expect(term.cleared).toBe(1);
    expect(term.focused).toBe(1);
  });

  it('gives the selected text', () => {
    expect(session.selection()).toBe('');
    term.selected = 'vm info';
    expect(session.selection()).toBe('vm info');
  });

  it('finds text forward and backward, and clears what it found', () => {
    const search = xterm.searches[0];
    expect(session.find('vm')).toBe(true);
    search.found = false;
    expect(session.find('VM', { backwards: true, caseSensitive: true })).toBe(
      false,
    );
    session.find('v', { incremental: true });
    term.selected = 'vm';
    session.clearSearch();
    expect(search.calls).toEqual([
      ['next', 'vm', { caseSensitive: false, incremental: false }],
      ['previous', 'VM', { caseSensitive: true, incremental: false }],
      ['next', 'v', { caseSensitive: false, incremental: true }],
      ['clear'],
    ]);
    expect(term.selected).toBe('');
  });

  it('changes its colors and its text size, which it fits to', () => {
    session.setTheme(false);
    expect(term.options.theme.background).toBe('#fafafa');
    session.setTheme(true);
    expect(term.options.theme.background).toBe('#2b2b2b');

    const fits = session.fitAddon.fits;
    session.setFontSize(99);
    expect(term.options.fontSize).toBe(24);
    expect(session.fitAddon.fits).toBe(fits + 1);
  });

  it('starts with the text size this browser remembers', async () => {
    consoleSession.closeConsole();
    axios.get.mockResolvedValue({ data: { pid: 42 } });
    localStorage.setItem('phenix.console.fontSize', '18');
    await consoleTerminal.openConsole();
    expect(xterm.terminals[1].options.fontSize).toBe(18);
  });

  it('ends the console, also when it has already ended', async () => {
    await session.end();
    expect(axios.delete).toHaveBeenCalledWith('console/42');

    axios.delete.mockRejectedValue(notFound());
    await expect(session.end()).resolves.toBe(undefined);

    const err = httpError(500);
    axios.delete.mockRejectedValue(err);
    await expect(session.end()).rejects.toBe(err);
  });

  it('tells of each line entered, reading a recalled line from the screen', () => {
    const lines = [];
    const stop = session.onSubmit((line) => lines.push(line));
    term.dataListener('vm info\r');
    term.buffer.active = fakeBuffer([
      'minimega:/tmp/minimega/minimega[demo]$ qu',
      '\tit',
    ]);
    term.dataListener('\x1b[A\r');
    term.buffer.active = fakeBuffer(['some output']);
    term.dataListener('\t\r');
    stop();
    term.dataListener('help\r');
    expect(lines).toEqual(['vm info', 'quit', '']);
  });

  it('scrolls its output by pages and to either end', () => {
    session.scrollPages(-1);
    session.scrollPages(1);
    session.scrollToTop();
    session.scrollToBottom();
    expect(term.scrolled).toEqual([-1, 1, 'top', 'bottom']);
  });

  it('lets a key handler keep keys from the console', () => {
    const key = { type: 'keydown', key: 'f' };
    expect(term.keyHandler(key)).toBe(true);
    session.setKeyHandler(() => false);
    expect(term.keyHandler(key)).toBe(false);
    session.setKeyHandler(() => undefined);
    expect(term.keyHandler(key)).toBe(true);
    session.dispose();
    expect(term.keyHandler(key)).toBe(true);
  });
});

describe('the Console page toolbar', () => {
  let Console;
  let dialogs;
  let confirmDialog;

  const experiments = [
    { name: 'zeta', running: false },
    { name: 'demo', running: true },
    { name: 'alpha', running: true },
  ];

  beforeEach(async () => {
    Console = (await import('@/views/Console.vue')).default;
    ({ dialogs, confirmDialog } = await import('./helpers/mount.js'));
    axios.get.mockImplementation((url) =>
      url.startsWith('experiments')
        ? Promise.resolve({ data: { experiments } })
        : Promise.reject(notFound()),
    );
  });

  afterEach(() => {
    document.querySelectorAll('.dialog').forEach((d) => d.remove());
  });

  // the page with its console open
  const openPage = async (options) => {
    const page = await mountComponent(Console, options);
    await flushPromises();
    FakeWebSocket.instances.at(-1).onopen();
    await flushPromises();
    return page;
  };
  const term = () => xterm.terminals.at(-1);
  const state = (page) => textOf(page.find('.console-state'));
  // the console's socket closes; whether the console still runs decides
  // how the page takes it
  const closeSocket = async (running) => {
    axios.get.mockImplementation((url) =>
      url.startsWith('experiments')
        ? Promise.resolve({ data: { experiments } })
        : running
          ? Promise.resolve({ data: { pid: 42, namespace: '' } })
          : Promise.reject(notFound()),
    );
    FakeWebSocket.instances.at(-1).onclose({ code: 1000 });
    await flushPromises();
  };

  it('shows the state of the console', async () => {
    const page = await mountComponent(Console);
    await flushPromises();
    expect(state(page)).toBe('Connecting');
    expect(page.find('.console-state').attributes('role')).toBe('status');

    FakeWebSocket.instances[0].onopen();
    await flushPromises();
    expect(state(page)).toBe('Connected');

    await closeSocket(false);
    expect(state(page)).toBe('Ended');
  });

  it('clears the terminal and gives it the focus back', async () => {
    const page = await openPage();
    await buttonNamed(page, 'Clear').trigger('click');
    expect(term().cleared).toBe(1);
    expect(term().focused).toBeGreaterThan(0);
  });

  it('copies the whole output, and says so', async () => {
    const writeText = vi.fn().mockResolvedValue();
    vi.stubGlobal('navigator', { clipboard: { writeText }, platform: 'Linux' });
    const page = await openPage();
    term().buffer.active = fakeBuffer(['one', 'two']);

    await buttonNamed(page, 'Copy all').trigger('click');
    await flushPromises();
    expect(writeText).toHaveBeenCalledWith('one\ntwo');
    expect(textOf(page.find('.console-status'))).toBe(
      'Copied the console output.',
    );
  });

  it('says when the output it copies or downloads lost its oldest lines', async () => {
    const writeText = vi.fn().mockResolvedValue();
    vi.stubGlobal('navigator', { clipboard: { writeText }, platform: 'Linux' });
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:transcript');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    const page = await openPage();
    term().buffer.active = fakeBuffer(Array(10024).fill('x'));

    await buttonNamed(page, 'Copy all').trigger('click');
    await flushPromises();
    expect(textOf(page.find('.console-status'))).toBe(
      'Copied the console output. It has only the last 10,024 lines.',
    );
    await byLabel(page, 'Download the transcript').trigger('click');
    expect(textOf(page.find('.console-status'))).toBe(
      'The transcript has only the last 10,024 lines.',
    );
  });

  it('copies through a text area when the page has no clipboard', async () => {
    vi.stubGlobal('navigator', { platform: 'Linux' });
    document.execCommand = vi.fn(() => true);
    const page = await openPage();
    term().buffer.active = fakeBuffer(['one']);

    await buttonNamed(page, 'Copy all').trigger('click');
    await flushPromises();
    expect(document.execCommand).toHaveBeenCalledWith('copy');
    expect(document.querySelector('textarea[readonly]')).toBe(null);
    expect(textOf(page.find('.console-status'))).toBe(
      'Copied the console output.',
    );

    document.execCommand = vi.fn(() => {
      throw new Error('not allowed');
    });
    await buttonNamed(page, 'Copy all').trigger('click');
    await flushPromises();
    expect(textOf(page.find('.console-status'))).toBe(
      'The browser did not copy the console output.',
    );
    delete document.execCommand;
  });

  it('downloads the output as a dated text file', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] });
    vi.setSystemTime(new Date(2026, 10, 4, 15, 30, 12));
    const blobs = [];
    vi.stubGlobal('URL', {
      createObjectURL: (blob) => {
        blobs.push(blob);
        return 'blob:transcript';
      },
      revokeObjectURL: vi.fn(),
    });
    const clicks = [];
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
      function () {
        clicks.push([this.href, this.download]);
      },
    );
    const page = await openPage();
    term().buffer.active = fakeBuffer(['vm info', 'none']);

    await byLabel(page, 'Download the transcript').trigger('click');
    expect(clicks).toEqual([
      ['blob:transcript', 'minimega-console-2026-11-04-153012.txt'],
    ]);
    // the download says nothing of itself
    expect(textOf(page.find('.console-status'))).toBe('');
    expect(await blobs[0].text()).toBe('vm info\nnone\n');
    expect(blobs[0].type).toContain('text/plain');
    vi.advanceTimersByTime(1000);
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:transcript');
  });

  it('ends the console only once the user confirms', async () => {
    const page = await openPage();

    await buttonNamed(page, 'End console').trigger('click');
    await flushPromises();
    expect(textOf(dialogs().at(-1))).toContain(
      'minimega and its experiments keep running',
    );
    dialogs().at(-1).querySelector('footer .button').click();
    await flushPromises();
    expect(axios.delete).not.toHaveBeenCalled();

    await buttonNamed(page, 'End console').trigger('click');
    await flushPromises();
    await confirmDialog();
    expect(axios.delete).toHaveBeenCalledWith('console/42');

    await closeSocket(false);
    expect(buttonNamed(page, 'End console').attributes('disabled')).toBe('');
    expect(buttonNamed(page, 'Start a new console').exists()).toBe(true);
  });

  it('reports a console it could not end', async () => {
    const page = await openPage();
    axios.delete.mockRejectedValue(httpError(500, { message: 'stuck' }));
    vi.spyOn(console, 'error').mockImplementation(() => {});

    await buttonNamed(page, 'End console').trigger('click');
    await flushPromises();
    await confirmDialog();
    expect(state(page)).toBe('Connected');
  });

  it('opens a search from its button or Ctrl+F, and closes it', async () => {
    const page = await openPage();
    const search = xterm.searches.at(-1);

    await buttonNamed(page, 'Search').trigger('click');
    const input = byLabel(page, 'Text to find in the console output');
    expect(input.exists()).toBe(true);
    await input.setValue('vm');
    await input.trigger('keydown', { key: 'Enter' });
    await input.trigger('keydown', { key: 'Enter', shiftKey: true });
    await page.find('.console-search input[type="checkbox"]').setValue(true);
    expect(search.calls).toEqual([
      ['next', 'vm', { caseSensitive: false, incremental: true }],
      ['next', 'vm', { caseSensitive: false, incremental: false }],
      ['previous', 'vm', { caseSensitive: false, incremental: false }],
      ['next', 'vm', { caseSensitive: true, incremental: true }],
    ]);

    search.found = false;
    await byLabel(page, 'Find the next match').trigger('click');
    expect(textOf(page.find('.console-search-result'))).toBe('No match');
    await byLabel(page, 'Find the previous match').trigger('click');
    expect(search.calls.at(-1)[0]).toBe('previous');

    await input.trigger('keydown', { key: 'Escape' });
    expect(page.find('.console-search').exists()).toBe(false);
    expect(search.calls.at(-1)).toEqual(['clear']);

    const ctrlF = {
      type: 'keydown',
      key: 'f',
      ctrlKey: true,
      preventDefault: vi.fn(),
    };
    expect(term().keyHandler(ctrlF)).toBe(false);
    expect(ctrlF.preventDefault).toHaveBeenCalled();
    await flushPromises();
    expect(page.find('.console-search').exists()).toBe(true);
    // again while it is open, and from the toolbar to close it
    expect(term().keyHandler(ctrlF)).toBe(false);
    await buttonNamed(page, 'Search').trigger('click');
    expect(page.find('.console-search').exists()).toBe(false);
  });

  it('takes the highlight away when the search text is erased', async () => {
    const page = await openPage();
    const search = xterm.searches.at(-1);
    await buttonNamed(page, 'Search').trigger('click');
    const input = byLabel(page, 'Text to find in the console output');
    await input.setValue('vm');
    term().selected = 'vm';
    await input.setValue('');
    expect(search.calls.at(-1)).toEqual(['clear']);
    expect(term().selected).toBe('');
    expect(page.find('.console-search').exists()).toBe(true);
  });

  it('finds nothing in an empty search', async () => {
    const page = await openPage();
    await buttonNamed(page, 'Search').trigger('click');
    const input = byLabel(page, 'Text to find in the console output');
    await input.trigger('keydown', { key: 'Enter' });
    expect(xterm.searches.at(-1).calls).toEqual([]);
    await byLabel(page, 'Close the search').trigger('click');
    expect(page.find('.console-search').exists()).toBe(false);
  });

  it.each([
    ['Linux', { ctrlKey: true }],
    ['MacIntel', { metaKey: true }],
  ])(
    'on %s, uses the copy, paste and search keys, with no Shortcuts list',
    async (platform, mod) => {
      const writeText = vi.fn().mockResolvedValue();
      vi.stubGlobal('navigator', { clipboard: { writeText }, platform });
      const page = await openPage();

      expect(
        page.findAll('button').some((b) => textOf(b) === 'Shortcuts'),
      ).toBe(false);
      expect(page.find('#console-shortcuts').exists()).toBe(false);

      const key = (k, extra) => ({
        type: 'keydown',
        key: k,
        preventDefault: vi.fn(),
        ...extra,
      });
      expect(term().keyHandler(key('f', mod))).toBe(false);
      expect(term().keyHandler({ ...key('c'), type: 'keyup' })).toBe(true);
      expect(term().keyHandler(key('x', mod))).toBe(true);

      term().selected = 'vm info';
      const copy = key('C', { ctrlKey: true, shiftKey: true });
      const paste = key('V', { ctrlKey: true, shiftKey: true });
      if (platform === 'Linux') {
        expect(term().keyHandler(copy)).toBe(false);
        await flushPromises();
        expect(writeText).toHaveBeenCalledWith('vm info');
        // the browser pastes, not the terminal
        expect(term().keyHandler(paste)).toBe(false);
        expect(paste.preventDefault).not.toHaveBeenCalled();
        term().selected = '';
        expect(term().keyHandler(copy)).toBe(false);
        expect(writeText).toHaveBeenCalledTimes(1);
      } else {
        expect(term().keyHandler(copy)).toBe(true);
        expect(term().keyHandler(paste)).toBe(true);
      }
    },
  );

  describe.each(['Linux', 'MacIntel'])(
    'on %s, the terminal keys',
    (platform) => {
      const key = (k, extra) => ({
        type: 'keydown',
        key: k,
        preventDefault: vi.fn(),
        ...extra,
      });

      beforeEach(() => {
        vi.stubGlobal('navigator', { platform });
      });

      it('clears the screen and the scrollback with Ctrl+L, not Cmd+L', async () => {
        await openPage();
        const ctrlL = key('l', { ctrlKey: true });
        expect(term().keyHandler(ctrlL)).toBe(false);
        expect(ctrlL.preventDefault).toHaveBeenCalled();
        expect(term().cleared).toBe(1);

        // the browser keeps Cmd+L, and the shifted and Alt forms
        for (const extra of [
          { metaKey: true },
          { ctrlKey: true, shiftKey: true },
          { ctrlKey: true, altKey: true },
        ]) {
          const other = key('l', extra);
          expect(term().keyHandler(other)).toBe(true);
          expect(other.preventDefault).not.toHaveBeenCalled();
        }
        expect(term().cleared).toBe(1);
      });

      it('scrolls a page with PageUp and PageDown, and to the ends with Shift+Home and Shift+End', async () => {
        await openPage();
        const keys = [
          key('PageUp'),
          key('PageDown'),
          key('Home', { shiftKey: true }),
          key('End', { shiftKey: true }),
        ];
        for (const k of keys) {
          expect(term().keyHandler(k)).toBe(false);
          expect(k.preventDefault).toHaveBeenCalled();
        }
        expect(term().scrolled).toEqual([-1, 1, 'top', 'bottom']);

        // the console gets the plain and modified forms
        for (const k of [
          key('Home'),
          key('End'),
          key('PageUp', { shiftKey: true }),
          key('PageDown', { ctrlKey: true }),
          key('Home', { shiftKey: true, ctrlKey: true }),
        ]) {
          expect(term().keyHandler(k)).toBe(true);
          expect(k.preventDefault).not.toHaveBeenCalled();
        }
        expect(term().scrolled).toHaveLength(4);
      });

      it('sends Ctrl+R to the console for its reverse search, not to the browser', async () => {
        await openPage();
        const ctrlR = key('r', { ctrlKey: true });
        expect(term().keyHandler(ctrlR)).toBe(true);
        expect(ctrlR.preventDefault).toHaveBeenCalled();

        // a reload with Ctrl+Shift+R or Cmd+R stays with the browser
        for (const extra of [
          { ctrlKey: true, shiftKey: true },
          { metaKey: true },
        ]) {
          const reload = key('r', extra);
          expect(term().keyHandler(reload)).toBe(true);
          expect(reload.preventDefault).not.toHaveBeenCalled();
        }
      });

      it('sends Ctrl+C, the arrows and Tab to the console', async () => {
        await openPage();
        for (const k of [
          key('c', { ctrlKey: true }),
          key('ArrowUp'),
          key('ArrowDown'),
          key('Tab'),
        ]) {
          expect(term().keyHandler(k)).toBe(true);
          expect(k.preventDefault).not.toHaveBeenCalled();
        }
      });
    },
  );

  it('types the commands of its menu, entering only those that read', async () => {
    const page = await openPage();
    const items = page.findAll('.console-command');
    expect(textOf(items[0])).toBe('vm info Each VM and its state');

    await items[0].trigger('click');
    const start = items.find((item) => textOf(item).startsWith('vm start'));
    expect(textOf(start)).toContain('(not entered)');
    await start.trigger('click');
    await items[1].trigger('keydown', { key: 'Enter' });
    // Ctrl+E and Ctrl+U first clear what is on the line
    expect(term().typed).toEqual([
      '\x05\x15vm info\r',
      '\x05\x15vm start ',
      '\x05\x15vm info summary\r',
    ]);

    // a quit left on the line is not entered with the command
    term().dataListener('\r');
    term().dataListener('quit ');
    await items[0].trigger('click');
    await flushPromises();
    expect(page.find('[role="alert"]').exists()).toBe(false);

    const reference = page.find('.console-commands a[target="_blank"]');
    expect(reference.attributes('href')).toBe(
      'https://sandia-minimega.github.io/minimega/reference/minimega/',
    );
    expect(reference.attributes('rel')).toBe('noopener noreferrer');
  });

  it('offers the commands only while the console is open', async () => {
    const page = await mountComponent(Console);
    await flushPromises();
    expect(buttonNamed(page, 'Commands').attributes('disabled')).toBe('');
  });

  it.each([
    ['quit\r', 'asks you to enter quit again'],
    ['  quit 5\r', 'does not ask again'],
  ])('warns, but lets through, %j', async (typed, warning) => {
    const page = await openPage();
    term().dataListener(typed);
    await flushPromises();
    const alert = page.find('[role="alert"]');
    expect(textOf(alert)).toContain('for every user and every experiment');
    expect(textOf(alert)).toContain(warning);

    // another quit keeps it, any other line takes it away
    term().dataListener('quit\r');
    term().dataListener('\r');
    await flushPromises();
    expect(page.find('[role="alert"]').exists()).toBe(true);
    term().dataListener('vm info\r');
    await flushPromises();
    expect(page.find('[role="alert"]').exists()).toBe(false);

    term().dataListener('quit\r');
    await flushPromises();
    await byLabel(page, 'Dismiss the warning').trigger('click');
    expect(page.find('[role="alert"]').exists()).toBe(false);
  });

  it('changes the text size, within bounds, and remembers it', async () => {
    const page = await openPage();
    expect(textOf(page.find('.console-font-size'))).toBe('14 px');
    expect(byLabel(page, 'Reset the text size').attributes('disabled')).toBe(
      '',
    );

    await byLabel(page, 'Larger text').trigger('click');
    expect(term().options.fontSize).toBe(15);
    expect(localStorage.getItem('phenix.console.fontSize')).toBe('15');
    await byLabel(page, 'Reset the text size').trigger('click');
    expect(term().options.fontSize).toBe(14);

    for (let i = 0; i < 6; i++) {
      await byLabel(page, 'Smaller text').trigger('click');
    }
    expect(term().options.fontSize).toBe(10);
    expect(byLabel(page, 'Smaller text').attributes('disabled')).toBe('');
    page.unmount();

    localStorage.setItem('phenix.console.fontSize', '24');
    const again = await mountComponent(Console);
    expect(byLabel(again, 'Larger text').attributes('disabled')).toBe('');
    expect(term().options.fontSize).toBe(24);
  });

  it('is dark, as the app is, unless the page sets data-theme="light"', async () => {
    vi.stubGlobal('matchMedia', (query) => ({
      matches: query.includes('light'),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    const page = await openPage();
    expect(term().options.theme.background).toBe('#2b2b2b');

    document.documentElement.dataset.theme = 'light';
    await flushPromises();
    expect(term().options.theme.background).toBe('#fafafa');
    document.documentElement.dataset.theme = 'dark';
    await flushPromises();
    expect(term().options.theme.background).toBe('#2b2b2b');
    delete document.documentElement.dataset.theme;
    page.unmount();

    // a page left watches no more
    document.documentElement.dataset.theme = 'light';
    await flushPromises();
    expect(term().options.theme.background).toBe('#2b2b2b');
    delete document.documentElement.dataset.theme;
  });

  it('keeps its colors in a browser without MutationObserver', async () => {
    vi.stubGlobal('MutationObserver', undefined);
    const page = await openPage();
    expect(term().options.theme.background).toBe('#2b2b2b');
    page.unmount();
  });

  it('fits the terminal again as the search or the quit warning opens and closes', async () => {
    viewport.room = 500;
    const page = await openPage();
    const host = page.find('.console-host').element;
    expect(host.style.height).toBe('500px');
    const fits = consoleSession.currentConsole().fitAddon;
    const before = fits.fits;

    const step = async (room, act) => {
      viewport.room = room;
      await act();
      await flushPromises();
      expect(host.style.height).toBe(`${room}px`);
    };
    await step(460, () => buttonNamed(page, 'Search').trigger('click'));
    await step(400, () => term().dataListener('quit\r'));
    await step(440, () =>
      byLabel(page, 'Dismiss the warning').trigger('click'),
    );
    await step(500, () => byLabel(page, 'Close the search').trigger('click'));
    expect(fits.fits).toBeGreaterThanOrEqual(before + 4);
  });

  it('fits the terminal when its space changes size', async () => {
    vi.useFakeTimers();
    const observers = [];
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(callback) {
          this.callback = callback;
          this.observed = [];
          this.disconnected = false;
          observers.push(this);
        }
        observe(el) {
          this.observed.push(el);
        }
        disconnect() {
          this.disconnected = true;
        }
      },
    );
    const page = await mountComponent(Console);
    await vi.runAllTimersAsync();
    FakeWebSocket.instances[0].onopen();
    const session = consoleSession.currentConsole();
    expect(observers[0].observed).toEqual([page.find('.console-host').element]);

    const fits = session.fitAddon.fits;
    observers[0].callback();
    observers[0].callback();
    await vi.advanceTimersByTimeAsync(100);
    expect(session.fitAddon.fits).toBe(fits + 1);

    page.unmount();
    expect(observers[0].disconnected).toBe(true);
  });

  it('gives the terminal the height left above the footer', async () => {
    viewport.room = 500.7;
    const page = await mountComponent(Console);
    await flushPromises();
    const host = page.find('.console-host').element;
    expect(viewport.measured).toContain(host);
    expect(host.style.height).toBe('500px');

    // never shorter than a few lines
    viewport.room = 100;
    window.dispatchEvent(new Event('resize'));
    await new Promise((resolve) => setTimeout(resolve, 150));
    expect(host.style.height).toBe('240px');
  });
});

describe('the Console page, as the connection is lost', () => {
  let Console;
  // whether the console the server is asked about is still running
  let running;

  beforeEach(async () => {
    Console = (await import('@/views/Console.vue')).default;
    running = true;
    axios.get.mockImplementation((url) => {
      if (url.startsWith('experiments')) {
        return Promise.resolve({ data: { experiments: [] } });
      }
      return running
        ? Promise.resolve({ data: { pid: 42, namespace: '' } })
        : Promise.reject(notFound());
    });
    vi.useFakeTimers();
  });

  const openPage = async () => {
    const page = await mountComponent(Console);
    await vi.advanceTimersByTimeAsync(0);
    FakeWebSocket.instances.at(-1).onopen();
    await vi.advanceTimersByTimeAsync(0);
    return page;
  };
  // the socket drops and the check for a shutdown finds none
  const lose = async () => {
    FakeWebSocket.instances.at(-1).onclose({ code: 1006 });
    await vi.advanceTimersByTimeAsync(1500);
  };
  const banner = (page) => page.find('.console-reconnect');

  it('reconnects to the same console after a countdown', async () => {
    const page = await openPage();
    await lose();
    expect(textOf(banner(page))).toContain('Reconnecting in 1 s (try 1 of 5)');
    expect(buttonNamed(page, 'Reconnect').exists()).toBe(false);

    await vi.advanceTimersByTimeAsync(1000);
    expect(banner(page).exists()).toBe(false);
    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(FakeWebSocket.instances[1].url).toContain('/console/42/ws');
    // no second console starts; the socket that opened sent its size
    expect(axios.post.mock.calls.map(([url]) => url)).toEqual([
      'console',
      'console/42/size?cols=80&rows=24',
    ]);

    // it waits longer each time, until the console opens again
    await lose();
    expect(textOf(banner(page))).toContain('Reconnecting in 2 s (try 2 of 5)');
    await vi.advanceTimersByTimeAsync(1000);
    expect(textOf(banner(page))).toContain('Reconnecting in 1 s');
    await vi.advanceTimersByTimeAsync(1000);
    FakeWebSocket.instances.at(-1).onopen();
    await vi.advanceTimersByTimeAsync(0);
    await lose();
    expect(textOf(banner(page))).toContain('try 1 of 5');
  });

  it('fits the terminal again as the reconnect banner opens and closes', async () => {
    viewport.room = 500;
    const page = await openPage();
    const host = page.find('.console-host').element;
    viewport.room = 450;
    await lose();
    expect(host.style.height).toBe('450px');
    viewport.room = 500;
    await buttonNamed(page, 'Cancel').trigger('click');
    await vi.advanceTimersByTimeAsync(0);
    expect(host.style.height).toBe('500px');
  });

  it('reconnects at once, or not at all, as the user says', async () => {
    const page = await openPage();
    await lose();
    await buttonNamed(page, 'Reconnect now').trigger('click');
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeWebSocket.instances).toHaveLength(2);

    await lose();
    await buttonNamed(page, 'Cancel').trigger('click');
    expect(banner(page).exists()).toBe(false);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(FakeWebSocket.instances).toHaveLength(2);

    // reconnecting by hand starts the count again
    await buttonNamed(page, 'Reconnect').trigger('click');
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeWebSocket.instances).toHaveLength(3);
    await lose();
    expect(textOf(banner(page))).toContain('try 1 of 5');
  });

  it('keeps trying while the server does not answer, then gives up', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const page = await openPage();
    axios.get.mockRejectedValue(
      new AxiosError('Network Error', 'ERR_NETWORK', {}, {}),
    );
    await lose();

    for (const [delay, attempt] of [
      [1, 1],
      [2, 2],
      [4, 3],
      [8, 4],
      [15, 5],
    ]) {
      expect(textOf(banner(page))).toContain(
        `Reconnecting in ${delay} s (try ${attempt} of 5)`,
      );
      await vi.advanceTimersByTimeAsync(delay * 1000);
    }
    expect(banner(page).exists()).toBe(false);
    expect(textOf(page.find('h1'))).toContain('Could not reach');
    expect(buttonNamed(page, 'Reconnect').exists()).toBe(true);
  });

  it('stops trying on an answer that another try would not change', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const page = await openPage();
    await lose();
    axios.get.mockRejectedValue(httpError(403));
    await vi.advanceTimersByTimeAsync(1000);
    expect(banner(page).exists()).toBe(false);
    expect(textOf(page.find('h1'))).toBe(
      'You do not have access to the console.',
    );
  });

  it('does not reconnect a console that ended', async () => {
    const page = await openPage();
    running = false;
    FakeWebSocket.instances.at(-1).onclose({ code: 1000 });
    await vi.advanceTimersByTimeAsync(0);
    expect(banner(page).exists()).toBe(false);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it('stops reconnecting on logout, and when the page is left', async () => {
    const page = await openPage();
    await lose();
    for (const listener of store.actionListeners) {
      listener({ name: 'login', store });
    }
    expect(banner(page).exists()).toBe(true);
    for (const listener of store.actionListeners) {
      listener({ name: 'logout', store });
    }
    await vi.advanceTimersByTimeAsync(0);
    expect(banner(page).exists()).toBe(false);

    await buttonNamed(page, 'Reconnect').trigger('click');
    await vi.advanceTimersByTimeAsync(0);
    await lose();
    page.unmount();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(FakeWebSocket.instances).toHaveLength(2);
  });
});

describe('the namespace of the Console page', () => {
  let Console;
  let confirmDialog;
  let dialogs;

  beforeEach(async () => {
    Console = (await import('@/views/Console.vue')).default;
    ({ confirmDialog, dialogs } = await import('./helpers/mount.js'));
    axios.get.mockImplementation((url) =>
      url.startsWith('experiments')
        ? Promise.resolve({
            data: {
              experiments: [
                { name: 'zeta', running: false },
                { name: 'demo', running: true },
                { name: 'alpha', running: true },
              ],
            },
          })
        : Promise.reject(notFound()),
    );
    axios.post.mockImplementation((url) =>
      Promise.resolve({
        data: {
          pid: url.includes('namespace') ? 43 : 42,
          namespace: url.split('namespace=')[1] ?? '',
        },
      }),
    );
  });

  afterEach(() => {
    document.querySelectorAll('.dialog').forEach((d) => d.remove());
  });

  const openPage = async () => {
    const page = await mountComponent(Console);
    await flushPromises();
    FakeWebSocket.instances.at(-1).onopen();
    await flushPromises();
    return page;
  };
  const picker = (page) => page.find('#console-namespace');
  // each option: its group and text, and whether it is chosen
  const options = (page) =>
    picker(page)
      .findAll('option')
      .map(
        (o) =>
          `${o.element.parentElement.label ?? ''}:${textOf(o)}${o.element.selected ? ' *' : ''}`,
      );

  it('lists the experiments, running ones first', async () => {
    const page = await openPage();
    expect(axios.get).toHaveBeenCalledWith('experiments?vms=false', undefined);
    expect(textOf(page.find('label[for="console-namespace"]'))).toBe(
      'Namespace',
    );
    expect(options(page)).toEqual([
      ':All (no namespace) *',
      'Running:alpha',
      'Running:demo',
      'Not running:zeta',
    ]);
  });

  it('lists none for a role that may not list experiments', async () => {
    rbac.allowed = false;
    const page = await openPage();
    expect(axios.get).not.toHaveBeenCalledWith(
      'experiments?vms=false',
      undefined,
    );
    expect(options(page)).toEqual([':All (no namespace) *']);
  });

  it('shows the namespace of the console it attaches to, listed or not', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    consoleSession.rememberConsolePid('alice', 7);
    axios.get.mockImplementation((url) =>
      url.startsWith('experiments')
        ? Promise.reject(httpError(500))
        : Promise.resolve({ data: { pid: 7, namespace: 'gone' } }),
    );
    const page = await openPage();
    expect(options(page)).toEqual([
      ':All (no namespace)',
      'Not running:gone *',
    ]);
  });

  it('ends the console and starts one in the namespace the user confirms', async () => {
    const page = await openPage();

    await picker(page).setValue('demo');
    await flushPromises();
    expect(textOf(dialogs().at(-1))).toContain(
      'ends the console and starts one in the demo namespace',
    );
    dialogs().at(-1).querySelector('footer .button').click();
    await flushPromises();
    expect(axios.delete).not.toHaveBeenCalled();
    // the picker shows the console's namespace again
    expect(options(page)).toContain(':All (no namespace) *');
    expect(options(page)).toContain('Running:demo');

    await picker(page).setValue('demo');
    await flushPromises();
    await confirmDialog();
    expect(axios.delete).toHaveBeenCalledWith('console/42');
    // the new console starts before the old one ends
    expect(axios.post.mock.invocationCallOrder.at(-1)).toBeLessThan(
      axios.delete.mock.invocationCallOrder.at(-1),
    );
    expect(axios.post).toHaveBeenLastCalledWith('console?namespace=demo');
    expect(consoleSession.currentConsole().namespace).toBe('demo');
    expect(options(page)).toContain('Running:demo *');

    // and back to no namespace
    FakeWebSocket.instances.at(-1).onopen();
    await flushPromises();
    await picker(page).setValue('');
    await flushPromises();
    expect(textOf(dialogs().at(-1))).toContain('starts one in no namespace');
    await confirmDialog();
    expect(axios.delete).toHaveBeenLastCalledWith('console/43');
    expect(axios.post).toHaveBeenLastCalledWith('console');
  });

  it('keeps the console when one in the new namespace does not start', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const page = await openPage();
    axios.post.mockRejectedValueOnce(
      httpError(400, { message: 'experiment demo not found' }),
    );

    await picker(page).setValue('demo');
    await flushPromises();
    await confirmDialog();
    expect(axios.delete).not.toHaveBeenCalled();
    expect(consoleSession.currentConsole().pid).toBe(42);
    expect(page.find('.console-terminal').exists()).toBe(true);
    expect(textOf(page.find('.console-state'))).toBe('Connected');
    expect(options(page)).toContain(':All (no namespace) *');
    expect(document.body.textContent).toContain('experiment demo not found');
    document.querySelectorAll('.notices').forEach((n) => n.remove());
  });

  it('shows the console started in the new namespace on the next visit when the page is left', async () => {
    const page = await openPage();
    const started = Promise.withResolvers();
    axios.post.mockReturnValueOnce(started.promise);
    await picker(page).setValue('demo');
    await flushPromises();
    await confirmDialog();
    page.unmount();

    started.resolve({ data: { pid: 43, namespace: 'demo' } });
    await flushPromises();
    expect(axios.delete).toHaveBeenCalledWith('console/42');
    const again = await mountComponent(Console);
    expect(consoleSession.currentConsole().pid).toBe(43);
    expect(again.find('.console-terminal').exists()).toBe(true);
  });

  it('lists the experiments again when the picker gets the focus', async () => {
    const page = await openPage();
    const lists = () =>
      axios.get.mock.calls.filter(([url]) => url.startsWith('experiments'))
        .length;
    expect(lists()).toBe(1);
    axios.get.mockImplementation((url) =>
      url.startsWith('experiments')
        ? Promise.resolve({ data: { experiments: [{ name: 'demo' }] } })
        : Promise.reject(notFound()),
    );
    await picker(page).trigger('focus');
    await flushPromises();
    expect(lists()).toBe(2);
    expect(options(page)).toEqual([
      ':All (no namespace) *',
      'Not running:demo',
    ]);
  });

  it('starts a console in the namespace at once once the last has ended', async () => {
    const page = await openPage();
    FakeWebSocket.instances.at(-1).onclose({ code: 1000 });
    await flushPromises();

    await picker(page).setValue('alpha');
    await flushPromises();
    expect(dialogs()).toHaveLength(0);
    expect(axios.post).toHaveBeenLastCalledWith('console?namespace=alpha');
    expect(consoleSession.currentConsole().namespace).toBe('alpha');
  });
});
