// The terminal: what it says when its websocket closes, and the way out of it.
import { afterEach, beforeEach, describe, expect, it, test, vi } from 'vitest';
import { mount } from '@vue/test-utils';

vi.mock('@/store.js', () => ({ usePhenixStore: () => ({ token: null }) }));

// A stand-in for xterm.js that keeps the key handler and what goes in and out.
const xterm = vi.hoisted(() => ({
  terminals: [],
  handler: null,
  textarea: null,
  sent: [],
}));
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    constructor() {
      this.written = [];
      xterm.terminals.push(this);
    }
    attachCustomKeyEventHandler(handler) {
      xterm.handler = handler;
    }
    loadAddon() {}
    open(parent) {
      this.textarea = document.createElement('textarea');
      parent.appendChild(this.textarea);
      xterm.textarea = this.textarea;
    }
    resize() {}
    write(data) {
      this.written.push(data);
    }
    input(data) {
      xterm.sent.push(data);
    }
    dispose() {}
  },
}));
vi.mock('@xterm/addon-attach', () => ({ AttachAddon: class {} }));
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

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

const SHUTDOWN = '[terminal closed: phēnix server shutting down]';
const CLOSED = '[terminal connection closed]';

const open = () =>
  mountComponent(MiniTerminal, {
    props: { wsPath: '/api/v1/scorch/terminal/ws' },
  });

describe('the SCORCH terminal', () => {
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

function key(type, init) {
  return new KeyboardEvent(type, { cancelable: true, ...init });
}

describe('MiniTerminal', () => {
  beforeEach(() => {
    xterm.sent = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('the terminal input is described by the visible way out', () => {
    const wrapper = mount(MiniTerminal, { props: { wsPath: '/ws' } });
    const hint = wrapper.find('p.help');

    expect(hint.text()).toContain('Ctrl+]');
    expect(xterm.textarea.getAttribute('aria-describedby')).toBe(
      hint.attributes('id'),
    );
  });

  test('a Ctrl+] is dropped when focus leaves or the session changes', async () => {
    const wrapper = mount(MiniTerminal, { props: { wsPath: '/ws' } });
    const handle = xterm.handler;
    const chord = () => key('keydown', { key: ']', ctrlKey: true });
    const tab = () => key('keydown', { key: 'Tab', code: 'Tab' });

    handle(chord());
    xterm.textarea.dispatchEvent(new FocusEvent('blur'));
    expect(handle(tab())).toBe(true);

    handle(chord());
    await wrapper.setProps({ wsPath: '/ws2' });
    expect(handle(tab())).toBe(true);
    expect(xterm.sent).toEqual([]);
  });

  test('Tab goes to the shell until Ctrl+] lets the next one leave', () => {
    mount(MiniTerminal, { props: { wsPath: '/ws' } });
    const handle = xterm.handler;

    expect(handle(key('keydown', { key: 'Tab', code: 'Tab' }))).toBe(true);

    const chord = key('keydown', {
      key: ']',
      code: 'BracketRight',
      ctrlKey: true,
    });
    expect(handle(chord)).toBe(false);
    expect(chord.defaultPrevented).toBe(true);

    // Shift alone keeps the way out open for Shift+Tab.
    expect(handle(key('keydown', { key: 'Shift', shiftKey: true }))).toBe(true);
    expect(
      handle(key('keydown', { key: 'Tab', code: 'Tab', shiftKey: true })),
    ).toBe(false);

    // The way out is used once.
    expect(handle(key('keydown', { key: 'Tab', code: 'Tab' }))).toBe(true);
    expect(xterm.sent).toEqual([]);
  });

  test('another key after Ctrl+] sends Ctrl+], that key, and Tab to the shell', () => {
    mount(MiniTerminal, { props: { wsPath: '/ws' } });
    const handle = xterm.handler;

    handle(key('keydown', { key: ']', code: 'BracketRight', ctrlKey: true }));
    expect(xterm.sent).toEqual([]);
    expect(handle(key('keydown', { key: 'a', code: 'KeyA' }))).toBe(true);
    expect(xterm.sent).toEqual(['\x1d']);
    expect(handle(key('keydown', { key: 'Tab', code: 'Tab' }))).toBe(true);
    expect(xterm.sent).toEqual(['\x1d']);
  });

  test('Ctrl+] is matched by the character, not the key position', () => {
    mount(MiniTerminal, { props: { wsPath: '/ws' } });
    const handle = xterm.handler;

    // On a German layout this key position types "+", and Ctrl++ zooms.
    expect(
      handle(key('keydown', { key: '+', code: 'BracketRight', ctrlKey: true })),
    ).toBe(true);
    expect(handle(key('keydown', { key: 'Tab', code: 'Tab' }))).toBe(true);

    for (const extra of ['shiftKey', 'altKey', 'metaKey']) {
      expect(
        handle(key('keydown', { key: ']', ctrlKey: true, [extra]: true })),
      ).toBe(true);
    }
    expect(xterm.sent).toEqual([]);
  });

  test('Escape, plain ], and Ctrl+Shift+M still go to the shell', () => {
    mount(MiniTerminal, { props: { wsPath: '/ws' } });
    const handle = xterm.handler;

    expect(handle(key('keydown', { key: 'Escape', code: 'Escape' }))).toBe(
      true,
    );
    expect(handle(key('keydown', { key: ']', code: 'BracketRight' }))).toBe(
      true,
    );
    expect(
      handle(
        key('keydown', {
          key: 'M',
          code: 'KeyM',
          ctrlKey: true,
          shiftKey: true,
        }),
      ),
    ).toBe(true);
  });
});
