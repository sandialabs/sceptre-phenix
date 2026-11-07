// The keyboard chord and fullscreen of a console pane (T34): Ctrl+] matches
// event.code, so it works on any layout; then Tab, 1–4, F and Esc. Keyboard
// Lock only in a secure context that has it, with a toast over plain HTTP.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';
import { ToastProgrammatic as Toast } from 'buefy';

import {
  CHORD_HINT,
  attachChord,
  createChord,
  isChordStart,
} from '@/vnc/chord.js';
import {
  KEYBOARD_CAPTURE_NEEDS_HTTPS,
  enterFullscreen,
  exitFullscreen,
  keepNoticesInFullscreen,
  keyboardCaptureNote,
} from '@/vnc/fullscreen.js';

const key = (code, key, fields = {}) =>
  new KeyboardEvent('keydown', {
    code,
    key,
    bubbles: true,
    cancelable: true,
    ...fields,
  });

const ctrlBracket = (fields) =>
  key('BracketRight', ']', { ctrlKey: true, ...fields });

let handlers;
beforeEach(() => {
  handlers = {
    onLeave: vi.fn(),
    onSelectPane: vi.fn(),
    onExitFullscreen: vi.fn(),
  };
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  delete document.fullscreenElement;
  delete document.exitFullscreen;
});

describe('arming the chord', () => {
  it('matches Ctrl and the key in the US ] position, by event.code', () => {
    expect(isChordStart(ctrlBracket())).toBe(true);
    // a German keyboard has + in that position
    expect(isChordStart(key('BracketRight', '+', { ctrlKey: true }))).toBe(
      true,
    );
    // ] typed elsewhere, as with AltGr+9 on a German keyboard, is not it
    expect(
      isChordStart(key('Digit9', ']', { ctrlKey: true, altKey: true })),
    ).toBe(false);
    expect(isChordStart(key('BracketRight', ']'))).toBe(false);
    expect(isChordStart(ctrlBracket({ altKey: true }))).toBe(false);
    expect(isChordStart(ctrlBracket({ metaKey: true }))).toBe(false);
    expect(isChordStart(ctrlBracket({ shiftKey: true }))).toBe(false);
  });

  it('a German layout event arms it, and the console does not get it', () => {
    const chord = createChord(handlers);
    const event = key('BracketRight', '+', { ctrlKey: true });
    expect(chord.handleKeydown(event)).toBe(true);
    expect(chord.armed.value).toBe(true);
    expect(event.defaultPrevented).toBe(true);
  });

  it('names the way out in its hint', () => {
    expect(CHORD_HINT).toBe('Ctrl+] then Tab to leave');
  });
});

