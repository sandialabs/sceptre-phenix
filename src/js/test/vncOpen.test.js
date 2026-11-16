// Opening a console (T36): the phenix-vnc duplicate query, its toast and
// "Open another anyway", a unique window name for each console tab, and the
// phenix-auth token handoff.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { modules, resetMocks, store } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/store.js', modules.store);

import {
  BLOCKED_MESSAGE,
  CONSOLE_CHANNEL,
  DUPLICATE_MESSAGE,
  OPEN_ANOTHER,
  OPEN_BLOCKED,
  QUERY_WAIT_MS,
  answerConsoleQueries,
  detachOpener,
  handleConsoleLinkClick,
  newConsoleWindowName,
  openConsole,
} from '@/vnc/openConsole.js';
import {
  AUTH_CHANNEL,
  TOKEN_WAIT_MS,
  answerTokenRequests,
  signInFromOtherTab,
} from '@/utils/authHandoff.js';

// A BroadcastChannel that delivers each message to the other open channels of
// the same name, as tabs of one origin get them.
class FakeChannel {
  static open = new Set();
  static posted = [];
  constructor(name) {
    this.name = name;
    this.onmessage = null;
    FakeChannel.open.add(this);
  }
  postMessage(data) {
    FakeChannel.posted.push({ name: this.name, data });
    const copy = structuredClone(data);
    for (const other of FakeChannel.open) {
      if (other !== this && other.name === this.name) {
        queueMicrotask(() => other.onmessage?.({ data: copy }));
      }
    }
  }
  close() {
    FakeChannel.open.delete(this);
  }
}

const HREF = '/vnc?vm=demo/web&vm=demo/db&layout=cols';
const VMS = ['demo/web', 'demo/db'];

let stops;
let open;
let notify;

beforeEach(() => {
  resetMocks();
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  FakeChannel.open = new Set();
  FakeChannel.posted = [];
  vi.stubGlobal('BroadcastChannel', FakeChannel);
  stops = [];
  open = vi.fn(() => ({}));
  notify = vi.fn();
});

