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

const xterm = vi.hoisted(() => ({ terminals: [], attached: [] }));
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    constructor() {
      this.written = [];
      this.addons = [];
      this.opened = 0;
      this.disposed = false;
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
    write(data) {
      this.written.push(data);
    }
    dispose() {
      this.disposed = true;
    }
  },
}));
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

import { httpError } from './helpers/fixtures.js';
import { mountComponent, textOf } from './helpers/mount.js';
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

let consoleSession;
let consoleTerminal;

beforeEach(async () => {
  vi.resetModules();
  FakeWebSocket.instances = [];
  xterm.terminals = [];
  xterm.attached = [];
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
});

afterEach(() => {
  consoleSession.closeConsole();
  vi.unstubAllGlobals();
});

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

    xterm.terminals[0].resizeListener({ cols: 120, rows: 40 });
    expect(axios.post).toHaveBeenLastCalledWith(
      'console/42/size?cols=120&rows=40',
    );
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
    const session = await consoleTerminal.openConsole();
    session.mount(document.createElement('div'));
    FakeWebSocket.instances[0].onclose({ code: 1006 });
    await flushPromises();

    expect(axios.get).not.toHaveBeenCalled();
    expect(session.state).toBe('lost');
    expect(xterm.terminals[0].written.join('')).toContain(
      '[console connection lost]',
    );
    expect(consoleSession.currentConsole()).toBe(null);
    expect(consoleSession.rememberedConsolePid('alice')).toBe(42);
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
    const restart = page.find('button');
    expect(textOf(restart)).toBe('Start a new console');

    axios.post.mockResolvedValue({ data: { pid: 43 } });
    await restart.trigger('click');
    expect(ended.disposed).toBe(true);
    await flushPromises();
    expect(consoleSession.currentConsole().pid).toBe(43);
    expect(terminal(page).exists()).toBe(true);
  });

  it('explains a console the server does not offer', async () => {
    axios.post.mockRejectedValue(httpError(405));
    const page = await mountComponent(Console);
    await flushPromises();
    expect(textOf(page.find('h1'))).toBe('Console access is not configured.');
  });
});