describe('the key after Ctrl+]', () => {
  function armed() {
    const chord = createChord(handlers);
    chord.handleKeydown(ctrlBracket());
    return chord;
  }

  it('Tab leaves the console', () => {
    const chord = armed();
    const event = key('Tab', 'Tab');
    expect(chord.handleKeydown(event)).toBe(true);
    expect(event.defaultPrevented).toBe(true);
    expect(handlers.onLeave).toHaveBeenCalledWith({ backward: false });
    expect(chord.armed.value).toBe(false);
  });

  it('Shift+Tab leaves the console backward', () => {
    armed().handleKeydown(key('Tab', 'Tab', { shiftKey: true }));
    expect(handlers.onLeave).toHaveBeenCalledWith({ backward: true });
  });

  it.each([
    ['Digit1', '1', 1],
    ['Digit2', '2', 2],
    ['Digit3', '3', 3],
    ['Digit4', '4', 4],
    // a French keyboard types & in the 1 position without Shift
    ['Digit1', '&', 1],
    ['Numpad4', '4', 4],
  ])('%s (%s) moves keyboard input to pane %i', (code, k, pane) => {
    const chord = armed();
    expect(chord.handleKeydown(key(code, k))).toBe(true);
    expect(handlers.onSelectPane).toHaveBeenCalledWith(pane);
    expect(chord.armed.value).toBe(false);
  });

  it('F exits fullscreen', () => {
    expect(armed().handleKeydown(key('KeyF', 'f'))).toBe(true);
    expect(handlers.onExitFullscreen).toHaveBeenCalledOnce();
  });

  it('Esc cancels the chord', () => {
    const chord = armed();
    const event = key('Escape', 'Escape');
    expect(chord.handleKeydown(event)).toBe(true);
    expect(event.defaultPrevented).toBe(true);
    expect(chord.armed.value).toBe(false);
    expect(
      Object.values(handlers).every((h) => h.mock.calls.length === 0),
    ).toBe(true);
  });

  it('a modifier keeps it armed and goes to the console', () => {
    const chord = armed();
    expect(
      chord.handleKeydown(key('ShiftLeft', 'Shift', { shiftKey: true })),
    ).toBe(false);
    expect(chord.armed.value).toBe(true);
  });

  it('any other key ends it and goes to the console', () => {
    const chord = armed();
    const event = key('KeyA', 'a');
    expect(chord.handleKeydown(event)).toBe(false);
    expect(event.defaultPrevented).toBe(false);
    expect(chord.armed.value).toBe(false);
  });

  it('a key with no chord armed goes to the console', () => {
    const chord = createChord(handlers);
    expect(chord.handleKeydown(key('Tab', 'Tab'))).toBe(false);
    expect(handlers.onLeave).not.toHaveBeenCalled();
  });
});

describe('the pane listener', () => {
  it('gets keys in the capture phase, before the console canvas does', () => {
    const pane = document.createElement('div');
    const canvas = document.createElement('canvas');
    pane.append(canvas);
    document.body.append(pane);
    const canvasKeys = vi.fn();
    canvas.addEventListener('keydown', canvasKeys);
    const { armed, detach } = attachChord(pane, handlers);

    canvas.dispatchEvent(ctrlBracket());
    expect(armed.value).toBe(true);
    canvas.dispatchEvent(key('Tab', 'Tab'));
    expect(handlers.onLeave).toHaveBeenCalledOnce();
    canvas.dispatchEvent(key('KeyA', 'a'));
    expect(canvasKeys).toHaveBeenCalledOnce();

    detach();
    canvas.dispatchEvent(ctrlBracket());
    expect(armed.value).toBe(false);
    expect(canvasKeys).toHaveBeenCalledTimes(2);
  });

  it('a chord that focus left behind does not apply when focus comes back', () => {
    const pane = document.createElement('div');
    document.body.append(pane);
    const { armed } = attachChord(pane, handlers);
    pane.dispatchEvent(ctrlBracket());
    pane.dispatchEvent(new FocusEvent('focusout'));
    expect(armed.value).toBe(false);
  });
});

