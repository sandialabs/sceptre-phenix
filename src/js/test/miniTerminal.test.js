// The SCORCH terminal: what it says when its websocket closes.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/store.js', () => ({ usePhenixStore: () => ({ token: null }) }));

const xterm = vi.hoisted(() => ({ terminals: [] }));
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    constructor() {
      this.written = [];
      xterm.terminals.push(this);
    }
    open() {}
    resize() {}
    loadAddon() {}
    write(data) {
      this.written.push(data);
    }
    dispose() {}
  },
}));
vi.mock('@xterm/addon-attach', () => ({ AttachAddon: class {} }));

import MiniTerminal from '@/components/MiniTerminal.vue';
import { mountComponent } from './helpers/mount.js';

class FakeWebSocket {
  static instances = [];

  constructor(url) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  close() {}
}

let freshness;

beforeEach(async () => {
  FakeWebSocket.instances = [];
  xterm.terminals = [];
  vi.stubGlobal('WebSocket', FakeWebSocket);
  vi.useFakeTimers();
  freshness = await import('@/utils/freshness.js');
});

afterEach(() => {
  freshness.setWsDown(false);
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const SHUTDOWN = '[terminal closed: phēnix server shutting down]';
const CLOSED = '[terminal connection closed]';

const open = () =>
  mountComponent(MiniTerminal, {
    props: { wsPath: '/api/v1/scorch/terminal/ws' },
  });

describe('the SCORCH terminal', () => {
  it.each([
    ['the server shutting down', { code: 1001, reason: '' }, 'never', SHUTDOWN],
    ['a normal close', { code: 1000, reason: '' }, 'never', CLOSED],
    ['a normal close as it shuts down', { code: 1000 }, 'before', SHUTDOWN],
    ['a drop', { code: 1006, reason: '' }, 'never', CLOSED],
    ['a drop, then the shutdown', { code: 1006 }, 'after', SHUTDOWN],
  ])('says it closed after %s', async (_, event, brokerAt, want) => {
    await open();
    if (brokerAt === 'before') freshness.setWsDown(true, 'shutdown');
    FakeWebSocket.instances[0].onclose(event);
    await vi.advanceTimersByTimeAsync(700);
    if (brokerAt === 'after') freshness.setWsDown(true, 'shutdown');
    await vi.advanceTimersByTimeAsync(800);
    expect(xterm.terminals[0].written.join('').trim()).toBe(want);
  });

  it('writes nothing once it has closed', async () => {
    const terminal = await open();
    FakeWebSocket.instances[0].onclose({ code: 1006 });
    terminal.unmount();
    await vi.advanceTimersByTimeAsync(1500);
    expect(xterm.terminals[0].written).toEqual([]);
  });
});