afterEach(() => {
  stops.forEach((stop) => stop());
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// the module as another tab loads it, with its own tab id
async function otherTab() {
  vi.resetModules();
  return import('@/vnc/openConsole.js');
}

// another tab's console page, showing what currentVms gives
async function consoleTab(vms, currentVms = () => vms) {
  const other = await otherTab();
  const stop = other.answerConsoleQueries(currentVms);
  stops.push(stop);
  return stop;
}

async function opened(promise) {
  await vi.advanceTimersByTimeAsync(QUERY_WAIT_MS);
  return promise;
}

describe('the duplicate query', () => {
  it('asks the console tabs over phenix-vnc', async () => {
    expect(CONSOLE_CHANNEL).toBe('phenix-vnc');
    await opened(openConsole(HREF, VMS, { open, notify }));
    expect(FakeChannel.posted).toEqual([
      {
        name: 'phenix-vnc',
        data: {
          type: 'query',
          id: expect.any(String),
          tab: expect.stringMatching(/^[0-9a-f]{16}$/),
          vms: VMS,
        },
      },
    ]);
  });

  it('shows the toast when a tab already shows the same VMs, in any order', async () => {
    await consoleTab(['demo/db', 'demo/web']);
    const result = await opened(openConsole(HREF, VMS, { open, notify }));

    expect(result).toBe('duplicate');
    expect(open).not.toHaveBeenCalled();
    expect(notify).toHaveBeenCalledWith({
      message: DUPLICATE_MESSAGE,
      actionText: OPEN_ANOTHER,
      onAction: expect.any(Function),
    });
    expect(DUPLICATE_MESSAGE).toBe(
      'This console is already open in another tab',
    );
    expect(OPEN_ANOTHER).toBe('Open another anyway');
  });

  it('opens another window on "Open another anyway"', async () => {
    await consoleTab(VMS);
    await opened(openConsole(HREF, VMS, { open, notify }));
    notify.mock.lastCall[0].onAction();
    expect(open).toHaveBeenCalledWith(
      HREF,
      expect.stringMatching(/^phenix-vnc-/),
    );
  });

  it('opens a window when the tabs show other VMs', async () => {
    await consoleTab(['demo/web']);
    await consoleTab(['demo/web', 'demo/db', 'demo/hmi']);
    const result = await opened(openConsole(HREF, VMS, { open, notify }));
    expect(result).toBe('opened');
    expect(open).toHaveBeenCalledOnce();
    expect(notify).not.toHaveBeenCalled();
  });

  it('answers only queries for the VMs the console tab shows now', async () => {
    let shown = ['demo/hmi'];
    await consoleTab(null, () => shown);
    shown = VMS;
    expect(await opened(openConsole(HREF, VMS, { open, notify }))).toBe(
      'duplicate',
    );
  });

  it('stops answering when the console page closes', async () => {
    const stop = await consoleTab(VMS);
    stop();
    expect(await opened(openConsole(HREF, VMS, { open, notify }))).toBe(
      'opened',
    );
  });

  it('opens at once in a browser with no BroadcastChannel', async () => {
    vi.stubGlobal('BroadcastChannel', undefined);
    expect(await openConsole(HREF, VMS, { open, notify })).toBe('opened');
  });

  it('does not answer a query of its own tab', async () => {
    stops.push(answerConsoleQueries(() => VMS));
    expect(await opened(openConsole(HREF, VMS, { open, notify }))).toBe(
      'opened',
    );
    expect(notify).not.toHaveBeenCalled();
  });

  it('offers to open the console again when the browser blocked the window', async () => {
    open.mockReturnValue(null);
    expect(await opened(openConsole(HREF, VMS, { open, notify }))).toBe(
      'blocked',
    );
    expect(notify).toHaveBeenCalledWith({
      message: BLOCKED_MESSAGE,
      actionText: OPEN_BLOCKED,
      onAction: expect.any(Function),
    });
    // the click on the action lets the browser open it
    open.mockReturnValue({});
    notify.mock.lastCall[0].onAction();
    expect(open).toHaveBeenLastCalledWith(
      HREF,
      expect.stringMatching(/^phenix-vnc-/),
    );
    expect(notify).toHaveBeenCalledOnce();
  });

  it('shows the blocked toast with a button that opens the console', async () => {
    const wnd = vi.fn(() => null);
    vi.stubGlobal('open', wnd);
    vi.useRealTimers();
    const result = await openConsole(HREF, VMS);
    expect(result).toBe('blocked');
    const snackbar = document.querySelector('.snackbar');
    expect(snackbar?.textContent).toContain(BLOCKED_MESSAGE);
    const action = [...snackbar.querySelectorAll('button')].find((b) =>
      b.textContent.includes(OPEN_BLOCKED),
    );
    wnd.mockReturnValue({});
    action.click();
    expect(wnd).toHaveBeenCalledTimes(2);
  });
});

describe('the new window', () => {
  it('gets a unique name for each console tab, and keeps the opener', async () => {
    await opened(openConsole(HREF, VMS, { open, notify }));
    await opened(openConsole(HREF, VMS, { open, notify }));
    const [first, second] = open.mock.calls;
    // no features: noopener would keep sessionStorage from the new tab
    expect(first).toHaveLength(2);
    expect(first[1]).toMatch(/^phenix-vnc-[0-9a-f]{16}$/);
    expect(second[1]).not.toBe(first[1]);
    expect(newConsoleWindowName()).not.toBe(newConsoleWindowName());
  });

  it('lets go of the opener once the console page loads', () => {
    // browsers let a page set window.opener; happy-dom has only a getter
    Object.defineProperty(window, 'opener', {
      value: {},
      writable: true,
      configurable: true,
    });
    detachOpener();
    expect(window.opener).toBe(null);
    delete window.opener;
  });

  it.each([
    ['a plain click', {}, true],
    ['Ctrl+click', { ctrlKey: true }, false],
    ['Cmd+click', { metaKey: true }, false],
    ['Shift+click', { shiftKey: true }, false],
    ['Alt+click', { altKey: true }, false],
    ['a middle click', { button: 1 }, false],
  ])(
    '%s on a console link opens through the query: %s',
    async (_, fields, handled) => {
      const event = new MouseEvent('click', {
        cancelable: true,
        button: 0,
        ...fields,
      });
      const anchor = Object.assign(document.createElement('a'), { href: HREF });
      Object.defineProperty(event, 'currentTarget', { value: anchor });
      handleConsoleLinkClick(event, VMS, { open, notify });
      expect(event.defaultPrevented).toBe(handled);
      await vi.advanceTimersByTimeAsync(QUERY_WAIT_MS);
      expect(open.mock.calls.length).toBe(handled ? 1 : 0);
      if (handled) expect(open.mock.lastCall[0]).toBe(anchor.href);
    },
  );
});

describe('the phenix-auth token handoff', () => {
  const ROLE = { name: 'Experiment User', policies: [] };

  // a signed-in phēnix tab: answers token requests with its store
  function signedInTab() {
    const tabStore = {
      auth: true,
      token: 'tok',
      username: 'alice',
      role: ROLE,
    };
    stops.push(answerTokenRequests(() => tabStore));
    return tabStore;
  }

  beforeEach(() => {
    resetMocks({
      store: { auth: false, token: null, username: null, role: null },
    });
    store.login = vi.fn();
  });

  it('signs the console tab in with the token of another tab', async () => {
    expect(AUTH_CHANNEL).toBe('phenix-auth');
    signedInTab();
    const result = signInFromOtherTab();
    await vi.advanceTimersByTimeAsync(0);
    await expect(result).resolves.toBe(true);
    expect(store.login).toHaveBeenCalledWith(
      { token: 'tok', user: { username: 'alice', role: ROLE } },
      false,
      false,
    );
  });

  it('gives up when no tab answers, for the sign-in page', async () => {
    const result = signInFromOtherTab();
    await vi.advanceTimersByTimeAsync(TOKEN_WAIT_MS - 1);
    let settled = false;
    result.then(() => (settled = true));
    await vi.advanceTimersByTimeAsync(0);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    await expect(result).resolves.toBe(false);
    expect(store.login).not.toHaveBeenCalled();
  });

  it('does not ask when the tab is signed in already', async () => {
    store.auth = true;
    store.token = 'mine';
    await expect(signInFromOtherTab()).resolves.toBe(true);
    expect(FakeChannel.posted).toEqual([]);
  });

  it('a tab that is signed out or has an expired token does not answer', async () => {
    const tabStore = signedInTab();
    tabStore.auth = false;
    const expired = `x.${btoa(JSON.stringify({ exp: 1 }))}.y`;
    stops.push(
      answerTokenRequests(() => ({
        auth: true,
        token: expired,
        username: 'a',
        role: ROLE,
      })),
    );
    const result = signInFromOtherTab();
    await vi.advanceTimersByTimeAsync(TOKEN_WAIT_MS);
    await expect(result).resolves.toBe(false);
  });

  it('gives up at once in a browser with no BroadcastChannel', async () => {
    vi.stubGlobal('BroadcastChannel', undefined);
    await expect(signInFromOtherTab()).resolves.toBe(false);
    expect(answerTokenRequests(() => store)).toBeTypeOf('function');
  });
});
