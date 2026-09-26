import { beforeEach, describe, expect, test } from 'vitest';
import { watch } from 'vue';

import { LAYOUT_ALGORITHMS } from '@/builder/layouts/index.js';
import {
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

function fakeStorage(initial = {}) {
  const data = { ...initial };

  return {
    data,
    getItem: (key) => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = String(value);
    },
    removeItem: (key) => {
      delete data[key];
    },
  };
}

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
  return JSON.parse(storage.data[SETTINGS_STORAGE_KEY] ?? 'null');
}

beforeEach(() => {
  resetSettings(null);
});

describe('Builder settings', () => {
  test('default to ELK layered, the minimap, 100% and the system’s motion', () => {
    expect(SETTINGS_STORAGE_KEY).toBe('phenix.builder.settings');
    expect(SETTING_DEFAULTS).toEqual({
      layoutAlgorithm: 'elk',
      showMinimap: true,
      openZoom: 'actual',
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
    expect({ ...loadSettings(fakeStorage()) }).toEqual(SETTING_DEFAULTS);
    expect({ ...loadSettings(null) }).toEqual(SETTING_DEFAULTS);
    expect(settingsAtDefaults()).toBe(true);
  });

  test('keep only the settings changed from their defaults, and read them back', () => {
    const storage = fakeStorage();

    expect(setSetting('layoutAlgorithm', 'dagre', storage)).toBe(true);
    expect(setSetting('showMinimap', false, storage)).toBe(true);
    expect(setSetting('openZoom', 'fit', storage)).toBe(true);
    expect(builderSettings.layoutAlgorithm).toBe('dagre');
    expect(settingsAtDefaults()).toBe(false);
    expect(stored(storage)).toEqual({
      layoutAlgorithm: 'dagre',
      showMinimap: false,
      openZoom: 'fit',
    });

    // A new page reads them back.
    resetSettings(null);
    expect(loadSettings(storage)).toMatchObject({
      layoutAlgorithm: 'dagre',
      showMinimap: false,
      openZoom: 'fit',
      reduceMotion: false,
    });

    // A setting back at its default is not kept; none left removes the key.
    setSetting('layoutAlgorithm', 'elk', storage);
    setSetting('openZoom', 'actual', storage);
    expect(stored(storage)).toEqual({ showMinimap: false });
    setSetting('showMinimap', true, storage);
    expect(storage.data).not.toHaveProperty(SETTINGS_STORAGE_KEY);

    setSetting('reduceMotion', true, storage);
    resetSettings(storage);
    expect({ ...builderSettings }).toEqual(SETTING_DEFAULTS);
    expect(storage.data).not.toHaveProperty(SETTINGS_STORAGE_KEY);
  });

  test('store only the values they offer: no names, content or other keys', () => {
    const storage = fakeStorage();

    for (const [key, value] of [
      ['layoutAlgorithm', 'Secret lab'],
      ['layoutAlgorithm', undefined],
      ['showMinimap', 'false'],
      ['openZoom', 1.5],
      ['reduceMotion', null],
      ['draftName', 'Secret lab'],
      ['token', 'abc'],
      ['__proto__', {}],
    ]) {
      expect(setSetting(key, value, storage), key).toBe(false);
    }
    expect(storage.data).toEqual({});
    expect({ ...builderSettings }).toEqual(SETTING_DEFAULTS);
  });

  test('apply for the page when storage is blocked', () => {
    expect({ ...loadSettings(throwing) }).toEqual(SETTING_DEFAULTS);
    expect(setSetting('layoutAlgorithm', 'cards', throwing)).toBe(true);
    expect(builderSettings.layoutAlgorithm).toBe('cards');
    expect(() => resetSettings(throwing)).not.toThrow();
    expect(builderSettings.layoutAlgorithm).toBe('elk');

    // Full: the write fails, the choice still applies.
    const full = { ...fakeStorage(), setItem: throwing.setItem };
    expect(setSetting('reduceMotion', true, full)).toBe(true);
    expect(builderSettings.reduceMotion).toBe(true);
  });

  test('drop what another version stored that this one does not offer', () => {
    const storage = fakeStorage({
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
      const other = fakeStorage({ [SETTINGS_STORAGE_KEY]: unreadable });
      expect({ ...loadSettings(other) }, unreadable).toEqual(SETTING_DEFAULTS);
    }
  });

  test('keep a change another tab made, and follow it', () => {
    const storage = fakeStorage();
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
    const storage = fakeStorage();
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
