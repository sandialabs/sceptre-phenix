import { beforeEach, describe, expect, test } from 'vitest';
import { watch } from 'vue';

import { LAYOUT_ALGORITHMS } from '@/builder/layouts/index.js';
import {
  OPEN_ZOOM_PERCENT,
  SETTINGS_STORAGE_KEY,
  SETTING_DEFAULTS,
  builderSettings,
  followSettings,
  isSettingValue,
  loadSettings,
  resetSettings,
  setSetting,
  settingsAtDefaults,
} from '@/builder/settings.js';

import { memoryStorage } from './fixtures.js';

const throwing = {
  getItem() {
    throw new Error('blocked');
  },
  setItem() {
    throw new Error('blocked');
  },
  removeItem() {
    throw new Error('blocked');
  },
};

function stored(storage) {
  return JSON.parse(storage.entries.get(SETTINGS_STORAGE_KEY) ?? 'null');
}

beforeEach(() => {
  resetSettings(null);
});

describe('Builder settings', () => {
  test('default to ELK layered, the minimap, node notes, 100% and the system’s motion', () => {
    expect(SETTINGS_STORAGE_KEY).toBe('phenix.builder.settings');
    expect(SETTING_DEFAULTS).toEqual({
      layoutAlgorithm: 'elk',
      showMinimap: true,
      showNodeNotes: true,
      openZoom: 'actual',
      openZoomPercent: 100,
      reduceMotion: false,
    });
    expect(LAYOUT_ALGORITHMS.map((algorithm) => algorithm.id)).toEqual([
      'elk',
      'cards',
      'dagre',
      'standard',
    ]);
    for (const algorithm of LAYOUT_ALGORITHMS) {
      expect(algorithm.label).toBeTruthy();
      expect(algorithm.description).toBeTruthy();
      expect(isSettingValue('layoutAlgorithm', algorithm.id)).toBe(true);
    }

    // Nothing stored, or no storage at all.
    expect({ ...loadSettings(memoryStorage()) }).toEqual(SETTING_DEFAULTS);
    expect({ ...loadSettings(null) }).toEqual(SETTING_DEFAULTS);
    expect(settingsAtDefaults()).toBe(true);
  });

  test('keep only the settings changed from their defaults, and read them back', () => {
    const storage = memoryStorage();

    expect(setSetting('layoutAlgorithm', 'dagre', storage)).toBe(true);
    expect(setSetting('showMinimap', false, storage)).toBe(true);
    expect(setSetting('showNodeNotes', false, storage)).toBe(true);
    expect(setSetting('openZoom', 'fit', storage)).toBe(true);
    expect(setSetting('showNodeNotes', 'no', storage)).toBe(false);
    expect(builderSettings.layoutAlgorithm).toBe('dagre');
    expect(builderSettings.showNodeNotes).toBe(false);
    expect(settingsAtDefaults()).toBe(false);
    expect(stored(storage)).toEqual({
      layoutAlgorithm: 'dagre',
      showMinimap: false,
      showNodeNotes: false,
      openZoom: 'fit',
    });

    // A new page reads them back.
    resetSettings(null);
    expect(loadSettings(storage)).toMatchObject({
      layoutAlgorithm: 'dagre',
      showMinimap: false,
      showNodeNotes: false,
      openZoom: 'fit',
      reduceMotion: false,
    });

    // A setting back at its default is not kept; none left removes the key.
    setSetting('layoutAlgorithm', 'elk', storage);
    setSetting('openZoom', 'actual', storage);
    setSetting('showNodeNotes', true, storage);
    expect(stored(storage)).toEqual({ showMinimap: false });
    setSetting('showMinimap', true, storage);
    expect(storage.entries.has(SETTINGS_STORAGE_KEY)).toBe(false);

    setSetting('reduceMotion', true, storage);
    resetSettings(storage);
    expect({ ...builderSettings }).toEqual(SETTING_DEFAULTS);
    expect(storage.entries.has(SETTINGS_STORAGE_KEY)).toBe(false);
  });

  test('a custom zoom is a percentage from 20 to 200, in steps of 5', () => {
    const storage = memoryStorage();

    expect(OPEN_ZOOM_PERCENT).toEqual({ min: 20, max: 200, step: 5 });
    expect(isSettingValue('openZoom', 'custom')).toBe(true);

    for (const percent of [20, 100, 75, 200]) {
      expect(isSettingValue('openZoomPercent', percent), percent).toBe(true);
    }

    for (const percent of [15, 33, 205, 0, -50, 72.5, NaN, '50', null]) {
      expect(setSetting('openZoomPercent', percent, storage), percent).toBe(
        false,
      );
    }
    expect(storage.entries.size).toBe(0);

    expect(setSetting('openZoom', 'custom', storage)).toBe(true);
    expect(setSetting('openZoomPercent', 75, storage)).toBe(true);
    expect(stored(storage)).toEqual({
      openZoom: 'custom',
      openZoomPercent: 75,
    });

    // A new page reads both back.
    resetSettings(null);
    expect(loadSettings(storage)).toMatchObject({
      openZoom: 'custom',
      openZoomPercent: 75,
    });

    // Reset to defaults clears both.
    resetSettings(storage);
    expect(builderSettings.openZoom).toBe('actual');
    expect(builderSettings.openZoomPercent).toBe(100);
    expect(storage.entries.has(SETTINGS_STORAGE_KEY)).toBe(false);
  });

  test('what an earlier Builder stored for the zoom still reads', () => {
    // Before the custom zoom there was no percentage: 100% and Fit stay
    // what they were, and the percentage takes its default.
    for (const openZoom of ['actual', 'fit']) {
      const storage = memoryStorage({
        [SETTINGS_STORAGE_KEY]: JSON.stringify({ openZoom }),
      });

      expect({ ...loadSettings(storage) }, openZoom).toEqual({
        ...SETTING_DEFAULTS,
        openZoom,
      });
    }

    // A percentage this Builder does not take is dropped by itself: Custom
    // then opens at the default percentage.
    const odd = memoryStorage({
      [SETTINGS_STORAGE_KEY]: JSON.stringify({
        openZoom: 'custom',
        openZoomPercent: 33,
      }),
    });

    expect({ ...loadSettings(odd) }).toEqual({
      ...SETTING_DEFAULTS,
      openZoom: 'custom',
    });
  });

  test('store only the values they offer: no names, content or other keys', () => {
    const storage = memoryStorage();

    for (const [key, value] of [
      ['layoutAlgorithm', 'Secret lab'],
      ['layoutAlgorithm', undefined],
      ['showMinimap', 'false'],
      ['openZoom', 1.5],
      ['openZoom', 75],
      ['openZoomPercent', 'custom'],
      ['reduceMotion', null],
      ['draftName', 'Secret lab'],
      ['token', 'abc'],
      ['__proto__', {}],
    ]) {
      expect(setSetting(key, value, storage), key).toBe(false);
    }
    expect(storage.entries.size).toBe(0);
    expect({ ...builderSettings }).toEqual(SETTING_DEFAULTS);
  });

  test('apply for the page when storage is blocked', () => {
    expect({ ...loadSettings(throwing) }).toEqual(SETTING_DEFAULTS);
    expect(setSetting('layoutAlgorithm', 'cards', throwing)).toBe(true);
    expect(builderSettings.layoutAlgorithm).toBe('cards');
    expect(() => resetSettings(throwing)).not.toThrow();
    expect(builderSettings.layoutAlgorithm).toBe('elk');

    // Full: the write fails, the choice still applies.
    const full = { ...memoryStorage(), setItem: throwing.setItem };
    expect(setSetting('reduceMotion', true, full)).toBe(true);
    expect(builderSettings.reduceMotion).toBe(true);
  });

  test('drop what another version stored that this one does not offer', () => {
    const storage = memoryStorage({
      [SETTINGS_STORAGE_KEY]: JSON.stringify({
        layoutAlgorithm: 'cola',
        showMinimap: false,
        openZoom: 'fit',
        gridSnap: true,
        theme: 'dark',
      }),
    });

    expect({ ...loadSettings(storage) }).toEqual({
      ...SETTING_DEFAULTS,
      showMinimap: false,
      openZoom: 'fit',
    });
    // The next change writes back only what this version knows.
    setSetting('reduceMotion', true, storage);
    expect(stored(storage)).toEqual({
      showMinimap: false,
      openZoom: 'fit',
      reduceMotion: true,
    });

    for (const unreadable of ['not json', '[1]', '"elk"', 'null']) {
      const other = memoryStorage({ [SETTINGS_STORAGE_KEY]: unreadable });
      expect({ ...loadSettings(other) }, unreadable).toEqual(SETTING_DEFAULTS);
    }
  });

  test('keep a change another tab made, and follow it', () => {
    const storage = memoryStorage();
    const listeners = new Set();
    const target = {
      addEventListener: (_, listener) => listeners.add(listener),
      removeEventListener: (_, listener) => listeners.delete(listener),
    };
    const stop = followSettings(target, storage);
    const otherTab = (key) =>
      listeners.forEach((listener) => listener({ key }));

    // The other tab stores a layout; this one then turns the minimap off
    // without losing it.
    storage.setItem(
      SETTINGS_STORAGE_KEY,
      JSON.stringify({ layoutAlgorithm: 'cards' }),
    );
    setSetting('showMinimap', false, storage);
    expect(stored(storage)).toEqual({
      layoutAlgorithm: 'cards',
      showMinimap: false,
    });

    otherTab('phenix.builder.theme');
    expect(builderSettings.layoutAlgorithm).toBe('elk');
    otherTab(SETTINGS_STORAGE_KEY);
    expect(builderSettings.layoutAlgorithm).toBe('cards');

    // Cleared site data (key null) takes the defaults again.
    storage.removeItem(SETTINGS_STORAGE_KEY);
    otherTab(null);
    expect({ ...builderSettings }).toEqual(SETTING_DEFAULTS);

    stop();
    expect(listeners.size).toBe(0);
  });

  test('write only the settings whose value changes when read again', () => {
    const storage = memoryStorage();
    const heard = [];
    const stop = watch(
      () => builderSettings.showMinimap,
      (value) => heard.push(value),
      { flush: 'sync' },
    );

    setSetting('showMinimap', false, storage);
    expect(heard).toEqual([false]);

    // Another tab changes the layout. The minimap, off in both, is not
    // written, so the view's toolbar toggle that follows it is left alone.
    storage.setItem(
      SETTINGS_STORAGE_KEY,
      JSON.stringify({ showMinimap: false, layoutAlgorithm: 'dagre' }),
    );
    loadSettings(storage);
    expect(builderSettings.layoutAlgorithm).toBe('dagre');
    expect(heard).toEqual([false]);

    stop();
  });
});
