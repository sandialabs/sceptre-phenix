// The keys of the Keys menu (D5): the keysyms that the toolbar sends, the key
// sequences, and the connection labels of the top bar.
import { describe, expect, it } from 'vitest';

import {
  HOLD_KEYS,
  KEYS,
  RELEASE_KEYS,
  keyEvents,
  SEND_KEYS,
} from '@/vnc/keysyms.js';
import {
  announcement,
  connectionLabel,
  connectionTone,
  detailsLine,
  showsReconnect,
} from '@/vnc/connectionStatus.js';

describe('the keysyms', () => {
  // the X11 keysym values (X11/keysymdef.h), as noVNC core/input/keysym.js
  it.each([
    ['escape', 0xff1b, 'Escape'],
    ['tab', 0xff09, 'Tab'],
    ['delete', 0xffff, 'Delete'],
    ['printScreen', 0xff61, 'PrintScreen'],
    ['controlLeft', 0xffe3, 'ControlLeft'],
    ['altLeft', 0xffe9, 'AltLeft'],
    ['superLeft', 0xffeb, 'MetaLeft'],
    ['f1', 0xffbe, 'F1'],
    ['f7', 0xffc4, 'F7'],
    ['f12', 0xffc9, 'F12'],
  ])('has %s as keysym %i with the code %s', (name, keysym, code) => {
    expect(KEYS[name]).toEqual({ keysym, code });
  });

  it('presses Ctrl, Alt and Del, then lets them go in the other order', () => {
    expect(keyEvents('ctrlAltDel')).toEqual([
      [0xffe3, 'ControlLeft', true],
      [0xffe9, 'AltLeft', true],
      [0xffff, 'Delete', true],
      [0xffff, 'Delete', false],
      [0xffe9, 'AltLeft', false],
      [0xffe3, 'ControlLeft', false],
    ]);
  });

  it.each([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12])(
    'sends Ctrl+Alt+F%i',
    (n) => {
      const f = 0xffbe + n - 1;
      expect(keyEvents(`ctrlAltF${n}`)).toEqual([
        [0xffe3, 'ControlLeft', true],
        [0xffe9, 'AltLeft', true],
        [f, `F${n}`, true],
        [f, `F${n}`, false],
        [0xffe9, 'AltLeft', false],
        [0xffe3, 'ControlLeft', false],
      ]);
    },
  );

  it.each([
    ['escape', 0xff1b, 'Escape'],
    ['tab', 0xff09, 'Tab'],
    ['printScreen', 0xff61, 'PrintScreen'],
  ])('taps %s', (name, keysym, code) => {
    expect(keyEvents(name)).toEqual([
      [keysym, code, true],
      [keysym, code, false],
    ]);
  });

  it('has no sequence for an unknown name', () => {
    expect(keyEvents('bogus')).toEqual([]);
    expect(keyEvents('ctrlAltF13')).toEqual([]);
  });

  it('offers each key the Keys menu sends', () => {
    expect(SEND_KEYS.map((key) => key.name)).toEqual([
      'ctrlAltDel',
      'escape',
      'tab',
      'printScreen',
    ]);
    expect(Object.keys(HOLD_KEYS)).toEqual(['ctrl', 'alt', 'win']);
  });

  it('lets go of every modifier key on both sides', () => {
    const codes = RELEASE_KEYS.map((key) => key.code);
    expect(codes).toEqual([
      'ControlLeft',
      'ControlRight',
      'AltLeft',
      'AltRight',
      'ShiftLeft',
      'ShiftRight',
      'MetaLeft',
      'MetaRight',
    ]);
  });
});

describe('the connection indicator', () => {
  it.each([
    [{ state: 'idle' }, 'Not connected', 'off'],
    [{ state: 'loading' }, 'Connecting', 'busy'],
    [{ state: 'connecting' }, 'Connecting', 'busy'],
    [{ state: 'connected' }, 'Connected', 'ok'],
    [{ state: 'checking' }, 'Checking the connection', 'busy'],
    [{ state: 'waiting', retrySeconds: 5 }, 'Reconnecting in 5 s', 'wait'],
    [{ state: 'waiting', retrySeconds: null }, 'Reconnecting', 'wait'],
    [{ state: 'notRunning' }, 'VM is not running', 'error'],
    [{ state: 'experimentStopped' }, 'Experiment stopped', 'error'],
    [{ state: 'denied' }, 'No permission', 'error'],
    [{ state: 'notFound' }, 'VM not found', 'error'],
    [{ state: 'failed' }, 'Connection failed', 'error'],
  ])('labels %o as "%s"', (status, label, tone) => {
    expect(connectionLabel(status)).toBe(label);
    expect(connectionTone(status.state)).toBe(tone);
  });

  it('announces a wait without the countdown, which changes each second', () => {
    expect(announcement('web', { state: 'waiting', retrySeconds: 4 })).toBe(
      'web: Reconnecting',
    );
    expect(announcement('web', { state: 'connected' })).toBe('web: Connected');
  });

  it.each([
    ['idle', false],
    ['loading', false],
    ['connecting', false],
    ['connected', false],
    ['checking', false],
    ['waiting', true],
    ['notRunning', true],
    ['experimentStopped', true],
    ['denied', true],
    ['notFound', true],
    ['failed', true],
  ])('offers Reconnect in the state %s: %s', (state, offered) => {
    expect(showsReconnect(state)).toBe(offered);
  });

  it('tells the resolution, scaling, encoding, quality and copy/paste mode', () => {
    expect(
      detailsLine({
        width: 1024,
        height: 768,
        scale: 1.18,
        scaling: 'fit',
        quality: 6,
        clipboardMode: 'pasteAsKeys',
      }),
    ).toBe('1024×768 · scaled 118% · Tight · quality 6 · paste as keys');
    expect(
      detailsLine({
        width: 0,
        height: 0,
        scale: null,
        scaling: 'actual',
        quality: 3,
        clipboardMode: 'bidirectional',
      }),
    ).toBe('actual size · Tight · quality 3 · bidirectional copy/paste');
    expect(
      detailsLine({ scaling: 'remote', quality: 6, clipboardMode: null }),
    ).toBe('VM resized to fit · Tight · quality 6');
  });
});
