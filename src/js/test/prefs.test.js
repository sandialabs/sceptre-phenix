// Values the browser remembers in localStorage, and what happens when it
// cannot.
import fc from 'fast-check';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  DEFAULT_LIVE_TILE_LIMIT,
  DEFAULT_VNC_SETTINGS,
  LIVE_TILE_LIMITS,
  loadLiveTileLimit,
  loadVncSettings,
  loadVncToolbarHidden,
  readPref,
  removePref,
  saveLiveTileLimit,
  saveVncSettings,
  saveVncToolbarHidden,
  writePref,
} from '@/utils/prefs.js';
import { blockedStorage } from './helpers/storage.js';

afterEach(() => vi.unstubAllGlobals());

// test/setup.js gives each test an empty stand-in localStorage
it('keeps values as strings in localStorage', () => {
  expect(readPref('a')).toBe(null);
  writePref('a', 7);
  expect(readPref('a')).toBe('7');
  removePref('a');
  expect(readPref('a')).toBe(null);
});

it('reads back any value as its string, and drops one with no string', () => {
  const value = fc.anything({ withNullPrototype: true });
  fc.assert(
    fc.property(fc.string(), value, (key, v) => {
      let text = null;
      try {
        text = String(v);
      } catch {
        // an object with no prototype has no string
      }
      expect(() => writePref(key, v)).not.toThrow();
      expect(readPref(key)).toBe(text);
      removePref(key);
      expect(readPref(key)).toBe(null);
    }),
  );
});

it.each([
  [
    'storage that throws',
    () => vi.stubGlobal('localStorage', blockedStorage()),
  ],
  ['no storage', () => vi.stubGlobal('localStorage', undefined)],
  [
    // when site data is blocked, even reading localStorage throws
    'a localStorage that cannot be read',
    () => {
      vi.stubGlobal('localStorage', undefined);
      Object.defineProperty(globalThis, 'localStorage', {
        configurable: true,
        get() {
          throw new Error('blocked');
        },
      });
    },
  ],
])('finds nothing and keeps nothing with %s', (_, stub) => {
  stub();
  expect(readPref('a')).toBe(null);
  expect(() => writePref('a', 1)).not.toThrow();
  expect(() => removePref('a')).not.toThrow();
});

// T33: the live tile limit of the VNC tab and the VM Tiles page (Q5)
describe('the live tile limit', () => {
  const KEY = 'phenix.vnc.liveTileLimit';

  it('offers 4, 8, 16, 24 and 32, and starts at 16', () => {
    expect(LIVE_TILE_LIMITS).toEqual([4, 8, 16, 24, 32]);
    expect(DEFAULT_LIVE_TILE_LIMIT).toBe(16);
    expect(loadLiveTileLimit()).toBe(16);
  });

  it.each(LIVE_TILE_LIMITS)('keeps %i', (limit) => {
    saveLiveTileLimit(limit);
    expect(readPref(KEY)).toBe(String(limit));
    expect(loadLiveTileLimit()).toBe(limit);
  });

  it('goes back to 16 for a stored value that is not a choice', () => {
    fc.assert(
      fc.property(fc.string(), (text) => {
        fc.pre(!LIVE_TILE_LIMITS.map(String).includes(text));
        writePref(KEY, text);
        expect(loadLiveTileLimit()).toBe(16);
      }),
      { numRuns: 200 },
    );
    for (const text of ['0', '-4', '5', '16.5', '1e1', ' 16', '0x10', '']) {
      writePref(KEY, text);
      expect(loadLiveTileLimit()).toBe(16);
    }
  });

  it('keeps nothing for a limit that is not a choice', () => {
    saveLiveTileLimit(8);
    saveLiveTileLimit(12);
    expect(loadLiveTileLimit()).toBe(8);
  });

  it('starts at 16 when storage throws', () => {
    vi.stubGlobal('localStorage', blockedStorage());
    expect(loadLiveTileLimit()).toBe(16);
    expect(() => saveLiveTileLimit(4)).not.toThrow();
  });
});

describe('the VNC console settings', () => {
  const key = (user) => `phenix.vnc.settings.${user}`;

  it('starts at Fit, quality 6 and compression 2', () => {
    expect(DEFAULT_VNC_SETTINGS).toEqual({
      scaling: 'fit',
      quality: 6,
      compression: 2,
    });
    expect(loadVncSettings('alice')).toEqual(DEFAULT_VNC_SETTINGS);
  });

  it('keeps the settings of each user apart', () => {
    saveVncSettings('alice', { scaling: 'actual', quality: 3, compression: 9 });
    expect(loadVncSettings('alice')).toEqual({
      scaling: 'actual',
      quality: 3,
      compression: 9,
    });
    expect(loadVncSettings('bob')).toEqual(DEFAULT_VNC_SETTINGS);
    expect(JSON.parse(readPref(key('alice')))).toEqual({
      scaling: 'actual',
      quality: 3,
      compression: 9,
    });
  });

  it.each([
    ['text that is not JSON', '{'],
    ['a list', '[1]'],
    ['an unknown scaling', '{"scaling":"zoom","quality":4}'],
    ['a quality above 9', '{"quality":10,"compression":4}'],
    ['a quality that is not a whole number', '{"quality":2.5}'],
    ['a compression below 0', '{"compression":-1}'],
  ])('uses the default for each value that %s gives', (_, text) => {
    writePref(key('alice'), text);
    const settings = loadVncSettings('alice');
    expect(Object.keys(settings)).toEqual([
      'scaling',
      'quality',
      'compression',
    ]);
    expect(['fit', 'actual', 'remote']).toContain(settings.scaling);
    for (const level of [settings.quality, settings.compression]) {
      expect(Number.isInteger(level) && level >= 0 && level <= 9).toBe(true);
    }
  });

  it('keeps a good value beside a bad one', () => {
    writePref(key('alice'), '{"scaling":"remote","quality":12}');
    expect(loadVncSettings('alice')).toEqual({
      scaling: 'remote',
      quality: 6,
      compression: 2,
    });
  });

  it('starts at the defaults when storage throws', () => {
    vi.stubGlobal('localStorage', blockedStorage());
    expect(loadVncSettings('alice')).toEqual(DEFAULT_VNC_SETTINGS);
    expect(() => saveVncSettings('alice', DEFAULT_VNC_SETTINGS)).not.toThrow();
  });
});

describe('the hidden top bar of the VNC console page', () => {
  it('shows the bar when the browser keeps no choice', () => {
    expect(loadVncToolbarHidden()).toBe(false);
  });

  it.each([true, false])('keeps the choice hidden=%s', (hidden) => {
    saveVncToolbarHidden(hidden);
    expect(loadVncToolbarHidden()).toBe(hidden);
  });

  it.each(['yes', '1', '{}'])('shows the bar for a stored %s', (value) => {
    writePref('phenix.vnc.toolbarHidden', value);
    expect(loadVncToolbarHidden()).toBe(false);
  });

  it('shows the bar, and drops the choice, when the browser blocks storage', () => {
    vi.stubGlobal('localStorage', blockedStorage());
    expect(() => saveVncToolbarHidden(true)).not.toThrow();
    expect(loadVncToolbarHidden()).toBe(false);
  });
});
