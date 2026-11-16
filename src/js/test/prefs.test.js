// Values the browser remembers in localStorage, and what happens when it
// cannot.
import fc from 'fast-check';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  DEFAULT_LIVE_TILE_LIMIT,
  DEFAULT_VNC_SETTINGS,
  VNC_ZOOMS,
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
});

describe('the VNC console settings', () => {
  const key = (user) => `phenix.vnc.settings.${user}`;

  // the settings with the defaults for those that `values` does not give
  const withDefaults = (values) => ({ ...DEFAULT_VNC_SETTINGS, ...values });

  it('starts at Fit at 100 %, quality 6 and compression 2, with the pane headers and banners shown', () => {
    expect(DEFAULT_VNC_SETTINGS).toEqual({
      scaling: 'fit',
      zoom: 1,
      quality: 6,
      compression: 2,
      paneHeaders: true,
      banners: true,
    });
    expect(loadVncSettings('alice')).toEqual(DEFAULT_VNC_SETTINGS);
  });

  it('keeps the settings of each user apart', () => {
    const alice = {
      scaling: 'actual',
      zoom: 1.5,
      quality: 3,
      compression: 9,
      paneHeaders: false,
      banners: false,
    };
    saveVncSettings('alice', alice);
    expect(loadVncSettings('alice')).toEqual(alice);
    expect(loadVncSettings('bob')).toEqual(DEFAULT_VNC_SETTINGS);
    expect(JSON.parse(readPref(key('alice')))).toEqual(alice);
  });

  it.each([
    ['text that is not JSON', '{'],
    ['a list', '[1]'],
    ['an unknown scaling', '{"scaling":"zoom","quality":4}'],
    ['a quality above 9', '{"quality":10,"compression":4}'],
    ['a quality that is not a whole number', '{"quality":2.5}'],
    ['a compression below 0', '{"compression":-1}'],
    ['a zoom that is not a level', '{"zoom":1.25}'],
    ['headers that are not true or false', '{"paneHeaders":"no"}'],
    ['banners that are not true or false', '{"banners":0}'],
  ])('uses the default for each value that %s gives', (_, text) => {
    writePref(key('alice'), text);
    const settings = loadVncSettings('alice');
    expect(Object.keys(settings)).toEqual(Object.keys(DEFAULT_VNC_SETTINGS));
    expect(['fit', 'actual', 'remote']).toContain(settings.scaling);
    expect(VNC_ZOOMS).toContain(settings.zoom);
    expect(settings.paneHeaders).toBe(true);
    expect(settings.banners).toBe(true);
    for (const level of [settings.quality, settings.compression]) {
      expect(Number.isInteger(level) && level >= 0 && level <= 9).toBe(true);
    }
  });

  it('keeps a good value beside a bad one', () => {
    writePref(
      key('alice'),
      '{"scaling":"remote","quality":12,"zoom":3,"banners":false}',
    );
    expect(loadVncSettings('alice')).toEqual(
      withDefaults({ scaling: 'remote', banners: false }),
    );
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
});

it.each([
  [
    'the live tile limit',
    loadLiveTileLimit,
    () => saveLiveTileLimit(4),
    DEFAULT_LIVE_TILE_LIMIT,
  ],
  [
    'the VNC console settings',
    () => loadVncSettings('alice'),
    () => saveVncSettings('alice', DEFAULT_VNC_SETTINGS),
    DEFAULT_VNC_SETTINGS,
  ],
  [
    'the hidden top bar',
    loadVncToolbarHidden,
    () => saveVncToolbarHidden(true),
    false,
  ],
])('%s starts at its default when storage throws', (_, load, save, want) => {
  vi.stubGlobal('localStorage', blockedStorage());
  expect(save).not.toThrow();
  expect(load()).toEqual(want);
});
