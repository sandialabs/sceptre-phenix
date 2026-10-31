// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';
import MiniTerminal from '@/components/MiniTerminal.vue';

// A stand-in for xterm.js that keeps the key handler and the data sent.
const xterm = vi.hoisted(() => ({ handler: null, textarea: null, sent: [] }));
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
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
    onResize() {}
    write() {}
    input(data) {
      xterm.sent.push(data);
    }
  },
}));
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class {} }));
vi.mock('@xterm/addon-attach', () => ({ AttachAddon: class {} }));
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

enableAutoUnmount(afterEach);

function key(type, init) {
  return new KeyboardEvent(type, { cancelable: true, ...init });
}

describe('MiniTerminal', () => {
  beforeEach(() => {
    xterm.sent = [];
    vi.stubGlobal(
      'WebSocket',
      class {
        close() {}
      },
    );
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