describe('fullscreen and Keyboard Lock', () => {
  function stubBrowser({ secure, keyboard }) {
    vi.stubGlobal('isSecureContext', secure);
    const nav = Object.create(navigator);
    if (keyboard) Object.defineProperty(nav, 'keyboard', { value: keyboard });
    vi.stubGlobal('navigator', nav);
  }
  const pane = () =>
    Object.assign(document.createElement('div'), {
      requestFullscreen: vi.fn().mockResolvedValue(undefined),
    });
  const keyboard = () => ({
    lock: vi.fn().mockResolvedValue(undefined),
    unlock: vi.fn(),
  });

  it('locks the keyboard in a secure context that has Keyboard Lock', async () => {
    const kb = keyboard();
    stubBrowser({ secure: true, keyboard: kb });
    const toast = vi.fn();
    const el = pane();
    await expect(enterFullscreen(el, { toast })).resolves.toEqual({
      keyboardLocked: true,
    });
    expect(el.requestFullscreen).toHaveBeenCalledOnce();
    expect(kb.lock).toHaveBeenCalledOnce();
    expect(toast).not.toHaveBeenCalled();
    expect(keyboardCaptureNote()).toBe(null);
  });

  it('does not call Keyboard Lock over plain HTTP, and the HTTPS toast shows', async () => {
    const kb = keyboard();
    stubBrowser({ secure: false, keyboard: kb });
    const toast = vi.fn();
    const el = pane();
    await expect(enterFullscreen(el, { toast })).resolves.toEqual({
      keyboardLocked: false,
    });
    expect(el.requestFullscreen).toHaveBeenCalledOnce();
    expect(kb.lock).not.toHaveBeenCalled();
    expect(toast).toHaveBeenCalledWith(KEYBOARD_CAPTURE_NEEDS_HTTPS);
    expect(KEYBOARD_CAPTURE_NEEDS_HTTPS).toBe('Keyboard capture needs HTTPS');
    expect(keyboardCaptureNote()).toBe(KEYBOARD_CAPTURE_NEEDS_HTTPS);
  });

  it('keeps normal fullscreen in a browser with no Keyboard Lock', async () => {
    stubBrowser({ secure: true, keyboard: null });
    const toast = vi.fn();
    const el = pane();
    await expect(enterFullscreen(el, { toast })).resolves.toEqual({
      keyboardLocked: false,
    });
    expect(el.requestFullscreen).toHaveBeenCalledOnce();
    expect(toast).not.toHaveBeenCalled();
  });

  it('keeps fullscreen when Keyboard Lock fails', async () => {
    const kb = keyboard();
    kb.lock.mockRejectedValue(new Error('not allowed'));
    stubBrowser({ secure: true, keyboard: kb });
    await expect(enterFullscreen(pane(), { toast: vi.fn() })).resolves.toEqual({
      keyboardLocked: false,
    });
  });

  it('unlocks the keyboard and leaves fullscreen', async () => {
    const kb = keyboard();
    stubBrowser({ secure: true, keyboard: kb });
    const exit = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(document, 'exitFullscreen', {
      value: exit,
      configurable: true,
    });
    Object.defineProperty(document, 'fullscreenElement', {
      value: document.body,
      configurable: true,
    });
    await exitFullscreen();
    expect(kb.unlock).toHaveBeenCalledOnce();
    expect(exit).toHaveBeenCalledOnce();

    delete document.fullscreenElement;
    exit.mockClear();
    await exitFullscreen();
    expect(exit).not.toHaveBeenCalled();
  });
});

describe('notices in fullscreen', () => {
  const setFullscreen = (el) => {
    Object.defineProperty(document, 'fullscreenElement', {
      value: el,
      configurable: true,
    });
    document.dispatchEvent(new Event('fullscreenchange'));
  };
  const toastIn = (el) => el.querySelector(':scope > .notices .toast');

  it('shows the HTTPS toast inside the fullscreen element', async () => {
    vi.stubGlobal('isSecureContext', false);
    const el = document.createElement('div');
    document.body.append(el);
    el.requestFullscreen = vi.fn(async () => setFullscreen(el));
    await enterFullscreen(el);
    await flushPromises();
    expect(toastIn(el)?.textContent).toContain(KEYBOARD_CAPTURE_NEEDS_HTTPS);
  });

  it('mounts toasts inside the fullscreen element, and on the body again after it', async () => {
    const stop = keepNoticesInFullscreen();
    const el = document.createElement('div');
    document.body.append(el);
    setFullscreen(el);
    new Toast().open({ message: 'inside' });
    await flushPromises();
    expect(toastIn(el)?.textContent).toContain('inside');

    setFullscreen(null);
    new Toast().open({ message: 'outside' });
    await flushPromises();
    expect(toastIn(document.body)?.textContent).toContain('outside');
    expect(el.hasAttribute('data-vnc-notices')).toBe(false);
    stop();
  });
});
